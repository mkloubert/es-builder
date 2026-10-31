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

# App layer (Task 6): helpers of step 40 (build.env, overlay copy, staging) and
# provision/common/app-10-security.sh with stubs and PROVISION_ROOT.
# The real mount/chroot run happens in the builder container.
# shellcheck disable=SC2016,SC2030,SC2031,SC2034,SC2154

bats_require_minimum_version 1.5.0

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  STUBS="$BATS_TEST_TMPDIR/stubs"
  CALLS="$BATS_TEST_TMPDIR/calls"
  mkdir -p "$STUBS"
  : >"$CALLS"
  export CALLS
}

make_cmd_stubs() {
  local c
  for c in "$@"; do
    printf '#!/usr/bin/env bash\necho "%s $*" >>"$CALLS"\n' "$c" >"$STUBS/$c"
  done
  chmod +x "$STUBS"/*
  PATH="$STUBS:$PATH"
}

# Source the step without running main
load_step() {
  # shellcheck source=/dev/null
  . "$ROOT/builder/steps/40-provision-app.sh"
}

# --- Step 40 -------------------------------------------------------------------

@test "40-provision-app.sh: no target -> exit 2; missing base layer -> 'base layer missing'" {
  run bash "$ROOT/builder/steps/40-provision-app.sh"
  [ "$status" -eq 2 ]
  cat >"$BATS_TEST_TMPDIR/build.conf" <<EOF2
USER_PASSWORD=x
RPI_IMAGE_URL=file:///nowhere/pi.img.xz
RPI_IMAGE_CHECKSUM=sha256:$(printf 'a%.0s' {1..64})
EOF2
  run env BUILD_CONFIG="$BATS_TEST_TMPDIR/build.conf" CACHE_DIR="$BATS_TEST_TMPDIR/cache" \
    bash "$ROOT/builder/steps/40-provision-app.sh" rpi4
  [ "$status" -ne 0 ]
  [[ "$output" == *"base layer missing"* ]]
}

@test "40-provision-app.sh: has the function for the web binary and TLS, and stages webconf.sh" {
  load_step
  declare -F app_build_web_and_tls >/dev/null
  run app_build_web_and_tls
  [ "$status" -ne 0 ] # needs the architecture argument
  local root="$BATS_TEST_TMPDIR/root"
  mkdir -p "$root"
  CFG_USER_PASSWORD=x
  app_stage_provision "$root" rpi4
  [ -f "$root/root/provision/lib/webconf.sh" ]
  [ -f "$root/root/provision/common/app-30-webui.sh" ]
}

@test "app_write_env: mode 0600, quoted values, config_load reads back exactly (also quotes in values)" {
  load_step
  CFG_USER_PASSWORD='"quoted" pass$(id)`x`'
  CFG_USER_SSH_PUBKEY='ssh-ed25519 AAAA me@host'
  CFG_WEB_USER=zocken CFG_WEB_PASSWORD=''
  CFG_HOSTNAME=retro CFG_TLS_EXTRA_SANS='DNS:a.lan,IP:10.0.0.2' CFG_ES_AUTOSTART=yes
  local f="$BATS_TEST_TMPDIR/build.env"
  app_write_env "$f"
  [ "$(stat -c %a "$f")" = 600 ]
  grep -qx 'HOSTNAME="retro"' "$f"
  grep -qx 'ES_AUTOSTART="yes"' "$f"
  CFG_USER_PASSWORD=changed CFG_TLS_EXTRA_SANS=changed CFG_ES_AUTOSTART=no
  config_load "$f"
  [ "$CFG_ES_AUTOSTART" = yes ]
  [ "$CFG_USER_PASSWORD" = '"quoted" pass$(id)`x`' ]
  [ "$CFG_TLS_EXTRA_SANS" = 'DNS:a.lan,IP:10.0.0.2' ]
  [ "$CFG_USER_SSH_PUBKEY" = 'ssh-ed25519 AAAA me@host' ]
  # no upstream pins or image URLs in the file
  run ! grep -E 'IMAGE|_REF' "$f"
}

@test "app_write_env: a line break in a value is refused" {
  load_step
  CFG_USER_PASSWORD=$'a\nb'
  run app_write_env "$BATS_TEST_TMPDIR/build.env"
  [ "$status" -ne 0 ]
}

@test "app_copy_overlay: copies the tree with modes, keeps modes of existing directories" {
  load_step
  local dst="$BATS_TEST_TMPDIR/root"
  mkdir -p "$dst/etc" "$dst/usr/local"
  chmod 0751 "$dst/etc"
  app_copy_overlay "$ROOT/overlay" "$dst"
  [ -x "$dst/usr/local/lib/emulationstation/priv" ]
  [ -f "$dst/etc/nftables.conf" ]
  [ -f "$dst/etc/sudoers.d/emulationstation" ]
  [ "$(stat -c %a "$dst/etc")" = 751 ]
  [ "$(stat -c %a "$dst/usr/local/lib/emulationstation/priv")" = "$(stat -c %a "$ROOT/overlay/usr/local/lib/emulationstation/priv")" ]
}

@test "app_stage_provision: scripts, lib and 0600 build.env; no base scripts; directory 0700" {
  load_step
  local root="$BATS_TEST_TMPDIR/root"
  mkdir -p "$root"
  CFG_USER_PASSWORD=secret
  app_stage_provision "$root" rpi4
  local p="$root/root/provision"
  [ "$(stat -c %a "$p")" = 700 ]
  [ "$(stat -c %a "$p/build.env")" = 600 ]
  [ -f "$p/lib/config.sh" ]
  [ -f "$p/lib/log.sh" ]
  [ -f "$p/common/app-05-identity.sh" ]
  [ -f "$p/common/app-10-security.sh" ]
  run ! ls "$p"/common/base-*.sh "$p"/rpi4/base-*.sh
  grep -qx 'USER_PASSWORD="secret"' "$p/build.env"
}

# --- app-10-security.sh --------------------------------------------------------

security_root() {
  local r=$1
  mkdir -p "$r"
  app_copy_overlay "$ROOT/overlay" "$r"
  mkdir -p "$r/etc/ssh"
  : >"$r/etc/ssh/ssh_host_ed25519_key"
  : >"$r/etc/ssh/ssh_host_ed25519_key.pub"
  printf 'Include /etc/ssh/sshd_config.d/*.conf\n' >"$r/etc/ssh/sshd_config"
  # visudo needs the owner root:root, which a non-root test run cannot have: use a stub
  # that checks the syntax of a copy (visudo -c accepts any owner with -f only as root).
}

@test "app-10-security.sh: modes, enables ssh/nftables/fail2ban, no host keys, ssh.socket off, purge installed packages only" {
  load_step
  make_cmd_stubs chown systemctl apt-get
  cat >"$STUBS/dpkg-query" <<'EOF2'
#!/usr/bin/env bash
case "${@: -1}" in
  avahi-daemon | rpcbind) printf 'install ok installed' ;;
  triggerhappy) printf 'deinstall ok config-files' ;;
  *) exit 1 ;;
esac
EOF2
  chmod +x "$STUBS/dpkg-query"
  local r="$BATS_TEST_TMPDIR/r"
  security_root "$r"
  run env PROVISION_ROOT="$r" bash "$ROOT/provision/common/app-10-security.sh"
  echo "$output"
  [ "$status" -eq 0 ]
  [ "$(stat -c %a "$r/etc/sudoers.d/emulationstation")" = 440 ]
  [ "$(stat -c %a "$r/usr/local/lib/emulationstation/priv")" = 755 ]
  [ "$(stat -c %a "$r/etc/nftables.conf")" = 644 ]
  grep -q 'chown root:root .*/etc/sudoers.d/emulationstation' "$CALLS"
  grep -qx 'systemctl enable ssh.service' "$CALLS"
  grep -qx 'systemctl enable nftables.service' "$CALLS"
  grep -qx 'systemctl enable fail2ban.service' "$CALLS"
  grep -qx 'systemctl disable ssh.socket' "$CALLS"
  grep -qx 'apt-get -y purge avahi-daemon rpcbind' "$CALLS"
  grep -q '^systemctl mask avahi-daemon.service' "$CALLS"
  run ! ls "$r"/etc/ssh/ssh_host_*
}

@test "app-10-security.sh: nothing to purge -> apt-get is not called; invalid sudoers file -> abort" {
  load_step
  make_cmd_stubs chown systemctl apt-get
  printf '#!/usr/bin/env bash\nexit 1\n' >"$STUBS/dpkg-query"
  chmod +x "$STUBS/dpkg-query"
  local r="$BATS_TEST_TMPDIR/r"
  security_root "$r"
  run env PROVISION_ROOT="$r" bash "$ROOT/provision/common/app-10-security.sh"
  [ "$status" -eq 0 ]
  run ! grep -q '^apt-get' "$CALLS"
  chmod 0644 "$r/etc/sudoers.d/emulationstation"
  printf 'emulationstation ALL=(root NOPASSWD\n' >"$r/etc/sudoers.d/emulationstation"
  run env PROVISION_ROOT="$r" bash "$ROOT/provision/common/app-10-security.sh"
  [ "$status" -ne 0 ]
  [[ "$output" == *sudoers* ]]
}

@test "overlay: sshd config has the port and the secure settings; jails have the fixed values" {
  local c="$ROOT/overlay/etc/ssh/sshd_config.d/10-emulationstation.conf" j="$ROOT/overlay/etc/fail2ban/jail.d/emulationstation.conf"
  grep -qx 'Port 11911' "$c"
  grep -qx 'ListenAddress 0.0.0.0' "$c"
  grep -qx 'PermitRootLogin no' "$c"
  grep -qx 'MaxAuthTries 5' "$c"
  [ "$(grep -c '^maxretry = 5$' "$j")" -eq 3 ]
  [ "$(grep -c '^findtime = 1h$' "$j")" -eq 3 ]
  [ "$(grep -c '^bantime = 1h$' "$j")" -eq 3 ]
  grep -qx 'journalmatch = _SYSTEMD_UNIT=ssh.service' "$j"
  grep -qx 'banaction = nftables-multiport' "$j"
  grep -qx 'backend = systemd' "$j"
  grep -qx 'port = 11911' "$j"
  grep -qx 'port = 11944' "$j"
}

@test "overlay: nftables.conf has no flush ruleset and allows only 11911/11944" {
  local n="$ROOT/overlay/etc/nftables.conf"
  run ! grep -E '^[[:space:]]*flush ruleset' "$n"
  # no "destroy" (needs Linux 6.3, the Debian 12 VM has 6.1): make the table exist, then delete it
  run ! grep -q destroy <(grep -v '^#' "$n")
  local t d
  t=$(grep -nx 'table inet filter' "$n" | head -n1 | cut -d: -f1)
  d=$(grep -nx 'delete table inet filter' "$n" | head -n1 | cut -d: -f1)
  [ -n "$t" ] && [ -n "$d" ]
  [ $((t + 1)) -eq "$d" ]
  grep -q 'tcp dport { 11911, 11944 } accept' "$n"
  grep -q 'policy drop' "$n"
}

@test "overlay: nftables.service drop-in clears ExecStop and never flushes the whole ruleset" {
  local d="$ROOT/overlay/etc/systemd/system/nftables.service.d/keep-f2b.conf"
  [ -f "$d" ]
  grep -qx 'ExecStop=' "$d"
  grep -qxF 'ExecStop=/usr/sbin/nft "table inet filter; delete table inet filter"' "$d"
  run ! grep -q destroy <(grep -v '^#' "$d")
  run ! grep -E 'flush ruleset' <(grep -v '^#' "$d")
}

# --- Web service (Task 8) --------------------------------------------------------

load_webconf() {
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/webconf.sh"
}

@test "webconf_set: appends a new key, replaces an existing one once, keeps the mode" {
  load_webconf
  local f="$BATS_TEST_TMPDIR/web.conf"
  printf 'WEB_USER=a\nSOURCE_URL=old\nSOURCE_URL=older\nLISTEN=0.0.0.0:11944\n' >"$f"
  chmod 600 "$f"
  webconf_set "$f" SOURCE_URL 'https://example.org/src'
  webconf_set "$f" SCREENSCRAPER_USER bob
  [ "$(stat -c %a "$f")" = 600 ]
  [ "$(grep -c '^SOURCE_URL=' "$f")" -eq 1 ]
  [ "$(webconf_get "$f" SOURCE_URL)" = 'https://example.org/src' ]
  [ "$(webconf_get "$f" SCREENSCRAPER_USER)" = bob ]
  [ "$(webconf_get "$f" LISTEN)" = '0.0.0.0:11944' ]
  [ "$(sed -n 1p "$f")" = WEB_USER=a ]
}

@test "webconf_set: values are literal (quotes, equals signs, dollar, backticks, backslashes)" {
  load_webconf
  local f="$BATS_TEST_TMPDIR/web.conf"
  : >"$f"
  webconf_set "$f" SCREENSCRAPER_PASSWORD '"p=w" $(id) `x` \n %s'
  [ "$(webconf_get "$f" SCREENSCRAPER_PASSWORD)" = '"p=w" $(id) `x` \n %s' ]
  grep -qxF 'SCREENSCRAPER_PASSWORD="p=w" $(id) `x` \n %s' "$f"
}

@test "webconf_set: an empty value is written; a line break or a bad key is refused" {
  load_webconf
  local f="$BATS_TEST_TMPDIR/web.conf"
  : >"$f"
  webconf_set "$f" SOURCE_URL ''
  grep -qx 'SOURCE_URL=' "$f"
  run webconf_set "$f" X $'a\nb'
  [ "$status" -ne 0 ]
  run webconf_set "$f" 'bad key' v
  [ "$status" -ne 0 ]
  run webconf_set "$BATS_TEST_TMPDIR/missing.conf" X v
  [ "$status" -ne 0 ]
}

@test "webconf_hash_ok: accepts a bcrypt hash, refuses plain text and a missing key" {
  load_webconf
  local f="$BATS_TEST_TMPDIR/web.conf"
  printf 'WEB_PASSWORD_HASH=%s\n' '$2a$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ01234' >"$f"
  webconf_hash_ok "$f"
  printf 'WEB_PASSWORD_HASH=secret\n' >"$f"
  run webconf_hash_ok "$f"
  [ "$status" -ne 0 ]
  : >"$f"
  run webconf_hash_ok "$f"
  [ "$status" -ne 0 ]
}

@test "webconf_no_plain_password: finds the password, ignores WEB_USER and ScreenScraper lines; short passwords may be in the hash" {
  load_webconf
  local f="$BATS_TEST_TMPDIR/web.conf"
  printf 'WEB_USER=longsecret1\nSCREENSCRAPER_PASSWORD=longsecret1\nWEB_PASSWORD_HASH=$2a$10$xyz\n' >"$f"
  webconf_no_plain_password "$f" longsecret1
  printf 'WEB_PASSWORD_HASH=longsecret1\n' >"$f"
  run webconf_no_plain_password "$f" longsecret1
  [ "$status" -ne 0 ]
  printf 'OTHER=x-longsecret1-y\n' >"$f"
  run webconf_no_plain_password "$f" longsecret1
  [ "$status" -ne 0 ]
  printf 'WEB_PASSWORD_HASH=$2a$10$abc1def\n' >"$f"
  webconf_no_plain_password "$f" abc
  run webconf_no_plain_password "$f" '$2a$10$abc1def'
  [ "$status" -ne 0 ]
  # a short password inside another value is not a leak (only the exact value counts)
  printf 'WEB_PASSWORD_HASH=$2a$10$abc1def\nOTHER=x-abc-y\n' >"$f"
  webconf_no_plain_password "$f" abc
}

# A web.conf like the one app-30-webui.sh writes, with a bcrypt hash and a GitHub source URL.
make_full_webconf() {
  local f=$1
  : >"$f"
  chmod 600 "$f"
  webconf_set "$f" WEB_USER emulationstation
  webconf_set "$f" WEB_PASSWORD_HASH '$2a$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ01234'
  webconf_set "$f" SCREENSCRAPER_USER ''
  webconf_set "$f" SCREENSCRAPER_PASSWORD ''
  webconf_set "$f" LISTEN 0.0.0.0:11944
  webconf_set "$f" TLS_CERT /etc/emulationstation-web/tls.crt
  webconf_set "$f" TLS_KEY /etc/emulationstation-web/tls.key
  webconf_set "$f" MAX_UPLOAD_MB 2048
  webconf_set "$f" MAX_EXTRACT_MB 4096
  webconf_set "$f" MAX_ZIP_ENTRIES 20000
  webconf_set "$f" HOME_DIR /home/emulationstation
  webconf_set "$f" ES_SYSTEMS_CFG /etc/emulationstation/es_systems.cfg
  webconf_set "$f" SOURCE_URL https://github.com/alice/es-builder
}

@test "webconf_no_plain_password: key names and URL parts do not fail short passwords" {
  load_webconf
  local f="$BATS_TEST_TMPDIR/web.conf" pw
  make_full_webconf "$f"
  for pw in 0000 home alice; do
    webconf_no_plain_password "$f" "$pw"
  done
}

@test "webconf_no_plain_password: an exact value match is found, also for short passwords" {
  load_webconf
  local f="$BATS_TEST_TMPDIR/web.conf"
  make_full_webconf "$f"
  webconf_set "$f" LISTEN secretpw
  run webconf_no_plain_password "$f" secretpw
  [ "$status" -eq 1 ]
}

@test "webconf_no_plain_password: a short password that is exactly a value is found" {
  load_webconf
  local f="$BATS_TEST_TMPDIR/web.conf"
  make_full_webconf "$f"
  webconf_set "$f" LISTEN abc
  run webconf_no_plain_password "$f" abc
  [ "$status" -eq 1 ]
}

@test "webconf_no_plain_password: a long password that is part of a key name is not a leak" {
  load_webconf
  local f="$BATS_TEST_TMPDIR/web.conf"
  make_full_webconf "$f"
  # MAX_EXTRACT is part of the key MAX_EXTRACT_MB; key names are never searched
  run webconf_no_plain_password "$f" MAX_EXTRACT
  [ "$status" -eq 0 ]
}

@test "webconf_no_plain_password: a long password inside a value is found" {
  load_webconf
  local f="$BATS_TEST_TMPDIR/web.conf"
  make_full_webconf "$f"
  webconf_set "$f" HOME_DIR /x/longpassword/y
  run webconf_no_plain_password "$f" longpassword
  [ "$status" -eq 1 ]
}

@test "webconf_no_plain_password: a long password only inside the hash is not a leak" {
  load_webconf
  local f="$BATS_TEST_TMPDIR/web.conf"
  make_full_webconf "$f"
  webconf_set "$f" WEB_PASSWORD_HASH '$2a$10$abcdefghlongpasswordTUVWXYZ0123456789ABCDEFGHIJKLM'
  webconf_no_plain_password "$f" longpassword
}

@test "emulationstation-web.service: required settings, and none of the forbidden sandbox options" {
  local u="$ROOT/overlay/etc/systemd/system/emulationstation-web.service"
  grep -qx 'User=emulationstation' "$u"
  grep -qx 'ExecStart=/usr/local/bin/emulationstation-web serve --config /etc/emulationstation-web/web.conf' "$u"
  grep -qx 'StateDirectory=emulationstation-web' "$u"
  grep -qx 'Restart=on-failure' "$u"
  grep -qx 'After=network-online.target emulationstation-firstboot.service' "$u"
  grep -qx 'Wants=network-online.target' "$u"
  grep -qx 'AmbientCapabilities=' "$u"
  grep -qx 'NoNewPrivileges=no' "$u"
  grep -qx 'ProtectSystem=full' "$u"
  grep -qx 'ReadWritePaths=/etc/emulationstation-web /home/emulationstation /run/emulationstation' "$u"
  grep -qx 'WantedBy=multi-user.target' "$u"
  run ! grep -E '^(PrivateDevices|RestrictSUIDSGID|CapabilityBoundingSet)=' "$u"
}

@test "app-30-webui.sh enables the service and uses credentials with --password-stdin only" {
  local s="$ROOT/provision/common/app-30-webui.sh"
  grep -q 'systemctl enable emulationstation-web.service' "$s"
  grep -q -- '--password-stdin' "$s"
  run ! grep -E -- '--password[ =]' "$s"
}

@test "app_build_web: builds from a temporary copy; the repository's webui/ is not changed" {
  load_step
  local fake="$BATS_TEST_TMPDIR/repo"
  mkdir -p "$fake/webui/scripts" "$fake/webui/internal/web/static"
  echo repo-css >"$fake/webui/internal/web/static/app.css"
  cat >"$fake/webui/scripts/build.sh" <<'EOF2'
set -eu
cd "$(dirname "$0")/.."
echo generated >internal/web/static/app.css
echo "binary $1" >"$2"
EOF2
  ROOT=$fake
  OUT_TMP="$BATS_TEST_TMPDIR/tmp"
  app_build_web arm64 "$BATS_TEST_TMPDIR/bin"
  [ "$(cat "$BATS_TEST_TMPDIR/bin")" = "binary arm64" ]
  [ "$(cat "$fake/webui/internal/web/static/app.css")" = repo-css ]
  [ -z "$(ls -A "$OUT_TMP")" ]
}

@test "app_stage_webui_custom: copies templates/ and static/, skips .gitkeep, README.md and dot files" {
  load_step
  local src="$BATS_TEST_TMPDIR/wc" dst="$BATS_TEST_TMPDIR/out/webui-custom"
  mkdir -p "$src/templates/partials" "$src/static/img"
  echo a >"$src/templates/dashboard.html"
  echo p >"$src/templates/partials/my.html"
  echo png >"$src/static/img/logo.png"
  echo x >"$src/static/.DS_Store"
  : >"$src/.gitkeep"
  echo doc >"$src/README.md"
  app_stage_webui_custom "$src" "$dst"
  [ "$(cat "$dst/templates/dashboard.html")" = a ]
  [ "$(cat "$dst/templates/partials/my.html")" = p ]
  [ "$(cat "$dst/static/img/logo.png")" = png ]
  [ ! -e "$dst/.gitkeep" ]
  [ ! -e "$dst/README.md" ]
  [ ! -e "$dst/static/.DS_Store" ]
}

@test "app_stage_webui_custom: a missing source gives an empty folder" {
  load_step
  local dst="$BATS_TEST_TMPDIR/out/webui-custom"
  app_stage_webui_custom "$BATS_TEST_TMPDIR/none" "$dst"
  [ -d "$dst" ]
  [ -z "$(ls -A "$dst")" ]
}

@test "app_stage_webui_custom: a symlink stops the build" {
  load_step
  local src="$BATS_TEST_TMPDIR/wc"
  mkdir -p "$src/static"
  ln -s /etc/passwd "$src/static/pw.txt"
  run app_stage_webui_custom "$src" "$BATS_TEST_TMPDIR/out/webui-custom"
  [ "$status" -ne 0 ]
  [[ "$output" == *"webui-custom/static/pw.txt must be a regular file."* ]]
}

@test "app_stage_webui_custom: another top-level entry or a non-HTML template stops the build" {
  load_step
  local src="$BATS_TEST_TMPDIR/wc"
  mkdir -p "$src/other"
  run app_stage_webui_custom "$src" "$BATS_TEST_TMPDIR/out/a"
  [ "$status" -ne 0 ]
  [[ "$output" == *"webui-custom/other: only templates/ and static/ are allowed."* ]]
  rm -rf "$src/other"
  mkdir -p "$src/templates"
  echo x >"$src/templates/notes.txt"
  run app_stage_webui_custom "$src" "$BATS_TEST_TMPDIR/out/b"
  [ "$status" -ne 0 ]
  [[ "$output" == *"webui-custom/templates/notes.txt: only .html files are allowed in templates/."* ]]
}

@test "app_stage_provision: puts webui-custom/ into the provision folder" {
  load_step
  local root="$BATS_TEST_TMPDIR/root"
  mkdir -p "$root"
  CFG_USER_PASSWORD=secret
  app_stage_provision "$root" rpi4
  [ -d "$root/root/provision/webui-custom" ]
}

@test "app_build_web: custom templates are in the copy as custom/templates for Tailwind" {
  load_step
  local fake="$BATS_TEST_TMPDIR/repo"
  mkdir -p "$fake/webui/scripts" "$fake/webui/custom/templates" "$fake/webui-custom/templates/partials"
  : >"$fake/webui/custom/templates/.gitkeep"
  echo mine >"$fake/webui-custom/templates/partials/my.html"
  cat >"$fake/webui/scripts/build.sh" <<'EOF2'
set -eu
cd "$(dirname "$0")/.."
cat custom/templates/partials/my.html >"$2"
EOF2
  ROOT=$fake
  OUT_TMP="$BATS_TEST_TMPDIR/tmp"
  app_build_web arm64 "$BATS_TEST_TMPDIR/bin"
  [ "$(cat "$BATS_TEST_TMPDIR/bin")" = mine ]
  [ ! -e "$fake/webui/custom/templates/partials" ]
}
