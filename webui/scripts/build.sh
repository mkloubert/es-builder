#!/usr/bin/env sh
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

# Usage: scripts/build.sh <GOARCH> <out>
# Builds the Tailwind stylesheet and a static linux binary.
# The Tailwind CLI is taken from $TAILWINDCSS (default: tailwindcss on PATH).
set -eu
if [ $# -ne 2 ]; then
	echo "usage: $0 <GOARCH> <out>" >&2
	exit 2
fi
cd "$(dirname "$0")/.."
${TAILWINDCSS:-tailwindcss} -i assets/input.css -o internal/web/static/app.css --minify
CGO_ENABLED=0 GOOS=linux GOARCH="$1" go build -buildvcs=false -trimpath -ldflags "-s -w" -o "$2" ./cmd/emulationstation-web
