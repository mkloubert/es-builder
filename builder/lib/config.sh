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
ES_AUTOSTART WEBUI_UPDATE_URL WEBUI_UPDATE_SIGNERS WEBUI_UPDATE_SSH_KEY WEBUI_UPDATE_KNOWN_HOSTS"

CONFIG_FILE=""
# Repository root for the update file paths (tests may set CONFIG_ROOT).
_CONFIG_LIB_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

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
  printf -v CFG_WEBUI_UPDATE_URL '%s' https://github.com/mkloubert/es-builder

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

  # An empty URL means: self-update is off.
  if [ -n "$CFG_WEBUI_UPDATE_URL" ] && ! config_check_update_url "$CFG_WEBUI_UPDATE_URL"; then
    log_error "WEBUI_UPDATE_URL is not allowed (use https://, ssh://, user@host:path, file:// or an absolute folder)"
    return 1
  fi
  config_check_update_files || return 1
}

# config_check_update_url <url> : returns 0 if the update URL is allowed. These are the same rules
# as CheckURL in webui/internal/update/url.go. Keep both in line.
config_check_update_url() {
  local s=${1-} nl=$'\n' bs=$'\\' host user rest p
  local hostre='^[A-Za-z0-9][A-Za-z0-9.-]*$' userre='^[A-Za-z0-9][A-Za-z0-9._-]*$'
  [ -n "$s" ] && [ "${#s}" -le 512 ] || return 1
  case "$s" in
    -* | *[[:space:]]* | *[[:cntrl:]]* | *"$bs"* | *"$nl"*) return 1 ;;
  esac
  case "$s" in
    https://*) _config_url_net "${s#https://}" https ;;
    ssh://*) _config_url_net "${s#ssh://}" ssh ;;
    file://*)
      case "$s" in *%*) return 1 ;; esac
      _config_url_local "${s#file://}"
      ;;
    /*) _config_url_local "$s" ;;
    *://*) return 1 ;;
    *)
      # scp form: user@host:path
      case "$s" in *@*) ;; *) return 1 ;; esac
      user=${s%%@*}
      rest=${s#*@}
      [[ $user =~ $userre ]] || return 1
      case "$rest" in *:*) ;; *) return 1 ;; esac
      host=${rest%%:*}
      p=${rest#*:}
      [[ $host =~ $hostre ]] || return 1
      case "$p" in "" | -* | /*) return 1 ;; esac
      _config_path_has_part "$p" .. && return 1
      return 0
      ;;
  esac
}

# _config_url_net <rest> <scheme> : checks what follows https:// or ssh://
_config_url_net() {
  local rest=$1 scheme=$2 auth p hostport user host port
  local hostre='^[A-Za-z0-9][A-Za-z0-9.-]*$' userre='^[A-Za-z0-9][A-Za-z0-9._-]*$' portre='^[0-9]{1,5}$'
  case "$rest" in *\?* | *'#'* | *%*) return 1 ;; esac
  case "$rest" in */*) ;; *) return 1 ;; esac
  auth=${rest%%/*}
  p=/${rest#*/}
  [ "$p" != / ] || return 1
  hostport=$auth
  case "$auth" in
    *@*)
      [ "$scheme" = ssh ] || return 1
      user=${auth%%@*}
      hostport=${auth#*@}
      [[ $user =~ $userre ]] || return 1
      ;;
  esac
  host=$hostport
  case "$hostport" in
    *:*)
      host=${hostport%%:*}
      port=${hostport#*:}
      [[ $port =~ $portre ]] || return 1
      ;;
  esac
  [[ $host =~ $hostre ]] || return 1
  _config_path_has_part "$p" .. && return 1
  [[ $p =~ ^/[A-Za-z0-9._~/+@=,:-]*$ ]] || return 1
  return 0
}

# _config_url_local <path> : an absolute, clean folder (no //, ., .. and not only /)
_config_url_local() {
  local p=${1%/}
  case "$p" in /*) ;; *) return 1 ;; esac
  case "$p" in *//* | */) return 1 ;; esac
  _config_path_has_part "$p" . && return 1
  _config_path_has_part "$p" .. && return 1
  return 0
}

# _config_path_has_part <path> <name> : returns 0 if a part between slashes equals <name>.
# The path is split without globbing.
_config_path_has_part() {
  local -a parts
  local part
  IFS=/ read -r -a parts <<<"$1"
  for part in "${parts[@]}"; do
    [ "$part" = "$2" ] && return 0
  done
  return 1
}

# _config_update_file <KEY> : checks one file path of the update options. The path is relative to the
# repository root. It must be a regular file (no symlink, also not in the path) inside the root and
# not below secrets/ or out/.
_config_update_file() {
  local key=$1 var="CFG_$1" v root rel part cur
  local -a parts
  v=${!var}
  [ -n "$v" ] || return 0
  root=${CONFIG_ROOT:-$(cd "$_CONFIG_LIB_DIR/../.." && pwd)}
  rel=$v
  case "$rel" in
    /*)
      log_error "$key must be a path relative to the repository, not an absolute path (found: $v)"
      return 1
      ;;
  esac
  case "$rel" in
    secrets | secrets/* | out | out/* | "" | -*)
      log_error "$key must not be in secrets/ or out/ (found: $v)"
      return 1
      ;;
  esac
  cur=$root
  IFS=/ read -r -a parts <<<"$rel"
  for part in "${parts[@]}"; do
    case "$part" in
      "" | . | ..)
        log_error "$key must not contain . or .. (found: $v)"
        return 1
        ;;
    esac
    cur=$cur/$part
    if [ -L "$cur" ]; then
      log_error "$key must not use a symlink (found: $v)"
      return 1
    fi
  done
  if [ ! -f "$cur" ]; then
    log_error "$key is not a regular file: $v"
    return 1
  fi
}

# config_check_update_files : checks the file keys of the update options (paths and the known_hosts rule).
config_check_update_files() {
  local k
  for k in WEBUI_UPDATE_SIGNERS WEBUI_UPDATE_SSH_KEY WEBUI_UPDATE_KNOWN_HOSTS; do
    _config_update_file "$k" || return 1
  done
  if [ -n "$CFG_WEBUI_UPDATE_SSH_KEY" ] && [ -z "$CFG_WEBUI_UPDATE_KNOWN_HOSTS" ]; then
    log_error "WEBUI_UPDATE_SSH_KEY needs WEBUI_UPDATE_KNOWN_HOSTS"
    return 1
  fi
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
