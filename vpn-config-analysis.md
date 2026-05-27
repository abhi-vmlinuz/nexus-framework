# VPN Config Endpoint Gap Analysis

## Context

The stripped CTF platform (`zecurx-ctf`) is used purely to test Nexus. The real company CTF site will integrate with Nexus separately. So the fix lives in the **Nexus Engine** — the CTF platform just needs proxy pass-throughs.

## Problem

The stripped CTF frontend (`VPNAccess.tsx`) calls endpoints that don't exist anywhere:

| Frontend Call | Exists? |
|---|---|
| `GET /api/vpn/config` (header `X-User-ID`) | NO |
| `GET /api/vpn/status` (header `X-User-ID`) | NO |
| `POST /api/vpn/regenerate` (header `X-User-ID`) | NO |

The download button hits the backend, gets a 404 or connection refused, and shows "Failed to download VPN configuration."

## What Already Exists

The node-agent has three WireGuard gRPC RPCs fully implemented but **never called from the engine**:

- `EnsureWireGuardPeer(user_id, public_key, vpn_ip)` -- adds peer to wg0
- `RevokeWireGuardPeer(user_id, public_key)` -- removes peer
- `GetWireGuardStatus()` -- returns interface status + peer list

The engine's `nodeagent.Client` already wraps these:
- `client.go:129` -- `EnsureWireGuardPeer()`
- `client.go:147` -- `RevokeWireGuardPeer()`
- `client.go:158` -- `GetWireGuardStatus()`

The engine config (`config.go`) has no WireGuard endpoint/server IP setting.

## Root Cause

There is a missing layer between "user wants VPN config" and "node-agent manages WireGuard peers":

1. Nobody assigns VPN IPs -- the engine expects `vpn_ip` in session requests but never generates one
2. Nobody generates keypairs -- WireGuard needs a private/public key pair per peer
3. Nobody calls `EnsureWireGuardPeer` -- the session create flow calls `EnsureUserIsolation` (ipset/iptables) but skips the WireGuard peer step entirely
4. Nobody generates `.conf` files -- the frontend expects a downloadable WireGuard config

## Proposed Fix: Add VPN Endpoints to Nexus Engine

### New file: `nexus-engine/internal/api/vpn.go`

A `vpnHandler` with two methods:

#### `GET /api/v1/vpn/config` (header: `X-User-ID`)

Flow:
1. Check Redis for existing VPN config at key `vpn:<user_id>`
2. If not found:
   a. Generate keypair: run `wg genkey` -> private key, pipe to `wg pubkey` -> public key
   b. Assign IP: scan Redis keys `vpn:*`, find next unused IP in `10.8.0.2-254`
   c. Call `NodeAgent.EnsureWireGuardPeer(user_id, public_key, vpn_ip)`
   d. Store in Redis: `{user_id, public_key, private_key, vpn_ip, created_at}`
3. Read server public key: `wg show wg0 public-key` (cache in handler after first call)
4. Get server endpoint: from new env var `NEXUS_WG_ENDPOINT` (e.g., `13.233.126.78:51820`)
5. Return `.conf` file as download:

```ini
[Interface]
PrivateKey = <peer_private_key>
Address = <vpn_ip>/32

[Peer]
PublicKey = <server_public_key>
Endpoint = <server_endpoint>
AllowedIPs = 10.8.0.0/24
PersistentKeepalive = 25
```

> **Split-tunnel design:** `AllowedIPs = 10.8.0.0/24` only routes pod subnet traffic through the VPN. Internet traffic uses the normal default route. No `DNS` override is set — adding one would break DNS in split-tunnel mode.

#### `GET /api/v1/vpn/status` (header: `X-User-ID`)

Flow:
1. Check Redis for `vpn:<user_id>`
2. If not found: return `{has_vpn: false}`
3. If found: call `NodeAgent.GetWireGuardStatus()`, find peer by public key
4. Return `{has_vpn: true, vpn_ip: "...", connected: bool}`

### Modified file: `nexus-engine/internal/api/routes.go`

Add under the `v1` group:
```go
vh := newVPNHandler(d)
v1.GET("/vpn/config", vh.Config)
v1.GET("/vpn/status", vh.Status)
v1.POST("/vpn/regenerate", vh.Regenerate)
```

### Modified file: `nexus-engine/internal/state/redis.go`

Add:
```go
type VPNConfig struct {
    UserID     string `json:"user_id"`
    PublicKey  string `json:"public_key"`
    PrivateKey string `json:"private_key"`
    VPNIp      string `json:"vpn_ip"`
    CreatedAt  int64  `json:"created_at"`
}

func (s *Store) GetVPNConfig(userID string) (*VPNConfig, error)  // key: vpn:<user_id>
func (s *Store) SetVPNConfig(cfg *VPNConfig) error
func (s *Store) DeleteVPNConfig(userID string) error
func (s *Store) GetNextAvailableVPNIP() (string, error)  // scan vpn:*, find next in 10.8.0.2-254
```

### Modified file: `nexus-engine/internal/config/config.go`

Add to `Config`:
```go
WireGuard struct {
    Endpoint     string // e.g., "13.233.126.78:51820"
    ServerPubKey string // cached, read from wg show
}
```

Load from `NEXUS_WG_ENDPOINT` env var.

### CTF Platform Proxy

The frontend calls `/api/vpn/*` but the CTF backend has no proxy for it. Two options:

**Option A (recommended):** Add a pass-through in the CTF backend:
```python
@api_router.get("/vpn/config")
async def vpn_config(current_user: dict = Depends(get_current_user)):
    async with httpx.AsyncClient() as client:
        resp = await client.get(
            f"{NEXUS_ENGINE_URL}/api/v1/vpn/config",
            headers={"X-User-ID": current_user["id"]},
        )
        return Response(content=resp.content, media_type="text/plain",
                       headers={"Content-Disposition": "attachment; filename=wg.conf"})
```

**Option B:** Add an nginx proxy rule for `/api/vpn/` -> Nexus Engine.

## Dependency Chain

```
Frontend "Download VPN" button
  -> GET /api/vpn/config (CTF backend proxy)
    -> GET /api/v1/vpn/config (Nexus Engine)
      -> Redis: check/store keypair
      -> wg genkey | wg pubkey (generate keypair)
      -> NodeAgent.EnsureWireGuardPeer() (gRPC to node-agent)
        -> node-agent writes peer to /etc/wireguard/wg0.conf (persistence)
        -> wg set wg0 peer <pubkey> allowed-ips <ip>/32 (live interface, AppArmor-safe)
      -> Return .conf file
```

> **Note on wg syncconf:** The original design used `wg-quick strip | wg syncconf` via a tmp file. This was replaced with direct `wg set` calls because Ubuntu's AppArmor profile blocks the `wg` binary from reading files in `/tmp`. See [DEBUGGING.md](../DEBUGGING.md#9-wireguard-peer-registration-fails-on-ubuntu-apparmor-blocks-wg-syncconf).

> **AWS Requirement:** Port `51820/UDP` must be open inbound in the EC2 Security Group. Without it, WireGuard handshakes never complete and clients cannot reach `10.8.0.1`. This is permanent.

## Session Flow After VPN Fix

When the CTF platform starts a session, it should:
1. Check if user has a VPN config (call `/api/vpn/status`)
2. If yes, use the assigned `vpn_ip` in the session creation request
3. Engine calls `EnsureUserIsolation(user_id, vpn_ip)` -- ipset/iptables
4. Engine calls `GrantPodAccess(user_id, pod_ip)` -- allow VPN user to reach pod

The current CTF backend code (`server.py:6047`) generates `vpn_ip` as `f"10.8.0.{hash(user_id) % 254 + 1}"` which is a hack. After the fix, it should use the actual assigned IP from the VPN config.

## What Needs to Change in the CTF Backend (`server.py`)

1. Add proxy endpoints for `/api/vpn/config`, `/api/vpn/status`, `/api/vpn/regenerate`
2. Fix session creation to use the real `vpn_ip` from VPN config instead of the hash hack at line 6047
3. Before starting a session, ensure the user has a VPN peer registered

## Files Summary

| File | Change |
|---|---|
| `nexus-engine/internal/api/vpn.go` | NEW -- VPN handler (Config, Status, Regenerate) |
| `nexus-engine/internal/api/routes.go` | Add 3 VPN routes under `/api/v1/vpn/` |
| `nexus-engine/internal/state/redis.go` | Add VPNConfig struct + Redis methods |
| `nexus-engine/internal/config/config.go` | Add WireGuard endpoint config |
| `zecurx-ctf/backend/server.py` | Add VPN proxy endpoints, fix vpn_ip assignment |
