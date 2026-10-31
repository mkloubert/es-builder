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

# write_autostart from provision/common/app-20-es.sh: the two forms of RetroPie's autostart.sh.
# shellcheck disable=SC2016,SC2034

bats_require_minimum_version 1.5.0

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  # shellcheck source=provision/common/app-20-es.sh
  . "$ROOT/provision/common/app-20-es.sh"
  F="$BATS_TEST_TMPDIR/autostart.sh"
}

@test "write_autostart yes: exec line only" {
  write_autostart "$F" yes
  [ "$(cat "$F")" = 'exec /usr/local/lib/emulationstation/es-session' ]
  [ "$(stat -c %a "$F")" = 644 ]
}

@test "write_autostart no: hint, no exec" {
  write_autostart "$F" no
  [ "$(cat "$F")" = '# EmulationStation autostart is off (build.conf ES_AUTOSTART=no).
echo "EmulationStation autostart is off. To start it, type:"
echo "  exec /usr/local/lib/emulationstation/es-session"' ]
  run ! grep -q '^exec' "$F"
  run bash "$F"
  [ "$status" -eq 0 ]
  [ "$output" = 'EmulationStation autostart is off. To start it, type:
  exec /usr/local/lib/emulationstation/es-session' ]
}

@test "write_autostart: wrong or missing value -> error, no file" {
  run write_autostart "$F" maybe
  [ "$status" -ne 0 ]
  run write_autostart "$F" ""
  [ "$status" -ne 0 ]
  [ ! -e "$F" ]
  [ -z "$(ls "$BATS_TEST_TMPDIR" | grep autostart)" ]
}

@test "write_autostart: second run gives the same file" {
  write_autostart "$F" no
  local a
  a=$(sha256sum <"$F")
  write_autostart "$F" no
  [ "$(sha256sum <"$F")" = "$a" ]
}

@test "read_autostart: value from build.env; CFG_EPICNOIR_REF and die stay as they are" {
  local env="$BATS_TEST_TMPDIR/build.env"
  printf 'USER_PASSWORD="x"\nES_AUTOSTART="yes"\n' >"$env"
  CFG_EPICNOIR_REF=abc123
  PROVISION_LIB="$ROOT/builder/lib" read_autostart "$env"
  [ "$CFG_ES_AUTOSTART" = yes ]
  [ "$CFG_EPICNOIR_REF" = abc123 ]
  # still the die of app-20-es.sh (with its prefix), not the one of log.sh
  run die "x"
  [[ "$output" == *"[app-20-es] ERROR: x"* ]]
}

@test "read_autostart: missing key -> no; wrong value or missing file -> error" {
  local env="$BATS_TEST_TMPDIR/build.env"
  printf 'USER_PASSWORD="x"\n' >"$env"
  PROVISION_LIB="$ROOT/builder/lib" read_autostart "$env"
  [ "$CFG_ES_AUTOSTART" = no ]
  printf 'ES_AUTOSTART="maybe"\n' >"$env"
  PROVISION_LIB="$ROOT/builder/lib" run read_autostart "$env"
  [ "$status" -ne 0 ]
  PROVISION_LIB="$ROOT/builder/lib" run read_autostart "$BATS_TEST_TMPDIR/nothing"
  [ "$status" -ne 0 ]
}
