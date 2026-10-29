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

# Step 10: download and verify the upstream image -> $CACHE_DIR/dl/<name without .xz>
# Usage: bash builder/steps/10-fetch.sh <rpi4|vm-amd64|vm-arm64>; configuration from $BUILD_CONFIG.
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

url=$(target_image_url "$TARGET")
sum=$(target_image_checksum "$TARGET")
if [ -z "$url" ] || [ -z "$sum" ]; then
  die "Image URL or checksum for $TARGET is missing in $BUILD_CONFIG"
fi
dest=$(cache_dl_path "$TARGET")
fetch_verified "$url" "$sum" "$dest"
