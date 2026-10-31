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

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
}

@test "all own source files carry the AGPL header" {
  run "$ROOT/scripts/check-license-headers.sh"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
}

@test "check-license-headers reports a file without a header (exit 1)" {
  # Copy of the script in a throw-away repo, so the real files stay untouched
  mkdir -p "$BATS_TEST_TMPDIR/r/scripts"
  cp "$ROOT/scripts/check-license-headers.sh" "$BATS_TEST_TMPDIR/r/scripts/"
  printf '#!/usr/bin/env bash\necho hi\n' >"$BATS_TEST_TMPDIR/r/x.sh"
  run "$BATS_TEST_TMPDIR/r/scripts/check-license-headers.sh"
  [ "$status" -eq 1 ]
  [[ "$output" == *"x.sh"* ]]
}

@test "check-license-headers skips webui-custom/ (the owner's own web UI files)" {
  mkdir -p "$BATS_TEST_TMPDIR/r/scripts" "$BATS_TEST_TMPDIR/r/webui-custom/static"
  cp "$ROOT/scripts/check-license-headers.sh" "$BATS_TEST_TMPDIR/r/scripts/"
  echo 'alert(1)' >"$BATS_TEST_TMPDIR/r/webui-custom/static/my.js"
  run "$BATS_TEST_TMPDIR/r/scripts/check-license-headers.sh"
  [ "$status" -eq 0 ]
}
