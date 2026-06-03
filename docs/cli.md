# Nexus CLI Reference Manual

The `nexus` command-line interface (CLI) is an operator tool for debugging, configuration, and monitoring the Nexus Engine. It is not the primary interface for managing the CTF platform — that role belongs to the CTF platform backend which communicates with the Engine API directly.

Use the CLI to:
- Inspect engine health, sessions, and cluster state
- Register and manage challenges during development/testing
- Configure engine settings and registry connections
- Monitor live activity via the TUI dashboard

For programmatic integration, use the [Engine REST API](api.md) directly.

---

## Global Flags and Environment Variables

Every command supports overriding the target Nexus Engine URL.

* **Global Flag**: `--engine <url>` (e.g., `nexus status --engine http://10.0.0.5:8081`)
* **Environment Variable**: `NEXUS_ENGINE_URL` (e.g., `export NEXUS_ENGINE_URL=http://10.0.0.5:8081`)

By default, the CLI attempts to read configuration from `~/.config/nexus/config.json`. If this file is missing, it will fall back to environment variables.

---

## Command Reference

### `nexus status`

Perform a health check against the remote Nexus Engine, verify connectivity, and inspect active worker statistics.

```bash
nexus status
```

**Example Output:**
```text
Engine: healthy | mode=dev | time=2026-06-03T04:12:00Z
Sessions: 4  Pods: 4  Registry: localhost:5000
Controller: active | workers=5 | queued=0 | in-flight=0
```

---

### `nexus tui`

Launch the live Bubbletea terminal dashboard. The TUI provides tabs for active sessions, registered challenges, system configuration, and reconciler statistics.

```bash
nexus tui
```

* **Controls:**
  * `←` / `→` or `1`-`4` keys to switch tabs
  * `r` to manually refresh statistics
  * `q` or `Ctrl+C` to exit the dashboard
  * Mouse wheel and clicks are supported for scrolling lists and selecting entries

---

### `nexus challenge` (alias: `ch`, `chal`)

Manage challenge definitions, register new single or multi-container configurations, and rebuild images.

#### 1. Register a Challenge
Register a challenge from a local `Dockerfile` (single-container) or a `docker-compose.yml` specification (multi-container).

```bash
# Single container challenge (build via dockerfile)
nexus challenge register \
  --name pwn-101 \
  --dockerfile ./challenges/pwn-101/Dockerfile \
  --ports 4444 \
  --ttl 60

# Multi-container challenge (build via docker-compose)
nexus challenge register \
  --name web-app-db \
  --compose ./challenges/web-app/docker-compose.yml \
  --ttl 120
```

**Flags:**
* `--name <string>` (Required): Unique identifier name of the challenge.
* `--dockerfile <path>`: Local path to the challenge Dockerfile (single-container only).
* `--compose <path>`: Local path to the challenge `docker-compose.yml` (multi-container only).
* `--ports <ints>`: Comma-separated list of exposed ports (overrides Dockerfile `EXPOSE` values, single-container only).
* `--ttl <int>`: Default session duration limit in minutes for players spawning this challenge.
* `--cpu <string>`: CPU limit constraint (e.g., `0.5`, `500m`, single-container only).
* `--memory <string>`: Memory limit constraint (e.g., `256Mi`, `512M` — always use a unit suffix like `Mi`/`Gi`! Single-container only).

#### 2. List Challenges
List all challenges currently registered in the database.

```bash
nexus challenge list
```

#### 3. Inspect Challenge Details
Retrieve complete configuration details of a challenge, returned in JSON format.

```bash
nexus challenge get <challenge-id>
```

#### 4. Delete Challenge
Delete a challenge definition from the control plane registry. Note that this command is safe and does not terminate existing sessions spawned from this challenge.

```bash
nexus challenge delete <challenge-id>
```

#### 5. Rebuild Challenge Images
Trigger a clean rebuild of all container images associated with a challenge on the Nexus Engine node.

```bash
nexus challenge rebuild <challenge-id>
```

---

### `nexus session` (alias: `s`, `sess`)

Create, monitor, extend, and terminate player challenge sessions.

#### 1. Spawn a Session
Spawns a dedicated challenge instance for a specific player.

```bash
nexus session create --challenge pwn-101 --user alice
```

In **prod mode**, the WireGuard VPN is active, and you must assign or specify a client VPN IP:
```bash
nexus session create --challenge pwn-101 --user alice --vpn-ip 10.8.0.5
```

**Flags:**
* `--challenge <challenge-id>` (Required): ID of the challenge to spawn.
* `--user <user-id>` (Required): Unique identifier for the player.
* `--vpn-ip <string>`: Dedicated static WireGuard IP allocated to the player (required in production mode).

#### 2. List Active Sessions
List currently active challenge instances.

```bash
# List active sessions only
nexus session list

# List all sessions (including terminated, expired, or failed)
nexus session list --all
```

#### 3. Inspect Session Details
Retrieve the runtime JSON state of a session, including pod IPs, endpoints, resource statuses, and expirations.

```bash
nexus session get <session-id>
```

#### 4. Terminate a Session
Forcefully terminate a session, which tears down the K3s pods and removes WireGuard peer/firewall configuration immediately.

```bash
nexus session terminate <session-id>
```

#### 5. Extend Session TTL
Extend the expiration limit of a session.

```bash
# Add 30 minutes to session lifetime
nexus session extend <session-id> --minutes 30
```

**Flags:**
* `--minutes <int>`: Number of minutes to add to the session (defaults to configured engine default TTL).
* `--duration <int>`: Alias for `--minutes`.

---

### `nexus config`

Manage CLI and remote hot-reloadable engine settings.

#### 1. View Configuration
Compare local CLI configuration and remote hot-reloadable settings side-by-side.

```bash
nexus config view
```

#### 2. Set Configuration Keys
Update local CLI settings or remote Engine settings live.

```bash
# Update remote Engine settings (hot-reloadable)
nexus config set challenge.cpu 0.5
nexus config set challenge.memory 256Mi
nexus config set session.ttl 120

# Update local CLI settings
nexus config set engine.url http://10.0.0.10:8081
nexus config set k8s.namespace nexus-dev
```

**Supported Engine Keys:**
* `challenge.cpu`: Default CPU cores limit per challenge pod (e.g. `0.5`, `500m`).
* `challenge.memory`: Default memory limit per challenge pod (e.g. `256Mi`, `1Gi` — always add `Mi` or `Gi` unit!).
* `session.ttl`: Default session duration limit in minutes.
* `session.max_per_user`: Max concurrent active sessions a user can run.
* `reconciler.workers`: Number of concurrent workers for the K3s state controller.

**Supported Local CLI Keys:**
* `api_key`: API authentication key for the engine (no category prefix needed).
* `engine.url`: Remote Nexus Engine API base URL.
* `engine.mode`: Run mode (`dev` or `prod`).
* `registry.type`: Target container registry type (`local`, `basic`, `ghcr`, `awsecr`).
* `registry.url`: Container registry URL.
* `redis.url`: Redis instance connection URL.
* `node_agent.addr`: Address of the node agent daemon.
* `k8s.namespace`: K3s namespace to isolate challenge pods.

**Setting the API Key:**
```bash
# Set the API key (used to authenticate with the engine)
nexus config set api_key <your-api-key>

# The key is stored in ~/.config/nexus/config.json
# and sent as Authorization: Bearer <key> on all API requests
```

The API key is generated during installation and written to both `/etc/nexus/engine.env` (engine side) and `~/.config/nexus/config.json` (CLI side). If the engine has a key configured, the CLI must have the same key to communicate with it.

#### 3. Configure Container Registry (Interactive)
Interactively link and authenticate Nexus Engine with external container registries like GHCR, ECR, Docker Hub, private repositories, or local registry servers. This automatically generates appropriate image pull secrets and configures the container build engine.

```bash
nexus config registry
```

#### 4. Initialize Local Config
Interactively setup a brand new CLI configuration file at `~/.config/nexus/config.json`.

```bash
nexus config init
```

#### 5. Validate Configuration
Verify local settings and check socket connectivity to Redis and the Nexus Engine.

```bash
nexus config validate
```

#### 6. Reset Configuration
Delete the local configuration file.

```bash
nexus config reset
```

---

### `nexus admin`

Perform operator-level tasks like checking underlying infrastructure health or triggering reconciliation loops manually.

#### 1. Cluster Health Audit
Check status metrics of Redis, K3s namespace connectivity, and node agent sockets.

```bash
nexus admin health
```

#### 2. Trigger Reconciler
Manually force an immediate state verification and reconciliation pass for all active sessions.

```bash
nexus admin reconcile
```

---

### `nexus version`

Print the version of the `nexus` CLI client.

```bash
nexus version
```

---

### `nexus completion`

Generate autocomplete scripts for Bash, Zsh, Fish, or PowerShell.

```bash
# Load bash auto-completions in current shell
source <(nexus completion bash)

# Load zsh auto-completions in current shell
source <(nexus completion zsh)
```
