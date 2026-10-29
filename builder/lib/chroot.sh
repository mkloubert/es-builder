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

# chroot into the mounted target system (builder container only, Bash 5).
# Expects builder/lib/log.sh and builder/lib/image.sh (cleanup_push).
#
# Foreign architecture (e.g. arm64 image on an amd64 host): NOTHING is copied into the chroot
# (no qemu-*-static). The host registers binfmt_misc with flag F (fix binary), so the
# kernel opens the interpreter at registration time and also uses it inside the chroot.
set -euo pipefail

CHROOT_ROOT=""
_CHROOT_MOUNTS=()
_CHROOT_RESOLV=0
_CHROOT_POLICY=0
_CHROOT_BAK=.emulationstation-build-bak

# chroot_prepare [root] : bind /dev /dev/pts /proc /sys /run; temporary resolv.conf
# (host DNS) and usr/sbin/policy-rc.d (exit 101, no service starts). Registers
# chroot_teardown on the cleanup stack. Default root: $TARGET_MNT.
chroot_prepare() {
  local root=${1:-$TARGET_MNT} d
  [ -z "$CHROOT_ROOT" ] || die "chroot is already prepared: $CHROOT_ROOT"
  [ -d "$root" ] || die "chroot directory is missing: $root"
  [ "$(readlink -f "$root")" != / ] || die "chroot on / is not allowed"
  CHROOT_ROOT=$root
  _CHROOT_MOUNTS=()
  cleanup_push chroot_teardown

  for d in dev dev/pts proc sys run; do
    mkdir -p "$root/$d"
    mount --bind "/$d" "$root/$d"
    _CHROOT_MOUNTS+=("$root/$d")
  done

  # resolv.conf in the target is often a symlink (systemd-resolved -> ../run/...) that
  # points to nothing because of the /run bind. Save the original (also a symlink) with mv, create a copy.
  mkdir -p "$root/etc"
  if [ -e "$root/etc/resolv.conf" ] || [ -L "$root/etc/resolv.conf" ]; then
    mv "$root/etc/resolv.conf" "$root/etc/resolv.conf$_CHROOT_BAK"
  fi
  _CHROOT_RESOLV=1
  cat "${HOST_RESOLV:-/etc/resolv.conf}" >"$root/etc/resolv.conf"

  mkdir -p "$root/usr/sbin"
  if [ -e "$root/usr/sbin/policy-rc.d" ]; then
    mv "$root/usr/sbin/policy-rc.d" "$root/usr/sbin/policy-rc.d$_CHROOT_BAK"
  fi
  _CHROOT_POLICY=1
  printf '#!/bin/sh\n# temporary during the image build\nexit 101\n' >"$root/usr/sbin/policy-rc.d"
  chmod 0755 "$root/usr/sbin/policy-rc.d"
}

# chroot_run <command...> : run in the chroot with a clean environment
chroot_run() {
  [ -n "$CHROOT_ROOT" ] || die "chroot_run without chroot_prepare"
  chroot "$CHROOT_ROOT" /usr/bin/env -i \
    HOME=/root LANG=C.UTF-8 TERM="${TERM:-dumb}" DEBIAN_FRONTEND=noninteractive \
    PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    "$@"
}

# Stop processes whose root is in the chroot (e.g. gpg-agent/dirmngr after apt),
# otherwise umount fails with "busy".
_chroot_kill_procs() {
  local root p r found=0
  root=$(readlink -f "$CHROOT_ROOT")
  for p in /proc/[0-9]*; do
    r=$(readlink "$p/root" 2>/dev/null) || continue
    if [ "$r" = "$root" ]; then
      kill -TERM "${p#/proc/}" 2>/dev/null || true
      found=1
    fi
  done
  if [ "$found" -eq 1 ]; then
    sleep 1
    for p in /proc/[0-9]*; do
      r=$(readlink "$p/root" 2>/dev/null) || continue
      [ "$r" = "$root" ] && kill -KILL "${p#/proc/}" 2>/dev/null
    done
  fi
  return 0
}

# chroot_teardown : restores resolv.conf/policy-rc.d, removes the binds (in reverse order).
# Can be called more than once; returns != 0 if something could not be torn down.
chroot_teardown() {
  local root=$CHROOT_ROOT i m failed=0
  [ -n "$root" ] || return 0

  if [ "$_CHROOT_POLICY" -eq 1 ]; then
    rm -f "$root/usr/sbin/policy-rc.d" || failed=1
    if [ -e "$root/usr/sbin/policy-rc.d$_CHROOT_BAK" ]; then
      mv "$root/usr/sbin/policy-rc.d$_CHROOT_BAK" "$root/usr/sbin/policy-rc.d" || failed=1
    fi
    _CHROOT_POLICY=0
  fi
  if [ "$_CHROOT_RESOLV" -eq 1 ]; then
    rm -f "$root/etc/resolv.conf" || failed=1
    if [ -e "$root/etc/resolv.conf$_CHROOT_BAK" ] || [ -L "$root/etc/resolv.conf$_CHROOT_BAK" ]; then
      mv "$root/etc/resolv.conf$_CHROOT_BAK" "$root/etc/resolv.conf" || failed=1
    fi
    _CHROOT_RESOLV=0
  fi

  _chroot_kill_procs
  for ((i = ${#_CHROOT_MOUNTS[@]} - 1; i >= 0; i--)); do
    m=${_CHROOT_MOUNTS[i]}
    if ! umount "$m"; then
      log_warn "umount $m failed, trying lazy umount"
      umount -l "$m" || failed=1
    fi
  done
  _CHROOT_MOUNTS=()
  CHROOT_ROOT=""
  return "$failed"
}
