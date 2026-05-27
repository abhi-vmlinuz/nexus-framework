# Nexus OSS — Architecture

## Overview

Nexus OSS is a **generic infrastructure layer** for orchestrating isolated, ephemeral challenge environments. It is decoupled from any specific CTF platform, scoring system, or billing logic.

```
┌─────────────────────────────────────────────────────────────┐
│                    Consumer (CTF Platform)                   │
│            POST /api/v1/sessions  { challenge_id, user_id } │
└───────────────────────────┬─────────────────────────────────┘
                            │ HTTP REST
┌───────────────────────────▼─────────────────────────────────┐
│                     nexus-engine (Go)                        │
│                                                              │
│  ┌─────────────┐  ┌──────────────┐  ┌──────────────────┐   │
│  │ Gin HTTP API│  │ Reconciliation│  │  k3s Client      │   │
│  │             │  │ Controller   │  │  (client-go)     │   │
│  │ /sessions   │  │ (worker pool)│  │                  │   │
│  │ /challenges │  │              │  │  SpawnPod()      │   │
│  │ /admin      │  │ 15s interval │  │  TerminatePod()  │   │
│  └──────┬──────┘  └──────┬───────┘  └──────────────────┘   │
│         │                │                                   │
│         │    ┌───────────┴────────┐                         │
│         │    │  Redis (state)      │                         │
│         │    │  session:<id>       │                         │
│         │    │  challenge:<id>     │                         │
│         │    │  active_sessions    │                         │
│         │    └────────────────────┘                         │
│         │ gRPC                                              │
└─────────┼───────────────────────────────────────────────────┘
          │
          │  gRPC (mTLS in prod / insecure in dev)
          │
┌─────────▼───────────────────────────────────────────────────┐
│                  nexus-node-agent (Rust)                     │
│                                                              │
│  ┌─────────────────┐  ┌────────────────┐  ┌─────────────┐  │
│  │ EnsureUserIso   │  │ GrantPodAccess │  │  WireGuard  │  │
│  │ RevokeUserIso   │  │ RevokePodAccess│  │  EnsurePeer │  │
│  └────────┬────────┘  └───────┬────────┘  └──────┬──────┘  │
│           │                   │                   │         │
│  ┌────────▼───────────────────▼───────────────────▼──────┐  │
│  │              Kernel Adapters                           │  │
│  │  ipset (hash:ip)  iptables (FORWARD)  wg syncconf    │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

## Architectural Boundary: Challenge Building & Registry (Separation of Concerns)

A critical design choice in Nexus OSS is the **strict separation of concerns** between the consuming CTF platform (or developer tools) and the Nexus Engine regarding how challenge files are managed, built, and stored.

```mermaid
graph TD
    subgraph Platform / Developer (Outside Nexus)
        A[Challenge Zip / Code] -->|CI/CD, SCP, Git, or Portal Upload| B[Host Local Filesystem]
        C[Direct Build Pipeline] -->|Docker Build & Push| D[(Target Registry)]
    end

    subgraph Nexus Engine (On-Host Control Plane)
        B -->|POST /challenges {dockerfile_path}| E[validateDockerfile]
        E -->|nerdctl build| F[Local Containerd]
        F -->|nerdctl push| D
    end
```

### 1. Developer / Consumer Responsibility
Nexus **does not** manage remote file transfers, Git repositories, or user upload portals. 
* **The File Transfer Phase**: The consuming platform (e.g. your CTF backend) is entirely responsible for getting the challenge source files and the `Dockerfile` onto the engine host's local filesystem (using mechanisms like SCP, Git clone, or custom backend ZIP extraction).
* **Direct Registry Pushes**: Alternatively, developers/platforms with their own CI/CD pipelines can build and push their challenge images directly to the configured container registry, bypassing the Nexus build system entirely and supplying only prebuilt `containers[]` to the session creation API.

### 2. Nexus Engine Responsibility
Once the files reside on the engine host's filesystem, Nexus takes over:
* **`POST /api/v1/challenges` with `dockerfile_path`**: The engine expects a local filesystem path on the host. It validates that the `Dockerfile` exists locally (`builder.go`), builds it using `nerdctl` inside the K3s namespace, tags it, and pushes the compiled image to the configured target registry.
* **`POST /api/v1/challenges` with `compose_path`**: The engine parses the Docker Compose file, builds or pulls each service locally on the host, and registers them.
* **`POST /api/v1/challenges` with `containers[]`**: The engine skips the build stage entirely, storing the pre-existing container references directly.

> [!IMPORTANT]
> **Host Filesystem Dependency**: When invoking the build-from-source endpoint (`dockerfile_path`), the engine executes the compilation locally. Therefore, the files **must** already reside on the same server that the `nexus-engine` service is running on.

### Environment Variable Handling (Compose)

When using `compose_path`, the engine extracts `environment` variables from each service and passes them to the K3s pod containers. This supports both compose formats:

**Map format:**
```yaml
services:
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: ctf
      POSTGRES_PASSWORD: ctf
      POSTGRES_DB: ctf
```

**List format:**
```yaml
services:
  db:
    image: postgres:16-alpine
    environment:
      - POSTGRES_USER=ctf
      - POSTGRES_PASSWORD=ctf
      - POSTGRES_DB=ctf
```

> [!NOTE]
> **CTF Platform Integration**: When a CTF platform creates challenges via `containers[]` (pre-built images), it must include the `env` field for each container. The engine passes these environment variables directly to the K3s pod. Without them, services that require configuration (like databases) will fail to start.

**Example with environment variables:**
```json
{
  "name": "web-app-challenge",
  "containers": [
    {
      "name": "web",
      "image": "localhost:5000/my-web:latest",
      "ports": [8080],
      "env": {"DB_HOST": "localhost", "DB_PORT": "5432"}
    },
    {
      "name": "db",
      "image": "localhost:5000/my-db:latest",
      "ports": [5432],
      "env": {"POSTGRES_USER": "ctf", "POSTGRES_PASSWORD": "ctf"}
    }
  ]
}
```

### API Examples: Registering Challenges

#### Option A: Single-Container — Build from Source (Engine builds)

The developer deploys the challenge folder to the engine host, then tells Nexus to build it.

```bash
# 1. Developer deploys files to engine host (outside Nexus)
scp -r ./my-challenge/ user@engine-host:/opt/nexus/challenges/my-challenge/

# 2. Register challenge — engine builds + pushes to registry
curl -X POST http://localhost:8081/api/v1/challenges \
  -H "Content-Type: application/json" \
  -d '{
    "name": "pwn-101",
    "dockerfile_path": "/opt/nexus/challenges/my-challenge/Dockerfile",
    "ttl_minutes": 60
  }'
```

**What Nexus does:** Validates the Dockerfile exists on the engine host → runs `nerdctl build --namespace k8s.io -t localhost:5000/pwn-101:latest -f /opt/nexus/challenges/my-challenge/Dockerfile /opt/nexus/challenges/my-challenge/` → runs `nerdctl push localhost:5000/pwn-101:latest` → stores challenge in Redis with the image reference.

**Response:**
```json
{
  "id": "pwn-101-a1b2c3d4",
  "name": "pwn-101",
  "image": "localhost:5000/pwn-101:latest",
  "tag": "latest",
  "ports": [8080],
  "ttl_minutes": 60,
  "created_at": "2026-05-27T12:00:00Z"
}
```

#### Option B: Single-Container — Pre-Built Image (Developer pushes)

The developer builds and pushes the image themselves (CI/CD, local docker, etc.), then tells Nexus where it is.

```bash
# 1. Developer builds and pushes directly to registry (outside Nexus)
docker build -t localhost:5000/pwn-101:latest .
docker push localhost:5000/pwn-101:latest

# 2. Register challenge — no build, just references
curl -X POST http://localhost:8081/api/v1/challenges \
  -H "Content-Type: application/json" \
  -d '{
    "name": "pwn-101",
    "containers": [
      {
        "name": "main",
        "image": "localhost:5000/pwn-101:latest",
        "ports": [8080]
      }
    ],
    "ttl_minutes": 60
  }'
```

**What Nexus does:** Skips build entirely → stores the container spec in Redis.

#### Option C: Multi-Container — Build from Compose (Engine builds)

The developer deploys a `docker-compose.yml` to the engine host. Nexus parses it, builds/pulls each service, and creates the container spec array.

```bash
# 1. Developer deploys compose folder to engine host
scp -r ./web-challenge/ user@engine-host:/opt/nexus/challenges/web-challenge/

# 2. Register challenge — engine parses compose, builds/pulls all services
curl -X POST http://localhost:8081/api/v1/challenges \
  -H "Content-Type: application/json" \
  -d '{
    "name": "web-app-101",
    "compose_path": "/opt/nexus/challenges/web-challenge/docker-compose.yml",
    "ttl_minutes": 90
  }'
```

**docker-compose.yml** (on engine host):
```yaml
services:
  web:
    build:
      context: ./web
      dockerfile: Dockerfile
    ports:
      - "80:80"
  db:
    image: postgres:16-alpine
    ports:
      - "5432:5432"
  cache:
    image: redis:7-alpine
    ports:
      - "6379:6379"
```

**What Nexus does:** Parses the compose file → for `web` (has `build`): runs `nerdctl build` + `nerdctl push localhost:5000/web-app-101-web:latest` → for `db` and `cache` (has `image`): runs `nerdctl pull` into k8s.io namespace → stores all three as `containers[]` in Redis.

**Response:**
```json
{
  "id": "web-app-101-e5f6g7h8",
  "name": "web-app-101",
  "containers": [
    {"name": "web", "image": "localhost:5000/web-app-101-web:latest", "ports": [80]},
    {"name": "db", "image": "postgres:16-alpine", "ports": [5432]},
    {"name": "cache", "image": "redis:7-alpine", "ports": [6379]}
  ],
  "ports": [80, 5432, 6379],
  "ttl_minutes": 90,
  "created_at": "2026-05-27T12:00:00Z"
}
```

#### Option D: Multi-Container — Pre-Built Images (Developer pushes)

The developer pushes all images to the registry themselves, then tells Nexus about them.

```bash
# 1. Developer builds and pushes all images (outside Nexus)
docker build -t localhost:5000/web-app-101-web:latest ./web/
docker push localhost:5000/web-app-101-web:latest
docker pull postgres:16-alpine
docker tag postgres:16-alpine localhost:5000/web-app-101-db:latest
docker push localhost:5000/web-app-101-db:latest

# 2. Register challenge — no build, just references
curl -X POST http://localhost:8081/api/v1/challenges \
  -H "Content-Type: application/json" \
  -d '{
    "name": "web-app-101",
    "containers": [
      {"name": "web", "image": "localhost:5000/web-app-101-web:latest", "ports": [80]},
      {"name": "db", "image": "localhost:5000/web-app-101-db:latest", "ports": [5432]}
    ],
    "ttl_minutes": 90
  }'
```

**What Nexus does:** Skips build → stores the container spec array in Redis.

### Quick Reference: Which Option to Use?

| Scenario | Use | Developer Does | Nexus Does |
|---|---|---|---|
| No CI/CD, want Nexus to build | `dockerfile_path` or `compose_path` | Deploy files to engine host | Build + push to registry |
| Have CI/CD pipeline | `containers[]` | Build + push images to registry | Store references only |
| Mix (some built, some pulled) | `compose_path` | Deploy compose file | Build services with `build:`, pull services with `image:` |

> [!IMPORTANT]
> **Environment Variables**: When using `compose_path`, Nexus extracts env vars automatically. When using `containers[]`, you must include the `env` field for each container that needs configuration. Services like databases will fail without their required environment variables.

---

## Control Plane: nexus-engine

**Language:** Go 1.22  
**Runtime:** Gin (HTTP), client-go (k3s), go-redis (state)

### Session lifecycle

```
POST /api/v1/sessions
       │
       ├─→ Validate: challenge exists, user_id present
       ├─→ Check: session limit (if configured)
       ├─→ k3s: SpawnPod() → waits for PodIP (90s timeout)
       ├─→ node agent: GrantPodAccess(user_id, pod_ip)  [prod]
       ├─→ node agent: EnsureUserIsolation(user_id, vpn_ip)  [prod]
       ├─→ Redis: SaveSession()
       ├─→ Redis: TouchDesiredVersion() → enqueue reconcile
       └─→ Return: session_id, pod_ip, expires_at
```

### Reconciliation controller

The controller runs as a background process. It ensures **desired state converges to actual state**:

1. **Bootstrap scan** — on startup, enqueues all active sessions
2. **Periodic scan** — every `NEXUS_RECONCILE_INTERVAL` (±20% jitter)
3. **Touch-triggered** — on session create/terminate/extend
4. **Worker pool** — `NEXUS_MAX_WORKERS` goroutines drain the job queue
5. **Idempotent repairs** — re-applies VPN grants (safe to duplicate), re-checks pod health
6. **TTL enforcement** — expires sessions past `ExpiresAt`
7. **Cleanup loop** — removes orphaned pods every 5 minutes

---

## Execution Plane: nexus-node-agent

**Language:** Rust (tokio, tonic)  
**Privileges:** Runs with `CAP_NET_ADMIN` for kernel operations

### Per-user VPN isolation (prod mode)

```
EnsureUserIsolation(user_id="alice", vpn_ip="10.8.0.2")

  1. ipset create nexus-user-alice hash:ip -exist
  2. iptables -I FORWARD 1 -s 10.8.0.2 -m set --match-set nexus-user-alice dst -j ACCEPT
  3. iptables -I FORWARD 2 -s 10.8.0.2 -j DROP
```

When a pod is granted:
```
GrantPodAccess(user_id="alice", pod_ip="10.244.0.5")

  1. ipset add nexus-user-alice 10.244.0.5 -exist
```

Result: Alice's VPN traffic (`10.8.0.2`) can only reach her pod IP (`10.244.0.5`). All other traffic is dropped at the FORWARD chain.

### Idempotency guarantees

| Operation | Idempotent | How |
|---|---|---|
| `EnsureUserIsolation` | ✅ | `ipset create -exist`, `iptables -C` before `-I` |
| `RevokeUserIsolation` | ✅ | loop `-D` until not found |
| `GrantPodAccess` | ✅ | `ipset add -exist` |
| `RevokePodAccess` | ✅ | `ipset del`, ignores "element not found" |
| `EnsureWireGuardPeer` | ✅ | Remove block then re-append, `wg syncconf` |
| `RevokeWireGuardPeer` | ✅ | Remove block, `wg set peer remove` |

---

## Operator Interface: nexus-cli

**Language:** Go 1.22  
**UI:** Cobra (commands) + Bubbletea (TUI) + Lipgloss (styling)

### TUI tabs

| Tab | Content |
|---|---|
| Sessions | Live session table with status, pod IP, TTL |
| Challenges | Registered challenges, ports, images |
| System | Session/pod counts, mode, registry |
| Controller | Worker stats, queue depth, reconcile interval |

Polling interval: 3 seconds. Keyboard: `←/→` tabs, `↑/↓` rows, `r` refresh, `q` quit.

---

## State Schema (Redis)

```
session:<id>           → Session JSON (TTL = session expiry)
session:<id>:desired   → int64 counter (desired reconcile version)
session:<id>:observed  → ReconcileMeta JSON
challenge:<id>         → Challenge JSON (no TTL)
active_sessions        → set of session IDs
user_sessions:<uid>    → set of session IDs for user
grant:pod:<pod_ip>     → GrantRecord JSON
challenges             → set of challenge IDs
```

---

## Operating Modes

| Feature | dev | prod |
|---|---|---|
| VPN isolation (ipset/iptables) | ❌ Optional | ✅ Required |
| `vpn_ip` in session create | Optional | Required |
| mTLS on gRPC | ❌ (insecure) | ✅ |
| Pod access | Direct pod IP | VPN only |
| Network policy | allow-all | VPN-subnet-only |
