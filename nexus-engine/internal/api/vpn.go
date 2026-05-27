package api

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/nexus-oss/nexus/nexus-engine/internal/state"
)

// vpnHandler serves WireGuard config endpoints.
// It generates per-user keypairs, assigns VPN IPs, registers peers via the
// node agent, and returns a ready-to-import .conf file.
type vpnHandler struct {
	d Deps

	// serverPubKey is read once from "wg show wg0 public-key" and cached.
	serverPubKeyOnce sync.Once
	serverPubKey     string
	serverPubKeyErr  error
}

func newVPNHandler(d Deps) *vpnHandler { return &vpnHandler{d: d} }

// Config handles GET /api/v1/vpn/config
// Header: X-User-ID (required)
//
// Flow:
//  1. Check Redis for existing VPN config → return cached .conf if found.
//  2. Generate new keypair (wg genkey | wg pubkey).
//  3. Assign next available VPN IP in 10.8.0.2-254.
//  4. Register peer with node agent (EnsureWireGuardPeer).
//  5. Store config in Redis (no expiry — persists until regenerated).
//  6. Return .conf file download.
func (h *vpnHandler) Config(c *gin.Context) {
	userID := strings.TrimSpace(c.GetHeader("X-User-ID"))
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "X-User-ID header required"})
		return
	}

	// Check for existing config first.
	existing, err := h.d.Store.GetVPNConfig(userID)
	if err == nil && existing != nil {
		h.returnConfFile(c, existing)
		return
	}

	// Generate new keypair.
	privKey, pubKey, err := generateWireGuardKeypair()
	if err != nil {
		log.Printf("[VPN] keypair generation failed for user %s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate WireGuard keypair"})
		return
	}

	// Assign VPN IP.
	vpnIP, err := h.d.Store.GetNextAvailableVPNIP()
	if err != nil {
		log.Printf("[VPN] IP assignment failed for user %s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "VPN IP pool exhausted"})
		return
	}

	// Register peer with node agent (prod mode only).
	if h.d.NodeAgent != nil {
		ctx := context.Background()
		if err := h.d.NodeAgent.EnsureWireGuardPeer(ctx, userID, pubKey, vpnIP); err != nil {
			log.Printf("[VPN] EnsureWireGuardPeer failed for user %s: %v", userID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register VPN peer"})
			return
		}
	}

	// Persist config.
	cfg := &state.VPNConfig{
		UserID:     userID,
		PublicKey:  pubKey,
		PrivateKey: privKey,
		VPNip:      vpnIP,
	}
	if err := h.d.Store.SetVPNConfig(cfg); err != nil {
		log.Printf("[VPN] Redis store failed for user %s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store VPN config"})
		return
	}

	log.Printf("[VPN] provisioned peer for user %s ip=%s", userID, vpnIP)
	h.returnConfFile(c, cfg)
}

// Status handles GET /api/v1/vpn/status
// Header: X-User-ID (required)
func (h *vpnHandler) Status(c *gin.Context) {
	userID := strings.TrimSpace(c.GetHeader("X-User-ID"))
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "X-User-ID header required"})
		return
	}

	cfg, err := h.d.Store.GetVPNConfig(userID)
	if err != nil || cfg == nil {
		c.JSON(http.StatusOK, gin.H{"has_vpn": false})
		return
	}

	connected := false
	if h.d.NodeAgent != nil {
		resp, err := h.d.NodeAgent.GetWireGuardStatus(context.Background())
		if err == nil && resp != nil {
			for _, peer := range resp.Peers {
				if peer.PublicKey == cfg.PublicKey {
					connected = true
					break
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"has_vpn":    true,
		"vpn_ip":     cfg.VPNip,
		"public_key": cfg.PublicKey,
		"connected":  connected,
	})
}

// Regenerate handles POST /api/v1/vpn/regenerate
// Header: X-User-ID (required)
// Revokes the current peer and issues a new keypair + IP.
func (h *vpnHandler) Regenerate(c *gin.Context) {
	userID := strings.TrimSpace(c.GetHeader("X-User-ID"))
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "X-User-ID header required"})
		return
	}

	// Revoke existing peer if it exists.
	existing, err := h.d.Store.GetVPNConfig(userID)
	if err == nil && existing != nil && h.d.NodeAgent != nil {
		ctx := context.Background()
		if err := h.d.NodeAgent.RevokeWireGuardPeer(ctx, userID, existing.PublicKey); err != nil {
			log.Printf("[VPN] RevokeWireGuardPeer failed for user %s (non-fatal): %v", userID, err)
		}
	}

	// Delete old config from Redis before re-provisioning.
	_ = h.d.Store.DeleteVPNConfig(userID)

	// Re-use the Config handler to generate a fresh one.
	h.Config(c)
}

// returnConfFile writes the WireGuard .conf as a file download response.
func (h *vpnHandler) returnConfFile(c *gin.Context, cfg *state.VPNConfig) {
	serverPubKey, err := h.getServerPubKey()
	if err != nil {
		log.Printf("[VPN] cannot read server public key: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server WireGuard not configured"})
		return
	}

	endpoint := h.d.Cfg.WireGuard.Endpoint
	if endpoint == "" {
		log.Printf("[VPN] NEXUS_WG_ENDPOINT not set — cannot generate config")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "NEXUS_WG_ENDPOINT is not configured"})
		return
	}

	conf := fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = %s/32

[Peer]
PublicKey = %s
Endpoint = %s
AllowedIPs = 10.8.0.0/24, 10.42.0.0/16
PersistentKeepalive = 25
`, cfg.PrivateKey, cfg.VPNip, serverPubKey, endpoint)

	c.Header("Content-Disposition", "attachment; filename=nexus-vpn.conf")
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.String(http.StatusOK, conf)
}

// getServerPubKey reads the WireGuard server public key once and caches it.
func (h *vpnHandler) getServerPubKey() (string, error) {
	h.serverPubKeyOnce.Do(func() {
		out, err := exec.Command("wg", "show", "wg0", "public-key").Output()
		if err != nil {
			h.serverPubKeyErr = fmt.Errorf("wg show wg0 public-key: %w", err)
			return
		}
		h.serverPubKey = strings.TrimSpace(string(out))
	})
	return h.serverPubKey, h.serverPubKeyErr
}

// generateWireGuardKeypair runs wg genkey and derives the public key.
// Returns (privateKey, publicKey, error).
func generateWireGuardKeypair() (string, string, error) {
	privOut, err := exec.Command("wg", "genkey").Output()
	if err != nil {
		return "", "", fmt.Errorf("wg genkey: %w", err)
	}
	privKey := strings.TrimSpace(string(privOut))

	cmd := exec.Command("wg", "pubkey")
	cmd.Stdin = bytes.NewBufferString(privKey + "\n")
	pubOut, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("wg pubkey: %w", err)
	}
	pubKey := strings.TrimSpace(string(pubOut))

	return privKey, pubKey, nil
}
