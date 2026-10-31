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

# App layer, in the chroot: VICE (C64) setup, spec 2026-10-31-c64-music-vm-systems-design.md.
#  - 3.2c: joysticks in /opt/retropie/configs/c64/sdl-vicerc: port 1 = keyset 2 (W/S/A/D, fire
#    Return), port 2 = keyset 1 (arrows, fire Space), JoyMenuControl=1 (a pad button opens the
#    menu). RETURN on pad button 2 through the joymap sdl-joymap-C64.vjm (written only when missing).
#  - 3.2: ES system "c64music" for C64 music files (.sid).
#  - The c64music system only when vsid exists. Else the script logs and skips it; a failed
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
VICERC=/opt/retropie/configs/c64/sdl-vicerc
# VICE joymap of x64 (machine name C64); read after the default map, as a patch
JOYMAP=/opt/retropie/configs/c64/sdl-joymap-C64.vjm

# ini_section_set <file> <section> <key=value>... : sets the keys inside [section] of a VICE
# resource file. A key that is there is replaced (once, more copies are removed), a missing key is
# added at the end of the section, a missing section at the end of the file. Other lines stay.
# Temp file + rename, mode 0644. The file is created when missing.
ini_section_set() {
  local file=$1 section=$2 tmp
  shift 2
  [ -f "$file" ] || : >"$file"
  tmp=$(mktemp "$file.XXXXXX")
  if ! awk -v sec="[$section]" -v kvs="$*" '
    BEGIN { n = split(kvs, list, " "); for (i = 1; i <= n; i++) { k = list[i]; sub(/=.*/, "", k); key[i] = k; val[i] = list[i] } }
    function flush(   i) { for (i = 1; i <= n; i++) if (!done[i]) print val[i]; for (i = 1; i <= n; i++) done[i] = 1 }
    /^[[:space:]]*\[/ {
      if (insec) flush()
      t = $0; gsub(/[[:space:]]/, "", t)
      insec = (t == sec)
      if (insec) seen = 1
      print
      next
    }
    insec {
      for (i = 1; i <= n; i++) {
        if ($0 ~ "^[[:space:]]*" key[i] "[[:space:]]*=") {
          if (!done[i]) { print val[i]; done[i] = 1 }
          next
        }
      }
    }
    { print }
    END {
      if (insec) flush()
      if (!seen) { print sec; flush() }
    }' "$file" >"$tmp"; then
    rm -f "$tmp"
    die "Cannot change $file"
  fi
  chmod 0644 "$tmp"
  mv -f "$tmp" "$file"
}

# VICE joystick resources (spec 3.2c, owner's setup). Keys are SDL1 codes (VICE maps SDL2 keys
# to them): Up 273, Down 274, Right 275, Left 276, Space 32, Return 13, a 97, d 100, s 115, w 119.
# JoyDevice: 2 = keyset 1, 3 = keyset 2. "Swap ports" is no resource; VICE starts with it off.
VICE_JOY=(
  JoyDevice1=3 JoyDevice2=2 KeySetEnable=1
  KeySet1North=273 KeySet1South=274 KeySet1West=276 KeySet1East=275 KeySet1Fire=32
  KeySet2North=119 KeySet2South=115 KeySet2West=97 KeySet2East=100 KeySet2Fire=13
  JoyMenuControl=1
)

# set_vice_joysticks : the C64 joystick setup (spec 3.2c), when VICE's config folder exists
set_vice_joysticks() {
  if [ ! -d "$R$(dirname "$VICERC")" ]; then
    log "VICE is not installed, no joystick setup"
    return 0
  fi
  ini_section_set "$R$VICERC" C64 "${VICE_JOY[@]}"
  chown "$U:$U" "$R$VICERC"
  log "VICE joysticks: port 1 = keyset 2 (W/S/A/D, Return), port 2 = keyset 1 (arrows, Space)"
  write_joymap "$R$JOYMAP"
}

# write_joymap <file> : RETURN (C64 key matrix row 0, column 1) on button 2 of pad 1 and 2. Only
# when the file is missing: the owner may have saved an own map in the VICE menu (spec 3.2c).
write_joymap() {
  local file=$1 tmp
  if [ -e "$file" ]; then
    log "VICE joymap exists, not changed: ${file#"$R"}"
    return 0
  fi
  tmp=$(mktemp "$file.XXXXXX")
  printf '%s\n' '# EmuStation Builder: RETURN on pad button 2 (patch on the VICE default map)' \
    '# joynum inputtype(1=button) inputindex action(2=keyboard) row col' \
    '0 1 2 2 0 1' '1 1 2 2 0 1' >"$tmp"
  chmod 0644 "$tmp"
  mv -f "$tmp" "$file"
  chown "$U:$U" "$file"
  log "VICE joymap: RETURN on pad button 2"
}

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
  getent passwd "$U" >/dev/null || die "User $U is missing"
  set_vice_joysticks
  if [ ! -f "$R$VICE/vsid" ] || [ ! -x "$R$VICE/vsid" ]; then
    log "vsid is missing (vice is not installed), no $SYS system"
    return 0
  fi
  [ -f "$R$ES_SYSTEMS" ] || die "$ES_SYSTEMS is missing"

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
