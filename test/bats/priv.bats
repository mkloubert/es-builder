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

# Root helper "priv" (Task 6): argument checks and the commands it runs, with stubs in PATH.
# The sudoers file is checked with visudo.
# shellcheck disable=SC2016,SC2034

bats_require_minimum_version 1.5.0

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  PRIV="$ROOT/overlay/usr/local/lib/emulationstation/priv"
  STUBS="$BATS_TEST_TMPDIR/stubs"
  CALLS="$BATS_TEST_TMPDIR/calls"
  mkdir -p "$STUBS"
  : >"$CALLS"
  export CALLS
  # The helper ignores PATH; tests use PRIV_TEST_PATH (works only when not root).
  export PRIV_TEST_PATH="$STUBS:/usr/bin:/bin"
  MNT="$BATS_TEST_TMPDIR/usbmnt"
  export PRIV_TEST_MOUNT_DIR="$MNT"
}

need_nonroot() {
  [ "$(id -u)" -ne 0 ] || skip "PRIV_TEST_PATH is ignored for root"
}

stub() {
  printf '#!/bin/bash\nprintf "%s %%s\\n" "$*" >>"$CALLS"\n%s\n' "$1" "${2:-}" >"$STUBS/$1"
  chmod +x "$STUBS/$1"
}

@test "priv: f2b-unban rejects a shell injection, stub is not called" {
  need_nonroot
  stub fail2ban-client
  run "$PRIV" f2b-unban '1.2.3.4;id'
  [ "$status" -eq 2 ]
  [ ! -s "$CALLS" ]
}

@test "priv: f2b-unban accepts IPv4 and IPv6" {
  need_nonroot
  stub fail2ban-client
  run "$PRIV" f2b-unban 2001:db8::1
  [ "$status" -eq 0 ]
  run "$PRIV" f2b-unban 10.0.255.0
  [ "$status" -eq 0 ]
  run "$PRIV" f2b-unban ::1
  [ "$status" -eq 0 ]
  run "$PRIV" f2b-unban 1:2:3:4:5:6:7:8
  [ "$status" -eq 0 ]
  [ "$(cat "$CALLS")" = "$(printf 'fail2ban-client unban 2001:db8::1\nfail2ban-client unban 10.0.255.0\nfail2ban-client unban ::1\nfail2ban-client unban 1:2:3:4:5:6:7:8')" ]
}

@test "priv: f2b-unban rejects bad addresses with exit 2" {
  need_nonroot
  stub fail2ban-client
  local bad
  for bad in '' 256.1.1.1 1.2.3 1.2.3.4.5 01.2.3.4 ' 1.2.3.4' '1.2.3.4 ' -h --all 'a.b.c.d' \
    1::2::3 :::1 1:2:3:4:5:6:7 1:2:3:4:5:6:7:8:9 12345::1 ::g 1:2:3:4:5:6:7:8::  :1:2:3:4:5:6:7 \
    '::ffff:1.2.3.4' 'fe80::1%eth0' '1.2.3.4/24' $'1.2.3.4\nid'; do
    run "$PRIV" f2b-unban "$bad"
    [ "$status" -eq 2 ] || { echo "accepted: [$bad]"; return 1; }
  done
  run "$PRIV" f2b-unban
  [ "$status" -eq 2 ]
  [ ! -s "$CALLS" ]
}

@test "priv: journal allows three units and rejects others" {
  need_nonroot
  stub journalctl
  run "$PRIV" journal nginx
  [ "$status" -eq 2 ]
  [ ! -s "$CALLS" ]
  run "$PRIV" journal ssh.service
  [ "$status" -eq 2 ]
  run "$PRIV" journal -- -x
  [ "$status" -eq 2 ]
  local u
  for u in emulationstation-web ssh fail2ban; do
    run "$PRIV" journal "$u"
    [ "$status" -eq 0 ]
  done
  [ "$(cat "$CALLS")" = "$(printf 'journalctl -u emulationstation-web -n 500 --no-pager\njournalctl -u ssh -n 500 --no-pager\njournalctl -u fail2ban -n 500 --no-pager')" ]
}

@test "priv: f2b-list prints '<jail> <ip>' for each banned address" {
  need_nonroot
  cat >"$STUBS/fail2ban-client" <<'STUB'
#!/bin/bash
if [ $# -eq 1 ] && [ "$1" = status ]; then
  printf 'Status\n|- Number of jail:\t2\n`- Jail list:\temulationstation-web, sshd\n'
elif [ "$1 $2" = "status sshd" ]; then
  printf 'Status for the jail: sshd\n|- Filter\n|  `- Currently failed:\t0\n`- Actions\n   |- Currently banned:\t2\n   `- Banned IP list:\t1.2.3.4 2001:db8::5\n'
elif [ "$1 $2" = "status emulationstation-web" ]; then
  printf 'Status for the jail: emulationstation-web\n`- Actions\n   `- Banned IP list:\t\n'
else
  exit 1
fi
STUB
  chmod +x "$STUBS/fail2ban-client"
  run "$PRIV" f2b-list
  [ "$status" -eq 0 ]
  [ "$output" = "$(printf 'sshd 1.2.3.4\nsshd 2001:db8::5')" ]
}

@test "priv: apt-update and apt-upgrade run through systemd-run with DEBIAN_FRONTEND" {
  need_nonroot
  stub systemd-run
  run "$PRIV" apt-update
  [ "$status" -eq 0 ]
  run "$PRIV" apt-upgrade
  [ "$status" -eq 0 ]
  [ "$(cat "$CALLS")" = "$(printf '%s\n%s' \
    'systemd-run --wait --pipe --collect --quiet --setenv=DEBIAN_FRONTEND=noninteractive -- apt-get update' \
    'systemd-run --wait --pipe --collect --quiet --setenv=DEBIAN_FRONTEND=noninteractive -- apt-get -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold upgrade')" ]
}

@test "priv: reboot and poweroff call systemctl" {
  need_nonroot
  stub systemctl
  run "$PRIV" reboot
  [ "$status" -eq 0 ]
  run "$PRIV" poweroff
  [ "$status" -eq 0 ]
  [ "$(cat "$CALLS")" = "$(printf 'systemctl reboot\nsystemctl poweroff')" ]
}

@test "priv: unknown command, no command and extra arguments -> exit 2 with a message" {
  need_nonroot
  stub systemctl
  run --separate-stderr "$PRIV" frobnicate
  [ "$status" -eq 2 ]
  [[ "$stderr" == *Usage* ]]
  run "$PRIV"
  [ "$status" -eq 2 ]
  run "$PRIV" reboot now
  [ "$status" -eq 2 ]
  [ ! -s "$CALLS" ]
}

@test "priv: does not trust PATH (a fake systemctl in PATH is not used)" {
  need_nonroot
  stub systemctl
  unset PRIV_TEST_PATH
  PATH="$STUBS:$PATH" run bash -c '"$0" f2b-unban 1.2.3.4 || true' "$PRIV"
  # The real fail2ban-client may be missing or fail; the stub dir must not matter
  [ ! -s "$CALLS" ]
}

@test "priv: fixed shebang, executable, bash -n" {
  [ "$(head -n1 "$PRIV")" = '#!/bin/bash' ]
  [ -x "$PRIV" ]
  bash -n "$PRIV"
  grep -qx 'set -euo pipefail' "$PRIV"
}

@test "sudoers: visudo accepts the file and the header lines are plain comments" {
  command -v visudo >/dev/null || skip "visudo is missing (runs in the builder container)"
  run visudo -cf "$ROOT/overlay/etc/sudoers.d/emulationstation"
  [ "$status" -eq 0 ]
  grep -qx 'emulationstation ALL=(root) NOPASSWD: /usr/sbin/reboot, /usr/sbin/poweroff, /usr/local/lib/emulationstation/priv' \
    "$ROOT/overlay/etc/sudoers.d/emulationstation"
}

# --- usb-list, usb-mount, usb-umount ------------------------------------------------------------

# usb_stubs <fixture> [root-source] : lsblk prints the fixture, findmnt prints the root device
usb_stubs() {
  cp "$ROOT/test/fixtures/lsblk/$1.json" "$BATS_TEST_TMPDIR/lsblk.json"
  printf '#!/bin/bash\ncat "%s"\n' "$BATS_TEST_TMPDIR/lsblk.json" >"$STUBS/lsblk"
  printf '#!/bin/bash\necho "%s"\n' "${2:-/dev/mmcblk0p2}" >"$STUBS/findmnt"
  printf '#!/bin/bash\necho 1234\n' >"$STUBS/id"
  chmod +x "$STUBS/lsblk" "$STUBS/findmnt" "$STUBS/id"
  # install records its call and creates the directory (the last argument)
  printf '#!/bin/bash\nprintf "install %%s\\n" "$*" >>"$CALLS"\nmkdir -p "${@: -1}"\n' >"$STUBS/install"
  chmod +x "$STUBS/install"
  # The helper must never call chown or chmod (it uses install -d). These stubs only record a call,
  # so a test can show that none happened, and a real chown/chmod never runs on the test machine.
  stub chown
  stub chmod
  stub sync
  stub mount
  stub umount
  # mountpoint succeeds only when the file "mounted" exists
  printf '#!/bin/bash\n[ -e "%s/mounted" ]\n' "$BATS_TEST_TMPDIR" >"$STUBS/mountpoint"
  chmod +x "$STUBS/mountpoint"
}

@test "priv usb-list: a FAT32 stick gives one line in the exact TAB format" {
  need_nonroot
  usb_stubs usb-stick
  run "$PRIV" usb-list
  [ "$status" -eq 0 ]
  [ "$output" = "$(printf '/dev/sda1\tvfat\t15999000000\tRETRO')" ]
}

@test "priv usb-list: the root disk on USB is never listed, other sticks are" {
  need_nonroot
  usb_stubs usb-root /dev/sda2
  run "$PRIV" usb-list
  [ "$status" -eq 0 ]
  [ "$output" = "$(printf '/dev/sdb1\texfat\t64000000000\tGAMES')" ]
}

@test "priv usb-list: root disk is excluded by findmnt even without mount points in lsblk" {
  need_nonroot
  usb_stubs usb-root /dev/sda2
  jq '(.. | objects | select(has("mountpoints")) | .mountpoints) = [null]' "$BATS_TEST_TMPDIR/lsblk.json" >"$BATS_TEST_TMPDIR/x.json"
  mv "$BATS_TEST_TMPDIR/x.json" "$BATS_TEST_TMPDIR/lsblk.json"
  run "$PRIV" usb-list
  [ "$status" -eq 0 ]
  [ "$output" = "$(printf '/dev/sdb1\texfat\t64000000000\tGAMES')" ]
}

@test "priv usb-list: a SATA disk is not listed; empty output and exit 0" {
  need_nonroot
  usb_stubs sata
  run "$PRIV" usb-list
  [ "$status" -eq 0 ]
  [ -z "$output" ]
}

@test "priv usb-list: ext4 partitions are skipped; labels get ? for TAB and control characters, empty becomes -" {
  need_nonroot
  usb_stubs label-tab
  run "$PRIV" usb-list
  [ "$status" -eq 0 ]
  [ "$output" = "$(printf '/dev/sda1\tvfat\t15999000000\tA?B?C\n/dev/sda2\texfat\t1000000\t-\n/dev/sda4\tntfs\t2000000\tNT')" ]
}

@test "priv usb-list: a USB disk without partitions is listed" {
  need_nonroot
  usb_stubs no-partition
  run "$PRIV" usb-list
  [ "$status" -eq 0 ]
  [ "$output" = "$(printf '/dev/sda\texfat\t32000000000\tBARE')" ]
}

@test "priv usb-list: takes no argument" {
  need_nonroot
  run "$PRIV" usb-list x
  [ "$status" -eq 2 ]
}

@test "priv usb-mount: a FAT stick is mounted with the exact options" {
  need_nonroot
  usb_stubs usb-stick
  run "$PRIV" usb-mount /dev/sda1
  [ "$status" -eq 0 ]
  [ "$output" = "$MNT" ]
  grep -qx 'USB_MOUNT_DIR=/run/emulationstation-usb' "$PRIV"
  grep -qx "install -d -m 0755 -o root -g root $MNT" "$CALLS"
  grep -qx "mount -t vfat -o rw,nodev,nosuid,noexec,noatime,uid=1234,gid=1234,fmask=0137,dmask=0027 -- /dev/sda1 $MNT" "$CALLS"
  run ! grep -q '^umount' "$CALLS"
}

@test "priv usb-mount: ntfs uses ntfs3, exfat uses exfat" {
  need_nonroot
  usb_stubs label-tab
  run "$PRIV" usb-mount /dev/sda4
  [ "$status" -eq 0 ]
  grep -q '^mount -t ntfs3 -o .* -- /dev/sda4 ' "$CALLS"
  run "$PRIV" usb-mount /dev/sda2
  [ "$status" -eq 0 ]
  grep -q '^mount -t exfat -o .* -- /dev/sda2 ' "$CALLS"
}

@test "priv usb-mount: a device that is not listed gives exit 2 and no mount" {
  need_nonroot
  usb_stubs sata
  run "$PRIV" usb-mount /dev/sdb1
  [ "$status" -eq 2 ]
  run "$PRIV" usb-mount /dev/mmcblk0
  [ "$status" -eq 2 ]
  usb_stubs usb-root /dev/sda2
  run "$PRIV" usb-mount /dev/sda1
  [ "$status" -eq 2 ]
  run ! grep -q '^mount' "$CALLS"
}

@test "priv usb-mount: bad paths and wrong argument counts give exit 2" {
  need_nonroot
  usb_stubs usb-stick
  local bad
  for bad in '../dev/sda1' '/dev/sda1;x' '' '/dev/../dev/sda1' '/dev/sda1 ' '-o' '/dev/disk/by-label/RETRO'; do
    run "$PRIV" usb-mount "$bad"
    [ "$status" -eq 2 ]
  done
  run "$PRIV" usb-mount
  [ "$status" -eq 2 ]
  run "$PRIV" usb-mount /dev/sda1 /dev/sda1
  [ "$status" -eq 2 ]
  run ! grep -q '^mount' "$CALLS"
}

@test "priv usb-mount: something already mounted is unmounted first" {
  need_nonroot
  usb_stubs usb-stick
  mkdir -p "$MNT"; : >"$BATS_TEST_TMPDIR/mounted"
  run "$PRIV" usb-mount /dev/sda1
  [ "$status" -eq 0 ]
  grep -qx "umount $MNT" "$CALLS"
  [ "$(grep -n '^umount' "$CALLS" | cut -d: -f1)" -lt "$(grep -n '^mount' "$CALLS" | cut -d: -f1)" ]
}

@test "priv usb-mount: a failing mount gives exit 1" {
  need_nonroot
  usb_stubs usb-stick
  stub mount 'exit 32'
  run "$PRIV" usb-mount /dev/sda1
  [ "$status" -eq 1 ]
}

@test "priv usb-umount: nothing mounted -> exit 0 and no umount" {
  need_nonroot
  usb_stubs usb-stick
  run "$PRIV" usb-umount
  [ "$status" -eq 0 ]
  run ! grep -q '^umount' "$CALLS"
}

@test "priv usb-umount: syncs, then unmounts" {
  need_nonroot
  usb_stubs usb-stick
  mkdir -p "$MNT"; : >"$BATS_TEST_TMPDIR/mounted"
  run "$PRIV" usb-umount
  [ "$status" -eq 0 ]
  grep -qx "sync -f $MNT" "$CALLS"
  grep -qx "umount $MNT" "$CALLS"
  run ! grep -q '^umount -l' "$CALLS"
}

@test "priv usb-umount: when umount fails, umount -l is used" {
  need_nonroot
  usb_stubs usb-stick
  mkdir -p "$MNT"; : >"$BATS_TEST_TMPDIR/mounted"
  printf '#!/bin/bash\nprintf "umount %%s\\n" "$*" >>"$CALLS"\n[ "$1" = -l ]\n' >"$STUBS/umount"
  run "$PRIV" usb-umount
  [ "$status" -eq 0 ]
  grep -qx "umount -l $MNT" "$CALLS"
}

# --- app-30-webui.sh: jq in the app layer --------------------------------------------------------

# run_app30 <dpkg-exit> : runs app-30-webui.sh with stubs; apt-get calls go to $CALLS
run_app30() {
  local r="$BATS_TEST_TMPDIR/root" d="$BATS_TEST_TMPDIR/stubs30"
  mkdir -p "$d" "$r/usr/local/bin" "$r/etc/emulationstation-web/tls" "$r/etc/systemd/system"
  printf '#!/bin/bash\nprintf "%%s\\n" bcrypt >/dev/null\nprintf "WEB_USER=u\\nWEB_PASSWORD_HASH=\\$2a\\$10\\$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ01234\\n" >"${@: 3:1}"\n' >"$r/usr/local/bin/emulationstation-web"
  chmod +x "$r/usr/local/bin/emulationstation-web"
  : >"$r/etc/emulationstation-web/tls/server.key"
  : >"$r/etc/emulationstation-web/tls/server.crt"
  : >"$r/etc/systemd/system/emulationstation-web.service"
  printf 'WEB_USER=u\nWEB_PASSWORD=pw123456\n' >"$BATS_TEST_TMPDIR/build.env"
  local c
  for c in chown systemctl; do printf '#!/bin/bash\nexit 0\n' >"$d/$c"; done
  printf '#!/bin/bash\n[ "$1" = -s ] && [ "$2" = jq ] && exit %s\nexit 0\n' "$1" >"$d/dpkg"
  printf '#!/bin/bash\nprintf "apt-get %%s [DEBIAN_FRONTEND=%%s]\\n" "$*" "${DEBIAN_FRONTEND:-}" >>"$CALLS"\n' >"$d/apt-get"
  chmod +x "$d"/*
  PATH="$d:$PATH" PROVISION_ROOT="$r" PROVISION_LIB="$ROOT/builder/lib" BUILD_ENV="$BATS_TEST_TMPDIR/build.env" \
    WEB_BIN=/usr/local/bin/emulationstation-web run bash "$ROOT/provision/common/app-30-webui.sh"
}

@test "app-30-webui.sh: jq is installed when it is missing (apt-get update first)" {
  run_app30 1
  [ "$status" -eq 0 ]
  [ "$(sed -n 1p "$CALLS")" = "apt-get update [DEBIAN_FRONTEND=noninteractive]" ]
  [ "$(sed -n 2p "$CALLS")" = "apt-get install -y --no-install-recommends jq [DEBIAN_FRONTEND=noninteractive]" ]
}

@test "app-30-webui.sh: jq is not installed again when it is present" {
  run_app30 0
  [ "$status" -eq 0 ]
  [ ! -s "$CALLS" ]
}

@test "priv usb-mount: an existing plain directory is not created or changed again" {
  need_nonroot
  usb_stubs usb-stick
  mkdir "$MNT"
  run "$PRIV" usb-mount /dev/sda1
  [ "$status" -eq 0 ]
  run ! grep -q '^install' "$CALLS"
  run ! grep -q '^chown' "$CALLS"
}

@test "priv usb-mount: the old mount is removed before the directory is checked or created" {
  need_nonroot
  usb_stubs usb-stick
  mkdir -p "$MNT"; : >"$BATS_TEST_TMPDIR/mounted"
  run "$PRIV" usb-mount /dev/sda1
  [ "$status" -eq 0 ]
  [ "$(grep -n '^umount' "$CALLS" | cut -d: -f1)" -lt "$(grep -n '^mount' "$CALLS" | cut -d: -f1)" ]
}

@test "priv usb-mount and usb-umount: a symlink at the mount point is refused, nothing is called" {
  need_nonroot
  usb_stubs usb-stick
  mkdir "$BATS_TEST_TMPDIR/target"
  ln -s "$BATS_TEST_TMPDIR/target" "$MNT"
  mkdir -p "$MNT"; : >"$BATS_TEST_TMPDIR/mounted"
  run "$PRIV" usb-mount /dev/sda1
  [ "$status" -eq 1 ]
  [[ $output == *"priv: mount point is a symbolic link"* ]]
  run "$PRIV" usb-umount
  [ "$status" -eq 1 ]
  [[ $output == *"priv: mount point is a symbolic link"* ]]
  [ ! -s "$CALLS" ]
}

@test "priv usb-mount and usb-umount: a regular file at the mount point is refused" {
  need_nonroot
  usb_stubs usb-stick
  : >"$MNT"
  run "$PRIV" usb-mount /dev/sda1
  [ "$status" -eq 1 ]
  [[ $output == *"priv: mount point is not a directory"* ]]
  run "$PRIV" usb-umount
  [ "$status" -eq 1 ]
  [[ $output == *"priv: mount point is not a directory"* ]]
  [ ! -s "$CALLS" ]
}

@test "priv usb-umount: a missing mount point means nothing is mounted (exit 0)" {
  need_nonroot
  usb_stubs usb-stick
  run "$PRIV" usb-umount
  [ "$status" -eq 0 ]
  [ ! -s "$CALLS" ]
}

@test "priv usb-list: a partition that is mounted elsewhere (overlay root, USB SSD) is not listed" {
  need_nonroot
  usb_stubs usb-overlay overlay
  run "$PRIV" usb-list
  [ "$status" -eq 0 ]
  [ "$output" = "$(printf '/dev/sdb1\texfat\t64000000000\tGAMES')" ]
}

@test "priv usb-list: a partition mounted on the helper mount point is still listed" {
  need_nonroot
  usb_stubs usb-stick
  jq --arg m "$MNT" '(.blockdevices[1].children[0].mountpoints) = [$m]' "$BATS_TEST_TMPDIR/lsblk.json" >"$BATS_TEST_TMPDIR/x.json"
  mv "$BATS_TEST_TMPDIR/x.json" "$BATS_TEST_TMPDIR/lsblk.json"
  run "$PRIV" usb-list
  [ "$output" = "$(printf '/dev/sda1\tvfat\t15999000000\tRETRO')" ]
}

@test "priv usb-list: a USB root disk without partitions is not listed" {
  need_nonroot
  usb_stubs no-partition /dev/sda
  run "$PRIV" usb-list
  [ "$status" -eq 0 ]
  [ -z "$output" ]
}

@test "priv usb-list and usb-mount: when findmnt fails, nothing is listed and the exit code is 3" {
  need_nonroot
  usb_stubs usb-stick
  printf '#!/bin/bash\nexit 1\n' >"$STUBS/findmnt"
  run "$PRIV" usb-list
  [ "$status" -eq 3 ]
  [[ $output == *"priv: could not find the root disk"* ]]
  [[ $output != *"/dev/sda1"* ]]
  # findmnt prints nothing: the root disk is unknown as well
  printf '#!/bin/bash\nexit 0\n' >"$STUBS/findmnt"
  run "$PRIV" usb-list
  [ "$status" -eq 3 ]
  [[ $output != *"/dev/sda1"* ]]
  run "$PRIV" usb-mount /dev/sda1
  [ "$status" -eq 3 ]
  [[ $output == *"priv: could not read the USB devices"* ]]
  run ! grep -q '^mount' "$CALLS"
}

@test "priv usb-mount: a failing lsblk or jq is a system error (exit 3), not an unusable stick" {
  need_nonroot
  usb_stubs usb-stick
  printf '#!/bin/bash\nexit 1\n' >"$STUBS/lsblk"
  run "$PRIV" usb-mount /dev/sda1
  [ "$status" -eq 3 ]
  [[ $output == *"priv: could not read the USB devices"* ]]
  [[ $output != *"not a usable USB stick"* ]]
  usb_stubs usb-stick
  printf '#!/bin/bash\ncat >/dev/null\nexit 5\n' >"$STUBS/jq"
  chmod +x "$STUBS/jq"
  run "$PRIV" usb-mount /dev/sda1
  [ "$status" -eq 3 ]
  [[ $output == *"priv: could not read the USB devices"* ]]
  run ! grep -q '^mount' "$CALLS"
}

@test "priv usb-umount: when umount and umount -l both fail, exit 1 with a message" {
  need_nonroot
  usb_stubs usb-stick
  mkdir -p "$MNT"; : >"$BATS_TEST_TMPDIR/mounted"
  stub umount 'exit 32'
  run "$PRIV" usb-umount
  [ "$status" -eq 1 ]
  [[ $output == *"priv: could not unmount the USB stick"* ]]
  grep -qx "umount -l $MNT" "$CALLS"
  run "$PRIV" usb-mount /dev/sda1
  [ "$status" -eq 1 ]
  [[ $output == *"priv: could not unmount the USB stick"* ]]
  run ! grep -q '^mount' "$CALLS"
}
