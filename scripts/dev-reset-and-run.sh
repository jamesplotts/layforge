#!/usr/bin/env bash
# Copyright (c) 2026 James Duane Plotts
# Licensed under the MIT License. See LICENSE in the repository root.
#
# Nukes and rebuilds a local layforge + OpenCombatEngine checkout from
# scratch, then launches both and opens the admin panel. This is a
# fresh-clone smoke-test tool, not anything to point at a checkout
# holding work you care about: it deletes ~/layforge and
# ~/opencombatengine outright, no confirmation beyond the one prompt
# below, no undo.
#
# IMPORTANT — keep your runnable copy OUTSIDE ~/layforge and
# ~/opencombatengine. This script deletes both of those directories; if
# it were running from inside one of them, deleting the file out from
# under the still-running interpreter is undefined behavior. This copy,
# checked into the repo at scripts/dev-reset-and-run.sh, is for
# reference/version-control only — copy it to, e.g., ~/dev-reset-layforge.sh
# once and run it from there (the script itself refuses to run from
# inside either target directory, as a backstop).
#
# Usage:
#   ~/dev-reset-layforge.sh          # asks "type yes" before deleting anything
#   ~/dev-reset-layforge.sh -y       # skips the confirmation
#
# What it does, in order:
#   1. Kills any running Master (whatever holds -addr/-admin-addr's ports)
#   2. Deletes ~/layforge
#   3. Kills any running OpenCombatEngine sidecar (port 5265)
#   4. Deletes ~/opencombatengine
#   5. Clones layforge fresh into ~/layforge, runs protocol/generate.sh
#   6. Clones opencombatengine fresh into ~/opencombatengine, starts the
#      sidecar in the background, waits until it's actually listening
#   7. Starts Master in the background against that sidecar, waits until
#      the admin panel actually answers
#   8. Opens the admin panel in your default browser
#
# Master and the sidecar are left running in the background (redirected
# to log files under /tmp) after this script exits — it hands you a
# ready table, not a foreground process to babysit. Their PIDs are
# printed at the end and written to ~/.layforge-dev-pids so the *next*
# run of this script can find and stop them precisely, though the
# primary kill mechanism is always "whatever is actually bound to the
# port," which works even if a PID went stale.

set -euo pipefail
trap 'echo "dev-reset-and-run.sh: failed at line $LINENO" >&2' ERR

HOME_DIR="${HOME:?HOME is not set — refusing to guess where to operate}"
LAYFORGE_DIR="$HOME_DIR/layforge"
OCE_DIR="$HOME_DIR/opencombatengine"
PIDFILE="$HOME_DIR/.layforge-dev-pids"
SIDECAR_LOG="/tmp/opencombatengine-sidecar.log"
MASTER_LOG="/tmp/layforge-master.log"
SIDECAR_PORT=5265
MASTER_PLAYER_PORT=8080
MASTER_ADMIN_PORT=8090
MASTER_ADMIN_URL="http://127.0.0.1:${MASTER_ADMIN_PORT}/"

# Refuse to run from inside either directory this script is about to
# delete — see the IMPORTANT note above.
self_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
case "$self_dir" in
  "$LAYFORGE_DIR"|"$LAYFORGE_DIR"/*|"$OCE_DIR"|"$OCE_DIR"/*)
    echo "error: this script is running from inside $LAYFORGE_DIR or $OCE_DIR," >&2
    echo "       both of which it deletes. Copy it somewhere else first, e.g.:" >&2
    echo "         cp '$0' '$HOME_DIR/dev-reset-layforge.sh' && chmod +x '$HOME_DIR/dev-reset-layforge.sh'" >&2
    echo "       and run that copy instead." >&2
    exit 1
    ;;
esac

require_safe_target_dir() {
  local dir="$1"
  if [[ -z "$dir" || "$dir" == "/" || "$dir" == "$HOME_DIR" ]]; then
    echo "error: refusing to delete suspicious path '$dir'" >&2
    exit 1
  fi
}
require_safe_target_dir "$LAYFORGE_DIR"
require_safe_target_dir "$OCE_DIR"

# --- prerequisites, checked before anything destructive -----------------

for tool in git go dotnet; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "error: '$tool' is not on PATH — install it before running this script." >&2
    exit 1
  fi
done

# --- confirmation ---------------------------------------------------------

assume_yes=0
if [[ "${1:-}" == "-y" || "${1:-}" == "--yes" ]]; then
  assume_yes=1
fi

echo "This will:"
echo "  - stop any running Master and OpenCombatEngine sidecar"
echo "  - permanently delete $LAYFORGE_DIR"
echo "  - permanently delete $OCE_DIR"
echo "  - re-clone both fresh from GitHub and relaunch them"
echo
if [[ "$assume_yes" -ne 1 ]]; then
  read -r -p "Type 'yes' to continue: " confirm
  if [[ "$confirm" != "yes" ]]; then
    echo "Aborted — nothing was touched."
    exit 1
  fi
fi

# --- helpers ---------------------------------------------------------------

# wait_for_port polls host:port with a plain TCP connect (bash's own
# /dev/tcp, no extra tools required) until something answers or timeout
# seconds pass. Works equally well for the sidecar's h2c gRPC port and
# Master's plain-HTTP admin port — a connect succeeding is all that's
# asked.
wait_for_port() {
  local host="$1" port="$2" timeout="${3:-120}" waited=0
  while ! (exec 3<>"/dev/tcp/$host/$port") 2>/dev/null; do
    sleep 2
    waited=$((waited + 2))
    if (( waited % 20 == 0 )); then
      echo "   ...still waiting for $host:$port ($waited/${timeout}s)"
    fi
    if (( waited >= timeout )); then
      return 1
    fi
  done
  exec 3<&- 3>&- 2>/dev/null || true
  return 0
}

# kill_port stops whatever is actually bound (LISTEN) to a local TCP
# port, regardless of how it was started (`go run`'s temp binary, a
# prebuilt binary, `dotnet run`) — the one thing that's always true
# about "the process serving this port" is that it holds the port.
kill_port() {
  local port="$1" pids=""
  if command -v lsof >/dev/null 2>&1; then
    pids="$(lsof -ti :"$port" -sTCP:LISTEN 2>/dev/null || true)"
  elif command -v fuser >/dev/null 2>&1; then
    pids="$(fuser -n tcp "$port" 2>/dev/null || true)"
  fi
  if [[ -n "$pids" ]]; then
    echo "   stopping pid(s) $pids listening on port $port"
    kill $pids 2>/dev/null || true
    sleep 1
    kill -9 $pids 2>/dev/null || true
  fi
}

# kill_by_pattern is a supplementary best-effort stop for a process by
# command-line substring — useful for dotnet (whose command line keeps
# the project name) but not relied on alone for Master, since `go run`'s
# compiled temp binary's path doesn't reliably contain anything
# recognizable.
kill_by_pattern() {
  pkill -f "$1" 2>/dev/null || true
}

# --- load PIDs from the last run, if any -----------------------------------

old_sidecar_pid=""
old_master_pid=""
if [[ -f "$PIDFILE" ]]; then
  # shellcheck disable=SC1090
  source "$PIDFILE"
  old_sidecar_pid="${SIDECAR_PID:-}"
  old_master_pid="${MASTER_PID:-}"
fi

# --- 1/8: stop Master --------------------------------------------------

echo "==> 1/8: stopping any running Master"
if [[ -n "$old_master_pid" ]] && kill -0 "$old_master_pid" 2>/dev/null; then
  kill "$old_master_pid" 2>/dev/null || true
  sleep 1
  kill -9 "$old_master_pid" 2>/dev/null || true
fi
kill_port "$MASTER_PLAYER_PORT"
kill_port "$MASTER_ADMIN_PORT"

# --- 2/8: delete ~/layforge ----------------------------------------------

echo "==> 2/8: deleting $LAYFORGE_DIR"
rm -rf -- "$LAYFORGE_DIR"

# --- 3/8: stop the sidecar -------------------------------------------------

echo "==> 3/8: stopping any running OpenCombatEngine sidecar"
if [[ -n "$old_sidecar_pid" ]] && kill -0 "$old_sidecar_pid" 2>/dev/null; then
  kill "$old_sidecar_pid" 2>/dev/null || true
  sleep 1
  kill -9 "$old_sidecar_pid" 2>/dev/null || true
fi
kill_by_pattern "OpenCombatEngine.GrpcSidecar"
kill_port "$SIDECAR_PORT"

# --- 4/8: delete ~/opencombatengine ----------------------------------------

echo "==> 4/8: deleting $OCE_DIR"
rm -rf -- "$OCE_DIR"

# --- 5/8: clone layforge, generate protocol stubs --------------------------

echo "==> 5/8: cloning layforge and generating protocol stubs"
git clone https://github.com/jamesplotts/layforge.git "$LAYFORGE_DIR"
(
  cd "$LAYFORGE_DIR"
  ./protocol/generate.sh
)

# --- 6/8: clone OpenCombatEngine, start the sidecar ------------------------

echo "==> 6/8: cloning OpenCombatEngine and starting the sidecar"
git clone https://github.com/jamesplotts/opencombatengine.git "$OCE_DIR"
: > "$SIDECAR_LOG"
cd "$OCE_DIR"
nohup dotnet run --project src/OpenCombatEngine.GrpcSidecar/OpenCombatEngine.GrpcSidecar.csproj \
  >"$SIDECAR_LOG" 2>&1 &
sidecar_pid=$!
cd "$HOME_DIR"
echo "   sidecar starting (pid $sidecar_pid) — first run also restores/builds, this can take a while"
if ! wait_for_port localhost "$SIDECAR_PORT" 240; then
  echo "error: sidecar never started listening on port $SIDECAR_PORT — check $SIDECAR_LOG" >&2
  exit 1
fi
echo "   sidecar is up."

# --- 7/8: start Master ------------------------------------------------------

echo "==> 7/8: starting Master"
: > "$MASTER_LOG"
cd "$LAYFORGE_DIR/master"
nohup go run . -system-engine-addr localhost:"$SIDECAR_PORT" \
  >"$MASTER_LOG" 2>&1 &
master_pid=$!
cd "$HOME_DIR"
echo "   Master starting (pid $master_pid) — first run also downloads modules and builds"
if ! wait_for_port localhost "$MASTER_ADMIN_PORT" 240; then
  echo "error: Master's admin panel never came up on port $MASTER_ADMIN_PORT — check $MASTER_LOG" >&2
  exit 1
fi
echo "   Master is up."

{
  echo "SIDECAR_PID=$sidecar_pid"
  echo "MASTER_PID=$master_pid"
} > "$PIDFILE"

# --- 8/8: open the admin panel ----------------------------------------------

echo "==> 8/8: opening the admin panel"
if command -v xdg-open >/dev/null 2>&1; then
  xdg-open "$MASTER_ADMIN_URL" >/dev/null 2>&1 &
else
  echo "   xdg-open not found — open this yourself: $MASTER_ADMIN_URL"
fi

cat <<EOF

Done. Running in the background:
  sidecar  pid $sidecar_pid   log: $SIDECAR_LOG
  master   pid $master_pid   log: $MASTER_LOG
  admin    $MASTER_ADMIN_URL

Tail logs with:
  tail -f $SIDECAR_LOG
  tail -f $MASTER_LOG

The next run of this script stops both of these first.
EOF
