# Progress

_Rewrite this file each session. Don't let it grow forever._

## Last known state

2026-11-27: `feat/attackbox-env-session` (**MR !8** open on GitLab) now includes:
- Env-pod sessions (`POST /api/v1/sessions/env`, `/artifacts` read-only hostPath mounts, `NEXUS_WS_PROXY_ADDR` web-app mode, `nexus-role=env`, `sleep infinity` keep-alive, `deploy/network-policy-prod.yaml` `/22` + `nexus-env-internet`).
- Capacity guards (D-025): `deploy/resource-quota.yaml` (`LimitRange` + `ResourceQuota` 250 pods in `nexus-challenges`) and `NEXUS_MAX_SESSIONS` global active session cap in `nexus-engine` (`checkSessionLimits` returning `429 GLOBAL_SESSION_LIMIT_REACHED`).
- VPN correctness (D-025): `VpnStatus.connected` uses `peer.Connected` (180s handshake window), cached `Config` hits re-ensure the WireGuard peer, failed provisioning releases newly claimed IPs via `Store.ReleaseVPNIP`, and `Regenerate` pins the user's existing VPN IP.
- Slim Kali Dockerfile (`deploy/kali-slim/Dockerfile`, `sleep infinity`, no systemd/sshd/code-server).

## Current task

- Goal: finish December P0 hardening in `nexus-framework` and verify end-to-end AttackBox flow with `rlabz-ctf` on the Fedora host.
- In scope (files/dirs): `nexus-engine/internal/config/config.go`, `nexus-engine/internal/api/sessions.go`, `nexus-engine/internal/api/vpn.go`, `nexus-engine/internal/state/redis.go`, `nexus-engine/internal/api/env_sessions_test.go`, `nexus-engine/internal/state/redis_test.go`, `deploy/resource-quota.yaml`, `deploy/kali-slim/Dockerfile`, `.continuity/`.
- Out of scope: telemetry T1 MR (`feat/telemetry-t1-scrape-stack`), multi-tenancy/HA.
- Done when: `go build ./...`, `go vet ./...`, `go test ./...` pass, and live browser AttackBox + `deploy/resource-quota.yaml` + slim Kali image are verified against `nexus-vm`.

## Last session

- Implemented D-025 capacity guards + VPN lifecycle fixes + `deploy/kali-slim/Dockerfile`.
- Verified: `go build ./...`, `go vet ./...`, `go test ./...` pass (`internal/api`, `internal/k8s`, `internal/state`); `deploy/resource-quota.yaml` validated with `yaml.safe_load_all` (`LimitRange`, `ResourceQuota`).
- Not yet verified live: building `deploy/kali-slim/Dockerfile` on `nexus-vm`, applying `deploy/resource-quota.yaml` and `deploy/network-policy-prod.yaml` on `nexus-vm`, and running the full browser AttackBox test from `rlabz-ctf` on the Fedora host.

## Next up

1. Build `deploy/kali-slim/Dockerfile` on `nexus-vm` (or local registry) and apply `deploy/resource-quota.yaml` + `deploy/network-policy-prod.yaml`.
2. Reopen/update `rlabz-ctf` PR for `fix/attackbox-terminal-proxy`, run the full browser AttackBox test from Fedora host → `nexus-vm`, and merge both `MR !8` and `rlabz-ctf` PR.
3. Open and merge `feat/telemetry-t1-scrape-stack` (`31b5104`) after core product flows are verified.

## Private share (not in git)

`~/nexus-private/` is one directory, mounted or symlinked on every machine so
the path stays stable. It holds:

- `plan/nexus-euphoria-event-plan.md` — the December event strategy, the nine
  locked decisions and the P0-P3 schedule. This is the document to read before
  touching event code.
- `plan/nexus-telemetry-implementation.md` — the T1-T7 telemetry task tracker.
- `db/` — the web app's schema and full dump (lives here because that repo has
  no migrations).

If the directory is missing, say so; do not reconstruct the plan from guesswork.

## Open questions / blockers

- The default NetworkPolicy on `nexus-vm` (the permissive
  `{"securityContext":{"namespace": "any"}}` one) blocks a strict connect test: it
  sits in `nexus-challenges` and allows everything. A real policy test needs the
  restricted policies applied or the default policy deleted.
- `ClaimNextAvailableVPNIP` allocates 1015 addresses (`10.8.0.2`–`10.8.3.254`),
  while the docs claim 1022.
- Real event host specs are unknown, so the capacity math and the 3x load test
  have no target yet.
- `nexus-vm` root-owned `:8081` `nexus-engine` service needs sudo to upgrade to `feat/attackbox-env-session`.

## Noted, unresolved on the VM

- `nexus-vm` git repo is at branch `feat/terminal-exec-gateway`
  (`a3a30a8 feat(engine): add interactive terminal PTY websocket and $HOME file
  sync endpoints`), detached or behind `main`; use it as the file-transfer
  baseline and note the drift when upgrading.
- The k3s admin kubeconfig the backlog mentions is not present on `nexus-vm`;
  unprivileged user uses `KUBECONFIG=~/.kube/nexus`.

## Don't redo

- Do not bring back Firecracker, TAP devices or a per-VM rootfs copy. Pods
  start in seconds and share the network model.
- Do not make Prometheus a cluster Service or grant it RBAC. It scrapes through
  a loopback `kubectl proxy` with the admin kubeconfig, by design.
- Do not add a second orchestrator or a public IPv4 per pod.
- Do not treat `VpnStatus.connected` as "peer exists"; that was the bug.
- Do not claim HA. The promise is quotas, a cold standby and a rehearsed
  30-minute restore.
