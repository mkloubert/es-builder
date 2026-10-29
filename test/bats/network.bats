#!/usr/bin/env bats
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


# provision/common/app-15-network.sh (Wi-Fi from build.conf, Pi Wi-Fi country at build time)
# and the WIFI_COUNTRY line of provision/common/app-40-firstboot.sh, with a fake root.
# shellcheck disable=SC2016,SC2030,SC2031,SC2034

bats_require_minimum_version 1.5.0

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  T="$BATS_TEST_TMPDIR"
  R="$T/root"
  STUBS="$T/stubs"
  CALLS="$T/calls"
  mkdir -p "$STUBS" "$R/usr/local/lib/emulationstation" "$R/boot/firmware" "$R/var/lib/systemd/rfkill"
  cp "$ROOT/overlay/usr/local/lib/emulationstation/nmwifi.sh" "$R/usr/local/lib/emulationstation/"
  printf 'console=serial0,115200 console=tty1 root=PARTUUID=abc-02 rootfstype=ext4 fsck.repair=yes rootwait\n' \
    >"$R/boot/firmware/cmdline.txt"
  printf '1\n' >"$R/var/lib/systemd/rfkill/platform-fe300000.mmcnr:wlan"
  printf '1\n' >"$R/var/lib/systemd/rfkill/platform-3f300000.mmcnr:wlan"
  printf '1\n' >"$R/var/lib/systemd/rfkill/platform-xyz:bluetooth"
  printf '#!/usr/bin/env bash\necho "chown $*" >>"$CALLS"\n' >"$STUBS/chown"
  chmod +x "$STUBS/chown"
  : >"$CALLS"
  export CALLS
}

# net_env [KEY=VALUE...] : build.env like step 40 writes it (all values in double quotes)
net_env() {
  local kv
  : >"$T/build.env"
  for kv in "$@"; do printf '%s="%s"\n' "${kv%%=*}" "${kv#*=}" >>"$T/build.env"; done
}

run_net() {
  run env PROVISION_ROOT="$R" BUILD_ENV="$T/build.env" PROVISION_LIB="$ROOT/builder/lib" \
    CHOWN="$STUBS/chown" CFG_TARGET_KIND="${KIND:-rpi4}" bash "$ROOT/provision/common/app-15-network.sh"
}

nm() { echo "$R/etc/NetworkManager/system-connections/emulationstation-wifi.nmconnection"; }

@test "app-15: SSID and password -> 0600 connection, no password in the log" {
  net_env WIFI_SSID='My Net' WIFI_PASSWORD='pass word;#1' WIFI_COUNTRY=
  run_net
  [ "$status" -eq 0 ]
  [ "$(stat -c %a "$(nm)")" = 600 ]
  grep -qx 'ssid=My Net' "$(nm)"
  grep -qx 'psk=pass word;#1' "$(nm)"
  grep -qx 'key-mgmt=wpa-psk' "$(nm)"
  grep -qx "chown root:root $(nm)" "$CALLS"
  grep -qx "chown root:root $R/usr/local/lib/emulationstation/nmwifi.sh" "$CALLS"
  [ "$(stat -c %a "$R/usr/local/lib/emulationstation/nmwifi.sh")" = 644 ]
  [[ $output != *"pass word"* ]]
}

@test "app-15: the same file as firstboot writes for the same values" {
  net_env WIFI_SSID=' lead;x#y\ ' WIFI_PASSWORD='  p;a#s\s\\ w  ' WIFI_COUNTRY=
  run_net
  [ "$status" -eq 0 ]
  local fr="$T/fb" boot="$T/boot"
  mkdir -p "$fr/etc/ssh" "$boot"
  : >"$fr/etc/ssh/ssh_host_ed25519_key"
  printf 'WIFI_SSID=" lead;x#y\\ "\nWIFI_PASSWORD="  p;a#s\\s\\\\ w  "\n' >"$boot/emulationstation.txt"
  run env ROOT="$fr" BOOT_DIR="$boot" TARGET_KIND=rpi4 RFKILL=true WIFI_COUNTRY_CMD=true \
    bash "$ROOT/overlay/usr/local/lib/emulationstation/firstboot"
  [ "$status" -eq 0 ]
  cmp "$(nm)" "$fr/etc/NetworkManager/system-connections/emulationstation-wifi.nmconnection"
}

@test "app-15: no SSID -> no connection; bad values stop the build" {
  net_env WIFI_SSID= WIFI_PASSWORD= WIFI_COUNTRY=
  run_net
  [ "$status" -eq 0 ]
  [ ! -e "$(nm)" ]
  net_env WIFI_SSID=Home WIFI_PASSWORD=short
  run_net
  [ "$status" -ne 0 ]
  [[ $output == *"WIFI_PASSWORD must have 8 to 63"* ]]
  net_env WIFI_SSID=Home WIFI_PASSWORD=
  run_net
  [ "$status" -ne 0 ]
  net_env WIFI_SSID= WIFI_PASSWORD=password123
  run_net
  [ "$status" -ne 0 ]
  net_env WIFI_SSID="$(printf 's%.0s' {1..33})" WIFI_PASSWORD=password123
  run_net
  [ "$status" -ne 0 ]
  [ ! -e "$(nm)" ]
}

@test "app-15 rpi4: WIFI_COUNTRY -> regdom in cmdline.txt (once, replaced), wlan unblocked; idempotent" {
  net_env WIFI_SSID= WIFI_PASSWORD= WIFI_COUNTRY=DE
  run_net
  [ "$status" -eq 0 ]
  [ "$(cat "$R/boot/firmware/cmdline.txt")" = 'console=serial0,115200 console=tty1 root=PARTUUID=abc-02 rootfstype=ext4 fsck.repair=yes rootwait cfg80211.ieee80211_regdom=DE' ]
  [ "$(cat "$R/var/lib/systemd/rfkill/platform-fe300000.mmcnr:wlan")" = 0 ]
  [ "$(cat "$R/var/lib/systemd/rfkill/platform-3f300000.mmcnr:wlan")" = 0 ]
  [ "$(cat "$R/var/lib/systemd/rfkill/platform-xyz:bluetooth")" = 1 ]
  run_net
  [ "$(grep -o 'regdom=' "$R/boot/firmware/cmdline.txt" | wc -l)" -eq 1 ]
  [ "$(wc -l <"$R/boot/firmware/cmdline.txt")" -eq 1 ]
  net_env WIFI_COUNTRY=US
  run_net
  [ "$status" -eq 0 ]
  grep -q ' rootwait cfg80211.ieee80211_regdom=US$' "$R/boot/firmware/cmdline.txt"
  run ! grep -q 'regdom=DE' "$R/boot/firmware/cmdline.txt"
}

@test "app-15 rpi4: a bad WIFI_COUNTRY stops the build, cmdline.txt stays" {
  local before bad
  before=$(cat "$R/boot/firmware/cmdline.txt")
  for bad in de DEU 'D1' 'DE x' '$(id)'; do
    net_env WIFI_COUNTRY="$bad"
    run_net
    [ "$status" -ne 0 ] || { echo "accepted: $bad"; return 1; }
  done
  [ "$(cat "$R/boot/firmware/cmdline.txt")" = "$before" ]
}

@test "app-15 rpi4: SSID without country -> warning, wlan unblocked, no regdom" {
  net_env WIFI_SSID=Home WIFI_PASSWORD=password123 WIFI_COUNTRY=
  run_net
  [ "$status" -eq 0 ]
  [[ $output == *"WIFI_COUNTRY is empty"* ]]
  [ "$(cat "$R/var/lib/systemd/rfkill/platform-fe300000.mmcnr:wlan")" = 0 ]
  run ! grep -q regdom "$R/boot/firmware/cmdline.txt"
}

@test "app-15 vm: country ignored, cmdline.txt and rfkill untouched" {
  net_env WIFI_SSID=Home WIFI_PASSWORD=password123 WIFI_COUNTRY=DE
  KIND=vm run_net
  [ "$status" -eq 0 ]
  [ -f "$(nm)" ]
  run ! grep -q regdom "$R/boot/firmware/cmdline.txt"
  [ "$(cat "$R/var/lib/systemd/rfkill/platform-fe300000.mmcnr:wlan")" = 1 ]
}

# --- app-40-firstboot.sh: the build's country in firstboot.conf -------------------------

fb_root() {
  mkdir -p "$R/etc/systemd/system" "$R/boot-template"
  cp "$ROOT/overlay/usr/local/lib/emulationstation/firstboot" "$R/usr/local/lib/emulationstation/"
  cp "$ROOT/overlay/etc/systemd/system/emulationstation-firstboot.service" "$R/etc/systemd/system/"
  cp "$ROOT/overlay/boot-template/emulationstation.txt.example" "$R/boot-template/"
  printf '#!/usr/bin/env bash\n' >"$STUBS/systemctl"
  chmod +x "$STUBS/systemctl"
}

run_fb40() {
  run env PATH="$STUBS:$PATH" PROVISION_ROOT="$R" BUILD_ENV="$T/build.env" PROVISION_LIB="$ROOT/builder/lib" \
    CHOWN="$STUBS/chown" CFG_TARGET_KIND="${KIND:-rpi4}" bash "$ROOT/provision/common/app-40-firstboot.sh"
}

@test "app-40 rpi4: WIFI_COUNTRY of build.env goes to firstboot.conf; a bad one does not" {
  fb_root
  net_env WIFI_COUNTRY=GB
  run_fb40
  [ "$status" -eq 0 ]
  grep -qx 'WIFI_COUNTRY=GB' "$R/usr/local/lib/emulationstation/firstboot.conf"
  grep -qx 'TARGET_KIND=rpi4' "$R/usr/local/lib/emulationstation/firstboot.conf"
  fb_root
  net_env 'WIFI_COUNTRY=G B'
  run_fb40
  [ "$status" -eq 0 ]
  run ! grep -q WIFI_COUNTRY "$R/usr/local/lib/emulationstation/firstboot.conf"
}

@test "app-40 vm: no WIFI_COUNTRY in firstboot.conf" {
  mkdir -p "$R/boot/efi"
  fb_root
  net_env WIFI_COUNTRY=GB
  KIND=vm run_fb40
  [ "$status" -eq 0 ]
  run ! grep -q WIFI_COUNTRY "$R/usr/local/lib/emulationstation/firstboot.conf"
}
