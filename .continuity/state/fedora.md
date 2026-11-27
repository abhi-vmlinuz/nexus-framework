# Auto state (machine-written; do not edit)
- host: fedora
- time: 2026-11-27T09:50:19Z
- branch: feat/attackbox-env-session
- head: 8b57201 docs(continuity): sync PROGRESS.md with HEAD and MR !8
## Changed files (staged or unstaged)
M  .continuity/DECISIONS.md
M  .continuity/PROGRESS.md
A  deploy/kali-slim/Dockerfile
A  deploy/resource-quota.yaml
M  nexus-engine/internal/api/env_sessions_test.go
M  nexus-engine/internal/api/sessions.go
M  nexus-engine/internal/api/vpn.go
M  nexus-engine/internal/config/config.go
M  nexus-engine/internal/state/redis.go
M  nexus-engine/internal/state/redis_test.go
?? analysis.zip
?? docs/nexus-telemetry-plan.md
?? docs/nexus-telemetry-plan.tex
## Recent commits
8b57201 docs(continuity): sync PROGRESS.md with HEAD and MR !8
61acc34 docs(continuity): D-024 env pod keep-alive
f96b829 fix(engine): env pods keep the container alive
89b0511 feat(engine): session records carry a role
1732e3f docs(continuity): D-022 web-app mode
4cd07b4 feat(engine): web-app mode for the session terminal
85cbf9b feat(networkpolicy): env pods get internet, challenges default-deny
4410f55 fix(networkpolicy): /22 ingress and DNS/same-pod egress allow
