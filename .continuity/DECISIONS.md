# Decisions

Append-only. Newest at the bottom. Don't edit old entries; supersede them with a new one.

## D-001: Pods, not microVMs
- Date: 2026-11-22
- Chose: k3s pods for every session, including the browser AttackBox.
- Over: Firecracker microVMs with a per-VM copy of a 1-2 GB rootfs and TAP devices, which the prototype used.
- Because: the rootfs copy is per-session cost that pods do not pay, pods start in a couple of seconds once the image is cached, and the networking model is the same one the rest of the platform already uses.
- Revisit if: we need a kernel boundary between participants rather than a container one.

## D-002: Monitoring is host-level, scraped through a loopback proxy
- Date: 2026-11-22
- Chose: Prometheus, Grafana and kube-state-metrics run under nerdctl on the host network, alongside Redis and the registry. Cluster metrics are scraped through `kubectl proxy` on `127.0.0.1:8001` using the admin kubeconfig.
- Over: an in-cluster Prometheus with a ServiceAccount, RBAC, NodePort or kubelet TLS.
- Because: one host, one operator, and an event deadline. The in-cluster path needs three pieces of cluster security configuration to do what one proxy does, and none of it survives the host dying anyway.
- Revisit if: there is a second node, or Prometheus needs to outlive a k3s restart.

## D-003: WireGuard stays, and pods are never exposed directly
- Date: 2026-11-22
- Chose: keep the existing WireGuard design, hardened, and never hand out a public address per pod.
- Over: renting public IPv4 per session, or putting an HTTP reverse proxy in front of challenges.
- Because: a few hundred public addresses is not affordable or defensible, and directly exposing a participant-controlled container is a liability. The VPN model also matches what participants already know.
- Revisit if: a challenge genuinely needs to be reachable from outside the VPN.

## D-004: No HA for the December event
- Date: 2026-11-22
- Chose: keep the single-host design and answer resilience with quotas, a session cap, pre-pulled images, a cold standby and a rehearsed restore.
- Over: making the VPN endpoint, node agent, pod routing and Redis highly available.
- Because: all four are single-host by construction, so HA is a rewrite rather than a change, and a rehearsed 30-minute restore is a promise that can actually be kept.
- Revisit if: the event moves to multi-node, which also unblocks D-002.

## D-005: The node agent's handshake window is the definition of "connected"
- Date: 2026-11-22
- Chose: report a participant as connected only when the node agent saw a handshake inside its 180-second window.
- Over: treating "the peer appears in the WireGuard dump" as connected, which is what the engine does now.
- Because: a peer that exists but never handshakes is precisely the failure participants report, and hiding it turns a diagnosable problem into a support queue.
- Revisit if: the handshake window proves too tight for real client sleep/wake behaviour.
