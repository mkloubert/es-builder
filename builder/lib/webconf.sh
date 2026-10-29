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

# Helpers for web.conf (KEY=VALUE lines, as read by the Go service in webui/internal/config).
# The Go parser splits at the first "=", keeps the rest of the line as the value (no quotes, no
# escapes) and the last line of a key wins. So values are written literally.
# Secrets are never passed as arguments to other programs here: only bash builtins see them.
set -euo pipefail

# webconf_set <file> <KEY> <VALUE> : replace the line of KEY, or append it. The mode of the file is kept.
webconf_set() {
  local file=$1 key=$2 value=$3 line tmp done=0
  case "$key" in
    '' | *[!A-Z0-9_]*) echo "webconf_set: invalid key '$key'" >&2; return 1 ;;
  esac
  case "$value" in
    *$'\n'* | *$'\r'*) echo "webconf_set: the value of $key must not contain a line break" >&2; return 1 ;;
  esac
  [ -f "$file" ] || { echo "webconf_set: file is missing: $file" >&2; return 1; }
  tmp=$(mktemp "$file.XXXXXX") || return 1
  chmod --reference="$file" "$tmp" 2>/dev/null || chmod 600 "$tmp"
  while IFS= read -r line || [ -n "$line" ]; do
    if [ "${line%%=*}" = "$key" ]; then
      # drop the old line; write the new one once, at the place of the first old line
      if [ "$done" -eq 0 ]; then printf '%s=%s\n' "$key" "$value" >>"$tmp"; done=1; fi
    else
      printf '%s\n' "$line" >>"$tmp"
    fi
  done <"$file"
  if [ "$done" -eq 0 ]; then printf '%s=%s\n' "$key" "$value" >>"$tmp"; fi
  mv "$tmp" "$file"
}

# webconf_get <file> <KEY> : prints the value of the last line of KEY; fails if there is none
webconf_get() {
  local file=$1 key=$2 line found=1 val=
  while IFS= read -r line || [ -n "$line" ]; do
    if [ "${line%%=*}" = "$key" ] && [[ $line == *=* ]]; then val=${line#*=}; found=0; fi
  done <"$file"
  [ "$found" -eq 0 ] || return 1
  printf '%s\n' "$val"
}

# webconf_hash_ok <file> : WEB_PASSWORD_HASH looks like a bcrypt hash ($2a$/$2b$/$2y$, cost, 53 chars)
webconf_hash_ok() {
  local h
  h=$(webconf_get "$1" WEB_PASSWORD_HASH) || return 1
  [[ $h =~ ^\$2[aby]\$[0-9]{2}\$[./A-Za-z0-9]{53}$ ]]
}

# webconf_no_plain_password <file> <password> : the web password is not stored as plain text.
# Lines of WEB_USER and SCREENSCRAPER_* are skipped (they may be the same text by choice).
# No other line may have the password as its exact value. Passwords of 8 or more characters are also
# searched inside the values of all other lines (never in key names). Short passwords are not searched
# inside values: they would match by chance (URLs, paths, the random hash). webconf_hash_ok checks
# that WEB_PASSWORD_HASH is a bcrypt hash.
webconf_no_plain_password() {
  local file=$1 pw=$2 line key
  [ -n "$pw" ] || return 0
  while IFS= read -r line || [ -n "$line" ]; do
    key=${line%%=*}
    case "$key" in WEB_USER | SCREENSCRAPER_USER | SCREENSCRAPER_PASSWORD) continue ;; esac
    [ "${line#*=}" != "$pw" ] || return 1
    [ "$key" != WEB_PASSWORD_HASH ] || continue
    if [ "${#pw}" -ge 8 ] && [[ ${line#*=} == *"$pw"* ]]; then return 1; fi
  done <"$file"
  return 0
}
