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

# Static check of the Raspberry Pi image (spec 8), read only.
#   test/rpi4-static.sh [--stage base|app] [--report FILE] <image.img|image.img.xz>
#   test/rpi4-static.sh [--stage base|app] [--report FILE] --root DIRECTORY
# --stage base: base layer. --stage app: also the app layer (web service, controllers,
#   and test/lib/static-app.sh); app always includes base.
# --report: module report; a missing module with a report line counts as PASS (spec 9).
#   Default for /cache/base-rpi4-<key>.img: /cache/report-rpi4-<key>.txt.
# --root: already unpacked root directory (boot under boot/firmware), without mounting.
# Without root rights (and without --root) the script restarts itself in the builder container
# (privileged, because of loop devices). This host part must run under Bash 3.2 (macOS).
# Output: PASS/FAIL per check; exit 1 on at least one FAIL, 2 on usage errors.
# The predicates below are called indirectly through check():
# shellcheck disable=SC2317
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)

usage() {
  echo "Usage: test/rpi4-static.sh [--stage base|app] [--report FILE] <image.img[.xz]> | --root DIR" >&2
  exit 2
}

STAGE=base
REPORT=""
IMG=""
RDIR=""
while [ $# -gt 0 ]; do
  case "$1" in
    --stage)
      [ $# -ge 2 ] || usage
      STAGE=$2
      shift
      ;;
    --report)
      [ $# -ge 2 ] || usage
      REPORT=$2
      shift
      ;;
    --root)
      [ $# -ge 2 ] || usage
      RDIR=$2
      shift
      ;;
    -h | --help) usage ;;
    -*) usage ;;
    *)
      [ -z "$IMG" ] || usage
      IMG=$1
      ;;
  esac
  shift
done
case "$STAGE" in
  base | app) ;;
  *) usage ;;
esac
if [ -z "$IMG" ] && [ -z "$RDIR" ]; then usage; fi

# --- Host: restart in the builder container --------------------------------------

# container_path <path> : existing file in the repo -> /work/...; otherwise unchanged (e.g. /cache/...)
container_path() {
  local p=$1 abs
  if [ -e "$p" ]; then
    abs=$(cd "$(dirname "$p")" && pwd)/$(basename "$p")
    case "$abs" in
      "$ROOT"/*) printf '/work/%s\n' "${abs#"$ROOT"/}" ;;
      *)
        echo "File $p is not in the repo ($ROOT); only the repo (/work) and /cache are visible in the container." >&2
        exit 2
        ;;
    esac
  else
    printf '%s\n' "$p"
  fi
}

if [ -z "$RDIR" ] && [ "$(id -u)" -ne 0 ]; then
  set -- --stage "$STAGE"
  if [ -n "$REPORT" ]; then set -- "$@" --report "$(container_path "$REPORT")"; fi
  set -- "$@" "$(container_path "$IMG")"
  cd "$ROOT"
  exec docker run --rm --privileged -v emulationstation-cache:/cache -v "$PWD:/work" -w /work \
    emulationstation-builder test/rpi4-static.sh "$@"
fi

# --- Checks (run against $R) ---------------------------------------------

FAILS=0
pass() { printf 'PASS: %s\n' "$1"; }
fail() {
  printf 'FAIL: %s\n' "$1"
  FAILS=$((FAILS + 1))
}
# check <description> <command...>
check() {
  local desc=$1
  shift
  if "$@"; then pass "$desc"; else fail "$desc"; fi
}

passwd_field() { awk -F: -v u="$1" -v f="$2" '$1 == u { print $f; found = 1 } END { exit !found }' "$R/etc/passwd"; }
user_exists() { passwd_field "$1" 1 >/dev/null 2>&1; }
user_absent() { ! user_exists "$1"; }
user_ok() {
  [ "$(passwd_field emulationstation 6 2>/dev/null)" = /home/emulationstation ] &&
    [ "$(passwd_field emulationstation 7 2>/dev/null)" = /bin/bash ] &&
    [ -d "$R/home/emulationstation" ]
}
in_group() {
  awk -F: -v g="$1" -v u="$2" '$1 == g { n = split($4, m, ","); for (i = 1; i <= n; i++) if (m[i] == u) ok = 1 }
    END { exit !ok }' "$R/etc/group"
}
root_locked() {
  local pw
  pw=$(awk -F: '$1 == "root" { print $2 }' "$R/etc/shadow")
  case "$pw" in "!"* | "*"*) return 0 ;; esac
  return 1
}
is_masked() { [ "$(readlink "$R/etc/systemd/system/$1" 2>/dev/null || true)" = /dev/null ]; }
is_wanted() { [ -L "$R/etc/systemd/system/$1.wants/$2" ] || [ -e "$R/etc/systemd/system/$1.wants/$2" ]; }
has_line() { grep -qxF "$2" "$R/$1" 2>/dev/null; }
no_match() { ! grep -qE "$2" "$R/$1" 2>/dev/null; }
has_word() { grep -qE "(^|[[:space:]])$2([[:space:]]|\$)" "$R/$1" 2>/dev/null; }
cloud_init_off() { [ ! -d "$R/etc/cloud" ] || [ -e "$R/etc/cloud/cloud-init.disabled" ]; }
rpi_swap_off() {
  [ ! -f "$R/etc/rpi/swap.conf" ] || grep -qx 'Mechanism=none' "$R"/etc/rpi/swap.conf.d/*.conf 2>/dev/null
}
in_report() { [ -n "$REPORT" ] && [ -f "$REPORT" ] && grep -q "^$1 " "$REPORT"; }
# xpadneo line for this kernel ("-k <ver>" or "kernel <ver> skipped") or without kernel reference
# (e.g. sources cannot be downloaded)
xpadneo_reported() {
  [ -n "$REPORT" ] && [ -f "$REPORT" ] &&
    awk -v k=" -k $1 " -v s=" kernel $1 skipped" '/^xpadneo / {
        l = $0 " "
        if ((index(l, " -k ") == 0 && index(l, " skipped") == 0) || index(l, k) > 0 || index(l, s) > 0) ok = 1
      }
      END { exit !ok }' "$REPORT"
}

checks_base() {
  local g m k d mods=0 kernels=0

  check "User emulationstation (home, bash)" user_ok
  for g in sudo video render input audio bluetooth; do
    check "User emulationstation in group $g" in_group "$g" emulationstation
  done
  check "User pi removed" user_absent pi
  check "User rpi-first-boot-wizard removed" user_absent rpi-first-boot-wizard
  check "root password locked" root_locked
  check "userconfig.service masked" is_masked userconfig.service
  check "getty@tty1 enabled" is_wanted getty.target getty@tty1.service
  check "no sshd banner from rename-user" test ! -e "$R/etc/ssh/sshd_config.d/rename_user.conf"

  check "config.txt: dtoverlay=vc4-kms-v3d" has_line boot/firmware/config.txt dtoverlay=vc4-kms-v3d
  check "config.txt: disable_overscan=1" has_line boot/firmware/config.txt disable_overscan=1
  check "config.txt: no disable-bt" no_match boot/firmware/config.txt '^[[:space:]]*dtoverlay=(pi3-)?disable-bt'
  check "cmdline.txt: resize (root grows at first boot)" has_word boot/firmware/cmdline.txt resize
  check "rpi-resize.service enabled" is_wanted sysinit.target rpi-resize.service
  check "cloud-init disabled" cloud_init_off
  check "zramswap: ALGO=zstd" has_line etc/default/zramswap ALGO=zstd
  check "zramswap: PERCENT=50" has_line etc/default/zramswap PERCENT=50
  check "rpi-swap switched off (Mechanism=none)" rpi_swap_off

  check "RetroPie-Setup in /home/emulationstation/RetroPie-Setup" test -f "$R/home/emulationstation/RetroPie-Setup/.git/HEAD"
  check "EmulationStation: /opt/retropie/supplementary/emulationstation/emulationstation" \
    test -x "$R/opt/retropie/supplementary/emulationstation/emulationstation"
  while IFS= read -r m || [ -n "$m" ]; do
    m=${m%%#*}
    m=$(printf '%s' "$m" | tr -d '[:space:]')
    [ -n "$m" ] || continue
    mods=$((mods + 1))
    if [ -d "$R/opt/retropie/libretrocores/$m" ] || [ -d "$R/opt/retropie/emulators/$m" ]; then
      pass "Module $m installed"
    elif in_report "$m"; then
      pass "Module $m is missing, listed in the report: $(grep "^$m " "$REPORT" | head -n1)"
    else
      fail "Module $m: neither under /opt/retropie/{libretrocores,emulators} nor in the report"
    fi
  done <"$ROOT/provision/rpi4/modules.list"
  check "modules.list is not empty" test "$mods" -gt 0

  for d in "$R"/lib/modules/*/; do
    [ -d "$d/kernel" ] || continue
    k=$(basename "$d")
    kernels=$((kernels + 1))
    if [ -n "$(find "$d" -name 'hid-xpadneo.ko*' -print -quit 2>/dev/null)" ]; then
      pass "xpadneo for kernel $k"
    elif xpadneo_reported "$k"; then
      pass "xpadneo for kernel $k is missing, listed in the report"
    else
      fail "xpadneo for kernel $k: no hid-xpadneo.ko and no report entry"
    fi
  done
  check "at least one kernel under /lib/modules" test "$kernels" -gt 0
}

# App layer: Tasks 8, 10, 12 add their checks here.
# --- Web service (Task 8) ---
# web_arch_ok <file command output pattern> : the web binary has the expected CPU
web_arch_ok() { file -b "$R/usr/local/bin/emulationstation-web" 2>/dev/null | grep -q "$1"; }
# owned_by_es <path> <mode> : owner and group emulationstation (numbers from the image's passwd), given mode
owned_by_es() {
  local uid gid
  uid=$(passwd_field emulationstation 3) && gid=$(passwd_field emulationstation 4) || return 1
  [ "$(stat -c '%u:%g %a' "$R/$1" 2>/dev/null)" = "$uid:$gid $2" ]
}
web_conf_key() { grep -q "^$1=" "$R/etc/emulationstation-web/web.conf" 2>/dev/null; }
web_hash_ok() { webconf_hash_ok "$R/etc/emulationstation-web/web.conf"; }
web_no_plain() { webconf_no_plain_password "$R/etc/emulationstation-web/web.conf" "$1"; }
cert_has_san() {
  openssl x509 -in "$R/etc/emulationstation-web/tls/server.crt" -noout -ext subjectAltName 2>/dev/null | grep -q "DNS:$1"
}
# Only tls/server.key may hold a private key under /etc/emulationstation-web; the CA key is nowhere.
no_stray_keys() {
  local f
  while IFS= read -r f; do
    [ "$f" = "$R/etc/emulationstation-web/tls/server.key" ] && continue
    grep -q 'PRIVATE KEY' "$f" 2>/dev/null && return 1
  done < <(find "$R/etc/emulationstation-web" -type f 2>/dev/null)
  return 0
}
no_ca_key() { [ -z "$(find "$R/etc" "$R/root" "$R/home" "$R/usr/local" "$R/var/lib" -name 'ca.key' -print -quit 2>/dev/null)" ]; }

checks_web() {
  local expect_arch=$1 host=emulationstation webpw="" cfg=${BUILD_CONFIG:-$ROOT/build.conf}
  # shellcheck source=builder/lib/log.sh
  . "$ROOT/builder/lib/log.sh"
  # shellcheck source=builder/lib/config.sh
  . "$ROOT/builder/lib/config.sh"
  # shellcheck source=builder/lib/webconf.sh
  . "$ROOT/builder/lib/webconf.sh"
  if [ -f "$cfg" ] && (config_load "$cfg") >/dev/null 2>&1; then
    config_load "$cfg"
    host=$CFG_HOSTNAME
    webpw=$CFG_WEB_PASSWORD
  fi

  check "web binary: $expect_arch" web_arch_ok "$expect_arch"
  check "web binary: mode 755" test "$(stat -c %a "$R/usr/local/bin/emulationstation-web" 2>/dev/null)" = 755
  check "emulationstation-web.service enabled" is_wanted multi-user.target emulationstation-web.service
  check "/etc/emulationstation-web: emulationstation:emulationstation 750" owned_by_es etc/emulationstation-web 750
  check "/etc/emulationstation-web/tls: emulationstation:emulationstation 750" owned_by_es etc/emulationstation-web/tls 750
  check "web.conf: emulationstation:emulationstation 600" owned_by_es etc/emulationstation-web/web.conf 600
  check "web.conf: WEB_USER present" web_conf_key WEB_USER
  check "web.conf: SOURCE_URL present" web_conf_key SOURCE_URL
  check "web.conf: WEB_PASSWORD_HASH looks like bcrypt" web_hash_ok
  if [ -n "$webpw" ]; then
    check "web.conf: no plain-text web password" web_no_plain "$webpw"
  else
    echo "SKIP: web.conf: no plain-text web password (no build.conf with WEB_PASSWORD found)"
  fi
  check "server.key: emulationstation:emulationstation 600" owned_by_es etc/emulationstation-web/tls/server.key 600
  check "server.crt: emulationstation:emulationstation 644" owned_by_es etc/emulationstation-web/tls/server.crt 644
  check "server.crt: subjectAltName has DNS:$host" cert_has_san "$host"
  check "no other private key under /etc/emulationstation-web" no_stray_keys
  check "no ca.key in the image" no_ca_key
}

# --- Controllers and Bluetooth (Task 10) ---
# root_path <path> : <path> inside the image; a symlink with an absolute target (RetroPie links
# ~/.emulationstation and retroarch-joypads this way) is followed inside $R, one level.
root_path() {
  local t
  if t=$(readlink "$R$1" 2>/dev/null); then
    case "$t" in
      /*) printf '%s\n' "$R$t" ;;
      *) printf '%s/%s\n' "$(dirname "$R$1")" "$t" ;;
    esac
  else
    printf '%s\n' "$R$1"
  fi
}
# ctl_files : the controller files of the repo (templates and custom), as app-50-controllers.sh reads them
ctl_files() {
  local f
  for f in "$ROOT"/controllers/templates/*.xml "$ROOT"/controllers/custom/*.xml; do
    if [ -f "$f" ]; then printf '%s\n' "$f"; fi
  done
}
# ctl_attr <attribute> : the attribute of every joystick node of the controller files, sorted, unique
ctl_attr() {
  local f
  ctl_files | while IFS= read -r f; do
    grep '<inputConfig' "$f" | grep 'type="joystick"' | sed -n "s/.*$1=\"\\([^\"]*\\)\".*/\\1/p"
  done | LC_ALL=C sort -u
}
es_input_cfg() { printf '%s/es_input.cfg\n' "$(root_path /home/emulationstation/.emulationstation)"; }
es_has_guids() {
  local cfg g n=0
  cfg=$(es_input_cfg)
  [ -f "$cfg" ] || return 1
  for g in $(ctl_attr deviceGUID); do
    n=$((n + 1))
    grep -qi "deviceGUID=\"$g\"" "$cfg" || return 1
  done
  [ "$n" -gt 0 ]
}
es_no_keyboard() {
  local cfg
  cfg=$(es_input_cfg)
  [ -f "$cfg" ] && ! grep -q 'type="keyboard"' "$cfg"
}
# joypads_ok : one RetroArch autoconfig per device name (file name rule of RetroPie's retroarch.sh)
joypads_ok() {
  local dir name n=0
  dir=$(root_path /opt/retropie/configs/all/retroarch-joypads)
  while IFS= read -r name; do
    n=$((n + 1))
    [ -f "$dir/${name//[:><?\"\/\\|*]/}.cfg" ] || return 1
  done < <(ctl_attr deviceName)
  [ "$n" -gt 0 ]
}

checks_controllers() {
  check "es_input.cfg has all template GUIDs" es_has_guids
  check "es_input.cfg has no keyboard node" es_no_keyboard
  check "retroarch-joypads has one file per template device name" joypads_ok
  check "bluetooth.service enabled" is_wanted bluetooth.target bluetooth.service
}

# Shared app-layer checks (SSH, firewall, fail2ban, theme, units, first boot files, leftovers)
BOOT=boot/firmware
# shellcheck source=test/lib/static-app.sh
. "$ROOT/test/lib/static-app.sh"

checks_app() {
  checks_web "ARM aarch64"
  checks_controllers
  checks_security
  checks_units
  checks_firstboot
  checks_usb
}

run_checks() {
  checks_base
  if [ "$STAGE" = app ]; then checks_app; fi
  if [ "$FAILS" -gt 0 ]; then
    echo "$FAILS check(s) failed."
    return 1
  fi
  echo "All checks passed (stage $STAGE)."
}

# Default report next to a base cache image
default_report() {
  local name dir
  name=$(basename "${1%.xz}")
  dir=$(dirname "$1")
  case "$name" in
    base-rpi4-*.img)
      name=${name#base-rpi4-}
      printf '%s/report-rpi4-%s.txt\n' "$dir" "${name%.img}"
      ;;
  esac
}

if [ -n "$RDIR" ]; then
  [ -d "$RDIR" ] || {
    echo "Directory is missing: $RDIR" >&2
    exit 2
  }
  R=$RDIR
  run_checks
  exit $?
fi

# --- Mount the image (root, builder container) --------------------------------
# shellcheck source=builder/lib/log.sh
. "$ROOT/builder/lib/log.sh"
TARGET_MNT=${STATIC_MNT:-/mnt/rpi4-static}
# shellcheck source=builder/lib/image.sh
. "$ROOT/builder/lib/image.sh"
cleanup_install

[ -f "$IMG" ] || die "Image not found: $IMG" 2
[ -n "$REPORT" ] || REPORT=$(default_report "$IMG")
if [ -n "$REPORT" ] && [ ! -f "$REPORT" ]; then
  log_warn "Report $REPORT is missing; missing modules count as FAIL"
fi
case "$IMG" in
  *.xz)
    tmpd=$(mktemp -d)
    cleanup_push rm -rf "$tmpd"
    log_info "Unpacking $IMG ..."
    xz -dc "$IMG" >"$tmpd/image.img"
    IMG=$tmpd/image.img
    ;;
esac
mount_target "$IMG" rpi4
# read only
mount -o remount,ro "$TARGET_MNT/boot/firmware"
mount -o remount,ro "$TARGET_MNT"
R=$TARGET_MNT
rc=0
run_checks || rc=$?
exit "$rc"
