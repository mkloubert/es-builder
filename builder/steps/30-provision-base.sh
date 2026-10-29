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

# Step 30: provision the base layer (cached). If the base is in the cache and no
# --rebuild-base is set, do nothing. Otherwise mount the <base>.tmp prepared by
# 20-prepare-image.sh, run provision/common/base-*.sh in the chroot and then
# provision/<kind>/base-*.sh (each sorted), tear down and save as <base>.
# Module report: $CACHE_DIR/report-<target>-<key>.txt (empty = built, no errors; one line
# "<module> <exit> <hint>" per failed module). If the file is missing, nothing was built.
# Usage: bash builder/steps/30-provision-base.sh <rpi4|vm-amd64|vm-arm64>; configuration from $BUILD_CONFIG.
#
# Only values that are part of the cache key (pins, target) go into the chroot. User values
# (password, SSH key, locale, time zone, keyboard) are set by the app layer (app-05-identity.sh).
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# shellcheck source=builder/lib/log.sh
. "$ROOT/builder/lib/log.sh"
# shellcheck source=builder/lib/config.sh
. "$ROOT/builder/lib/config.sh"
# shellcheck source=builder/lib/image.sh
. "$ROOT/builder/lib/image.sh"
# shellcheck source=builder/lib/chroot.sh
. "$ROOT/builder/lib/chroot.sh"
# shellcheck source=builder/lib/cache.sh
. "$ROOT/builder/lib/cache.sh"

TARGET=${1:-}
[ -n "$TARGET" ] || die "Usage: $0 <target>" 2
[ -n "${BUILD_CONFIG:-}" ] || die "BUILD_CONFIG is not set" 2
cleanup_install
config_load "$BUILD_CONFIG"

base=$(cache_base_path "$TARGET")
if [ "${REBUILD_BASE:-0}" != 1 ] && [ -f "$base" ]; then
  log_info "Base layer from cache: $base"
  exit 0
fi
tmp="$base.tmp"
[ -f "$tmp" ] || die "Prepared image is missing: $tmp (step 20-prepare-image.sh)"

kind=$(target_kind "$TARGET")
arch=$(target_arch "$TARGET")
report="$CACHE_DIR/report-$TARGET-$(cache_key "$TARGET").txt"

# Target-specific values for the shared scripts. RetroPie platform: empty = automatic.
# rp_cpu_flags / rp_kms: only for the VM (see below), empty = RetroPie default.
rp_cpu_flags=""
rp_kms=""
case "$TARGET" in
  rpi4) rp_platform=rpi4 ;;
  # RetroPie-Setup f5ebfba6 (scriptmodules/system.sh, get_platform): there is no platform named for
  # a generic PC or VM. Without a Pi/SoC match the name is "uname -m" and platform_native is used
  # (flags: gl vulkan, kms or x11, "x86" if x86). For amd64 we use "x86", the flag the modules
  # check; for arm64 "aarch64", which is the auto-detected name (isPlatform aarch64 is true).
  # platform_native sets -march=native, which would build for the CPU of the BUILD host (or for
  # the qemu CPU), so a generic CPU baseline is preset ("__cpu_flags" is respected, system.sh).
  # "__has_kms=1" makes platform_native choose kms (no X11): the VM shows EmulationStation on
  # the virtio-gpu console.
  vm-amd64)
    rp_platform=x86
    rp_cpu_flags=-march=x86-64
    rp_kms=1
    ;;
  vm-arm64)
    rp_platform=aarch64
    rp_cpu_flags=-march=armv8-a
    rp_kms=1
    ;;
  *) die "Unknown target: $TARGET" 2 ;;
esac

: >"$report"

mount_target "$tmp" "$TARGET"
chroot_prepare "$TARGET_MNT"
prov_rel=root/provision
prov="$TARGET_MNT/$prov_rel"
cleanup_push rm -rf "$prov"
rm -rf "$prov"
mkdir -p "$prov/common" "$prov/$kind"
chmod 0700 "$prov"

shopt -s nullglob
scripts=()
for f in "$ROOT"/provision/common/base-*.sh; do
  cp "$f" "$prov/common/"
  scripts+=("common/${f##*/}")
done
for f in "$ROOT/provision/$kind"/base-*.sh; do
  cp "$f" "$prov/$kind/"
  scripts+=("$kind/${f##*/}")
done
shopt -u nullglob
if [ -f "$ROOT/provision/$kind/modules.list" ]; then
  cp "$ROOT/provision/$kind/modules.list" "$prov/$kind/"
fi
: >"$prov/report.txt"

env_args=(
  "CFG_TARGET=$TARGET"
  "CFG_TARGET_KIND=$kind"
  "CFG_TARGET_ARCH=$arch"
  "CFG_RETROPIE_SETUP_REF=$CFG_RETROPIE_SETUP_REF"
  "CFG_RETROPIE_PLATFORM=$rp_platform"
  "CFG_XPADNEO_REF=$CFG_XPADNEO_REF"
)
[ -z "$rp_cpu_flags" ] || env_args+=("CFG_RETROPIE_CPU_FLAGS=$rp_cpu_flags")
[ -z "$rp_kms" ] || env_args+=("CFG_RETROPIE_KMS=$rp_kms")
env_args+=(
  "PROVISION_DIR=/$prov_rel"
  "PROVISION_REPORT=/$prov_rel/report.txt"
)

for s in "${scripts[@]}"; do
  log_info "Provisioning: $s"
  rc=0
  chroot_run env "${env_args[@]}" bash "/$prov_rel/$s" || rc=$?
  # Save report lines right away, even if the script failed
  cat "$prov/report.txt" >>"$report"
  : >"$prov/report.txt"
  [ "$rc" -eq 0 ] || die "Provisioning failed: $s (exit $rc)"
done

# Tear down explicitly (provision directory, chroot, umount, loop), only then save
# shellcheck disable=SC2119  # without exit code: returns instead of exiting
cleanup_run || die "Cleanup failed; the base layer is not saved"
cache_commit "$tmp" "$base"
cache_prune_old "$TARGET"

if [ -s "$report" ]; then
  log_warn "Failed modules (report $report):"
  while IFS= read -r line; do log_warn "  $line"; done <"$report"
else
  log_info "All modules installed (report $report is empty)."
fi
