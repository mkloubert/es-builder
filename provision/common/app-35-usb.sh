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

# App layer, in the chroot: USB import check for the kernel modules (spec 2026-10-28, section 5).
#  - For every kernel in /lib/modules, vfat, exfat and ntfs3 must be there: a module file, or built
#    into the kernel (modinfo knows both; modules.builtin is checked too).
#  - For each missing module the line "usb <module> missing for kernel <release>" is written to
#    /usr/local/lib/emulationstation/build-report.txt (the file is removed when nothing is missing).
#    test/rpi4-static.sh and test/vm-static.sh read it. A missing ntfs3 is only a warning there; a
#    missing vfat or exfat fails the check. The build goes on in every case.
# This file is not part of the base layer, so the base cache key does not change.
# Idempotent. Test override: PROVISION_ROOT (path prefix).
set -euo pipefail

log() { printf '[app-35-usb] %s\n' "$*" >&2; }

R=${PROVISION_ROOT:-}
REPORT=$R/usr/local/lib/emulationstation/build-report.txt

# module_present <release> <module>
module_present() {
  modinfo -b "${R:-/}" -k "$1" "$2" >/dev/null 2>&1 ||
    grep -q "/$2\.ko" "$R/lib/modules/$1/modules.builtin" 2>/dev/null
}

mkdir -p "$(dirname "$REPORT")"
tmp=$(mktemp "$REPORT.XXXXXX")
trap 'rm -f "$tmp"' EXIT
for d in "$R"/lib/modules/*/; do
  [ -d "$d/kernel" ] || continue
  k=$(basename "$d")
  for m in vfat exfat ntfs3; do
    if module_present "$k" "$m"; then
      log "kernel $k has $m"
    else
      log "WARNING: kernel $k has no $m"
      printf 'usb %s missing for kernel %s\n' "$m" "$k" >>"$tmp"
    fi
  done
done
if [ -s "$tmp" ]; then
  mv -f "$tmp" "$REPORT"
  chmod 644 "$REPORT"
else
  rm -f "$REPORT"
fi
