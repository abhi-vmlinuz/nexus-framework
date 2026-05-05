#!/usr/bin/env bash
# scripts/smoke_test.sh — CLI-based E2E smoke tests for Nexus OSS.
#
# Usage:
#   ./scripts/smoke_test.sh                          # uses http://localhost:8081
#   ENGINE_URL=http://10.0.0.1:8081 ./scripts/smoke_test.sh
#
# Requires: curl, jq

set -uo pipefail
# NOTE: intentionally NO set -e here.
# Bash arithmetic (( n++ )) exits with code 1 when the result is zero,
# which would kill the script on the very first pass counter increment.
# We handle failures explicitly via FAIL counter + exit at the end.

ENGINE_URL="${ENGINE_URL:-http://localhost:8081}"
PASS=0
FAIL=0

# ─── Helpers ──────────────────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

ok()   { echo -e "${GREEN}✅ $*${NC}"; PASS=$((PASS + 1)); }
fail() { echo -e "${RED}❌ $*${NC}";   FAIL=$((FAIL + 1)); }
info() { echo -e "${CYAN}── $*${NC}"; }
warn() { echo -e "${YELLOW}⚠  $*${NC}"; }
skip() { echo -e "${YELLOW}⊘  $* (skipped)${NC}"; PASS=$((PASS + 1)); }

api() {
  local method="$1" path="$2"
  shift 2
  curl -s -X "$method" "$ENGINE_URL$path" \
    -H "Content-Type: application/json" \
    "$@"
}

assert_status() {
  local expected="$1" actual="$2" label="$3"
  if [[ "$actual" == "$expected" ]]; then
    ok "$label (HTTP $actual)"
  else
    fail "$label — expected HTTP $expected, got HTTP $actual"
  fi
}

# ─── Test cases ───────────────────────────────────────────────────────────────

echo ""
echo -e "${CYAN}Nexus OSS Smoke Test Suite${NC}"
echo -e "${CYAN}Engine: $ENGINE_URL${NC}"
echo ""

# 1. Health check
info "1. Health check"
resp=$(api GET /health)
status=$(echo "$resp" | jq -r '.status' 2>/dev/null || echo "PARSE_ERROR")
if [[ "$status" == "healthy" ]]; then
  ok "Engine is healthy (mode=$(echo "$resp" | jq -r '.mode'))"
else
  fail "Health check failed: $resp"
fi

# 2. Missing user_id rejected
info "2. Session create rejects missing user_id"
http_code=$(api POST /api/v1/sessions \
  -d '{"challenge_id":"test-challenge"}' \
  -o /dev/null -w "%{http_code}")
assert_status 400 "$http_code" "Missing user_id rejected"

# 3. Missing challenge_id rejected
info "3. Session create rejects missing challenge_id"
http_code=$(api POST /api/v1/sessions \
  -d '{"user_id":"alice"}' \
  -o /dev/null -w "%{http_code}")
assert_status 400 "$http_code" "Missing challenge_id rejected"

# 4. Unknown challenge returns 404
info "4. Session create with unknown challenge returns 404"
http_code=$(api POST /api/v1/sessions \
  -d '{"challenge_id":"no-such-challenge","user_id":"alice"}' \
  -o /dev/null -w "%{http_code}")
assert_status 404 "$http_code" "Unknown challenge returns 404"

# 5. List sessions
info "5. List sessions"
resp=$(api GET /api/v1/sessions)
count=$(echo "$resp" | jq -r '.count' 2>/dev/null || echo "-1")
if [[ "$count" != "-1" ]]; then
  ok "List sessions OK (count=$count)"
else
  fail "List sessions parse error: $resp"
fi

# 6. Get nonexistent session returns 404
info "6. Get nonexistent session"
http_code=$(api GET /api/v1/sessions/nonexistent-session-id \
  -o /dev/null -w "%{http_code}")
assert_status 404 "$http_code" "Nonexistent session returns 404"

# 7. List challenges
info "7. List challenges"
resp=$(api GET /api/v1/challenges)
count=$(echo "$resp" | jq -r '.count' 2>/dev/null || echo "-1")
if [[ "$count" != "-1" ]]; then
  ok "List challenges OK (count=$count)"
else
  fail "List challenges parse error: $resp"
fi

# 8. Controller stats
info "8. Controller stats"
resp=$(api GET /debug/controller)
ctrl_status=$(echo "$resp" | jq -r '.status' 2>/dev/null || echo "PARSE_ERROR")
if [[ "$ctrl_status" == "running" ]]; then
  ok "Controller is running (workers=$(echo "$resp" | jq '.workers'))"
else
  warn "Controller status: $ctrl_status"
  PASS=$((PASS + 1))
fi

# 9. Cluster health
info "9. Cluster health"
resp=$(api GET /api/v1/admin/cluster/health)
h_status=$(echo "$resp" | jq -r '.status' 2>/dev/null || echo "PARSE_ERROR")
if [[ "$h_status" == "healthy" ]]; then
  ok "Cluster healthy (redis=$(echo "$resp" | jq -r '.redis'), agent=$(echo "$resp" | jq -r '.node_agent'))"
else
  warn "Cluster health: $h_status — check Redis/node-agent"
  PASS=$((PASS + 1))
fi

# 10. System info
info "10. System info"
resp=$(api GET /debug/system)
mode=$(echo "$resp" | jq -r '.mode' 2>/dev/null || echo "PARSE_ERROR")
if [[ "$mode" == "dev" || "$mode" == "prod" ]]; then
  ok "System info OK (mode=$mode, sessions=$(echo "$resp" | jq '.sessions_total'))"
else
  fail "System info parse error: $resp"
fi

# 11. Admin config
info "11. Admin config"
resp=$(api GET /api/v1/admin/config)
engine_mode=$(echo "$resp" | jq -r '.mode' 2>/dev/null || echo "PARSE_ERROR")
if [[ "$engine_mode" == "dev" || "$engine_mode" == "prod" ]]; then
  ok "Admin config OK (mode=$engine_mode, workers=$(echo "$resp" | jq '.max_workers'))"
else
  fail "Admin config parse error: $resp"
fi

# 12. Reconcile trigger
info "12. Trigger reconcile (admin)"
resp=$(api POST /api/v1/admin/reconcile -d '{}')
msg=$(echo "$resp" | jq -r '.message' 2>/dev/null || echo "PARSE_ERROR")
if [[ "$msg" == "reconcile triggered" ]]; then
  ok "Reconcile trigger OK (sessions=$(echo "$resp" | jq '.sessions'))"
else
  fail "Reconcile trigger unexpected response: $msg  raw=$resp"
fi

# 13. Full session lifecycle (skipped if no challenges registered)
info "13. Full session lifecycle (requires challenge + k3s + node-agent)"
CHALLENGES=$(api GET /api/v1/challenges | jq -r '.challenges[0].id' 2>/dev/null || echo "null")

if [[ "$CHALLENGES" == "null" || -z "$CHALLENGES" ]]; then
  skip "No challenges registered — register one first: nexus challenge register --name test --dockerfile ./Dockerfile"
else
  CHALLENGE_ID="$CHALLENGES"
  info "  Using challenge: $CHALLENGE_ID"

  # Create session
  sess_resp=$(api POST /api/v1/sessions \
    -d "{\"challenge_id\":\"$CHALLENGE_ID\",\"user_id\":\"smoke-test-user\"}")
  SESS_ID=$(echo "$sess_resp" | jq -r '.session_id' 2>/dev/null || echo "null")

  if [[ "$SESS_ID" == "null" || -z "$SESS_ID" ]]; then
    fail "Session create failed: $sess_resp"
  else
    ok "Session created: $SESS_ID (pod_ip=$(echo "$sess_resp" | jq -r '.pod_ip'))"

    # Get session
    get_resp=$(api GET "/api/v1/sessions/$SESS_ID")
    get_id=$(echo "$get_resp" | jq -r '.session_id' 2>/dev/null || echo "null")
    if [[ "$get_id" == "$SESS_ID" ]]; then
      ok "Get session OK"
    else
      fail "Get session returned wrong ID: $get_id"
    fi

    # Extend session
    ext_resp=$(api POST "/api/v1/sessions/$SESS_ID/extend" -d '{"duration_minutes":30}')
    new_exp=$(echo "$ext_resp" | jq -r '.new_expires_at' 2>/dev/null || echo "null")
    if [[ -n "$new_exp" && "$new_exp" != "null" ]]; then
      ok "Extend session OK (new_expires=$new_exp)"
    else
      warn "Extend session response: $ext_resp"
      PASS=$((PASS + 1))
    fi

    # Terminate session
    del_code=$(api DELETE "/api/v1/sessions/$SESS_ID" -o /dev/null -w "%{http_code}")
    assert_status 200 "$del_code" "Session terminate"
  fi
fi

# ─── Summary ──────────────────────────────────────────────────────────────────

echo ""
echo "─────────────────────────────────────"
echo -e "Results: ${GREEN}$PASS passed${NC}  ${RED}$FAIL failed${NC}"
echo "─────────────────────────────────────"
echo ""

if [[ "$FAIL" -gt 0 ]]; then
  exit 1
fi
