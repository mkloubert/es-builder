#!/usr/bin/env bash
# EmuStation Builder
# Copyright (C) 2026  Marcel Joachim Kloubert
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <http://www.gnu.org/licenses/>.

# Container entry point: builder/run.sh <target> [--rebuild-base] [--base-only] [--config FILE]
# build.sh mounts build.conf at /config/build.conf (not below /run, see build.sh).
# Runs in the builder container (Bash 5), repo at /work.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# shellcheck source=builder/lib/log.sh
. "$ROOT/builder/lib/log.sh"
# shellcheck source=builder/lib/config.sh
. "$ROOT/builder/lib/config.sh"

OUT_DIR=${OUT_DIR:-$ROOT/out}
SECRETS_DIR=${SECRETS_DIR:-$ROOT/secrets}

# owner_fix <dir>... : on Linux the container writes as root into the bind mounts. Give the
# files to the host user (HOST_UID/HOST_GID from build.sh, ruling R22). Modes stay (keys 0600).
# Nothing happens if the ids are not set, not numbers or 0.
owner_fix() {
  local d uid=${HOST_UID:-} gid=${HOST_GID:-}
  [[ $uid =~ ^[0-9]+$ && $gid =~ ^[0-9]+$ ]] || return 0
  [ "$uid" -ne 0 ] || return 0
  for d in "$@"; do
    [ -d "$d" ] || continue
    chown -R "$uid:$gid" "$d" || log_warn "chown $uid:$gid $d failed"
  done
  return 0
}

# step_missing <script> <task> : exit 3 if a later task has not delivered the script yet
step_missing() {
  log_error "Step '$1' is not implemented yet (Task $2)."
  exit 3
}

# --- Signals ------------------------------------------------------------------
# Docker sends SIGINT/SIGTERM (Ctrl+C, docker stop) only to this script. The steps run in a
# process group of their own (set -m), so this script can pass the signal on to the whole
# step (the step runs its cleanup trap), wait until the step is done and then exit 130/143.
_JOB_PGID=""
_STOP_SIG=""

_on_signal() {
  _STOP_SIG=$1
  if [ -n "$_JOB_PGID" ]; then
    log_warn "Got SIG$1, stopping the running step (cleanup runs) ..."
    kill -s "$1" -- "-$_JOB_PGID" 2>/dev/null || true
  else
    _exit_for_signal
  fi
}

signal_traps_install() {
  trap '_on_signal INT' INT
  trap '_on_signal TERM' TERM
}

_exit_for_signal() {
  case "$_STOP_SIG" in
    INT) exit 130 ;;
    *) exit 143 ;;
  esac
}

# run_job <command...> : runs the command in its own process group and waits for it.
# Signals are passed on (see above). The exit code is the one of the command.
# stdin is /dev/null: a background process group that reads the terminal (docker run -t) would be
# stopped by SIGTTIN, and no build step may wait for input anyway.
run_job() {
  local rc=0
  set -m
  # the trap keeps this subshell alive until the pipeline in it is done (it is reset to the
  # default for the commands it starts, so the step can trap the signal itself)
  (
    trap ':' INT TERM
    "$@"
  ) </dev/null &
  _JOB_PGID=$!
  set +m
  while :; do
    if wait "$_JOB_PGID"; then rc=0; else rc=$?; fi
    kill -0 "$_JOB_PGID" 2>/dev/null || break
  done
  _JOB_PGID=""
  if [ -n "$_STOP_SIG" ]; then _exit_for_signal; fi
  return "$rc"
}

# build_lock : only one image build at a time on the shared cache volume. run_targets frees it
# at the end; if this script dies early, the lock stays until all steps are gone too (they
# inherit the file descriptor).
build_lock() {
  local dir=${CACHE_DIR:-/cache}
  mkdir -p "$dir"
  exec 9>"$dir/.build.lock"
  flock -n 9 || die "Another build is already running on the same cache ($dir). Wait until it is done, or stop it with 'docker ps' and 'docker stop <id>'." 1
}

# run_step <target> <script> <task> : runs builder/steps/<script>, log in out/logs/<target>-<step>.log
run_step() {
  local target=$1 script=$2 task=$3 name
  [ -f "$ROOT/builder/steps/$script" ] || step_missing "$script" "$task"
  name=${script%.sh}
  # explicit exit: also stops when the caller runs us in a "||" list (errexit is off there)
  run_job run_logged "$target" "$name" bash "$ROOT/builder/steps/$script" "$target" ||
    die "Step '$name' ($target) failed, see $(log_file "$target" "$name")" 1
}

# shell_scripts : all bash scripts of the repo: *.sh and files without extension that start
# with a bash shebang (overlay priv, es-session, firstboot, ...)
shell_scripts() {
  local f
  find . \( -path ./out -o -path ./secrets -o -path ./.git -o -path ./.superpowers \) -prune -o -type f -print |
    LC_ALL=C sort | while IFS= read -r f; do
    case "$f" in
      *.sh) printf '%s\n' "$f" ;;
      *.bats) ;;
      *) if head -n 1 "$f" 2>/dev/null | grep -Eq '^#!.*\bbash\b'; then printf '%s\n' "$f"; fi ;;
    esac
  done
}

run_tests() {
  local files
  log_info "shellcheck ..."
  files=$(shell_scripts)
  # shellcheck disable=SC2086
  run_job run_logged test shellcheck shellcheck -x $files
  log_info "License headers ..."
  run_job run_logged test license-headers "$ROOT/scripts/check-license-headers.sh"
  log_info "bats ..."
  # run_job gives stdin /dev/null: some tests read stdin when it is not a terminal
  run_job run_logged test bats bats test/bats
  log_info "Go (vet, test -race) ..."
  run_job run_logged test go bash -c 'cd webui && go vet ./... && go test -race ./...'
  log_info "All tests passed."
}

run_image() {
  local t=$1
  config_load "$CONFIG" || die "Cannot read $CONFIG" 2
  config_require USER_PASSWORD || exit 2
  run_step "$t" 10-fetch.sh 2
  run_step "$t" 20-prepare-image.sh 2
  run_step "$t" 30-provision-base.sh 3
  if [ "$BASE_ONLY" -eq 1 ]; then
    log_info "--base-only: base layer done, app layer skipped."
    return 0
  fi
  run_step "$t" 40-provision-app.sh 6
  run_step "$t" 50-finalize.sh 11
}

# run_targets <rpi4|vm-amd64|vm-arm64|all> : image build(s). A new build report is started
# (50-finalize.sh appends one section per target); `all` stops at the first failure.
run_targets() {
  local t targets=$1
  [ "$targets" != all ] || targets="rpi4 vm-amd64 vm-arm64"
  build_lock
  if [ "${BASE_ONLY:-0}" -ne 1 ]; then
    mkdir -p "$OUT_DIR"
    {
      printf 'EmuStation Builder build report
'
      printf 'Started: %s

' "$(date -u '+%Y-%m-%d %H:%M:%S UTC')"
    } >"$OUT_DIR/build-report.txt"
  fi
  for t in $targets; do
    run_image "$t" || die "Target '$t' failed; stopping." 1
  done
  exec 9>&-
}

main() {
  cd "$ROOT"
  trap 'owner_fix "$OUT_DIR" "$SECRETS_DIR"' EXIT
  signal_traps_install

  TARGET=${1:-}
  [ -n "$TARGET" ] || die "Usage: builder/run.sh <target> [options]" 2
  shift

  CONFIG="$ROOT/build.conf"
  REBUILD_BASE=0
  BASE_ONLY=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --rebuild-base) REBUILD_BASE=1 ;;
      --base-only) BASE_ONLY=1 ;;
      --config)
        [ $# -ge 2 ] || die "--config needs a file name" 2
        CONFIG=$2
        shift
        ;;
      *) die "Unknown option: $1" 2 ;;
    esac
    shift
  done
  BUILD_CONFIG=$CONFIG
  # one date for all outputs of this run (also for `all` across midnight)
  BUILD_DATE=$(date -u +%Y%m%d)
  export REBUILD_BASE BASE_ONLY BUILD_CONFIG BUILD_DATE OUT_DIR

  case "$TARGET" in
    test) run_tests ;;
    cert)
      if [ -f "$CONFIG" ]; then config_load "$CONFIG"; fi
      run_step cert cert.sh 5
      ;;
    rpi4 | vm-amd64 | vm-arm64 | all) run_targets "$TARGET" ;;
    *) die "Unknown target: $TARGET" 2 ;;
  esac
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
