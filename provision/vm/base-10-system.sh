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

# Base layer VM (amd64/arm64), in the chroot: extra packages and GRUB kernel parameters.
# - cloud-guest-utils (growpart), mesa-utils and libgl1-mesa-dri (software/virtio GL for
#   EmulationStation on the virtio-gpu console).
# - Kernel parameters: drop-in /etc/default/grub.d/50-emulationstation.cfg. It drops every
#   console= word of earlier drop-ins (Debian cloud has its own) and adds ours. Then grub.cfg is
#   made again with update-grub. In a chroot on a loop device update-grub often fails in
#   grub-probe, or writes root=/dev/loopN. Then the old grub.cfg is restored and the "linux"
#   lines are edited with sed. grub-install is NOT run (the image already boots).
# Environment: CFG_TARGET_ARCH (amd64|arm64). PROVISION_ROOT (tests only): path prefix.
set -euo pipefail

log() { printf '[vm/base-10-system] %s\n' "$*" >&2; }
die() {
  printf '[vm/base-10-system] ERROR: %s\n' "$*" >&2
  exit 1
}

# grub_params <arch> : kernel parameters for the console
grub_params() {
  case "$1" in
    amd64) echo "console=tty0 console=ttyS0,115200" ;;
    arm64) echo "console=ttyAMA0" ;;
    *) return 1 ;;
  esac
}

# grub_cfg_set_console <grub.cfg> <params> : on every "linux" line remove all console= words and
# add <params> at the end. Other lines stay as they are. Running it twice changes nothing.
grub_cfg_set_console() {
  sed -i -E "/^[[:space:]]*linux(efi)?[[:space:]]/{s/[[:space:]]console=[^[:space:]]+//g;s|\$| $2|}" "$1"
}

# grub_cfg_has_params <grub.cfg> <params> : at least one linux line, and each has every word
grub_cfg_has_params() {
  local f=$1 w
  grep -qE '^[[:space:]]*linux(efi)?[[:space:]]' "$f" || return 1
  for w in $2; do
    if grep -E '^[[:space:]]*linux(efi)?[[:space:]]' "$f" | grep -qvE "[[:space:]]${w//./\\.}([[:space:]]|\$)"; then
      return 1
    fi
  done
}

# dropin_cmdline <file> : GRUB_CMDLINE_LINUX_DEFAULT after sourcing <file> in a subshell, with
# "quiet console=junk" set before (like an earlier drop-in). Fails on a syntax error.
dropin_cmdline() {
  GRUB_CMDLINE_LINUX_DEFAULT="quiet console=junk" sh -c '. "$1" && printf "%s\n" "$GRUB_CMDLINE_LINUX_DEFAULT"' sh "$1"
}

# Sourced by tests: define the functions only
if [ "${BASH_SOURCE[0]}" != "$0" ]; then return 0; fi

R=${PROVISION_ROOT:-}
arch=${CFG_TARGET_ARCH:?CFG_TARGET_ARCH is missing}
params=$(grub_params "$arch") || die "Unknown architecture: $arch"

apt_opts=(-y -q --no-install-recommends -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold)
log "Installing cloud-guest-utils mesa-utils libgl1-mesa-dri"
apt-get install "${apt_opts[@]}" cloud-guest-utils mesa-utils libgl1-mesa-dri

mkdir -p "$R/etc/default/grub.d"
cat >"$R/etc/default/grub.d/50-emulationstation.cfg" <<DROPIN
# EmuStation Builder: kernel console for the VM. Earlier console= words are dropped.
es_cmdline=""
for es_word in \${GRUB_CMDLINE_LINUX_DEFAULT:-}; do
  case "\$es_word" in
    console=*) ;;
    *) es_cmdline="\$es_cmdline \$es_word" ;;
  esac
done
GRUB_CMDLINE_LINUX_DEFAULT="\${es_cmdline# }\${es_cmdline:+ }$params"
unset es_cmdline es_word
DROPIN
log "GRUB drop-in written ($params)"
# A broken drop-in must not hide behind the sed fallback below
[ "$(dropin_cmdline "$R/etc/default/grub.d/50-emulationstation.cfg")" = "quiet $params" ] ||
  die "The GRUB drop-in does not give 'quiet $params'"

cfg=$R/boot/grub/grub.cfg
bak=$cfg.emulationstation-bak
[ -f "$cfg" ] || die "$cfg is missing"
cp -p "$cfg" "$bak"
rc=0
errf=$(mktemp)
update-grub 2>"$errf" >&2 || rc=$?
cat "$errf" >&2
good=1
if [ "$rc" -ne 0 ]; then
  # Only the known chroot/loop-device problems are tolerated; any other error is real
  grep -qiE 'grub-probe|/dev/loop|canonical path|cannot find a device|failed to get' "$errf" ||
    die "update-grub failed (exit $rc) with an unexpected error"
  log "update-grub failed in the chroot (exit $rc)"
  good=0
elif grep -q 'root=/dev/loop' "$cfg"; then
  log "update-grub wrote a loop device as root"
  good=0
elif ! grub_cfg_has_params "$cfg" "$params"; then
  log "update-grub did not set the parameters"
  good=0
fi
if [ "$good" -eq 0 ]; then
  log "Fallback: old grub.cfg is restored and its linux lines are edited with sed"
  cp -p "$bak" "$cfg"
  grub_cfg_set_console "$cfg" "$params"
  grub_cfg_has_params "$cfg" "$params" || die "No linux line with $params in $cfg"
fi
rm -f "$bak" "$errf"
log "grub.cfg: $params"
