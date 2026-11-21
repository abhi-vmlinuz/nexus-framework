# Vision

## What this is

A self-hosted execution plane for hacking events, running on bare-metal Linux.
It gives every participant their own ephemeral challenge environment, reachable
by an address on a WireGuard network, and cleans it up afterwards. One install
per college, on hardware the college owns.

Four components:

- **nexus-engine** (Go/Gin, `:8081`) — REST API, session lifecycle, pod
  creation in the `nexus-challenges` namespace, Redis state, gRPC calls to the
  node agent.
- **nexus-node-agent** (Rust/Tonic, `127.0.0.1:50051`, `CAP_NET_ADMIN`) — the
  only privileged piece: WireGuard peers, ipset, iptables.
- **nexus-cli** (Go, Cobra + Bubbletea) — operator CLI and live TUI.
- **nexus-installer** (Go, Bubbletea) — interactive install wizard.

## Why it exists

Shared boxes and static files do not make a CTF. Participants need a real
machine each, with isolated networking, that can be created in seconds and
destroyed when the session ends. Doing that per participant means the
orchestration, the network isolation and the VPN provisioning all have to be
one system, and it has to work on a single machine a college can afford.

## Non-goals

- No multi-tenancy and no central control plane. One install, one college.
- No public IPv4 per pod. Participants reach pods over WireGuard; that is the
  ThreatZone/HTB model and it is the only one that fits the address budget.
- No hosted SaaS. This is software a college runs.
- No second orchestrator. Firecracker/TAP-device microVMs were prototyped and
  dropped; pods replace them.
- No HTTP reverse proxy for challenges, and no Tailscale/Headscale. Both were
  considered and rejected for the December event.
- Not the web product. Identity, content, scoring and the browser UI live in
  `rlabz-ctf`; this repo does not grow a frontend for participants.

## Principles

- **One host is the unit of deployment.** k3s, Redis, registry and the VPN
  endpoint all live on the same machine. Do not introduce a dependency that
  only works multi-node.
- **The node agent is the only privileged component.** Anything touching
  iptables, ipset or `wg0` goes through its gRPC contract, and every mutating
  RPC is idempotent.
- **Provisioning must be idempotent.** `setup.sh` and the installer must be
  safe to re-run; a half-finished install has to be recoverable by running it
  again.
- **No HA pretence.** The honest resilience story is a cold standby, a
  rehearsed restore, quotas and a session cap, not a claim of high
  availability.
- **Measure before trusting.** The monitoring stack exists because capacity
  claims have to be backed by a load test.

## Architecture in brief

```
CLI / web app ──► nexus-engine :8081 ──► Redis (session state)
                        │
                        ├──► k3s (pods in nexus-challenges)
                        └──► nexus-node-agent :50051 (wg0, ipset, iptables)
```

- `nexus-engine/internal/api/` — Gin handlers; routes in `routes.go`.
- `nexus-engine/internal/controller/` — reconciliation loop and orphan cleanup.
- `nexus-engine/internal/state/` — Redis state, including VPN IP allocation.
- `nexus-engine/contracts/` — protobuf shared with the Rust agent.
- `deploy/` — `setup.sh`, monitoring stack, manifests.
- Deeper docs: `docs/architecture.md`, `docs/api.md`, `docs/runbook.md`,
  `docs/quickstart.md`.
