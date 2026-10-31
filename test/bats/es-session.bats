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

# Session wrapper es-session (Task 7) with stubs for ES, runcommand and sudo.
# shellcheck disable=SC2016,SC2030,SC2031,SC2034,SC2154

bats_require_minimum_version 1.5.0

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  SESSION="$ROOT/overlay/usr/local/lib/emulationstation/es-session"
  T="$BATS_TEST_TMPDIR"
  export RUN_DIR="$T/run" TMP_DIR="$T/tmp" POLL_SLEEP=0 MAX_ITER=2
  mkdir -p "$RUN_DIR" "$TMP_DIR"
  export ES_BIN="$T/es-stub" RUNCOMMAND="$T/rc-stub" SUDO="$T/sudo-stub"
  export ES_COUNT="$T/es-count" SUDO_LOG="$T/sudo-log" RC_ARGS="$T/rc-args" RC_STATE="$T/rc-state"
  : >"$ES_COUNT"
  # ES stub: counts runs, then runs $ES_EXIT_ACTION (for example "touch $TMP_DIR/es-shutdown")
  cat >"$ES_BIN" <<'STUB'
#!/usr/bin/env bash
echo run >>"$ES_COUNT"
if [ -n "${ES_EXIT_ACTION:-}" ]; then eval "$ES_EXIT_ACTION"; fi
if [ -n "${ES_SLEEP:-}" ]; then sleep "$ES_SLEEP"; fi
STUB
  # runcommand stub: one argument per line, the state file and the process group
  cat >"$RUNCOMMAND" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$@" >"$RC_ARGS"
cp "$RUN_DIR/state" "$RC_STATE"
cp "$RUN_DIR/game.pid" "$RC_ARGS.pid"
ps -o pgid= -p $$ | tr -d ' ' >"$RC_ARGS.pgid"
STUB
  printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >>"$SUDO_LOG"\n' >"$SUDO"
  chmod +x "$ES_BIN" "$RUNCOMMAND" "$SUDO"
}

es_runs() { wc -l <"$ES_COUNT" | tr -d ' '; }

@test "ES ends without flags: ES is started again" {
  run "$SESSION"
  [ "$status" -eq 0 ]
  [ "$(es_runs)" -eq 2 ]
  [ "$(cat "$RUN_DIR/state")" = "es" ]
  [ ! -e "$RUN_DIR/es.pid" ]
}

@test "launch file: runcommand gets exact arguments, state is game, files are cleaned" {
  printf 'snes\t/r/Super Mario.sfc\n' >"$RUN_DIR/launch"
  MAX_ITER=1 run "$SESSION"
  [ "$status" -eq 0 ]
  [ "$(cat "$RC_ARGS")" = "$(printf '0\n_SYS_\nsnes\n/r/Super Mario.sfc')" ]
  [ "$(cat "$RC_STATE")" = "$(printf 'game\tsnes\t/r/Super Mario.sfc')" ]
  [ ! -e "$RUN_DIR/launch" ]
  [ ! -e "$RUN_DIR/game.pid" ]
  [ "$(es_runs)" -eq 0 ]
}

@test "game.pid holds the process group ID of the game" {
  printf 'nes\t/r/a.nes\n' >"$RUN_DIR/launch"
  MAX_ITER=1 run "$SESSION"
  [ "$status" -eq 0 ]
  pid=$(cat "$RC_ARGS.pid")
  [ "$pid" = "$(cat "$RC_ARGS.pgid")" ]
  [ "$pid" != "$$" ]
}

@test "after a game the loop starts ES again" {
  printf 'nes\t/r/a.nes\n' >"$RUN_DIR/launch"
  run "$SESSION"
  [ "$status" -eq 0 ]
  [ "$(es_runs)" -eq 1 ]
}

@test "ROM path with special characters is passed unchanged and never evaluated" {
  rom='/r/a $(touch '"$T"'/pwned) `x` ; "q" '"'"'s'"'"' *.nes'
  printf 'nes\t%s\n' "$rom" >"$RUN_DIR/launch"
  MAX_ITER=1 run "$SESSION"
  [ "$status" -eq 0 ]
  [ "$(sed -n 4p "$RC_ARGS")" = "$rom" ]
  [ ! -e "$T/pwned" ]
}

@test "launch file without TAB or with extra TAB is ignored and removed" {
  printf 'snes-without-tab\n' >"$RUN_DIR/launch"
  MAX_ITER=1 run "$SESSION"
  [ ! -e "$RUN_DIR/launch" ]
  [ ! -e "$RC_ARGS" ]
  printf 'snes\t/r/a\tb\n' >"$RUN_DIR/launch"
  MAX_ITER=1 run "$SESSION"
  [ ! -e "$RC_ARGS" ]
}

@test "launch of a missing ROM is not checked by the wrapper (runcommand decides)" {
  printf 'snes\t/does/not/exist.sfc\n' >"$RUN_DIR/launch"
  MAX_ITER=1 run "$SESSION"
  [ "$(sed -n 4p "$RC_ARGS")" = "/does/not/exist.sfc" ]
}

@test "hold: state is stopped and ES is not started" {
  : >"$RUN_DIR/hold"
  run "$SESSION"
  [ "$status" -eq 0 ]
  [ "$(cat "$RUN_DIR/state")" = "stopped" ]
  [ "$(es_runs)" -eq 0 ]
}

@test "hold wins over a launch file" {
  : >"$RUN_DIR/hold"
  printf 'nes\t/r/a.nes\n' >"$RUN_DIR/launch"
  run "$SESSION"
  [ ! -e "$RC_ARGS" ]
  [ -e "$RUN_DIR/launch" ]
}

@test "ES killed while hold appears: state stopped, no restart" {
  ES_SLEEP=30 MAX_ITER=3 "$SESSION" &
  wpid=$!
  for _ in $(seq 50); do [ -s "$RUN_DIR/es.pid" ] && break; sleep 0.1; done
  [ -s "$RUN_DIR/es.pid" ]
  : >"$RUN_DIR/hold"
  kill -TERM "$(cat "$RUN_DIR/es.pid")"
  wait "$wpid"
  [ "$(cat "$RUN_DIR/state")" = "stopped" ]
  [ "$(es_runs)" -eq 1 ]
  [ ! -e "$RUN_DIR/es.pid" ]
}

@test "es-shutdown: sudo poweroff, ES is not started again" {
  export ES_EXIT_ACTION="touch $TMP_DIR/es-shutdown"
  MAX_ITER=3 run "$SESSION"
  [ "$(cat "$SUDO_LOG")" = "poweroff" ]
  [ "$(es_runs)" -eq 1 ]
  [ "$(cat "$RUN_DIR/state")" = "stopped" ]
  [ ! -e "$TMP_DIR/es-shutdown" ]
}

@test "es-sysrestart: sudo reboot" {
  export ES_EXIT_ACTION="touch $TMP_DIR/es-sysrestart"
  MAX_ITER=3 run "$SESSION"
  [ "$(cat "$SUDO_LOG")" = "reboot" ]
  [ "$(es_runs)" -eq 1 ]
}

@test "es-restart: flag is deleted and ES starts again, no sudo" {
  export ES_EXIT_ACTION="touch $TMP_DIR/es-restart"
  run "$SESSION"
  [ ! -e "$TMP_DIR/es-restart" ]
  [ ! -e "$SUDO_LOG" ]
  [ "$(es_runs)" -eq 2 ]
}

@test "stale flag files from before are removed before ES starts" {
  : >"$TMP_DIR/es-shutdown"
  MAX_ITER=1 run "$SESSION"
  [ ! -e "$SUDO_LOG" ]
}

@test "failed poweroff: ES comes back" {
  printf '#!/usr/bin/env bash\nexit 1\n' >"$SUDO"
  export ES_EXIT_ACTION="touch $TMP_DIR/es-shutdown"
  MAX_ITER=2 run "$SESSION"
  [ "$(es_runs)" -eq 2 ]
}

@test "stale es.pid and game.pid are removed at start, hold and launch are kept" {
  echo 99999 >"$RUN_DIR/es.pid"
  echo 99999 >"$RUN_DIR/game.pid"
  printf 'nes\t/r/a.nes\n' >"$RUN_DIR/launch"
  : >"$RUN_DIR/hold"
  MAX_ITER=1 run "$SESSION"
  [ ! -e "$RUN_DIR/es.pid" ]
  [ ! -e "$RUN_DIR/game.pid" ]
  [ -e "$RUN_DIR/launch" ]
  [ -e "$RUN_DIR/hold" ]
  [ ! -e "$RC_ARGS" ]
}

@test "state is written without leftover temp files" {
  MAX_ITER=1 run "$SESSION"
  [ "$(cat "$RUN_DIR/state")" = "es" ]
  for f in "$RUN_DIR"/*; do
    case "${f##*/}" in *tmp*) return 1 ;; esac
  done
}

# start_bg : runs the wrapper in the background (endless loop), sets WPID
start_bg() {
  # Background jobs start with SIGINT/SIGQUIT ignored; tty1 has them at default, so reset them.
  MAX_ITER=0 python3 -I -c 'import os, signal, sys
signal.signal(signal.SIGINT, signal.SIG_DFL)
signal.signal(signal.SIGQUIT, signal.SIG_DFL)
os.execv(sys.argv[1], sys.argv[1:])' "$SESSION" &
  WPID=$!
  for _ in $(seq 50); do [ -s "$RUN_DIR/es.pid" ] && break; sleep 0.1; done
  [ -s "$RUN_DIR/es.pid" ]
}

@test "SIGINT, SIGQUIT and SIGTSTP do not stop the wrapper or ES" {
  sleep 60 &
  export EXIT_HUP_TARGET=$!
  ES_SLEEP=30 start_bg
  espid=$(cat "$RUN_DIR/es.pid")
  kill -INT "$WPID"
  kill -QUIT "$WPID"
  kill -TSTP "$WPID"
  sleep 0.5
  kill -0 "$WPID"
  [ "$(ps -o stat= -p "$WPID" | cut -c1)" != "T" ]
  kill -0 "$espid"
  [ "$(cat "$RUN_DIR/es.pid")" = "$espid" ]
  [ "$(cat "$RUN_DIR/state")" = "es" ]
  disown -a
  kill -KILL "$WPID" "$espid" "$EXIT_HUP_TARGET" || true
}

@test "wrapper that ends abnormally sends HUP to the parent session and stops ES" {
  sleep 60 &
  helper=$!
  export EXIT_HUP_TARGET=$helper
  ES_SLEEP=30 start_bg
  espid=$(cat "$RUN_DIR/es.pid")
  kill -TERM "$WPID"
  wait "$WPID" || true
  for _ in $(seq 30); do kill -0 "$helper" 2>/dev/null || break; sleep 0.1; done
  run kill -0 "$helper"
  [ "$status" -ne 0 ]
  run kill -0 "$espid"
  [ "$status" -ne 0 ]
}

@test "session.pid holds the wrapper PID while ES runs" {
  ES_EXIT_ACTION='cp "$RUN_DIR/session.pid" "$RUN_DIR/../seen-session"' MAX_ITER=1 run "$SESSION"
  [ "$status" -eq 0 ]
  grep -qxE '[0-9]+' "$BATS_TEST_TMPDIR/seen-session"
}

@test "after the end session.pid is gone" {
  MAX_ITER=1 run "$SESSION"
  [ "$status" -eq 0 ]
  [ ! -e "$RUN_DIR/session.pid" ]
}

@test "a session.pid of another wrapper is not removed" {
  ES_EXIT_ACTION='echo 999999 >"$RUN_DIR/session.pid"' MAX_ITER=1 run "$SESSION"
  [ "$status" -eq 0 ]
  [ "$(cat "$RUN_DIR/session.pid")" = 999999 ]
}

@test "abnormal end also removes session.pid" {
  sleep 60 &
  helper=$!
  export EXIT_HUP_TARGET=$helper
  ES_SLEEP=30 start_bg
  local had=0
  [ ! -e "$RUN_DIR/session.pid" ] || had=1
  # stop the wrapper before any check, so a failed check leaves no process behind
  kill -TERM "$WPID"
  wait "$WPID" || true
  kill "$helper" 2>/dev/null || true
  [ "$had" -eq 1 ]
  [ ! -e "$RUN_DIR/session.pid" ]
}

@test "a second wrapper ends at once while another session runs" {
  sleep 60 &
  local other=$!
  echo "$other" >"$RUN_DIR/session.pid"
  echo 4242 >"$RUN_DIR/es.pid"
  MAX_ITER=1 run "$SESSION"
  kill "$other" 2>/dev/null || true
  [ "$status" -eq 1 ]
  [[ "$output" == *"Another EmulationStation session runs already"* ]]
  [ "$(es_runs)" -eq 0 ]
  [ "$(cat "$RUN_DIR/session.pid")" = "$other" ]
  [ "$(cat "$RUN_DIR/es.pid")" = 4242 ]
}

@test "a stale session.pid (dead PID) does not block a new wrapper" {
  echo 999999 >"$RUN_DIR/session.pid"
  MAX_ITER=1 run "$SESSION"
  [ "$status" -eq 0 ]
  [ "$(es_runs)" -eq 1 ]
}

@test "normal end (MAX_ITER) does not send HUP" {
  sleep 60 &
  helper=$!
  EXIT_HUP_TARGET=$helper run "$SESSION"
  kill -0 "$helper"
  kill "$helper"
}

@test "a stopped game is continued and waited for" {
  cat >"$RUNCOMMAND" <<'STUB'
#!/usr/bin/env bash
kill -STOP $$
echo done >"$RC_ARGS.done"
STUB
  printf 'nes\t/r/a.nes\n' >"$RUN_DIR/launch"
  timeout 20 env MAX_ITER=1 "$SESSION"
  [ -e "$RC_ARGS.done" ]
  [ ! -e "$RUN_DIR/game.pid" ]
}

@test "a game killed by SIGINT does not end the wrapper; ES starts again" {
  cat >"$RUNCOMMAND" <<'STUB'
#!/usr/bin/env bash
kill -INT $$
sleep 5
STUB
  printf 'nes\t/r/a.nes\n' >"$RUN_DIR/launch"
  sleep 60 &
  helper=$!
  EXIT_HUP_TARGET=$helper run timeout 20 env MAX_ITER=2 "$SESSION"
  [ "$status" -eq 0 ]
  [ "$(es_runs)" -eq 1 ]
  kill -0 "$helper"
  kill "$helper"
}
