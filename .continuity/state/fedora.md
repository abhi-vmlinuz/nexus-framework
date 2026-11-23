# Auto state (machine-written; do not edit)
- host: fedora
- time: 2026-11-23T09:54:58Z
- branch: feat/attackbox-env-session
- head: 1732e3f docs(continuity): D-022 web-app mode
## Changed files (staged or unstaged)
M  .continuity/DECISIONS.md
 M .gitignore
M  nexus-engine/internal/api/sessions.go
M  nexus-engine/internal/state/redis.go
?? analysis.zip
?? docs/nexus-telemetry-plan.md
?? docs/nexus-telemetry-plan.tex
## Recent commits
1732e3f docs(continuity): D-022 web-app mode
4cd07b4 feat(engine): web-app mode for the session terminal
85cbf9b feat(networkpolicy): env pods get internet, challenges default-deny
4410f55 fix(networkpolicy): /22 ingress and DNS/same-pod egress allow
993649d docs(continuity): VM install and network-policy reality notes
4aeece0 feat(engine): environment sessions with an artefact mount
93daf12 Merge branch 'feat/attackbox-engine-foundations' into 'main'
87cccdb feat(engine): nexus-role label and hostPath volumes for env pods
