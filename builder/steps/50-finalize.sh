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

# Step 50: finish the work image ($CACHE_DIR/work/<target>.img from 40-provision-app.sh).
#  1. Mount it and clean the root file system (caches, logs, machine-id, SSH host keys,
#     histories, build leftovers). No chroot: only files are removed or written.
#  2. Unmount, shrink the root file system and its partition (the physically last one) to the
#     data + 16 MiB, cut the file behind it and zero the free blocks (zerofree), so deleted
#     build files (build.env) cannot be read back and xz packs better.
#  3. Outputs in $OUT_DIR (default out/), date = $BUILD_DATE (UTC, YYYYMMDD):
#     rpi4: emulationstation-rpi4-<date>.img.xz + .img.xz.sha256
#     vm:   emulationstation-vm-<arch>-<date>.raw (sparse, shrunk size: enlarge it before the
#           first boot) + .qcow2 (not compressed: faster to write and to run; use
#           "qemu-img convert -c" yourself to share it), resized to $FINALIZE_QCOW2_SIZE virtual
#           size (sparse); the first boot (growpart) grows root to fill it
#  4. Append a section to $OUT_DIR/build-report.txt and remove the work image.
# Usage: bash builder/steps/50-finalize.sh <rpi4|vm-amd64|vm-arm64>; configuration from $BUILD_CONFIG.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# shellcheck source=builder/lib/log.sh
. "$ROOT/builder/lib/log.sh"
# shellcheck source=builder/lib/config.sh
. "$ROOT/builder/lib/config.sh"
# shellcheck source=builder/lib/image.sh
. "$ROOT/builder/lib/image.sh"
# shellcheck source=builder/lib/cache.sh
. "$ROOT/builder/lib/cache.sh"

FINALIZE_BUFFER_MIB=16
# virtual disk size of the VM qcow2 (sparse, costs no space; root grows to it at the first boot)
FINALIZE_QCOW2_SIZE=16G
_MIB=1048576
# Same suffix as builder/lib/chroot.sh uses for its backups
_FINALIZE_BAK=.emulationstation-build-bak

# --- pure helpers ------------------------------------------------------------------

# finalize_out_name <target> <date> : file name without extension
finalize_out_name() {
  case "$1" in
    rpi4 | vm-amd64 | vm-arm64) printf 'emulationstation-%s-%s\n' "$1" "$2" ;;
    *) die "Unknown target: $1" 2 ;;
  esac
}

# finalize_part_sectors <start> <fs-bytes> <sector-size> <old-size> : new partition size in
# sectors. The file system plus a 16 MiB buffer; the END is rounded up to a whole MiB.
# Never larger than <old-size>.
finalize_part_sectors() {
  local start=$1 fs=$2 ss=$3 old=$4 end_bytes end
  [ $((_MIB % ss)) -eq 0 ] || die "Unsupported sector size: $ss"
  end_bytes=$((start * ss + fs + FINALIZE_BUFFER_MIB * _MIB))
  # shellcheck disable=SC2017  # rounding to whole MiB is wanted
  end=$(((end_bytes + _MIB - 1) / _MIB * (_MIB / ss)))
  if [ $((end - start)) -gt "$old" ]; then
    printf '%s\n' "$old"
  else
    printf '%s\n' $((end - start))
  fi
}

# finalize_image_bytes <dos|gpt> <end-bytes> : new file size. GPT keeps 1 MiB behind the last
# partition for the backup GPT (gpt-bak-mini needs 35 sectors).
finalize_image_bytes() {
  case "$1" in
    dos) printf '%s\n' "$2" ;;
    gpt) printf '%s\n' $(($2 + _MIB)) ;;
    *) die "Unknown partition table: $1" ;;
  esac
}

# finalize_sha256_file <file> : writes <file>.sha256 with "<hex>  <name>" (bare name, so
# "sha256sum -c" works in the same directory)
finalize_sha256_file() {
  local dir name
  dir=$(dirname "$1")
  name=$(basename "$1")
  (cd "$dir" && sha256sum "$name" >"$name.sha256.tmp" && mv -f "$name.sha256.tmp" "$name.sha256")
}

# finalize_report_section <target> <date> <cache-key> <module-report> [file...] : one report
# section on stdout. <module-report> is $CACHE_DIR/report-<target>-<key>.txt from step 30.
finalize_report_section() {
  local target=$1 date=$2 key=$3 modrep=$4 f size sum line
  shift 4
  printf '== Target: %s ==\n' "$target"
  printf 'Build date (UTC): %s\n' "$date"
  printf 'Upstream image: %s\n' "$(target_image_url "$target")"
  printf 'Upstream checksum: %s\n' "$(target_image_checksum "$target")"
  printf 'RetroPie-Setup: %s\n' "${CFG_RETROPIE_SETUP_REF:-}"
  printf 'EPIC Noir theme: %s\n' "${CFG_EPICNOIR_REF:-}"
  printf 'xpadneo: %s\n' "${CFG_XPADNEO_REF:-}"
  printf 'Cache key: %s\n' "$key"
  if [ ! -f "$modrep" ]; then
    printf 'Failed modules: unknown (module report %s is missing)\n' "$modrep"
  elif [ ! -s "$modrep" ]; then
    printf 'Failed modules: none\n'
  else
    printf 'Failed modules (module, exit code, hint):\n'
    while IFS= read -r line || [ -n "$line" ]; do
      printf '  %s\n' "$line"
    done <"$modrep"
  fi
  printf 'CA certificate: out/emulationstation-ca.crt. Import it into your browser or OS, then open https://%s:11944/\n' \
    "${CFG_HOSTNAME:-emulationstation}"
  case "$target" in
    vm-*)
      printf 'VM disk: the qcow2 has a %s virtual size (root grows to it at the first boot); enlarge the raw before the first boot (e.g. truncate -s %s or VBoxManage modifymedium --resize).\n' \
        "$FINALIZE_QCOW2_SIZE" "$FINALIZE_QCOW2_SIZE"
      ;;
  esac
  printf 'Output files:\n'
  for f in "$@"; do
    size=$(stat -c %s "$f")
    sum=$(sha256sum <"$f" | cut -d' ' -f1)
    printf '  %s  %s bytes (%s)  sha256:%s\n' "$(basename "$f")" "$size" \
      "$(numfmt --to=iec-i --suffix=B "$size")" "$sum"
  done
  printf '\n'
}

# --- clean the root file system ------------------------------------------------------------

# finalize_clean_root <root> <rpi4|vm> <boot-dir relative to root>
finalize_clean_root() {
  local r=${1:?} kind=$2 boot=$3 f d host
  [ "$(readlink -f "$r")" != / ] || die "finalize_clean_root on / is not allowed"
  [ -d "$r/etc" ] || die "Not a root file system: $r"

  # rpi4: Pi OS grows the root partition only at the first boot (rpi-resize.service has
  # ConditionFirstBoot=yes, initramfs resize_early needs "resize" on the kernel command line).
  if [ "$kind" = rpi4 ]; then
    f="$r/$boot/cmdline.txt"
    [ -f "$f" ] || die "cmdline.txt is missing: $f"
    tr -s ' \t' '\n' <"$f" | grep -qx resize ||
      die "'resize' is missing in $f; the root partition would not grow at the first boot"
  fi

  # build leftovers. build.env holds passwords: overwrite it before it is deleted.
  if [ -e "$r/root/provision/build.env" ]; then
    shred -u "$r/root/provision/build.env"
  fi
  rm -rf "${r:?}/root/provision"
  rm -f "$r/usr/sbin/policy-rc.d" "$r/usr/sbin/policy-rc.d$_FINALIZE_BAK"
  if [ -e "$r/etc/resolv.conf$_FINALIZE_BAK" ] || [ -L "$r/etc/resolv.conf$_FINALIZE_BAK" ]; then
    log_warn "Restoring the original resolv.conf from a left-over backup"
    rm -f "$r/etc/resolv.conf"
    mv "$r/etc/resolv.conf$_FINALIZE_BAK" "$r/etc/resolv.conf"
  fi
  host=${HOST_RESOLV:-/etc/resolv.conf}
  if [ -f "$r/etc/resolv.conf" ] && [ ! -L "$r/etc/resolv.conf" ] && [ -s "$host" ] &&
    cmp -s "$host" "$r/etc/resolv.conf"; then
    die "$r/etc/resolv.conf is the temporary copy from the build (chroot teardown failed)"
  fi

  # apt (like "apt-get clean") and package lists
  rm -f "$r"/var/cache/apt/*.bin
  for d in "$r/var/cache/apt/archives" "$r/var/lib/apt/lists"; do
    [ ! -d "$d" ] || find "$d" -type f ! -name lock -delete
  done

  # logs: delete rotated logs and journal files, empty the rest, keep the directories
  if [ -d "$r/var/log" ]; then
    find "$r/var/log" -type f \( -name '*.gz' -o -name '*.xz' -o -name '*.[0-9]' -o -name '*.old' \) -delete
    [ ! -d "$r/var/log/journal" ] || find "$r/var/log/journal" -mindepth 1 -delete
    find "$r/var/log" -type f -exec truncate -s 0 {} +
  fi

  # identity of this build: systemd makes a new machine-id at the first boot ("uninitialized"
  # also makes it a first boot, see machine-id(5)); D-Bus uses the same id
  rm -f "$r/etc/machine-id"
  printf 'uninitialized\n' >"$r/etc/machine-id"
  chmod 0444 "$r/etc/machine-id"
  if [ -d "$r/var/lib/dbus" ]; then
    rm -f "$r/var/lib/dbus/machine-id"
    ln -s /etc/machine-id "$r/var/lib/dbus/machine-id"
  fi
  rm -f "$r/var/lib/systemd/random-seed"
  rm -f "$r"/etc/ssh/ssh_host_*

  # histories and temporary files
  rm -f "$r/root/.bash_history" "$r"/home/*/.bash_history
  rm -rf "$r"/home/*/RetroPie-Setup/tmp
  for d in "$r/tmp" "$r/var/tmp"; do
    [ ! -d "$d" ] || find "$d" -mindepth 1 -delete
  done

  # machine-id "uninitialized" makes the next boot a "first boot" for systemd, so Debian's
  # systemd-firstboot.service (sysinit.target.wants, ConditionFirstBoot=yes) runs and asks on
  # the console for every value without a file: locale (/etc/locale.conf), keymap
  # (/etc/vconsole.conf, which Debian does not use) and time zone. The boot would wait there.
  # We set these values ourselves (build.conf at build time, emulationstation.txt at the first
  # boot), so mask the wizard. Other ConditionFirstBoot units (rpi-resize) still run.
  mkdir -p "$r/etc/systemd/system"
  ln -sfn /dev/null "$r/etc/systemd/system/systemd-firstboot.service"

  # VM: systemd-ssh-generator (systemd 257) would bind sshd to AF_VSOCK port 22 in a VM.
  # Mask the generator so sshd listens on 11911 only.
  if [ "$kind" = vm ]; then
    mkdir -p "$r/etc/systemd/system-generators"
    ln -sfn /dev/null "$r/etc/systemd/system-generators/systemd-ssh-generator"
  fi
  log_info "Root file system cleaned ($kind)"
}

# --- shrink ----------------------------------------------------------------------------

# _finalize_fsck <dev> : e2fsck -fy, exit code 1 (errors fixed) counts as success
_finalize_fsck() {
  local rc=0
  e2fsck -fy "$1" || rc=$?
  [ "$rc" -le 1 ] || die "e2fsck reports an error ($rc) on $1"
}

# _finalize_fs_bytes <dev> : size of the ext4 file system in bytes
_finalize_fs_bytes() {
  dumpe2fs -h "$1" 2>/dev/null | awk -F: '
    /^Block count:/ { gsub(/ /, "", $2); c = $2 }
    /^Block size:/  { gsub(/ /, "", $2); s = $2 }
    END { if (c == "" || s == "") exit 1; printf "%d\n", c * s }'
}

# finalize_shrink <img> : shrink the root file system and its partition (must be the physically
# last one), cut the file, zero the free blocks. dos: Pi OS (p2). gpt: Debian (p1, backup GPT is
# moved to the new end). Leaves no loop device behind.
finalize_shrink() {
  local img=$1 json n label ss start size fs new end bytes
  json=$(partitions_json "$img")
  n=$(part_last "$json")
  [ "$n" = "$(part_role "$json" root)" ] ||
    die "The last partition ($n) is not the root partition; cannot shrink"
  label=$(jq -r .partitiontable.label <<<"$json")
  ss=$(jq -r '.partitiontable.sectorsize // 512' <<<"$json")
  start=$(($(part_offset "$json" "$n") / ss))
  size=$(($(part_size "$json" "$n") / ss))

  loop_attach "$img" $((start * ss)) $((size * ss))
  _finalize_fsck "$LOOP_DEV"
  resize2fs -M "$LOOP_DEV"
  fs=$(_finalize_fs_bytes "$LOOP_DEV") || die "Cannot read the file system size of $img partition $n"
  cleanup_pop

  new=$(finalize_part_sectors "$start" "$fs" "$ss" "$size")
  log_info "Shrinking partition $n: $size -> $new sectors (file system $fs bytes + ${FINALIZE_BUFFER_MIB} MiB)"
  printf ',%s\n' "$new" |
    sfdisk -q --no-reread --no-tell-kernel --wipe never --wipe-partitions never -N "$n" "$img"
  end=$(((start + new) * ss))
  bytes=$(finalize_image_bytes "$label" "$end")
  if [ "$label" = gpt ]; then
    # Before the cut: move the backup GPT right behind the last partition. This also sets
    # "last usable LBA" in both headers to just behind the partition (libfdisk
    # gpt_minimize_alternative_lba). Without this step, the primary header's last usable LBA
    # would lie behind the end of the cut file, and the backup header would be cut off.
    # libfdisk then rejects both headers (gpt_check_lba_sanity), and no relocate can repair it.
    sfdisk -q --relocate gpt-bak-mini "$img"
  fi
  # gpt: <bytes> = partition end + 1 MiB, more than the 35 sectors of the minimal backup area
  truncate -s "$bytes" "$img"
  if [ "$label" = gpt ]; then
    # UEFI wants the backup GPT in the last sector: move it to the new end of the file
    sfdisk -q --relocate gpt-bak-std "$img"
  fi

  json=$(partitions_json "$img")
  [ "$(part_size "$json" "$n")" -eq $((new * ss)) ] || die "Partition $n has the wrong size after sfdisk"
  [ $(($(part_offset "$json" "$n") + $(part_size "$json" "$n"))) -le "$(stat -c %s "$img")" ] ||
    die "Partition $n ends behind the end of $img"

  loop_attach "$img" $((start * ss)) $((new * ss))
  # use the buffer: the file system fills the partition again
  resize2fs "$LOOP_DEV"
  _finalize_fsck "$LOOP_DEV"
  log_info "Zeroing free blocks (zerofree) ..."
  zerofree -v "$LOOP_DEV"
  e2fsck -fn "$LOOP_DEV" || die "e2fsck -fn reports errors on $img partition $n"
  cleanup_pop
  log_info "Image shrunk: $img ($bytes bytes)"
}

# finalize_image_report <root> <target> : the lines of <root>/usr/local/lib/emulationstation/build-report.txt
# (written by provision/common/app-35-usb.sh, e.g. "usb ntfs3 missing for kernel 6.12.0") as a
# block for out/build-report.txt. Prints nothing when the file is missing or empty.
finalize_image_report() {
  local f="$1/usr/local/lib/emulationstation/build-report.txt" line
  [ -s "$f" ] || return 0
  printf 'Notes from the image build (%s):\n' "$2"
  while IFS= read -r line || [ -n "$line" ]; do
    printf '%s\n' "$line"
  done <"$f"
  printf '\n'
}

# --- outputs ---------------------------------------------------------------------------

# finalize_outputs <target> <img> <out-dir> <date> : writes the output files and sets
# FINALIZE_FILES (paths, for the report)
finalize_outputs() {
  local target=$1 img=$2 out=$3 date=$4 name f raw qcow
  name=$(finalize_out_name "$target" "$date")
  mkdir -p "$out"
  FINALIZE_FILES=()
  case "$(target_kind "$target")" in
    rpi4)
      f="$out/$name.img.xz"
      log_info "Compressing $f (xz -T0 -6) ..."
      xz -T0 -6 -c "$img" >"$f.tmp"
      mv -f "$f.tmp" "$f"
      finalize_sha256_file "$f"
      FINALIZE_FILES=("$f" "$f.sha256")
      ;;
    vm)
      raw="$out/$name.raw"
      qcow="$out/$name.qcow2"
      log_info "Writing $raw (sparse) ..."
      cp --sparse=always "$img" "$raw.tmp"
      mv -f "$raw.tmp" "$raw"
      log_info "Writing $qcow ..."
      qemu-img convert -f raw -O qcow2 "$img" "$qcow.tmp"
      qemu-img resize -f qcow2 "$qcow.tmp" "$FINALIZE_QCOW2_SIZE"
      mv -f "$qcow.tmp" "$qcow"
      FINALIZE_FILES=("$qcow" "$raw")
      ;;
  esac
}

main() {
  local target=${1:-} kind boot work date key out modrep imgrep
  [ -n "$target" ] || die "Usage: $0 <target>" 2
  [ -n "${BUILD_CONFIG:-}" ] || die "BUILD_CONFIG is not set" 2
  cleanup_install
  config_load "$BUILD_CONFIG"

  kind=$(target_kind "$target")
  case "$kind" in
    rpi4) boot=boot/firmware ;;
    vm) boot=boot/efi ;;
  esac
  work=$(cache_work_path "$target")
  [ -f "$work" ] || die "work image missing: $work (step 40-provision-app.sh)"
  date=${BUILD_DATE:-$(date -u +%Y%m%d)}
  [[ $date =~ ^[0-9]{8}$ ]] || die "BUILD_DATE must be YYYYMMDD, not '$date'"
  out=${OUT_DIR:-$ROOT/out}
  key=$(cache_key "$target")
  modrep="$CACHE_DIR/report-$target-$key.txt"

  mount_target "$work" "$target"
  # read before the clean-up, written to the report after the image is finished
  imgrep=$(finalize_image_report "$TARGET_MNT" "$target")
  finalize_clean_root "$TARGET_MNT" "$kind" "$boot"
  sync
  # shellcheck disable=SC2119  # without exit code: returns instead of exiting
  cleanup_run || die "Unmounting the work image failed"

  finalize_shrink "$work"
  finalize_outputs "$target" "$work" "$out" "$date"
  finalize_report_section "$target" "$date" "$key" "$modrep" "${FINALIZE_FILES[@]}" >>"$out/build-report.txt"
  if [ -n "$imgrep" ]; then printf '%s\n\n' "$imgrep" >>"$out/build-report.txt"; fi
  rm -f "$work"
  log_info "Done: ${FINALIZE_FILES[*]}"
  log_info "Report: $out/build-report.txt"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
