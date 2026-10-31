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

# icons.sh <lucide-static-dir> <icons.txt> <out.svg>
#
# Builds the icon sprite of the web UI from the Lucide icon files.
# <lucide-static-dir> is the unpacked npm package "lucide-static" (it has an
# icons/ folder). <icons.txt> has one icon name per line ("#" starts a comment).
# The inner path data of each icon stays unchanged. This is a helper for the
# owner. It does not run during the image build.
#
# Example:
#   npm pack lucide-static@1.66.0 && mkdir lucide && tar xzf lucide-static-1.66.0.tgz -C lucide
#   webui/scripts/icons.sh lucide/package webui/assets/icons.txt \
#     webui/internal/web/static/icons.svg
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: icons.sh <lucide-static-dir> <icons.txt> <out.svg>" >&2
  exit 2
fi

dir=$1
list=$2
out=$3

[ -d "$dir/icons" ] || { echo "icons.sh: no icons folder in $dir" >&2; exit 1; }
[ -f "$list" ] || { echo "icons.sh: names file not found: $list" >&2; exit 1; }

tmp=$(mktemp "$out.XXXXXX")
trap 'rm -f "$tmp"' EXIT

version=$(sed -n 's/.*@license lucide-static v\([0-9][0-9.]*\).*/\1/p;T;q' "$dir/icons/menu.svg" 2>/dev/null || true)

{
  printf '<!-- Lucide icons%s, ISC license. Built by webui/scripts/icons.sh -->\n' \
    "${version:+ (lucide-static $version)}"
  printf '<svg xmlns="http://www.w3.org/2000/svg">\n'
  while IFS= read -r name || [ -n "$name" ]; do
    name=${name%%#*}
    name=${name//[[:space:]]/}
    [ -n "$name" ] || continue
    if ! [[ $name =~ ^[a-z0-9-]+$ ]]; then
      echo "icons.sh: bad icon name: $name" >&2
      exit 1
    fi
    file="$dir/icons/$name.svg"
    if [ ! -f "$file" ]; then
      echo "icons.sh: icon file missing: $file" >&2
      exit 1
    fi
    printf '<symbol id="%s" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">\n' "$name"
    # Keep the inner elements: everything between the <svg ...> and </svg> tags.
    awk '
      /^<svg/ { inside = 1; next }
      inside && /^>/ { started = 1; next }
      /^<\/svg>/ { exit }
      started { print }
    ' "$file"
    printf '</symbol>\n'
  done <"$list"
  printf '</svg>\n'
} >"$tmp"

chmod 0644 "$tmp"
mv -f "$tmp" "$out"
trap - EXIT
