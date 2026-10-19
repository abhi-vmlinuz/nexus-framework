# Nexus Framework — Competition Operations & Disaster Recovery Runbook

> **Operational guide for organizing and running university cybersecurity competitions (300–400 participants) on Nexus Framework.**

---

## 1. Overview & Architecture Envelope

* **Deployment**: Single bare-metal host running K3s, `nexus-engine` (Go), `nexus-node-agent` (Rust), Redis 7 (persisted), and WireGuard (`wg0`).
* **Capacity Limit**: WireGuard VPN subnet `10.8.0.0/22` supports **1,022 usable IPs** (`10.8.0.2` – `10.8.3.254`).
* **Concurrency Target**: 300–400 active students simultaneously running container challenge pods in namespace `nexus-challenges`.
* **State & Persistence**:
  * Redis: Data volume mounted at `/var/lib/nexus/redis` with AOF (`--appendonly yes`) enabled.
  * Telemetry: On-disk JSONL append log at `/var/lib/nexus/events.jsonl` (mode `0600`).
  * WireGuard: `/etc/wireguard/wg0.conf` updated atomically with mutex locking.

---

## 2. Pre-Flight Checklist (T-24h to T-1h)

Run these checks prior to student onboarding:

### 2.1 Verify Services & Health Endpoints
```bash
# Check systemd daemons
sudo systemctl is-active nexus-engine nexus-node-agent k3s

# Query engine cluster health
curl -s -H "Authorization: Bearer $(sudo grep NEXUS_API_KEY /etc/nexus/engine.env | cut -d= -f2)" \
  http://localhost:8081/api/v1/admin/cluster/health | jq .
```
Verify:
* `status`: `"healthy"`
* `redis`: `"ok"`
* `node_agent`: `"healthy"` (reports non-zero `disk_percent`, `cpu_percent`, `mem_percent`)

### 2.1a Verify Container-Runtime Access (non-root nerdctl)
```bash
# Socket must stay group-readable or manual nerdctl calls break on k3s restart
stat -c %G /run/k3s/containerd/containerd.sock  # expect: nexus
systemctl is-active nexus-socket-fix.path       # expect: active

# Non-root nerdctl ignores --address and dies on a rootless socket that
# doesn't exist, so human ops go through scoped passwordless sudo
# (installed by setup.sh / installer Phase 10 for the installing user):
sudo -n nerdctl --address /run/k3s/containerd/containerd.sock --namespace k8s.io ps | head -3
```
If the socket group isn't `nexus`, re-run: `sudo chown root:nexus /run/k3s/containerd/containerd.sock && sudo chmod 660 /run/k3s/containerd/containerd.sock`. If `sudo -n nerdctl` asks for a password, recreate `/etc/sudoers.d/nexus-nerdctl` with `<user> ALL=(ALL) NOPASSWD: /usr/local/bin/nerdctl` (mode 440) and verify with `sudo visudo -c -q`.

### 2.2 Verify Redis Persistence
```bash
# Confirm volume mount and AOF
nerdctl inspect nexus-redis | grep -E "appendonly|/var/lib/nexus/redis"

# Confirm write test
redis-cli set preflight_check "$(date)"
redis-cli bgsave
```

### 2.3 Verify WireGuard Subnet
```bash
# Check interface wg0 has /22 subnet
ip addr show wg0 | grep "10.8.0.1/22"
```

### 2.4 Verify Monitoring & Alerts

The monitoring stack is installed by `deploy/setup.sh` (and by the TUI
installer). It is host-level, in the same style as Redis and the registry:
four pieces, no Kubernetes RBAC.

| Piece | Port | Role |
|---|---|---|
| `nexus-kubectl-proxy` (systemd) | 127.0.0.1:8001 | Loopback bridge to the k3s API, used for pod scrapes |
| `nexus-prometheus` (nerdctl) | 9090 | Scrapes engine, kubelet, kube-state-metrics |
| `nexus-grafana` (nerdctl) | 3000 | Dashboards, provisioned from disk |
| `kube-state-metrics` (pod) | — | Exposes pod labels for per-session attribution |

```bash
# All three scrape targets should read "up"
curl -s 'http://localhost:9090/api/v1/targets?state=active' \
  | grep -o '"health":"up"' | wc -l   # expect: 3

# Grafana login: admin / password from the file below
sudo cat /etc/nexus/monitoring/grafana-admin.txt

# The engine scrape is authenticated, so an unauthenticated call must fail
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8081/metrics   # expect: 401
```

The dashboard lives in the Grafana `Nexus` folder. It is mounted from
`/etc/nexus/monitoring/grafana/dashboards/nexus.json`, so editing the tracked
`deploy/monitoring/grafana-dashboard.json` and re-running the installer is how
panels change — do not edit the dashboard in the UI, it is overwritten.

**Attribution.** Pod resource series (cAdvisor) carry no identity. They are
joined to `kube_pod_labels` on `(namespace, pod)` so every series resolves to
`nexus-session-id`, `nexus-user-id`, and `nexus-challenge-id`:

```promql
sum by (label_nexus_user_id) (
  container_memory_working_set_bytes{namespace="nexus-challenges",container!=""}
  * on (namespace,pod) group_left(label_nexus_user_id) kube_pod_labels
) / 1024 / 1024
```

If that query returns nothing while pods are running, kube-state-metrics has
lost its label allowlist — see "Troubleshooting" below.

#### Troubleshooting the monitoring stack

```bash
# Container logs
sudo nerdctl --address /run/k3s/containerd/containerd.sock logs nexus-prometheus
sudo nerdctl --address /run/k3s/containerd/containerd.sock logs nexus-grafana

# Is the loopback bridge alive? Must print a version payload, not 000.
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8001/version

# Pod labels exposed? Must be non-zero.
curl -s 'http://127.0.0.1:8001/api/v1/namespaces/kube-system/services/kube-state-metrics:http/proxy/metrics' \
  | grep -c '^kube_pod_labels'
```

| Symptom | Cause | Fix |
|---|---|---|
| `nexus-engine` target down, "unable to read authorization credential" | `api-key` file not readable by the container user | Prometheus must run as uid 0 (`--user 0`); do not loosen the file to 0644 |
| `kube-state-metrics` target down, HTTP 404 | kube-state-metrics not deployed | `kubectl apply -f /etc/nexus/monitoring/kube-state-metrics.yaml` |
| `kube_pod_labels` missing but metrics otherwise flow | label allowlist dropped from the deployment args | Must include `--metric-labels-allowlist=pods=[nexus-session-id,nexus-user-id,nexus-challenge-id]` |
| All cluster targets down | `kubectl proxy` not running | `sudo systemctl restart nexus-kubectl-proxy` |

---

## 3. Backup & Disaster Recovery Procedures

### 3.1 Redis Snapshot & Backup
Redis holds session metadata, active tokens, and challenge registrations.

```bash
# 1. Trigger Redis background save
redis-cli bgsave

# 2. Copy RDB dump and AOF directory to external/backup storage
sudo mkdir -p /opt/nexus-backups/$(date +%Y%m%d_%H%M%S)
sudo cp -r /var/lib/nexus/redis/* /opt/nexus-backups/$(date +%Y%m%d_%H%M%S)/
```

#### Redis Recovery Procedure
If the Redis container or volume is corrupted:
```bash
# Stop Redis container
nerdctl stop nexus-redis && nerdctl rm nexus-redis

# Restore backup files to /var/lib/nexus/redis
sudo cp /opt/nexus-backups/<backup_timestamp>/dump.rdb /var/lib/nexus/redis/

# Start Redis with volume mount and AOF
nerdctl run -d \
  --name nexus-redis \
  --restart always \
  -p 6379:6379 \
  -v /var/lib/nexus/redis:/data \
  redis:7-alpine redis-server --appendonly yes

# Restart engine to trigger bootstrap reconciliation
sudo systemctl restart nexus-engine
```

### 3.2 WireGuard Configuration Backup & Restore
```bash
# Backup config
sudo cp /etc/wireguard/wg0.conf /opt/nexus-backups/wg0.conf.$(date +%s)

# Restore if corrupted
sudo cp /opt/nexus-backups/wg0.conf.<timestamp> /etc/wireguard/wg0.conf
sudo wg syncconf wg0 <(wg-quick strip wg0)
```

### 3.3 Event Telemetry Log Backup
The competition event log (`/var/lib/nexus/events.jsonl`) contains all pitch and audit data:
```bash
# Periodic snapshot during the event
sudo cp /var/lib/nexus/events.jsonl /opt/nexus-backups/events-$(date +%H%M%S).jsonl
```

---

## 4. Emergency Operational Drills

### Scenario A: VPN Pool Exhaustion Warning (`NexusVPNPoolHigh` Alert)
* **Trigger**: PromQL `(nexus_vpn_ips_used / 1022) * 100 > 80%`.
* **Action**:
  1. Inspect student count: `redis-cli scard vpn_ips`.
  2. Check expired sessions that failed to de-allocate:
     ```bash
     curl -s -H "Authorization: Bearer <KEY>" http://localhost:8081/api/v1/admin/sessions | jq length
     ```
  3. Force reconciler sweep:
     ```bash
     sudo systemctl restart nexus-engine
     ```

### Scenario B: High 5xx Error Rate / Pod Unschedulable
* **Trigger**: K3s worker node running out of CPU or memory.
* **Diagnosis**:
  ```bash
  kubectl get nodes
  kubectl describe nodes
  kubectl get pods -n nexus-challenges | grep -v Running
  ```
* **Remediation**:
  * If pods are stuck in `Pending` due to resource exhaustion, adjust per-challenge limits or prune orphaned pods:
    ```bash
    kubectl delete pods -n nexus-challenges --field-selector=status.phase=Failed
    ```

### Scenario C: Node Agent Fails / Unreachable
* **Impact**: New student isolation grants fail; **existing student sessions and VPN routing remain functional in the Linux kernel**.
* **Action**:
  ```bash
  sudo systemctl restart nexus-node-agent
  sudo journalctl -u nexus-node-agent -n 50 --no-pager
  ```
  Once restarted, the engine reconciler will automatically re-verify ipset/iptables mappings during the next 15-second cycle.

---

## 5. Rollback Procedures

If a newly deployed binary of `nexus-engine` or `nexus-node-agent` encounters regressions:

### 5.1 Rolling back `nexus-engine`
```bash
# 1. Stop current engine
sudo systemctl stop nexus-engine

# 2. Swap binary with previous release (e.g., from backup or git commit)
sudo cp /usr/local/bin/nexus-engine.prev /usr/local/bin/nexus-engine

# 3. Start engine
sudo systemctl start nexus-engine

# 4. Confirm bootstrap reconciliation
sudo journalctl -u nexus-engine -n 30 --no-pager
```

### 5.2 Rolling back `nexus-node-agent`
```bash
# 1. Stop node agent (kernel rules persist)
sudo systemctl stop nexus-node-agent

# 2. Swap binary
sudo cp /usr/local/bin/nexus-node-agent.prev /usr/local/bin/nexus-node-agent

# 3. Start node agent
sudo systemctl start nexus-node-agent
```

---

## 6. Post-Event Reporting & Pitch Artifacts

Immediately after the competition concludes, generate the official competition metrics brief:

### 6.1 Generate Executive PDF Brief & CSV
```bash
# Generate report from live event log
python3 scripts/event-report.py \
  --input /var/lib/nexus/events.jsonl \
  --pdf competition-report.pdf \
  --csv competition-report.csv \
  --title "Spring 2026 Inter-Collegiate CTF Report"
```

### 6.2 Export Telemetry via Engine API
```bash
# Export CSV directly from engine admin API
curl -s -H "Authorization: Bearer <KEY>" \
  "http://localhost:8081/api/v1/admin/telemetry/export?format=csv" \
  -o api_events_export.csv

# Export JSON
curl -s -H "Authorization: Bearer <KEY>" \
  "http://localhost:8081/api/v1/admin/telemetry/export?format=json" \
  -o api_events_export.json
```

Deliver `competition-report.pdf` to university department heads, faculty advisors, and corporate sponsors as proof of platform scale, student engagement, and infrastructure reliability.
