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
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/log.sh"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/tls.sh"
  command -v openssl >/dev/null || skip "openssl missing (runs in the builder container)"
  CA="$BATS_TEST_TMPDIR/ca"
  OUT="$BATS_TEST_TMPDIR/out"
}

text() { openssl x509 -in "$1" -noout -text; }

@test "second tls_ensure_ca call keeps ca.crt and ca.key" {
  tls_ensure_ca "$CA"
  a=$(sha256sum "$CA/ca.crt" "$CA/ca.key")
  tls_ensure_ca "$CA"
  [ "$a" = "$(sha256sum "$CA/ca.crt" "$CA/ca.key")" ]
}

@test "ca.key not touched when a server certificate is issued again" {
  tls_ensure_ca "$CA"
  a=$(sha256sum "$CA/ca.key")
  tls_issue_server "$CA" "$OUT" emulationstation ""
  tls_issue_server "$CA" "$OUT" emulationstation ""
  [ "$a" = "$(sha256sum "$CA/ca.key")" ]
  [ ! -e "$CA/ca.srl" ]
}

@test "only one of ca.key and ca.crt: dies and keeps the file" {
  mkdir -p "$CA"
  echo x >"$CA/ca.crt"
  run tls_ensure_ca "$CA"
  [ "$status" -ne 0 ]
  [[ "$output" == *"Only one of ca.key and ca.crt"* ]]
  [ ! -e "$CA/ca.key" ]
  [ "$(cat "$CA/ca.crt")" = x ]
}

@test "CA is CA:TRUE critical with keyCertSign and has the right name" {
  tls_ensure_ca "$CA"
  t=$(text "$CA/ca.crt")
  [[ "$t" == *"CN = EmuStation Builder Local CA"* ]]
  [[ "$t" == *"X509v3 Basic Constraints: critical"*"CA:TRUE"* ]]
  [[ "$t" == *"Certificate Sign"* ]]
  [[ "$t" == *"CRL Sign"* ]]
  [[ "$t" == *"prime256v1"* ]]
}

@test "SAN list is exact" {
  tls_ensure_ca "$CA"
  tls_issue_server "$CA" "$OUT" emulationstation "box.fritz.box,192.168.178.50"
  san=$(text "$OUT/server.crt" | grep -A1 'Subject Alternative Name' | tail -1 | sed 's/^ *//')
  [ "$san" = "DNS:emulationstation, DNS:emulationstation.local, DNS:box.fritz.box, IP Address:192.168.178.50" ]
}

@test "IPv6 and spaces in the extra list" {
  tls_ensure_ca "$CA"
  tls_issue_server "$CA" "$OUT" emulationstation " fe80::1 , 2001:db8::5 ,"
  t=$(text "$OUT/server.crt")
  [[ "$t" == *"IP Address:FE80:0:0:0:0:0:0:1"* ]]
  [[ "$t" == *"IP Address:2001:DB8:0:0:0:0:0:5"* ]]
}

@test "server certificate verifies against the CA and chain has two certificates" {
  tls_ensure_ca "$CA"
  tls_issue_server "$CA" "$OUT" emulationstation ""
  run openssl verify -CAfile "$CA/ca.crt" "$OUT/server.crt"
  [ "$status" -eq 0 ]
  [[ "$output" == *": OK" ]]
  [ "$(grep -c 'BEGIN CERTIFICATE' "$OUT/server.crt")" -eq 2 ]
  tail -n +"$(($(grep -n 'END CERTIFICATE' "$OUT/server.crt" | head -1 | cut -d: -f1) + 1))" "$OUT/server.crt" >"$BATS_TEST_TMPDIR/second.crt"
  cmp "$BATS_TEST_TMPDIR/second.crt" "$CA/ca.crt"
}

@test "validity is at most 825 days" {
  tls_ensure_ca "$CA"
  tls_issue_server "$CA" "$OUT" emulationstation ""
  nb=$(openssl x509 -in "$OUT/server.crt" -noout -startdate | cut -d= -f2)
  na=$(openssl x509 -in "$OUT/server.crt" -noout -enddate | cut -d= -f2)
  days=$((($(date -d "$na" +%s) - $(date -d "$nb" +%s)) / 86400))
  [ "$days" -le 825 ]
  [ "$days" -ge 824 ]
}

@test "leaf has serverAuth, no CA:TRUE, key identifiers" {
  tls_ensure_ca "$CA"
  tls_issue_server "$CA" "$OUT" emulationstation ""
  t=$(text "$OUT/server.crt")
  [[ "$t" == *"TLS Web Server Authentication"* ]]
  [[ "$t" != *"CA:TRUE"* ]]
  [[ "$t" == *"Subject Key Identifier"* ]]
  [[ "$t" == *"Authority Key Identifier"* ]]
}

@test "key modes are 600" {
  tls_ensure_ca "$CA"
  tls_issue_server "$CA" "$OUT" emulationstation ""
  [ "$(stat -c %a "$CA/ca.key")" = 600 ]
  [ "$(stat -c %a "$OUT/server.key")" = 600 ]
}

@test "invalid host name dies" {
  tls_ensure_ca "$CA"
  run tls_issue_server "$CA" "$OUT" "Bad_Host" ""
  [ "$status" -ne 0 ]
  [[ "$output" == *"Invalid host name"* ]]
  run tls_issue_server "$CA" "$OUT" "-bad" ""
  [ "$status" -ne 0 ]
}

@test "invalid SAN entries die" {
  tls_ensure_ca "$CA"
  for bad in "bad_name" "999.1.1.1" "a..b" "x y" "-a.b" "1.2.3" "fe80:::1"; do
    run tls_issue_server "$CA" "$OUT" emulationstation "ok.example,$bad"
    [ "$status" -ne 0 ]
    [[ "$output" == *"Invalid entry in TLS_EXTRA_SANS"* ]]
  done
}

@test "issuing without a CA dies" {
  run tls_issue_server "$CA" "$OUT" emulationstation ""
  [ "$status" -ne 0 ]
  [[ "$output" == *"No CA found"* ]]
}

@test "malformed IPv6 entries die and leave no files" {
  tls_ensure_ca "$CA"
  for bad in "12345::1" ":1:2:3" "::1.2.3.4.5" "1::2::3" "1:2:3:4:5:6:7:8:9" "1:2:3:4:5:6:7"; do
    rm -rf "$OUT"
    run tls_issue_server "$CA" "$OUT" emulationstation "$bad"
    [ "$status" -ne 0 ]
    [[ "$output" == *"Invalid entry in TLS_EXTRA_SANS"* ]]
    [ ! -e "$OUT/server.key" ]
    [ ! -e "$OUT/server.crt" ]
  done
}

@test "IPv4-mapped IPv6 and full IPv6 are accepted" {
  tls_ensure_ca "$CA"
  tls_issue_server "$CA" "$OUT" emulationstation "::ffff:1.2.3.4,1:2:3:4:5:6:7:8,::1"
  [[ "$(text "$OUT/server.crt")" == *"IP Address:0:0:0:0:0:FFFF:102:304"* ]]
}

@test "failed openssl x509 gives non-zero in an if and writes no files" {
  tls_ensure_ca "$CA"
  real=$(command -v openssl)
  mkdir -p "$BATS_TEST_TMPDIR/bin"
  cat >"$BATS_TEST_TMPDIR/bin/openssl" <<STUB
#!/bin/sh
if [ "\$1" = x509 ]; then echo "stub: x509 failed" >&2; exit 1; fi
exec "$real" "\$@"
STUB
  chmod +x "$BATS_TEST_TMPDIR/bin/openssl"
  PATH="$BATS_TEST_TMPDIR/bin:$PATH"
  rc=0
  if tls_issue_server "$CA" "$OUT" emulationstation "" 2>"$BATS_TEST_TMPDIR/err"; then rc=0; else rc=1; fi
  [ "$rc" -eq 1 ]
  grep -q "stub: x509 failed" "$BATS_TEST_TMPDIR/err"
  [ ! -e "$OUT/server.crt" ]
  [ ! -e "$OUT/server.key" ]
  [ -z "$(ls -A "$OUT")" ]
}

@test "cert hint uses ssh -t and sets the group" {
  grep -q 'ssh -t -p 11911' "$ROOT/builder/steps/cert.sh"
  [ "$(grep -c -- '-g emulationstation' "$ROOT/builder/steps/cert.sh")" -ge 1 ]
  grep -q 'chown emulationstation:emulationstation' "$ROOT/builder/steps/cert.sh"
}
