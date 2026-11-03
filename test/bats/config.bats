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
  FIX="$ROOT/test/fixtures"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/log.sh"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/config.sh"
}

@test "reads simple values, comments and empty lines are ignored" {
  config_load "$FIX/build.conf.simple"
  [ "$CFG_HOSTNAME" = "mybox" ]
  [ "$CFG_USER_PASSWORD" = "secret" ]
}

@test "surrounding quotes are removed" {
  config_load "$FIX/build.conf.simple"
  [ "$CFG_WEB_USER" = "webadmin" ]
  [ "$CFG_WIFI_SSID" = "My Wi-Fi" ]
}

@test "defaults apply when keys are missing" {
  config_load "$FIX/build.conf.simple"
  [ "$CFG_LOCALE" = "de_DE.UTF-8" ]
  [ "$CFG_TIMEZONE" = "Europe/Berlin" ]
  [ "$CFG_KEYMAP" = "de" ]
  [ "$CFG_WEB_PASSWORD" = "zocken" ]
  [ "$CFG_WIFI_COUNTRY" = "DE" ]
  [ -z "$CFG_USER_SSH_PUBKEY" ]
}

@test "CRLF is removed" {
  config_load "$FIX/build.conf.crlf"
  [ "$CFG_HOSTNAME" = "winbox" ]
  [ "$CFG_USER_PASSWORD" = "pw" ]
  [ "$CFG_TIMEZONE" = "UTC" ]
}

@test "malicious code in a value is taken literally, never executed" {
  rm -f /tmp/pwned-emulationstation-test
  config_load "$FIX/build.conf.evil" 2>/dev/null
  [ "$CFG_WIFI_PASSWORD" = 'a"b$(touch /tmp/pwned-emulationstation-test)`touch /tmp/pwned-emulationstation-test`' ]
  [ ! -e /tmp/pwned-emulationstation-test ]
}

@test "unknown keys: warning on stderr, ignored" {
  run bash -c ". '$ROOT/builder/lib/log.sh'; . '$ROOT/builder/lib/config.sh'; config_load '$FIX/build.conf.evil' 2>&1 >/dev/null"
  [ "$status" -eq 0 ]
  [[ "$output" == *NOT_A_KEY* ]]
  [[ "$output" == *lowercase* ]]
}

@test "missing file aborts" {
  run config_load "$FIX/does-not-exist"
  [ "$status" -ne 0 ]
}

@test "config_require without USER_PASSWORD: exit != 0 and 'USER_PASSWORD is missing'" {
  config_load "$FIX/build.conf.nopw"
  run config_require USER_PASSWORD
  [ "$status" -ne 0 ]
  [[ "$output" == *"USER_PASSWORD is missing"* ]]
}

@test "config_require with a set value succeeds" {
  config_load "$FIX/build.conf.simple"
  run config_require USER_PASSWORD HOSTNAME
  [ "$status" -eq 0 ]
}

@test "SOURCE_URL: default is the upstream repository, empty value allowed" {
  config_load "$FIX/build.conf.simple"
  [ "$CFG_SOURCE_URL" = "https://github.com/mkloubert/es-builder" ]
  printf 'SOURCE_URL=\n' >"$BATS_TEST_TMPDIR/empty.conf"
  config_load "$BATS_TEST_TMPDIR/empty.conf"
  [ -z "$CFG_SOURCE_URL" ]
  printf 'SOURCE_URL=https://example.org/src\n' >"$BATS_TEST_TMPDIR/own.conf"
  config_load "$BATS_TEST_TMPDIR/own.conf"
  [ "$CFG_SOURCE_URL" = "https://example.org/src" ]
}

@test "config_load: ES_AUTOSTART defaults to no" {
  config_load "$FIX/build.conf.simple"
  [ "$CFG_ES_AUTOSTART" = no ]
}

@test "config_load: ES_AUTOSTART yes, no and \"yes\" are accepted" {
  local f="$BATS_TEST_TMPDIR/c" v
  for v in yes no '"yes"'; do
    printf 'USER_PASSWORD=x\nES_AUTOSTART=%s\n' "$v" >"$f"
    config_load "$f"
    [ "$CFG_ES_AUTOSTART" = "${v//\"/}" ]
  done
}

@test "config_load: ES_AUTOSTART Yes, 1 or empty -> error with the value" {
  local f="$BATS_TEST_TMPDIR/c"
  printf 'ES_AUTOSTART=Yes\n' >"$f"
  run config_load "$f"
  [ "$status" -eq 1 ]
  [[ "$output" == *"ES_AUTOSTART must be yes or no (found: Yes)"* ]]
  printf 'ES_AUTOSTART=1\n' >"$f"
  run config_load "$f"
  [ "$status" -eq 1 ]
  printf 'ES_AUTOSTART=\n' >"$f"
  run config_load "$f"
  [ "$status" -eq 1 ]
  [[ "$output" == *"(found: )"* ]]
}

# --- Web UI self-update keys (spec 2026-11-02-webui-self-update-design.md 3.1) ---

@test "config_load: WEBUI_UPDATE_URL defaults to the upstream repository, the file keys are empty" {
  config_load "$FIX/build.conf.simple"
  [ "$CFG_WEBUI_UPDATE_URL" = https://github.com/mkloubert/es-builder ]
  [ -z "$CFG_WEBUI_UPDATE_SIGNERS" ]
  [ -z "$CFG_WEBUI_UPDATE_SSH_KEY" ]
  [ -z "$CFG_WEBUI_UPDATE_KNOWN_HOSTS" ]
}

@test "config_load: an empty WEBUI_UPDATE_URL is allowed (self-update off)" {
  local f="$BATS_TEST_TMPDIR/c"
  printf 'WEBUI_UPDATE_URL=\n' >"$f"
  config_load "$f"
  [ -z "$CFG_WEBUI_UPDATE_URL" ]
}

# The same rows as TestCheckURL in webui/internal/update/url_test.go.
@test "config_check_update_url: accepts the good URLs of the Go check" {
  local u
  for u in https://github.com/mkloubert/es-builder https://git.example:8443/a/b.git \
    ssh://git@host:2222/a/b.git ssh://host/a/b.git git@github.com:a/b.git file:///mnt/repo \
    /media/usb/es-builder /media/usb/es-builder/; do
    config_check_update_url "$u" || { echo "should pass: $u" >&2; return 1; }
  done
  local base=https://h/
  config_check_update_url "$base$(printf 'a%.0s' $(seq 1 $((512 - ${#base}))))"
}

@test "config_check_update_url: refuses the bad URLs of the Go check" {
  local u
  local bad=("" http://h/x HTTPS://h/x git://h/x "ext::sh -c x" ext::x -oProxyCommand=x --upload-pack=x
    https://u:p@h/x https://u@h/x "https://h/x?y" "https://h/x#y" https://h https://h:port/x https://h_x/x
    ssh://git:pw@host/a "ssh://git@host/a?x" "ssh://git@host/a#x" ssh://-oProxyCommand=x@host/a ssh://host
    ssh://-oProxyCommand/a ssh://-oX@host/a git@-oX:a https://-h/x /mnt/../etc /mnt/./x /mnt//x /
    relative/path //h/x file://h/x file:///mnt/../x file:///mnt/%2e%2e/x file://relative file:////x
    git@h:../x git@h:a/../x git@h: git@h:/abs git@h:-x -u@h:x @h:x git@:x "a b@h:x" "https://h/a b"
    $'https://h/a\nb' $'https://h/a\tb' $'https://h/a\x7fb' 'https://h/a\b' $'\n' /mnt// https://h:/x
    "https://github.com/o/repo(1)" "https://h/päth" "https://h/a!b" "https://h/a*b" "https://h/x'y"
    "https://h/a[b]" "https://h/a%20b" "ssh://us%41er@h/x")
  for u in "${bad[@]}"; do
    if config_check_update_url "$u"; then echo "should fail: $u" >&2; return 1; fi
  done
  local base=https://h/
  ! config_check_update_url "$base$(printf 'a%.0s' $(seq 1 $((513 - ${#base}))))"
}

@test "config_load: a bad WEBUI_UPDATE_URL stops with a clear message" {
  local f="$BATS_TEST_TMPDIR/c"
  printf 'WEBUI_UPDATE_URL=ext::sh -c x\n' >"$f"
  run config_load "$f"
  [ "$status" -eq 1 ]
  [[ "$output" == *"WEBUI_UPDATE_URL is not allowed"* ]]
}

# update_repo : a fake repository root with update-keys/ files, for CONFIG_ROOT
update_repo() {
  CONFIG_ROOT="$BATS_TEST_TMPDIR/repo"
  export CONFIG_ROOT
  mkdir -p "$CONFIG_ROOT/update-keys" "$CONFIG_ROOT/secrets" "$CONFIG_ROOT/out" "$CONFIG_ROOT/dir"
  printf 'x\n' >"$CONFIG_ROOT/update-keys/signers"
  printf 'x\n' >"$CONFIG_ROOT/update-keys/key"
  printf 'x\n' >"$CONFIG_ROOT/update-keys/known_hosts"
  printf 'x\n' >"$CONFIG_ROOT/secrets/ca.crt"
  printf 'x\n' >"$CONFIG_ROOT/out/f"
  printf 'x\n' >"$BATS_TEST_TMPDIR/outside"
  ln -s signers "$CONFIG_ROOT/update-keys/link"
  ln -s ../update-keys "$CONFIG_ROOT/dirlink"
}

@test "config_load: the update files are read and checked (relative to the repository)" {
  update_repo
  local f="$BATS_TEST_TMPDIR/c"
  printf 'WEBUI_UPDATE_SIGNERS=update-keys/signers\nWEBUI_UPDATE_SSH_KEY=update-keys/key\nWEBUI_UPDATE_KNOWN_HOSTS=update-keys/known_hosts\n' >"$f"
  config_load "$f"
  [ "$CFG_WEBUI_UPDATE_SIGNERS" = update-keys/signers ]
  [ "$CFG_WEBUI_UPDATE_SSH_KEY" = update-keys/key ]
  [ "$CFG_WEBUI_UPDATE_KNOWN_HOSTS" = update-keys/known_hosts ]
}

@test "config_load: WEBUI_UPDATE_SSH_KEY without WEBUI_UPDATE_KNOWN_HOSTS fails" {
  update_repo
  local f="$BATS_TEST_TMPDIR/c"
  printf 'WEBUI_UPDATE_SSH_KEY=update-keys/key\n' >"$f"
  run config_load "$f"
  [ "$status" -eq 1 ]
  [[ "$output" == *"WEBUI_UPDATE_SSH_KEY needs WEBUI_UPDATE_KNOWN_HOSTS"* ]]
}

@test "config_load: update file paths in secrets/ or out/, outside the repo, symlinks, folders and missing files fail" {
  update_repo
  local f="$BATS_TEST_TMPDIR/c" p
  for p in secrets/ca.crt out/f "$BATS_TEST_TMPDIR/outside" ../outside update-keys/link dirlink/signers dir \
    update-keys/missing update-keys/../secrets/ca.crt; do
    printf 'WEBUI_UPDATE_SIGNERS=%s\n' "$p" >"$f"
    run config_load "$f"
    if [ "$status" -ne 1 ]; then echo "should fail: $p" >&2; return 1; fi
  done
  # an absolute path is refused, also one inside the repository (the container sees /work)
  printf 'WEBUI_UPDATE_SIGNERS=%s\n' "$CONFIG_ROOT/update-keys/signers" >"$f"
  run config_load "$f"
  [ "$status" -eq 1 ]
  [[ "$output" == *"relative to the repository"* ]]
}
