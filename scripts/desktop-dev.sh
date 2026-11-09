#!/usr/bin/env bash
# desktop-dev.sh — one-click launch of `electron-vite dev` against a freshly
# built Go core. Idempotent: running it again while a session is alive
# performs a clean restart of that session (kill → rebuild → relaunch).
# If nothing is running, the kill step is a no-op and the script proceeds
# straight to the build.
#
# What this script does, and why each step matters:
#
#   1. Kills any stale electron-vite / Electron / wuu app-server / `go run`
#      processes from previous sessions. Two traps documented in AGENTS.md
#      make this necessary:
#        a. `go run ./cmd/wuu` builds the core into Go's build cache and the
#           parent `go run` process keeps that cache entry alive for its
#           lifetime. A long-running `electron-vite dev` therefore keeps the
#           OLD core binary in memory even after we rebuild.
#        b. Electron's main process is NOT hot-reloaded by electron-vite —
#           only renderer and preload are. So a stale main process keeps the
#           old IPC handler table and the old core spawn logic.
#      Both are fixed by killing the full process tree first.
#
#   2. `make install` rebuilds ~/go/bin/wuu from the current source. The
#      desktop resolves the core via PATH by default (see
#      desktop/src/main/wuuCommand.ts: WUU_BIN > go run > PATH), so the
#      freshly installed binary is what the desktop will pick up.
#
#   3. Verifies the installed binary's vcs.revision matches HEAD. Warns on
#      mismatch but does not fail — `make install` is best-effort when the
#      source is unchanged from the cached build.
#
#   4. Clears env vars that would steer the desktop away from the freshly
#      installed PATH binary, and prepends ~/.local/bin to PATH so the
#      `~/.local/bin/wuu -> ~/go/bin/wuu` symlink resolves.
#
#   5. `exec`s into `npm run dev` in desktop/, so this terminal is taken
#      over by electron-vite and Ctrl+C cleanly terminates the whole tree.
#
# The script does NOT touch git remotes by default — no push, no fetch, no
# PR. Pass --pull to opt in to `git pull --rebase --autostash` before the
# build, so the session runs the latest pushed code.
#
# Install for launch-from-anywhere (the symlink is resolved back to the repo):
#   ln -sf "$(pwd)/scripts/desktop-dev.sh" ~/.local/bin/wuu-dev

set -euo pipefail

DO_PULL=0
for arg in "$@"; do
  case "$arg" in
    --pull) DO_PULL=1 ;;
    *)
      echo "usage: $(basename "$0") [--pull]" >&2
      exit 2
      ;;
  esac
done

# --- locate repo root -------------------------------------------------------
# Allow invocation from anywhere, including via a symlink on PATH: walk
# symlinks back to the real script location, then take its parent. Fall back
# to the current working directory for `bash <(curl ...)`-style invocations.
resolve_script_path() {
  local src="${BASH_SOURCE[0]:-}"
  local dir
  while [[ -L "$src" ]]; do
    dir="$(cd -P "$(dirname "$src")" && pwd)"
    src="$(readlink "$src")"
    [[ "$src" != /* ]] && src="$dir/$src"
  done
  [[ -n "$src" ]] && cd -P "$(dirname "$src")" && pwd
}

SCRIPT_DIR="$(resolve_script_path || true)"
if [[ -z "$SCRIPT_DIR" || ! -f "$SCRIPT_DIR/../go.mod" ]]; then
  SCRIPT_DIR="$(pwd)"
fi
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

if [[ ! -f "$REPO_ROOT/go.mod" || ! -d "$REPO_ROOT/desktop" ]]; then
  echo "error: $REPO_ROOT does not look like the wuu repo (missing go.mod or desktop/)" >&2
  exit 1
fi

echo "==> repo:   $REPO_ROOT"

# --- 0. optionally sync to the latest pushed code ---------------------------
# Rebase + autostash keeps uncommitted local edits intact across the pull.
if [[ "$DO_PULL" -eq 1 ]]; then
  echo "==> [0/4] pulling latest (git pull --rebase --autostash)"
  git pull --rebase --autostash
fi

HEAD_SHORT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
echo "==> HEAD:   $HEAD_SHORT"

# --- 1. restart: detect & stop any already-running session -----------------
# Two traps documented in AGENTS.md make this necessary even on a clean
# start:
#   a. `go run ./cmd/wuu` builds into Go's build cache and the parent
#      `go run` process keeps that cache entry alive for its lifetime. A
#      long-running `electron-vite dev` therefore keeps the OLD core
#      binary in memory even after we rebuild.
#   b. Electron's main process is NOT hot-reloaded by electron-vite —
#      only renderer and preload are. So a stale main process keeps the
#      old IPC handler table and the old core spawn logic.
# Both are fixed by killing the full process tree before rebuilding.
echo "==> [1/4] scanning for an already-running desktop session"

# `pgrep -fl` returns `PID command`. Strip the PID column for logging.
scan_running() {
  pgrep -fl "$1" 2>/dev/null | awk '{$1=""; sub(/^ /, ""); print}' || true
}

# Patterns that indicate an active desktop dev session. Narrow enough to
# avoid hitting unrelated processes (e.g. an editor named `electron-vite`).
patterns=(
  "electron-vite dev"
  "Electron.app/Contents/MacOS/Electron"
  "/Electron Helper"
  "wuu app-server"
  "go run ./cmd/wuu"
)

found_any=0
for pattern in "${patterns[@]}"; do
  matches="$(scan_running "$pattern")"
  if [[ -n "$matches" ]]; then
    while IFS= read -r line; do
      [[ -n "$line" ]] && echo "    running: $line"
    done <<<"$matches"
    found_any=1
  fi
done

if [[ "$found_any" -eq 0 ]]; then
  echo "    nothing running — clean start"
else
  echo "    sending SIGTERM..."
  for pattern in "${patterns[@]}"; do
    pkill -f "$pattern" 2>/dev/null || true
  done
  # Give Electron a moment to close windows and release ports.
  sleep 2

  # Re-scan: anything still alive gets escalated to SIGKILL.
  still_alive=0
  for pattern in "${patterns[@]}"; do
    matches="$(scan_running "$pattern")"
    if [[ -n "$matches" ]]; then
      still_alive=1
      while IFS= read -r line; do
        [[ -n "$line" ]] && echo "    still alive: $line — escalating to SIGKILL"
      done <<<"$matches"
      pkill -9 -f "$pattern" 2>/dev/null || true
    fi
  done
  sleep 1
  if [[ "$still_alive" -eq 1 ]]; then
    echo "    warning: some processes did not exit cleanly; build may still proceed" >&2
  else
    echo "    all stopped"
  fi
fi

# --- 2. build core from current source -------------------------------------
echo "==> [2/4] building core from current source (make install)"
make install

# --- 3. verify the installed binary matches HEAD ---------------------------
echo "==> [3/4] verifying installed core"
INSTALLED_BIN="$HOME/go/bin/wuu"
SYMLINK_BIN="$HOME/.local/bin/wuu"
if [[ ! -x "$INSTALLED_BIN" ]]; then
  echo "  warning: $INSTALLED_BIN is missing or not executable after make install" >&2
else
  INSTALLED_REV="$(go version -m "$INSTALLED_BIN" 2>/dev/null \
    | awk -F'=' '/vcs\.revision=/{print $2; exit}')"
  INSTALLED_TIME="$(go version -m "$INSTALLED_BIN" 2>/dev/null \
    | awk -F'=' '/vcs\.time=/{print $2; exit}')"
  INSTALLED_MODIFIED="$(go version -m "$INSTALLED_BIN" 2>/dev/null \
    | awk -F'=' '/vcs\.modified=/{print $2; exit}')"
  echo "  installed: $INSTALLED_BIN"
  echo "    vcs.revision = ${INSTALLED_REV:-unknown}"
  echo "    vcs.time     = ${INSTALLED_TIME:-unknown}"
  echo "    vcs.modified = ${INSTALLED_MODIFIED:-unknown}"
  if [[ -n "${INSTALLED_REV:-}" && "${INSTALLED_REV:0:7}" != "${HEAD_SHORT}" ]]; then
    echo "  warning: installed core revision does not match HEAD ($HEAD_SHORT)" >&2
    echo "           this usually means make install skipped the rebuild" >&2
  fi
fi
if [[ -L "$SYMLINK_BIN" ]]; then
  echo "  symlink:   $SYMLINK_BIN -> $(readlink "$SYMLINK_BIN")"
fi

# --- 4. pin desktop to the freshly installed PATH core ---------------------
echo "==> [4/4] pinning desktop to the freshly installed PATH core"
# These env vars (if set in the user's shell) would override the default
# PATH resolution and send the desktop back to the cached `go run` binary.
unset WUU_BIN WUU_DESKTOP_USE_GO_RUN WUU_SOURCE_ROOT
# Make sure ~/.local/bin is on PATH so `wuu` resolves to the symlink.
case ":$PATH:" in
  *":$HOME/.local/bin:"*) ;;
  *) export PATH="$HOME/.local/bin:$PATH" ;;
esac
if command -v wuu >/dev/null 2>&1; then
  echo "  resolved wuu -> $(command -v wuu)"
else
  echo "  warning: 'wuu' is not on PATH after install; the desktop will fail to start the core" >&2
fi

# Hand the terminal over to electron-vite. `exec` replaces this shell so the
# user sees electron-vite's output directly and Ctrl+C cleanly tears down
# the whole tree.
echo "==> starting electron-vite dev (Ctrl+C to stop)"
cd "$REPO_ROOT/desktop"
exec npm run dev