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

# Base layer (Task 3): 30-provision-base.sh with stubs, provision scripts with PROVISION_ROOT,
# test/rpi4-static.sh against rebuilt root directories. Real image builds: builder container.
# shellcheck disable=SC2016,SC2030,SC2031,SC2034,SC2086

bats_require_minimum_version 1.5.0

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  FIX="$ROOT/test/fixtures"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/log.sh"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/config.sh"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/image.sh"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/cache.sh"
  export CACHE_DIR="$BATS_TEST_TMPDIR/cache"
  mkdir -p "$CACHE_DIR"
  STUBS="$BATS_TEST_TMPDIR/stubs"
  CALLS="$BATS_TEST_TMPDIR/calls"
  mkdir -p "$STUBS"
  : >"$CALLS"
  export CALLS
}

MODULES="lr-fceumm lr-snes9x lr-gambatte lr-mgba lr-beetle-vb lr-gw lr-genesis-plus-gx lr-picodrive \
lr-beetle-pce-fast lr-beetle-supergrafx lr-fbneo lr-mame2003-plus lr-beetle-ngp lr-beetle-wswan \
lr-stella2014 lr-atari800 lr-prosystem lr-handy hatari vice amiberry lr-caprice32 lr-fuse lr-bluemsx \
linapple lr-px68k jzintv lr-vecx dosbox scummvm lr-pcsx-rearmed lr-flycast ppsspp mupen64plus \
lr-mupen64plus-next"

# --- Content ------------------------------------------------------------------

@test "modules.list: exactly the emulators from spec 5.2, one per line, in order" {
  [ "$(grep -Ev '^(#|$)' "$ROOT/provision/rpi4/modules.list")" = "$(printf '%s\n' $MODULES)" ]
}

@test "Provision scripts: shebang, set -euo pipefail, bash -n" {
  local f n=0
  for f in "$ROOT"/provision/common/base-*.sh "$ROOT"/provision/common/app-05-identity.sh "$ROOT"/provision/rpi4/base-*.sh; do
    [ "$(head -n1 "$f")" = '#!/usr/bin/env bash' ]
    grep -qx 'set -euo pipefail' "$f"
    bash -n "$f"
    n=$((n + 1))
  done
  [ "$n" -eq 6 ]
}

# --- 30-provision-base.sh ----------------------------------------------------

write_conf() {
  cat >"$BATS_TEST_TMPDIR/build.conf" <<EOF
USER_PASSWORD=secret-not-in-the-base
RPI_IMAGE_URL=file:///nowhere/pi.img.xz
RPI_IMAGE_CHECKSUM=sha256:$(printf 'a%.0s' {1..64})
RETROPIE_SETUP_REF=r1
XPADNEO_REF=x1
EOF
  export BUILD_CONFIG="$BATS_TEST_TMPDIR/build.conf"
  config_load "$BUILD_CONFIG"
  BASE=$(REPO_ROOT="$ROOT" cache_base_path rpi4)
  KEY=$(REPO_ROOT="$ROOT" cache_key rpi4)
  REPORT="$CACHE_DIR/report-rpi4-$KEY.txt"
}

# losetup/mount/umount/sfdisk as in image.bats; chroot logs script + environment.
# CHROOT_FAIL=<script> makes this script fail; base-30-retropie.sh writes a report line.
make_step_stubs() {
  cat >"$STUBS/losetup" <<'EOF'
#!/usr/bin/env bash
echo "losetup $*" >>"$CALLS"
case "$1" in
  -f)
    n=$(cat "$CALLS.loopnr" 2>/dev/null || echo 0)
    echo $((n + 1)) >"$CALLS.loopnr"
    echo "/dev/loop$n"
    ;;
esac
EOF
  printf '#!/usr/bin/env bash\necho "mount $*" >>"$CALLS"\n' >"$STUBS/mount"
  printf '#!/usr/bin/env bash\necho "umount $*" >>"$CALLS"\n' >"$STUBS/umount"
  printf '#!/usr/bin/env bash\ncat "%s"\n' "$FIX/sfdisk-rpi.json" >"$STUBS/sfdisk"
  cat >"$STUBS/chroot" <<'EOF'
#!/usr/bin/env bash
root=$1
shift
script="" envs=""
for a in "$@"; do
  case "$a" in
    /root/provision/*.sh) script=$a ;;
    CFG_*=* | PROVISION_*=*) envs="$envs $a" ;;
  esac
done
[ -f "$root$script" ] || { echo "chroot: $script is missing" >>"$CALLS"; exit 99; }
[ -f "$root/root/provision/rpi4/modules.list" ] || { echo "chroot: modules.list is missing" >>"$CALLS"; exit 98; }
echo "chroot $script$envs" >>"$CALLS"
case "$script" in
  */base-30-retropie.sh) echo "lr-px68k 1 Installation failed" >>"$root/root/provision/report.txt" ;;
esac
[ "${CHROOT_FAIL:-}" != "${script##*/}" ] || exit 7
exit 0
EOF
  chmod +x "$STUBS"/*
  PATH="$STUBS:$PATH"
}

@test "30-provision-base.sh: existing base -> 'Base layer from cache', nothing mounted" {
  make_step_stubs
  write_conf
  : >"$BASE"
  echo "old" >"$REPORT"
  run bash "$ROOT/builder/steps/30-provision-base.sh" rpi4
  [ "$status" -eq 0 ]
  [[ "$output" == *"Base layer from cache"* ]]
  [ ! -s "$CALLS" ]
  [ "$(cat "$REPORT")" = old ]
}

@test "30-provision-base.sh: without prepared .tmp -> abort with hint to step 20" {
  make_step_stubs
  write_conf
  run bash "$ROOT/builder/steps/30-provision-base.sh" rpi4
  [ "$status" -ne 0 ]
  [[ "$output" == *"20-prepare-image"* ]]
  [ ! -e "$BASE" ]
}

@test "30-provision-base.sh: scripts sorted (common, then rpi4), pins as environment, report, teardown, commit" {
  make_step_stubs
  write_conf
  echo image >"$BASE.tmp"
  export TARGET_MNT="$BATS_TEST_TMPDIR/mnt"
  run bash "$ROOT/builder/steps/30-provision-base.sh" rpi4
  echo "$output"
  [ "$status" -eq 0 ]
  local env=" CFG_TARGET=rpi4 CFG_TARGET_KIND=rpi4 CFG_TARGET_ARCH=arm64 CFG_RETROPIE_SETUP_REF=r1 CFG_RETROPIE_PLATFORM=rpi4 CFG_XPADNEO_REF=x1 PROVISION_DIR=/root/provision PROVISION_REPORT=/root/provision/report.txt"
  [ "$(grep '^chroot' "$CALLS")" = "$(printf '%s\n' \
    "chroot /root/provision/common/base-10-packages.sh$env" \
    "chroot /root/provision/common/base-20-user.sh$env" \
    "chroot /root/provision/common/base-30-retropie.sh$env" \
    "chroot /root/provision/common/base-40-xpadneo.sh$env" \
    "chroot /root/provision/rpi4/base-10-system.sh$env")" ]
  [ "$(grep -c secret "$CALLS")" -eq 0 ]
  [ "$(cat "$REPORT")" = "lr-px68k 1 Installation failed" ]
  [ ! -e "$TARGET_MNT/root/provision" ]
  [ "$(cat "$BASE")" = image ]
  [ ! -e "$BASE.tmp" ]
  grep -qx "umount $TARGET_MNT/boot/firmware" "$CALLS"
  grep -qx "umount $TARGET_MNT" "$CALLS"
  [ "$(grep -c '^losetup -d' "$CALLS")" -eq 2 ]
  [[ "$output" == *"lr-px68k"* ]]
}

@test "30-provision-base.sh: failing script -> abort, no .img, report so far, teardown" {
  make_step_stubs
  write_conf
  echo image >"$BASE.tmp"
  export TARGET_MNT="$BATS_TEST_TMPDIR/mnt"
  CHROOT_FAIL=base-30-retropie.sh run bash "$ROOT/builder/steps/30-provision-base.sh" rpi4
  [ "$status" -ne 0 ]
  [[ "$output" == *"base-30-retropie.sh"* ]]
  [ ! -e "$BASE" ]
  [ -e "$BASE.tmp" ]
  run ! grep -q base-40-xpadneo "$CALLS"
  [ "$(cat "$REPORT")" = "lr-px68k 1 Installation failed" ]
  grep -qx "umount $TARGET_MNT" "$CALLS"
  [ ! -e "$TARGET_MNT/root/provision" ]
}

@test "30-provision-base.sh: REBUILD_BASE=1 rebuilds despite an existing base" {
  make_step_stubs
  write_conf
  echo old >"$BASE"
  echo new >"$BASE.tmp"
  export TARGET_MNT="$BATS_TEST_TMPDIR/mnt"
  REBUILD_BASE=1 run bash "$ROOT/builder/steps/30-provision-base.sh" rpi4
  [ "$status" -eq 0 ]
  [ "$(cat "$BASE")" = new ]
  grep -q '^chroot' "$CALLS"
}

# --- provision/rpi4/base-10-system.sh (PROVISION_ROOT) -----------------------

make_cmd_stubs() {
  local c
  for c in "$@"; do
    printf '#!/usr/bin/env bash\necho "%s $*" >>"$CALLS"\n' "$c" >"$STUBS/$c"
  done
  chmod +x "$STUBS"/*
  PATH="$STUBS:$PATH"
}

pi_root() {
  local r=$1
  mkdir -p "$r/boot/firmware" "$r/etc/default" "$r/etc/cloud" "$r/etc/rpi"
  cat >"$r/boot/firmware/config.txt" <<'EOF'
dtparam=audio=on
dtoverlay=disable-bt
[cm4]
otg_mode=1

[all]
EOF
  echo "console=serial0,115200 console=tty1 root=PARTUUID=27b94958-02 rootfstype=ext4 fsck.repair=yes rootwait resize" \
    >"$r/boot/firmware/cmdline.txt"
  printf '# Compression algorithm selection\n#ALGO=lz4\n\n#PERCENT=50\n' >"$r/etc/default/zramswap"
  printf '[Main]\n#Mechanism=auto\n' >"$r/etc/rpi/swap.conf"
}

@test "base-10-system.sh: config.txt, zramswap, rpi-swap off, cloud-init off; idempotent" {
  make_cmd_stubs systemctl
  local r="$BATS_TEST_TMPDIR/r" snap
  pi_root "$r"
  PROVISION_ROOT="$r" run bash "$ROOT/provision/rpi4/base-10-system.sh"
  [ "$status" -eq 0 ]
  grep -qx 'dtoverlay=vc4-kms-v3d' "$r/boot/firmware/config.txt"
  grep -qx 'disable_overscan=1' "$r/boot/firmware/config.txt"
  run ! grep -q 'disable-bt' "$r/boot/firmware/config.txt"
  # new lines are in an [all] section, not under [cm4]
  [ "$(tail -n1 "$r/boot/firmware/config.txt")" != otg_mode=1 ]
  grep -qx 'ALGO=zstd' "$r/etc/default/zramswap"
  grep -qx 'PERCENT=50' "$r/etc/default/zramswap"
  [ "$(grep -c 'ALGO=' "$r/etc/default/zramswap")" -eq 1 ]
  grep -qx 'Mechanism=none' "$r"/etc/rpi/swap.conf.d/*.conf
  [ -e "$r/etc/cloud/cloud-init.disabled" ]
  grep -q 'resize' "$r/boot/firmware/cmdline.txt"
  grep -qx 'systemctl enable zramswap.service' "$CALLS"
  grep -qx 'systemctl enable rpi-resize.service' "$CALLS"

  snap=$(cd "$r" && find . -type f -exec sha256sum {} + | sort)
  PROVISION_ROOT="$r" run bash "$ROOT/provision/rpi4/base-10-system.sh"
  [ "$status" -eq 0 ]
  [ "$(cd "$r" && find . -type f -exec sha256sum {} + | sort)" = "$snap" ]
}

@test "base-10-system.sh: 'resize' missing in cmdline.txt -> abort (root would not grow)" {
  make_cmd_stubs systemctl
  local r="$BATS_TEST_TMPDIR/r"
  pi_root "$r"
  echo "console=tty1 root=PARTUUID=x rootwait" >"$r/boot/firmware/cmdline.txt"
  PROVISION_ROOT="$r" run bash "$ROOT/provision/rpi4/base-10-system.sh"
  [ "$status" -ne 0 ]
  [[ "$output" == *resize* ]]
}

# --- provision/common/base-30-retropie.sh (PROVISION_ROOT, stub RetroPie) ------

# Fake RetroPie: creates tmp/build/<m> per module; RP_FAIL modules fail (without clean),
# all others install and remove their build tree. Logs which
# build trees still exist when a module starts.
retropie_root() {
  local r=$1 rp=$1/home/emulationstation/RetroPie-Setup
  mkdir -p "$rp/.git" "$BATS_TEST_TMPDIR/prov/rpi4"
  printf '%s\n' lr-fceumm lr-px68k vice >"$BATS_TEST_TMPDIR/prov/rpi4/modules.list"
  : >"$BATS_TEST_TMPDIR/prov/report.txt"
  cat >"$rp/retropie_packages.sh" <<'EOF2'
m=${1:-}
[ -n "$m" ] || exit 0
echo "rp $m present:[$(ls tmp/build 2>/dev/null | tr '\n' ' ')]" >>"$CALLS"
mkdir -p "tmp/build/$m"
: >"tmp/build/$m/huge.o"
case " ${RP_FAIL:-} " in *" $m "*) exit 1 ;; esac
o=$PROVISION_ROOT/opt/retropie
case "$m" in
  emulationstation) mkdir -p "$o/supplementary/$m" && printf '#!/bin/sh\n' >"$o/supplementary/$m/$m" && chmod +x "$o/supplementary/$m/$m" ;;
  runcommand | skyscraper) mkdir -p "$o/supplementary/$m" ;;
  lr-*) mkdir -p "$o/libretrocores/$m" ;;
  *) mkdir -p "$o/emulators/$m" ;;
esac
rm -rf "tmp/build/$m"
EOF2
  printf '#!/usr/bin/env bash\n[ "$1 $2" = "passwd emulationstation" ] && echo "emulationstation:x:1000:1000::/home/emulationstation:/bin/bash"\n' >"$STUBS/getent"
  printf '#!/usr/bin/env bash\ncase "$*" in *"rev-parse HEAD"*) echo f5ebfba6 ;; esac\nexit 0\n' >"$STUBS/git"
  printf '#!/usr/bin/env bash\nexit 0\n' >"$STUBS/chown"
  printf '#!/usr/bin/env bash\necho "Filesystem 1024-blocks Used Available Capacity Mounted on"\necho "/dev/loop0 10000000 1 ${DF_AVAIL:-9000000} 1%% /"\n' >"$STUBS/df"
  chmod +x "$STUBS"/*
  PATH="$STUBS:$PATH"
}

run_retropie() {
  run env PROVISION_ROOT="$1" CFG_RETROPIE_SETUP_REF=f5ebfba6 CFG_RETROPIE_PLATFORM=rpi4 CFG_TARGET_KIND=rpi4 \
    PROVISION_DIR="$BATS_TEST_TMPDIR/prov" PROVISION_REPORT="$BATS_TEST_TMPDIR/prov/report.txt" \
    bash "$ROOT/provision/common/base-30-retropie.sh"
}

@test "base-30-retropie.sh: build tree of a failed module is removed at once, tmp is gone at the end, df in the log" {
  local r="$BATS_TEST_TMPDIR/r" rp
  retropie_root "$r"
  rp="$r/home/emulationstation/RetroPie-Setup"
  RP_FAIL=lr-px68k run_retropie "$r"
  echo "$output"
  [ "$status" -eq 0 ]
  [ "$(cat "$BATS_TEST_TMPDIR/prov/report.txt")" = "lr-px68k 1 installation failed, see log 30-provision-base" ]
  # when vice starts (after the failure) no build tree is left over
  grep -qx 'rp vice present:\[\]' "$CALLS"
  [ ! -e "$rp/tmp" ]
  [ -d "$r/opt/retropie/emulators/vice" ]
  [ "$(grep -c '^Filesystem' <<<"$output")" -ge 8 ]
}

@test "base-30-retropie.sh: core module fails -> abort, build tree is gone anyway" {
  local r="$BATS_TEST_TMPDIR/r" rp
  retropie_root "$r"
  rp="$r/home/emulationstation/RetroPie-Setup"
  RP_FAIL=emulationstation run_retropie "$r"
  [ "$status" -ne 0 ]
  [[ "$output" == *"Core module emulationstation failed"* ]]
  [ ! -e "$rp/tmp/build/emulationstation" ]
  grep -q '^emulationstation 1 ' "$BATS_TEST_TMPDIR/prov/report.txt"
}

@test "base-30-retropie.sh: little space -> hint in the report" {
  local r="$BATS_TEST_TMPDIR/r"
  retropie_root "$r"
  RP_FAIL=vice DF_AVAIL=1000 run_retropie "$r"
  [ "$status" -eq 0 ]
  grep -qx 'vice 1 installation failed, little space left on / (1000 KiB free), see log 30-provision-base' \
    "$BATS_TEST_TMPDIR/prov/report.txt"
}

# --- provision/common/app-05-identity.sh (PROVISION_ROOT) --------------------

identity_root() {
  local r=$1
  mkdir -p "$r/etc/default" "$r/home/emulationstation" "$r/usr/share/zoneinfo/Europe" "$r/usr/share/i18n"
  printf '# en_GB.UTF-8 UTF-8\n# de_DE.UTF-8 UTF-8\n' >"$r/etc/locale.gen"
  printf 'de_DE.UTF-8 UTF-8\nen_GB.UTF-8 UTF-8\n' >"$r/usr/share/i18n/SUPPORTED"
  echo tz >"$r/usr/share/zoneinfo/Europe/Berlin"
  printf 'XKBMODEL="pc105"\nXKBLAYOUT="gb"\nXKBVARIANT=""\n' >"$r/etc/default/keyboard"
}

# identity_env <file> [KEY=VALUE...] : build.env like step 40 writes it (values in double quotes)
identity_env() {
  local f=$1 kv
  shift
  : >"$f"
  for kv in "$@"; do printf '%s="%s"\n' "${kv%%=*}" "${kv#*=}" >>"$f"; done
}

run_identity() {
  run env PROVISION_ROOT="$R_ID" BUILD_ENV="$BATS_TEST_TMPDIR/build.env" PROVISION_LIB="$ROOT/builder/lib" \
    bash "$ROOT/provision/common/app-05-identity.sh"
}

@test "app-05-identity.sh: password only through stdin, key, host name, locale, time zone, keyboard; idempotent" {
  cat >"$STUBS/chpasswd" <<'EOF2'
#!/usr/bin/env bash
echo "chpasswd args=[$*] stdin=[$(cat)]" >>"$CALLS"
EOF2
  make_cmd_stubs chown locale-gen update-locale
  local r="$BATS_TEST_TMPDIR/r" snap
  R_ID=$r
  identity_root "$r"
  printf '127.0.0.1\tlocalhost\n127.0.1.1\told-name\n::1\tlocalhost\n' >"$r/etc/hosts"
  identity_env "$BATS_TEST_TMPDIR/build.env" USER_PASSWORD='"pa ss:word"' USER_SSH_PUBKEY='ssh-ed25519 AAAA test' \
    HOSTNAME=retro-1 LOCALE=de_DE.UTF-8 TIMEZONE=Europe/Berlin KEYMAP=de
  run_identity
  echo "$output"
  [ "$status" -eq 0 ]
  # a password that starts and ends with a double quote survives the parser
  grep -qx 'chpasswd args=\[\] stdin=\[emulationstation:"pa ss:word"\]' "$CALLS"
  [ "$(cat "$r/home/emulationstation/.ssh/authorized_keys")" = 'ssh-ed25519 AAAA test' ]
  [ "$(stat -c %a "$r/home/emulationstation/.ssh")" = 700 ]
  [ "$(stat -c %a "$r/home/emulationstation/.ssh/authorized_keys")" = 600 ]
  grep -q "chown -R emulationstation:emulationstation $r/home/emulationstation/.ssh" "$CALLS"
  [ "$(cat "$r/etc/hostname")" = retro-1 ]
  [ "$(grep -c '^127\.0\.1\.1' "$r/etc/hosts")" -eq 1 ]
  grep -qx $'127.0.1.1\tretro-1' "$r/etc/hosts"
  grep -qx $'127.0.0.1\tlocalhost' "$r/etc/hosts"
  grep -qx 'de_DE.UTF-8 UTF-8' "$r/etc/locale.gen"
  grep -qx '# en_GB.UTF-8 UTF-8' "$r/etc/locale.gen"
  grep -qx 'update-locale LANG=de_DE.UTF-8' "$CALLS"
  [ "$(readlink "$r/etc/localtime")" = /usr/share/zoneinfo/Europe/Berlin ]
  [ "$(cat "$r/etc/timezone")" = Europe/Berlin ]
  grep -qx 'XKBLAYOUT="de"' "$r/etc/default/keyboard"
  grep -qx 'XKBMODEL="pc105"' "$r/etc/default/keyboard"
  [[ "$output" != *"pa ss"* ]]

  snap=$(cd "$r" && find . \( -type f -o -type l \) -exec ls -l --time-style=+ {} + | sort; find . -type f -exec sha256sum {} + | sort)
  run_identity
  [ "$status" -eq 0 ]
  [ "$(cd "$r" && find . \( -type f -o -type l \) -exec ls -l --time-style=+ {} + | sort; find . -type f -exec sha256sum {} + | sort)" = "$snap" ]
}

@test "app-05-identity.sh: without key no authorized_keys; unknown time zone -> warning, no link" {
  printf '#!/usr/bin/env bash\ncat >/dev/null\n' >"$STUBS/chpasswd"
  make_cmd_stubs chown locale-gen update-locale
  local r="$BATS_TEST_TMPDIR/r"
  R_ID=$r
  identity_root "$r"
  identity_env "$BATS_TEST_TMPDIR/build.env" USER_PASSWORD=x USER_SSH_PUBKEY= HOSTNAME=emulationstation \
    LOCALE=de_DE.UTF-8 TIMEZONE=Mars/Olympus KEYMAP=de
  run_identity
  [ "$status" -eq 0 ]
  [ ! -e "$r/home/emulationstation/.ssh/authorized_keys" ]
  [ ! -e "$r/etc/localtime" ]
  [[ "$output" == *Mars/Olympus* ]]
}

@test "app-05-identity.sh: empty password, bad keyboard or bad host name -> abort, nothing changed" {
  printf '#!/usr/bin/env bash\ncat >/dev/null\n' >"$STUBS/chpasswd"
  make_cmd_stubs chown locale-gen update-locale
  local r="$BATS_TEST_TMPDIR/r" h
  R_ID=$r
  identity_root "$r"
  identity_env "$BATS_TEST_TMPDIR/build.env" USER_PASSWORD= LOCALE=de_DE.UTF-8 TIMEZONE=Europe/Berlin KEYMAP=de
  run_identity
  [ "$status" -ne 0 ]
  identity_env "$BATS_TEST_TMPDIR/build.env" USER_PASSWORD=x LOCALE=de_DE.UTF-8 TIMEZONE=Europe/Berlin 'KEYMAP=de"; rm -rf /'
  run_identity
  [ "$status" -ne 0 ]
  grep -qx 'XKBLAYOUT="gb"' "$r/etc/default/keyboard"
  for h in -bad bad- Bad 'a b' 'a.b' "$(printf 'a%.0s' {1..64})" 'x;id'; do
    identity_env "$BATS_TEST_TMPDIR/build.env" USER_PASSWORD=x "HOSTNAME=$h"
    run_identity
    [ "$status" -ne 0 ] || { echo "accepted: $h"; return 1; }
  done
  # only the default host name from the earlier runs, never a bad one
  [ "$(cat "$r/etc/hostname")" = emulationstation ]
}

# --- test/rpi4-static.sh --root ----------------------------------------------

# Replica of a fully provisioned base root
good_root() {
  local r=$1 m k=6.18.50+rpt-rpi-v8
  mkdir -p "$r/etc/systemd/system/getty.target.wants" "$r/etc/systemd/system/sysinit.target.wants" \
    "$r/boot/firmware" "$r/etc/default" "$r/etc/cloud" "$r/etc/rpi/swap.conf.d" \
    "$r/opt/retropie/supplementary/emulationstation" "$r/opt/retropie/libretrocores" "$r/opt/retropie/emulators" \
    "$r/home/emulationstation/RetroPie-Setup/.git" "$r/lib/modules/$k/kernel" "$r/lib/modules/$k/updates/dkms" \
    "$r/usr/src/hid-xpadneo-v0.10.5"
  printf 'root:x:0:0:root:/root:/bin/bash\nemulationstation:x:1000:1000::/home/emulationstation:/bin/bash\n' >"$r/etc/passwd"
  printf 'root:*:20000:0:99999:7:::\nemulationstation:!:20000:0:99999:7:::\n' >"$r/etc/shadow"
  printf '%s\n' root:x:0: sudo:x:27:emulationstation audio:x:29:foo,emulationstation video:x:44:emulationstation \
    input:x:105:emulationstation render:x:106:emulationstation bluetooth:x:107:emulationstation \
    emulationstation:x:1000: >"$r/etc/group"
  ln -s /dev/null "$r/etc/systemd/system/userconfig.service"
  ln -s /usr/lib/systemd/system/getty@.service "$r/etc/systemd/system/getty.target.wants/getty@tty1.service"
  ln -s /usr/lib/systemd/system/rpi-resize.service "$r/etc/systemd/system/sysinit.target.wants/rpi-resize.service"
  printf 'dtparam=audio=on\n[all]\ndtoverlay=vc4-kms-v3d\ndisable_overscan=1\n' >"$r/boot/firmware/config.txt"
  echo "console=tty1 root=PARTUUID=27b94958-02 rootfstype=ext4 rootwait resize" >"$r/boot/firmware/cmdline.txt"
  printf 'ALGO=zstd\nPERCENT=50\n' >"$r/etc/default/zramswap"
  : >"$r/etc/cloud/cloud-init.disabled"
  printf '[Main]\n' >"$r/etc/rpi/swap.conf"
  printf '[Main]\nMechanism=none\n' >"$r/etc/rpi/swap.conf.d/80-emulationstation.conf"
  printf '#!/bin/sh\n' >"$r/opt/retropie/supplementary/emulationstation/emulationstation"
  chmod +x "$r/opt/retropie/supplementary/emulationstation/emulationstation"
  echo f5ebfba638179631d4619f2b52075dec33d4bca8 >"$r/home/emulationstation/RetroPie-Setup/.git/HEAD"
  for m in $MODULES; do
    case "$m" in
      lr-*) mkdir -p "$r/opt/retropie/libretrocores/$m" ;;
      *) mkdir -p "$r/opt/retropie/emulators/$m" ;;
    esac
  done
  : >"$r/lib/modules/$k/updates/dkms/hid-xpadneo.ko.xz"
}

# Replica of the unchanged Raspberry Pi OS (user pi, no RetroPie)
pi_os_root() {
  local r=$1
  mkdir -p "$r/etc/systemd/system" "$r/boot/firmware" "$r/lib/modules/6.18.50+rpt-rpi-v8/kernel"
  printf 'root:x:0:0:root:/root:/bin/bash\npi:x:1000:1000:,,,:/home/pi:/bin/bash\n' >"$r/etc/passwd"
  printf 'root:*:20000:0:99999:7:::\npi::20000:0:99999:7:::\n' >"$r/etc/shadow"
  printf '%s\n' root:x:0: sudo:x:27:pi video:x:44:pi pi:x:1000: >"$r/etc/group"
  printf 'dtoverlay=vc4-kms-v3d\ndisable_overscan=1\n' >"$r/boot/firmware/config.txt"
  echo "console=tty1 root=PARTUUID=27b94958-02 rootwait resize" >"$r/boot/firmware/cmdline.txt"
}

@test "rpi4-static.sh --root: provisioned root -> PASS only, exit 0" {
  local r="$BATS_TEST_TMPDIR/r"
  good_root "$r"
  run bash "$ROOT/test/rpi4-static.sh" --stage base --root "$r"
  echo "$output"
  [ "$status" -eq 0 ]
  [[ "$output" == *PASS* ]]
  [[ "$output" != *FAIL* ]]
}

@test "rpi4-static.sh --root: unchanged Pi OS -> FAIL, exit != 0" {
  local r="$BATS_TEST_TMPDIR/r"
  pi_os_root "$r"
  run bash "$ROOT/test/rpi4-static.sh" --stage base --root "$r"
  echo "$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"FAIL: User emulationstation"* ]]
  [[ "$output" == *"FAIL: User pi removed"* ]]
  [[ "$output" == *"FAIL: userconfig.service masked"* ]]
  [[ "$output" == *"FAIL: EmulationStation"* ]]
}

@test "rpi4-static.sh --root: missing module -> FAIL; with report line -> PASS" {
  local r="$BATS_TEST_TMPDIR/r"
  good_root "$r"
  rm -rf "$r/opt/retropie/libretrocores/lr-px68k" "$r/lib/modules/6.18.50+rpt-rpi-v8/updates"
  run bash "$ROOT/test/rpi4-static.sh" --stage base --root "$r"
  [ "$status" -ne 0 ]
  [[ "$output" == *"FAIL: Module lr-px68k"* ]]
  [[ "$output" == *"FAIL: xpadneo"* ]]
  printf 'lr-px68k 1 Installation failed\nxpadneo 10 dkms build/install -k 6.18.50+rpt-rpi-v8 failed\n' \
    >"$BATS_TEST_TMPDIR/report.txt"
  run bash "$ROOT/test/rpi4-static.sh" --stage base --report "$BATS_TEST_TMPDIR/report.txt" --root "$r"
  echo "$output"
  [ "$status" -eq 0 ]
  [[ "$output" == *"PASS: Module lr-px68k"* ]]
  # A report line for another kernel (Pi 5) does not count for the Pi 4 kernel
  printf 'lr-px68k 1 x\nxpadneo 10 dkms build/install -k 6.18.50+rpt-rpi-2712 failed\n' >"$BATS_TEST_TMPDIR/report.txt"
  run bash "$ROOT/test/rpi4-static.sh" --stage base --report "$BATS_TEST_TMPDIR/report.txt" --root "$r"
  [ "$status" -ne 0 ]
  [[ "$output" == *"FAIL: xpadneo for kernel 6.18.50+rpt-rpi-v8"* ]]
  # An error without kernel reference (sources cannot be downloaded) applies to all kernels
  printf 'lr-px68k 1 x\nxpadneo 128 cannot download sources v0.10.5\n' >"$BATS_TEST_TMPDIR/report.txt"
  run bash "$ROOT/test/rpi4-static.sh" --stage base --report "$BATS_TEST_TMPDIR/report.txt" --root "$r"
  [ "$status" -eq 0 ]
}

@test "rpi4-static.sh --stage app includes the base checks" {
  local r="$BATS_TEST_TMPDIR/r"
  pi_os_root "$r"
  run bash "$ROOT/test/rpi4-static.sh" --stage app --root "$r"
  [ "$status" -ne 0 ]
  [[ "$output" == *"FAIL: User emulationstation"* ]]
}

@test "rpi4-static.sh without root: restarts itself in the builder container (repo paths -> /work)" {
  printf '#!/usr/bin/env bash\n[ "$1" = -u ] && echo 1000 || /usr/bin/id "$@"\n' >"$STUBS/id"
  printf '#!/usr/bin/env bash\necho "docker $*" >>"$CALLS"\n' >"$STUBS/docker"
  chmod +x "$STUBS"/*
  mkdir -p "$ROOT/out"
  local img
  img=$(mktemp "$ROOT/out/static-test-XXXXXX.img")
  PATH="$STUBS:$PATH" run bash "$ROOT/test/rpi4-static.sh" --stage base "$img"
  rm -f "$img"
  echo "$output"
  [ "$status" -eq 0 ]
  [ "$(cat "$CALLS")" = "docker run --rm --privileged -v emulationstation-cache:/cache -v $ROOT:/work -w /work emulationstation-builder test/rpi4-static.sh --stage base /work/out/${img##*/}" ]
  : >"$CALLS"
  PATH="$STUBS:$PATH" run bash "$ROOT/test/rpi4-static.sh" /cache/base-rpi4-0123456789abcdef.img
  [ "$status" -eq 0 ]
  [ "$(cat "$CALLS")" = "docker run --rm --privileged -v emulationstation-cache:/cache -v $ROOT:/work -w /work emulationstation-builder test/rpi4-static.sh --stage base /cache/base-rpi4-0123456789abcdef.img" ]
}

@test "rpi4-static.sh: unknown stage or missing image -> exit 2" {
  run bash "$ROOT/test/rpi4-static.sh" --stage foo --root /
  [ "$status" -eq 2 ]
  run bash "$ROOT/test/rpi4-static.sh" --stage base
  [ "$status" -eq 2 ]
}

# --- Raspberry Pi kernel headers (base-10-packages.sh) and xpadneo report lines (base-40) ---

V8=6.12.47+rpt-rpi-v8
P5=6.12.47+rpt-rpi-2712
V8NEW=6.12.62+rpt-rpi-v8

# kernel_stubs : dpkg-query lists the packages in $BATS_TEST_TMPDIR/installed, apt-cache has a
# candidate for the packages in $BATS_TEST_TMPDIR/available; apt-get only logs.
kernel_stubs() {
  : >"$BATS_TEST_TMPDIR/installed"
  : >"$BATS_TEST_TMPDIR/available"
  cat >"$STUBS/dpkg-query" <<'EOF2'
#!/usr/bin/env bash
p=${!#}
if grep -qxF "$p" "$BATS_TEST_TMPDIR/installed"; then echo 'install ok installed'; else exit 1; fi
EOF2
  cat >"$STUBS/apt-cache" <<'EOF2'
#!/usr/bin/env bash
echo "$2:"
if grep -qxF "$2" "$BATS_TEST_TMPDIR/available"; then echo '  Candidate: 1:1.0'; else echo '  Candidate: (none)'; fi
EOF2
  make_cmd_stubs apt-get
  chmod +x "$STUBS"/*
  export BATS_TEST_TMPDIR
}

# kroot <release>... : fake root with these kernels (kernel/ directory)
kroot() {
  local k
  KR="$BATS_TEST_TMPDIR/kr"
  rm -rf "$KR"
  for k in "$@"; do mkdir -p "$KR/lib/modules/$k/kernel"; done
  export PROVISION_ROOT=$KR
}

@test "rpi_flavour: v8 and 2712 kernels; other names -> empty" {
  . "$ROOT/provision/common/base-10-packages.sh"
  [ "$(rpi_flavour "$V8")" = rpi-v8 ]
  [ "$(rpi_flavour "$P5")" = rpi-2712 ]
  [ -z "$(rpi_flavour 6.12.113+deb13-arm64)" ]
}

@test "rpi_headers_pkgs: headers in the archive -> linux-headers-<release> for EVERY kernel (v8 and 2712)" {
  . "$ROOT/provision/common/base-10-packages.sh"
  kernel_stubs
  kroot "$V8" "$P5"
  printf '%s\n' "linux-headers-$V8" "linux-headers-$P5" >"$BATS_TEST_TMPDIR/available"
  run rpi_headers_pkgs
  [ "$status" -eq 0 ]
  [ "$(grep -v '^\[' <<<"$output")" = "$(printf '%s\n' "linux-headers-$P5" "linux-headers-$V8")" ]
}

@test "rpi_headers_pkgs: headers gone from the archive -> image and headers meta per flavour; installed headers skipped" {
  . "$ROOT/provision/common/base-10-packages.sh"
  kernel_stubs
  kroot "$V8" "$P5"
  run rpi_headers_pkgs
  [ "$(grep -v '^\[' <<<"$output")" = "$(printf '%s\n' linux-image-rpi-2712 linux-headers-rpi-2712 linux-image-rpi-v8 linux-headers-rpi-v8)" ]
  echo "linux-headers-$V8" >"$BATS_TEST_TMPDIR/installed"
  run rpi_headers_pkgs
  [ "$(grep -v '^\[' <<<"$output")" = "$(printf '%s\n' linux-image-rpi-2712 linux-headers-rpi-2712)" ]
  # two old v8 kernels without headers: the meta packages only once
  kroot "$V8" "$V8NEW"
  : >"$BATS_TEST_TMPDIR/installed"
  run rpi_headers_pkgs
  [ "$(grep -v '^\[' <<<"$output")" = "$(printf '%s\n' linux-image-rpi-v8 linux-headers-rpi-v8)" ]
}

@test "rpi_stale_releases: only a kernel without headers that has a newer one with headers; the 2712 stays" {
  . "$ROOT/provision/common/base-10-packages.sh"
  run rpi_stale_releases <<EOF2
$V8 0
$V8NEW 1
$P5 0
EOF2
  [ "$output" = "$V8" ]
  run rpi_stale_releases <<EOF2
$V8 1
$P5 0
EOF2
  [ -z "$output" ]
}

@test "rpi_remove_stale: old v8 purged only when kernel8.img is the new kernel; 2712 is never removed" {
  . "$ROOT/provision/common/base-10-packages.sh"
  kernel_stubs
  kroot "$V8" "$V8NEW" "$P5"
  mkdir -p "$KR/lib/modules/$V8NEW/build" "$KR/boot/firmware"
  : >"$KR/lib/modules/$V8NEW/build/Makefile"
  echo "linux-image-$V8" >"$BATS_TEST_TMPDIR/installed"
  echo old >"$KR/boot/firmware/kernel8.img"
  echo new >"$KR/boot/vmlinuz-$V8NEW"
  run rpi_remove_stale
  [ "$status" -eq 0 ]
  [ -d "$KR/lib/modules/$V8" ]
  [[ $output == *"Kernel $V8 stays"* ]]
  [ ! -s "$CALLS" ]
  echo new >"$KR/boot/firmware/kernel8.img"
  run rpi_remove_stale
  [ "$status" -eq 0 ]
  [ ! -e "$KR/lib/modules/$V8" ]
  [ -d "$KR/lib/modules/$V8NEW" ]
  [ -d "$KR/lib/modules/$P5" ]
  grep -q "apt-get purge .*linux-image-$V8\$" "$CALLS"
  run ! grep -q 2712 "$CALLS"
}

# xpadneo_run : runs base-40-xpadneo.sh on the fake root $KR with stubs (git clone makes dkms.conf)
xpadneo_run() {
  cat >"$STUBS/git" <<'EOF2'
#!/usr/bin/env bash
echo "git $*" >>"$CALLS"
if [ "$1" = clone ]; then mkdir -p "$4/hid-xpadneo"; echo 'PACKAGE_VERSION="v0.10.5"' >"$4/hid-xpadneo/dkms.conf"; fi
EOF2
  make_cmd_stubs make dkms
  chmod +x "$STUBS/git"
  mkdir -p "$BATS_TEST_TMPDIR/prov"
  : >"$BATS_TEST_TMPDIR/prov/report.txt"
  run env CFG_XPADNEO_REF=v0.10.5 PROVISION_DIR="$BATS_TEST_TMPDIR/prov" \
    PROVISION_REPORT="$BATS_TEST_TMPDIR/prov/report.txt" PROVISION_ROOT="$KR" bash "$ROOT/provision/common/base-40-xpadneo.sh"
}

@test "base-40-xpadneo.sh: builds for kernels with headers; a kernel without headers gets a report line" {
  kroot "$V8" "$P5"
  mkdir -p "$KR/lib/modules/$V8/build" "$KR/lib/modules/6.1.0-old"
  : >"$KR/lib/modules/$V8/build/Makefile"
  xpadneo_run
  [ "$status" -eq 0 ]
  grep -qx "dkms build -m hid-xpadneo -v v0.10.5 -k $V8" "$CALLS"
  run ! grep -q "dkms build .* -k $P5" "$CALLS"
  [ "$(cat "$BATS_TEST_TMPDIR/prov/report.txt")" = "xpadneo 0 kernel $P5 skipped: no headers" ]
}

@test "rpi4-static.sh: a 'kernel <k> skipped' line counts for that kernel only, not for all" {
  local r="$BATS_TEST_TMPDIR/r"
  good_root "$r"
  rm -rf "$r/lib/modules/6.18.50+rpt-rpi-v8/updates"
  mkdir -p "$r/lib/modules/6.18.50+rpt-rpi-2712/kernel"
  printf 'xpadneo 0 kernel 6.18.50+rpt-rpi-2712 skipped: no headers\n' >"$BATS_TEST_TMPDIR/report.txt"
  run bash "$ROOT/test/rpi4-static.sh" --stage base --report "$BATS_TEST_TMPDIR/report.txt" --root "$r"
  [ "$status" -ne 0 ]
  [[ "$output" == *"PASS: xpadneo for kernel 6.18.50+rpt-rpi-2712 is missing, listed in the report"* ]]
  [[ "$output" == *"FAIL: xpadneo for kernel 6.18.50+rpt-rpi-v8"* ]]
  printf 'xpadneo 0 kernel 6.18.50+rpt-rpi-2712 skipped: no headers\nxpadneo 0 kernel 6.18.50+rpt-rpi-v8 skipped: no headers\n' \
    >"$BATS_TEST_TMPDIR/report.txt"
  run bash "$ROOT/test/rpi4-static.sh" --stage base --report "$BATS_TEST_TMPDIR/report.txt" --root "$r"
  [ "$status" -eq 0 ]
}

# --- base-30-retropie.sh: dpkg repair and apt options (stubs) ----------------------------

# rp_root : fake root with a RetroPie-Setup checkout whose retropie_packages.sh is a stub. The stub
# logs the module, whether the apt.conf.d file exists and what stdin is; it fails for the modules
# in $BATS_TEST_TMPDIR/failing. dpkg --audit prints the text of $BATS_TEST_TMPDIR/audit.
rp_root() {
  RPR="$BATS_TEST_TMPDIR/rpr"
  local rp="$RPR/home/emulationstation/RetroPie-Setup"
  mkdir -p "$rp/.git" "$RPR/opt/retropie/supplementary/emulationstation" "$BATS_TEST_TMPDIR/prov/rpi4"
  printf '#!/bin/sh\n' >"$RPR/opt/retropie/supplementary/emulationstation/emulationstation"
  chmod +x "$RPR/opt/retropie/supplementary/emulationstation/emulationstation"
  printf 'lr-fceumm\nlr-snes9x\n' >"$BATS_TEST_TMPDIR/prov/rpi4/modules.list"
  : >"$BATS_TEST_TMPDIR/failing"
  : >"$BATS_TEST_TMPDIR/audit"
  cat >"$rp/retropie_packages.sh" <<'EOF2'
m=${1:-none}
c=no
[ ! -f "$RPR/etc/apt/apt.conf.d/90-emulationstation-build" ] || c=yes
echo "rp $m conf=$c stdin=$(readlink /proc/$$/fd/0)" >>"$CALLS"
if grep -qxF "$m" "$BATS_TEST_TMPDIR/failing"; then exit 1; fi
case "$m" in lr-*) mkdir -p "$RPR/opt/retropie/libretrocores/$m" ;; esac
exit 0
EOF2
  cat >"$STUBS/dpkg" <<'EOF2'
#!/usr/bin/env bash
echo "dpkg $*" >>"$CALLS"
if [ "$1" = --audit ]; then cat "$BATS_TEST_TMPDIR/audit"; fi
EOF2
  cat >"$STUBS/df" <<'EOF2'
#!/usr/bin/env bash
printf 'Filesystem 1024-blocks Used Available Capacity Mounted\nx 100 1 99999999 1%% /\n'
EOF2
  make_cmd_stubs getent git chown apt-get
  chmod +x "$STUBS"/*
  export RPR BATS_TEST_TMPDIR
}

run_rp30() {
  run env PROVISION_ROOT="$RPR" CFG_RETROPIE_SETUP_REF=f5ebfba638179631d4619f2b52075dec33d4bca8 \
    CFG_RETROPIE_PLATFORM=rpi4 CFG_TARGET_KIND=rpi4 PROVISION_DIR="$BATS_TEST_TMPDIR/prov" \
    PROVISION_REPORT="$BATS_TEST_TMPDIR/prov/report.txt" bash "$ROOT/provision/common/base-30-retropie.sh"
}

@test "base-30: modules run without stdin and with the apt options file; it is removed at the end" {
  rp_root
  run_rp30
  echo "$output"
  [ "$status" -eq 0 ]
  grep -qx 'rp lr-fceumm conf=yes stdin=/dev/null' "$CALLS"
  grep -qx 'rp retroarch conf=yes stdin=/dev/null' "$CALLS"
  grep -qx 'rp none conf=yes stdin=/dev/null' "$CALLS"
  [ ! -e "$RPR/etc/apt/apt.conf.d/90-emulationstation-build" ]
  # one repair at the end
  [ "$(grep -c '^dpkg --configure -a' "$CALLS")" -eq 1 ]
}

@test "base-30: a failed module -> dpkg repair right after it, the build goes on" {
  rp_root
  echo lr-fceumm >"$BATS_TEST_TMPDIR/failing"
  run_rp30
  [ "$status" -eq 0 ]
  grep -qx 'lr-fceumm 1 installation failed, see log 30-provision-base' "$BATS_TEST_TMPDIR/prov/report.txt"
  # order: failed module, repair, next module
  [ "$(grep -A4 '^rp lr-fceumm' "$CALLS" | sed -n '2,5p')" = "dpkg --configure -a
apt-get -f install -y -q -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold
dpkg --audit
rp lr-snes9x conf=yes stdin=/dev/null" ]
  [ "$(grep -c '^dpkg --configure -a' "$CALLS")" -eq 2 ]
}

@test "base-30: dpkg still broken after the repair -> abort (base not saved), options file removed" {
  rp_root
  echo lr-fceumm >"$BATS_TEST_TMPDIR/failing"
  echo 'The following packages are only half configured' >"$BATS_TEST_TMPDIR/audit"
  run_rp30
  [ "$status" -ne 0 ]
  [[ $output == *"dpkg is still broken after the repair (after lr-fceumm)"* ]]
  run ! grep -q '^rp lr-snes9x' "$CALLS"
  [ ! -e "$RPR/etc/apt/apt.conf.d/90-emulationstation-build" ]
}

@test "base-30: a failed core module aborts and removes the options file" {
  rp_root
  echo retroarch >"$BATS_TEST_TMPDIR/failing"
  run_rp30
  [ "$status" -ne 0 ]
  [[ $output == *"Core module retroarch failed"* ]]
  [ ! -e "$RPR/etc/apt/apt.conf.d/90-emulationstation-build" ]
}
