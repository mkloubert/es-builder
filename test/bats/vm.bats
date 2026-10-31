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

# Base layer VM (Task 4): header package choice, GRUB parameters (drop-in and the
# grub.cfg fallback), 30-provision-base.sh for vm targets, test/vm-static.sh --root.
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

make_cmd_stubs() {
  local c
  for c in "$@"; do
    printf '#!/usr/bin/env bash\necho "%s $*" >>"$CALLS"\n' "$c" >"$STUBS/$c"
  done
  chmod +x "$STUBS"/*
  PATH="$STUBS:$PATH"
}

# --- header package choice (base-10-packages.sh) --------------------------------

@test "vm_headers_pkgs: headers for the installed kernel exist -> exactly these" {
  . "$ROOT/provision/common/base-10-packages.sh"
  [ "$(vm_headers_pkgs amd64 6.12.113+deb13-amd64 1)" = linux-headers-6.12.113+deb13-amd64 ]
  [ "$(vm_headers_pkgs arm64 6.12.113+deb13-arm64 1)" = linux-headers-6.12.113+deb13-arm64 ]
}

@test "vm_headers_pkgs: headers of the installed kernel are gone from the archive -> kernel and headers together" {
  . "$ROOT/provision/common/base-10-packages.sh"
  [ "$(vm_headers_pkgs amd64 6.12.113+deb13-amd64 0 | tr '\n' ' ')" = "linux-image-amd64 linux-headers-amd64 " ]
  [ "$(vm_headers_pkgs arm64 '' 0 | tr '\n' ' ')" = "linux-image-arm64 linux-headers-arm64 " ]
}

@test "old_kernel_pkgs: two kernels installed -> image, headers and common of the OLD one; meta packages stay" {
  . "$ROOT/provision/common/base-10-packages.sh"
  run old_kernel_pkgs amd64 6.12.113+deb13-amd64 <<'EOF2'
linux-image-6.12.100+deb13-amd64
linux-image-6.12.113+deb13-amd64
linux-image-amd64
linux-headers-6.12.100+deb13-amd64
linux-headers-6.12.100+deb13-common
linux-headers-6.12.113+deb13-amd64
linux-headers-6.12.113+deb13-common
linux-headers-amd64
linux-image-6.12.100+deb13-amd64-dbg
EOF2
  [ "$status" -eq 0 ]
  [ "$output" = "$(printf '%s\n' linux-image-6.12.100+deb13-amd64 linux-headers-6.12.100+deb13-amd64 linux-headers-6.12.100+deb13-common)" ]
}

@test "old_kernel_pkgs: only one kernel -> nothing to purge" {
  . "$ROOT/provision/common/base-10-packages.sh"
  run old_kernel_pkgs arm64 6.12.113+deb13-arm64 <<'EOF2'
linux-image-6.12.113+deb13-arm64
linux-headers-6.12.113+deb13-arm64
linux-headers-6.12.113+deb13-common
EOF2
  [ -z "$output" ]
}

@test "kernel_hooks_off/on: zz-update-grub is not executable in between and executable again" {
  . "$ROOT/provision/common/base-10-packages.sh"
  local r="$BATS_TEST_TMPDIR/r"
  mkdir -p "$r/etc/kernel/postinst.d" "$r/etc/kernel/postrm.d"
  printf '#!/bin/sh\n' >"$r/etc/kernel/postinst.d/zz-update-grub"
  printf '#!/bin/sh\n' >"$r/etc/kernel/postrm.d/zz-update-grub"
  printf '#!/bin/sh\n' >"$r/etc/kernel/postinst.d/initramfs-tools"
  chmod +x "$r"/etc/kernel/*/*
  export PROVISION_ROOT=$r
  kernel_hooks_off
  [ ! -x "$r/etc/kernel/postinst.d/zz-update-grub" ]
  [ ! -x "$r/etc/kernel/postrm.d/zz-update-grub" ]
  [ -x "$r/etc/kernel/postinst.d/initramfs-tools" ]
  kernel_hooks_on
  [ -x "$r/etc/kernel/postinst.d/zz-update-grub" ]
  [ -x "$r/etc/kernel/postrm.d/zz-update-grub" ]
}

# --- GRUB (vm/base-10-system.sh) ------------------------------------------------

grub_fixture() {
  cat >"$1" <<'EOF2'
set default=0
menuentry 'Debian GNU/Linux' {
	linux	/boot/vmlinuz-6.12.113+deb13-amd64 root=PARTUUID=abc ro console=tty0 console=ttyS0,115200 earlyprintk=ttyS0,115200 consoleblank=0
	initrd	/boot/initrd.img-6.12.113+deb13-amd64
}
menuentry 'Debian GNU/Linux (recovery mode)' {
	linux	/boot/vmlinuz-6.12.113+deb13-amd64 root=PARTUUID=abc ro single
}
# linux is only a word here
EOF2
}

@test "grub_cfg_set_console: replaces console= on linux lines only, keeps the rest, idempotent" {
  . "$ROOT/provision/vm/base-10-system.sh"
  local f="$BATS_TEST_TMPDIR/grub.cfg" snap
  grub_fixture "$f"
  grub_cfg_set_console "$f" "console=ttyAMA0"
  [ "$(grep -c 'console=ttyAMA0$' "$f")" -eq 2 ]
  run ! grep -q 'console=ttyS0' "$f"
  grep -q 'root=PARTUUID=abc ro earlyprintk=ttyS0,115200 consoleblank=0 console=ttyAMA0$' "$f"
  grep -q 'initrd	/boot/initrd.img' "$f"
  grep -qx '# linux is only a word here' "$f"
  snap=$(cat "$f")
  grub_cfg_set_console "$f" "console=ttyAMA0"
  [ "$(cat "$f")" = "$snap" ]
}

@test "grub_params: amd64 and arm64 values, unknown arch -> error" {
  . "$ROOT/provision/vm/base-10-system.sh"
  [ "$(grub_params amd64)" = "console=tty0 console=ttyS0,115200" ]
  [ "$(grub_params arm64)" = "console=ttyAMA0" ]
  run grub_params riscv
  [ "$status" -ne 0 ]
}

vm_root() {
  local r=$1
  mkdir -p "$r/boot/grub" "$r/etc/default/grub.d" "$r/boot/efi"
  grub_fixture "$r/boot/grub/grub.cfg"
  printf 'GRUB_CMDLINE_LINUX_DEFAULT="console=tty0 console=ttyS0,115200 consoleblank=0"\n' >"$r/etc/default/grub.d/15_cloud.cfg"
}

run_vm_system() {
  run env PROVISION_ROOT="$1" CFG_TARGET_ARCH="$2" bash "$ROOT/provision/vm/base-10-system.sh"
}

@test "vm base-10-system.sh: update-grub fails (grub-probe) -> drop-in written, grub.cfg fixed by sed" {
  printf '#!/usr/bin/env bash\necho "update-grub" >>"$CALLS"\necho "grub-probe: error: failed to get canonical path of /dev/loop3." >&2\nexit 1\n' >"$STUBS/update-grub"
  make_cmd_stubs apt-get
  chmod +x "$STUBS/update-grub"
  local r="$BATS_TEST_TMPDIR/r"
  vm_root "$r"
  run_vm_system "$r" amd64
  echo "$output"
  [ "$status" -eq 0 ]
  grep -qx update-grub "$CALLS"
  grep -q 'apt-get .*cloud-guest-utils mesa-utils libgl1-mesa-dri' "$CALLS"
  grep -q 'GRUB_CMDLINE_LINUX_DEFAULT' "$r/etc/default/grub.d/50-emulationstation.cfg"
  # the drop-in keeps foreign words and replaces console=
  run bash -c ". '$r/etc/default/grub.d/15_cloud.cfg'; . '$r/etc/default/grub.d/50-emulationstation.cfg'; echo \"\$GRUB_CMDLINE_LINUX_DEFAULT\""
  [ "$output" = "consoleblank=0 console=tty0 console=ttyS0,115200" ]
  [ "$(grep -c ' console=tty0 console=ttyS0,115200$' "$r/boot/grub/grub.cfg")" -eq 2 ]
  grep -q 'earlyprintk=ttyS0,115200 consoleblank=0 console=tty0 console=ttyS0,115200$' "$r/boot/grub/grub.cfg"
}

@test "vm base-10-system.sh: update-grub writes root=/dev/loopN -> old grub.cfg is restored, sed fallback" {
  cat >"$STUBS/update-grub" <<'EOF2'
#!/usr/bin/env bash
sed -i 's|root=PARTUUID=abc|root=/dev/loop3p1|' "$PROVISION_ROOT/boot/grub/grub.cfg"
EOF2
  make_cmd_stubs apt-get
  chmod +x "$STUBS/update-grub"
  local r="$BATS_TEST_TMPDIR/r"
  vm_root "$r"
  run_vm_system "$r" arm64
  [ "$status" -eq 0 ]
  run ! grep -q '/dev/loop' "$r/boot/grub/grub.cfg"
  [ "$(grep -c 'root=PARTUUID=abc.* console=ttyAMA0$' "$r/boot/grub/grub.cfg")" -eq 2 ]
}

@test "vm base-10-system.sh: update-grub works -> grub.cfg stays as update-grub made it" {
  cat >"$STUBS/update-grub" <<'EOF2'
#!/usr/bin/env bash
sed -i 's|$| console=tty0 console=ttyS0,115200|' "$PROVISION_ROOT/boot/grub/grub.cfg"
EOF2
  make_cmd_stubs apt-get
  chmod +x "$STUBS/update-grub"
  local r="$BATS_TEST_TMPDIR/r"
  vm_root "$r"
  run_vm_system "$r" amd64
  [ "$status" -eq 0 ]
  grep -q 'single console=tty0 console=ttyS0,115200$' "$r/boot/grub/grub.cfg"
  [ ! -e "$r/boot/grub/grub.cfg.emulationstation-bak" ]
}

@test "vm base-10-system.sh: update-grub fails with an unexpected error -> abort, no silent sed fix" {
  printf '#!/usr/bin/env bash\necho "/etc/default/grub.d/50-emulationstation.cfg: line 3: syntax error" >&2\nexit 2\n' >"$STUBS/update-grub"
  make_cmd_stubs apt-get
  chmod +x "$STUBS/update-grub"
  local r="$BATS_TEST_TMPDIR/r"
  vm_root "$r"
  run_vm_system "$r" amd64
  [ "$status" -ne 0 ]
  [[ "$output" == *"unexpected error"* ]]
}

@test "dropin_cmdline: valid drop-in -> value; syntax error -> failure" {
  . "$ROOT/provision/vm/base-10-system.sh"
  local f="$BATS_TEST_TMPDIR/d.cfg"
  printf 'GRUB_CMDLINE_LINUX_DEFAULT="x"\n' >"$f"
  [ "$(dropin_cmdline "$f")" = x ]
  printf 'for a in; do\n' >"$f"
  run dropin_cmdline "$f"
  [ "$status" -ne 0 ]
}

@test "vm base-10-system.sh: unknown architecture -> abort" {
  make_cmd_stubs apt-get update-grub
  local r="$BATS_TEST_TMPDIR/r"
  vm_root "$r"
  run_vm_system "$r" riscv
  [ "$status" -ne 0 ]
}

# --- 30-provision-base.sh for vm targets ------------------------------------------

write_vm_conf() {
  cat >"$BATS_TEST_TMPDIR/build.conf" <<EOF2
USER_PASSWORD=secret-not-in-the-base
VM_IMAGE_URL_AMD64=file:///nowhere/a.raw
VM_IMAGE_CHECKSUM_AMD64=sha512:$(printf 'a%.0s' {1..128})
VM_IMAGE_URL_ARM64=file:///nowhere/b.raw
VM_IMAGE_CHECKSUM_ARM64=sha512:$(printf 'b%.0s' {1..128})
RETROPIE_SETUP_REF=r1
XPADNEO_REF=x1
EOF2
  export BUILD_CONFIG="$BATS_TEST_TMPDIR/build.conf"
  config_load "$BUILD_CONFIG"
  BASE=$(REPO_ROOT="$ROOT" cache_base_path "$1")
}

make_vm_step_stubs() {
  printf '#!/usr/bin/env bash\necho "losetup $*" >>"$CALLS"\ncase "$1" in -f) echo /dev/loop0 ;; esac\n' >"$STUBS/losetup"
  printf '#!/usr/bin/env bash\necho "mount $*" >>"$CALLS"\n' >"$STUBS/mount"
  printf '#!/usr/bin/env bash\necho "umount $*" >>"$CALLS"\n' >"$STUBS/umount"
  printf '#!/usr/bin/env bash\ncat "%s"\n' "$FIX/sfdisk-vm.json" >"$STUBS/sfdisk"
  cat >"$STUBS/chroot" <<'EOF2'
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
[ -f "$root/root/provision/vm/modules.list" ] || { echo "chroot: modules.list is missing" >>"$CALLS"; exit 98; }
echo "chroot $script$envs" >>"$CALLS"
EOF2
  chmod +x "$STUBS"/*
  PATH="$STUBS:$PATH"
}

@test "30-provision-base.sh vm-amd64: common scripts, then vm; platform x86, generic CPU flags, KMS" {
  make_vm_step_stubs
  write_vm_conf vm-amd64
  echo image >"$BASE.tmp"
  export TARGET_MNT="$BATS_TEST_TMPDIR/mnt"
  run bash "$ROOT/builder/steps/30-provision-base.sh" vm-amd64
  echo "$output"
  [ "$status" -eq 0 ]
  local env=" CFG_TARGET=vm-amd64 CFG_TARGET_KIND=vm CFG_TARGET_ARCH=amd64 CFG_RETROPIE_SETUP_REF=r1 CFG_RETROPIE_PLATFORM=x86 CFG_XPADNEO_REF=x1 CFG_RETROPIE_CPU_FLAGS=-march=x86-64 CFG_RETROPIE_KMS=1 PROVISION_DIR=/root/provision PROVISION_REPORT=/root/provision/report.txt"
  [ "$(grep '^chroot' "$CALLS")" = "$(printf '%s\n' \
    "chroot /root/provision/common/base-10-packages.sh$env" \
    "chroot /root/provision/common/base-20-user.sh$env" \
    "chroot /root/provision/common/base-30-retropie.sh$env" \
    "chroot /root/provision/common/base-40-xpadneo.sh$env" \
    "chroot /root/provision/vm/base-10-system.sh$env")" ]
  grep -qx "umount $TARGET_MNT/boot/efi" "$CALLS"
  [ "$(cat "$BASE")" = image ]
}

@test "30-provision-base.sh vm-arm64: platform aarch64 (generic, from platform_native)" {
  make_vm_step_stubs
  write_vm_conf vm-arm64
  echo image >"$BASE.tmp"
  export TARGET_MNT="$BATS_TEST_TMPDIR/mnt"
  run bash "$ROOT/builder/steps/30-provision-base.sh" vm-arm64
  [ "$status" -eq 0 ]
  grep -q 'CFG_TARGET_ARCH=arm64 CFG_RETROPIE_SETUP_REF=r1 CFG_RETROPIE_PLATFORM=aarch64 CFG_XPADNEO_REF=x1 CFG_RETROPIE_CPU_FLAGS=-march=armv8-a CFG_RETROPIE_KMS=1 ' "$CALLS"
}

@test "vm modules.list: exactly lr-fceumm lr-snes9x lr-gambatte vice flycast lr-pcsx-rearmed" {
  [ "$(grep -Ev '^(#|$)' "$ROOT/provision/vm/modules.list" | tr '\n' ' ')" = "lr-fceumm lr-snes9x lr-gambatte vice flycast lr-pcsx-rearmed " ]
}

@test "Provision scripts (vm): shebang, set -euo pipefail, bash -n" {
  local f
  for f in "$ROOT"/provision/vm/base-*.sh; do
    [ "$(head -n1 "$f")" = '#!/usr/bin/env bash' ]
    grep -qx 'set -euo pipefail' "$f"
    bash -n "$f"
  done
}

@test "20-prepare-image.sh: vm grows by 4 GiB, rpi4 by 8 GiB" {
  grep -qE '^[[:space:]]*vm\) grow=4 ;;' "$ROOT/builder/steps/20-prepare-image.sh"
  grep -qE '^[[:space:]]*rpi4\) grow=8 ;;' "$ROOT/builder/steps/20-prepare-image.sh"
}

@test "base-30-retropie.sh: passes CPU flags and KMS to RetroPie" {
  grep -q 'CFG_RETROPIE_CPU_FLAGS' "$ROOT/provision/common/base-30-retropie.sh"
  grep -q '__cpu_flags=' "$ROOT/provision/common/base-30-retropie.sh"
  grep -q '__has_kms=1' "$ROOT/provision/common/base-30-retropie.sh"
}

# --- test/vm-static.sh --root --------------------------------------------------------

vm_good_root() {
  local r=$1 arch=$2 k=6.1.0-54-$2 m params
  mkdir -p "$r/etc/systemd/system/getty.target.wants" "$r/boot/grub" "$r/lib/modules/$k/kernel" \
    "$r/lib/modules/$k/updates/dkms" "$r/opt/retropie/supplementary/emulationstation" \
    "$r/opt/retropie/libretrocores" "$r/home/emulationstation/RetroPie-Setup/.git" "$r/etc/default/grub.d"
  printf 'NAME="Debian GNU/Linux"\nVERSION_CODENAME=bookworm\n' >"$r/etc/os-release"
  printf 'root:x:0:0:root:/root:/bin/bash\nemulationstation:x:1000:1000::/home/emulationstation:/bin/bash\n' >"$r/etc/passwd"
  printf 'root:!:20000:0:99999:7:::\nemulationstation:!:20000:0:99999:7:::\n' >"$r/etc/shadow"
  printf '%s\n' root:x:0: sudo:x:27:emulationstation audio:x:29:emulationstation video:x:44:emulationstation \
    input:x:105:emulationstation render:x:106:emulationstation bluetooth:x:107:emulationstation \
    emulationstation:x:1000: >"$r/etc/group"
  mkdir -p "$r/home/emulationstation"
  printf '#!/bin/sh\n' >"$r/opt/retropie/supplementary/emulationstation/emulationstation"
  chmod +x "$r/opt/retropie/supplementary/emulationstation/emulationstation"
  for m in lr-fceumm lr-snes9x lr-gambatte; do mkdir -p "$r/opt/retropie/libretrocores/$m"; done
  : >"$r/lib/modules/$k/updates/dkms/hid-xpadneo.ko.xz"
  echo f5ebfba6 >"$r/home/emulationstation/RetroPie-Setup/.git/HEAD"
  case "$arch" in amd64) params="console=tty0 console=ttyS0,115200" ;; *) params="console=ttyAMA0" ;; esac
  printf 'menuentry x {\n\tlinux /boot/vmlinuz-%s root=PARTUUID=abc ro %s\n}\n' "$k" "$params" >"$r/boot/grub/grub.cfg"
  printf '%s\n' 'es_cmdline=""' 'for es_word in ${GRUB_CMDLINE_LINUX_DEFAULT:-}; do' \
    '  case "$es_word" in console=*) ;; *) es_cmdline="$es_cmdline $es_word" ;; esac' 'done' \
    "GRUB_CMDLINE_LINUX_DEFAULT=\"\${es_cmdline# }\${es_cmdline:+ }$params\"" >"$r/etc/default/grub.d/50-emulationstation.cfg"
  ln -s /usr/lib/systemd/system/getty@.service "$r/etc/systemd/system/getty.target.wants/getty@tty1.service"
}

@test "vm-static.sh --root: provisioned amd64 and arm64 roots -> PASS only" {
  local a r="$BATS_TEST_TMPDIR/r"
  for a in amd64 arm64; do
    rm -rf "$r"
    vm_good_root "$r" $a
    run bash "$ROOT/test/vm-static.sh" --stage base --arch $a --root "$r"
    echo "$output"
    [ "$status" -eq 0 ]
    [[ "$output" == *"PASS: Kernel parameters in grub.cfg"* ]]
    [[ "$output" != *FAIL* ]]
  done
}

@test "vm-static.sh --root: unchanged Debian image -> FAIL (user, ES, parameters, root not locked)" {
  local r="$BATS_TEST_TMPDIR/r"
  mkdir -p "$r/etc" "$r/boot/grub" "$r/lib/modules/6.12.113+deb13-amd64/kernel"
  printf 'root:x:0:0:root:/root:/bin/bash\n' >"$r/etc/passwd"
  printf 'root::20000:0:99999:7:::\n' >"$r/etc/shadow"
  printf 'root:x:0:\n' >"$r/etc/group"
  printf 'menuentry x {\n\tlinux /boot/vmlinuz root=PARTUUID=abc ro console=tty0\n}\n' >"$r/boot/grub/grub.cfg"
  run bash "$ROOT/test/vm-static.sh" --stage base --arch amd64 --root "$r"
  echo "$output"
  [ "$status" -ne 0 ]
  [[ "$output" == *"FAIL: User emulationstation"* ]]
  [[ "$output" == *"FAIL: root password locked"* ]]
  [[ "$output" == *"FAIL: EmulationStation"* ]]
  [[ "$output" == *"FAIL: Kernel parameters in grub.cfg"* ]]
}

@test "vm-static.sh --root: empty root password -> FAIL; missing module -> FAIL, with report line -> PASS" {
  local r="$BATS_TEST_TMPDIR/r"
  vm_good_root "$r" amd64
  printf 'root::20000:0:99999:7:::\nemulationstation:!:20000:0:99999:7:::\n' >"$r/etc/shadow"
  rmdir "$r/opt/retropie/libretrocores/lr-gambatte"
  run bash "$ROOT/test/vm-static.sh" --stage base --arch amd64 --root "$r"
  [ "$status" -ne 0 ]
  [[ "$output" == *"FAIL: root password locked"* ]]
  [[ "$output" == *"FAIL: Module lr-gambatte"* ]]
  printf 'root:*:20000:0:99999:7:::\nemulationstation:!:20000:0:99999:7:::\n' >"$r/etc/shadow"
  printf 'lr-gambatte 1 Installation failed\n' >"$BATS_TEST_TMPDIR/report.txt"
  run bash "$ROOT/test/vm-static.sh" --stage base --arch amd64 --report "$BATS_TEST_TMPDIR/report.txt" --root "$r"
  echo "$output"
  [ "$status" -eq 0 ]
  [[ "$output" == *"PASS: Module lr-gambatte is missing, listed in the report"* ]]
}

@test "vm-static.sh --root: wrong parameters for the arch -> FAIL" {
  local r="$BATS_TEST_TMPDIR/r"
  vm_good_root "$r" arm64
  run bash "$ROOT/test/vm-static.sh" --stage base --arch amd64 --root "$r"
  [ "$status" -ne 0 ]
  [[ "$output" == *"FAIL: Kernel parameters in grub.cfg"* ]]
}

@test "vm-static.sh --root: broken drop-in -> FAIL; two kernels -> FAIL" {
  local r="$BATS_TEST_TMPDIR/r"
  vm_good_root "$r" amd64
  printf 'for a in; do\n' >"$r/etc/default/grub.d/50-emulationstation.cfg"
  mkdir -p "$r/lib/modules/6.12.100+deb13-amd64/kernel"
  run bash "$ROOT/test/vm-static.sh" --stage base --arch amd64 --root "$r"
  [ "$status" -ne 0 ]
  [[ "$output" == *"FAIL: GRUB drop-in"* ]]
  [[ "$output" == *"FAIL: exactly one kernel"* ]]
  [[ "$output" == *"FAIL: xpadneo for kernel 6.12.100"* ]]
}

@test "vm-static.sh without root: restarts itself in the builder container" {
  printf '#!/usr/bin/env bash\n[ "$1" = -u ] && echo 1000 || /usr/bin/id "$@"\n' >"$STUBS/id"
  printf '#!/usr/bin/env bash\necho "docker $*" >>"$CALLS"\n' >"$STUBS/docker"
  chmod +x "$STUBS"/*
  PATH="$STUBS:$PATH" run bash "$ROOT/test/vm-static.sh" /cache/base-vm-amd64-0123456789abcdef.img
  [ "$status" -eq 0 ]
  [ "$(cat "$CALLS")" = "docker run --rm --privileged -v emulationstation-cache:/cache -v $ROOT:/work -w /work emulationstation-builder test/vm-static.sh --stage base /cache/base-vm-amd64-0123456789abcdef.img" ]
}

@test "vm-static.sh: unknown stage or missing image -> exit 2" {
  run bash "$ROOT/test/vm-static.sh" --stage foo --root /
  [ "$status" -eq 2 ]
  run bash "$ROOT/test/vm-static.sh" --stage base
  [ "$status" -eq 2 ]
}

@test "vm-static.sh --root: a 'kernel <k> skipped' report line counts only for that kernel" {
  local r="$BATS_TEST_TMPDIR/r" k
  vm_good_root "$r" amd64
  k=$(basename "$(find "$r/lib/modules" -mindepth 1 -maxdepth 1 -type d | head -n 1)")
  find "$r/lib/modules/$k" -name 'hid-xpadneo.ko*' -delete
  printf 'xpadneo 0 kernel 6.12.100+deb13-amd64 skipped: no headers\n' >"$BATS_TEST_TMPDIR/report.txt"
  run bash "$ROOT/test/vm-static.sh" --stage base --arch amd64 --report "$BATS_TEST_TMPDIR/report.txt" --root "$r"
  [ "$status" -ne 0 ]
  [[ "$output" == *"FAIL: xpadneo for kernel $k"* ]]
  printf 'xpadneo 0 kernel %s skipped: no headers\n' "$k" >"$BATS_TEST_TMPDIR/report.txt"
  run bash "$ROOT/test/vm-static.sh" --stage base --arch amd64 --report "$BATS_TEST_TMPDIR/report.txt" --root "$r"
  [[ "$output" == *"PASS: xpadneo for kernel $k is missing, listed in the report"* ]]
}
