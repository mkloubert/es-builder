#!/usr/bin/env bats
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

# Ctrl+C / docker stop: run.sh passes the signal on to the running step, the step cleans up,
# tee in run_logged stays alive, and only one image build runs on the cache at a time.
# shellcheck disable=SC2016,SC2030,SC2031,SC2034,SC2317

bats_require_minimum_version 1.5.0

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  T="$BATS_TEST_TMPDIR"
  REC="$T/rec"
  export REC
}

# a failed test must not leave the endless stub step running
teardown() {
  if [ -n "${R:-}" ]; then pkill -KILL -f "$R/builder/" 2>/dev/null || true; fi
  if [ -n "${PID:-}" ]; then kill -KILL -- "-$PID" 2>/dev/null || true; fi
}

# fake_root : copy of run.sh and the libs, plus a step "stub.sh" that waits for a signal.
# The step uses the real cleanup trap; its cleanup entry waits a bit and then writes to stdout
# (so the log pipe must still be there) and to $REC.
fake_root() {
  R="$T/root"
  mkdir -p "$R/builder/lib" "$R/builder/steps"
  cp "$ROOT/builder/run.sh" "$R/builder/"
  cp "$ROOT"/builder/lib/*.sh "$R/builder/lib/"
  cat >"$R/builder/steps/stub.sh" <<'STEP'
#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/../lib/log.sh"
. "$(dirname "$0")/../lib/image.sh"
cleanup_install
trap 'echo "signal INT" >>"$REC"; cleanup_run 130' INT
trap 'echo "signal TERM" >>"$REC"; cleanup_run 143' TERM
cleanup_push sh -c 'sleep 0.3; echo "cleanup message"; echo cleanup-done >>"$REC"'
echo "step $1 started"
: >"$REC.ready"
while :; do sleep 0.1; done
STEP
}

wait_ready() {
  local i
  for i in $(seq 1 100); do
    [ -e "$REC.ready" ] && return 0
    sleep 0.1
  done
  echo "stub step did not start" >&2
  return 1
}

# start_run_step : run.sh (sourced) runs the stub step, in a process group of its own like
# under the init process of the container. PID in $PID.
start_run_step() {
  set -m
  bash -c ". '$R/builder/run.sh'; signal_traps_install; LOG_DIR='$T/logs' run_step t stub.sh 0" \
    >"$T/out" 2>&1 </dev/null &
  PID=$!
  set +m
  wait_ready
}

@test "run.sh: TERM is passed on to the step, the step cleans up, run.sh exits 143" {
  fake_root
  start_run_step
  kill -TERM "$PID"
  local rc=0
  wait "$PID" || rc=$?
  [ "$rc" -eq 143 ]
  [ "$(cat "$REC")" = "$(printf 'signal TERM\ncleanup-done')" ]
  grep -q 'cleanup message' "$T/logs/t-stub.log"
  grep -q 'cleanup message' "$T/out"
}

@test "run.sh: INT (Ctrl+C) is passed on to the step, the step cleans up, run.sh exits 130" {
  fake_root
  start_run_step
  # Ctrl+C on a terminal: SIGINT to the foreground process group (run.sh), not to the step
  kill -INT -- "-$PID"
  local rc=0
  wait "$PID" || rc=$?
  [ "$rc" -eq 130 ]
  [ "$(cat "$REC")" = "$(printf 'signal INT\ncleanup-done')" ]
  grep -q 'cleanup message' "$T/logs/t-stub.log"
}

@test "run.sh: the step runs in its own process group" {
  fake_root
  start_run_step
  local step_pid
  step_pid=$(pgrep -f "$R/builder/steps/stub.sh" | head -n 1)
  [ -n "$step_pid" ]
  [ "$(ps -o pgid= -p "$step_pid" | tr -d ' ')" != "$(ps -o pgid= -p "$PID" | tr -d ' ')" ]
  kill -TERM "$PID"
  wait "$PID" || true
}

@test "run_logged: Ctrl+C to the whole group keeps tee alive, the late output is logged" {
  set -m
  bash -c ". '$ROOT/builder/lib/log.sh'
    LOG_DIR='$T/logs' run_logged t s bash -c '
      trap \"sleep 0.3; echo after-signal; exit 7\" INT
      : >\"\$REC.ready\"
      while :; do sleep 0.1; done'" >"$T/out" 2>&1 </dev/null &
  PID=$!
  set +m
  wait_ready
  kill -INT -- "-$PID"
  local rc=0
  wait "$PID" || rc=$?
  [ "$rc" -eq 7 ]
  grep -qx 'after-signal' "$T/logs/t-s.log"
  grep -qx 'after-signal' "$T/out"
}

@test "cleanup_run: a broken log pipe does not stop the cleanup (SIGPIPE ignored)" {
  # the reader of the pipe ("true") is gone before the cleanup entry writes to stdout
  bash -c "
    set -euo pipefail
    . '$ROOT/builder/lib/log.sh'; . '$ROOT/builder/lib/image.sh'
    cleanup_install
    cleanup_push sh -c 'echo second >>\"\$REC\"'
    cleanup_push sh -c 'sleep 0.3; echo lost-message; echo first >>\"\$REC\"'
    exit 1
  " | true
  [ "$(cat "$REC")" = "$(printf 'first\nsecond')" ]
}

@test "run.sh: a second image build on the same cache stops with a clear message" {
  . "$ROOT/builder/run.sh"
  CACHE_DIR="$T/cache"
  OUT_DIR="$T/out-dir"
  BASE_ONLY=1
  mkdir -p "$CACHE_DIR"
  run_image() { echo "built $1"; }
  exec 8>"$CACHE_DIR/.build.lock"
  flock -n 8
  run run_targets rpi4
  [ "$status" -eq 1 ]
  [[ "$output" == *"Another build is already running on the same cache"* ]]
  [[ "$output" != *"built rpi4"* ]]
  exec 8>&-
  run run_targets rpi4
  [ "$status" -eq 0 ]
  [[ "$output" == *"built rpi4"* ]]
}

@test "run_job: the command gets /dev/null as stdin and keeps its exit code" {
  . "$ROOT/builder/run.sh"
  run run_job bash -c 'readlink /proc/$$/fd/0; exit 3' <<<"some input"
  [ "$status" -eq 3 ]
  [ "$output" = /dev/null ]
}
