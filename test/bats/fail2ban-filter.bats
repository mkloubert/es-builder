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

# fail2ban filters (Task 6) with fail2ban-regex. Needs fail2ban (builder container).
# shellcheck disable=SC2034

bats_require_minimum_version 1.5.0

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  FILTER="$ROOT/overlay/etc/fail2ban/filter.d/emulationstation-web.conf"
}

need_f2b() {
  command -v fail2ban-regex >/dev/null || skip "fail2ban-regex missing (runs in the builder container)"
}

# matched_lines <line> <filter> : number of matched lines from the fail2ban-regex summary
matched_lines() {
  fail2ban-regex "$1" "$2" 2>&1 | sed -n 's/^Lines: .* \([0-9][0-9]*\) matched,.*/\1/p'
}

@test "emulationstation-web filter: IPv4 line matches once, IP is 10.0.0.9" {
  need_f2b
  line="Oct 27 12:00:00 host emulationstation-web[1]: auth failure from 10.0.0.9"
  [ "$(matched_lines "$line" "$FILTER")" = 1 ]
  run fail2ban-regex -o ip "$line" "$FILTER"
  [ "$status" -eq 0 ]
  [[ "$output" == *10.0.0.9* ]]
}

@test "emulationstation-web filter: IPv6 line matches once, IP is 2001:db8::1" {
  need_f2b
  line="Oct 27 12:00:00 host emulationstation-web[1]: auth failure from 2001:db8::1"
  [ "$(matched_lines "$line" "$FILTER")" = 1 ]
  run fail2ban-regex -o ip "$line" "$FILTER"
  [ "$status" -eq 0 ]
  [[ "$output" == *2001:db8::1* ]]
}

@test "emulationstation-web filter: other lines do not match" {
  need_f2b
  line="Oct 27 12:00:00 host emulationstation-web[1]: login ok for 10.0.0.9"
  [ "$(matched_lines "$line" "$FILTER")" = 0 ]
}

@test "sshd filter (Debian): a failed login logged by sshd-session (OpenSSH 9.8+, later Debian) matches" {
  need_f2b
  [ -f /etc/fail2ban/filter.d/sshd.conf ] || skip "sshd filter is not installed"
  line="Oct 27 12:00:00 host sshd-session[4242]: Failed password for invalid user bob from 10.0.0.9 port 51234 ssh2"
  [ "$(matched_lines "$line" sshd)" = 1 ]
}

@test "sshd filter (Debian): a failed login logged by sshd (OpenSSH 9.2, bookworm) matches" {
  need_f2b
  [ -f /etc/fail2ban/filter.d/sshd.conf ] || skip "sshd filter is not installed"
  line="Oct 27 12:00:00 host sshd[4242]: Failed password for invalid user bob from 10.0.0.9 port 51234 ssh2"
  [ "$(matched_lines "$line" sshd)" = 1 ]
}

@test "nftables.conf: syntax check with nft -c" {
  command -v nft >/dev/null || skip "nft missing (runs in the builder container)"
  run nft -c -f "$ROOT/overlay/etc/nftables.conf"
  echo "$output"
  [ "$status" -eq 0 ]
}
