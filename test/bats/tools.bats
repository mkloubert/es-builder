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

# provision/common/app-12-tools.sh: mc and net-tools, apt only for missing packages
# (spec 2026-10-31-c64-music-vm-systems-design.md 3.2a), and the sbin PATH file of profile.d
# (3.2b). dpkg and apt-get are stubs.
# shellcheck disable=SC2016,SC2034

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  SCRIPT="$ROOT/provision/common/app-12-tools.sh"
  STUBS="$BATS_TEST_TMPDIR/stubs"
  export CALLS="$BATS_TEST_TMPDIR/calls" HAVE="$BATS_TEST_TMPDIR/have"
  mkdir -p "$STUBS" "$HAVE"
  : >"$CALLS"
  # dpkg -s <pkg> succeeds when $HAVE/<pkg> exists
  printf '#!/usr/bin/env bash\n[ "$1" = -s ] && [ -e "$HAVE/$2" ]\n' >"$STUBS/dpkg"
  printf '#!/usr/bin/env bash\necho "apt-get $* DF=$DEBIAN_FRONTEND" >>"$CALLS"\n' >"$STUBS/apt-get"
  chmod +x "$STUBS/dpkg" "$STUBS/apt-get"
  export PATH="$STUBS:$PATH"
}

@test "both missing: one update, one install with both" {
  run bash "$SCRIPT"
  [ "$status" -eq 0 ]
  [ "$(sed -n 1p "$CALLS")" = "apt-get update DF=noninteractive" ]
  [ "$(sed -n 2p "$CALLS")" = "apt-get install -y --no-install-recommends mc net-tools DF=noninteractive" ]
  [ "$(wc -l <"$CALLS" | tr -d ' ')" -eq 2 ]
}

@test "one missing: only that one is installed" {
  : >"$HAVE/mc"
  run bash "$SCRIPT"
  [ "$status" -eq 0 ]
  [ "$(sed -n 2p "$CALLS")" = "apt-get install -y --no-install-recommends net-tools DF=noninteractive" ]
}

@test "all there: apt is not called" {
  : >"$HAVE/mc"
  : >"$HAVE/net-tools"
  run bash "$SCRIPT"
  [ "$status" -eq 0 ]
  [ ! -s "$CALLS" ]
  [[ "$output" == *"Already installed"* ]]
}

@test "failed install stops the script" {
  printf '#!/usr/bin/env bash\n[ "$1" = update ]\n' >"$STUBS/apt-get"
  run bash "$SCRIPT"
  [ "$status" -ne 0 ]
}

# path_after <PATH> : PATH after the profile.d file is read by a POSIX shell
path_after() {
  env -i PATH="$1" sh -c '. "$0" && printf "%s" "$PATH"' "$ROOT/overlay/etc/profile.d/20-emulationstation-sbin.sh"
}

@test "profile.d: sbin folders are appended at the end" {
  [ "$(path_after /usr/local/bin:/usr/bin:/bin:/usr/games)" = "/usr/local/bin:/usr/bin:/bin:/usr/games:/usr/local/sbin:/usr/sbin:/sbin" ]
}

@test "profile.d: nothing is added twice and the old order stays" {
  [ "$(path_after /usr/sbin:/usr/bin:/sbin)" = "/usr/sbin:/usr/bin:/sbin:/usr/local/sbin" ]
  [ "$(path_after /usr/local/sbin:/usr/sbin:/sbin:/usr/bin)" = "/usr/local/sbin:/usr/sbin:/sbin:/usr/bin" ]
}

@test "profile.d: the helper variable is not left behind" {
  run env -i PATH=/usr/bin:/bin sh -c '. "$0"; [ -z "${_es_dir+x}" ]' "$ROOT/overlay/etc/profile.d/20-emulationstation-sbin.sh"
  [ "$status" -eq 0 ]
}
