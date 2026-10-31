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

# App layer, in the chroot: ES system "c64music" for C64 music files (.sid), spec
# 2026-10-31-c64-music-vm-systems-design.md 3.2.
#  - Only when VICE is installed (vsid exists). Else the script logs and does nothing; a failed
#    vice build is already in the build report.
#  - Writes /opt/retropie/configs/c64music/emulators.cfg (default vice-vsid), the ROM folder, and
#    one <system> in /etc/emulationstation/es_systems.cfg, right after the c64 system.
# Checked at RetroPie-Setup f5ebfba6: vice.sh (made by configure_vice) takes the program name as
# first argument; VICE 3.7.1 builds and installs vsid. x64 cannot start a .sid file.
# Idempotent. PROVISION_ROOT (tests only): path prefix. When sourced, only the functions load.
set -euo pipefail

log() { printf '[app-25-c64music] %s\n' "$*" >&2; }
die() {
  printf '[app-25-c64music] ERROR: %s\n' "$*" >&2
  exit 1
}

U=emulationstation
R=${PROVISION_ROOT:-}
SYS=c64music
VICE=/opt/retropie/emulators/vice/bin
ES_SYSTEMS=/etc/emulationstation/es_systems.cfg
ROM_DIR=/home/$U/RetroPie/roms/$SYS
CONF_DIR=/opt/retropie/configs/$SYS
RUNCOMMAND=/opt/retropie/supplementary/runcommand/runcommand.sh

# write_emulators <file> : runcommand's emulators.cfg for c64music (temp file + rename, 0644)
write_emulators() {
  local file=$1 tmp
  mkdir -p "$(dirname "$file")"
  tmp=$(mktemp "$file.XXXXXX")
  printf '%s\n' "vice-vsid = \"$VICE/vice.sh vsid %ROM%\"" 'default = "vice-vsid"' >"$tmp"
  chmod 0644 "$tmp"
  mv -f "$tmp" "$file"
}

# add_system <es_systems.cfg> : removes every c64music system and adds one after the c64 system
# (or at the end). Temp file + rename, mode 0644. Fails when the result is not valid XML.
add_system() {
  local file=$1 tmp where=(-s /systemList) n
  tmp=$(mktemp "$file.XXXXXX")
  n=$(xmlstarlet sel -t -v "count(/systemList/system[name='c64'])" "$file") ||
    die "Cannot read $ES_SYSTEMS"
  [ "$n" = 0 ] || where=(-a "/systemList/system[name='c64'][1]")
  if ! xmlstarlet ed \
    -d "/systemList/system[name='$SYS']" \
    "${where[@]}" -t elem -n newsys \
    -s //newsys -t elem -n name -v "$SYS" \
    -s //newsys -t elem -n fullname -v "Commodore 64 Music" \
    -s //newsys -t elem -n path -v "$ROM_DIR" \
    -s //newsys -t elem -n extension -v ".sid .SID" \
    -s //newsys -t elem -n command -v "$RUNCOMMAND 0 _SYS_ $SYS %ROM%" \
    -s //newsys -t elem -n platform -v c64 \
    -s //newsys -t elem -n theme -v c64 \
    -r //newsys -v system \
    "$file" >"$tmp"; then
    rm -f "$tmp"
    die "Cannot change $ES_SYSTEMS"
  fi
  if ! xmlstarlet val -q -w "$tmp"; then
    rm -f "$tmp"
    die "The new $ES_SYSTEMS is not valid XML"
  fi
  chmod 0644 "$tmp"
  mv -f "$tmp" "$file"
}

main() {
  if [ ! -f "$R$VICE/vsid" ] || [ ! -x "$R$VICE/vsid" ]; then
    log "vsid is missing (vice is not installed), no $SYS system"
    return 0
  fi
  [ -f "$R$ES_SYSTEMS" ] || die "$ES_SYSTEMS is missing"
  getent passwd "$U" >/dev/null || die "User $U is missing"

  write_emulators "$R$CONF_DIR/emulators.cfg"
  chown -R "$U:$U" "$R$CONF_DIR"
  mkdir -p "$R$ROM_DIR"
  chmod 0755 "$R$ROM_DIR"
  chown "$U:$U" "$R$ROM_DIR"
  add_system "$R$ES_SYSTEMS"
  log "System $SYS (.sid, vsid) is set up"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
