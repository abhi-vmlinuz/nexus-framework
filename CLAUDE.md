# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Nexus Framework is a self-hosted, bare-metal CTF challenge orchestration platform. It deploys, isolates, and manages ephemeral hacking challenges on bare-metal Linux using K3s, nerdctl, WireGuard, and Redis. The platform has four components:

- **nexus-engine** (Go/Gin) — Central REST API server on `:8081`. Manages challenge session lifecycle via K3s pods, stores state in Redis, communicates with node agent over gRPC.
- **nexus-cli** (Go/Cobra+Bubbletea) — Operator CLI and live TUI dashboard.
- **nexus-node-agent** (Rust/Tonic) — Privileged daemon (`CAP_NET_ADMIN`) managing ipset, iptables, and WireGuard peers via gRPC on `:50051`.
- **nexus-installer** (Go/Bubbletea) — Interactive TUI installer wizard (9-phase bootstrap).

## Build Commands

### nexus-engine (has Makefile)
```bash
cd nexus-engine
make build          # go build -o nexus-engine ./cmd/
make test           # go test ./... -v -count=1 -race
make lint           # go vet + optional golangci-lint
make run            # NEXUS_MODE=dev go run ./cmd/
make proto          # buf generate (requires buf, protoc-gen-go, protoc-gen-go-grpc)
make proto-tools    # install proto toolchain
make clean          # remove binary and generated proto stubs
```

### Other components
```bash
cd nexus-cli && go build -o nexus .
cd nexus-node-agent && cargo build --release
cd nexus-installer && go build -o nexus-installer *.go
```

### Single test
```bash
cd nexus-engine && go test ./internal/api/ -v -run TestHealthEndpoint
```

## Architecture

```
CLI/External --> [nexus-engine :8081 REST API] --> [Redis (state)]
                       |                                |
                       v                                v
              [K3s/Kubernetes]                   [nexus-node-agent :50051 gRPC]
           (pod lifecycle mgmt)              (ipset, iptables, WireGuard)
```

### Data flow
1. CLI or external platform (e.g., CTFd) sends REST requests to nexus-engine.
2. Engine creates/destroys K3s pods in the `nexus-challenges` namespace.
3. Engine calls node-agent gRPC to configure network isolation (WireGuard peers, ipset/iptables rules).
4. Session state is persisted in Redis with TTL-based cleanup.

### Key internal packages (nexus-engine)
- `internal/api/` — Gin handlers and routes. Dependency injection via `Deps` struct. Routes registered in `routes.go`.
- `internal/controller/` — Reconciliation loop: worker pool (default 5 workers), periodic scan with jitter, retry with backoff, orphaned pod cleanup.
- `internal/state/` — Redis state management (challenges, sessions, reconciliation versioning).
- `internal/k8s/` — Kubernetes client for K3s pod operations.
- `internal/nodeagent/` — gRPC client stubs for node agent communication.
- `internal/config/` — Environment-variable-based config with thread-safe hot-reload for "soft config" (CPU, memory, TTLs).
- `internal/registry/` — Container registry interaction and compose builder.
- `contracts/` — Protobuf definitions (compiled via `buf` for Go, `tonic-build` for Rust).

### Dev vs Prod modes (`NEXUS_MODE`)
- **dev** (default): WireGuard disabled, insecure gRPC, relaxed isolation.
- **prod**: WireGuard on `wg0` (10.8.0.1/24), ipset/iptables enforced per session, mTLS for gRPC.

## Configuration

All config is environment-variable driven. Key variables: `NEXUS_MODE`, `NEXUS_PORT`, `NEXUS_REDIS_URL`, `NEXUS_REGISTRY_URL`, `NEXUS_NODE_AGENT_ADDR`, `NEXUS_K3S_NAMESPACE`, `NEXUS_ENGINE_URL`.

Engine config loads from `/etc/nexus/engine.env` in production. CLI config at `~/.config/nexus/config.json`.

"Soft config" parameters (`challenge.cpu`, `challenge.memory`, `session.ttl`, `session.max_per_user`) can be hot-reloaded without restarting the engine.

## Protobuf / gRPC Contract

The proto file is at `nexus-engine/contracts/nexus/nodeagent/v1/nodeagent.proto`. It defines 8 RPCs (Health, EnsureUserIsolation, RevokeUserIsolation, GrantPodAccess, RevokePodAccess, EnsureWireGuardPeer, RevokeWireGuardPeer, GetWireGuardStatus). All mutating RPCs are idempotent.

- Go stubs: generated into `nexus-engine/gen/` via `buf generate`.
- Rust stubs: compiled at build time via `tonic-build` in `nexus-node-agent/build.rs`.

After modifying the proto file, regenerate with `make proto` in nexus-engine. The Rust side regenerates automatically on `cargo build`.

## Testing

- Go tests use `testing` + `testify/assert` + `testify/require`.
- Redis integration tests (`internal/state/redis_test.go`) require a running Redis instance and are skipped otherwise.
- HTTP handler tests (`internal/api/handlers_test.go`) use `httptest` with mock dependencies.
- No Rust tests currently exist in nexus-node-agent.

## CI/CD

GitLab CI (`.gitlab-ci.yml`): stages are `setup` -> `build` -> `deploy`. Builds all 4 components for amd64+arm64. Go components use `CGO_ENABLED=0` static builds; Rust uses `cargo zigbuild` with MUSL targets. Releases are published to GitLab Generic Package Registry on version tags.

## Cross-Compilation

- Go: `CGO_ENABLED=0 GOOS=linux GOARCH={amd64,arm64}` (static binaries)
- Rust: `cargo zigbuild --release --target {x86_64-unknown-linux-musl,aarch64-unknown-linux-musl}` (fully static MUSL)

## Version Injection

Go: `-ldflags "-X main.Version=${VERSION}"` at build time. Rust: `env!("CARGO_PKG_VERSION")` from Cargo.toml.
