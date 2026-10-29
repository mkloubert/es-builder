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

# TLS helpers: a local CA and a server certificate (host: not needed; runs in the container or locally).
# Needs openssl 3.0 or newer. Keys are ECDSA P-256. The CA key never leaves the CA directory.
set -euo pipefail

# _tls_valid_ipv4 <text>
_tls_valid_ipv4() {
  local ip=$1 part
  [[ $ip =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] || return 1
  local IFS=.
  for part in $ip; do
    [ "$((10#$part))" -le 255 ] || return 1
  done
  return 0
}

# _tls_hex_groups <text> : succeeds if text is empty or groups of 1-4 hex digits separated by ":"; prints the count
_tls_hex_groups() {
  local t=$1 g n=0
  if [ -n "$t" ]; then
    local IFS=:
    for g in $t; do
      [[ $g =~ ^[0-9a-fA-F]{1,4}$ ]] || return 1
      n=$((n + 1))
    done
    # a trailing or doubled ":" would give a missing group
    [[ $t != *: && $t != :* && $t != *::* ]] || return 1
  fi
  printf '%s' "$n"
}

# _tls_valid_ipv6 <text> : hex groups (max 4 digits, max 8 groups), one "::" at most, optional IPv4 tail
_tls_valid_ipv6() {
  local ip=$1 tail left right nl nr
  [[ $ip == *:* ]] || return 1
  [[ $ip =~ ^[0-9a-fA-F:.]+$ ]] || return 1
  [[ $ip != *:::* ]] || return 1
  if [[ $ip == *.* ]]; then
    tail=${ip##*:}
    _tls_valid_ipv4 "$tail" || return 1
    ip=${ip%"$tail"}0:0
  fi
  if [[ $ip == *::* ]]; then
    left=${ip%%::*}
    right=${ip#*::}
    [[ $right != *::* ]] || return 1
    nl=$(_tls_hex_groups "$left") || return 1
    nr=$(_tls_hex_groups "$right") || return 1
    [ $((nl + nr)) -le 7 ] || return 1
  else
    nl=$(_tls_hex_groups "$ip") || return 1
    [ "$nl" -eq 8 ] || return 1
  fi
  return 0
}

# _tls_valid_dns <text>
_tls_valid_dns() {
  [ "${#1}" -le 253 ] || return 1
  # a last label with only digits looks like a broken IP address
  [[ ${1##*.} =~ ^[0-9]+$ ]] && return 1
  [[ $1 =~ ^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$ ]]
}

# _tls_san_list <hostname> <extra-sans>
# Prints the subjectAltName value, for example "DNS:a,DNS:a.local,IP:1.2.3.4". Dies on a bad entry.
_tls_san_list() {
  local host=$1 extra=$2 item san
  [[ $host =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] ||
    die "Invalid host name '$host': use a-z, 0-9 and '-', start and end with a letter or digit."
  san="DNS:$host,DNS:$host.local"
  local -a items=()
  if [ -n "$extra" ]; then
    IFS=',' read -r -a items <<<"$extra"
  fi
  for item in ${items[@]+"${items[@]}"}; do
    item=${item#"${item%%[![:space:]]*}"}
    item=${item%"${item##*[![:space:]]}"}
    [ -n "$item" ] || continue
    if _tls_valid_ipv4 "$item" || _tls_valid_ipv6 "$item"; then
      san="$san,IP:$item"
    elif _tls_valid_dns "$item"; then
      san="$san,DNS:$item"
    else
      die "Invalid entry in TLS_EXTRA_SANS: '$item' (use an IP address or a DNS name)."
    fi
  done
  printf '%s' "$san"
}

# tls_ensure_ca <dir> : create <dir>/ca.key and <dir>/ca.crt if both are missing.
# If only one of them exists, it stops (a new CA would hide the old one that users imported).
tls_ensure_ca() {
  local dir=$1
  [ -n "$dir" ] || die "tls_ensure_ca: directory is missing"
  if [ -f "$dir/ca.key" ] && [ -f "$dir/ca.crt" ]; then
    return 0
  fi
  if [ -e "$dir/ca.key" ] || [ -e "$dir/ca.crt" ]; then
    die "Only one of ca.key and ca.crt exists in $dir. Restore the missing file or remove both to make a new CA."
  fi
  log_info "Creating a new local CA in $dir"
  (
    umask 077
    mkdir -p "$dir" || exit 1
    stage=$(mktemp -d "$dir/.stage.XXXXXX") || exit 1
    trap 'rm -rf "$stage"' EXIT
    cat >"$stage/ca.cnf" <<'CNF' || exit 1
[req]
distinguished_name = dn
prompt = no
x509_extensions = v3_ca
[dn]
CN = EmuStation Builder Local CA
[v3_ca]
basicConstraints = critical,CA:TRUE
keyUsage = critical,keyCertSign,cRLSign
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid:always
CNF
    openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$stage/ca.key" || exit 1
    openssl req -new -x509 -key "$stage/ca.key" -config "$stage/ca.cnf" -days 3650 \
      -set_serial "0x1$(openssl rand -hex 15)" -out "$stage/ca.crt" || exit 1
    chmod 600 "$stage/ca.key" || exit 1
    chmod 644 "$stage/ca.crt" || exit 1
    mv "$stage/ca.key" "$dir/ca.key" || exit 1
    mv "$stage/ca.crt" "$dir/ca.crt" || exit 1
  )
}

# tls_issue_server <cadir> <outdir> <hostname> <extra-sans>
# Writes <outdir>/server.key (0600) and <outdir>/server.crt (server certificate followed by the CA certificate).
# The files are made in a staging directory and moved into place at the end, so a failure leaves nothing behind.
tls_issue_server() {
  local cadir=$1 outdir=$2 host=$3 extra=${4:-} san
  san=$(_tls_san_list "$host" "$extra") || return 1
  if [ ! -f "$cadir/ca.key" ] || [ ! -f "$cadir/ca.crt" ]; then
    die "No CA found in $cadir. Run tls_ensure_ca first."
  fi
  log_info "Issuing server certificate for: $san"
  (
    umask 077
    mkdir -p "$outdir" || exit 1
    chmod 755 "$outdir" || exit 1
    stage=$(mktemp -d "$outdir/.stage.XXXXXX") || exit 1
    trap 'rm -rf "$stage"' EXIT
    cat >"$stage/leaf.cnf" <<CNF || exit 1
[v3_leaf]
basicConstraints = critical,CA:FALSE
keyUsage = critical,digitalSignature
extendedKeyUsage = serverAuth
subjectAltName = $san
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid:always
CNF
    openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$stage/server.key" || exit 1
    openssl req -new -key "$stage/server.key" -subj "/CN=$host" -out "$stage/server.csr" || exit 1
    openssl x509 -req -in "$stage/server.csr" -CA "$cadir/ca.crt" -CAkey "$cadir/ca.key" \
      -set_serial "0x1$(openssl rand -hex 15)" -days 825 \
      -extfile "$stage/leaf.cnf" -extensions v3_leaf -out "$stage/leaf.crt" || exit 1
    cat "$stage/leaf.crt" "$cadir/ca.crt" >"$stage/server.crt" || exit 1
    chmod 600 "$stage/server.key" || exit 1
    chmod 644 "$stage/server.crt" || exit 1
    mv "$stage/server.key" "$outdir/server.key" || exit 1
    mv "$stage/server.crt" "$outdir/server.crt" || exit 1
  )
}
