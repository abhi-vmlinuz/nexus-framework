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

## D-006: Environment pods are ordinary sessions with a `nexus-role` label
- Date: 2026-11-23
- Chose: the browser AttackBox becomes a normal nexus session whose pod carries `nexus-role=env`; pods keep `app=nexus-challenge` so the existing selectors, TTL and cleanup keep working.
- Over: a separate namespace, a separate resource type, or a second orchestrator.
- Because: everything downstream (quota, NetworkPolicy, telemetry, TTL, terminal) already selects on `app=nexus-challenge`. One extra label distinguishes "something you attack" from "somewhere you work" without forking the session model.
- Revisit if: env pods need a different quota or lifecycle than challenge pods, at which point a namespace is cleaner than a label.

## D-007: Artefact volumes are read-only hostPaths
- Date: 2026-11-23
- Chose: `SpawnRequest.Volumes` mounts host directories into every pod container, read-only by default (`ReadWrite` is opt-in, so the zero value is safe), with absolute-path validation and `HostPathDirectory` so a missing directory fails the pod instead of silently creating one.
- Over: a PVC, an initContainer that copies from the web app, or a sidecar downloader.
- Because: one k3s node, one copy on disk, no network dependency at spawn, and a 500 MB forensic image does not get copied 200 times. A PVC needs a storage class for no benefit on a single node.
- Revisit if: nexus goes multi-node, where hostPath stops being meaningful and this becomes object storage or a node-local PV.

## D-008: Environment pods get their own endpoint, not a synthetic challenge
- Date: 2026-11-23
- Chose: `POST /api/v1/sessions/env` spawns an AttackBox with no challenge record.
- Over: creating a hidden `env` challenge row and reusing `POST /api/v1/sessions`.
- Because: the challenge store feeds the challenge list, the leaderboard and telemetry attribution. A fake row leaks into all three and has to be filtered everywhere forever. The endpoint reuses the same pod, session record, TTL, grant and telemetry path, so only the lookup is skipped.
- Revisit if: env pods need per-college variation, where a real resource type is cleaner than a second endpoint.

## D-009: Artefact mounts are named by ID and resolved inside the engine
- Date: 2026-11-23
- Chose: the request carries `artifact_challenge_id`; the engine joins it to `NEXUS_ARTIFACT_ROOT` (default `/var/lib/nexus/artifacts`), rejects anything that is not a plain identifier, and fails with 400 if the directory does not exist.
- Over: accepting a host path from the caller, and over auto-creating the directory.
- Because: a host path from the web app means a compromised or buggy caller can mount `/etc`, `/root` or a docker socket into a participant's pod. Accepting an ID keeps the trust boundary in the engine. Requiring the directory to exist turns a typo into a clear 400 instead of a pod stuck in `ContainerCreating` because hostPath type `Directory` refuses to create it.
- Revisit if: the web app needs to mount several directories per session, where a list of IDs (not paths) is the natural extension.

## D-010: Env pods get internet, challenge pods do not, by default
- Date: 2026-11-23
- Chose: a second NetworkPolicy in `deploy/network-policy-prod.yaml` selecting `nexus-role=env` with permissive egress, while the main prod policy is default-deny for everything.
- Over: giving every pod internet and discovering the attacker does not need it.
- Because: the AttackBox is a browser running arbitrary user commands and it must have internet to be useful — admin, piping tools, package installs, whatever is normal work. A challenge, by contrast, runs the code an attacker is invited to run, so internet access is the privilege the person who wrote the challenge has to ask for, not something to default to. NetworkPolicies are additive, so the env policy only widens the env pods.
- Revisit if: user feedback says people cannot do their work with a browser, at which point reconsider default-deny.

## D-022: Web-app mode relays the terminal through a second engine
- Date: 2026-11-23
- Chose: the engine, when `NEXUS_WS_PROXY_ADDR` is set, proxies `/api/v1/sessions/{id}/terminal` to another engine's terminal endpoint and injects that engine's API key itself.
- Over: a kubelet `pods/exec` or SPDY proxy (that's what `ExecStream` needs, and it bypasses the whole "who is the caller" question), or shipping the key to the browser.
- Because: a real PTY through `kubectl exec` needs the kube-apiserver creds on the connecting pod, and exec has no caller scoping beyond the user's own pod anyway — so by the time a second engine has the same API key, the marginal win is the engine-to-engine link being controllable. The relay still gives a browser something to *see* (errors, echo, compile output) and a place to put the auth check and the ownership check, which the raw stream had neither.
- Revisit if: someone builds the kubelet exec backend — it's better because it works without a second engine.
- Not the answer to the connect test: no need for `VITE_NEXUS_URL` or any node-agent change.
