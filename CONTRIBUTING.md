# Contributing to Nexus Framework

Thanks for your interest in contributing. This guide covers everything you need to get started.

## Quick Start

```bash
git clone https://github.com/abhi-vmlinuz/nexus-oss.git
cd nexus-oss
```

### Prerequisites

| Tool | Version | Purpose |
|---|---|---|
| Go | 1.21+ | Engine, CLI, Installer |
| Rust + Cargo | 1.70+ | Node Agent |
| Git | Any | Version control |
| Docker or nerdctl | Any | Testing container builds |

### Build All Components

```bash
# Engine
cd nexus-engine && go build -o nexus-engine ./cmd/ && cd ..

# CLI
cd nexus-cli && go build -o nexus . && cd ..

# Node Agent
cd nexus-node-agent && cargo build --release && cd ..

# Installer
cd nexus-installer && go build -o nexus-installer ./cmd/ && cd ..
```

### Run Tests

```bash
cd nexus-engine && go test ./... && cd ..
cd nexus-node-agent && cargo test && cd ..
```

## Project Structure

```
nexus-oss/
├── nexus-engine/        # Go (Gin) — REST API server
│   ├── cmd/             # Entrypoint
│   ├── internal/
│   │   ├── api/         # HTTP handlers
│   │   ├── config/      # Configuration loading
│   │   ├── controller/  # Session lifecycle controller
│   │   ├── k8s/         # K3s client wrapper
│   │   ├── nodeagent/   # gRPC client for node agent
│   │   ├── registry/    # Image build/push (nerdctl)
│   │   └── state/       # Redis state store
│   └── gen/             # Generated protobuf code
├── nexus-cli/           # Go (Cobra + Bubbletea) — Operator CLI
├── nexus-node-agent/    # Rust (Tonic) — Privileged network daemon
├── nexus-installer/     # Go (Bubbletea) — TUI installer
├── docs/                # Documentation
├── bootstrap.sh         # One-command installer
└── bump-version.sh      # Version management
```

## Code Style

**Go:**
- Standard `gofmt` formatting
- Run `go vet ./...` before committing
- Exported types and functions need doc comments
- Error messages lowercase, no trailing punctuation

**Rust:**
- Standard `rustfmt` formatting
- Run `cargo clippy` before committing
- Use `tracing` for logging, not `println!`

**General:**
- No emojis in code, logs, or console output
- No hardcoded IPs, credentials, or internal URLs
- Environment variables prefixed with `NEXUS_`

## Commit Convention

We use [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <description>

[optional body]
```

**Types:**
- `feat` — New feature
- `fix` — Bug fix
- `docs` — Documentation only
- `refactor` — Code change that neither fixes a bug nor adds a feature
- `chore` — Build process, tooling, dependencies
- `ci` — CI/CD changes
- `test` — Adding or fixing tests

**Scopes:**
- `engine` — nexus-engine
- `cli` — nexus-cli
- `node-agent` — nexus-node-agent
- `installer` — nexus-installer
- `api` — HTTP API changes

**Examples:**
```
feat(engine): add build logs streaming endpoint
fix(cli): prevent TUI crash on empty session list
docs(api): document challenge creation response format
chore: bump version to 0.1.2
```

## Submitting a Pull Request

1. Fork the repo and create a branch from `main`
2. Make your changes following the code style above
3. Build and test locally
4. Write a clear PR description (use the PR template)
5. Link any related issues

**PR checklist:**
- [ ] Code compiles without errors
- [ ] `go vet` / `cargo clippy` passes
- [ ] Tests pass
- [ ] Documentation updated if API changed
- [ ] Commit messages follow convention

## Reporting Bugs

Use the [Bug Report](https://github.com/abhi-vmlinuz/nexus-oss/issues/new?template=bug_report.yml) issue template. Include:

- Nexus version (`nexus --version`)
- Linux distro and version
- Steps to reproduce
- Expected vs actual behavior
- Relevant logs (`/var/log/nexus-install.log`, engine logs, etc.)

## Architecture Decisions

For significant changes, open an issue first to discuss the approach. This avoids wasted effort on PRs that don't align with the project direction.

Key architectural constraints:
- Engine is the only component that builds/pushes images (via nerdctl)
- Node agent handles all kernel-level networking (requires root)
- CLI is a thin client — all logic lives in the engine
- Redis is the single source of truth for state
- K3s is the container runtime — no Docker dependency in production

## Version Management

Versions are managed centrally via the `VERSION` file at the repo root:

```bash
# Preview what changes
./bump-version.sh --dry-run 0.1.2

# Update all components
./bump-version.sh 0.1.2

# Commit and tag
git add -A && git commit -m "chore: bump version to 0.1.2"
git tag v0.1.2
git push sync main --tags
```

## Questions?

Open a [Discussion](https://github.com/abhi-vmlinuz/nexus-oss/discussions) or join the issue tracker.
