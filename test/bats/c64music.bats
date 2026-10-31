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

# rc_section <file> : the lines of the [C64] section, space separated
rc_section() { awk '/^\[/{ins=($0=="[C64]");next} ins' "$1" | tr '\n' ' '; }

# JOY : the [C64] lines of spec 3.2c in the order the script writes new ones
JOY="JoyDevice1=3 JoyDevice2=2 KeySetEnable=1 KeySet1North=273 KeySet1South=274 KeySet1West=276 KeySet1East=275 KeySet1Fire=32 KeySet2North=119 KeySet2South=115 KeySet2West=97 KeySet2East=100 KeySet2Fire=13 JoyMenuControl=1"

@test "VICE joysticks: keysets on both ports in [C64], other lines stay (spec 3.2c)" {
  local rc="$FAKE/opt/retropie/configs/c64/sdl-vicerc"
  mkdir -p "$(dirname "$rc")"
  printf '[C64]\nMouse=1\nJoyDevice2=4\nJoyDevice2=4\n[VSID]\nSoundSampleRate=22050\n' >"$rc"
  run bash "$SCRIPT"
  [ "$status" -eq 0 ]
  [[ "$output" == *"VICE joysticks: port 1 = keyset 2"* ]]
  # JoyDevice2 is replaced in place, the rest is added at the end of [C64]
  [ "$(rc_section "$rc")" = "Mouse=1 JoyDevice2=2 $(echo "$JOY" | sed 's/JoyDevice2=2 //') " ]
  grep -qx 'SoundSampleRate=22050' "$rc"
  [ "$(grep -c '^JoyDevice2=' "$rc")" -eq 1 ]
  [ "$(stat -c %a "$rc")" = 644 ]
  cp "$rc" "$BATS_TEST_TMPDIR/first"
  bash "$SCRIPT"
  cmp "$rc" "$BATS_TEST_TMPDIR/first"
}

@test "VICE joysticks: missing [C64] section or file is added" {
  local rc="$FAKE/opt/retropie/configs/c64/sdl-vicerc"
  mkdir -p "$(dirname "$rc")"
  printf '[VSID]\nX=1\n' >"$rc"
  bash "$SCRIPT"
  [ "$(cat "$rc")" = "$(printf '[VSID]\nX=1\n[C64]\n'; echo "$JOY" | tr ' ' '\n')" ]
  rm "$rc"
  bash "$SCRIPT"
  [ "$(cat "$rc")" = "$(printf '[C64]\n'; echo "$JOY" | tr ' ' '\n')" ]
}

@test "VICE joysticks: no VICE config folder -> nothing is written" {
  run bash "$SCRIPT"
  [ "$status" -eq 0 ]
  [[ "$output" == *"no joystick setup"* ]]
  [ ! -e "$FAKE/opt/retropie/configs/c64" ]
}

@test "VICE joymap: RETURN on button 2 when missing; an own map stays (spec 3.2c)" {
  local jm="$FAKE/opt/retropie/configs/c64/sdl-joymap-C64.vjm"
  mkdir -p "$(dirname "$jm")"
  run bash "$SCRIPT"
  [ "$status" -eq 0 ]
  [[ "$output" == *"RETURN on pad button 2"* ]]
  [ "$(grep -v '^#' "$jm")" = "$(printf '0 1 2 2 0 1\n1 1 2 2 0 1')" ]
  run grep -q '^!CLEAR' "$jm"
  [ "$status" -ne 0 ]
  [ "$(stat -c %a "$jm")" = 644 ]
  printf '!CLEAR\n0 1 0 1 1 16\n' >"$jm"
  run bash "$SCRIPT"
  [ "$status" -eq 0 ]
  [[ "$output" == *"joymap exists, not changed"* ]]
  [ "$(cat "$jm")" = "$(printf '!CLEAR\n0 1 0 1 1 16')" ]
}
