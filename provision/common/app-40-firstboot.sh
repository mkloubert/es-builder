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

# App layer, in the chroot: first boot setup.
#  - makes the script /usr/local/lib/emulationstation/firstboot executable (root:root 0755),
#  - writes /usr/local/lib/emulationstation/firstboot.conf (TARGET_KIND, BOOT_DIR and on rpi4 the
#    WIFI_COUNTRY of build.env; the script reads it on the device; /etc/emulationstation is owned
#    by RetroPie, so it is not used),
#  - writes the drop-in emulationstation-firstboot.service.d/boot-dir.conf with
#    RequiresMountsFor=<BOOT_DIR> (the path differs per target),
#  - copies the template /boot-template/emulationstation.txt.example (from overlay/) to the boot
#    partition (mounted at /boot/firmware for rpi4, /boot/efi for vm) and removes /boot-template,
#  - enables the unit.
# The boot partition is FAT: the template is copied with cat, without chown or chmod.
# Idempotent. Needs CFG_TARGET_KIND (rpi4|vm, set by step 40).
# Test overrides: PROVISION_ROOT, CHOWN, BUILD_ENV, PROVISION_LIB.
set -euo pipefail

log() { printf '[app-40-firstboot] %s\n' "$*" >&2; }
die() {
  printf '[app-40-firstboot] ERROR: %s\n' "$*" >&2
  exit 1
}

R=${PROVISION_ROOT:-}
CHOWN=${CHOWN:-chown}
PROVISION_DIR=${PROVISION_DIR:-/root/provision}
BUILD_ENV=${BUILD_ENV:-$PROVISION_DIR/build.env}
PROVISION_LIB=${PROVISION_LIB:-$PROVISION_DIR/lib}
kind=${CFG_TARGET_KIND:-}
case "$kind" in
  rpi4) boot=/boot/firmware ;;
  vm) boot=/boot/efi ;;
  *) die "CFG_TARGET_KIND must be rpi4 or vm (got '$kind')" ;;
esac

lib=$R/usr/local/lib/emulationstation
unit=$R/etc/systemd/system/emulationstation-firstboot.service
tpl=$R/boot-template/emulationstation.txt.example

[ -f "$lib/firstboot" ] || die "Script is missing: /usr/local/lib/emulationstation/firstboot (step 40 copies overlay/)"
[ -f "$unit" ] || die "Unit file is missing: emulationstation-firstboot.service"
[ -f "$tpl" ] || die "Template is missing: /boot-template/emulationstation.txt.example"
[ -d "$R$boot" ] || die "Boot partition is not mounted: $boot"

"$CHOWN" root:root "$lib/firstboot" "$unit"
chmod 0755 "$lib/firstboot"
chmod 0644 "$unit"

# rpi4: the Wi-Fi country of the build. firstboot uses it when emulationstation.txt sets a
# Wi-Fi network without WIFI_COUNTRY (it sets the channels of the country).
# Read in a subshell, so log.sh does not replace this script's log/die.
country=""
if [ "$kind" = rpi4 ] && [ -f "$BUILD_ENV" ]; then
  # shellcheck source=/dev/null
  country=$(
    . "$PROVISION_LIB/log.sh"
    . "$PROVISION_LIB/config.sh"
    config_load "$BUILD_ENV" >/dev/null
    printf '%s' "${CFG_WIFI_COUNTRY:-}"
  ) || die "Cannot read $BUILD_ENV"
  [[ $country =~ ^[A-Z]{2}$ ]] || country=""
fi

{
  printf 'TARGET_KIND=%s\nBOOT_DIR=%s\n' "$kind" "$boot"
  if [ -n "$country" ]; then printf 'WIFI_COUNTRY=%s\n' "$country"; fi
} >"$lib/firstboot.conf"
chmod 0644 "$lib/firstboot.conf"

mkdir -p "$unit.d"
printf '[Unit]\nRequiresMountsFor=%s\n' "$boot" >"$unit.d/boot-dir.conf"
chmod 0644 "$unit.d/boot-dir.conf"

cat "$tpl" >"$R$boot/emulationstation.txt.example"
rm -rf "$R/boot-template"
log "Template copied to $boot"

systemctl enable emulationstation-firstboot.service
log "emulationstation-firstboot.service enabled ($kind, $boot)"
