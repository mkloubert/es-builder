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

# Lists own source files without the AGPL-3.0 header (one per line) and
# exits with 1 if there are any.
#
# Excluded: .git, .superpowers, out, secrets, vendor/, testdata/, fixtures/
# (content is checked by tests), controllers/custom/ (controller exports of the user),
# webui-custom/ (web UI files of the user),
# Markdown, JSON, go.mod/go.sum, LICENSE, the generated app.css and empty files.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT"

# wants_header <file> : 0 if the file needs a header according to the rules
wants_header() {
  local f=$1 base first
  base=${f##*/}
  [ -s "$f" ] || return 1
  case "$base" in
    LICENSE | go.mod | go.sum | app.css) return 1 ;;
    Dockerfile | .gitignore | .shellcheckrc) return 0 ;;
    *.go | *.sh | *.bats | *.html | *.css | *.js | *.xml | *.conf | *.list | *.example | \
      *.service | *.timer | *.target | *.path | *.mount | *.socket) return 0 ;;
    *.md | *.json) return 1 ;;
  esac
  case "$f" in
    ./overlay/*) return 0 ;;
  esac
  IFS= read -r first <"$f" || true
  case "$first" in
    "#!"*) return 0 ;;
  esac
  return 1
}

# has_header <file> : 0 if the license text is in the first 40 lines
has_header() {
  head -n 40 "$1" | grep -q 'GNU Affero General Public License as published by' &&
    head -n 40 "$1" | grep -q 'Copyright (C) 2026  Marcel Joachim Kloubert'
}

missing=0
while IFS= read -r f; do
  wants_header "$f" || continue
  if ! has_header "$f"; then
    printf '%s\n' "${f#./}"
    missing=1
  fi
done < <(find . \( -path ./.git -o -path ./.superpowers -o -path ./out -o -path ./secrets -o -path ./controllers/custom -o -path ./webui-custom \
  -o -name vendor -o -name testdata -o -name fixtures -o -name node_modules \) -prune -o -type f -print | LC_ALL=C sort)

exit "$missing"
