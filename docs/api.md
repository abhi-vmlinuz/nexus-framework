# Nexus API Reference

This document provides a comprehensive guide to the HTTP APIs provided by **Nexus Engine**.

---

## 🛰️ Base URL
All API endpoints are prefixed with:
`http://<engine-host>:8081`

---

## 🧩 Session API (Public)
Used by frontends or participants to manage challenge instances.

### 🟢 Create Session
`POST /api/v1/sessions`

Spawns a new challenge instance (Pod) for a specific user.

**Request Body:**
```json
{
  "challenge_id": "test-challenge-abcd1234",
  "user_id": "player-1",
  "vpn_ip": "10.13.37.1"
}
```
*   `challenge_id` (Required): The registered ID of the challenge.
*   `user_id` (Required): Unique identifier for the user. Used for network isolation.
*   `vpn_ip` (Optional): The user's VPN IP. **Required in production mode** for firewall isolation.

**Response (201 Created):**
```json
{
  "session_id": "sess-a1b2c3d4",
  "user_id": "player-1",
  "challenge_id": "test-challenge-abcd1234",
  "pod_ip": "10.42.0.5",
  "status": "running",
  "created_at": "2026-05-01T17:30:00Z",
  "expires_at": "2026-05-01T18:30:00Z",
  "ports": [80, 8080],
  "services": [
    {"name": "web", "port": 80},
    {"name": "api", "port": 8080}
  ]
}
```

### 🟡 Get Session Status
`GET /api/v1/sessions/:id`

**Response (200 OK):**
Returns the same structure as the Create response.

### 🔴 Terminate Session
`DELETE /api/v1/sessions/:id`

Terminates the instance and revokes network access.

---

## 🛠️ Challenge API (Admin)
Used to register and manage challenge definitions.

### 🟢 Register Challenge
`POST /api/v1/challenges`

Registers a challenge. Supports three modes:

**Option 1: Single Container (Build from Source)**
```json
{
  "name": "pwn-challenge",
  "dockerfile_path": "/absolute/path/to/Dockerfile",
  "ttl_minutes": 60,
  "resources": {
    "cpu": "0.5",
    "memory": "256Mi"
  },
  "readiness_probe": {
    "http_get": { "path": "/", "port": 80 },
    "initial_delay_seconds": 5
  }
}
```
Engine builds image via `nerdctl build` and pushes to configured registry.

**Option 2: Multi-Container (Build from Compose)**
```json
{
  "name": "complex-web",
  "compose_path": "/absolute/path/to/docker-compose.yml",
  "ttl_minutes": 90
}
```
Engine parses compose file, builds/pulls each service, extracts environment variables.

**Option 3: Pre-Built Images (No Build)**
```json
{
  "name": "web-challenge",
  "containers": [
    {
      "name": "web",
      "image": "ghcr.io/your-org/my-web:latest",
      "ports": [8080],
      "env": {"DB_HOST": "localhost", "DB_PORT": "5432"}
    },
    {
      "name": "db",
      "image": "ghcr.io/your-org/my-db:latest",
      "ports": [5432],
      "env": {"POSTGRES_USER": "ctf", "POSTGRES_PASSWORD": "ctf", "POSTGRES_DB": "ctf"}
    }
  ],
  "ttl_minutes": 60
}
```
Engine stores container specs directly. No build step. Use `env` field for container configuration.

> [!IMPORTANT]
> **Environment Variables**: When using `containers[]`, you must include the `env` field for each container that needs configuration. Services like databases will fail without their required environment variables.

**Response (201 Created):**
```json
{
  "id": "pwn-challenge-1234abcd",
  "name": "pwn-challenge",
  "image": "localhost:5000/pwn-challenge:latest",
  "ports": [80],
  "ttl_minutes": 60
}
```

### 🔵 List Challenges
`GET /api/v1/challenges`

---

## 🛡️ Admin & Monitoring API
Restricted endpoints for cluster visibility and maintenance.

### 🩺 Cluster Health
`GET /api/v1/admin/cluster/health`

Returns health status of Engine, Redis, and Node Agent.

### ⚙️ Get Configuration
`GET /api/v1/admin/config`

Returns current runtime configuration including default resource limits.

### 🔄 Update Registry Configuration
`PUT /api/v1/admin/registry`

Switch the container registry at runtime without restarting the engine.

**Request Body:**
```json
{
  "url": "ghcr.io/your-org",
  "auth_type": "ghcr",
  "username": "your-username",
  "password": "***"
}
```

**Supported `auth_type` values:**
- `local` — Local registry (no authentication)
- `basic` — Username/password authentication (Docker Hub, custom registries)
- `ghcr` — GitHub Container Registry (uses GitHub Personal Access Token)
- `awsecr` — AWS Elastic Container Registry (uses AWS credentials)

**Response (200 OK):**
```json
{
  "message": "registry configuration updated",
  "url": "ghcr.io/your-org",
  "status": "active"
}
```

**What happens:**
1. Engine updates in-memory configuration
2. Persists to `/etc/nexus/engine.env`
3. Runs `nerdctl login` to authenticate
4. Creates/updates K8s image pull secret (`nexus-pull-secret`)

### 📊 Cluster Visibility
*   `GET /api/v1/admin/nodes`: List K8s nodes and their status.
*   `GET /api/v1/admin/cluster/pods`: Raw list of all challenge pods.
*   `GET /api/v1/admin/registry/images`: List images stored in the local registry.

---

## 💓 Meta & Diagnostics
*   `/health`: Liveness check for the Engine process.
*   `/metrics`: Prometheus-compatible metrics endpoint.
*   `/debug/system`: High-level system statistics (Total sessions, total pods).
*   `/debug/controller`: Internal reconciler loop statistics.
