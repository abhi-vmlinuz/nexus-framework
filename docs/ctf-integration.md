# Nexus OSS — CTF Platform Integration Guide

This guide is for developers building CTF (Capture The Flag) platforms who want to use Nexus OSS as their challenge infrastructure backend.

## Overview

Nexus OSS handles the **infrastructure layer**: deploying isolated challenge containers, managing VPN access, and cleaning up sessions. Your CTF platform handles **user management, scoring, authentication, and UI**.

```
┌─────────────────────────────────────────────────────────────┐
│                    Your CTF Platform                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐   │
│  │   Auth   │  │ Scoring  │  │   UI     │  │  Admin   │   │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘   │
└───────────────────────────┬─────────────────────────────────┘
                            │ HTTP REST API
┌───────────────────────────▼─────────────────────────────────┐
│                     Nexus Engine                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐   │
│  │ Challenges│  │ Sessions │  │ VPN      │  │ Registry │   │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘   │
└───────────────────────────┬─────────────────────────────────┘
                            │
┌───────────────────────────▼─────────────────────────────────┐
│                   K3s Pods (Challenges)                      │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐   │
│  │  Web App │  │ Database │  │  Cache   │  │  Other   │   │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘   │
└─────────────────────────────────────────────────────────────┘
```

## Quick Start

### 1. Engine URL

Your backend connects to Nexus Engine at: `http://<engine-host>:8081`

### 2. Register a Challenge

**Single Container (pre-built image):**
```bash
curl -X POST http://localhost:8081/api/v1/challenges \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-challenge",
    "containers": [
      {
        "name": "main",
        "image": "ghcr.io/your-org/my-challenge:latest",
        "ports": [80, 443]
      }
    ],
    "ttl_minutes": 60
  }'
```

**Multi-Container (pre-built images):**
```bash
curl -X POST http://localhost:8081/api/v1/challenges \
  -H "Content-Type: application/json" \
  -d '{
    "name": "web-app-challenge",
    "containers": [
      {
        "name": "web",
        "image": "ghcr.io/your-org/web-app:latest",
        "ports": [8080],
        "env": {"DB_HOST": "localhost", "DB_PORT": "5432"}
      },
      {
        "name": "db",
        "image": "ghcr.io/your-org/postgres:16",
        "ports": [5432],
        "env": {"POSTGRES_USER": "ctf", "POSTGRES_PASSWORD": "***"  "POSTGRES_DB": "ctf"}
      }
    ],
    "ttl_minutes": 90
  }'
```

**Build from Source (engine builds):**
```bash
# First, deploy challenge files to engine host
scp -r ./my-challenge/ user@engine-host:/opt/nexus/challenges/my-challenge/

# Then register
curl -X POST http://localhost:8081/api/v1/challenges \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-challenge",
    "dockerfile_path": "/opt/nexus/challenges/my-challenge/Dockerfile",
    "ttl_minutes": 60
  }'
```

### 3. Start a Session

When a student starts a challenge:

```bash
curl -X POST http://localhost:8081/api/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{
    "challenge_id": "my-challenge-abc123",
    "user_id": "student-uuid-here",
    "vpn_ip": "10.8.0.5"
  }'
```

**Response:**
```json
{
  "session_id": "sess-xyz789",
  "user_id": "student-uuid-here",
  "challenge_id": "my-challenge-abc123",
  "pod_ip": "10.42.0.5",
  "pod_name": "chal-sess-xyz789",
  "status": "running",
  "created_at": "2026-05-29T12:00:00Z",
  "expires_at": "2026-05-29T13:00:00Z",
  "ports": [80, 443],
  "services": [
    {"name": "main", "port": 80},
    {"name": "main", "port": 443}
  ]
}
```

### 4. Display Connection Info to Student

The student needs:
- **Pod IP**: `10.42.0.5` (from response)
- **Ports**: `[80, 443]` (from response)
- **VPN Connection**: They must connect via WireGuard VPN first

**Connection flow for student:**
1. Download VPN config from your platform
2. Import into WireGuard client
3. Connect to VPN
4. Access challenge at `10.42.0.5:80` (or whatever port)

### 5. Check Session Status

```bash
curl http://localhost:8081/api/v1/sessions/sess-xyz789
```

### 6. Terminate Session

When student finishes or timeout occurs:

```bash
curl -X DELETE http://localhost:8081/api/v1/sessions/sess-xyz789
```

## Environment Variables for Containers

**Critical**: When using `containers[]`, you MUST include the `env` field for containers that need configuration.

**Without env (WILL FAIL):**
```json
{
  "name": "db",
  "image": "postgres:16-alpine",
  "ports": [5432]
  // ❌ No POSTGRES_PASSWORD → PostgreSQL won't start
}
```

**With env (CORRECT):**
```json
{
  "name": "db",
  "image": "postgres:16-alpine",
  "ports": [5432],
  "env": {
    "POSTGRES_USER": "ctf",
    "POSTGRES_PASSWORD": "ctf",
    "POSTGRES_DB": "ctf"
  }
  // ✅ PostgreSQL starts correctly
}
```

## Challenge Packs (Database Schema)

For managing multiple challenges, create a `challenge_packs` table:

```sql
CREATE TABLE challenge_packs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pack_name VARCHAR(100) UNIQUE NOT NULL,
    display_name VARCHAR(200),
    images JSONB NOT NULL,
    combined_ports JSONB,
    compose_content TEXT,
    is_multi_container BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);
```

**Example entry:**
```json
{
  "pack_name": "web-app-101",
  "display_name": "Web Application Challenge",
  "images": [
    {
      "name": "web",
      "image": "ghcr.io/your-org/web-app:latest",
      "ports": [8080],
      "env": {"DB_HOST": "localhost"}
    },
    {
      "name": "db",
      "image": "ghcr.io/your-org/postgres:16",
      "ports": [5432],
      "env": {"POSTGRES_PASSWORD": "***"    }
  ],
  "combined_ports": [8080, 5432],
  "is_multi_container": true
}
```

## VPN Integration

### Student VPN Flow

1. **Student registers** on your platform
2. **Your platform calls** `GET /api/v1/vpn/config` with `X-User-ID` header
3. **Nexus returns** a WireGuard `.conf` file
4. **Your platform serves** the `.conf` file to the student
5. **Student imports** into WireGuard client and connects

### VPN Config Endpoint

```bash
curl -H "X-User-ID: student-uuid" http://localhost:8081/api/v1/vpn/config
```

Returns a WireGuard `.conf` file like:
```ini
[Interface]
PrivateKey = <student-private-key>
Address = 10.8.0.5/32

[Peer]
PublicKey = <server-public-key>
Endpoint = 13.233.126.78:51820
AllowedIPs = 10.8.0.0/24, 10.42.0.0/16
PersistentKeepalive = 25
```

**Important**: This is a **split-tunnel** config. Only traffic to `10.8.0.0/24` (VPN) and `10.42.0.0/16` (pods) goes through VPN. Student's internet works normally.

## Auto-Destroy on Completion

When a student solves all flags, terminate the session:

```bash
curl -X DELETE http://localhost:8081/api/v1/sessions/sess-xyz789
```

Or let TTL handle it (default: 60 minutes).

## Error Handling

| Error | Meaning | Action |
|-------|---------|--------|
| `CHALLENGE_NOT_FOUND` | Challenge ID doesn't exist | Check challenge registration |
| `SESSION_LIMIT_REACHED` | User has max concurrent sessions | Terminate old sessions first |
| `VPN_IP_REQUIRED` | Missing `vpn_ip` in prod mode | Get VPN config first |
| `POD_SPAWN_FAILED` | K3s couldn't create pod | Check resources, registry access |
| `BUILD_FAILED` | `nerdctl build` failed | Check Dockerfile, build context |

## Best Practices

1. **Use pre-built images** (`containers[]`) for production — faster, more reliable
2. **Include env vars** for all containers that need configuration
3. **Set reasonable TTLs** — 60-120 minutes is typical for CTF challenges
4. **Clean up sessions** — implement auto-destroy on flag submission
5. **Handle errors gracefully** — show user-friendly messages, not raw API errors
6. **Cache challenge definitions** — don't re-register on every session start

## Related Documentation

- [API Reference](api.md) — Full endpoint documentation
- [Architecture](architecture.md) — System design and separation of concerns
- [Quickstart](quickstart.md) — Installation and setup
- [README](../README.md) — Overview, cloud deployment, registry configuration

## Example: Full Integration Flow

```python
# Python example using httpx
import httpx

NEXUS_URL = "http://localhost:8081"

async def start_challenge(user_id: str, challenge_id: str, vpn_ip: str):
    """Start a challenge session for a student."""
    
    # 1. Create session
    resp = await httpx.AsyncClient().post(
        f"{NEXUS_URL}/api/v1/sessions",
        json={
            "challenge_id": challenge_id,
            "user_id": user_id,
            "vpn_ip": vpn_ip
        },
        timeout=120.0
    )
    
    if resp.status_code != 201:
        raise Exception(f"Failed to start challenge: {resp.text}")
    
    session = resp.json()
    
    # 2. Return connection info to student
    return {
        "session_id": session["session_id"],
        "pod_ip": session["pod_ip"],
        "ports": session["ports"],
        "expires_at": session["expires_at"],
        "message": f"Connect via VPN to {session['pod_ip']}:{session['ports'][0]}"
    }

async def stop_challenge(session_id: str):
    """Terminate a challenge session."""
    resp = await httpx.AsyncClient().delete(
        f"{NEXUS_URL}/api/v1/sessions/{session_id}",
        timeout=30.0
    )
    return resp.status_code in [200, 404]
```
