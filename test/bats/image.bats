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

# shellcheck disable=SC2016,SC2030,SC2031

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  FIX="$ROOT/test/fixtures"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/log.sh"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/image.sh"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/chroot.sh"
  RPI=$(cat "$FIX/sfdisk-rpi.json")
  VM=$(cat "$FIX/sfdisk-vm.json")
  VMARM=$(cat "$FIX/sfdisk-vm-arm64.json")
}

# Stubs for losetup/mount/umount/sfdisk: log to $CALLS, no root needed.
make_stubs() {
  STUBS="$BATS_TEST_TMPDIR/stubs"
  CALLS="$BATS_TEST_TMPDIR/calls"
  mkdir -p "$STUBS"
  : >"$CALLS"
  cat >"$STUBS/losetup" <<'EOF'
#!/usr/bin/env bash
echo "losetup $*" >>"$CALLS"
case "$1" in
  -f)
    n=$(cat "$CALLS.loopnr" 2>/dev/null || echo 0)
    echo $((n + 1)) >"$CALLS.loopnr"
    echo "/dev/loop$n"
    ;;
esac
EOF
  printf '#!/usr/bin/env bash\necho "mount $*" >>"$CALLS"\n' >"$STUBS/mount"
  printf '#!/usr/bin/env bash\necho "umount $*" >>"$CALLS"\n' >"$STUBS/umount"
  printf '#!/usr/bin/env bash\ncat "$SFDISK_FIXTURE"\n' >"$STUBS/sfdisk"
  chmod +x "$STUBS"/*
  export CALLS
  PATH="$STUBS:$PATH"
}

# --- Partitions -------------------------------------------------------------

@test "rpi: part_offset/part_size from fixture (partition 2 start 1064960 x 512)" {
  [ "$(part_offset "$RPI" 2)" -eq $((1064960 * 512)) ]
  [ "$(part_size "$RPI" 2)" -eq $((4947968 * 512)) ]
  [ "$(part_offset "$RPI" 1)" -eq $((16384 * 512)) ]
  [ "$(part_size "$RPI" 1)" -eq $((1048576 * 512)) ]
}

@test "vm: p1 (root) is behind p14/p15" {
  [ "$(part_offset "$VM" 1)" -eq $((262144 * 512)) ]
  [ "$(part_offset "$VM" 15)" -eq $((8192 * 512)) ]
  [ "$(part_size "$VM" 15)" -eq $((253952 * 512)) ]
}

@test "sector size is taken from the JSON" {
  local j
  j=$(jq '.partitiontable.sectorsize = 4096' <<<"$RPI")
  [ "$(part_offset "$j" 2)" -eq $((1064960 * 4096)) ]
}

@test "unknown partition number aborts" {
  run part_offset "$RPI" 3
  [ "$status" -ne 0 ]
}

@test "part_last: physically last partition by start sector, not highest number" {
  [ "$(part_last "$RPI")" = 2 ]
  [ "$(part_last "$VM")" = 1 ]
  [ "$(part_last "$VMARM")" = 1 ]
}

@test "part_role: root/boot by partition type" {
  [ "$(part_role "$RPI" root)" = 2 ]
  [ "$(part_role "$RPI" boot)" = 1 ]
  [ "$(part_role "$VM" root)" = 1 ]
  [ "$(part_role "$VM" boot)" = 15 ]
  [ "$(part_role "$VMARM" root)" = 1 ]
  [ "$(part_role "$VMARM" boot)" = 15 ]
}

# --- Targets -------------------------------------------------------------------

@test "target_kind/target_arch" {
  [ "$(target_kind rpi4)" = rpi4 ]
  [ "$(target_kind vm-amd64)" = vm ]
  [ "$(target_kind vm-arm64)" = vm ]
  [ "$(target_arch rpi4)" = arm64 ]
  [ "$(target_arch vm-amd64)" = amd64 ]
  [ "$(target_arch vm-arm64)" = arm64 ]
  run target_kind riscv
  [ "$status" -ne 0 ]
}

# --- Cleanup --------------------------------------------------------------

@test "cleanup_run: LIFO, also on exit 1 in the script" {
  local log="$BATS_TEST_TMPDIR/order"
  run bash -c "
    set -euo pipefail
    . '$ROOT/builder/lib/log.sh'; . '$ROOT/builder/lib/image.sh'
    cleanup_install
    cleanup_push sh -c 'echo one >>\"\$1\"' x '$log'
    cleanup_push sh -c 'echo two >>\"\$1\"' x '$log'
    cleanup_push sh -c 'echo three >>\"\$1\"' x '$log'
    exit 1
  "
  [ "$status" -eq 1 ]
  [ "$(cat "$log")" = "$(printf 'three\ntwo\none')" ]
}

@test "cleanup_run: also on an error through set -e, arguments with spaces are kept" {
  local log="$BATS_TEST_TMPDIR/order b"
  run bash -c "
    set -euo pipefail
    . '$ROOT/builder/lib/log.sh'; . '$ROOT/builder/lib/image.sh'
    cleanup_install
    cleanup_push touch '$log'
    false
    echo not-reached
  "
  [ "$status" -ne 0 ]
  [ -f "$log" ]
  [[ "$output" != *not-reached* ]]
}

@test "cleanup_run: a failing command does not stop the others, exit != 0" {
  local log="$BATS_TEST_TMPDIR/order"
  run bash -c "
    set -euo pipefail
    . '$ROOT/builder/lib/log.sh'; . '$ROOT/builder/lib/image.sh'
    cleanup_install
    cleanup_push sh -c 'echo one >>\"\$1\"' x '$log'
    cleanup_push false
  "
  [ "$status" -ne 0 ]
  [ "$(cat "$log")" = one ]
}

@test "cleanup_run: SIGTERM cleans up and exits with 143" {
  local log="$BATS_TEST_TMPDIR/order"
  run bash -c "
    set -euo pipefail
    . '$ROOT/builder/lib/log.sh'; . '$ROOT/builder/lib/image.sh'
    cleanup_install
    cleanup_push sh -c 'echo cleaned-up >>\"\$1\"' x '$log'
    kill -TERM \$\$
    echo not-reached
  "
  [ "$status" -eq 143 ]
  [ "$(cat "$log")" = cleaned-up ]
  [[ "$output" != *not-reached* ]]
}

@test "cleanup_run: a second TERM during an entry does not abort it, all entries LIFO" {
  local log="$BATS_TEST_TMPDIR/order"
  run bash -c "
    set -euo pipefail
    . '$ROOT/builder/lib/log.sh'; . '$ROOT/builder/lib/image.sh'
    cleanup_install
    td() { echo td-start >>'$log'; kill -TERM \$\$; sleep 0.3; echo umount-binds >>'$log'; }
    cleanup_push sh -c 'echo umount-root >>\"\$1\"' x '$log'
    cleanup_push td
    kill -TERM \$\$
    echo not-reached
  "
  [ "$status" -eq 143 ]
  [ "$(cat "$log")" = "$(printf 'td-start\numount-binds\numount-root')" ]
  [[ "$output" != *not-reached* ]]
}

@test "cleanup_run in the middle of a script: signal traps are restored afterwards" {
  local log="$BATS_TEST_TMPDIR/order"
  run bash -c "
    set -euo pipefail
    . '$ROOT/builder/lib/log.sh'; . '$ROOT/builder/lib/image.sh'
    cleanup_install
    cleanup_push true
    cleanup_run
    cleanup_push sh -c 'echo later >>\"\$1\"' x '$log'
    kill -TERM \$\$
    echo not-reached
  "
  [ "$status" -eq 143 ]
  [ "$(cat "$log")" = later ]
  [[ "$output" != *not-reached* ]]
}

@test "cleanup_pop runs only the last entry; cleanup_run is empty afterwards" {
  local log="$BATS_TEST_TMPDIR/order"
  cleanup_push sh -c 'echo one >>"$1"' x "$log"
  cleanup_push sh -c 'echo two >>"$1"' x "$log"
  cleanup_pop
  [ "$(cat "$log")" = two ]
  cleanup_run
  cleanup_run
  [ "$(cat "$log")" = "$(printf 'two\none')" ]
}

# --- Download ----------------------------------------------------------------

@test "fetch_verified: wrong checksum -> exit != 0, no <dest>" {
  local src="$BATS_TEST_TMPDIR/src.img" dest="$BATS_TEST_TMPDIR/dl/out.img"
  echo content >"$src"
  mkdir -p "$BATS_TEST_TMPDIR/dl"
  run fetch_verified "file://$src" "sha256:$(printf '0%.0s' {1..64})" "$dest"
  [ "$status" -ne 0 ]
  [ ! -e "$dest" ]
  [ ! -e "$dest.part" ]
  [ ! -e "$dest.verified" ]
}

@test "fetch_verified: correct sha256 -> <dest> + .verified" {
  local src="$BATS_TEST_TMPDIR/src.img" dest="$BATS_TEST_TMPDIR/dl/out.img" sum
  echo content >"$src"
  mkdir -p "$BATS_TEST_TMPDIR/dl"
  sum=sha256:$(sha256sum "$src" | cut -d' ' -f1)
  fetch_verified "file://$src" "$sum" "$dest"
  [ "$(cat "$dest")" = content ]
  [ "$(cat "$dest.verified")" = "$sum" ]
  [ ! -e "$dest.part" ]
}

@test "fetch_verified: sha512 over the .xz file, then unpacked" {
  local src="$BATS_TEST_TMPDIR/src.img" dest="$BATS_TEST_TMPDIR/dl/src.img" sum
  echo raw >"$src"
  xz -k "$src"
  mkdir -p "$BATS_TEST_TMPDIR/dl"
  sum=sha512:$(sha512sum "$src.xz" | cut -d' ' -f1)
  fetch_verified "file://$src.xz" "$sum" "$dest"
  [ "$(cat "$dest")" = raw ]
}

@test "fetch_verified: unknown prefix aborts" {
  local src="$BATS_TEST_TMPDIR/src.img"
  echo content >"$src"
  run fetch_verified "file://$src" "md5:$(md5sum "$src" | cut -d' ' -f1)" "$BATS_TEST_TMPDIR/out.img"
  [ "$status" -ne 0 ]
  [[ "$output" == *md5* ]]
  [ ! -e "$BATS_TEST_TMPDIR/out.img" ]
}

@test "fetch_verified: an existing, verified file is not downloaded again" {
  local src="$BATS_TEST_TMPDIR/src.img" dest="$BATS_TEST_TMPDIR/out.img" sum
  echo content >"$src"
  sum=sha256:$(sha256sum "$src" | cut -d' ' -f1)
  fetch_verified "file://$src" "$sum" "$dest"
  rm "$src"
  run fetch_verified "file://$src" "$sum" "$dest"
  [ "$status" -eq 0 ]
  [ "$(cat "$dest")" = content ]
  # different checksum -> download again (source is missing -> error, old file is gone)
  run fetch_verified "file://$src" "sha256:$(printf 'a%.0s' {1..64})" "$dest"
  [ "$status" -ne 0 ]
  [ ! -e "$dest" ]
}

# --- Loop/Mount (Stubs) ------------------------------------------------------

@test "mount_target vm: root p1 to TARGET_MNT, ESP p15 to /boot/efi; teardown in reverse order" {
  make_stubs
  export SFDISK_FIXTURE="$FIX/sfdisk-vm.json"
  TARGET_MNT="$BATS_TEST_TMPDIR/mnt"
  mount_target "$BATS_TEST_TMPDIR/x.img" vm-amd64
  [ -d "$TARGET_MNT/boot/efi" ]
  cleanup_run
  local img="$BATS_TEST_TMPDIR/x.img"
  [ "$(cat "$CALLS")" = "$(printf '%s\n' \
    "losetup -f --show -o $((262144 * 512)) --sizelimit $((6027264 * 512)) $img" \
    "mount /dev/loop0 $TARGET_MNT" \
    "losetup -f --show -o $((8192 * 512)) --sizelimit $((253952 * 512)) $img" \
    "mount /dev/loop1 $TARGET_MNT/boot/efi" \
    "umount $TARGET_MNT/boot/efi" \
    "losetup -d /dev/loop1" \
    "umount $TARGET_MNT" \
    "losetup -d /dev/loop0")" ]
}

@test "mount_target rpi4: boot p1 to /boot/firmware" {
  make_stubs
  export SFDISK_FIXTURE="$FIX/sfdisk-rpi.json"
  TARGET_MNT="$BATS_TEST_TMPDIR/mnt"
  mount_target "$BATS_TEST_TMPDIR/x.img" rpi4
  cleanup_run
  grep -qx "mount /dev/loop0 $TARGET_MNT" "$CALLS"
  grep -qx "mount /dev/loop1 $TARGET_MNT/boot/firmware" "$CALLS"
  grep -q -- "-o $((1064960 * 512)) " "$CALLS"
}

@test "loop_attach in a subshell is rejected (cleanup would be lost)" {
  make_stubs
  run bash -c ". '$ROOT/builder/lib/log.sh'; . '$ROOT/builder/lib/image.sh'; x=\$(loop_attach /x.img 0 512)"
  [ "$status" -ne 0 ]
  [[ "$output" == *subshell* ]]
  [ ! -s "$CALLS" ]
}

# --- chroot (Stubs) ----------------------------------------------------------

@test "chroot_prepare/teardown: binds, resolv.conf symlink and policy-rc.d are restored" {
  make_stubs
  local r="$BATS_TEST_TMPDIR/root"
  mkdir -p "$r/etc" "$r/usr/sbin"
  ln -s ../run/systemd/resolve/stub-resolv.conf "$r/etc/resolv.conf"
  chroot_prepare "$r"
  [ ! -L "$r/etc/resolv.conf" ]
  cmp -s /etc/resolv.conf "$r/etc/resolv.conf"
  [ -x "$r/usr/sbin/policy-rc.d" ]
  run "$r/usr/sbin/policy-rc.d"
  [ "$status" -eq 101 ]
  cleanup_run
  [ "$(readlink "$r/etc/resolv.conf")" = ../run/systemd/resolve/stub-resolv.conf ]
  [ ! -e "$r/usr/sbin/policy-rc.d" ]
  [ "$(cat "$CALLS")" = "$(printf '%s\n' \
    "mount --bind /dev $r/dev" \
    "mount --bind /dev/pts $r/dev/pts" \
    "mount --bind /proc $r/proc" \
    "mount --bind /sys $r/sys" \
    "mount --bind /run $r/run" \
    "umount $r/run" \
    "umount $r/sys" \
    "umount $r/proc" \
    "umount $r/dev/pts" \
    "umount $r/dev")" ]
}

@test "chroot_teardown: existing policy-rc.d and regular resolv.conf are kept; can be called twice" {
  make_stubs
  local r="$BATS_TEST_TMPDIR/root"
  mkdir -p "$r/etc" "$r/usr/sbin"
  echo "nameserver 9.9.9.9" >"$r/etc/resolv.conf"
  printf '#!/bin/sh\nexit 0\n' >"$r/usr/sbin/policy-rc.d"
  chroot_prepare "$r"
  chroot_teardown
  chroot_teardown
  cleanup_run
  [ "$(cat "$r/etc/resolv.conf")" = "nameserver 9.9.9.9" ]
  [ "$(cat "$r/usr/sbin/policy-rc.d")" = "$(printf '#!/bin/sh\nexit 0')" ]
  [ "$(grep -c '^umount' "$CALLS")" -eq 5 ]
}

# --- real (builder container only) -----------------------------------------

@test "image_grow (GPT like Debian): physically last partition p1 grows, ext4 too, no loop left over" {
  command -v sfdisk >/dev/null || skip "sfdisk missing (runs in the builder container)"
  [ "$(id -u)" -eq 0 ] || skip "root missing (runs in the builder container)"
  local img="$BATS_TEST_TMPDIR/vm.img" before after j
  truncate -s 64M "$img"
  sfdisk -q "$img" <<'EOF'
label: gpt
unit: sectors
p14 : start=2048, size=6144, type="BIOS boot"
p15 : start=8192, size=16384, type="EFI System"
p1 : start=24576, type="Linux root (x86-64)"
EOF
  j=$(partitions_json "$img")
  mkfs.ext4 -q -F -E offset="$(part_offset "$j" 1)" "$img" $(($(part_size "$j" 1) / 1024))k
  before=$(part_size "$j" 1)
  image_grow "$img" 1
  j=$(partitions_json "$img")
  after=$(part_size "$j" 1)
  [ "$after" -gt $((before + 1000 * 1024 * 1024)) ]
  [ "$(part_offset "$j" 15)" -eq $((8192 * 512)) ]
  [ -z "$(losetup -j "$img")" ]
  loop_attach "$img" "$(part_offset "$j" 1)" "$after"
  local blocks bsize
  blocks=$(dumpe2fs -h "$LOOP_DEV" 2>/dev/null | awk -F: '/^Block count/ {gsub(/ /,"",$2); print $2}')
  bsize=$(dumpe2fs -h "$LOOP_DEV" 2>/dev/null | awk -F: '/^Block size/ {gsub(/ /,"",$2); print $2}')
  cleanup_run
  [ $((blocks * bsize)) -gt $((before + 1000 * 1024 * 1024)) ]
  [ -z "$(losetup -j "$img")" ]
}
