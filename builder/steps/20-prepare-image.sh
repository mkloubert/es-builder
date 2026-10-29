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

# Step 20: prepare the base image. Delete leftovers of aborted builds; if the
# base layer is in the cache, do nothing. Otherwise copy the upstream image to <base>.tmp and
# grow it (rpi4 +8 GiB, vm +4 GiB). Provisioning + cache_commit: 30-provision-base.sh.
# Usage: bash builder/steps/20-prepare-image.sh <rpi4|vm-amd64|vm-arm64>; configuration from $BUILD_CONFIG.
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

TARGET=${1:-}
[ -n "$TARGET" ] || die "Usage: $0 <target>" 2
[ -n "${BUILD_CONFIG:-}" ] || die "BUILD_CONFIG is not set" 2
cleanup_install
config_load "$BUILD_CONFIG"

cache_clean_stale
if cache_have_base "$TARGET"; then
  log_info "Base layer from cache: $(cache_base_path "$TARGET")"
  exit 0
fi

case "$(target_kind "$TARGET")" in
  rpi4) grow=8 ;;
  vm) grow=4 ;;
esac
src=$(cache_dl_path "$TARGET")
[ -f "$src" ] || die "Upstream image is missing: $src (step 10-fetch.sh)"
base=$(cache_base_path "$TARGET")
tmp="$base.tmp"

log_info "Copying $src -> $tmp"
cp --sparse=always "$src" "$tmp"
image_grow "$tmp" "$grow"
log_info "Base image prepared: $tmp"
