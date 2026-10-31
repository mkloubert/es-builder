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

# shellcheck disable=SC2034

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/log.sh"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/config.sh"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/image.sh"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/cache.sh"

  export CACHE_DIR="$BATS_TEST_TMPDIR/cache"
  mkdir -p "$CACHE_DIR"

  # Minimal repo for cache_key
  REPO_ROOT="$BATS_TEST_TMPDIR/repo"
  mkdir -p "$REPO_ROOT/provision/common" "$REPO_ROOT/provision/rpi4" "$REPO_ROOT/provision/vm" "$REPO_ROOT/builder/steps"
  echo "echo base10" >"$REPO_ROOT/provision/common/base-10-packages.sh"
  echo "echo app10" >"$REPO_ROOT/provision/common/app-10-security.sh"
  echo "echo rpi" >"$REPO_ROOT/provision/rpi4/base-10-system.sh"
  echo "echo vm" >"$REPO_ROOT/provision/vm/base-10-system.sh"
  echo "lr-fceumm" >"$REPO_ROOT/provision/rpi4/modules.list"
  echo "echo step30" >"$REPO_ROOT/builder/steps/30-provision-base.sh"

  CFG_RPI_IMAGE_CHECKSUM="sha256:$(printf 'a%.0s' {1..64})"
  CFG_VM_IMAGE_CHECKSUM_AMD64="sha512:$(printf 'b%.0s' {1..128})"
  CFG_VM_IMAGE_CHECKSUM_ARM64="sha512:$(printf 'c%.0s' {1..128})"
  CFG_RETROPIE_SETUP_REF=f5ebfba638179631d4619f2b52075dec33d4bca8
  CFG_XPADNEO_REF=v0.10.5
}

@test "cache_key: 16 hex characters, stable" {
  local a b
  a=$(cache_key rpi4)
  b=$(cache_key rpi4)
  [[ "$a" =~ ^[0-9a-f]{16}$ ]]
  [ "$a" = "$b" ]
}

@test "cache_key changes when a base file is changed" {
  local a
  a=$(cache_key rpi4)
  echo "echo changed" >>"$REPO_ROOT/provision/common/base-10-packages.sh"
  [ "$(cache_key rpi4)" != "$a" ]
  a=$(cache_key rpi4)
  echo "echo changed" >>"$REPO_ROOT/provision/rpi4/base-10-system.sh"
  [ "$(cache_key rpi4)" != "$a" ]
  a=$(cache_key rpi4)
  echo "lr-snes9x" >>"$REPO_ROOT/provision/rpi4/modules.list"
  [ "$(cache_key rpi4)" != "$a" ]
  a=$(cache_key rpi4)
  echo "echo changed" >>"$REPO_ROOT/builder/steps/30-provision-base.sh"
  [ "$(cache_key rpi4)" != "$a" ]
}

@test "cache_key: a new or renamed base file changes the key" {
  local a
  a=$(cache_key rpi4)
  echo "echo new" >"$REPO_ROOT/provision/common/base-50-new.sh"
  [ "$(cache_key rpi4)" != "$a" ]
  a=$(cache_key rpi4)
  mv "$REPO_ROOT/provision/common/base-50-new.sh" "$REPO_ROOT/provision/common/base-60-new.sh"
  [ "$(cache_key rpi4)" != "$a" ]
}

@test "cache_key does NOT change for app-* files or files of the other target" {
  local a
  a=$(cache_key rpi4)
  echo "echo changed" >>"$REPO_ROOT/provision/common/app-10-security.sh"
  echo "echo new" >"$REPO_ROOT/provision/rpi4/app-10-x.sh"
  echo "echo changed" >>"$REPO_ROOT/provision/vm/base-10-system.sh"
  [ "$(cache_key rpi4)" = "$a" ]
}

@test "cache_key: pins and target are included" {
  local a
  a=$(cache_key rpi4)
  CFG_RETROPIE_SETUP_REF=other
  [ "$(cache_key rpi4)" != "$a" ]
  a=$(cache_key rpi4)
  CFG_XPADNEO_REF=v9
  [ "$(cache_key rpi4)" != "$a" ]
  a=$(cache_key rpi4)
  CFG_RPI_IMAGE_CHECKSUM="sha256:$(printf 'd%.0s' {1..64})"
  [ "$(cache_key rpi4)" != "$a" ]
  [ "$(cache_key vm-amd64)" != "$(cache_key vm-arm64)" ]
}

@test "cache_key: missing provision directories are ok" {
  rm -rf "$REPO_ROOT/provision"
  run cache_key vm-amd64
  [ "$status" -eq 0 ]
  [[ "$output" =~ ^[0-9a-f]{16}$ ]]
}

@test "Paths: base, dl, work" {
  [ "$(cache_base_path vm-amd64)" = "$CACHE_DIR/base-vm-amd64-$(cache_key vm-amd64).img" ]
  [ "$(cache_work_path rpi4)" = "$CACHE_DIR/work/rpi4.img" ]
  CFG_RPI_IMAGE_URL=https://example.org/a/2026-10-06-raspios-bookworm-arm64-lite.img.xz
  [ "$(cache_dl_path rpi4)" = "$CACHE_DIR/dl/2026-10-06-raspios-bookworm-arm64-lite.img" ]
  CFG_VM_IMAGE_URL_ARM64=https://example.org/b/debian-12-nocloud-arm64.raw
  [ "$(cache_dl_path vm-arm64)" = "$CACHE_DIR/dl/debian-12-nocloud-arm64.raw" ]
}

# Simulates 30-provision-base.sh: creates .tmp and then aborts.
@test "aborted base build leaves no .img; .tmp is deleted on the next run" {
  local base
  base=$(cache_base_path rpi4)
  run bash -c "
    set -euo pipefail
    . '$ROOT/builder/lib/log.sh'; . '$ROOT/builder/lib/image.sh'; . '$ROOT/builder/lib/cache.sh'
    provision_fails() { : >'$base.tmp'; exit 1; }
    provision_fails
    cache_commit '$base.tmp' '$base'
  "
  [ "$status" -eq 1 ]
  [ ! -e "$base" ]
  [ -e "$base.tmp" ]
  run cache_have_base rpi4
  [ "$status" -ne 0 ]

  cache_clean_stale
  [ ! -e "$base.tmp" ]
  [ ! -e "$base" ]
}

@test "cache_commit: .tmp becomes .img; cache_have_base respects REBUILD_BASE" {
  local base
  base=$(cache_base_path rpi4)
  echo data >"$base.tmp"
  cache_commit "$base.tmp" "$base"
  [ ! -e "$base.tmp" ]
  [ "$(cat "$base")" = data ]
  REBUILD_BASE=0 cache_have_base rpi4
  REBUILD_BASE=1 run cache_have_base rpi4
  [ "$status" -ne 0 ]
}

# --- Steps ----------------------------------------------------------------

write_conf() {
  local src=$1
  cat >"$BATS_TEST_TMPDIR/build.conf" <<EOF
USER_PASSWORD=x
RPI_IMAGE_URL=file://$src
RPI_IMAGE_CHECKSUM=sha256:$(sha256sum "$src" | cut -d' ' -f1)
RETROPIE_SETUP_REF=r1
XPADNEO_REF=x1
EOF
  export BUILD_CONFIG="$BATS_TEST_TMPDIR/build.conf"
}

@test "10-fetch.sh downloads the upstream image to CACHE_DIR/dl and unpacks .xz" {
  local src="$BATS_TEST_TMPDIR/pi.img"
  echo pi-image >"$src"
  xz "$src"
  write_conf "$src.xz"
  run bash "$ROOT/builder/steps/10-fetch.sh" rpi4
  [ "$status" -eq 0 ]
  [ "$(cat "$CACHE_DIR/dl/pi.img")" = pi-image ]
  [ -f "$CACHE_DIR/dl/pi.img.verified" ]
}

@test "20-prepare-image.sh: existing base -> 'Base layer from cache', old .tmp removed" {
  local src="$BATS_TEST_TMPDIR/pi.img" base
  echo pi-image >"$src"
  write_conf "$src"
  config_load "$BUILD_CONFIG"
  base=$(REPO_ROOT="$ROOT" cache_base_path rpi4)
  : >"$base"
  : >"$CACHE_DIR/base-vm-amd64-0000000000000000.img.tmp"
  run bash "$ROOT/builder/steps/20-prepare-image.sh" rpi4
  [ "$status" -eq 0 ]
  [[ "$output" == *"Base layer from cache"* ]]
  [ ! -e "$CACHE_DIR/base-vm-amd64-0000000000000000.img.tmp" ]
  [ ! -e "$base.tmp" ]
}

@test "20-prepare-image.sh without cache: copy as .tmp, grown by 8 GiB" {
  command -v sfdisk >/dev/null || skip "sfdisk missing (runs in the builder container)"
  [ "$(id -u)" -eq 0 ] || skip "root missing (runs in the builder container)"
  local src="$BATS_TEST_TMPDIR/pi.img" base j
  truncate -s 64M "$src"
  sfdisk -q "$src" <<'EOF'
label: dos
unit: sectors
start=8192, size=16384, type=c
start=24576, type=83
EOF
  j=$(partitions_json "$src")
  mkfs.ext4 -q -F -E offset="$(part_offset "$j" 2)" "$src" $(($(part_size "$j" 2) / 1024))k
  write_conf "$src"
  mkdir -p "$CACHE_DIR/dl"
  cp "$src" "$CACHE_DIR/dl/pi.img"
  config_load "$BUILD_CONFIG"
  base=$(REPO_ROOT="$ROOT" cache_base_path rpi4)
  run bash "$ROOT/builder/steps/20-prepare-image.sh" rpi4
  [ "$status" -eq 0 ]
  [ ! -e "$base" ]
  [ "$(stat -c %s "$base.tmp")" -eq $((64 * 1024 * 1024 + 8 * 1024 * 1024 * 1024)) ]
  [ -z "$(losetup -a | grep -F "$base.tmp" || true)" ]
}

@test "cache_prune_old: removes older bases and reports of the same target only" {
  local base key
  base=$(cache_base_path vm-amd64)
  key=$(cache_key vm-amd64)
  echo new >"$base"
  : >"$CACHE_DIR/report-vm-amd64-$key.txt"
  echo old >"$CACHE_DIR/base-vm-amd64-0123456789abcdef.img"
  : >"$CACHE_DIR/report-vm-amd64-0123456789abcdef.txt"
  echo other >"$CACHE_DIR/base-vm-arm64-0123456789abcdef.img"
  : >"$CACHE_DIR/report-vm-arm64-0123456789abcdef.txt"
  echo pi >"$CACHE_DIR/base-rpi4-fedcba9876543210.img"
  echo building >"$CACHE_DIR/base-vm-amd64-1111111111111111.img.tmp"
  cache_prune_old vm-amd64
  [ "$(cat "$base")" = new ]
  [ -e "$CACHE_DIR/report-vm-amd64-$key.txt" ]
  [ ! -e "$CACHE_DIR/base-vm-amd64-0123456789abcdef.img" ]
  [ ! -e "$CACHE_DIR/report-vm-amd64-0123456789abcdef.txt" ]
  [ -e "$CACHE_DIR/base-vm-arm64-0123456789abcdef.img" ]
  [ -e "$CACHE_DIR/report-vm-arm64-0123456789abcdef.txt" ]
  [ -e "$CACHE_DIR/base-rpi4-fedcba9876543210.img" ]
  [ -e "$CACHE_DIR/base-vm-amd64-1111111111111111.img.tmp" ]
}
