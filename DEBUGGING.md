# Nexus Framework Debugging Guide

This document covers common issues encountered during the setup and operation of the Nexus Engine, Node Agent, and CLI.

## 1. Engine Connectivity Issues (404/Not Found)

If the CLI TUI shows `404 page not found` for specific tabs (Cluster, Registry, etc.):
- **Cause**: The `nexus-engine` service might be running an outdated binary.
- **Solution**: Rebuild the engine from source and restart the service.
  ```bash
  cd nexus-engine
  go build -o nexus-engine ./cmd/
  sudo mv nexus-engine /usr/local/bin/nexus-engine
  sudo systemctl restart nexus-engine
  ```

## 2. Service Startup Failures (status=203/EXEC)

If `systemctl status nexus-engine` shows `failed (Result: exit-code)` with `status=203/EXEC` and the logs mention `Permission denied`:
- **Cause**: On systems like Fedora, SELinux prevents systemd from executing binaries that inherit security labels from the user's home directory (e.g., `user_home_t`).
- **Solution**: Relabel the binary to the correct security context.
  ```bash
  sudo restorecon -v /usr/local/bin/nexus-engine
  sudo systemctl restart nexus-engine
  ```

## 3. Redis Connectivity (IPv6 Loopback)

If the Engine fails to connect to Redis with "connection refused":
- **Cause**: Some distributions prioritize `[::1]` (IPv6) for `localhost`, but Redis may only be listening on `127.0.0.1` (IPv4).
- **Solution**: Explicitly use `127.0.0.1` in your `nexus-engine.service` or configuration files.

## 4. TUI Loading Hang (Infinite Loading Screen)

If a tab in the TUI shows "loading..." indefinitely but the API returns 200 OK:
- **Cause**: The API might be returning `null` for a collection instead of an empty slice `[]`, causing the TUI rendering logic to wait indefinitely.
- **Solution**: Check the API logs and ensure the backend initializes response slices: `var result = []Type{}`.

## 5. Node Agent RPC Errors

If the Metrics tab shows "Node Agent RPC errors":
- **Cause**: The engine cannot reach the `nexus-node-agent` on port `50051`.
- **Solution**: 
  - Ensure the agent is running: `sudo systemctl status nexus-node-agent`
  - Verify the agent address in the engine service is set to `127.0.0.1:50051`.

## 6. Docker Container Runtime Crashes (time namespace errors)

If you run `docker run` or `docker compose` and get an error like:
`OCI runtime create failed: runc create failed: namespace {"time" ""} does not exist`
- **Cause**: Newer Docker CE versions (26.0+) enable private time namespaces by default. If your host kernel does not support time namespaces (common on some cloud VM kernels), the container startup fails.
- **Solution**: Disable time namespaces in your Docker daemon configuration:
  1. Edit `/etc/docker/daemon.json` and add:
     ```json
     {
       "features": {
         "time-namespaces": false
       }
     }
     ```
  2. Restart the Docker service:
     ```bash
     sudo systemctl restart docker
     ```

## 7. WireGuard VPN — No Handshake (ping 10.8.0.1 fails, 100% packet loss)

**Symptoms:**
- `ping 10.8.0.1` → 100% packet loss after connecting VPN config
- `sudo wg show wg0 latest-handshakes` shows `0` for all peers
- `sudo wg show wg0 endpoints` shows `(none)` for all peers

**Cause:** The AWS/GCP/cloud Security Group is blocking **UDP port 51820** inbound. WireGuard performs its handshake over UDP 51820. If the port is closed, the client's packets never arrive and the tunnel never establishes.

**Fix:** Add an inbound rule to your security group:

| Type | Protocol | Port | Source |
|------|----------|------|--------|
| Custom UDP | UDP | 51820 | `0.0.0.0/0` |

This is a **permanent** requirement — not a one-time fix. Every time you launch an instance or recreate the security group, this rule must be present.

**Verify after adding the rule:**
```bash
# On the client — reconnect
sudo wg-quick down admin && sudo wg-quick up admin
ping 10.8.0.1   # should respond within 5 seconds

# On the server — confirm handshake established
sudo wg show wg0 latest-handshakes   # should show a non-zero timestamp
```

---

## 8. WireGuard VPN — Internet Breaks While Connected

**Symptoms:**
- `ping google.com` fails with `Temporary failure in name resolution` while VPN is connected
- Disconnecting VPN restores internet

**Cause:** Old versions of the generated `.conf` file contained `DNS = 1.1.1.1`. This caused `wg-quick` to call `resolvconf` and override the system's DNS resolver. Since Nexus uses a **split-tunnel** config (`AllowedIPs = 10.8.0.0/24, 10.42.0.0/16`), DNS traffic to `1.1.1.1` doesn't route through the tunnel, breaking name resolution.

**Fix:** The `DNS = 1.1.1.1` line has been removed from the VPN config generator in `nexus-engine/internal/api/vpn.go`. Re-download your `.conf` from the CTF platform to get the fixed version.

If you have an old config file, simply remove the `DNS = ...` line from the `[Interface]` section manually.

---

## 9. WireGuard Peer Registration Fails on Ubuntu (AppArmor blocks wg syncconf)

**Symptoms:**
- `GET /api/v1/vpn/config` returns HTTP 500
- Node agent logs show: `wg syncconf wg0 /tmp/nexus-wg0-*.conf: fopen: Permission denied`
- Running `sudo wg syncconf wg0 /tmp/test.conf` manually also fails with EACCES

**Cause:** Ubuntu's AppArmor profile for the `wg` binary restricts file access to specific paths. It blocks `wg syncconf` and `wg setconf` from reading configuration files in `/tmp` (and other paths outside `/etc/wireguard/`). This is confirmed via:
```bash
sudo strace -e trace=openat wg syncconf wg0 /tmp/test.conf
# openat(AT_FDCWD, "/tmp/test.conf", O_RDONLY) = -1 EACCES (Permission denied)
sudo aa-status | grep wg   # shows wg and wg-quick have AppArmor profiles
```

**Fix (already applied in node-agent v0.1.1+):** The `reload_wireguard()` function has been replaced with a direct `wg set wg0 peer <pubkey> allowed-ips <ip>/32` call, which is AppArmor-safe (no file I/O). The `wg0.conf` file is still written for boot-time persistence. No manual action needed if running the current binary.

If you see this on an older binary, rebuild and redeploy:
```bash
cd nexus-node-agent
cargo build --release
sudo systemctl stop nexus-node-agent
sudo cp target/release/nexus-node-agent /usr/local/bin/nexus-node-agent
sudo systemctl start nexus-node-agent
```

---

## 10. Installer Fails with CNI Path / bridge Plugin Missing

**Symptoms:**
- The bootstrap installer fails during local registry setup or networking validation with the error:
  `fatal msg="failed to verify networking settings: failed to create default network: needs CNI plugin \"bridge\" to be installed in CNI_PATH (\"/opt/cni/bin\")"`

**Cause:**
`nerdctl` requires Container Network Interface (CNI) plugins (specifically `bridge`) to set up default networks. On a clean host VM where K3s or Kubernetes has not been set up yet, the `/opt/cni/bin` directory and its plugins do not exist.

**Fix:**
1. Install the standard CNI plugins package for your distribution:
   ```bash
   sudo apt-get update && sudo apt-get install -y containernetworking-plugins
   ```
2. Create the target `/opt/cni/bin/` folder and symlink the installed CNI plugins (which Ubuntu/Debian installs in `/usr/lib/cni/`):
   ```bash
   sudo mkdir -p /opt/cni/bin
   sudo ln -s /usr/lib/cni/* /opt/cni/bin/
   ```
3. Re-run the bootstrap installer script.


