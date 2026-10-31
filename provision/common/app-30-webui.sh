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

# App layer, in the chroot: web service. Step 40 has already
#  - copied the unit file from overlay/,
#  - installed /usr/local/bin/emulationstation-web and the TLS files (server.key, server.crt)
#    in /etc/emulationstation-web/tls (owner root until now).
# This script makes web.conf and sets owner and mode (ruling R10):
#  - /etc/emulationstation-web and tls/: emulationstation:emulationstation 0750 (the service saves
#    web.conf with a temp file and rename in this directory),
#  - server.key 0600, server.crt 0644, web.conf 0600, all owned by emulationstation,
#  - web.conf: WEB_USER and WEB_PASSWORD_HASH from "emulationstation-web credentials" (the password
#    goes through stdin, never as argument or environment), then the ScreenScraper values, SOURCE_URL
#    and UI_DIR.
#    The Go defaults are already right for the image (TLS paths, HOME_DIR, ES_SYSTEMS_CFG, LISTEN),
#    and "credentials" writes all keys, so nothing else is set.
#  - /home/emulationstation/webui-custom: a copy of webui-custom/ from the provision folder (the
#    old content is removed), owned by emulationstation, folders 0755, files 0644,
#  - the service is enabled,
#  - jq is installed (the helper uses it for usb-list). This script runs in the chroot, so apt-get
#    works directly. The cached base layer may have old package lists, so update them first.
# The ScreenScraper password is stored as plain text in web.conf (file mode 0600; Plan 1 design).
# Idempotent. Test overrides: PROVISION_ROOT (path prefix), PROVISION_DIR, BUILD_ENV, PROVISION_LIB,
# WEB_BIN.
set -euo pipefail

PROVISION_DIR=${PROVISION_DIR:-/root/provision}
BUILD_ENV=${BUILD_ENV:-$PROVISION_DIR/build.env}
PROVISION_LIB=${PROVISION_LIB:-$PROVISION_DIR/lib}
# shellcheck source=builder/lib/log.sh
. "$PROVISION_LIB/log.sh"
# shellcheck source=builder/lib/config.sh
. "$PROVISION_LIB/config.sh"
# shellcheck source=builder/lib/webconf.sh
. "$PROVISION_LIB/webconf.sh"
config_load "$BUILD_ENV"

log() { printf '[app-30-webui] %s\n' "$*" >&2; }
die() {
  printf '[app-30-webui] ERROR: %s\n' "$*" >&2
  exit 1
}

R=${PROVISION_ROOT:-}
U=emulationstation
BIN=${WEB_BIN:-/usr/local/bin/emulationstation-web}
D=/etc/emulationstation-web
CONF=$R$D/web.conf
UI=/home/$U/webui-custom

[ -x "$R$BIN" ] || die "Web binary is missing: $BIN (step 40 installs it)"
[ -f "$R$D/tls/server.key" ] || die "TLS key is missing in $D/tls (step 40 installs it)"
[ -f "$R$D/tls/server.crt" ] || die "TLS certificate is missing in $D/tls (step 40 installs it)"
[ -f "$R/etc/systemd/system/emulationstation-web.service" ] || die "Unit file is missing: emulationstation-web.service"

web_user=$CFG_WEB_USER
web_pw=$CFG_WEB_PASSWORD
[ -n "$web_user" ] || die "WEB_USER is empty"
[ -n "$web_pw" ] || die "WEB_PASSWORD is empty"
case "$web_pw$web_user" in
  *$'\n'* | *$'\r'*) die "Web user and password must not contain a line break" ;;
esac

# --- jq (needed by the root helper for usb-list) -----------------------------------
if ! dpkg -s jq >/dev/null 2>&1; then
  DEBIAN_FRONTEND=noninteractive apt-get update </dev/null
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends jq </dev/null
  log "jq installed"
fi

# --- Directories and TLS files -------------------------------------------------
chown "$U:$U" "$R$D" "$R$D/tls"
chmod 0750 "$R$D" "$R$D/tls"
chown "$U:$U" "$R$D/tls/server.key" "$R$D/tls/server.crt"
chmod 0600 "$R$D/tls/server.key"
chmod 0644 "$R$D/tls/server.crt"
chown root:root "$R/etc/systemd/system/emulationstation-web.service"
chmod 0644 "$R/etc/systemd/system/emulationstation-web.service"

# --- web.conf --------------------------------------------------------------------
rm -f "$CONF"
# The binary path is taken inside the chroot. With PROVISION_ROOT (tests) the binary is run directly.
if [ -n "$R" ]; then
  run_bin=("$R$BIN")
else
  run_bin=("$BIN")
fi
printf '%s\n' "$web_pw" | "${run_bin[@]}" credentials --config "$CONF" --user "$web_user" --password-stdin ||
  die "'emulationstation-web credentials' failed (does the binary run? the arm64 binary needs binfmt on an amd64 host)"
[ -f "$CONF" ] || die "credentials did not create $CONF"
# Belt and braces: the file is already 0600, but the owner would be root.
chown "$U:$U" "$CONF"
chmod 0600 "$CONF"

sc_user=$CFG_SCREENSCRAPER_USER
sc_pw=$CFG_SCREENSCRAPER_PASSWORD
if [ -n "$sc_user" ]; then webconf_set "$CONF" SCREENSCRAPER_USER "$sc_user"; fi
if [ -n "$sc_pw" ]; then webconf_set "$CONF" SCREENSCRAPER_PASSWORD "$sc_pw"; fi
# Always written; an empty value means no source link in the footer (AGPL section 13).
webconf_set "$CONF" SOURCE_URL "$CFG_SOURCE_URL"
webconf_set "$CONF" UI_DIR "$UI"
chown "$U:$U" "$CONF"
chmod 0600 "$CONF"
webconf_hash_ok "$CONF" || die "WEB_PASSWORD_HASH in web.conf is not a bcrypt hash"
webconf_no_plain_password "$CONF" "$web_pw" || die "The web password is in web.conf as plain text"
log "web.conf written for user $web_user"

# --- Custom web UI files ----------------------------------------------------------
# The folder matches webui-custom/ of the repository after each build; it is owned by the user,
# so the owner can change the files over SSH (the System page reloads the templates).
rm -rf -- "$R$UI"
mkdir -p "$R$UI"
if [ -d "$PROVISION_DIR/webui-custom" ]; then
  cp -R -- "$PROVISION_DIR/webui-custom/." "$R$UI/"
fi
chown -R "$U:$U" "$R$UI"
find "$R$UI" -type d -exec chmod 0755 {} +
find "$R$UI" -type f -exec chmod 0644 {} +
log "custom web UI folder $UI ready ($(find "$R$UI" -type f | wc -l) files)"

# --- Enable the service --------------------------------------------------------------
systemctl enable emulationstation-web.service
log "emulationstation-web.service enabled"
