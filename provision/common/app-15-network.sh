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

# App layer, in the chroot: network settings from build.conf.
#  - installs the shared Wi-Fi helper /usr/local/lib/emulationstation/nmwifi.sh (root:root 0644;
#    the first boot script uses the same helper for emulationstation.txt),
#  - WIFI_SSID + WIFI_PASSWORD: writes the NetworkManager connection emulationstation-wifi
#    (root, mode 0600). Bad values stop the build: the owner wants Wi-Fi, so a silent skip
#    could leave a headless device that nobody can reach,
#  - rpi4 + WIFI_COUNTRY: sets the Wi-Fi country now (cfg80211.ieee80211_regdom=<CC> in
#    cmdline.txt; raspi-config cannot be used in the chroot, it would change the build host).
#  - rpi4, always: Wi-Fi radio on (nmwifi_radio_on: saved wlan rfkill states "0" and
#    WirelessEnabled=true), because Raspberry Pi OS starts with Wi-Fi blocked
#    (spec 2026-10-31-rpi-wifi-on-design.md).
# Secrets come from build.env (ruling R18), never from the environment or arguments.
# Idempotent. Needs CFG_TARGET_KIND (rpi4|vm, set by step 40).
# Test overrides: PROVISION_ROOT (path prefix), BUILD_ENV (file), PROVISION_LIB (lib dir), CHOWN.
set -euo pipefail

PROVISION_DIR=${PROVISION_DIR:-/root/provision}
BUILD_ENV=${BUILD_ENV:-$PROVISION_DIR/build.env}
PROVISION_LIB=${PROVISION_LIB:-$PROVISION_DIR/lib}
# shellcheck source=builder/lib/log.sh
. "$PROVISION_LIB/log.sh"
# shellcheck source=builder/lib/config.sh
. "$PROVISION_LIB/config.sh"
config_load "$BUILD_ENV"

log() { printf '[app-15-network] %s\n' "$*" >&2; }
die() {
  printf '[app-15-network] ERROR: %s\n' "$*" >&2
  exit 1
}

R=${PROVISION_ROOT:-}
CHOWN=${CHOWN:-chown}
kind=${CFG_TARGET_KIND:-}
case "$kind" in
  rpi4 | vm) ;;
  *) die "CFG_TARGET_KIND must be rpi4 or vm (got '$kind')" ;;
esac

lib=$R/usr/local/lib/emulationstation/nmwifi.sh
[ -f "$lib" ] || die "Wi-Fi helper is missing: /usr/local/lib/emulationstation/nmwifi.sh (step 40 copies overlay/)"
"$CHOWN" root:root "$lib"
chmod 0644 "$lib"
# shellcheck source=overlay/usr/local/lib/emulationstation/nmwifi.sh
. "$lib"

# set_regdom <cmdline.txt> <country> : one cfg80211.ieee80211_regdom=<country> on the kernel line
set_regdom() {
  local file=$1 cc=$2 line w words=() new
  line=$(head -n 1 "$file")
  read -ra words <<<"$line"
  new=""
  for w in "${words[@]}"; do
    case "$w" in cfg80211.ieee80211_regdom=*) continue ;; esac
    new+="${new:+ }$w"
  done
  new+="${new:+ }cfg80211.ieee80211_regdom=$cc"
  if [ "$new" != "$line" ]; then
    printf '%s\n' "$new" >"$file"
  fi
}


ssid=${CFG_WIFI_SSID:-}
psk=${CFG_WIFI_PASSWORD:-}
cc=${CFG_WIFI_COUNTRY:-}

# --- Wi-Fi connection ---------------------------------------------------------
if [ -n "$ssid" ]; then
  nmwifi_ssid_ok "$ssid" || die "WIFI_SSID is longer than 32 bytes"
  [ -n "$psk" ] || die "WIFI_PASSWORD is empty (open networks are not supported)"
  nmwifi_psk_ok "$psk" || die "WIFI_PASSWORD must have 8 to 63 printable ASCII characters, or 64 hex digits"
  nmwifi_write "$R$NMWIFI_FILE" "$ssid" "$psk" || die "Could not write $NMWIFI_FILE"
  "$CHOWN" root:root "$R$NMWIFI_FILE"
  log "Wi-Fi connection written for SSID $ssid"
elif [ -n "$psk" ]; then
  die "WIFI_PASSWORD is set, but WIFI_SSID is empty"
fi

# --- Wi-Fi country (Raspberry Pi) -----------------------------------------------
if [ "$kind" != rpi4 ]; then
  [ -z "$cc" ] || log "WIFI_COUNTRY is only used on the Raspberry Pi, ignored"
  exit 0
fi
if [ -n "$cc" ]; then
  nmwifi_country_ok "$cc" || die "WIFI_COUNTRY must be two capital letters (for example DE, US, GB)"
  cmdline=$R/boot/firmware/cmdline.txt
  [ -f "$cmdline" ] || die "cmdline.txt is missing: /boot/firmware/cmdline.txt"
  set_regdom "$cmdline" "$cc"
  log "Wi-Fi country $cc"
else
  log "WARNING: WIFI_COUNTRY is empty. Wi-Fi uses the world domain (fewer channels)."
fi
# Wi-Fi on from the start, with and without SSID and country (spec 2026-10-31-rpi-wifi-on-design.md)
nmwifi_radio_on "$R" || die "Could not turn the Wi-Fi radio on"
log "Wi-Fi radio on (rfkill state and NetworkManager.state)"
