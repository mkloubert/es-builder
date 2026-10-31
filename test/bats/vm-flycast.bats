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

# provision/vm/app-20-flycast.sh: OpenGL 4.3 override for flycast on virgl
# (spec 2026-10-31-c64-music-vm-systems-design.md 3.2d). chown is a stub.
# shellcheck disable=SC2016,SC2034

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  SCRIPT="$ROOT/provision/vm/app-20-flycast.sh"
  FAKE="$BATS_TEST_TMPDIR/root"
  export PROVISION_ROOT="$FAKE"
  STUBS="$BATS_TEST_TMPDIR/stubs"
  mkdir -p "$STUBS" "$FAKE/opt/retropie/configs/dreamcast"
  printf '#!/usr/bin/env bash\nexit 0\n' >"$STUBS/chown"
  chmod +x "$STUBS/chown"
  export PATH="$STUBS:$PATH"
  CFG="$FAKE/opt/retropie/configs/dreamcast/emulators.cfg"
  CMD='/opt/retropie/emulators/flycast/flycast --config window:height=%YRES% --config window:fullscreen=yes %ROM%'
  ENV='MESA_GL_VERSION_OVERRIDE=4.3 MESA_GLSL_VERSION_OVERRIDE=430'
}

@test "prefix is added once, other lines stay, second run changes nothing" {
  printf 'lr-flycast = "/opt/x.so </dev/null"\nflycast = "%s"\ndefault = "flycast"\n' "$CMD" >"$CFG"
  run bash "$SCRIPT"
  [ "$status" -eq 0 ]
  [ "$(sed -n 2p "$CFG")" = "flycast = \"$ENV $CMD\"" ]
  [ "$(sed -n 1p "$CFG")" = 'lr-flycast = "/opt/x.so </dev/null"' ]
  [ "$(sed -n 3p "$CFG")" = 'default = "flycast"' ]
  [ "$(stat -c %a "$CFG")" = 644 ]
  cp "$CFG" "$BATS_TEST_TMPDIR/first"
  bash "$SCRIPT"
  cmp "$CFG" "$BATS_TEST_TMPDIR/first"
}

@test "an old prefix (for example a debug variable) is replaced" {
  printf 'flycast = "EGL_LOG_LEVEL=debug MESA_GL_VERSION_OVERRIDE=3.3 %s"\n' "$CMD" >"$CFG"
  bash "$SCRIPT"
  [ "$(cat "$CFG")" = "flycast = \"$ENV $CMD\"" ]
}

@test "no file or no flycast line: nothing changes, exit 0" {
  run bash "$SCRIPT"
  [ "$status" -eq 0 ]
  [[ "$output" == *"flycast is not installed"* ]]
  [ ! -e "$CFG" ]
  printf 'default = "x"\n' >"$CFG"
  run bash "$SCRIPT"
  [ "$status" -eq 0 ]
  [[ "$output" == *"No flycast line"* ]]
  [ "$(cat "$CFG")" = 'default = "x"' ]
}
