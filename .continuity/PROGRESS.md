# Progress

_Rewrite this file each session. Don't let it grow forever._

## Last known state

2026-11-27: `main` has the AttackBox engine primitives (`87cccdb`: `nexus-role`
label, hostPath volumes). Branch `feat/attackbox-env-session` (`61acc34`, **MR !8
open** on GitLab, unmerged) adds:
- `POST /api/v1/sessions/env` + `/artifacts` read-only hostPath mounts by ID (D-008, D-009)
- `deploy/network-policy-prod.yaml` `/22` ingress, default-deny challenge egress, and `nexus-env-internet` (D-010)
- Web-app WS proxy mode (`NEXUS_WS_PROXY_ADDR`, D-022)
- Session `Role` (`"challenge"|"env"`) on `state.Session` (D-023)
- `sleep infinity` keep-alive for env pods (D-024)

Live `nexus-vm` connect test ran on scratch engine (`:8082`/`:8084`, `mode=dev`):
env pod spawned with `nexus-role=env` and `sleep infinity`, and WS terminal shell
echo succeeded. Full browser AttackBox test is still pending.
Telemetry T1 remains unmerged on `feat/telemetry-t1-scrape-stack` (`31b5104`, no MR yet).

## Current task

- Goal: sync continuity state across `nexus-framework` and `rlabz-ctf`, then align on the next December P0/AttackBox milestone.
- In scope (files/dirs): `.continuity/PROGRESS.md`, `.gitignore` (local `plans*` sync).
- Out of scope: feature edits until plan/agenda is confirmed with user.
- Done when: `PROGRESS.md` matches `HEAD` (`61acc34`) and MR `!8` state, and `go test ./...` passes.

## Last session

- Verified repo vs. `PROGRESS.md`: `go test ./...` passes for `internal/api`,
  `internal/k8s`, `internal/state` (`cmd`, `config`, `controller`, `nodeagent`,
  `registry`, `telemetry` have no test files).
- Synced `PROGRESS.md` with commits `85cbf9b`–`61acc34` and open GitLab MR `!8`.
- Untracked local files left untouched: `analysis.zip`, `docs/nexus-telemetry-plan.md`, `docs/nexus-telemetry-plan.tex`.

## Next up

1. Finish browser-level AttackBox smoke test against `rlabz-ctf` (`fix/attackbox-terminal-proxy`) and merge **MR !8** (`feat/attackbox-env-session` → `main`).
2. Open and merge MR for `feat/telemetry-t1-scrape-stack` (`31b5104`).
3. Remaining December P0s from `~/nexus-private/plan/nexus-euphoria-event-plan.md`:
   - Capacity guards: `LimitRange` + `ResourceQuota` in `nexus-challenges` + global session cap.
   - VPN correctness: `VpnStatus.connected` 180s handshake window, re-ensure peer on cache hit, release IP on provision failure, pin IP across `Regenerate`.
   - Slim Kali image for env pods (`NEXUS_ENV_IMAGE`).

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
- `nexus-vm` has scratch engine instances on `:8082`/`:8084` (`mode=dev`); root-owned
  `:8081` service is unchanged (needs sudo to upgrade).

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
