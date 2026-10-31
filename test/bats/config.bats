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


setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  FIX="$ROOT/test/fixtures"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/log.sh"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/config.sh"
}

@test "reads simple values, comments and empty lines are ignored" {
  config_load "$FIX/build.conf.simple"
  [ "$CFG_HOSTNAME" = "mybox" ]
  [ "$CFG_USER_PASSWORD" = "secret" ]
}

@test "surrounding quotes are removed" {
  config_load "$FIX/build.conf.simple"
  [ "$CFG_WEB_USER" = "webadmin" ]
  [ "$CFG_WIFI_SSID" = "My Wi-Fi" ]
}

@test "defaults apply when keys are missing" {
  config_load "$FIX/build.conf.simple"
  [ "$CFG_LOCALE" = "de_DE.UTF-8" ]
  [ "$CFG_TIMEZONE" = "Europe/Berlin" ]
  [ "$CFG_KEYMAP" = "de" ]
  [ "$CFG_WEB_PASSWORD" = "zocken" ]
  [ "$CFG_WIFI_COUNTRY" = "DE" ]
  [ -z "$CFG_USER_SSH_PUBKEY" ]
}

@test "CRLF is removed" {
  config_load "$FIX/build.conf.crlf"
  [ "$CFG_HOSTNAME" = "winbox" ]
  [ "$CFG_USER_PASSWORD" = "pw" ]
  [ "$CFG_TIMEZONE" = "UTC" ]
}

@test "malicious code in a value is taken literally, never executed" {
  rm -f /tmp/pwned-emulationstation-test
  config_load "$FIX/build.conf.evil" 2>/dev/null
  [ "$CFG_WIFI_PASSWORD" = 'a"b$(touch /tmp/pwned-emulationstation-test)`touch /tmp/pwned-emulationstation-test`' ]
  [ ! -e /tmp/pwned-emulationstation-test ]
}

@test "unknown keys: warning on stderr, ignored" {
  run bash -c ". '$ROOT/builder/lib/log.sh'; . '$ROOT/builder/lib/config.sh'; config_load '$FIX/build.conf.evil' 2>&1 >/dev/null"
  [ "$status" -eq 0 ]
  [[ "$output" == *NOT_A_KEY* ]]
  [[ "$output" == *lowercase* ]]
}

@test "missing file aborts" {
  run config_load "$FIX/does-not-exist"
  [ "$status" -ne 0 ]
}

@test "config_require without USER_PASSWORD: exit != 0 and 'USER_PASSWORD is missing'" {
  config_load "$FIX/build.conf.nopw"
  run config_require USER_PASSWORD
  [ "$status" -ne 0 ]
  [[ "$output" == *"USER_PASSWORD is missing"* ]]
}

@test "config_require with a set value succeeds" {
  config_load "$FIX/build.conf.simple"
  run config_require USER_PASSWORD HOSTNAME
  [ "$status" -eq 0 ]
}

@test "SOURCE_URL: default is the upstream repository, empty value allowed" {
  config_load "$FIX/build.conf.simple"
  [ "$CFG_SOURCE_URL" = "https://github.com/mkloubert/es-builder" ]
  printf 'SOURCE_URL=\n' >"$BATS_TEST_TMPDIR/empty.conf"
  config_load "$BATS_TEST_TMPDIR/empty.conf"
  [ -z "$CFG_SOURCE_URL" ]
  printf 'SOURCE_URL=https://example.org/src\n' >"$BATS_TEST_TMPDIR/own.conf"
  config_load "$BATS_TEST_TMPDIR/own.conf"
  [ "$CFG_SOURCE_URL" = "https://example.org/src" ]
}

@test "config_load: ES_AUTOSTART defaults to no" {
  config_load "$FIX/build.conf.simple"
  [ "$CFG_ES_AUTOSTART" = no ]
}

@test "config_load: ES_AUTOSTART yes, no and \"yes\" are accepted" {
  local f="$BATS_TEST_TMPDIR/c" v
  for v in yes no '"yes"'; do
    printf 'USER_PASSWORD=x\nES_AUTOSTART=%s\n' "$v" >"$f"
    config_load "$f"
    [ "$CFG_ES_AUTOSTART" = "${v//\"/}" ]
  done
}

@test "config_load: ES_AUTOSTART Yes, 1 or empty -> error with the value" {
  local f="$BATS_TEST_TMPDIR/c"
  printf 'ES_AUTOSTART=Yes\n' >"$f"
  run config_load "$f"
  [ "$status" -eq 1 ]
  [[ "$output" == *"ES_AUTOSTART must be yes or no (found: Yes)"* ]]
  printf 'ES_AUTOSTART=1\n' >"$f"
  run config_load "$f"
  [ "$status" -eq 1 ]
  printf 'ES_AUTOSTART=\n' >"$f"
  run config_load "$f"
  [ "$status" -eq 1 ]
  [[ "$output" == *"(found: )"* ]]
}
