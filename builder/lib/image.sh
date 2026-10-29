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

# Image library (builder container only, Bash 5): targets, cleanup (LIFO),
# download with checksum, partitions (sfdisk -J + jq), loop devices, growing, mounting.
# Expects builder/lib/log.sh to be loaded.
set -euo pipefail

: "${TARGET_MNT:=/mnt/target}"

# --- Targets -------------------------------------------------------------------

# target_kind <target> -> rpi4|vm (provision directory); "vm" is passed through
target_kind() {
  case "$1" in
    rpi4) echo rpi4 ;;
    vm | vm-amd64 | vm-arm64) echo vm ;;
    *) die "Unknown target: $1" 2 ;;
  esac
}

# target_arch <target> -> arm64|amd64
target_arch() {
  case "$1" in
    rpi4 | vm-arm64) echo arm64 ;;
    vm-amd64) echo amd64 ;;
    *) die "Unknown target: $1" 2 ;;
  esac
}

# target_image_url <target> / target_image_checksum <target> : upstream pins from build.conf (CFG_*)
target_image_url() {
  case "$1" in
    rpi4) printf '%s\n' "${CFG_RPI_IMAGE_URL:-}" ;;
    vm-amd64) printf '%s\n' "${CFG_VM_IMAGE_URL_AMD64:-}" ;;
    vm-arm64) printf '%s\n' "${CFG_VM_IMAGE_URL_ARM64:-}" ;;
    *) die "Unknown target: $1" 2 ;;
  esac
}

target_image_checksum() {
  case "$1" in
    rpi4) printf '%s\n' "${CFG_RPI_IMAGE_CHECKSUM:-}" ;;
    vm-amd64) printf '%s\n' "${CFG_VM_IMAGE_CHECKSUM_AMD64:-}" ;;
    vm-arm64) printf '%s\n' "${CFG_VM_IMAGE_CHECKSUM_ARM64:-}" ;;
    *) die "Unknown target: $1" 2 ;;
  esac
}

# --- Cleanup (LIFO) -------------------------------------------------------
# Entries are commands with arguments (cleanup_push umount "/mnt/a b"); they are
# stored with printf %q, so spaces in arguments are kept.

_CLEANUP_STACK=()
# Subshell depth at load time: registrations from $(...) would be lost.
_IMAGE_SUBSHELL=$BASH_SUBSHELL

# cleanup_push <command> [arg...]
cleanup_push() {
  local q
  printf -v q '%q ' "$@"
  _CLEANUP_STACK+=("$q")
}

_cleanup_exec() {
  if ! eval "$1"; then
    log_warn "Cleanup failed: $1"
    return 1
  fi
}

# cleanup_pop : runs only the most recently registered command and removes it
cleanup_pop() {
  local n=${#_CLEANUP_STACK[@]} cmd
  [ "$n" -gt 0 ] || return 0
  cmd=${_CLEANUP_STACK[n - 1]}
  unset '_CLEANUP_STACK[n-1]'
  _cleanup_exec "$cmd"
}

# cleanup_run [exitcode] : all commands in reverse order, the stack is empty afterwards.
# A failing command does not stop the others. Returns: the previous exit
# status ($?), otherwise 1 if a command failed. With <exitcode> (signal trap)
# the script ends with this code afterwards.
# INT/TERM are ignored during cleanup (inherited by the commands too), so a
# second Ctrl+C/TERM does not abort a running entry (e.g. umount, chroot_teardown).
# PIPE is ignored too: if the log pipe is gone, a message fails, but the cleanup goes on.
# When called in the middle of a script, the previous traps are restored afterwards.
cleanup_run() {
  local rc=$? failed=0 i cmd saved
  saved=$(trap -p INT TERM PIPE)
  trap '' INT TERM PIPE
  for ((i = ${#_CLEANUP_STACK[@]} - 1; i >= 0; i--)); do
    cmd=${_CLEANUP_STACK[i]}
    unset '_CLEANUP_STACK[i]'
    _cleanup_exec "$cmd" || failed=1
  done
  _CLEANUP_STACK=()
  if [ $# -ge 1 ]; then
    trap - EXIT
    exit "$1"
  fi
  trap - INT TERM PIPE
  eval "$saved"
  if [ "$rc" -ne 0 ]; then return "$rc"; fi
  return "$failed"
}

# cleanup_install : trap for EXIT/INT/TERM. Signals end the script after
# cleanup (130/143) instead of continuing afterwards.
cleanup_install() {
  trap 'cleanup_run' EXIT
  trap 'cleanup_run 130' INT
  trap 'cleanup_run 143' TERM
}

# --- Download ----------------------------------------------------------------

# fetch_verified <url> <checksum> <dest>
# Downloads to <dest>.part, checks sha256:/sha512: over the file as published,
# unpacks .xz and only then creates <dest>. <dest>.verified remembers the checksum;
# if it matches and <dest> exists, the download is skipped.
fetch_verified() {
  local url=$1 sum=$2 dest=$3 algo hex tool len actual
  algo=${sum%%:*}
  hex=${sum#*:}
  case "$algo" in
    sha256) tool=sha256sum len=64 ;;
    sha512) tool=sha512sum len=128 ;;
    *) die "Unknown checksum format '$algo' (allowed: sha256:<hex>, sha512:<hex>)" ;;
  esac
  hex=${hex,,}
  [[ "$hex" =~ ^[0-9a-f]{$len}$ ]] || die "Invalid $algo checksum: $sum"

  if [ -f "$dest" ] && [ -f "$dest.verified" ] && [ "$(cat "$dest.verified")" = "$algo:$hex" ]; then
    log_info "Already downloaded and verified: $dest"
    return 0
  fi
  rm -f "$dest" "$dest.verified" "$dest.part" "$dest.unxz"
  mkdir -p "$(dirname "$dest")"

  log_info "Downloading $url ..."
  if ! curl -fL --retry 3 --retry-delay 5 -sS -o "$dest.part" "$url"; then
    rm -f "$dest.part"
    die "Download failed: $url"
  fi
  actual=$("$tool" "$dest.part" | cut -d' ' -f1)
  if [ "$actual" != "$hex" ]; then
    rm -f "$dest.part"
    die "Wrong checksum for $url (expected $algo:$hex, got $algo:$actual)"
  fi
  case "$url" in
    *.xz)
      log_info "Unpacking ..."
      if ! xz -dc "$dest.part" >"$dest.unxz"; then
        rm -f "$dest.part" "$dest.unxz"
        die "Unpacking failed: $dest.part"
      fi
      rm -f "$dest.part"
      mv "$dest.unxz" "$dest"
      ;;
    *) mv "$dest.part" "$dest" ;;
  esac
  printf '%s\n' "$algo:$hex" >"$dest.verified"
  log_info "Verified: $dest"
}

# --- Partitions -------------------------------------------------------------
# Partition number = digits at the end of "node" (e.g. disk.raw15 -> 15, loop0p1 -> 1).

_JQ_NUM='(.node | capture("(?<n>[0-9]+)$").n | tonumber)'

# partitions_json <img> : sfdisk -J
partitions_json() {
  sfdisk -J "$1"
}

_part_field() {
  local json=$1 n=$2 field=$3 out
  out=$(jq -er --argjson n "$n" --arg f "$field" "
    .partitiontable as \$t
    | [\$t.partitions[] | select($_JQ_NUM == \$n)]
    | if length == 1 then .[0][\$f] * (\$t.sectorsize // 512) else empty end" <<<"$json") ||
    die "Partition $n not found"
  printf '%s\n' "$out"
}

# part_offset <json> <n> / part_size <json> <n> : in bytes
part_offset() { _part_field "$1" "$2" start; }
part_size() { _part_field "$1" "$2" size; }

# part_last <json> : number of the physically last partition (largest start sector)
part_last() {
  jq -er "[.partitiontable.partitions[]] | max_by(.start) | $_JQ_NUM" <<<"$1" ||
    die "No partitions found"
}

# part_role <json> root|boot : partition number by type.
#   root: dos 83; GPT Linux filesystem or Linux root (x86-64)/(ARM-64)
#   boot: dos c/b/e/ef (FAT, Pi boot partition); GPT EFI system partition
part_role() {
  local json=$1 role=$2 types out
  case "$role" in
    root) types='["83","0FC63DAF-8483-4772-8E79-3D69D8477DE4","4F68BCE3-E8CD-4DB1-96E7-FBCAF984B709","B921B045-1DF0-41C3-AF44-4C6F280D3FAE"]' ;;
    boot) types='["C","B","E","EF","C12A7328-F81F-11D2-BA4B-00A0C93EC93B"]' ;;
    *) die "Unknown partition role: $role" ;;
  esac
  out=$(jq -er --argjson t "$types" "
    [.partitiontable.partitions[] | select((.type | ascii_upcase) as \$x | any(\$t[]; . == \$x))]
    | if length == 1 then .[0] | $_JQ_NUM else empty end" <<<"$json") ||
    die "No unique $role partition found"
  printf '%s\n' "$out"
}

# --- loop devices ------------------------------------------------------------

# loop_attach <img> <offset> <size> : sets LOOP_DEV, registers "losetup -d".
# No partscan/udev needed (byte offset + --sizelimit). Do not call inside $(...).
loop_attach() {
  local img=$1 off=$2 size=$3 dev next
  [ "$BASH_SUBSHELL" -eq "$_IMAGE_SUBSHELL" ] ||
    die "do not call loop_attach in a subshell (cleanup would be lost); the result is in LOOP_DEV"
  if ! dev=$(losetup -f --show -o "$off" --sizelimit "$size" "$img"); then
    # In the container the device nodes of new loop devices are often missing (no udev).
    next=$(losetup -f) || die "No free loop device"
    if [ ! -b "$next" ]; then
      mknod "$next" b 7 "${next#/dev/loop}" || die "Cannot create $next"
    fi
    dev=$(losetup -f --show -o "$off" --sizelimit "$size" "$img") ||
      die "losetup failed for $img"
  fi
  cleanup_push losetup -d "$dev"
  LOOP_DEV=$dev
}

# --- Growing ------------------------------------------------------------

# image_grow <img> <GiB> : extend the file by <GiB>, extend the physically last partition (must be
# root) to the end, e2fsck + resize2fs. Leaves no loop device behind.
image_grow() {
  local img=$1 gib=$2 json n rc=0
  log_info "Growing $img by $gib GiB ..."
  truncate -s "+${gib}G" "$img"
  json=$(partitions_json "$img")
  n=$(part_last "$json")
  [ "$n" = "$(part_role "$json" root)" ] ||
    die "The last partition ($n) is not the root partition; cannot grow"
  if [ "$(jq -r .partitiontable.label <<<"$json")" = gpt ]; then
    # Move the backup GPT to the new end of the file to make room for the partition
    sfdisk -q --relocate gpt-bak-std "$img"
  fi
  printf ', +\n' | sfdisk -q --no-reread --no-tell-kernel --wipe never --wipe-partitions never -N "$n" "$img"
  json=$(partitions_json "$img")
  loop_attach "$img" "$(part_offset "$json" "$n")" "$(part_size "$json" "$n")"
  e2fsck -fy "$LOOP_DEV" || rc=$?
  [ "$rc" -le 1 ] || die "e2fsck reports an error ($rc) on $img partition $n"
  resize2fs "$LOOP_DEV"
  cleanup_pop
}

# --- Mounting --------------------------------------------------------------

# mount_target <img> <target> : root to $TARGET_MNT (default /mnt/target), boot to
# $TARGET_MNT/boot/firmware (rpi4) or $TARGET_MNT/boot/efi (vm). Torn down by cleanup_run.
# Raspberry Pi OS: p1 FAT (boot), p2 ext4 (root). Debian nocloud: p1 root (physically last),
# p14 BIOS boot (amd64 only), p15 ESP.
mount_target() {
  local img=$1 kind json root boot bootdir
  kind=$(target_kind "$2")
  case "$kind" in
    rpi4) bootdir=boot/firmware ;;
    vm) bootdir=boot/efi ;;
  esac
  json=$(partitions_json "$img")
  root=$(part_role "$json" root)
  boot=$(part_role "$json" boot)

  loop_attach "$img" "$(part_offset "$json" "$root")" "$(part_size "$json" "$root")"
  mkdir -p "$TARGET_MNT"
  mount "$LOOP_DEV" "$TARGET_MNT"
  cleanup_push umount "$TARGET_MNT"

  loop_attach "$img" "$(part_offset "$json" "$boot")" "$(part_size "$json" "$boot")"
  mkdir -p "$TARGET_MNT/$bootdir"
  mount "$LOOP_DEV" "$TARGET_MNT/$bootdir"
  cleanup_push umount "$TARGET_MNT/$bootdir"
}
