# Nexus API Reference

This document provides a comprehensive guide to the HTTP APIs provided by **Nexus Engine**.

---

## Base URL

All API endpoints are prefixed with:
`http://<engine-host>:8081`

---

## Session API (Public)

Used by frontends or participants to manage challenge instances.

### Create Session

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

- `challenge_id` (Required): The registered ID of the challenge.
- `user_id` (Required): Unique identifier for the user. Used for network isolation.
- `vpn_ip` (Optional): The user's VPN IP. **Required in production mode** for firewall isolation.

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

### Get Session Status

`GET /api/v1/sessions/:id`

**Response (200 OK):**
Returns the same structure as the Create response.

### Terminate Session

`DELETE /api/v1/sessions/:id`

Terminates the instance and revokes network access.

---

## Challenge API (Admin)

Used to register and manage challenge definitions.

### Register Challenge

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

> **Environment Variables**: When using `containers[]`, you must include the `env` field for each container that needs configuration. Services like databases will fail without their required environment variables.

---

#### Response (201 Created) — Single Container Build

```json
{
  "challenge": {
    "id": "pwn-challenge-1234abcd",
    "name": "pwn-challenge",
    "image": "localhost:5000/pwn-challenge:latest",
    "dockerfile_path": "/absolute/path/to/Dockerfile",
    "tag": "latest",
    "ttl_minutes": 60,
    "ports": [80],
    "created_at": "2026-06-01T03:40:29Z",
    "updated_at": "2026-06-01T03:40:29Z"
  },
  "build": {
    "status": "success",
    "started_at": "2026-06-01T03:40:20Z",
    "completed_at": "2026-06-01T03:40:29Z",
    "duration_ms": 9200,
    "registry": "localhost:5000",
    "registry_auth": {
      "method": "none",
      "authenticated": false
    },
    "tooling": {
      "nerdctl": "1.7.6",
      "buildkit": "v0.12.5"
    },
    "ready_for_deployment": true,
    "containers": [
      {
        "name": "pwn-challenge",
        "image": "localhost:5000/pwn-challenge:latest",
        "status": "built",
        "duration_ms": 9200,
        "ports": [80]
      }
    ]
  }
}
```

#### Response (201 Created) — Multi-Container Compose Build

```json
{
  "challenge": {
    "id": "complex-web-a1b2c3d4",
    "name": "complex-web",
    "containers": [
      {"name": "web", "image": "localhost:5000/complex-web-web:latest", "ports": [8080]},
      {"name": "db", "image": "localhost:5000/complex-web-db:latest", "ports": [5432]}
    ],
    "ttl_minutes": 90,
    "ports": [8080, 5432],
    "created_at": "2026-06-01T03:40:29Z",
    "updated_at": "2026-06-01T03:40:29Z"
  },
  "build": {
    "status": "success",
    "started_at": "2026-06-01T03:40:10Z",
    "completed_at": "2026-06-01T03:40:29Z",
    "duration_ms": 19000,
    "registry": "localhost:5000",
    "registry_auth": {
      "method": "none",
      "authenticated": false
    },
    "tooling": {
      "nerdctl": "1.7.6",
      "buildkit": "v0.12.5"
    },
    "ready_for_deployment": true,
    "containers": [
      {
        "name": "web",
        "image": "localhost:5000/complex-web-web:latest",
        "status": "built",
        "duration_ms": 10200,
        "ports": [8080]
      },
      {
        "name": "db",
        "image": "localhost:5000/complex-web-db:latest",
        "status": "built",
        "duration_ms": 8800,
        "ports": [5432]
      }
    ]
  }
}
```

#### Response (201 Created) — Pre-Built Images (No Build)

```json
{
  "challenge": {
    "id": "web-challenge-e5f6g7h8",
    "name": "web-challenge",
    "containers": [
      {"name": "web", "image": "ghcr.io/your-org/my-web:latest", "ports": [8080]},
      {"name": "db", "image": "ghcr.io/your-org/my-db:latest", "ports": [5432]}
    ],
    "ttl_minutes": 60,
    "ports": [8080, 5432],
    "created_at": "2026-06-01T03:40:29Z",
    "updated_at": "2026-06-01T03:40:29Z"
  },
  "build": {
    "status": "skipped",
    "registry": "localhost:5000",
    "registry_auth": {
      "method": "none",
      "authenticated": false
    },
    "ready_for_deployment": true,
    "containers": [
      {"name": "web", "image": "ghcr.io/your-org/my-web:latest", "status": "pre-built", "ports": [8080]},
      {"name": "db", "image": "ghcr.io/your-org/my-db:latest", "status": "pre-built", "ports": [5432]}
    ]
  }
}
```

#### Response (422) — Build Failure (Single Container)

```json
{
  "error": "BUILD_FAILED",
  "message": "nerdctl build failed: exit status 1\noutput: ...",
  "build": {
    "status": "failure",
    "registry": "localhost:5000",
    "registry_auth": {
      "method": "none",
      "authenticated": false
    },
    "ready_for_deployment": false,
    "containers": []
  },
  "retry_info": {
    "can_retry": true,
    "retry_url": "/api/v1/challenges",
    "failed_containers": ["pwn-challenge"]
  }
}
```

#### Response (422) — Partial Build Failure (Multi-Container)

When some containers build successfully but others fail, the entire request is rejected. No challenge is created. Successfully built images remain cached in the registry — retry will be faster.

```json
{
  "error": "PARTIAL_BUILD_FAILURE",
  "message": "1 of 3 services failed to build: service \"db\": nerdctl build failed: ...",
  "build": {
    "status": "partial_failure",
    "started_at": "2026-06-01T03:40:10Z",
    "completed_at": "2026-06-01T03:40:22Z",
    "duration_ms": 12000,
    "registry": "localhost:5000",
    "registry_auth": {
      "method": "none",
      "authenticated": false
    },
    "tooling": {
      "nerdctl": "1.7.6",
      "buildkit": "v0.12.5"
    },
    "ready_for_deployment": false,
    "containers": [
      {
        "name": "web",
        "image": "localhost:5000/my-challenge-web:latest",
        "status": "built",
        "duration_ms": 4200,
        "ports": [8080]
      },
      {
        "name": "api",
        "image": "localhost:5000/my-challenge-api:latest",
        "status": "built",
        "duration_ms": 3800,
        "ports": [3000]
      },
      {
        "name": "db",
        "image": "",
        "status": "failed",
        "error": "nerdctl build failed: ..."
      }
    ]
  },
  "retry_info": {
    "can_retry": true,
    "retry_url": "/api/v1/challenges",
    "failed_containers": ["db"],
    "note": "Successfully built containers are cached. Retry will be faster."
  }
}
```

#### Response (422) — Full Build Failure (Multi-Container)

```json
{
  "error": "FULL_BUILD_FAILURE",
  "message": "3 of 3 services failed to build: ...",
  "build": {
    "status": "full_failure",
    "started_at": "2026-06-01T03:40:10Z",
    "completed_at": "2026-06-01T03:40:15Z",
    "duration_ms": 5000,
    "registry": "localhost:5000",
    "registry_auth": {
      "method": "none",
      "authenticated": false
    },
    "tooling": {
      "nerdctl": "1.7.6",
      "buildkit": "v0.12.5"
    },
    "ready_for_deployment": false,
    "containers": [
      {"name": "web", "status": "failed", "error": "..."},
      {"name": "api", "status": "failed", "error": "..."},
      {"name": "db", "status": "failed", "error": "..."}
    ]
  },
  "retry_info": {
    "can_retry": true,
    "retry_url": "/api/v1/challenges",
    "failed_containers": ["web", "api", "db"]
  }
}
```

---

### Build Metadata Fields

| Field | Type | Description |
|---|---|---|
| `status` | string | `success`, `skipped`, `partial_failure`, `full_failure` |
| `started_at` | RFC3339 | When the build operation started |
| `completed_at` | RFC3339 | When the build operation finished |
| `duration_ms` | int | Total build duration in milliseconds |
| `registry` | string | Registry URL images were pushed to |
| `registry_auth.method` | string | `none`, `basic`, `ghcr`, `ecr` |
| `registry_auth.authenticated` | bool | Whether registry credentials are configured |
| `tooling.nerdctl` | string | nerdctl version used for build |
| `tooling.buildkit` | string | BuildKit version used for build |
| `ready_for_deployment` | bool | `true` only when all containers built successfully |
| `containers[].name` | string | Service/container name |
| `containers[].image` | string | Full image reference (empty on failure) |
| `containers[].status` | string | `built`, `pre-built`, `pulled`, `failed` |
| `containers[].duration_ms` | int | Per-container build duration |
| `containers[].ports` | int[] | Ports extracted from EXPOSE or compose config |
| `containers[].error` | string | Error message (only on `failed` status) |

### Retry Info Fields

| Field | Type | Description |
|---|---|---|
| `can_retry` | bool | Always `true` for build failures |
| `retry_url` | string | Endpoint to retry the request |
| `failed_containers` | string[] | Names of containers that failed to build |
| `note` | string | Helpful context about cached layers |

---

### Build Logs

`GET /api/v1/challenges/:id/build-logs`

Returns build logs for a challenge's containers. Logs are stored for 24 hours after build.

**Query Parameters:**

| Param | Type | Default | Description |
|---|---|---|---|
| `container` | string | (all) | Filter by container name |
| `lines` | int | (all) | Return only the last N lines |
| `follow` | bool | `false` | Stream logs via SSE (Server-Sent Events) |

**Response (200 OK) — All Containers:**
```json
{
  "challenge_id": "complex-web-a1b2c3d4",
  "logs": {
    "web": "[1/5] FROM docker.io/library/python:3.12-slim\n[2/5] COPY . /app\n...",
    "db": "[1/3] FROM docker.io/library/postgres:16\n[2/3] COPY init.sql /docker-entrypoint-initdb.d/\n..."
  }
}
```

**Response (200 OK) — Single Container with Pagination:**
```
GET /api/v1/challenges/complex-web-a1b2c3d4/build-logs?container=web&lines=50
```
```json
{
  "challenge_id": "complex-web-a1b2c3d4",
  "container": "web",
  "log": "[5/5] CMD [\"python\", \"app.py\"]\n ---> Running in 1a2b3c4d\n..."
}
```

**Streaming (SSE):**
```
GET /api/v1/challenges/complex-web-a1b2c3d4/build-logs?follow=true
```

Returns `text/event-stream` with events:
```
event: log
data: [1/5] FROM docker.io/library/python:3.12-slim

event: log
data: {"container":"db","log":"[1/3] FROM docker.io/library/postgres:16"}
```

**Error Responses:**
- `404` — Challenge not found
- `404` — Build log not found (expired or never built)

---

### Rebuild Challenge

`POST /api/v1/challenges/:id/rebuild`

Triggers a fresh nerdctl build for an existing challenge. Returns the same wrapped response format as Create.

**Response (200 OK):**
```json
{
  "challenge": {
    "id": "pwn-challenge-1234abcd",
    "name": "pwn-challenge",
    "image": "localhost:5000/pwn-challenge:latest",
    "tag": "latest",
    "ttl_minutes": 60,
    "ports": [80],
    "created_at": "2026-06-01T03:40:29Z",
    "updated_at": "2026-06-01T04:15:00Z"
  },
  "build": {
    "status": "success",
    "started_at": "2026-06-01T04:14:50Z",
    "completed_at": "2026-06-01T04:15:00Z",
    "duration_ms": 10000,
    "registry": "localhost:5000",
    "registry_auth": {
      "method": "none",
      "authenticated": false
    },
    "tooling": {
      "nerdctl": "1.7.6",
      "buildkit": "v0.12.5"
    },
    "ready_for_deployment": true,
    "containers": [
      {
        "name": "pwn-challenge",
        "image": "localhost:5000/pwn-challenge:latest",
        "status": "built",
        "duration_ms": 10000,
        "ports": [80]
      }
    ]
  }
}
```

---

### List Challenges

`GET /api/v1/challenges`

```json
{
  "challenges": [...],
  "count": 5
}
```

### Get Challenge

`GET /api/v1/challenges/:id`

Returns the challenge object (without build metadata).

### Delete Challenge

`DELETE /api/v1/challenges/:id`

```json
{
  "challenge_id": "pwn-challenge-1234abcd",
  "status": "deleted"
}
```

---

## Admin & Monitoring API

Restricted endpoints for cluster visibility and maintenance.

### Cluster Health

`GET /api/v1/admin/cluster/health`

Returns health status of Engine, Redis, and Node Agent.

### Get Configuration

`GET /api/v1/admin/config`

Returns current runtime configuration including default resource limits.

### Update Registry Configuration

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

### Cluster Visibility

- `GET /api/v1/admin/nodes`: List K8s nodes and their status.
- `GET /api/v1/admin/cluster/pods`: Raw list of all challenge pods.
- `GET /api/v1/admin/registry/images`: List images stored in the local registry.

---

## Meta & Diagnostics

- `/health`: Liveness check for the Engine process.
- `/metrics`: Prometheus-compatible metrics endpoint.
- `/debug/system`: High-level system statistics (Total sessions, total pods).
- `/debug/controller`: Internal reconciler loop statistics.
