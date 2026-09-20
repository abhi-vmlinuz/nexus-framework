// adapters/wireguard.rs — WireGuard peer management adapter.
// Manages /etc/wireguard/wg0.conf peer blocks and syncs runtime config via wg-quick.
// NOTE: We avoid wg-syncconf/setconf because AppArmor blocks wg from reading /tmp files.
// Instead we use `wg set wg0 peer <pubkey> allowed-ips <ip>/32` to update the live
// interface directly, and keep wg0.conf writes for boot-time persistence only.
use std::fs::{self, OpenOptions};
use std::io::Write;
use std::process::Command;
use std::sync::{LazyLock, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

use tonic::Status;
use tracing::{info, warn};

const WG_CONFIG_PATH: &str = "/etc/wireguard/wg0.conf";
const HANDSHAKE_ACTIVE_SECS: i64 = 180;

// Mutex to serialise concurrent wg0.conf file modifications.
static WG_CONF_LOCK: LazyLock<Mutex<()>> = LazyLock::new(|| Mutex::new(()));

/// Ensure a WireGuard peer exists (idempotent).
/// 1. Atomically rewrites wg0.conf via tmp+fsync+rename (persistence).
/// 2. Adds the peer to the live wg0 interface via `wg set` (no tmp file, AppArmor-safe).
pub fn ensure_peer(user_id: &str, public_key: &str, vpn_ip: &str) -> Result<(), Status> {
    // Update persistent config file first — serialised via mutex.
    {
        let _lock = WG_CONF_LOCK.lock().unwrap();
        rewrite_peer_block_atomic(user_id, public_key, vpn_ip)?;
    }
    // Add to live interface directly — avoids AppArmor restriction on tmp files.
    add_peer_to_runtime(public_key, vpn_ip)
}

/// Revoke a WireGuard peer (idempotent).
/// Removes from wg0.conf (persistence) and from the live interface.
pub fn revoke_peer(user_id: &str, public_key: &str) -> Result<(), Status> {
    {
        let _lock = WG_CONF_LOCK.lock().unwrap();
        remove_peer_block_atomic(user_id)?;
    }
    // Remove from live interface directly — no file needed.
    let out = Command::new("wg")
        .args(["set", "wg0", "peer", public_key, "remove"])
        .output()
        .map_err(|e| Status::internal(format!("wg set peer remove: {e}")))?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr).to_lowercase();
        if !stderr.contains("not found") && !stderr.contains("no peer") {
            return Err(Status::internal(format!(
                "wg remove peer failed: {}",
                String::from_utf8_lossy(&out.stderr).trim()
            )));
        }
    }
    info!(user_id = %user_id, "WireGuard peer removed from runtime");
    Ok(())
}

#[derive(Debug)]
pub struct WgStatus {
    pub interface_up: bool,
    pub total_peers: i32,
    pub active_peers: i32, // peers with handshake within HANDSHAKE_ACTIVE_SECS
    pub peers: Vec<WgPeer>,
}

#[derive(Debug)]
pub struct WgPeer {
    pub public_key: String,
    pub vpn_ip: String,
    pub endpoint: String,
    pub last_handshake_unix: i64,
    pub rx_bytes: i64,
    pub tx_bytes: i64,
    pub connected: bool,
}

/// Return WireGuard interface status and peer list.
pub fn get_status() -> Result<WgStatus, Status> {
    let out = Command::new("wg")
        .args(["show", "wg0", "dump"])
        .output()
        .map_err(|e| Status::internal(format!("wg show dump: {e}")))?;

    if !out.status.success() {
        return Ok(WgStatus {
            interface_up: false,
            total_peers: 0,
            active_peers: 0,
            peers: vec![],
        });
    }

    let output = String::from_utf8_lossy(&out.stdout);
    let now = current_unix();
    let mut peers = Vec::new();

    for (i, line) in output.lines().enumerate() {
        if i == 0 || line.trim().is_empty() {
            continue; // First line is the interface row.
        }
        let parts: Vec<&str> = line.split('\t').collect();
        if parts.len() < 8 {
            continue;
        }
        let public_key = parts[0].to_string();
        let endpoint = parts[2].to_string();
        let vpn_ip = parts[3]
            .split(',')
            .next()
            .unwrap_or_default()
            .trim_end_matches("/32")
            .to_string();
        let handshake = parts[4].parse::<i64>().unwrap_or_default();
        let rx = parts[5].parse::<i64>().unwrap_or_default();
        let tx = parts[6].parse::<i64>().unwrap_or_default();
        let connected = handshake > 0 && (now - handshake) <= HANDSHAKE_ACTIVE_SECS;

        peers.push(WgPeer {
            public_key,
            vpn_ip,
            endpoint,
            last_handshake_unix: handshake,
            rx_bytes: rx,
            tx_bytes: tx,
            connected,
        });
    }

    let active = peers.iter().filter(|p| p.connected).count() as i32;
    let total = peers.len() as i32;

    Ok(WgStatus {
        interface_up: true,
        total_peers: total,
        active_peers: active,
        peers,
    })
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

// filter_peer_block removes the 4-line block ([Peer], # User, PublicKey, AllowedIPs)
// belonging to user_id from conf text. Pure Rust, no sed.
fn filter_peer_block(conf: &str, user_id: &str) -> String {
    let safe = crate::adapters::ipset::sanitize_user_id(user_id);
    let marker = format!("# User: {safe}");
    let lines: Vec<&str> = conf.lines().collect();
    let mut out: Vec<&str> = Vec::with_capacity(lines.len());
    let mut i = 0;
    while i < lines.len() {
        if lines[i].trim() == "[Peer]"
            && i + 1 < lines.len()
            && lines[i + 1].trim() == marker
        {
            i += 4; // skip [Peer] + 3 following lines
            continue;
        }
        out.push(lines[i]);
        i += 1;
    }
    let mut s = out.join("\n");
    if !s.ends_with('\n') {
        s.push('\n');
    }
    s
}

// write_atomic writes data to path via tmp file + fsync + rename.
fn write_atomic(path: &str, data: &str) -> Result<(), Status> {
    let tmp = format!("{path}.tmp");
    let mut f = OpenOptions::new()
        .write(true)
        .create(true)
        .truncate(true)
        .open(&tmp)
        .map_err(|e| Status::internal(format!("open {tmp}: {e}")))?;
    f.write_all(data.as_bytes())
        .map_err(|e| Status::internal(format!("write {tmp}: {e}")))?;
    f.sync_all()
        .map_err(|e| Status::internal(format!("fsync {tmp}: {e}")))?;
    drop(f);
    fs::rename(&tmp, path).map_err(|e| Status::internal(format!("rename {tmp}: {e}")))?;
    Ok(())
}

fn rewrite_peer_block_atomic(user_id: &str, public_key: &str, vpn_ip: &str) -> Result<(), Status> {
    let safe = crate::adapters::ipset::sanitize_user_id(user_id);
    let conf = fs::read_to_string(WG_CONFIG_PATH)
        .map_err(|e| Status::internal(format!("read {WG_CONFIG_PATH}: {e}")))?;
    let mut filtered = filter_peer_block(&conf, &safe);
    let block = format!("\n[Peer]\n# User: {safe}\nPublicKey = {public_key}\nAllowedIPs = {vpn_ip}/32\n");
    filtered.push_str(&block);
    write_atomic(WG_CONFIG_PATH, &filtered)?;
    info!(user_id = %safe, "WireGuard peer block rewritten atomically");
    Ok(())
}

fn remove_peer_block_atomic(user_id: &str) -> Result<(), Status> {
    let safe = crate::adapters::ipset::sanitize_user_id(user_id);
    let conf = match fs::read_to_string(WG_CONFIG_PATH) {
        Ok(c) => c,
        Err(e) => {
            warn!(user_id = %safe, "read wg conf failed (treating as absent): {e}");
            return Ok(());
        }
    };
    let filtered = filter_peer_block(&conf, &safe);
    write_atomic(WG_CONFIG_PATH, &filtered)?;
    Ok(())
}


/// Add a peer to the live WireGuard interface without touching any tmp file.
/// Uses `wg set wg0 peer <pubkey> allowed-ips <ip>/32` which is AppArmor-safe.
fn add_peer_to_runtime(public_key: &str, vpn_ip: &str) -> Result<(), Status> {
    let allowed = format!("{}/32", vpn_ip);
    let out = Command::new("wg")
        .args(["set", "wg0", "peer", public_key, "allowed-ips", &allowed])
        .output()
        .map_err(|e| Status::internal(format!("wg set peer: {e}")))?;
    if !out.status.success() {
        return Err(Status::internal(format!(
            "wg set peer failed: {}",
            String::from_utf8_lossy(&out.stderr).trim()
        )));
    }
    info!(public_key = %public_key, vpn_ip = %vpn_ip, "WireGuard peer added to runtime");
    Ok(())
}


fn current_unix() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs() as i64
}
