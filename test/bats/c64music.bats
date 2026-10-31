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

# provision/common/app-25-c64music.sh: ES system c64music for .sid files
# (spec 2026-10-31-c64-music-vm-systems-design.md 3.2), on a fake root with stubs for getent and chown.
# shellcheck disable=SC2016,SC2034

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  SCRIPT="$ROOT/provision/common/app-25-c64music.sh"
  FAKE="$BATS_TEST_TMPDIR/root"
  export PROVISION_ROOT="$FAKE"
  STUBS="$BATS_TEST_TMPDIR/stubs"
  mkdir -p "$STUBS" "$FAKE/etc/emulationstation" "$FAKE/opt/retropie/emulators/vice/bin"
  printf '#!/usr/bin/env bash\n[ "$1 $2" = "passwd emulationstation" ] && echo "emulationstation:x:1000:1000::/home/emulationstation:/bin/bash"\n' >"$STUBS/getent"
  printf '#!/usr/bin/env bash\nexit 0\n' >"$STUBS/chown"
  chmod +x "$STUBS/getent" "$STUBS/chown"
  export PATH="$STUBS:$PATH"
  CFG="$FAKE/etc/emulationstation/es_systems.cfg"
  cp "$ROOT/test/fixtures/es_systems/es_systems.cfg" "$CFG"
  EMU="$FAKE/opt/retropie/configs/c64music/emulators.cfg"
}

with_vsid() {
  printf '#!/bin/sh\n' >"$FAKE/opt/retropie/emulators/vice/bin/vsid"
  chmod +x "$FAKE/opt/retropie/emulators/vice/bin/vsid"
}

# names <file> : the system names in file order, space separated
names() { xmlstarlet sel -t -m /systemList/system -v name -o ' ' "$1"; }

# field <file> <element> : one element of the c64music system
field() { xmlstarlet sel -t -v "/systemList/system[name='c64music']/$2" "$1"; }

@test "no vsid: nothing changes, exit 0" {
  cp "$CFG" "$BATS_TEST_TMPDIR/before"
  run bash "$SCRIPT"
  [ "$status" -eq 0 ]
  [[ "$output" == *"vsid is missing"* ]]
  cmp "$CFG" "$BATS_TEST_TMPDIR/before"
  [ ! -e "$EMU" ]
  [ ! -e "$FAKE/home/emulationstation/RetroPie/roms/c64music" ]
}

@test "with vsid: system after c64, emulators.cfg and ROM folder" {
  with_vsid
  run bash "$SCRIPT"
  [ "$status" -eq 0 ]
  [ "$(names "$CFG")" = "amiga c64 c64music nes " ]
  [ "$(field "$CFG" fullname)" = "Commodore 64 Music" ]
  [ "$(field "$CFG" path)" = "/home/emulationstation/RetroPie/roms/c64music" ]
  [ "$(field "$CFG" extension)" = ".sid .SID" ]
  [ "$(field "$CFG" command)" = "/opt/retropie/supplementary/runcommand/runcommand.sh 0 _SYS_ c64music %ROM%" ]
  [ "$(field "$CFG" platform)" = c64 ]
  [ "$(field "$CFG" theme)" = c64 ]
  # the other systems are unchanged
  [ "$(xmlstarlet sel -t -v "/systemList/system[name='c64']/extension" "$CFG")" = ".d64 .D64 .prg .PRG" ]
  [ "$(cat "$EMU")" = 'vice-vsid = "/opt/retropie/emulators/vice/bin/vice.sh vsid %ROM%"
default = "vice-vsid"' ]
  [ "$(stat -c %a "$EMU")" = 644 ]
  [ "$(stat -c %a "$CFG")" = 644 ]
  [ -d "$FAKE/home/emulationstation/RetroPie/roms/c64music" ]
  xmlstarlet val -q -w "$CFG"
}

@test "second run gives the same file with one c64music" {
  with_vsid
  bash "$SCRIPT"
  cp "$CFG" "$BATS_TEST_TMPDIR/first"
  bash "$SCRIPT"
  cmp "$CFG" "$BATS_TEST_TMPDIR/first"
  [ "$(xmlstarlet sel -t -v "count(/systemList/system[name='c64music'])" "$CFG")" = 1 ]
}

@test "no c64 system: c64music goes to the end" {
  with_vsid
  xmlstarlet ed -L -d "/systemList/system[name='c64']" "$CFG"
  run bash "$SCRIPT"
  [ "$status" -eq 0 ]
  [ "$(names "$CFG")" = "amiga nes c64music " ]
}

@test "missing es_systems.cfg is an error" {
  with_vsid
  rm "$CFG"
  run bash "$SCRIPT"
  [ "$status" -ne 0 ]
  [[ "$output" == *"es_systems.cfg is missing"* ]]
}
