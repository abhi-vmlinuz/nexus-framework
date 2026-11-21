# Protected paths

Levels:
- frozen: do not edit. Stop and ask the user. The pre-commit check blocks the commit.
- stable: editable only with a DECISIONS.md entry in the same commit.
Everything not listed is free to change inside the task's scope fence.

`.continuity/PROTECTED.md`, `.continuity/VISION.md` and `.continuity/continuity.sh` are human-owned and protected automatically; do not list them here.

frozen: nexus-engine/contracts/nexus/nodeagent/v1/nodeagent.proto | Wire contract shared by the Go engine and the Rust agent. Changing it breaks any machine whose agent was not rebuilt, and sessions fail with a version mismatch.
stable: deploy/setup.sh | The provisioning path for every install. Must stay idempotent and preserve existing state (env files, peers, volumes).
stable: nexus-engine/internal/state/redis.go | Owns VPN IP allocation and session state invariants; a subtle change leaks addresses or strands peers.
stable: nexus-node-agent/src/adapters/wireguard.rs | Mutates host networking. Wrong here means every participant loses connectivity.
stable: deploy/monitoring/** | Scrape config, recording rules and dashboard. Changing targets silently blinds event telemetry.
