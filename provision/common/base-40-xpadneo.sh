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

# Base layer, in the chroot: xpadneo (Xbox controller over Bluetooth) via DKMS at CFG_XPADNEO_REF.
# It is built for EVERY kernel under /lib/modules/<ver> that is installed (kernel/) and
# has headers (build/Makefile) - never for "uname -r" (that is the build host). An installed
# kernel without headers gets the report line "xpadneo 0 kernel <k> skipped: no headers".
# Upstream "make install" calls udevadm (not usable in the chroot without udevd) and "dkms add";
# both are switched off here and dkms is called directly. Failure -> line in the report
# ("xpadneo <exit> <hint>"), the base layer does not abort (spec 9).
# Environment: CFG_XPADNEO_REF, PROVISION_DIR, PROVISION_REPORT. PROVISION_ROOT: path prefix (tests only).
set -euo pipefail

log() { printf '[base-40-xpadneo] %s\n' "$*" >&2; }

ref=${CFG_XPADNEO_REF:?CFG_XPADNEO_REF is missing}
report=${PROVISION_REPORT:?PROVISION_REPORT is missing}
src=${PROVISION_DIR:?PROVISION_DIR is missing}/xpadneo-src
URL=https://github.com/atar-axis/xpadneo.git
MOD=hid-xpadneo
R=${PROVISION_ROOT:-}

fail() {
  printf 'xpadneo %s %s\n' "$1" "$2" >>"$report"
  log "WARNING: $2 (exit $1) - skipping xpadneo, Xbox controllers then work over USB only"
}

rm -rf "$src"
rc=0
{ git clone -q "$URL" "$src" && git -C "$src" checkout -q --detach "$ref"; } || rc=$?
if [ "$rc" -ne 0 ]; then
  fail "$rc" "cannot download sources $ref"
  exit 0
fi

# Create modprobe.d/udev rules + dkms.conf, without udevadm/dkms
rc=0
make -C "$src" UDEVADM=true DKMS=true install || rc=$?
if [ "$rc" -ne 0 ]; then
  fail "$rc" "make install failed"
  exit 0
fi
ver=$(sed -n 's/^PACKAGE_VERSION="\(.*\)"$/\1/p' "$src/$MOD/dkms.conf")
if [ -z "$ver" ]; then
  fail 1 "PACKAGE_VERSION is missing in dkms.conf"
  exit 0
fi

# Repeat run: remove the old registration of the same version
if [ -d "$R/var/lib/dkms/$MOD/$ver" ]; then
  dkms remove -m "$MOD" -v "$ver" --all || true
fi
rm -rf "$R/usr/src/$MOD-$ver"
rc=0
dkms add "$src/$MOD" || rc=$?
if [ "$rc" -ne 0 ]; then
  fail "$rc" "dkms add failed"
  exit 0
fi

n=0
for d in "$R"/lib/modules/*/; do
  k=$(basename "$d")
  if [ ! -d "$d/kernel" ]; then
    log "Skipping $k (no installed kernel)"
    continue
  fi
  if [ ! -e "$d/build/Makefile" ]; then
    # report line, so the static check (test/rpi4-static.sh) knows why the module is missing
    printf 'xpadneo 0 kernel %s skipped: no headers\n' "$k" >>"$report"
    log "WARNING: skipping kernel $k (no headers); Xbox controllers work over USB only with it"
    continue
  fi
  n=$((n + 1))
  log "Building $MOD/$ver for kernel $k"
  rc=0
  { dkms build -m "$MOD" -v "$ver" -k "$k" && dkms install -m "$MOD" -v "$ver" -k "$k"; } || rc=$?
  if [ "$rc" -ne 0 ]; then
    tail -n 30 "$R/var/lib/dkms/$MOD/$ver/build/make.log" >&2 2>/dev/null || true
    fail "$rc" "dkms build/install -k $k failed"
  fi
done
[ "$n" -gt 0 ] || fail 1 "no kernels with headers under /lib/modules"
rm -rf "$src"
log "xpadneo done"
