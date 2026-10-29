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

# Base layer Raspberry Pi 4, in the chroot: config.txt, zram swap, first-boot mechanism.
#
# First boot of the pinned Raspberry Pi OS Lite trixie 2026-10-06 (ruling R6). Sources:
# pi-gen b2cd98a9 (per .info of the release), raspberrypi-sys-mods 1:20260914 / userconf-pi 0.19 /
# rpi-cloud-init-mods (branch pios/trixie) and the package list of the release (.info):
# - cmdline.txt ends with "... rootwait resize", NO init=/usr/lib/raspberrypi-sys-mods/firstboot
#   any more. Growing happens in two stages:
#   1. initramfs-tools/scripts/local-premount/resize_early (raspberrypi-sys-mods): if " resize"
#      is in /proc/cmdline, parted grows the root partition (only if it is p2) to the end of the
#      medium; the disk ID is kept.
#   2. rpi-resize.service (ConditionFirstBoot=yes, WantedBy=sysinit.target, enabled by pi-gen)
#      pulls in systemd-growfs-root.service (ext4 grows) and disables itself afterwards.
#   -> "resize" must stay in cmdline.txt, rpi-resize.service must be enabled, and the finished image
#      needs /etc/machine-id = "uninitialized" (ConditionFirstBoot; pi-gen does it this way, Task 11).
# - cloud-init 25.2 + rpi-cloud-init-mods are installed (NoCloud, seedfrom /boot/firmware,
#   with user-data/meta-data/network-config for the Raspberry Pi Imager). cloud-init could
#   create users and change hostname/network/SSH. It is NOT the resize mechanism (growpart/
#   resizefs are not needed) -> switched off completely with /etc/cloud/cloud-init.disabled.
#   Imager customisations therefore have no effect; the replacement is emulationstation.txt (spec 5.4).
# - userconf-pi: pi-gen calls "rename-user -f -s" -> userconfig.service active, getty@tty1 off,
#   sshd banner; base-20-user.sh handles this (masks it, removes pi, tty1 on again).
# - raspberrypi-sys-mods: regenerate_ssh_host_keys.service (first boot, ssh-keygen -A) stays;
#   sshswitch.service enables ssh only if /boot/firmware/ssh exists - harmless, the port comes from the
#   app layer (11911), port 22 is not opened.
# - rpi-swap is installed (zram0 + swap file via systemd-zram-generator). Spec 5.1 wants
#   zram-tools -> switch rpi-swap off with a drop-in "Mechanism=none", otherwise there are two zram swaps.
#
# PROVISION_ROOT (tests only) sets a path prefix; empty in the chroot.
set -euo pipefail

log() { printf '[rpi4/base-10-system] %s\n' "$*" >&2; }
die() {
  printf '[rpi4/base-10-system] ERROR: %s\n' "$*" >&2
  exit 1
}

R=${PROVISION_ROOT:-}
CONFIG_TXT=$R/boot/firmware/config.txt
CMDLINE_TXT=$R/boot/firmware/cmdline.txt

# --- config.txt ----------------------------------------------------------------
[ -f "$CONFIG_TXT" ] || die "$CONFIG_TXT is missing"
# Bluetooth stays on (spec 5.5)
sed -i -E '/^[[:space:]]*dtoverlay=(pi3-)?disable-bt([[:space:],].*)?$/d' "$CONFIG_TXT"
missing=()
for line in dtoverlay=vc4-kms-v3d disable_overscan=1; do
  grep -qxF "$line" "$CONFIG_TXT" || missing+=("$line")
done
if [ "${#missing[@]}" -gt 0 ]; then
  log "config.txt: adding ${missing[*]}"
  {
    printf '\n# EmuStation Builder\n[all]\n'
    printf '%s\n' "${missing[@]}"
  } >>"$CONFIG_TXT"
fi

# --- Root growing at first boot ------------------------------------
[ -f "$CMDLINE_TXT" ] || die "$CMDLINE_TXT is missing"
grep -qE '(^|[[:space:]])resize([[:space:]]|$)' "$CMDLINE_TXT" ||
  die "cmdline.txt without 'resize': the root partition would not grow at first boot"
systemctl enable rpi-resize.service

# --- cloud-init off ----------------------------------------------------------
if [ -d "$R/etc/cloud" ]; then
  log "cloud-init disabled"
  : >"$R/etc/cloud/cloud-init.disabled"
fi

# --- zram swap (zram-tools), rpi-swap off -------------------------------------
ZRAM=$R/etc/default/zramswap
[ -f "$ZRAM" ] || die "$ZRAM is missing (package zram-tools)"
set_kv() {
  local key=$1 val=$2 file=$3
  if grep -qE "^#?[[:space:]]*$key=" "$file"; then
    sed -i -E "0,/^#?[[:space:]]*$key=.*/s//$key=$val/" "$file"
    # remove further (duplicate) assignments
    awk -v k="$key=" 'index($0, k) == 1 { if (seen++) next } { print }' "$file" >"$file.new"
    cat "$file.new" >"$file"
    rm -f "$file.new"
  else
    printf '%s=%s\n' "$key" "$val" >>"$file"
  fi
}
set_kv ALGO zstd "$ZRAM"
set_kv PERCENT 50 "$ZRAM"
systemctl enable zramswap.service

if [ -f "$R/etc/rpi/swap.conf" ]; then
  log "rpi-swap: Mechanism=none (zram-tools takes over)"
  mkdir -p "$R/etc/rpi/swap.conf.d"
  printf '# EmuStation Builder: swap comes from zram-tools (/etc/default/zramswap)\n[Main]\nMechanism=none\n' \
    >"$R/etc/rpi/swap.conf.d/80-emulationstation.conf"
fi
