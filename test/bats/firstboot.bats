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

# First boot (Task 9): overlay/usr/local/lib/emulationstation/firstboot with a fake root, stubs
# for all system commands, and provision/common/app-40-firstboot.sh. The real boot happens on
# the device. The input file is hostile: nothing from it may be run.
# shellcheck disable=SC2016,SC2030,SC2031,SC2034,SC2119,SC2120,SC2154

bats_require_minimum_version 1.5.0

setup() {
  REPO="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  FB="$REPO/overlay/usr/local/lib/emulationstation/firstboot"
  T="$BATS_TEST_TMPDIR"
  FR="$T/root"
  BOOT="$T/boot"
  STUBS="$T/stubs"
  CALLS="$T/calls"
  mkdir -p "$FR/etc/ssh" "$FR/etc/default" "$FR/etc/emulationstation-web" \
    "$FR/usr/share/zoneinfo/Europe" "$FR/var/lib" "$BOOT" "$STUBS"
  : >"$FR/usr/share/zoneinfo/Europe/Berlin"
  printf 'WEB_USER=zocken\nWEB_PASSWORD_HASH=x\n' >"$FR/etc/emulationstation-web/web.conf"
  printf 'oldname\n' >"$FR/etc/hostname"
  printf '127.0.0.1\tlocalhost\n127.0.1.1\toldname\n' >"$FR/etc/hosts"
  : >"$CALLS"
  export CALLS T
  # Every stub logs "name args" to $CALLS and its stdin (only a pipe) to $T/<name>.stdin.
  local c
  for c in chpasswd ew wificountry sshkeygen growpart resize2fs reboot setupcon hostname chown rfkill; do
    cat >"$STUBS/$c" <<STUB
#!/usr/bin/env bash
echo "$c \$*" >>"\$CALLS"
if [ -p /dev/stdin ]; then d=\$(cat); [ -z "\$d" ] || printf '%s\\n' "\$d" >>"\$T/$c.stdin"; fi
[ ! -f "\$T/$c.fail" ] || exit 1
exit 0
STUB
    chmod +x "$STUBS/$c"
  done
  # the ssh-keygen stub also creates a key file, like the real -A does
  cat >"$STUBS/sshkeygen" <<'STUB'
#!/usr/bin/env bash
echo "sshkeygen $*" >>"$CALLS"
touch "$FAKE_ROOT/etc/ssh/ssh_host_ed25519_key"
STUB
  chmod +x "$STUBS/sshkeygen"
  cat >"$STUBS/findmnt" <<'STUB'
#!/usr/bin/env bash
echo /dev/vda2
STUB
  cat >"$STUBS/lsblk" <<'STUB'
#!/usr/bin/env bash
echo vda
STUB
  chmod +x "$STUBS/findmnt" "$STUBS/lsblk"
  mkdir -p "$T/sys/vda2"
  echo 2 >"$T/sys/vda2/partition"
  # a hostile file must not be able to run "reboot" through PATH either
  PATH="$STUBS:$PATH"
  export FAKE_ROOT="$FR"
}

# run_fb [VAR=value ...] : runs the script with the fake root and the stubs
run_fb() {
  run env ROOT="$FR" BOOT_DIR="$BOOT" TARGET_KIND="${KIND:-rpi4}" \
    CHPASSWD="$STUBS/chpasswd" EW_BIN="$STUBS/ew" WIFI_COUNTRY_CMD="$STUBS/wificountry" \
    SSH_KEYGEN="$STUBS/sshkeygen" GROWPART="$STUBS/growpart" RESIZE2FS="$STUBS/resize2fs" \
    FINDMNT="$STUBS/findmnt" LSBLK="$STUBS/lsblk" SYS_BLOCK="$T/sys" \
    CHOWN="$STUBS/chown" SETUPCON="$STUBS/setupcon" HOSTNAME_CMD="$STUBS/hostname" \
    RFKILL="$STUBS/rfkill" "$@" bash "$FB"
}

# no <command...> : the command must fail (a plain ! does not fail a Bats test)
no() { if "$@"; then return 1; fi; }

# put_file <text> : writes the input file exactly as given (printf format)
# shellcheck disable=SC2059
put_file() { printf "$1" >"$BOOT/emulationstation.txt"; }

nm_file() { echo "$FR/etc/NetworkManager/system-connections/emulationstation-wifi.nmconnection"; }

# glib_value <file> <key> : the value as GLib's key file parser reads a string (strip, unescape)
glib_value() {
  python3 -I - "$1" "$2" <<'PY'
import sys
path, key = sys.argv[1], sys.argv[2]
for line in open(path, encoding="utf-8", errors="surrogateescape").read().split("\n"):
    if line.startswith(key + "="):
        v = line[len(key) + 1:].strip(" \t")
        out, i = "", 0
        while i < len(v):
            c = v[i]
            if c == "\\":
                i += 1
                c = {"s": " ", "n": "\n", "t": "\t", "r": "\r", "\\": "\\"}.get(v[i], "\\" + v[i])
            out += c
            i += 1
        sys.stdout.write(out)
        break
PY
}

# --- Step 1 ---------------------------------------------------------------------

@test "firstboot: without a file only step 1 runs and the exit code is 0" {
  run_fb
  [ "$status" -eq 0 ]
  grep -q '^sshkeygen' "$CALLS"
  no grep -qE '^(chpasswd|ew|wificountry|growpart|resize2fs) ' "$CALLS"
  [ ! -e "$(nm_file)" ]
  [ "$(cat "$FR/etc/hostname")" = oldname ]
}

@test "firstboot: ssh host keys are only made when there are none" {
  touch "$FR/etc/ssh/ssh_host_rsa_key"
  run_fb
  [ "$status" -eq 0 ]
  no grep -q '^sshkeygen' "$CALLS"
}

@test "firstboot: vm grows the root partition once (growpart, resize2fs, marker)" {
  KIND=vm run_fb
  [ "$status" -eq 0 ]
  grep -qx 'growpart /dev/vda 2' "$CALLS"
  grep -qx 'resize2fs /dev/vda2' "$CALLS"
  [ -f "$FR/var/lib/emulationstation/grown" ]
  : >"$CALLS"
  KIND=vm run_fb
  [ "$status" -eq 0 ]
  no grep -qE '^(growpart|resize2fs) ' "$CALLS"
}

@test "firstboot: growpart exit 1 with NOCHANGE is success" {
  cat >"$STUBS/growpart" <<'STUB'
#!/usr/bin/env bash
echo "growpart $*" >>"$CALLS"
echo "NOCHANGE: partition 2 is size 100. it cannot be grown"
exit 1
STUB
  KIND=vm run_fb
  [ "$status" -eq 0 ]
  grep -q '^resize2fs' "$CALLS"
  [ -f "$FR/var/lib/emulationstation/grown" ]
}

@test "firstboot: a real growpart failure leaves no marker, logs, and exits 1" {
  touch "$T/growpart.fail"
  KIND=vm run_fb
  [ "$status" -eq 1 ]
  [[ $output == *"growpart failed"* ]]
  [ ! -e "$FR/var/lib/emulationstation/grown" ]
}

@test "firstboot: rpi4 never calls growpart" {
  KIND=rpi4 run_fb
  no grep -qE '^(growpart|resize2fs) ' "$CALLS"
}

# --- Parsing --------------------------------------------------------------------

@test "firstboot: host name is set in /etc/hostname and /etc/hosts" {
  put_file 'HOSTNAME=retro-box\n'
  run_fb
  [ "$status" -eq 0 ]
  [ "$(cat "$FR/etc/hostname")" = retro-box ]
  [ "$(grep -c '^127\.0\.1\.1' "$FR/etc/hosts")" -eq 1 ]
  grep -qxP '127\.0\.1\.1\tretro-box' "$FR/etc/hosts"
  grep -q '^127.0.0.1' "$FR/etc/hosts"
}

@test "firstboot: invalid host names are ignored and logged" {
  local bad
  for bad in '../x' '-abc' 'Abc' 'a_b' 'a b' "$(printf 'a%.0s' {1..64})"; do
    put_file "HOSTNAME=$bad\n"
    run_fb
    [ "$status" -eq 0 ]
    [ "$(cat "$FR/etc/hostname")" = oldname ]
    [[ $output == *"HOSTNAME"*"ignored"* ]]
  done
}

@test "firstboot: Wi-Fi values stay literal; nothing from the file is run" {
  put_file 'WIFI_SSID=Home\nWIFI_PASSWORD=a"b$(reboot)`reboot`\n'
  run_fb
  [ "$status" -eq 0 ]
  [ "$(stat -c %a "$(nm_file)")" = 600 ]
  [ "$(glib_value "$(nm_file)" psk)" = 'a"b$(reboot)`reboot`' ]
  [ "$(glib_value "$(nm_file)" ssid)" = Home ]
  no grep -q '^reboot' "$CALLS"
  grep -qx 'id=emulationstation-wifi' "$(nm_file)"
  grep -qx 'type=wifi' "$(nm_file)"
  grep -qx 'autoconnect=true' "$(nm_file)"
  grep -qx 'key-mgmt=wpa-psk' "$(nm_file)"
  grep -A1 '^\[ipv4\]' "$(nm_file)" | grep -qx 'method=auto'
  grep -A1 '^\[ipv6\]' "$(nm_file)" | grep -qx 'method=auto'
}

@test "firstboot: GLib key file escaping for backslash, spaces, semicolon and hash" {
  put_file 'WIFI_SSID=" lead;x#y\\ "\nWIFI_PASSWORD="  p;a#s\\s\\\\ w  "\n'
  run_fb
  [ "$status" -eq 0 ]
  [ "$(glib_value "$(nm_file)" ssid)" = ' lead;x#y\ ' ]
  [ "$(glib_value "$(nm_file)" psk)" = '  p;a#s\s\\ w  ' ]
  # no raw leading or trailing space in the file (GLib would strip it)
  no grep -qE '^(ssid|psk)=( |.* $)' "$(nm_file)"
}

@test "firstboot: an SSID of only digits and semicolons is written as a byte list" {
  put_file 'WIFI_SSID=12;34\nWIFI_PASSWORD=password123\n'
  run_fb
  [ "$status" -eq 0 ]
  grep -qx 'ssid=49;50;59;51;52;' "$(nm_file)"
}

@test "firstboot: Wi-Fi password length rules (8 to 63, or 64 hex)" {
  put_file 'WIFI_SSID=Home\nWIFI_PASSWORD=short\n'
  run_fb
  [ ! -e "$(nm_file)" ]
  [[ $output == *"WIFI_PASSWORD"* ]]
  put_file "WIFI_SSID=Home\nWIFI_PASSWORD=$(printf 'a%.0s' {1..64})\n"
  run_fb
  [ -e "$(nm_file)" ]
  rm -f "$(nm_file)"
  put_file "WIFI_SSID=Home\nWIFI_PASSWORD=$(printf 'g%.0s' {1..64})\n"
  run_fb
  [ ! -e "$(nm_file)" ]
  put_file "WIFI_SSID=Home\nWIFI_PASSWORD=$(printf 'g%.0s' {1..63})\n"
  run_fb
  [ -e "$(nm_file)" ]
}

@test "firstboot: an SSID longer than 32 bytes, or a missing password, skips Wi-Fi" {
  put_file "WIFI_SSID=$(printf 's%.0s' {1..33})\nWIFI_PASSWORD=password123\n"
  run_fb
  [ ! -e "$(nm_file)" ]
  put_file 'WIFI_SSID=Home\n'
  run_fb
  [ ! -e "$(nm_file)" ]
}

@test "firstboot: an empty WIFI_SSID removes the file; an absent key keeps it" {
  put_file 'WIFI_SSID=Home\nWIFI_PASSWORD=password123\n'
  run_fb
  [ -e "$(nm_file)" ]
  put_file 'HOSTNAME=other\n'
  run_fb
  [ -e "$(nm_file)" ]
  put_file 'WIFI_SSID=\n'
  run_fb
  [ "$status" -eq 0 ]
  [ ! -e "$(nm_file)" ]
}

@test "firstboot: CRLF line ends and a BOM are handled" {
  printf '\xef\xbb\xbfHOSTNAME=crlf-box\r\nWIFI_SSID=Home\r\nWIFI_PASSWORD=password123\r\n' >"$BOOT/emulationstation.txt"
  run_fb
  [ "$status" -eq 0 ]
  [ "$(cat "$FR/etc/hostname")" = crlf-box ]
  [ "$(glib_value "$(nm_file)" ssid)" = Home ]
  [ "$(glib_value "$(nm_file)" psk)" = password123 ]
}

@test "firstboot: spaces around the key are removed; a pair of quotes around the value is removed" {
  put_file '  HOSTNAME  =  "quoted"\n'
  run_fb
  # the value keeps its own leading spaces, so it is not a valid host name
  [ "$(cat "$FR/etc/hostname")" = oldname ]
  put_file '  HOSTNAME  ="quoted"\n'
  run_fb
  [ "$(cat "$FR/etc/hostname")" = quoted ]
}

@test "firstboot: comments, empty lines and lines without = are skipped; the last duplicate wins" {
  put_file '# comment\n\nnonsense\nHOSTNAME=first\nHOSTNAME=second\n'
  run_fb
  [ "$status" -eq 0 ]
  [ "$(cat "$FR/etc/hostname")" = second ]
}

@test "firstboot: a control character in a value ignores that key only (TAB is allowed)" {
  put_file 'HOSTNAME=good\nWIFI_SSID=Ho\x01me\nWIFI_PASSWORD=password123\nUSER_PASSWORD=ab\x1bcd\nKEYMAP=de\n'
  run_fb
  [ "$status" -eq 0 ]
  [[ $output == *"WIFI_SSID"*"control character"* ]]
  [[ $output == *"USER_PASSWORD"*"control character"* ]]
  [ "$(cat "$FR/etc/hostname")" = good ]
  [ ! -e "$(nm_file)" ]
  no grep -q '^chpasswd' "$CALLS"
  put_file 'WIFI_SSID=a\tb\nWIFI_PASSWORD=password123\n'
  run_fb
  [ -e "$(nm_file)" ]
  [ "$(glib_value "$(nm_file)" ssid)" = "$(printf 'a\tb')" ]
}

@test "firstboot: an unknown key is logged and the rest is applied" {
  put_file 'FOO=bar\nHOSTNAME=known\n'
  run_fb
  [ "$status" -eq 0 ]
  [[ $output == *"unknown key FOO ignored"* ]]
  [ "$(cat "$FR/etc/hostname")" = known ]
}

@test "firstboot: a file over 64 KiB is ignored" {
  head -c 70000 /dev/zero | tr '\0' 'a' >"$BOOT/emulationstation.txt"
  run_fb
  [ "$status" -eq 0 ]
  [[ $output == *"too large"* ]]
  [ -e "$BOOT/emulationstation.txt" ]
}

# --- The .applied copy -------------------------------------------------------------

@test "firstboot: .applied masks every PASSWORD key; the original is gone" {
  put_file '# my settings\nHOSTNAME=box\nWIFI_SSID=Home\nWIFI_PASSWORD=secretwifi1\nUSER_PASSWORD=secretuser\nWEB_PASSWORD=secretweb\nmy_password = secretx\n'
  run_fb
  [ "$status" -eq 0 ]
  [ ! -e "$BOOT/emulationstation.txt" ]
  local a="$BOOT/emulationstation.txt.applied"
  [ -f "$a" ]
  grep -qx 'WIFI_PASSWORD=\*\*\*' "$a"
  grep -qx 'USER_PASSWORD=\*\*\*' "$a"
  grep -qx 'WEB_PASSWORD=\*\*\*' "$a"
  grep -qx 'my_password=\*\*\*' "$a"
  grep -qx 'HOSTNAME=box' "$a"
  grep -qx '# my settings' "$a"
  no grep -q secret "$a"
  [[ $output != *secret* ]]
}

@test "firstboot: a line without = is not copied into .applied" {
  put_file 'hunter2hunter2\nHOSTNAME=box\n'
  run_fb
  no grep -q hunter2 "$BOOT/emulationstation.txt.applied"
}

@test "firstboot: commented-out passwords are masked in .applied" {
  put_file '#WIFI_PASSWORD=secret1\n  # USER_PASSWORD = secret2\n#   web_password=secret6\n# a normal comment\n#HOSTNAME=box\n'
  run_fb
  local a="$BOOT/emulationstation.txt.applied"
  no grep -q secret "$a"
  grep -qx '#WIFI_PASSWORD=\*\*\*' "$a"
  grep -qx '#USER_PASSWORD=\*\*\*' "$a"
  grep -qx '#web_password=\*\*\*' "$a"
  grep -qx '# a normal comment' "$a"
  grep -qx '#HOSTNAME=box' "$a"
}

@test "firstboot: other comments that mention a password are removed from .applied" {
  put_file '# WIFI_PASSWORD: SECRETX\n#HOSTNAME=a WIFI_PASSWORD=SECRETY\n# New Password for the Linux user\n# a normal comment\n#HOSTNAME=box\n'
  run_fb
  local a="$BOOT/emulationstation.txt.applied"
  no grep -q SECRET "$a"
  no grep -qi 'new password' "$a"
  [ "$(grep -cx '# (comment removed: it mentions a password)' "$a")" -eq 3 ]
  grep -qx '# a normal comment' "$a"
  grep -qx '#HOSTNAME=box' "$a"
}

@test "firstboot: values of unknown or invalid keys and lines without a key are not copied" {
  put_file 'WIFI_PASWORD=secret3\nfoo=secret4\n=secret5\n  =secret7\nBad Key=secret8\ngarbage secret9\n'
  run_fb
  [ "$status" -eq 0 ]
  local a="$BOOT/emulationstation.txt.applied"
  no grep -q secret "$a"
  grep -qx 'WIFI_PASWORD=\*\*\* (unknown key)' "$a"
  [[ $output != *secret* ]]
}

@test "firstboot: a failed step still renames the file, lists the reason, and exits 1" {
  touch "$T/chpasswd.fail"
  put_file 'USER_PASSWORD=secretuser\nHOSTNAME=box\n'
  run_fb
  [ "$status" -eq 1 ]
  [ ! -e "$BOOT/emulationstation.txt" ]
  grep -q '^# NOT APPLIED: USER_PASSWORD' "$BOOT/emulationstation.txt.applied"
  no grep -q secretuser "$BOOT/emulationstation.txt.applied"
  [ "$(cat "$FR/etc/hostname")" = box ]
}

@test "firstboot: an old .applied file is replaced" {
  echo old >"$BOOT/emulationstation.txt.applied"
  put_file 'HOSTNAME=box\n'
  run_fb
  grep -qx 'HOSTNAME=box' "$BOOT/emulationstation.txt.applied"
}

# --- Other settings ---------------------------------------------------------------

@test "firstboot: WIFI_COUNTRY goes to the command; bad codes are ignored" {
  put_file 'WIFI_COUNTRY=US\n'
  run_fb
  grep -qx 'wificountry US' "$CALLS"
  : >"$CALLS"
  local bad
  for bad in us USA 'U1' '$(reboot)'; do
    put_file "WIFI_COUNTRY=$bad\n"
    run_fb
    no grep -q '^wificountry' "$CALLS"
  done
}

@test "firstboot: Wi-Fi without WIFI_COUNTRY on the Pi uses the build's country and unblocks wlan" {
  mkdir -p "$FR/usr/local/lib/emulationstation"
  printf 'TARGET_KIND=rpi4\nBOOT_DIR=%s\nWIFI_COUNTRY=DE\n' "$BOOT" >"$FR/usr/local/lib/emulationstation/firstboot.conf"
  put_file 'WIFI_SSID=Home\nWIFI_PASSWORD=password123\n'
  run_fb
  [ "$status" -eq 0 ]
  grep -qx 'wificountry DE' "$CALLS"
  grep -qx 'rfkill unblock wlan' "$CALLS"
  [ -f "$(nm_file)" ]
}

@test "firstboot: Wi-Fi without any country on the Pi still unblocks wlan and leaves a note" {
  put_file 'WIFI_SSID=Home\nWIFI_PASSWORD=password123\n'
  run_fb
  [ "$status" -eq 0 ]
  no grep -q '^wificountry' "$CALLS"
  grep -qx 'rfkill unblock wlan' "$CALLS"
  [[ $output == *"no Wi-Fi country is set"* ]]
}

@test "firstboot: the file's WIFI_COUNTRY wins over the build's; no Wi-Fi or the vm -> no default" {
  put_file 'WIFI_SSID=Home\nWIFI_PASSWORD=password123\nWIFI_COUNTRY=US\n'
  run_fb BUILD_WIFI_COUNTRY=DE
  grep -qx 'wificountry US' "$CALLS"
  no grep -qx 'wificountry DE' "$CALLS"
  : >"$CALLS"
  # Wi-Fi not written (bad password): no default country, no unblock
  put_file 'WIFI_SSID=Home\nWIFI_PASSWORD=short\n'
  run_fb BUILD_WIFI_COUNTRY=DE
  no grep -q '^wificountry' "$CALLS"
  no grep -q '^rfkill' "$CALLS"
  put_file 'WIFI_SSID=Home\nWIFI_PASSWORD=password123\n'
  KIND=vm run_fb BUILD_WIFI_COUNTRY=DE
  no grep -q '^wificountry' "$CALLS"
  no grep -q '^rfkill' "$CALLS"
  # a bad country in firstboot.conf is not used
  put_file 'WIFI_SSID=Home\nWIFI_PASSWORD=password123\n'
  run_fb 'BUILD_WIFI_COUNTRY=$(reboot)'
  no grep -q '^wificountry' "$CALLS"
  grep -qx 'rfkill unblock wlan' "$CALLS"
}

@test "firstboot: WIFI_COUNTRY is ignored on the vm" {
  put_file 'WIFI_COUNTRY=US\n'
  KIND=vm run_fb
  no grep -q '^wificountry' "$CALLS"
}

@test "firstboot: TIMEZONE needs an existing zone file and no .." {
  put_file 'TIMEZONE=Europe/Berlin\n'
  run_fb
  [ "$(readlink "$FR/etc/localtime")" = /usr/share/zoneinfo/Europe/Berlin ]
  [ "$(cat "$FR/etc/timezone")" = Europe/Berlin ]
  rm -f "$FR/etc/localtime" "$FR/etc/timezone"
  local bad
  for bad in Europe/Nowhere ../../etc/passwd Europe/../Europe/Berlin /etc/passwd; do
    put_file "TIMEZONE=$bad\n"
    run_fb
    [ ! -e "$FR/etc/localtime" ]
    [ ! -e "$FR/etc/timezone" ]
  done
}

@test "firstboot: KEYMAP sets XKBLAYOUT and runs setupcon; bad values are ignored" {
  printf 'XKBMODEL="pc105"\nXKBLAYOUT="gb"\n' >"$FR/etc/default/keyboard"
  put_file 'KEYMAP=de\n'
  run_fb
  grep -qx 'XKBLAYOUT="de"' "$FR/etc/default/keyboard"
  grep -qx 'XKBMODEL="pc105"' "$FR/etc/default/keyboard"
  grep -q '^setupcon' "$CALLS"
  put_file 'KEYMAP=d";rm\n'
  run_fb
  grep -qx 'XKBLAYOUT="de"' "$FR/etc/default/keyboard"
}

@test "firstboot: KEYMAP is added when the keyboard file has no XKBLAYOUT" {
  put_file 'KEYMAP=us\n'
  run_fb
  grep -qx 'XKBLAYOUT="us"' "$FR/etc/default/keyboard"
}

@test "firstboot: USER_PASSWORD goes to chpasswd through stdin only" {
  put_file 'USER_PASSWORD=pa:ss word\n'
  run_fb
  [ "$status" -eq 0 ]
  grep -qx 'chpasswd ' "$CALLS"
  [ "$(cat "$T/chpasswd.stdin")" = 'emulationstation:pa:ss word' ]
  no grep -q 'pa:ss' "$CALLS"
  [[ $output != *"pa:ss"* ]]
}

@test "firstboot: WEB_USER and WEB_PASSWORD call credentials; password only through stdin" {
  put_file 'WEB_USER=alice\nWEB_PASSWORD=webpw123\n'
  run_fb
  [ "$status" -eq 0 ]
  grep -qx "ew credentials --config $FR/etc/emulationstation-web/web.conf --user=alice --password-stdin" "$CALLS"
  [ "$(cat "$T/ew.stdin")" = webpw123 ]
  no grep -q webpw123 "$CALLS"
  grep -qx "chown emulationstation:emulationstation $FR/etc/emulationstation-web/web.conf" "$CALLS"
  [ "$(stat -c %a "$FR/etc/emulationstation-web/web.conf")" = 600 ]
}

@test "firstboot: WEB_USER alone and WEB_PASSWORD alone are each one credentials call" {
  put_file 'WEB_USER=bob\n'
  run_fb
  grep -qx "ew credentials --config $FR/etc/emulationstation-web/web.conf --user=bob" "$CALLS"
  [ ! -e "$T/ew.stdin" ]
  : >"$CALLS"
  put_file 'WEB_PASSWORD=onlypw123\n'
  run_fb
  grep -qx "ew credentials --config $FR/etc/emulationstation-web/web.conf --password-stdin" "$CALLS"
  [ "$(cat "$T/ew.stdin")" = onlypw123 ]
}

@test "firstboot: a web user starting with - is passed safely" {
  put_file 'WEB_USER=-evil\n'
  run_fb
  grep -q -- '--user=-evil$' "$CALLS"
}

@test "firstboot: web.conf is fixed to owner and 0600 even when credentials fails" {
  touch "$T/ew.fail"
  chmod 644 "$FR/etc/emulationstation-web/web.conf"
  put_file 'WEB_USER=bob\n'
  run_fb
  [ "$status" -eq 1 ]
  [ "$(stat -c %a "$FR/etc/emulationstation-web/web.conf")" = 600 ]
  grep -q '^chown emulationstation:emulationstation' "$CALLS"
}

# --- TLS warning -------------------------------------------------------------------

make_cert() {
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 2 -subj "/CN=$1" \
    -addext "subjectAltName=$2" -keyout "$T/k.pem" -out "$T/c.pem" 2>/dev/null
  mkdir -p "$FR/etc/emulationstation-web/tls"
  cp "$T/c.pem" "$FR/etc/emulationstation-web/tls/server.crt"
}

@test "firstboot: a host name outside the certificate SANs gives a clear warning" {
  command -v openssl >/dev/null || skip "openssl missing (runs in the builder container)"
  make_cert emulationstation 'DNS:emulationstation,DNS:emulationstation.local'
  put_file 'HOSTNAME=newname\n'
  run_fb
  [ "$status" -eq 0 ]
  [ "$(cat "$FR/etc/hostname")" = newname ]
  [[ $output == *"WARNING"*"newname"*"certificate"* ]]
}

@test "firstboot: no warning when the name and name.local are in the SANs" {
  command -v openssl >/dev/null || skip "openssl missing (runs in the builder container)"
  make_cert emulationstation 'DNS:newname,DNS:newname.local'
  put_file 'HOSTNAME=newname\n'
  run_fb
  [[ $output != *WARNING* ]]
}

@test "firstboot: a name without .local in the SANs warns about the .local name" {
  command -v openssl >/dev/null || skip "openssl missing (runs in the builder container)"
  make_cert emulationstation 'DNS:newname'
  put_file 'HOSTNAME=newname\n'
  run_fb
  [[ $output == *"WARNING"*"newname.local"* ]]
}

# --- Provision script and unit -----------------------------------------------------

@test "unit: runs at every boot, ordered before network, web and ssh" {
  local u="$REPO/overlay/etc/systemd/system/emulationstation-firstboot.service"
  grep -qx 'Type=oneshot' "$u"
  grep -qx 'Before=network-pre.target emulationstation-web.service ssh.service' "$u"
  grep -qx 'Wants=network-pre.target' "$u"
  grep -qx 'After=local-fs.target regenerate_ssh_host_keys.service' "$u"
  grep -qx 'WantedBy=multi-user.target' "$u"
  no grep -q '^ConditionPathExists' "$u"
  no grep -q '^DefaultDependencies=no' "$u"
}

@test "app-40-firstboot.sh: installs script, config, drop-in, example, and enables the unit" {
  local R="$T/img"
  mkdir -p "$R/usr/local/lib/emulationstation" "$R/etc/systemd/system" "$R/boot-template" "$R/boot/efi"
  cp "$FB" "$R/usr/local/lib/emulationstation/firstboot"
  chmod 644 "$R/usr/local/lib/emulationstation/firstboot"
  cp "$REPO/overlay/etc/systemd/system/emulationstation-firstboot.service" "$R/etc/systemd/system/"
  cp "$REPO/overlay/boot-template/emulationstation.txt.example" "$R/boot-template/"
  cat >"$STUBS/systemctl" <<'STUB'
#!/usr/bin/env bash
echo "systemctl $*" >>"$CALLS"
STUB
  chmod +x "$STUBS/systemctl"
  run env PROVISION_ROOT="$R" CFG_TARGET_KIND=vm CHOWN="$STUBS/chown" bash "$REPO/provision/common/app-40-firstboot.sh"
  [ "$status" -eq 0 ]
  [ "$(stat -c %a "$R/usr/local/lib/emulationstation/firstboot")" = 755 ]
  grep -qx 'TARGET_KIND=vm' "$R/usr/local/lib/emulationstation/firstboot.conf"
  grep -qx 'BOOT_DIR=/boot/efi' "$R/usr/local/lib/emulationstation/firstboot.conf"
  grep -qx 'RequiresMountsFor=/boot/efi' "$R/etc/systemd/system/emulationstation-firstboot.service.d/boot-dir.conf"
  [ -f "$R/boot/efi/emulationstation.txt.example" ]
  [ ! -e "$R/boot-template" ]
  grep -q 'systemctl enable emulationstation-firstboot.service' "$CALLS"
}

@test "app-40-firstboot.sh: rpi4 uses /boot/firmware; an unknown kind fails" {
  local R="$T/img"
  mkdir -p "$R/usr/local/lib/emulationstation" "$R/etc/systemd/system" "$R/boot-template" "$R/boot/firmware"
  cp "$FB" "$R/usr/local/lib/emulationstation/firstboot"
  cp "$REPO/overlay/etc/systemd/system/emulationstation-firstboot.service" "$R/etc/systemd/system/"
  cp "$REPO/overlay/boot-template/emulationstation.txt.example" "$R/boot-template/"
  printf '#!/usr/bin/env bash\n' >"$STUBS/systemctl"; chmod +x "$STUBS/systemctl"
  run env PROVISION_ROOT="$R" CFG_TARGET_KIND=rpi4 bash "$REPO/provision/common/app-40-firstboot.sh"
  [ "$status" -eq 0 ]
  grep -qx 'BOOT_DIR=/boot/firmware' "$R/usr/local/lib/emulationstation/firstboot.conf"
  run env PROVISION_ROOT="$R" CFG_TARGET_KIND=other bash "$REPO/provision/common/app-40-firstboot.sh"
  [ "$status" -ne 0 ]
}

@test "firstboot: reads TARGET_KIND and BOOT_DIR from firstboot.conf when not overridden" {
  mkdir -p "$FR/usr/local/lib/emulationstation"
  printf 'TARGET_KIND=vm\nBOOT_DIR=%s\n' "$BOOT" >"$FR/usr/local/lib/emulationstation/firstboot.conf"
  put_file 'HOSTNAME=fromconf\n'
  run env ROOT="$FR" CHPASSWD="$STUBS/chpasswd" EW_BIN="$STUBS/ew" SSH_KEYGEN="$STUBS/sshkeygen" \
    GROWPART="$STUBS/growpart" RESIZE2FS="$STUBS/resize2fs" FINDMNT="$STUBS/findmnt" LSBLK="$STUBS/lsblk" \
    SYS_BLOCK="$T/sys" CHOWN="$STUBS/chown" bash "$FB"
  [ "$status" -eq 0 ]
  [ "$(cat "$FR/etc/hostname")" = fromconf ]
  grep -q '^growpart' "$CALLS"
}
