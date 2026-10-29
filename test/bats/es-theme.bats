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

# set_theme_setting from provision/common/app-20-es.sh (es_settings.cfg edit) on fixture files.
# shellcheck disable=SC2016,SC2034

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  # shellcheck source=provision/common/app-20-es.sh
  . "$ROOT/provision/common/app-20-es.sh"
  CFG="$BATS_TEST_TMPDIR/es_settings.cfg"
}

@test "creates the file when it is missing" {
  set_theme_setting "$CFG" es-epicnoir
  [ "$(cat "$CFG")" = '<string name="ThemeSet" value="es-epicnoir" />' ]
}

@test "keeps other entries and adds ThemeSet" {
  printf '<bool name="DrawFramerate" value="false" />\n<string name="Language" value="de" />\n' >"$CFG"
  set_theme_setting "$CFG" es-epicnoir
  grep -qF '<bool name="DrawFramerate" value="false" />' "$CFG"
  grep -qF '<string name="Language" value="de" />' "$CFG"
  [ "$(grep -c 'name="ThemeSet"' "$CFG")" -eq 1 ]
  grep -qF '<string name="ThemeSet" value="es-epicnoir" />' "$CFG"
}

@test "replaces an existing ThemeSet and is idempotent" {
  printf '<string name="ThemeSet" value="carbon" />\n<int name="MaxVRAM" value="100" />\n' >"$CFG"
  set_theme_setting "$CFG" es-epicnoir
  set_theme_setting "$CFG" es-epicnoir
  [ "$(grep -c 'name="ThemeSet"' "$CFG")" -eq 1 ]
  run grep -q carbon "$CFG"
  [ "$status" -ne 0 ]
  grep -qF '<int name="MaxVRAM" value="100" />' "$CFG"
}

@test "last line without newline is kept" {
  printf '<int name="MaxVRAM" value="100" />' >"$CFG"
  set_theme_setting "$CFG" es-epicnoir
  [ "$(wc -l <"$CFG" | tr -d ' ')" -eq 2 ]
}

@test "autologin overlay file resets ExecStart first and uses the exact agetty line" {
  f="$ROOT/overlay/etc/systemd/system/getty@tty1.service.d/autologin.conf"
  [ "$(grep -v '^#' "$f" | grep -v '^$' | sed -n 2p)" = "ExecStart=" ]
  [ "$(grep -v '^#' "$f" | grep -v '^$' | sed -n 3p)" = 'ExecStart=-/sbin/agetty --autologin emulationstation --noclear %I $TERM' ]
  grep -q '^d /run/emulationstation 0755 emulationstation emulationstation -$' "$ROOT/overlay/etc/tmpfiles.d/emulationstation.conf"
}

@test "install_theme: chown goes through the ~/.emulationstation symlink (trailing slash)" {
  local stubs="$BATS_TEST_TMPDIR/stubs" calls="$BATS_TEST_TMPDIR/calls"
  mkdir -p "$stubs"
  cat >"$stubs/git" <<'STUB'
#!/usr/bin/env bash
case "$1" in
  clone) mkdir -p "${!#}/.git" ;;
  -C) [ "$3" != rev-parse ] || echo "$CFG_EPICNOIR_REF" ;;
esac
STUB
  printf '#!/usr/bin/env bash\necho "chown $*" >>"%s"\n' "$calls" >"$stubs/chown"
  chmod +x "$stubs/git" "$stubs/chown"
  R="$BATS_TEST_TMPDIR/root"
  H=$R/home/$U
  mkdir -p "$H" "$R/opt/retropie/configs/all/emulationstation"
  ln -s "$R/opt/retropie/configs/all/emulationstation" "$H/.emulationstation"
  CFG_EPICNOIR_REF=0123456789abcdef0123456789abcdef01234567
  export CFG_EPICNOIR_REF
  PATH="$stubs:$PATH" install_theme
  [ "$(cat "$calls")" = "chown -R $U:$U $H/.emulationstation/" ]
  [ -f "$R/opt/retropie/configs/all/emulationstation/es_settings.cfg" ]
  [ -d "$R/opt/retropie/configs/all/emulationstation/themes/es-epicnoir" ]
}
