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

# Root install script "webui-install": swap, restart, health check, rollback. Stubs in PATH.
# shellcheck disable=SC2016,SC2034

bats_require_minimum_version 1.5.0

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  SCRIPT="$ROOT/overlay/usr/local/lib/emulationstation/webui-install"
  STUBS="$BATS_TEST_TMPDIR/stubs"
  CALLS="$BATS_TEST_TMPDIR/calls"
  mkdir -p "$STUBS"
  : >"$CALLS"
  export CALLS
  export PRIV_TEST_PATH="$STUBS:/usr/bin:/bin"
  export PRIV_TEST_INSTALL="$BATS_TEST_TMPDIR/inst"
  export PRIV_TEST_BIN="$BATS_TEST_TMPDIR/bin"
  export PRIV_TEST_RUN="$BATS_TEST_TMPDIR/run"
  mkdir -p "$PRIV_TEST_INSTALL" "$PRIV_TEST_BIN" "$PRIV_TEST_RUN"
  printf 'NEW' >"$PRIV_TEST_INSTALL/new"
  printf 'OLD' >"$PRIV_TEST_BIN/emulationstation-web"
  CUR="$PRIV_TEST_BIN/emulationstation-web"
  # systemctl logs the binary content at the time of each restart
  printf '#!/bin/bash\nprintf "systemctl %%s [%%s]\\n" "$*" "$(cat "$PRIV_TEST_BIN/emulationstation-web" 2>/dev/null)" >>"$CALLS"\n' >"$STUBS/systemctl"
  printf '#!/bin/bash\nprintf "sleep %%s\\n" "$*" >>"$CALLS"\n' >"$STUBS/sleep"
  chmod +x "$STUBS/systemctl" "$STUBS/sleep"
}

need_nonroot() {
  [ "$(id -u)" -ne 0 ] || skip "PRIV_TEST_PATH is ignored for root"
}

# curl_stub <body> : curl prints the body
curl_stub() {
  printf '#!/bin/bash\nprintf "curl %%s\\n" "$*" >>"$CALLS"\nprintf "%%s" %q\n' "$1" >"$STUBS/curl"
  chmod +x "$STUBS/curl"
}

@test "webui-install: good health check installs the new binary" {
  need_nonroot
  curl_stub '{"status":"ok","version":"1.2.3"}'
  run "$SCRIPT" 1.2.3
  [ "$status" -eq 0 ]
  [ "$(cat "$CUR")" = NEW ]
  [ "$(cat "$CUR.prev")" = OLD ]
  [ "$(cat "$PRIV_TEST_RUN/webui-update.status")" = "ok 1.2.3" ]
  [ ! -e "$PRIV_TEST_INSTALL/new" ]
  [ ! -e "$CUR.new" ]
  [ "$(grep -c '^systemctl restart' "$CALLS")" -eq 1 ]
  ! grep -q reset-failed "$CALLS"
  grep -qx 'systemctl restart emulationstation-web.service \[NEW\]' "$CALLS"
  grep -q 'curl -sk --max-time 2' "$CALLS"
  grep -qF 'https://127.0.0.1:11944/healthz' "$CALLS"
  [ -x "$CUR" ]
}

@test "webui-install: .prev is a hardlink of the old file, the old file is never written" {
  need_nonroot
  curl_stub '{"version":"1.2.3"}'
  ino=$(stat -c %i "$CUR")
  run "$SCRIPT" 1.2.3
  [ "$status" -eq 0 ]
  [ "$(stat -c %i "$CUR.prev")" = "$ino" ]
}

@test "webui-install: a failing curl rolls back" {
  need_nonroot
  printf '#!/bin/bash\nprintf "curl\\n" >>"$CALLS"\nexit 7\n' >"$STUBS/curl"
  chmod +x "$STUBS/curl"
  run "$SCRIPT" 1.2.3
  [ "$status" -eq 1 ]
  [ "$(cat "$CUR")" = OLD ]
  [ ! -e "$CUR.prev" ]
  [ "$(cat "$PRIV_TEST_RUN/webui-update.status")" = "rolled-back 1.2.3" ]
  [ ! -e "$PRIV_TEST_INSTALL/new" ]
  [ "$(grep -c '^curl' "$CALLS")" -eq 15 ]
  [ "$(grep -c '^sleep 2' "$CALLS")" -eq 14 ]
  # restart order: new binary first, old binary after the rollback
  [ "$(grep '^systemctl' "$CALLS")" = "$(printf 'systemctl restart emulationstation-web.service [NEW]\nsystemctl reset-failed emulationstation-web.service [OLD]\nsystemctl restart emulationstation-web.service [OLD]')" ]
}

@test "webui-install: the wrong version rolls back" {
  need_nonroot
  curl_stub '{"version":"1.2.2"}'
  run "$SCRIPT" 1.2.3
  [ "$status" -eq 1 ]
  [ "$(cat "$CUR")" = OLD ]
  [ "$(cat "$PRIV_TEST_RUN/webui-update.status")" = "rolled-back 1.2.3" ]
}

@test "webui-install: the version must match exactly, not as a prefix" {
  need_nonroot
  curl_stub '{"version":"1.2.30"}'
  run "$SCRIPT" 1.2.3
  [ "$status" -eq 1 ]
}

@test "webui-install: success after some failed tries" {
  need_nonroot
  printf '#!/bin/bash\nn=$(cat "$CALLS.n" 2>/dev/null || echo 0)\nn=$((n+1))\necho $n >"$CALLS.n"\n[ "$n" -ge 3 ] || exit 7\nprintf "{\\"version\\":\\"1.2.3\\"}"\n' >"$STUBS/curl"
  chmod +x "$STUBS/curl"
  run "$SCRIPT" 1.2.3
  [ "$status" -eq 0 ]
  [ "$(grep -c '^sleep' "$CALLS")" -eq 2 ]
}

@test "webui-install: first install without a current binary" {
  need_nonroot
  rm "$CUR"
  curl_stub '{"version":"1.2.3"}'
  run "$SCRIPT" 1.2.3
  [ "$status" -eq 0 ]
  [ "$(cat "$CUR")" = NEW ]
  [ ! -e "$CUR.prev" ]
}

@test "webui-install: first install that fails removes the new binary and restarts" {
  need_nonroot
  rm "$CUR"
  curl_stub 'nothing'
  run "$SCRIPT" 1.2.3
  [ "$status" -eq 1 ]
  [ ! -e "$CUR" ]
  [ "$(grep -c '^systemctl restart' "$CALLS")" -eq 2 ]
  [ "$(cat "$PRIV_TEST_RUN/webui-update.status")" = "rolled-back 1.2.3" ]
}

@test "webui-install: refuses bad input with exit 2 and changes nothing" {
  need_nonroot
  curl_stub '{"version":"1.2.3"}'
  local bad
  for bad in '' 1.2 01.2.3 '1.2.3;x' ' 1.2.3'; do
    run "$SCRIPT" "$bad"
    [ "$status" -eq 2 ] || { echo "accepted: [$bad]"; return 1; }
  done
  run "$SCRIPT"
  [ "$status" -eq 2 ]
  # an invalid version writes no status
  [ ! -e "$PRIV_TEST_RUN/webui-update.status" ]
  rm "$PRIV_TEST_INSTALL/new"
  run "$SCRIPT" 1.2.3
  [ "$status" -eq 2 ]
  [ "$(cat "$PRIV_TEST_RUN/webui-update.status")" = "failed 1.2.3" ]
  rm "$PRIV_TEST_RUN/webui-update.status"
  ln -s /etc/passwd "$PRIV_TEST_INSTALL/new"
  run "$SCRIPT" 1.2.4
  [ "$status" -eq 2 ]
  [ "$(cat "$PRIV_TEST_RUN/webui-update.status")" = "failed 1.2.4" ]
  [ "$(cat "$CUR")" = OLD ]
  [ ! -s "$CALLS" ]
}

@test "webui-install: a symlink at the status path is replaced, its target is untouched" {
  need_nonroot
  curl_stub '{"version":"1.2.3"}'
  printf 'victim' >"$BATS_TEST_TMPDIR/victim"
  ln -s "$BATS_TEST_TMPDIR/victim" "$PRIV_TEST_RUN/webui-update.status"
  run "$SCRIPT" 1.2.3
  [ "$status" -eq 0 ]
  [ ! -L "$PRIV_TEST_RUN/webui-update.status" ]
  [ "$(cat "$PRIV_TEST_RUN/webui-update.status")" = "ok 1.2.3" ]
  [ "$(cat "$BATS_TEST_TMPDIR/victim")" = victim ]
}

@test "webui-install: a symlink to a folder at the status path writes nothing into the folder" {
  need_nonroot
  curl_stub '{"version":"1.2.3"}'
  mkdir "$BATS_TEST_TMPDIR/elsewhere"
  ln -s "$BATS_TEST_TMPDIR/elsewhere" "$PRIV_TEST_RUN/webui-update.status"
  run "$SCRIPT" 1.2.3
  [ "$status" -eq 0 ]
  [ -z "$(ls -A "$BATS_TEST_TMPDIR/elsewhere")" ]
  [ "$(cat "$PRIV_TEST_RUN/webui-update.status")" = "ok 1.2.3" ]
  [ "$(find "$PRIV_TEST_RUN" -mindepth 1 | wc -l)" -eq 1 ]
}

@test "webui-install: a failing install rolls back with a status and a restart" {
  need_nonroot
  curl_stub '{"version":"1.2.3"}'
  printf '#!/bin/bash\nexit 1\n' >"$STUBS/install"
  chmod +x "$STUBS/install"
  run "$SCRIPT" 1.2.3
  [ "$status" -eq 1 ]
  [ "$(cat "$CUR")" = OLD ]
  [ ! -e "$CUR.new" ]
  [ "$(cat "$PRIV_TEST_RUN/webui-update.status")" = "rolled-back 1.2.3" ]
  [ "$(grep -c '^systemctl restart' "$CALLS")" -eq 1 ]
  grep -q '^systemctl reset-failed' "$CALLS"
  [ ! -e "$PRIV_TEST_INSTALL/new" ]
}

@test "webui-install: a failing rollback writes failed and still restarts" {
  need_nonroot
  printf '#!/bin/bash\nexit 7\n' >"$STUBS/curl"
  printf '#!/bin/bash\ncase "$*" in *.prev*) exit 1 ;; esac\nexec /bin/mv "$@"\n' >"$STUBS/mv"
  chmod +x "$STUBS/curl" "$STUBS/mv"
  run "$SCRIPT" 1.2.3
  [ "$status" -eq 1 ]
  [ "$(cat "$PRIV_TEST_RUN/webui-update.status")" = "failed 1.2.3" ]
  [ "$(grep -c '^systemctl restart' "$CALLS")" -eq 2 ]
}
