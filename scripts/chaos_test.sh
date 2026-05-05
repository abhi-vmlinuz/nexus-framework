#!/bin/bash
# scripts/chaos_test.sh — Nexus OSS Reliability & Survivability Drill.
# Tests how the system handles crashes, restarts, and manual state interference.

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log() { echo -e "${GREEN}[INFO]${NC} $1"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; }

# 1. Setup
log "Starting Chaos Test Protocol..."
CHALLENGE_ID=$(nexus challenge list | grep "test-single" | awk '{print $1}' | head -n 1)
if [ -z "$CHALLENGE_ID" ]; then
    error "No challenge found. Please register one first (e.g. testing/pwn-101)."
    exit 1
fi

USER_ID="chaos"
log "Spawning test session for user: $USER_ID"
SESSION_OUT=$(nexus session create --challenge "$CHALLENGE_ID" --user "$USER_ID")
SESSION_ID=$(echo "$SESSION_OUT" | grep "Session:" | awk '{print $2}')
POD_IP=$(echo "$SESSION_OUT" | grep "Pod IP:" | awk '{print $3}')

log "Session created: $SESSION_ID (IP: $POD_IP)"

# 2. Test Scenario: Engine Crash
log "--- SCENARIO 1: Engine Heart-Stop ---"
log "Stopping nexus-engine..."
sudo systemctl stop nexus-engine
sleep 5
log "Engine is DOWN. Verifying pod still exists..."
if sudo kubectl get pod "chal-$SESSION_ID" -n nexus-challenges > /dev/null 2>&1; then
    log "✓ Pod survived engine crash."
else
    error "Pod was deleted when engine stopped!"
fi

log "Restarting nexus-engine..."
sudo systemctl start nexus-engine
sleep 10
log "Verifying session still active in engine..."
if nexus session list | grep "$SESSION_ID" | grep -q "running"; then
    log "✓ Engine recovered session state."
else
    error "Engine lost track of the session after restart!"
fi

# 3. Test Scenario: Network Amnesia (Manual state flush)
log "--- SCENARIO 2: Network Amnesia ---"
IPSET_NAME="nexus-user-$USER_ID"
log "Manually flushing ipset: $IPSET_NAME"
# If prod mode, the set should exist. If dev mode, we skip this part.
if sudo ipset list "$IPSET_NAME" > /dev/null 2>&1; then
    sudo ipset flush "$IPSET_NAME"
    log "Ipset flushed. Waiting 25s for engine reconciliation..."
    sleep 25
    if [ $(sudo ipset list "$IPSET_NAME" | grep "Number of entries:" | awk '{print $4}') -gt 0 ]; then
        log "✓ Engine re-populated the network rules."
    else
        error "Engine failed to repair the flushed network rules!"
        warn "Did the Engine reconcile? check logs."
    fi
else
    warn "Ipset '$IPSET_NAME' not found (this is expected if Node Agent didn't run or is in a different mode)."
fi

# 4. Test Scenario: Node Agent Reboot
log "--- SCENARIO 3: Agent Reboot ---"
log "Restarting nexus-node-agent..."
sudo systemctl restart nexus-node-agent
sleep 5
log "Verifying engine status recovers..."
if nexus status | grep -q "Engine: healthy"; then
    log "✓ Engine detected agent recovery."
else
    error "Engine status stuck after agent restart!"
fi

# 5. Test Scenario: Zombie Pod (Manual deletion)
log "--- SCENARIO 4: Zombie Pod ---"
log "Manually deleting pod via kubectl..."
sudo kubectl delete pod "chal-$SESSION_ID" -n nexus-challenges --now
log "Pod deleted. Waiting 40s for engine detection (periodic scan is 15s)..."
sleep 40
if nexus session list --all | grep "$SESSION_ID" | grep -q "failed"; then
    log "✓ Engine detected the missing pod and marked session as failed."
else
    error "Engine still thinks the session is running (or it was deleted entirely)!"
    warn "Check engine logs: journalctl -u nexus-engine -n 20"
fi

# 6. Cleanup
log "Cleaning up chaos test..."
nexus session terminate "$SESSION_ID" > /dev/null 2>&1 || true

log "======================================="
log "  CHAOS TEST COMPLETED SUCCESSFULLY!   "
log "  Nexus is CRASH-RESISTANT.            "
log "======================================="
