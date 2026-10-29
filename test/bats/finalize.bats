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

# Tests for builder/steps/50-finalize.sh (clean, shrink, outputs, report) and the
# target flow in builder/run.sh (owner fix on exit, `all`).
# shellcheck disable=SC2016,SC2030,SC2031,SC2034,SC2317

bats_require_minimum_version 1.5.0

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  STUBS="$BATS_TEST_TMPDIR/stubs"
  CALLS="$BATS_TEST_TMPDIR/calls"
  mkdir -p "$STUBS"
  : >"$CALLS"
  export CALLS
}

load_step() {
  # shellcheck source=/dev/null
  . "$ROOT/builder/steps/50-finalize.sh"
}

stub_cmd() {
  local c
  for c in "$@"; do
    printf '#!/usr/bin/env bash\necho "%s $*" >>"$CALLS"\n' "$c" >"$STUBS/$c"
    chmod +x "$STUBS/$c"
  done
  PATH="$STUBS:$PATH"
}

need_real_tools() {
  local t
  [ "$(id -u)" -eq 0 ] || skip "root missing (runs in the builder container)"
  for t in sfdisk losetup zerofree e2fsck resize2fs mkfs.ext4 debugfs jq; do
    command -v "$t" >/dev/null || skip "$t missing (runs in the builder container)"
  done
}

# --- pure functions -----------------------------------------------------------

@test "finalize_part_sectors: fs size + 16 MiB, end rounded up to a whole MiB" {
  load_step
  # start 520 MiB (Pi p2), fs exactly 100 MiB -> 116 MiB, already aligned
  run finalize_part_sectors 1064960 $((100 * 1048576)) 512 99999999
  [ "$status" -eq 0 ]
  [ "$output" -eq $((116 * 2048)) ]
  # fs 100 MiB + 1 byte -> end rounded up to the next MiB
  run finalize_part_sectors 1064960 $((100 * 1048576 + 1)) 512 99999999
  [ "$output" -eq $((117 * 2048)) ]
  # start not MiB aligned (2047): the END is aligned, not the size
  run finalize_part_sectors 2047 $((10 * 1048576)) 512 99999999
  [ "$(((2047 + output) % 2048))" -eq 0 ]
  [ "$((output * 512))" -ge $((26 * 1048576)) ]
  # 4096-byte sectors
  run finalize_part_sectors 256 $((100 * 1048576)) 4096 99999999
  [ "$output" -eq $((116 * 256)) ]
}

@test "finalize_part_sectors: never larger than the old partition" {
  load_step
  run finalize_part_sectors 2048 $((100 * 1048576)) 512 4096
  [ "$status" -eq 0 ]
  [ "$output" -eq 4096 ]
}

@test "finalize_image_bytes: dos ends with the partition, gpt adds 1 MiB for the backup GPT" {
  load_step
  [ "$(finalize_image_bytes dos 123731968)" -eq 123731968 ]
  [ "$(finalize_image_bytes gpt 123731968)" -eq $((123731968 + 1048576)) ]
  run finalize_image_bytes mbr 1
  [ "$status" -ne 0 ]
}

@test "finalize_out_name: names from the global constraints" {
  load_step
  [ "$(finalize_out_name rpi4 20261028)" = emulationstation-rpi4-20261028 ]
  [ "$(finalize_out_name vm-amd64 20261028)" = emulationstation-vm-amd64-20261028 ]
  [ "$(finalize_out_name vm-arm64 20261028)" = emulationstation-vm-arm64-20261028 ]
  run finalize_out_name vm 20261028
  [ "$status" -ne 0 ]
}

@test "finalize_sha256_file: line with the bare file name, sha256sum -c works in the directory" {
  load_step
  local d="$BATS_TEST_TMPDIR/out"
  mkdir -p "$d"
  printf 'hello\n' >"$d/emulationstation-rpi4-20261028.img.xz"
  finalize_sha256_file "$d/emulationstation-rpi4-20261028.img.xz"
  [ -f "$d/emulationstation-rpi4-20261028.img.xz.sha256" ]
  [ "$(cat "$d/emulationstation-rpi4-20261028.img.xz.sha256")" = \
    "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03  emulationstation-rpi4-20261028.img.xz" ]
  (cd "$d" && sha256sum -c emulationstation-rpi4-20261028.img.xz.sha256)
}

@test "finalize_report_section: pins, cache key, failed modules, CA hint, files with size and sha256" {
  load_step
  local d="$BATS_TEST_TMPDIR/out" rep="$BATS_TEST_TMPDIR/report-rpi4-abc.txt"
  mkdir -p "$d"
  printf 'hello\n' >"$d/a.img.xz"
  printf 'lr-mupen64plus 1 see base log\n' >"$rep"
  CFG_RPI_IMAGE_URL=https://example.org/pi.img.xz
  CFG_RPI_IMAGE_CHECKSUM=sha256:1111
  CFG_RETROPIE_SETUP_REF=f5ebfba6
  CFG_EPICNOIR_REF=noirref
  CFG_XPADNEO_REF=xpref
  CFG_HOSTNAME=retro
  run finalize_report_section rpi4 20261028 abc "$rep" "$d/a.img.xz"
  [ "$status" -eq 0 ]
  [[ "$output" == *"== Target: rpi4 =="* ]]
  [[ "$output" == *"Build date (UTC): 20261028"* ]]
  [[ "$output" == *"Upstream image: https://example.org/pi.img.xz"* ]]
  [[ "$output" == *"Upstream checksum: sha256:1111"* ]]
  [[ "$output" == *"RetroPie-Setup: f5ebfba6"* ]]
  [[ "$output" == *"EPIC Noir theme: noirref"* ]]
  [[ "$output" == *"xpadneo: xpref"* ]]
  [[ "$output" == *"Cache key: abc"* ]]
  [[ "$output" == *"  lr-mupen64plus 1 see base log"* ]]
  [[ "$output" == *"emulationstation-ca.crt"* ]]
  [[ "$output" == *"https://retro:11944/"* ]]
  [[ "$output" == *"  a.img.xz  6 bytes"*"sha256:5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"* ]]
}

@test "finalize_report_section: VM targets say the qcow2 virtual size and to enlarge the raw" {
  load_step
  [ "$FINALIZE_QCOW2_SIZE" = 16G ]
  : >"$BATS_TEST_TMPDIR/r.txt"
  run finalize_report_section vm-arm64 20261028 abc "$BATS_TEST_TMPDIR/r.txt"
  [[ "$output" == *"qcow2 has a 16G virtual size"* ]]
  [[ "$output" == *"enlarge the raw before the first boot"* ]]
  run finalize_report_section rpi4 20261028 abc "$BATS_TEST_TMPDIR/r.txt"
  [[ "$output" != *"VM disk"* ]]
}

@test "finalize_outputs vm: qcow2 converted and resized to FINALIZE_QCOW2_SIZE, raw kept" {
  load_step
  local out="$BATS_TEST_TMPDIR/out"
  printf '#!/usr/bin/env bash\necho "qemu-img $*" >>"$CALLS"\n[ "$1" != convert ] || : >"${!#}"\n' >"$STUBS/qemu-img"
  chmod +x "$STUBS/qemu-img"
  PATH="$STUBS:$PATH"
  printf 'img' >"$BATS_TEST_TMPDIR/w.img"
  finalize_outputs vm-amd64 "$BATS_TEST_TMPDIR/w.img" "$out" 20261028
  grep -qx "qemu-img convert -f raw -O qcow2 $BATS_TEST_TMPDIR/w.img $out/emulationstation-vm-amd64-20261028.qcow2.tmp" "$CALLS"
  grep -qx "qemu-img resize -f qcow2 $out/emulationstation-vm-amd64-20261028.qcow2.tmp 16G" "$CALLS"
  [ -f "$out/emulationstation-vm-amd64-20261028.qcow2" ]
  [ "$(cat "$out/emulationstation-vm-amd64-20261028.raw")" = img ]
  [ "${FINALIZE_FILES[*]}" = "$out/emulationstation-vm-amd64-20261028.qcow2 $out/emulationstation-vm-amd64-20261028.raw" ]
}

@test "finalize_report_section: empty module report -> none; missing report -> unknown" {
  load_step
  local rep="$BATS_TEST_TMPDIR/report.txt"
  : >"$rep"
  run finalize_report_section vm-amd64 20261028 abc "$rep"
  [[ "$output" == *"Failed modules: none"* ]]
  run finalize_report_section vm-amd64 20261028 abc "$BATS_TEST_TMPDIR/nothing.txt"
  [[ "$output" == *"Failed modules: unknown"* ]]
}

@test "finalize_image_report: lines of the image file go into a block; nothing without the file" {
  load_step
  local r="$BATS_TEST_TMPDIR/img"
  mkdir -p "$r/usr/local/lib/emulationstation"
  run finalize_image_report "$r" rpi4
  [ -z "$output" ]
  : >"$r/usr/local/lib/emulationstation/build-report.txt"
  run finalize_image_report "$r" rpi4
  [ -z "$output" ]
  printf 'usb ntfs3 missing for kernel 6.12.0\nusb exfat missing for kernel 6.12.0\n' \
    >"$r/usr/local/lib/emulationstation/build-report.txt"
  run finalize_image_report "$r" rpi4
  [[ "${lines[0]}" == "Notes from the image build (rpi4):" ]]
  [[ "$output" == *$'\nusb ntfs3 missing for kernel 6.12.0\n'* ]]
  [[ "$output" == *"usb exfat missing for kernel 6.12.0"* ]]
}

# --- cleaning the root file system (plain directory, no root needed) ---------------------

make_root() {
  local r=$1
  mkdir -p "$r"/etc/ssh "$r"/var/cache/apt/archives/partial "$r"/var/lib/apt/lists/partial \
    "$r"/var/log/apt "$r"/var/log/journal/0123 "$r"/var/lib/dbus "$r"/var/lib/systemd \
    "$r"/root/provision "$r"/home/emulationstation/RetroPie-Setup/tmp/build "$r"/tmp/.x "$r"/var/tmp \
    "$r"/usr/sbin "$r"/boot/firmware "$r"/boot/efi "$r"/etc/systemd/system
  printf '0123456789abcdef0123456789abcdef\n' >"$r/etc/machine-id"
  printf '0123456789abcdef0123456789abcdef\n' >"$r/var/lib/dbus/machine-id"
  printf 'seed' >"$r/var/lib/systemd/random-seed"
  : >"$r/etc/ssh/ssh_host_ed25519_key"
  : >"$r/etc/ssh/ssh_host_ed25519_key.pub"
  printf 'keep\n' >"$r/etc/ssh/sshd_config"
  printf 'deb' >"$r/var/cache/apt/archives/foo.deb"
  printf 'p' >"$r/var/cache/apt/archives/partial/bar.deb"
  : >"$r/var/cache/apt/archives/lock"
  printf 'bin' >"$r/var/cache/apt/pkgcache.bin"
  printf 'list' >"$r/var/lib/apt/lists/deb.debian.org_dists_trixie_InRelease"
  printf 'log line\n' >"$r/var/log/syslog"
  printf 'old' >"$r/var/log/syslog.1"
  printf 'gz' >"$r/var/log/dpkg.log.2.gz"
  printf 'apt\n' >"$r/var/log/apt/history.log"
  printf 'j' >"$r/var/log/journal/0123/system.journal"
  printf 'ls\n' >"$r/root/.bash_history"
  printf 'ls\n' >"$r/home/emulationstation/.bash_history"
  printf 'USER_PASSWORD="secret"\n' >"$r/root/provision/build.env"
  printf 'x' >"$r/home/emulationstation/RetroPie-Setup/tmp/build/f"
  printf 'x' >"$r/tmp/a"
  printf 'x' >"$r/tmp/.x/b"
  printf 'x' >"$r/var/tmp/c"
  printf '#!/bin/sh\nexit 101\n' >"$r/usr/sbin/policy-rc.d"
  ln -s ../run/systemd/resolve/stub-resolv.conf "$r/etc/resolv.conf"
  printf 'console=tty1 root=PARTUUID=x rootwait resize\n' >"$r/boot/firmware/cmdline.txt"
}

@test "finalize_clean_root rpi4: caches, logs, ids, keys, histories, temp files; machine-id uninitialized" {
  load_step
  local r="$BATS_TEST_TMPDIR/root"
  make_root "$r"
  stub_cmd shred
  HOST_RESOLV="$BATS_TEST_TMPDIR/host-resolv"
  printf 'nameserver 10.0.0.1\n' >"$HOST_RESOLV"
  run finalize_clean_root "$r" rpi4 boot/firmware
  [ "$status" -eq 0 ]
  [ "$(cat "$r/etc/machine-id")" = uninitialized ]
  [ "$(readlink "$r/var/lib/dbus/machine-id")" = /etc/machine-id ]
  [ ! -e "$r/var/lib/systemd/random-seed" ]
  [ -z "$(ls "$r"/etc/ssh/ssh_host_* 2>/dev/null)" ]
  [ -f "$r/etc/ssh/sshd_config" ]
  [ ! -e "$r/var/cache/apt/archives/foo.deb" ]
  [ ! -e "$r/var/cache/apt/archives/partial/bar.deb" ]
  [ ! -e "$r/var/cache/apt/pkgcache.bin" ]
  [ -d "$r/var/cache/apt/archives/partial" ]
  [ -z "$(find "$r/var/lib/apt/lists" -type f)" ]
  [ -d "$r/var/lib/apt/lists/partial" ]
  # logs: files truncated, rotated logs and journal files deleted, directories kept
  [ -f "$r/var/log/syslog" ] && [ ! -s "$r/var/log/syslog" ]
  [ -f "$r/var/log/apt/history.log" ] && [ ! -s "$r/var/log/apt/history.log" ]
  [ ! -e "$r/var/log/syslog.1" ]
  [ ! -e "$r/var/log/dpkg.log.2.gz" ]
  [ -d "$r/var/log/journal" ]
  [ -z "$(ls -A "$r/var/log/journal")" ]
  [ ! -e "$r/root/.bash_history" ]
  [ ! -e "$r/home/emulationstation/.bash_history" ]
  [ ! -e "$r/root/provision" ]
  grep -q "shred -u $r/root/provision/build.env" "$CALLS"
  [ ! -e "$r/home/emulationstation/RetroPie-Setup/tmp" ]
  [ -d "$r/home/emulationstation/RetroPie-Setup" ]
  [ -d "$r/tmp" ] && [ -z "$(ls -A "$r/tmp")" ]
  [ -d "$r/var/tmp" ] && [ -z "$(ls -A "$r/var/tmp")" ]
  [ ! -e "$r/usr/sbin/policy-rc.d" ]
  [ "$(readlink "$r/etc/resolv.conf")" = ../run/systemd/resolve/stub-resolv.conf ]
  [ "$(readlink "$r/etc/systemd/system/systemd-firstboot.service")" = /dev/null ]
  # the ssh generator is only masked on the VM
  [ ! -e "$r/etc/systemd/system-generators/systemd-ssh-generator" ]
}

@test "finalize_clean_root vm: masks systemd-ssh-generator (AF_VSOCK sshd)" {
  load_step
  local r="$BATS_TEST_TMPDIR/root"
  make_root "$r"
  stub_cmd shred
  HOST_RESOLV=/dev/null
  run finalize_clean_root "$r" vm boot/efi
  [ "$status" -eq 0 ]
  [ "$(readlink "$r/etc/systemd/system-generators/systemd-ssh-generator")" = /dev/null ]
  [ "$(cat "$r/etc/machine-id")" = uninitialized ]
}

@test "finalize_clean_root rpi4: cmdline.txt without 'resize' -> error" {
  load_step
  local r="$BATS_TEST_TMPDIR/root"
  make_root "$r"
  stub_cmd shred
  printf 'console=tty1 root=PARTUUID=x rootwait resize=no\n' >"$r/boot/firmware/cmdline.txt"
  run finalize_clean_root "$r" rpi4 boot/firmware
  [ "$status" -ne 0 ]
  [[ "$output" == *resize* ]]
  rm "$r/boot/firmware/cmdline.txt"
  run finalize_clean_root "$r" rpi4 boot/firmware
  [ "$status" -ne 0 ]
}

@test "finalize_clean_root: a left-over resolv.conf backup is restored" {
  load_step
  local r="$BATS_TEST_TMPDIR/root"
  make_root "$r"
  stub_cmd shred
  rm "$r/etc/resolv.conf"
  printf 'nameserver 10.0.0.1\n' >"$r/etc/resolv.conf"
  ln -s ../run/systemd/resolve/stub-resolv.conf "$r/etc/resolv.conf.emulationstation-build-bak"
  HOST_RESOLV=/dev/null
  run finalize_clean_root "$r" vm boot/efi
  [ "$status" -eq 0 ]
  [ "$(readlink "$r/etc/resolv.conf")" = ../run/systemd/resolve/stub-resolv.conf ]
  [ ! -e "$r/etc/resolv.conf.emulationstation-build-bak" ]
}

@test "finalize_clean_root: the temporary build resolv.conf (copy of the host file) -> error" {
  load_step
  local r="$BATS_TEST_TMPDIR/root"
  make_root "$r"
  stub_cmd shred
  rm "$r/etc/resolv.conf"
  HOST_RESOLV="$BATS_TEST_TMPDIR/host-resolv"
  printf 'nameserver 10.0.0.1\n' >"$HOST_RESOLV"
  cp "$HOST_RESOLV" "$r/etc/resolv.conf"
  run finalize_clean_root "$r" vm boot/efi
  [ "$status" -ne 0 ]
  [[ "$output" == *resolv.conf* ]]
}

@test "finalize_clean_root: refuses /" {
  load_step
  run finalize_clean_root / vm boot/efi
  [ "$status" -ne 0 ]
}

# --- step entry ---------------------------------------------------------------

@test "50-finalize.sh: no target -> exit 2; missing work image -> error" {
  run bash "$ROOT/builder/steps/50-finalize.sh"
  [ "$status" -eq 2 ]
  cat >"$BATS_TEST_TMPDIR/build.conf" <<EOF
USER_PASSWORD=x
RPI_IMAGE_CHECKSUM=sha256:$(printf 'a%.0s' {1..64})
EOF
  run env BUILD_CONFIG="$BATS_TEST_TMPDIR/build.conf" CACHE_DIR="$BATS_TEST_TMPDIR/cache" \
    bash "$ROOT/builder/steps/50-finalize.sh" rpi4
  [ "$status" -ne 0 ]
  [[ "$output" == *"work image missing"* ]]
}

@test "50-finalize.sh: invalid BUILD_DATE -> error" {
  mkdir -p "$BATS_TEST_TMPDIR/cache/work"
  : >"$BATS_TEST_TMPDIR/cache/work/rpi4.img"
  printf 'USER_PASSWORD=x\n' >"$BATS_TEST_TMPDIR/build.conf"
  run env BUILD_CONFIG="$BATS_TEST_TMPDIR/build.conf" CACHE_DIR="$BATS_TEST_TMPDIR/cache" BUILD_DATE=2026-10-28 \
    bash "$ROOT/builder/steps/50-finalize.sh" rpi4
  [ "$status" -ne 0 ]
  [[ "$output" == *BUILD_DATE* ]]
}

# --- 40-provision-app.sh: build.env is shredded (R24) ------------------------------------

@test "app_shred_env: shred -u on build.env, nothing to do without the file" {
  # shellcheck source=/dev/null
  . "$ROOT/builder/steps/40-provision-app.sh"
  stub_cmd shred
  printf 'x' >"$BATS_TEST_TMPDIR/build.env"
  app_shred_env "$BATS_TEST_TMPDIR/build.env"
  grep -qx "shred -u $BATS_TEST_TMPDIR/build.env" "$CALLS"
  : >"$CALLS"
  app_shred_env "$BATS_TEST_TMPDIR/none.env"
  [ ! -s "$CALLS" ]
}

# --- run.sh: owner of out/ and secrets/ (R22), report, `all` ---------------------------------

load_run() {
  # shellcheck source=/dev/null
  . "$ROOT/builder/run.sh"
  CACHE_DIR="$BATS_TEST_TMPDIR/cache"
}

@test "owner_fix: chown -R HOST_UID:HOST_GID on existing directories only" {
  load_run
  stub_cmd chown
  mkdir -p "$BATS_TEST_TMPDIR/out" "$BATS_TEST_TMPDIR/secrets"
  HOST_UID=1000 HOST_GID=1001 owner_fix "$BATS_TEST_TMPDIR/out" "$BATS_TEST_TMPDIR/secrets" "$BATS_TEST_TMPDIR/none"
  [ "$(cat "$CALLS")" = "chown -R 1000:1001 $BATS_TEST_TMPDIR/out
chown -R 1000:1001 $BATS_TEST_TMPDIR/secrets" ]
}

@test "owner_fix: nothing for unset, zero or non-numeric ids" {
  load_run
  stub_cmd chown
  mkdir -p "$BATS_TEST_TMPDIR/out"
  HOST_UID="" HOST_GID="" owner_fix "$BATS_TEST_TMPDIR/out"
  HOST_UID=0 HOST_GID=0 owner_fix "$BATS_TEST_TMPDIR/out"
  HOST_UID='1000;x' HOST_GID=1000 owner_fix "$BATS_TEST_TMPDIR/out"
  HOST_UID=1000 HOST_GID='' owner_fix "$BATS_TEST_TMPDIR/out"
  [ ! -s "$CALLS" ]
}

@test "run.sh: owner fix runs on exit for every target and keeps the exit code" {
  stub_cmd chown
  mkdir -p "$BATS_TEST_TMPDIR/out" "$BATS_TEST_TMPDIR/secrets"
  run env PATH="$PATH" HOST_UID=1000 HOST_GID=1000 OUT_DIR="$BATS_TEST_TMPDIR/out" \
    SECRETS_DIR="$BATS_TEST_TMPDIR/secrets" bash "$ROOT/builder/run.sh" nothing
  [ "$status" -eq 2 ]
  grep -qx "chown -R 1000:1000 $BATS_TEST_TMPDIR/out" "$CALLS"
  grep -qx "chown -R 1000:1000 $BATS_TEST_TMPDIR/secrets" "$CALLS"
}

@test "run.sh all: rpi4, vm-amd64, vm-arm64 in order; the first failure stops; report reset once" {
  load_run
  local order="$BATS_TEST_TMPDIR/order"
  OUT_DIR="$BATS_TEST_TMPDIR/out"
  mkdir -p "$OUT_DIR"
  printf 'old report\n' >"$OUT_DIR/build-report.txt"
  run_image() { echo "$1" >>"$order"; }
  run_targets all
  [ "$(cat "$order")" = "rpi4
vm-amd64
vm-arm64" ]
  run ! grep -q 'old report' "$OUT_DIR/build-report.txt"
  grep -q 'EmuStation Builder build report' "$OUT_DIR/build-report.txt"
  : >"$order"
  run_image() {
    echo "$1" >>"$order"
    [ "$1" != vm-amd64 ]
  }
  run run_targets all
  [ "$status" -ne 0 ]
  [ "$(cat "$order")" = "rpi4
vm-amd64" ]
}

@test "run.sh --base-only does not reset the build report" {
  load_run
  OUT_DIR="$BATS_TEST_TMPDIR/out"
  mkdir -p "$OUT_DIR"
  printf 'old report\n' >"$OUT_DIR/build-report.txt"
  run_image() { :; }
  BASE_ONLY=1
  run_targets rpi4
  grep -q 'old report' "$OUT_DIR/build-report.txt"
}

# --- shrink command order with stubs (no root needed) -----------------------------------

# stub_shrink <fixture> : stubs for finalize_shrink. sfdisk -N stores the new size, so the next
# partitions_json shows it; every command is logged in $CALLS. truncate really cuts the file.
stub_shrink() {
  FIXTURE=$1
  NEWSIZE="$BATS_TEST_TMPDIR/newsize"
  export NEWSIZE
  cat >"$STUBS/sfdisk" <<'EOF2'
#!/usr/bin/env bash
echo "sfdisk $*" >>"$CALLS"
for a in "$@"; do
  if [ "$a" = -N ]; then
    IFS=, read -r _ size
    echo "$size" >"$NEWSIZE"
  fi
done
EOF2
  cat >"$STUBS/truncate" <<'EOF2'
#!/usr/bin/env bash
echo "truncate $*" >>"$CALLS"
exec /usr/bin/truncate "$@"
EOF2
  chmod +x "$STUBS/sfdisk" "$STUBS/truncate"
  stub_cmd e2fsck resize2fs zerofree
  partitions_json() {
    local n
    n=$(part_last "$(cat "$FIXTURE")")
    if [ -s "$NEWSIZE" ]; then
      jq --argjson n "$n" --argjson s "$(cat "$NEWSIZE")" \
        '(.partitiontable.partitions[] | select(.node | endswith("\($n)") and (endswith("1\($n)") | not))).size = $s' "$FIXTURE"
    else
      cat "$FIXTURE"
    fi
  }
  loop_attach() { LOOP_DEV=/dev/loop9; }
  cleanup_pop() { :; }
  _finalize_fs_bytes() { echo $((500 * 1048576)); }
}

@test "finalize_shrink gpt: sfdisk -N, relocate gpt-bak-mini, truncate (+1 MiB), relocate gpt-bak-std" {
  load_step
  stub_shrink "$ROOT/test/fixtures/sfdisk-vm.json"
  local img="$BATS_TEST_TMPDIR/vm.img" end
  truncate -s $((6291456 * 512)) "$img"
  : >"$CALLS"
  finalize_shrink "$img"
  run grep -E '^(sfdisk|truncate)' "$CALLS"
  # start 262144 = 128 MiB; 500 MiB + 16 MiB -> end at 644 MiB
  end=$((644 * 1048576))
  [ "${lines[0]}" = "sfdisk -q --no-reread --no-tell-kernel --wipe never --wipe-partitions never -N 1 $img" ]
  [ "${lines[1]}" = "sfdisk -q --relocate gpt-bak-mini $img" ]
  [ "${lines[2]}" = "truncate -s $((end + 1048576)) $img" ]
  [ "${lines[3]}" = "sfdisk -q --relocate gpt-bak-std $img" ]
  [ "${#lines[@]}" -eq 4 ]
  [ "$(stat -c %s "$img")" -eq $((end + 1048576)) ]
  # the free space of the shrunk file system is zeroed
  grep -qx 'zerofree -v /dev/loop9' "$CALLS"
}

@test "finalize_shrink dos: sfdisk -N and truncate to the partition end, no relocate" {
  load_step
  stub_shrink "$ROOT/test/fixtures/sfdisk-rpi.json"
  local img="$BATS_TEST_TMPDIR/pi.img" end
  truncate -s $(((1064960 + 4947968) * 512)) "$img"
  : >"$CALLS"
  finalize_shrink "$img"
  run grep -E '^(sfdisk|truncate)' "$CALLS"
  # start 1064960 = 520 MiB; 500 MiB + 16 MiB -> end at 1036 MiB
  end=$((1036 * 1048576))
  [ "${lines[0]}" = "sfdisk -q --no-reread --no-tell-kernel --wipe never --wipe-partitions never -N 2 $img" ]
  [ "${lines[1]}" = "truncate -s $end $img" ]
  [ "${#lines[@]}" -eq 2 ]
}

# --- real shrink (builder container only) ------------------------------------------------

# make_test_image <img> <dos|gpt> : 100 MiB image like the upstream layouts, root ext4 with
# a kept file and a deleted "secret" (its data stays in free blocks until zerofree).
make_test_image() {
  local img=$1 label=$2 j n src="$BATS_TEST_TMPDIR/src"
  rm -f "$img"
  truncate -s 100M "$img"
  if [ "$label" = dos ]; then
    sfdisk -q "$img" <<'EOF'
label: dos
unit: sectors
start=8192, size=16384, type=c
start=24576, type=83
EOF
    n=2
  else
    sfdisk -q "$img" <<'EOF'
label: gpt
unit: sectors
p14 : start=2048, size=6144, type="BIOS boot"
p15 : start=8192, size=16384, type="EFI System"
p1 : start=24576, type="Linux root (x86-64)"
EOF
    n=1
  fi
  mkdir -p "$src"
  head -c 3145728 /dev/urandom >"$src/keep.bin"
  yes 'EMUSTATION-TEST-SECRET-0123456789' | head -c 1048576 >"$src/secret.txt"
  j=$(partitions_json "$img")
  mkfs.ext4 -q -F -d "$src" -E offset="$(part_offset "$j" "$n")" "$img" $(($(part_size "$j" "$n") / 1024))k
  loop_attach "$img" "$(part_offset "$j" "$n")" "$(part_size "$j" "$n")"
  debugfs -w -R 'rm /secret.txt' "$LOOP_DEV" >/dev/null 2>&1
  cleanup_pop
  ROOT_PART=$n
  KEEP_SUM=$(sha256sum <"$src/keep.bin" | cut -d' ' -f1)
}

check_shrunk() {
  local img=$1 before=$2 j size end last
  size=$(stat -c %s "$img")
  [ "$size" -lt "$before" ]
  j=$(partitions_json "$img")
  last=$(part_last "$j")
  [ "$last" = "$ROOT_PART" ]
  end=$(($(part_offset "$j" "$last") + $(part_size "$j" "$last")))
  [ "$end" -le "$size" ]
  # no free space behind the partition except the backup GPT area
  [ $((size - end)) -le 1048576 ]
  sfdisk -V "$img"
  if grep -aq 'EMUSTATION-TEST-SECRET' "$img"; then
    echo "the deleted secret is still in the image" >&2
    return 1
  fi
  loop_attach "$img" "$(part_offset "$j" "$last")" "$(part_size "$j" "$last")"
  e2fsck -fn "$LOOP_DEV"
  [ "$(debugfs -R 'cat /keep.bin' "$LOOP_DEV" 2>/dev/null | sha256sum | cut -d' ' -f1)" = "$KEEP_SUM" ]
  cleanup_pop
  [ -z "$(losetup -j "$img")" ]
}

@test "finalize_shrink (dos like Pi OS): smaller, table matches the file, fs clean, secret zeroed" {
  need_real_tools
  load_step
  local img="$BATS_TEST_TMPDIR/pi.img"
  make_test_image "$img" dos
  finalize_shrink "$img"
  check_shrunk "$img" $((100 * 1048576))
  [ "$(jq -r .partitiontable.label <<<"$(partitions_json "$img")")" = dos ]
}

@test "finalize_shrink (gpt like Debian): smaller, backup GPT at the new end, fs clean, secret zeroed" {
  need_real_tools
  load_step
  local img="$BATS_TEST_TMPDIR/vm.img" out
  make_test_image "$img" gpt
  finalize_shrink "$img"
  check_shrunk "$img" $((100 * 1048576))
  out=$(sfdisk -l "$img" 2>&1)
  [[ "$out" != *"backup GPT"* ]]
  [[ "$out" != *"not on the end"* ]]
  [ "$(part_offset "$(partitions_json "$img")" 15)" -eq $((8192 * 512)) ]
}

@test "run.sh test: shellcheck list has *.sh and bash scripts without extension, no bats files" {
  load_run
  cd "$ROOT"
  run shell_scripts
  [ "$status" -eq 0 ]
  [[ "$output" == *"./build.sh"* ]]
  [[ "$output" == *"./overlay/usr/local/lib/emulationstation/priv"* ]]
  [[ "$output" == *"./overlay/usr/local/lib/emulationstation/es-session"* ]]
  [[ "$output" == *"./overlay/usr/local/lib/emulationstation/firstboot"* ]]
  [[ "$output" != *".bats"* ]]
  [[ "$output" != *"sudoers"* ]]
}
