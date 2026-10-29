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

# Cache of the base layer (builder container only, Bash 5).
# Expects builder/lib/log.sh and builder/lib/image.sh (target_kind, target_image_checksum).
# CACHE_DIR (default /cache) and REPO_ROOT (default: this repo) can be overridden for tests.
set -euo pipefail

: "${CACHE_DIR:=/cache}"
: "${REPO_ROOT:=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"

# cache_key <target> : 16 hex characters (sha256) over name + content of
#   provision/common/base-*.sh, provision/<kind>/base-*.sh, provision/<kind>/modules.list,
#   builder/steps/30-provision-base.sh, plus image checksum, RETROPIE_SETUP_REF,
#   XPADNEO_REF and target name. Missing files are allowed. app-*-files are not included.
cache_key() {
  local target=$1 kind f rel files=() sorted ng
  kind=$(target_kind "$target")
  ng=$(shopt -p nullglob || true)
  shopt -s nullglob
  files=(
    "$REPO_ROOT"/provision/common/base-*.sh
    "$REPO_ROOT/provision/$kind"/base-*.sh
    "$REPO_ROOT/provision/$kind"/modules.list
    "$REPO_ROOT"/builder/steps/30-provision-base.sh
  )
  eval "$ng"
  sorted=$(for f in "${files[@]}"; do if [ -f "$f" ]; then printf '%s\n' "${f#"$REPO_ROOT"/}"; fi; done | LC_ALL=C sort)
  {
    while IFS= read -r rel; do
      [ -n "$rel" ] || continue
      printf 'file %s %s\n' "$rel" "$(sha256sum <"$REPO_ROOT/$rel" | cut -d' ' -f1)"
    done <<<"$sorted"
    printf 'target %s\n' "$target"
    printf 'image %s\n' "$(target_image_checksum "$target")"
    printf 'retropie %s\n' "${CFG_RETROPIE_SETUP_REF:-}"
    printf 'xpadneo %s\n' "${CFG_XPADNEO_REF:-}"
  } | sha256sum | cut -c1-16
}

# cache_base_path <target> : $CACHE_DIR/base-<target>-<key>.img (built as <path>.tmp)
cache_base_path() {
  local key
  key=$(cache_key "$1")
  printf '%s/base-%s-%s.img\n' "$CACHE_DIR" "$1" "$key"
}

# cache_dl_path <target> : $CACHE_DIR/dl/<file name of the URL without .xz>
cache_dl_path() {
  local url name
  url=$(target_image_url "$1")
  [ -n "$url" ] || die "No image URL for target $1 in build.conf"
  name=${url##*/}
  printf '%s/dl/%s\n' "$CACHE_DIR" "${name%.xz}"
}

# cache_work_path <target> : working copy of the app layer
cache_work_path() {
  printf '%s/work/%s.img\n' "$CACHE_DIR" "$1"
}

# cache_have_base <target> : base exists and no --rebuild-base
cache_have_base() {
  [ "${REBUILD_BASE:-0}" != 1 ] && [ -f "$(cache_base_path "$1")" ]
}

# cache_clean_stale : delete leftovers of aborted base builds ($CACHE_DIR/base-*.img.tmp)
cache_clean_stale() {
  local f
  for f in "$CACHE_DIR"/base-*.img.tmp; do
    [ -e "$f" ] || continue
    log_warn "Removing leftover of an aborted base build: $f"
    rm -f "$f"
  done
}

# cache_commit <tmp> <final> : only after success; sync, then atomic mv
cache_commit() {
  local tmp=$1 final=$2
  [ -f "$tmp" ] || die "cache_commit: $tmp is missing"
  sync "$tmp"
  mv -f "$tmp" "$final"
  sync "$(dirname "$final")"
  log_info "Base layer saved: $final"
}

# cache_prune_old <target> : after a new base was saved, delete the older bases of the SAME
# target ($CACHE_DIR/base-<target>-<other key>.img) and their module reports. One base is many
# GB; without this every change of a base script would leave one more in the volume.
# Bases of other targets are never touched.
cache_prune_old() {
  local target=$1 key f
  key=$(cache_key "$target")
  for f in "$CACHE_DIR/base-$target-"????????????????.img "$CACHE_DIR/report-$target-"????????????????.txt; do
    [ -e "$f" ] || continue
    case "$f" in
      "$CACHE_DIR/base-$target-$key.img" | "$CACHE_DIR/report-$target-$key.txt") continue ;;
    esac
    log_info "Removing old base layer file: $f"
    rm -f "$f"
  done
}
