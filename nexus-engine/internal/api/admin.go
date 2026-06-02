package api

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ─── Admin handler ────────────────────────────────────────────────────────────

type adminHandler struct{ d Deps }

func newAdminHandler(d Deps) *adminHandler { return &adminHandler{d: d} }

func (h *adminHandler) Sessions(c *gin.Context) {
	sessions, err := h.d.Store.ListAllSessions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions, "count": len(sessions)})
}

func (h *adminHandler) Nodes(c *gin.Context) {
	if h.d.K8s == nil {
		c.JSON(http.StatusOK, gin.H{"pods": []any{}, "count": 0, "note": "k8s not connected"})
		return
	}
	pods, err := h.d.K8s.ListPods()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"pods": pods, "count": len(pods)})
}

func (h *adminHandler) ClusterHealth(c *gin.Context) {
	if err := h.d.Store.Ping(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "degraded",
			"redis":  "unreachable",
			"error":  err.Error(),
		})
		return
	}
	agentStatus := "not_configured"
	if h.d.NodeAgent != nil {
		resp, err := h.d.NodeAgent.Health(c.Request.Context())
		if err != nil {
			agentStatus = "unreachable"
		} else if resp.Healthy {
			agentStatus = "healthy"
		} else {
			agentStatus = "degraded"
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"status":     "healthy",
		"redis":      "ok",
		"node_agent": agentStatus,
		"mode":       h.d.Cfg.Mode,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *adminHandler) Config(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"mode": h.d.Cfg.Mode,
		"registry": gin.H{
			"url":       h.d.Cfg.Registry.URL,
			"auth_type": h.d.Cfg.Registry.AuthType,
			"username":  h.d.Cfg.Registry.Username,
		},
		"node_agent": gin.H{
			"addr":     h.d.Cfg.NodeAgent.Addr,
			"insecure": h.d.Cfg.NodeAgent.Insecure,
		},
		"k3s_namespace": h.d.Cfg.K3sNamespace,
		"reconciler": gin.H{
			"reconcile_interval": h.d.Cfg.Reconciler.Interval.String(),
			"max_workers":        h.d.Cfg.Reconciler.MaxWorkers,
		},
		"session": gin.H{
			"default_ttl_minutes":   h.d.Cfg.Session.DefaultTTLMinutes,
			"max_sessions_per_user": h.d.Cfg.Session.MaxPerUser,
		},
		"challenge": gin.H{
			"default_cpu_limit":    h.d.Cfg.Challenge.DefaultCPULimit,
			"default_memory_limit": h.d.Cfg.Challenge.DefaultMemoryLimit,
		},
	})
}

type UpdateConfigRequest struct {
	DefaultCPULimit    *string `json:"default_cpu_limit,omitempty"`
	DefaultMemoryLimit *string `json:"default_memory_limit,omitempty"`
	DefaultTTLMinutes  *int    `json:"default_ttl_minutes,omitempty"`
	MaxSessionsPerUser *int    `json:"max_sessions_per_user,omitempty"`
	MaxWorkers         *int    `json:"max_workers,omitempty"`
}

func (h *adminHandler) UpdateConfig(c *gin.Context) {
	var req UpdateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.DefaultCPULimit != nil {
		h.d.Cfg.SetDefaultCPULimit(*req.DefaultCPULimit)
	}
	if req.DefaultMemoryLimit != nil {
		h.d.Cfg.SetDefaultMemoryLimit(*req.DefaultMemoryLimit)
	}
	if req.DefaultTTLMinutes != nil {
		h.d.Cfg.SetDefaultTTLMinutes(*req.DefaultTTLMinutes)
	}
	if req.MaxSessionsPerUser != nil {
		h.d.Cfg.SetMaxPerUser(*req.MaxSessionsPerUser)
	}
	if req.MaxWorkers != nil {
		h.d.Cfg.SetMaxWorkers(*req.MaxWorkers)
	}

	// Persist to disk
	if err := h.d.Cfg.SaveToFile("/etc/nexus/engine.env"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist config: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Config updated successfully.",
		"config": gin.H{
			"default_cpu_limit":    h.d.Cfg.GetDefaultCPULimit(),
			"default_memory_limit": h.d.Cfg.GetDefaultMemoryLimit(),
			"default_ttl_minutes":  h.d.Cfg.GetDefaultTTLMinutes(),
			"max_per_user":         h.d.Cfg.GetMaxPerUser(),
			"max_workers":          h.d.Cfg.GetMaxWorkers(),
		},
	})
}

// validURLRe matches http:// or https:// URLs.
var validURLRe = regexp.MustCompile(`^https?://[a-zA-Z0-9][a-zA-Z0-9._:/@-]*$`)

// validUsernameRe allows alphanumeric, underscores, hyphens, dots, and @.
var validUsernameRe = regexp.MustCompile(`^[a-zA-Z0-9._@-]+$`)

func (h *adminHandler) UpdateRegistry(c *gin.Context) {
	var req struct {
		URL      string `json:"url" binding:"required"`
		AuthType string `json:"auth_type" binding:"required"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Input validation to prevent injection.
	if !validURLRe.MatchString(req.URL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid registry URL: must start with http:// or https://"})
		return
	}
	if req.Username != "" && !validUsernameRe.MatchString(req.Username) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid username: only alphanumeric, ., _, @, - allowed"})
		return
	}

	// 1. Update in-memory config (thread-safe via mutex).
	h.d.Cfg.SetRegistryURL(req.URL)
	h.d.Cfg.SetRegistryAuth(req.AuthType, req.Username, req.Password)

	// 2. Persist to file
	configDir := "/etc/nexus"
	configPath := configDir + "/engine.env"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		log.Printf("failed to create config dir: %v", err)
	} else {
		if err := h.d.Cfg.SaveToFile(configPath); err != nil {
			log.Printf("failed to save config to file: %v", err)
		}
	}

	// 3. Perform nerdctl login if needed (no shell — safe from injection)
	if req.AuthType != "none" && req.Username != "" && req.Password != "" {
		cmd := exec.Command("nerdctl", "login", req.URL, "-u", req.Username, "--password-stdin")
		cmd.Stdin = strings.NewReader(req.Password)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "REGISTRY_LOGIN_FAILED",
				"message": err.Error(),
				"output":  out.String(),
			})
			return
		}

		// 3. Ensure K8s secret exists
		if h.d.K8s != nil {
			secretName := "nexus-pull-secret"
			if err := h.d.K8s.EnsureImagePullSecret(secretName, req.URL, req.Username, req.Password); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error":   "K8S_SECRET_CREATE_FAILED",
					"message": err.Error(),
				})
				return
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "registry configuration updated",
		"url":     req.URL,
		"status":  "active",
	})
}

func (h *adminHandler) TriggerReconcile(c *gin.Context) {
	sessions, err := h.d.Store.ListActiveSessions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, sess := range sessions {
		h.d.Controller.Touch(sess.ID, "manual_reconcile")
	}
	c.JSON(http.StatusOK, gin.H{
		"message":       "reconcile triggered",
		"sessions":      len(sessions),
		"timestamp":     time.Now().UTC().Format(time.RFC3339),
	})
}

// ─── Debug handler ────────────────────────────────────────────────────────────

type debugHandler struct{ d Deps }

func newDebugHandler(d Deps) *debugHandler { return &debugHandler{d: d} }

func (h *debugHandler) System(c *gin.Context) {
	sessions, _ := h.d.Store.ListAllSessions()
	podsCount := 0
	if h.d.K8s != nil {
		if pods, err := h.d.K8s.ListPods(); err == nil {
			podsCount = len(pods)
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"sessions_total": len(sessions),
		"pods_total":     podsCount,
		"mode":           h.d.Cfg.Mode,
		"registry":       h.d.Cfg.Registry.URL,
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *debugHandler) Controller(c *gin.Context) {
	stats := h.d.Controller.Stats()
	c.JSON(http.StatusOK, stats)
}

// ─── Metrics handler ──────────────────────────────────────────────────────────

func metricsHandler() gin.HandlerFunc {
	h := promhttp.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// sanitizeName converts a display name to a safe identifier component.
func sanitizeName(name string) string {
	r := strings.ToLower(name)
	r = strings.ReplaceAll(r, " ", "-")
	r = strings.ReplaceAll(r, "_", "-")
	var b strings.Builder
	for _, c := range r {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteRune(c)
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "challenge"
	}
	return result
}
