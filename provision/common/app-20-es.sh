#!/usr/bin/env bash
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

# App layer, in the chroot: EmulationStation session.
#  - enables RetroPie's "autostart" module (autologin on tty1 + /etc/profile.d/10-retropie.sh,
#    which runs autostart.sh on tty1 only, for user emulationstation only; SSH logins skip it),
#  - writes autostart.sh: with ES_AUTOSTART=yes it starts our wrapper (es-session) instead of
#    "emulationstation"; with no it only prints a hint, so tty1 shows a shell (spec
#    2026-10-31-es-autostart-design.md 3.2). The autologin on tty1 stays in both cases.
#  - installs the EpicNoir theme at CFG_EPICNOIR_REF and makes it the active theme.
# Checked at RetroPie-Setup f5ebfba6: "retropie_packages.sh autostart enable" calls enable_autostart
# (module autostart.sh). On Debian with systemd it writes getty@tty1.service.d/autologin.conf (same
# content as our overlay file); on "Raspbian" it would use raspi-config instead, so this script
# checks the result. Environment: CFG_EPICNOIR_REF, CFG_TARGET (step 40); ES_AUTOSTART comes from
# build.env (BUILD_ENV, read with config_load).
# Idempotent. PROVISION_ROOT (tests only): path prefix. When sourced, only the functions load.
set -euo pipefail

log() { printf '[app-20-es] %s\n' "$*" >&2; }
die() {
  printf '[app-20-es] ERROR: %s\n' "$*" >&2
  exit 1
}

U=emulationstation
R=${PROVISION_ROOT:-}
H=$R/home/$U
THEME_URL=https://github.com/c64-dev/es-theme-epicnoir.git
THEME_NAME=es-epicnoir
SESSION=/usr/local/lib/emulationstation/es-session

# set_file <mode> <path> : root:root, given mode; the file must exist
set_file() {
  [ -f "$R$2" ] || die "Overlay file is missing: $2"
  chown root:root "$R$2"
  chmod "$1" "$R$2"
}

# set_theme_setting <es_settings.cfg> <theme> : sets <string name="ThemeSet" .../>.
# ES (RetroPie fork) saves settings as a flat list of elements without a root element, one per
# line, so xmlstarlet cannot read the file directly. The edit is line based: all ThemeSet lines
# are removed, the new one is appended, the other entries stay. The file is created if missing.
set_theme_setting() {
  local file=$1 theme=$2 tmp
  tmp=$(mktemp "$file.XXXXXX")
  if [ -f "$file" ]; then
    grep -v 'name="ThemeSet"' "$file" >"$tmp" || true
  fi
  printf '<string name="ThemeSet" value="%s" />\n' "$theme" >>"$tmp"
  chmod 0644 "$tmp"
  mv -f "$tmp" "$file"
}

# write_autostart <file> <yes|no> : RetroPie's autostart.sh (run with bash on tty1 at login).
# yes: start es-session; no: print a hint only. Temp file + rename, mode 0644.
write_autostart() {
  local file=$1 mode=$2 tmp
  case "$mode" in
    yes | no) ;;
    *) die "ES_AUTOSTART must be yes or no (found: $mode)" ;;
  esac
  tmp=$(mktemp "$file.XXXXXX")
  if [ "$mode" = yes ]; then
    printf 'exec %s\n' "$SESSION" >"$tmp"
  else
    printf '%s\n' '# EmulationStation autostart is off (build.conf ES_AUTOSTART=no).' \
      'echo "EmulationStation autostart is off. To start it, type:"' \
      "echo \"  exec $SESSION\"" >"$tmp"
  fi
  chmod 0644 "$tmp"
  mv -f "$tmp" "$file"
}

# rp <args> : retropie_packages.sh as root for user emulationstation (as in base-30-retropie.sh)
rp() {
  local penv=()
  case "${CFG_TARGET:-}" in
    rpi4) penv=("__platform=rpi4") ;;
    vm-amd64) penv=("__platform=x86" "__cpu_flags=-march=x86-64" "__has_kms=1") ;;
    vm-arm64) penv=("__platform=aarch64" "__cpu_flags=-march=armv8-a" "__has_kms=1") ;;
    *) die "Unknown CFG_TARGET: ${CFG_TARGET:-}" ;;
  esac
  (cd "$R/home/$U/RetroPie-Setup" &&
    env SUDO_USER="$U" __nodialog=1 "${penv[@]}" bash ./retropie_packages.sh "$@")
}

install_autostart() {
  set_file 0644 /etc/tmpfiles.d/emulationstation.conf
  set_file 0644 /etc/systemd/system/getty@tty1.service.d/autologin.conf
  set_file 0755 /usr/local/lib/emulationstation/es-session

  rp autostart enable
  local hook=$R/etc/profile.d/10-retropie.sh login=$R/etc/systemd/system/getty@tty1.service.d/autologin.conf
  [ -f "$hook" ] || die "autostart hook is missing: /etc/profile.d/10-retropie.sh"
  grep -q '/dev/tty1' "$hook" || die "autostart hook does not check for tty1"
  grep -q -- "--autologin $U " "$login" || die "autologin drop-in is missing or wrong"
  chown root:root "$login"
  chmod 0644 "$login"

  mkdir -p "$R/opt/retropie/configs/all"
  write_autostart "$R/opt/retropie/configs/all/autostart.sh" "${CFG_ES_AUTOSTART:-}"
  chown "$U:$U" "$R/opt/retropie/configs/all/autostart.sh"
  if [ "$CFG_ES_AUTOSTART" = yes ]; then
    log "Autostart: ES starts at boot (tty1 only, autostart.sh starts es-session)"
  else
    log "Autostart: off, tty1 shows a shell"
  fi
}

install_theme() {
  local ref=${CFG_EPICNOIR_REF:?CFG_EPICNOIR_REF is missing}
  local es_dir=$H/.emulationstation dest=$H/.emulationstation/themes/$THEME_NAME
  mkdir -p "$es_dir/themes"
  rm -rf "${dest:?}"
  git clone -q "$THEME_URL" "$dest"
  git -C "$dest" -c advice.detachedHead=false checkout -q "$ref"
  [ "$(git -C "$dest" rev-parse HEAD)" = "$ref" ] || die "theme is not at $ref"
  rm -rf "$dest/.git"
  set_theme_setting "$es_dir/es_settings.cfg" "$THEME_NAME"
  # trailing slash: ~/.emulationstation is a symlink (RetroPie, to /opt/retropie/configs/all/
  # emulationstation); chown -R on the link alone would only change the link
  chown -R "$U:$U" "$es_dir/"
  log "Theme $THEME_NAME at $ref is active"
}

# read_autostart <build.env> : sets CFG_ES_AUTOSTART from build.env. Read in a subshell:
# config_load resets every CFG_* key (CFG_EPICNOIR_REF comes from the environment, not from
# build.env), and log.sh would replace this script's log/die.
read_autostart() {
  local lib=${PROVISION_LIB:-${PROVISION_DIR:-/root/provision}/lib} v
  # shellcheck source=/dev/null
  v=$(
    . "$lib/log.sh"
    . "$lib/config.sh"
    config_load "$1" >&2 && printf '%s' "$CFG_ES_AUTOSTART"
  ) || die "Cannot read ES_AUTOSTART from $1"
  CFG_ES_AUTOSTART=$v
}

main() {
  getent passwd "$U" >/dev/null || die "User $U is missing"
  read_autostart "${BUILD_ENV:-${PROVISION_DIR:-/root/provision}/build.env}"
  install_autostart
  install_theme
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
