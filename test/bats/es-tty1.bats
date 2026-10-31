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

# overlay/etc/profile.d/30-emulationstation-session.sh: "emulationstation" on tty1 starts
# es-session (spec 2026-10-31-es-session-detect-design.md 4.1). id, tty and the real command are
# stubs; the shell is an interactive sh (-i).
# shellcheck disable=SC2016,SC2034

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  F="$ROOT/overlay/etc/profile.d/30-emulationstation-session.sh"
  T="$BATS_TEST_TMPDIR"
  mkdir -p "$T/stubs" "$T/run"
  printf '#!/bin/sh\necho "${FAKE_USER:-emulationstation}"\n' >"$T/stubs/id"
  printf '#!/bin/sh\necho "${FAKE_TTY:-/dev/tty1}"\n' >"$T/stubs/tty"
  printf '#!/bin/sh\necho "REAL emulationstation $*"\n' >"$T/stubs/emulationstation"
  printf '#!/bin/sh\necho "SESSION started"\n' >"$T/session"
  chmod +x "$T/stubs/id" "$T/stubs/tty" "$T/stubs/emulationstation" "$T/session"
}

# in_shell <command> [VAR=value...] : runs the command in an interactive sh that read the file
in_shell() {
  local cmd=$1
  shift
  run env PATH="$T/stubs:/usr/bin:/bin" ES_SESSION="$T/session" ES_RUN_DIR="$T/run" "$@" \
    sh -ic ". \"$F\"; $cmd" </dev/null
}

@test "tty1, user emulationstation, no arguments: es-session starts" {
  in_shell emulationstation
  [[ $output == *"SESSION started"* ]]
  [[ $output != *"REAL"* ]]
}

@test "arguments go to the normal command" {
  in_shell 'emulationstation --help'
  [[ $output == *"REAL emulationstation --help"* ]]
  [[ $output != *"SESSION"* ]]
}

@test "other tty or other user: no function, the normal command runs" {
  in_shell emulationstation FAKE_TTY=/dev/pts/0
  [[ $output == *"REAL emulationstation"* ]]
  in_shell emulationstation FAKE_USER=pi
  [[ $output == *"REAL emulationstation"* ]]
}

@test "a running session is not started twice; a bad session.pid is ignored" {
  echo "$$" >"$T/run/session.pid"
  in_shell emulationstation
  [[ $output == *"runs already in its session (PID $$)"* ]]
  [[ $output != *"SESSION started"* ]]
  echo 'abc' >"$T/run/session.pid"
  in_shell emulationstation
  [[ $output == *"SESSION started"* ]]
}

@test "a non-interactive shell gets no function" {
  run env PATH="$T/stubs:/usr/bin:/bin" sh -c ". \"$F\"; emulationstation"
  [[ $output == *"REAL emulationstation"* ]]
}
