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

# Base layer, in the chroot: system packages. Kernel headers only if not installed yet:
# otherwise "apt-get install" would upgrade an existing header package to a newer version
# whose kernel is not in the image (DKMS would then build against a foreign kernel).
# Environment: CFG_TARGET_KIND (rpi4|vm), CFG_TARGET_ARCH (arm64|amd64).
#
# VM: the pinned Debian 12 nocloud image ships the generic kernel "linux-image-<arch>"
# (checked in the package list of debian-12-nocloud-*-20261026-2643.json; there is no
# linux-image-cloud-*). It has DRM and virtio-gpu, so no kernel change is needed. The headers
# must match the INSTALLED kernel version (xpadneo DKMS builds from /lib/modules/<ver>/build), so
# we install linux-headers-<release> of that kernel. If the archive no longer has it (a newer
# kernel replaced it), we install kernel and headers meta packages together, so versions match.
# This is the normal case for an older pinned image. Then (a) the kernel hook zz-update-grub is
# switched off during apt, because update-grub often fails in grub-probe in a chroot (the
# update-grub in vm/base-10-system.sh makes grub.cfg later), and (b) the older kernel packages
# are purged, so exactly one kernel with headers is left (xpadneo is built for it).
#
# Raspberry Pi: Raspberry Pi OS ships two kernels (rpi-v8 for the Pi 4, rpi-2712 for the Pi 5) and
# keeps both on purpose. The same rule is used for EACH installed release (rpi_headers_pkgs). When
# the meta packages bring a newer kernel, the old kernel of that flavour is removed only if the
# boot partition already has the new one (rpi_remove_stale). A kernel that still has no headers
# gets a report line from base-40-xpadneo.sh.
set -euo pipefail

log() { printf '[base-10-packages] %s\n' "$*" >&2; }

# vm_headers_pkgs <arch|flavour> <installed-kernel-release|empty> <available 1|0>
# Prints the packages (one per line) that give matching kernel headers. The Pi uses it with
# its kernel flavour (rpi-v8, rpi-2712) in place of the arch: the meta packages have the same form.
vm_headers_pkgs() {
  local arch=$1 rel=$2 available=$3
  if [ -n "$rel" ] && [ "$available" = 1 ]; then
    printf 'linux-headers-%s\n' "$rel"
  else
    printf 'linux-image-%s\nlinux-headers-%s\n' "$arch" "$arch"
  fi
}

# old_kernel_pkgs <arch> <keep-release> : reads installed package names (one per line), prints the
# kernel image/header packages of every other version (linux-image-<ver>-<arch>,
# linux-headers-<ver>-<arch>, linux-headers-<ver>-common). Meta packages are not touched.
old_kernel_pkgs() {
  local arch=$1 keep=${2%-"$1"} p rest ver
  while IFS= read -r p; do
    case "$p" in
      linux-image-[0-9]* | linux-headers-[0-9]*) ;;
      *) continue ;;
    esac
    rest=${p#linux-image-}
    rest=${rest#linux-headers-}
    case "$rest" in
      *-"$arch") ver=${rest%-"$arch"} ;;
      *-common) ver=${rest%-common} ;;
      *) continue ;;
    esac
    if [ "$ver" != "$keep" ]; then printf '%s\n' "$p"; fi
  done
}

# kernel_releases : installed kernels (directories under /lib/modules with kernel/), sorted.
# PROVISION_ROOT: tests only.
kernel_releases() {
  local d
  for d in "${PROVISION_ROOT:-}"/lib/modules/*/; do
    if [ -d "$d/kernel" ]; then basename "$d"; fi
  done | sort -V
}

# pkg_installed <package>
pkg_installed() { dpkg-query -W -f='${Status}\n' "$1" 2>/dev/null | grep -qx 'install ok installed'; }

# pkg_available <package> : the archive has a version of it
pkg_available() {
  local cand
  cand=$(apt-cache policy "$1" 2>/dev/null | awk '/Candidate:/ { print $2 }')
  [ -n "$cand" ] && [ "$cand" != "(none)" ]
}

# rpi_flavour <release> : rpi-v8 for 6.12.47+rpt-rpi-v8, rpi-2712 for ...-rpi-2712; empty otherwise
rpi_flavour() {
  case "$1" in
    *-rpi-*) printf 'rpi-%s\n' "${1##*-rpi-}" ;;
  esac
}

# rpi_headers_pkgs : Raspberry Pi OS ships more than one kernel (rpi-v8 for the Pi 4, rpi-2712 for
# the Pi 5) and keeps all of them. For EVERY installed release: nothing if its headers are
# installed, linux-headers-<release> if the archive has it, otherwise the image and headers meta
# packages of its flavour together (once per flavour; a newer kernel with headers comes in).
rpi_headers_pkgs() {
  local rel fl seen=" "
  while IFS= read -r rel; do
    [ -n "$rel" ] || continue
    if pkg_installed "linux-headers-$rel"; then continue; fi
    if pkg_available "linux-headers-$rel"; then
      printf 'linux-headers-%s\n' "$rel"
      continue
    fi
    fl=$(rpi_flavour "$rel")
    if [ -z "$fl" ]; then
      log "No headers in the archive for kernel $rel, and it is not a Raspberry Pi kernel"
      continue
    fi
    case "$seen" in *" $fl "*) continue ;; esac
    seen+="$fl "
    log "No headers in the archive for kernel $rel: installing kernel and headers of $fl together"
    vm_headers_pkgs "$fl" "$rel" 0
  done < <(kernel_releases)
}

# rpi_stale_releases : reads "<release> <has-headers 1|0>" lines. Prints the releases without
# headers for which another release of the SAME flavour has headers (that one replaces it).
# The only kernel of a flavour is never printed (the Pi 4 and the Pi 5 kernel both stay).
rpi_stale_releases() {
  local lines=() l rel h fl o orel oh
  mapfile -t lines
  for l in ${lines[@]+"${lines[@]}"}; do
    read -r rel h <<<"$l"
    [ "$h" = 0 ] || continue
    fl=$(rpi_flavour "$rel")
    [ -n "$fl" ] || continue
    for o in "${lines[@]}"; do
      read -r orel oh <<<"$o"
      if [ "$orel" != "$rel" ] && [ "$oh" = 1 ] && [ "$(rpi_flavour "$orel")" = "$fl" ]; then
        printf '%s\n' "$rel"
        break
      fi
    done
  done
}

# rpi_boot_file <flavour> : kernel file on the boot partition for the flavour (empty if unknown)
rpi_boot_file() {
  case "$1" in
    rpi-v8) printf 'kernel8.img\n' ;;
    rpi-2712) printf 'kernel_2712.img\n' ;;
  esac
}

# rpi_newest_with_headers <flavour> : newest installed release of the flavour with headers
rpi_newest_with_headers() {
  local rel best=""
  while IFS= read -r rel; do
    if [ "$(rpi_flavour "$rel")" = "$1" ] && [ -e "${PROVISION_ROOT:-}/lib/modules/$rel/build/Makefile" ]; then best=$rel; fi
  done < <(kernel_releases)
  printf '%s\n' "$best"
}

# rpi_remove_stale : after the kernel and headers meta packages came in, the old kernel of the
# same flavour has no headers. It is removed only if the boot partition already holds the new
# kernel of that flavour (the kernel hook copies /boot/vmlinuz-<release>); otherwise it stays
# and base-40-xpadneo.sh lists it in the report.
rpi_remove_stale() {
  local r=${PROVISION_ROOT:-} rel fl new file stale=()
  mapfile -t stale < <(
    kernel_releases | while IFS= read -r rel; do
      if [ -e "$r/lib/modules/$rel/build/Makefile" ]; then echo "$rel 1"; else echo "$rel 0"; fi
    done | rpi_stale_releases
  )
  for rel in ${stale[@]+"${stale[@]}"}; do
    fl=$(rpi_flavour "$rel")
    new=$(rpi_newest_with_headers "$fl")
    file=$(rpi_boot_file "$fl")
    if [ -z "$file" ] || ! cmp -s "$r/boot/vmlinuz-$new" "$r/boot/firmware/$file"; then
      log "Kernel $rel stays: /boot/firmware/${file:-?} is not the new kernel $new"
      continue
    fi
    log "Removing kernel $rel (no headers; replaced by $new)"
    if pkg_installed "linux-image-$rel"; then
      apt-get purge -y -q -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold "linux-image-$rel" </dev/null
    fi
    rm -rf "${r}/lib/modules/${rel:?}"
  done
}

# kernel_hooks_off / kernel_hooks_on : make zz-update-grub non-executable (run-parts skips it)
# and back. Registered files are remembered in HOOKS_OFF. PROVISION_ROOT: tests only.
HOOKS_OFF=()
kernel_hooks_off() {
  local f
  for f in "${PROVISION_ROOT:-}"/etc/kernel/postinst.d/zz-update-grub "${PROVISION_ROOT:-}"/etc/kernel/postrm.d/zz-update-grub; do
    if [ -x "$f" ]; then
      chmod -x "$f"
      HOOKS_OFF+=("$f")
    fi
  done
}
kernel_hooks_on() {
  local f
  for f in ${HOOKS_OFF[@]+"${HOOKS_OFF[@]}"}; do chmod +x "$f"; done
  HOOKS_OFF=()
}

# Sourced by tests: define the functions only
if [ "${BASH_SOURCE[0]}" != "$0" ]; then return 0; fi

replace_kernel=0
kind=${CFG_TARGET_KIND:?CFG_TARGET_KIND is missing}
arch=${CFG_TARGET_ARCH:?CFG_TARGET_ARCH is missing}

pkgs=(git dialog unzip xmlstarlet ca-certificates curl openssh-server nftables fail2ban
  python3-systemd bluez zram-tools dkms network-manager rfkill)

apt_opts=(-y -q --no-install-recommends -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold)
log "apt-get update"
apt-get -q update

case "$kind" in
  rpi4)
    log "Installed kernels: $(kernel_releases | tr '\n' ' ')"
    mapfile -t headers < <(rpi_headers_pkgs)
    ;;
  vm)
    # newest installed kernel (only directories with kernel/ count)
    rel=$(kernel_releases | tail -n1)
    available=0
    if [ -n "$rel" ] && pkg_available "linux-headers-$rel"; then available=1; fi
    log "Installed kernel: ${rel:-none}; headers in the archive: $available"
    mapfile -t headers < <(vm_headers_pkgs "$arch" "$rel" "$available")
    if [ "$available" != 1 ]; then
      replace_kernel=1
      log "Kernel and headers are installed together; older kernels will be purged"
    fi
    ;;
  *)
    log "Unknown target kind: $kind"
    exit 2
    ;;
esac

for h in ${headers[@]+"${headers[@]}"}; do
  # Only headers of one exact release are skipped when installed. Meta packages are always
  # listed: image and headers meta must be upgraded together, so their versions match.
  if [[ "$h" == linux-headers-[0-9]* ]] && pkg_installed "$h"; then
    log "$h is already installed (not upgraded)"
  else
    pkgs+=("$h")
  fi
done

log "Installing: ${pkgs[*]}"
if [ "$replace_kernel" = 1 ]; then
  trap kernel_hooks_on EXIT
  kernel_hooks_off
fi
apt-get install "${apt_opts[@]}" "${pkgs[@]}"

if [ "$replace_kernel" = 1 ]; then
  newest=$(dpkg-query -W -f='${Package}\n' 'linux-image-[0-9]*' | sed 's/^linux-image-//' | sort -V | tail -n1)
  [ -n "$newest" ] || { log "No kernel installed after apt"; exit 1; }
  mapfile -t old < <(dpkg-query -W -f='${Package}\n' 'linux-image-[0-9]*' 'linux-headers-[0-9]*' | old_kernel_pkgs "$arch" "$newest")
  if [ "${#old[@]}" -gt 0 ]; then
    log "Purging old kernels: ${old[*]}"
    apt-get purge "${apt_opts[@]}" "${old[@]}"
  fi
  for d in /lib/modules/*/; do
    if [ "$(basename "$d")" != "$newest" ]; then
      log "Removing leftover $d"
      rm -rf "$d"
    fi
  done
  kernel_hooks_on
fi

if [ "$kind" = rpi4 ]; then
  rpi_remove_stale
fi
