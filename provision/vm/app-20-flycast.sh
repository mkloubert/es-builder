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

# App layer, VM only, in the chroot: flycast (Dreamcast) gets an OpenGL 4.3 context on virgl
# (spec 2026-10-31-c64-music-vm-systems-design.md 3.2d). Mesa on virgl reports a lower GL version,
# so the context fails; the override in runcommand's emulators.cfg fixes that.
# Idempotent. PROVISION_ROOT (tests only): path prefix. When sourced, only the functions load.
set -euo pipefail

log() { printf '[vm/app-20-flycast] %s\n' "$*" >&2; }
die() {
  printf '[vm/app-20-flycast] ERROR: %s\n' "$*" >&2
  exit 1
}

U=emulationstation
R=${PROVISION_ROOT:-}
EMU_CFG=/opt/retropie/configs/dreamcast/emulators.cfg
GL_ENV="MESA_GL_VERSION_OVERRIDE=4.3 MESA_GLSL_VERSION_OVERRIDE=430"

# set_gl_override <emulators.cfg> : puts GL_ENV in front of the flycast command. An old prefix of
# NAME=value words is removed first. Other lines stay. Temp file + rename, mode 0644.
# Returns 2 when the file has no flycast line.
set_gl_override() {
  local file=$1 tmp
  grep -q '^flycast = "' "$file" || return 2
  tmp=$(mktemp "$file.XXXXXX")
  if ! sed -E -e 's#^flycast = "([A-Z_][A-Z0-9_]*=[^ "]* )*#flycast = "#' \
    -e "s#^flycast = \"#flycast = \"$GL_ENV #" "$file" >"$tmp"; then
    rm -f "$tmp"
    die "Cannot change $EMU_CFG"
  fi
  chmod 0644 "$tmp"
  mv -f "$tmp" "$file"
}

main() {
  local rc=0
  if [ ! -f "$R$EMU_CFG" ]; then
    log "flycast is not installed ($EMU_CFG is missing), nothing to do"
    return 0
  fi
  set_gl_override "$R$EMU_CFG" || rc=$?
  if [ "$rc" -eq 2 ]; then
    log "No flycast line in $EMU_CFG, nothing to do"
    return 0
  fi
  chown "$U:$U" "$R$EMU_CFG"
  log "flycast: OpenGL 4.3 override for virgl is set"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
