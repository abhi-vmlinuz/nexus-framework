# Auto state (machine-written; do not edit)
- host: fedora
- time: 2026-11-23T09:50:33Z
- branch: feat/attackbox-env-session
- head: 85cbf9b feat(networkpolicy): env pods get internet, challenges default-deny
## Changed files (staged or unstaged)
 M .gitignore
M  nexus-engine/cmd/main.go
M  nexus-engine/go.mod
M  nexus-engine/go.sum
M  nexus-engine/internal/api/env_sessions_test.go
M  nexus-engine/internal/api/routes.go
M  nexus-engine/internal/api/terminal.go
A  nexus-engine/internal/api/ws_backend.go
A  nexus-engine/internal/api/ws_backend_test.go
M  nexus-engine/internal/config/config.go
M  nexus-engine/internal/k8s/client.go
M  nexus-engine/internal/k8s/client_test.go
A  nexus-engine/internal/k8s/ws_proxy.go
?? analysis.zip
?? docs/nexus-telemetry-plan.md
?? docs/nexus-telemetry-plan.tex
## Recent commits
85cbf9b feat(networkpolicy): env pods get internet, challenges default-deny
4410f55 fix(networkpolicy): /22 ingress and DNS/same-pod egress allow
993649d docs(continuity): VM install and network-policy reality notes
4aeece0 feat(engine): environment sessions with an artefact mount
93daf12 Merge branch 'feat/attackbox-engine-foundations' into 'main'
87cccdb feat(engine): nexus-role label and hostPath volumes for env pods
c813a71 Merge branch 'chore/refresh-progress' into 'main'
62a4bc2 docs(continuity): refresh progress after the onboarding merge
