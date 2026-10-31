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

# App layer, in the chroot: shell tools for the owner (spec 2026-10-31-c64-music-vm-systems-design.md
# 3.2a): mc (Midnight Commander) and net-tools (ifconfig, netstat, route).
# This file is not part of the base layer, so the base cache key does not change.
# apt is called only for missing packages. Idempotent. When sourced, only the functions load.
set -euo pipefail

log() { printf '[app-12-tools] %s\n' "$*" >&2; }

TOOLS="mc net-tools"

# missing_packages <pkg>... : prints the packages that dpkg does not know as installed
missing_packages() {
  local p
  for p in "$@"; do
    dpkg -s "$p" >/dev/null 2>&1 || printf '%s\n' "$p"
  done
}

main() {
  local missing
  # shellcheck disable=SC2086 # TOOLS is a fixed word list
  missing=$(missing_packages $TOOLS | tr '\n' ' ')
  missing=${missing% }
  if [ -z "$missing" ]; then
    log "Already installed: $TOOLS"
    return 0
  fi
  DEBIAN_FRONTEND=noninteractive apt-get update </dev/null
  # shellcheck disable=SC2086 # one word per package
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends $missing </dev/null
  log "Installed: $missing"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
