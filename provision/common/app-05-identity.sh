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

# App layer, in the chroot: values from build.conf that are NOT part of the base cache key
# (ruling R16): password and SSH key for emulationstation, host name, locale, time zone, keyboard.
# The values are NOT passed as environment or arguments (ruling R18). Step 40 writes them to
# the 0600 file $PROVISION_DIR/build.env (default /root/provision/build.env). This script reads
# it with config_load (builder/lib/config.sh, copied to $PROVISION_DIR/lib), which never
# evaluates values. USER_PASSWORD is required; the others are optional.
# The password goes to chpasswd only through stdin, never as an argument, and is never printed.
# Idempotent. Test overrides: PROVISION_ROOT (path prefix), BUILD_ENV (file), PROVISION_LIB (lib dir).
set -euo pipefail

PROVISION_DIR=${PROVISION_DIR:-/root/provision}
BUILD_ENV=${BUILD_ENV:-$PROVISION_DIR/build.env}
PROVISION_LIB=${PROVISION_LIB:-$PROVISION_DIR/lib}
# shellcheck source=builder/lib/log.sh
. "$PROVISION_LIB/log.sh"
# shellcheck source=builder/lib/config.sh
. "$PROVISION_LIB/config.sh"
config_load "$BUILD_ENV"

log() { printf '[app-05-identity] %s\n' "$*" >&2; }
die() {
  printf '[app-05-identity] ERROR: %s\n' "$*" >&2
  exit 1
}

R=${PROVISION_ROOT:-}
U=emulationstation
H=$R/home/$U
pw=$CFG_USER_PASSWORD
key=$CFG_USER_SSH_PUBKEY
host=$CFG_HOSTNAME
locale=$CFG_LOCALE
tz=$CFG_TIMEZONE
keymap=$CFG_KEYMAP

# --- Password ----------------------------------------------------------------
[ -n "$pw" ] || die "USER_PASSWORD is empty"
case "$pw" in
  *$'\n'* | *$'\r'*) die "Password must not contain a line break" ;;
esac
printf '%s:%s\n' "$U" "$pw" | chpasswd
log "Password set for $U"

# --- Host name ----------------------------------------------------------------
if [ -n "$host" ]; then
  [[ $host =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] || die "Invalid host name: $host"
  mkdir -p "$R/etc"
  if [ "$(cat "$R/etc/hostname" 2>/dev/null || true)" != "$host" ]; then
    printf '%s\n' "$host" >"$R/etc/hostname"
  fi
  hosts=$R/etc/hosts
  touch "$hosts"
  # one 127.0.1.1 line with the host name; keep all other lines
  if ! grep -qxF "127.0.1.1"$'\t'"$host" "$hosts" || [ "$(grep -c '^127\.0\.1\.1[[:space:]]' "$hosts")" -ne 1 ]; then
    grep -v '^127\.0\.1\.1[[:space:]]' "$hosts" >"$hosts.new" || true
    printf '127.0.1.1\t%s\n' "$host" >>"$hosts.new"
    cat "$hosts.new" >"$hosts"
    rm -f "$hosts.new"
  fi
  log "Host name $host"
fi

# --- SSH-Key -----------------------------------------------------------------
if [ -n "$key" ]; then
  mkdir -p "$H/.ssh"
  chmod 0700 "$H/.ssh"
  printf '%s\n' "$key" >"$H/.ssh/authorized_keys.new"
  chmod 0600 "$H/.ssh/authorized_keys.new"
  mv -f "$H/.ssh/authorized_keys.new" "$H/.ssh/authorized_keys"
  chown -R "$U:$U" "$H/.ssh"
  log "authorized_keys installed for $U"
fi

# --- Locale ------------------------------------------------------------------
if [ -n "$locale" ]; then
  case "$locale" in
    *[!A-Za-z0-9_.@-]*) die "Invalid locale: $locale" ;;
  esac
  entry=$(awk -v l="$locale" '$1 == l { print; exit }' "$R/usr/share/i18n/SUPPORTED" 2>/dev/null || true)
  [ -n "$entry" ] || die "Locale $locale is not in /usr/share/i18n/SUPPORTED"
  gen=$R/etc/locale.gen
  touch "$gen"
  if ! grep -qxF "$entry" "$gen"; then
    if grep -qxE "#[[:space:]]*${entry//./\\.}" "$gen"; then
      sed -i -E "s/^#[[:space:]]*${entry//./\\.}\$/$entry/" "$gen"
    else
      printf '%s\n' "$entry" >>"$gen"
    fi
  fi
  locale-gen
  update-locale "LANG=$locale"
  log "Locale $locale"
fi

# --- Time zone ----------------------------------------------------------------
if [ -n "$tz" ]; then
  case "$tz" in
    /* | *..* | *[!A-Za-z0-9_+/-]*) die "Invalid time zone: $tz" ;;
  esac
  if [ -f "$R/usr/share/zoneinfo/$tz" ]; then
    if [ "$(readlink "$R/etc/localtime" 2>/dev/null || true)" != "/usr/share/zoneinfo/$tz" ]; then
      ln -sfn "/usr/share/zoneinfo/$tz" "$R/etc/localtime"
    fi
    if [ "$(cat "$R/etc/timezone" 2>/dev/null || true)" != "$tz" ]; then
      printf '%s\n' "$tz" >"$R/etc/timezone"
    fi
    log "Time zone $tz"
  else
    log "WARNING: Time zone $tz is unknown (/usr/share/zoneinfo/$tz is missing), left unchanged"
  fi
fi

# --- Keyboard ----------------------------------------------------------------
if [ -n "$keymap" ]; then
  case "$keymap" in
    *[!a-z0-9,_-]*) die "Invalid keyboard layout: $keymap" ;;
  esac
  kb=$R/etc/default/keyboard
  touch "$kb"
  if grep -q '^XKBLAYOUT=' "$kb"; then
    if ! grep -qxF "XKBLAYOUT=\"$keymap\"" "$kb"; then
      sed -i "s/^XKBLAYOUT=.*/XKBLAYOUT=\"$keymap\"/" "$kb"
    fi
  else
    printf 'XKBLAYOUT="%s"\n' "$keymap" >>"$kb"
  fi
  log "Keyboard $keymap"
fi
