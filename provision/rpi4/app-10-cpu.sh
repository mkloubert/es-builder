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
# App layer Raspberry Pi 4, in the chroot: enables emulationstation-cpu.service, which applies the
# saved top CPU speed at boot (priv cpu-apply; spec 2026-10-31-cpu-limit-design.md 3.2). The unit
# file comes from the overlay. The VM images do not enable it.
# Idempotent. PROVISION_ROOT (tests only) sets a path prefix; empty in the chroot.
set -euo pipefail

log() { printf '[rpi4/app-10-cpu] %s\n' "$*" >&2; }
die() {
  printf '[rpi4/app-10-cpu] ERROR: %s\n' "$*" >&2
  exit 1
}

R=${PROVISION_ROOT:-}
UNIT=/etc/systemd/system/emulationstation-cpu.service

[ -f "$R$UNIT" ] || die "Unit file is missing: $UNIT (overlay)"
chown root:root "$R$UNIT" 2>/dev/null || true
chmod 0644 "$R$UNIT"
systemctl enable emulationstation-cpu.service
log "emulationstation-cpu.service enabled"
