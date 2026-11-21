#!/usr/bin/env bash
# project-continuity core. Harness-agnostic: plain bash + git. Lives in .continuity/ so it travels with the repo.
#
#   start [--brief]              print the context brief (and self-install the git hook)
#   snap [--stage]               write machine state to .continuity/state/<host>.md
#   check --staged | --range A..B
#   check-path <file> | --stdin-json     exit 2 if the file is frozen / human-owned
#   sync [--all] [-m msg]        snapshot + commit continuity files (everything with --all) + push
#   install-hooks [--quiet]      install the pre-commit hook for this clone
#   pre-commit                   what the git hook runs
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"
cdir="$root/.continuity"
rules="$cdir/PROTECTED.md"
host="${HOSTNAME:-}"; [ -n "$host" ] || host="$(cat /etc/hostname 2>/dev/null || echo host)"
host="${host//[^A-Za-z0-9._-]/_}"
HUMAN_OWNED='.continuity/PROTECTED.md|.continuity/VISION.md|.continuity/continuity.sh'

usage() { sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; }
trim() { sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' <<<"$1"; }
rel() { local p="$1"; p="${p#"$root"/}"; p="${p#./}"; printf '%s' "$p"; }

# print "level<TAB>reason" for each PROTECTED.md rule matching path $1
rules_for() {
  local f="$1" line level pat why
  local re='^(frozen|stable):[[:space:]]*([^|]+)\|?(.*)$'
  [ -f "$rules" ] || return 0
  while IFS= read -r line; do
    [[ "$line" =~ $re ]] || continue
    level="${BASH_REMATCH[1]}"; pat="$(trim "${BASH_REMATCH[2]}")"; why="$(trim "${BASH_REMATCH[3]}")"
    # shellcheck disable=SC2053
    if [[ "$f" == $pat ]]; then printf '%s\t%s\n' "$level" "$why"; fi
  done < "$rules"
}

check_files() {
  local changed="$1" f lv why fail=0 decisions=0
  grep -qx '.continuity/DECISIONS.md' <<<"$changed" && decisions=1 || true
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    while IFS=$'\t' read -r lv why; do
      [ -n "$lv" ] || continue
      if [ "$lv" = frozen ] && [ "${ALLOW_FROZEN:-0}" != 1 ]; then
        echo "BLOCKED (frozen): $f - $why" >&2; fail=1
      elif [ "$lv" = stable ] && [ "$decisions" = 0 ]; then
        echo "BLOCKED (stable, no DECISIONS.md entry in this change): $f - $why" >&2; fail=1
      fi
    done < <(rules_for "$f")
  done <<<"$changed"
  if [ "$fail" = 1 ]; then
    echo "Frozen: ask the user first. Stable: add a DECISIONS.md entry explaining the change." >&2
    return 1
  fi
  return 0
}

# modifying/deleting the rules themselves (once committed) is a human decision
self_protect() {
  local st p fail=0
  while IFS=$'\t' read -r st p; do
    [ -n "${p:-}" ] || continue
    if [[ "$p" =~ ^($HUMAN_OWNED)$ ]] && [ "${ALLOW_FROZEN:-0}" != 1 ]; then
      echo "BLOCKED (human-owned): $p - only the user changes this (commit with ALLOW_FROZEN=1)" >&2; fail=1
    fi
  done <<<"$1"
  return "$fail"
}

stale_warn() {
  local last n
  last="$(git log -1 --format=%H -- .continuity/PROGRESS.md 2>/dev/null || true)"
  [ -n "$last" ] || return 0
  n="$(git rev-list --count "$last"..HEAD 2>/dev/null || echo 0)"
  if [ "$n" -ge "${CONTINUITY_STALE_COMMITS:-5}" ]; then
    echo "continuity: PROGRESS.md untouched for $n commits. Update 'Last known state' before ending the session." >&2
    [ "${CONTINUITY_STRICT:-0}" = 1 ] && return 1
  fi
  return 0
}

install_hooks() {
  local quiet="${1:-}" hook
  git rev-parse --git-dir >/dev/null 2>&1 || return 0
  hook="$(git rev-parse --git-path hooks/pre-commit)"
  case "$hook" in /*) ;; *) hook="$root/$hook" ;; esac
  mkdir -p "$(dirname "$hook")"
  if [ -f "$hook" ] && grep -q 'continuity.sh' "$hook"; then return 0; fi
  if [ -f "$hook" ]; then
    printf '\n"$(git rev-parse --show-toplevel)/.continuity/continuity.sh" pre-commit || exit $?\n' >> "$hook"
  else
    printf '#!/usr/bin/env bash\nexec "$(git rev-parse --show-toplevel)/.continuity/continuity.sh" pre-commit\n' > "$hook"
  fi
  chmod +x "$hook"
  [ "$quiet" = quiet ] || echo "pre-commit hook installed"
  return 0
}

cmd_snap() {
  local out="$cdir/state/$host.md"
  mkdir -p "$cdir/state"
  {
    echo "# Auto state (machine-written; do not edit)"
    echo "- host: $host"
    echo "- time: $(date -u +%FT%TZ)"
    echo "- branch: $(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo '?')"
    echo "- head: $(git log -1 --format='%h %s' 2>/dev/null || echo 'no commits')"
    echo "## Changed files (staged or unstaged)"
    git status --porcelain 2>/dev/null | grep -v ' \.continuity/state/' | head -n 40 || true
    echo "## Recent commits"
    git log --oneline -n 8 2>/dev/null || true
  } > "$out"
  if [ "${1:-}" = "--stage" ]; then git add "$out"; fi
  return 0
}

cmd_start() {
  local brief=0 files f
  if [ "${1:-}" = "--brief" ]; then brief=1; fi
  if [ "$brief" = 0 ]; then install_hooks quiet; fi
  echo "# Continuity brief ($host, $(date -u +%FT%TZ))"
  if [ "$brief" = 0 ]; then
    echo; echo "## Git"
    git status -sb | head -n 15
    git log --oneline -n 8 2>/dev/null || true
    if ls "$cdir"/state/*.md >/dev/null 2>&1; then
      echo; echo "## Last machine snapshots"
      for f in "$cdir"/state/*.md; do
        printf '%s: ' "$(basename "$f" .md)"; grep -m1 '^- time:' "$f" || echo "?"
      done
    fi
  fi
  echo; echo "## VISION"; head -n 60 "$cdir/VISION.md"
  echo; echo "## PROTECTED (rules only)"; grep -E '^(frozen|stable):' "$rules" 2>/dev/null || echo "(none set)"
  echo; echo "## PROGRESS"; cat "$cdir/PROGRESS.md"
  if [ "$brief" = 0 ]; then
    echo; echo "## Newest session logs"
    files="$(ls -1 "$cdir"/sessions/*.md 2>/dev/null | sort | tail -n 3 || true)"
    for f in $files; do echo "### $(basename "$f")"; head -n 40 "$f"; done
    echo; echo "## Recent decisions (headings)"; { grep '^## ' "$cdir/DECISIONS.md" 2>/dev/null || true; } | tail -n 10
    stale_warn 2>&1 || true
  fi
}

cmd_check() {
  local files status fail=0
  case "${1:---staged}" in
    --staged) files="$(git diff --cached --name-only)"; status="$(git diff --cached --name-status --diff-filter=MD)" ;;
    --range)  files="$(git diff --name-only "${2:?range required, e.g. origin/main..HEAD}")"
              status="$(git diff --name-status --diff-filter=MD "$2")" ;;
    *) usage; exit 2 ;;
  esac
  check_files "$files" || fail=1
  self_protect "$status" || fail=1
  exit "$fail"
}

cmd_check_path() {
  local p json lv why
  if [ "${1:-}" = "--stdin-json" ]; then
    json="$(cat)"
    if command -v jq >/dev/null 2>&1; then
      p="$(jq -r '.tool_input.file_path // .tool_input.path // empty' <<<"$json")"
    else
      p="$(sed -n 's/.*"file_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' <<<"$json" | head -n1)"
    fi
  else
    p="${1:?path required}"
  fi
  [ -n "${p:-}" ] || return 0
  p="$(rel "$p")"
  [ "${ALLOW_FROZEN:-0}" = 1 ] && return 0
  if [[ "$p" =~ ^($HUMAN_OWNED)$ ]] && git ls-files --error-unmatch -- "$p" >/dev/null 2>&1; then
    echo "BLOCKED (human-owned): $p - only the user changes this. Propose the change in chat instead." >&2
    return 2
  fi
  while IFS=$'\t' read -r lv why; do
    if [ "$lv" = frozen ]; then
      echo "BLOCKED (frozen): $p - $why. Ask the user; do not edit." >&2
      return 2
    fi
  done < <(rules_for "$p")
  return 0
}

cmd_pre_commit() {
  local files status fail=0
  files="$(git diff --cached --name-only)"
  status="$(git diff --cached --name-status --diff-filter=MD)"
  check_files "$files" || fail=1
  self_protect "$status" || fail=1
  if ! grep -qx '.continuity/PROGRESS.md' <<<"$files"; then stale_warn || fail=1; fi
  [ "$fail" = 0 ] || exit 1
  cmd_snap --stage
}

cmd_sync() {
  local all=0 msg=""
  while [ $# -gt 0 ]; do
    case "$1" in --all) all=1 ;; -m) shift || true; msg="${1:-}" ;; esac
    shift || true
  done
  cmd_snap
  if [ "$all" = 1 ]; then git add -A; else git add .continuity; fi
  if git diff --cached --quiet; then
    echo "nothing to commit"
  else
    git commit -q -m "${msg:-wip(continuity): snapshot from $host}" && echo "committed"
  fi
  if git rev-parse --abbrev-ref '@{u}' >/dev/null 2>&1; then
    git push -q && echo "pushed"
  else
    echo "no upstream set; push manually: git push -u origin $(git rev-parse --abbrev-ref HEAD)"
  fi
}

cmd="${1:-help}"; shift || true
case "$cmd" in
  start)         cmd_start "$@" ;;
  snap)          cmd_snap "$@" ;;
  check)         cmd_check "$@" ;;
  check-path)    cmd_check_path "$@" ;;
  sync)          cmd_sync "$@" ;;
  install-hooks) if [ "${1:-}" = "--quiet" ]; then install_hooks quiet; else install_hooks; fi ;;
  pre-commit)    cmd_pre_commit ;;
  *)             usage; exit 2 ;;
esac
