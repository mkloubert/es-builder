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

# Read build.conf without executing anything. Runs on the host (Bash 3.2) and in the container.
# Values are stored literally in CFG_<KEY>; the file is never read with `source` or eval.
# The callers read the CFG_* variables.
# shellcheck disable=SC2034
set -euo pipefail

CONFIG_KEYS="HOSTNAME LOCALE TIMEZONE KEYMAP USER_PASSWORD USER_SSH_PUBKEY WEB_USER WEB_PASSWORD \
WIFI_SSID WIFI_PASSWORD WIFI_COUNTRY TLS_EXTRA_SANS SCREENSCRAPER_USER SCREENSCRAPER_PASSWORD \
RPI_IMAGE_URL RPI_IMAGE_CHECKSUM VM_IMAGE_URL_AMD64 VM_IMAGE_CHECKSUM_AMD64 \
VM_IMAGE_URL_ARM64 VM_IMAGE_CHECKSUM_ARM64 RETROPIE_SETUP_REF EPICNOIR_REF XPADNEO_REF SOURCE_URL \
ES_AUTOSTART"

CONFIG_FILE=""

_config_known() {
  local k
  for k in $CONFIG_KEYS; do
    [ "$k" = "$1" ] && return 0
  done
  return 1
}

# config_load <file>
config_load() {
  local file=$1 line key val first last k
  [ -f "$file" ] || { log_error "Configuration file not found: $file"; return 1; }
  CONFIG_FILE=$file

  for k in $CONFIG_KEYS; do
    printf -v "CFG_$k" '%s' ''
  done
  printf -v CFG_HOSTNAME '%s' emulationstation
  printf -v CFG_LOCALE '%s' de_DE.UTF-8
  printf -v CFG_TIMEZONE '%s' Europe/Berlin
  printf -v CFG_KEYMAP '%s' de
  printf -v CFG_WEB_USER '%s' zocken
  printf -v CFG_WEB_PASSWORD '%s' zocken
  printf -v CFG_WIFI_COUNTRY '%s' DE
  printf -v CFG_SOURCE_URL '%s' https://github.com/mkloubert/es-builder
  printf -v CFG_ES_AUTOSTART '%s' no

  while IFS= read -r line || [ -n "$line" ]; do
    line=${line%$'\r'}
    # remove leading spaces/tabs
    line=${line#"${line%%[![:space:]]*}"}
    case "$line" in
      "" | "#"*) continue ;;
    esac
    case "$line" in
      *=*) ;;
      *)
        log_warn "$file: line without '=' ignored"
        continue
        ;;
    esac
    key=${line%%=*}
    val=${line#*=}
    # remove white space around the key
    key=${key%"${key##*[![:space:]]}"}
    case "$key" in
      "" | [!A-Z]* | *[!A-Z0-9_]*)
        log_warn "$file: invalid key '$key' ignored"
        continue
        ;;
    esac
    if ! _config_known "$key"; then
      log_warn "$file: unknown key '$key' ignored"
      continue
    fi
    # remove exactly one pair of surrounding quotes
    if [ "${#val}" -ge 2 ]; then
      first=${val:0:1}
      last=${val: -1}
      if { [ "$first" = '"' ] && [ "$last" = '"' ]; } || { [ "$first" = "'" ] && [ "$last" = "'" ]; }; then
        val=${val:1:${#val}-2}
      fi
    fi
    printf -v "CFG_$key" '%s' "$val"
  done <"$file"

  # ES_AUTOSTART: only these two words (an empty value in the file is wrong too)
  case "$CFG_ES_AUTOSTART" in
    yes | no) ;;
    *)
      log_error "ES_AUTOSTART must be yes or no (found: $CFG_ES_AUTOSTART)"
      return 1
      ;;
  esac
}

# config_require <KEY>... : fails (return 1) with a message if a value is empty.
config_require() {
  local k var missing=0
  for k in "$@"; do
    var="CFG_$k"
    if [ -z "${!var:-}" ]; then
      log_error "$k is missing in ${CONFIG_FILE:-build.conf} (see build.conf.example)"
      missing=1
    fi
  done
  return "$missing"
}
