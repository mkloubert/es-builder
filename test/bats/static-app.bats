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

# The shared app-layer checks (test/lib/static-app.sh) through test/rpi4-static.sh and
# test/vm-static.sh with --root. The fake root is made from overlay/. Only the lines of the
# shared checks are looked at; the other checks of the scripts fail on a fake root.

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  R="$BATS_TEST_TMPDIR/root"
  # ~/.emulationstation is a symlink into /opt like RetroPie makes it
  mkdir -p "$R/etc/systemd/system/multi-user.target.wants" "$R/root" "$R/usr/local/lib" \
    "$R/opt/retropie/configs/all/emulationstation/themes/es-epicnoir" "$R/home/emulationstation" \
    "$R/boot/firmware" "$R/boot/efi"
  ln -s /opt/retropie/configs/all/emulationstation "$R/home/emulationstation/.emulationstation"
  cp -r "$ROOT/overlay/etc/." "$R/etc/"
  # the files of the fake root belong to the test user: use its numbers for emulationstation
  printf 'emulationstation:x:%s:%s::/home/emulationstation:/bin/bash\n' "$(id -u)" "$(id -g)" >"$R/etc/passwd"
  cp -r "$ROOT/overlay/usr/local/lib/emulationstation" "$R/usr/local/lib/"
  chmod 440 "$R/etc/sudoers.d/emulationstation"
  : >"$R/opt/retropie/configs/all/emulationstation/themes/es-epicnoir/theme.xml"
  echo '<string name="ThemeSet" value="es-epicnoir" />' >"$R/opt/retropie/configs/all/emulationstation/es_settings.cfg"
  # autostart.sh in the "no" form (default)
  # (the script sets its own R, so the path is fixed before it is sourced)
  AUTOSTART="$R/opt/retropie/configs/all/autostart.sh"
  (. "$ROOT/provision/common/app-20-es.sh" && write_autostart "$AUTOSTART" no)
  local u
  for u in ssh nftables fail2ban emulationstation-firstboot; do
    ln -s "/lib/systemd/system/$u.service" "$R/etc/systemd/system/multi-user.target.wants/$u.service"
  done
  echo uninitialized >"$R/etc/machine-id"
  # the CPU limit unit is enabled on rpi4 only (rm_cpu_unit for VM checks)
  CPU_LINK="$R/etc/systemd/system/multi-user.target.wants/emulationstation-cpu.service"
  ln -s /etc/systemd/system/emulationstation-cpu.service "$CPU_LINK"
  # custom web UI folder and its web.conf key (spec 2026-10-31-webui-custom-design.md 3.2)
  mkdir -p "$R/home/emulationstation/webui-custom/static" "$R/etc/emulationstation-web"
  echo x >"$R/home/emulationstation/webui-custom/static/a.css"
  printf 'WEB_USER=u\nUI_DIR=/home/emulationstation/webui-custom\n' >"$R/etc/emulationstation-web/web.conf"
  cp "$ROOT/overlay/boot-template/emulationstation.txt.example" "$R/boot/firmware/"
  cp "$ROOT/overlay/boot-template/emulationstation.txt.example" "$R/boot/efi/"
  printf 'TARGET_KIND=rpi4\nBOOT_DIR=/boot/firmware\n' >"$R/usr/local/lib/emulationstation/firstboot.conf"
  # USB import: one kernel with the three modules, the tools, and a modinfo stub. The stub
  # succeeds when lib/modules/<release>/have/<module> exists below the -b directory.
  mkdir -p "$R/lib/modules/6.12.0-test/kernel" "$R/lib/modules/6.12.0-test/have" "$R/usr/bin" "$BATS_TEST_TMPDIR/bin"
  local m t
  for m in vfat exfat ntfs3; do : >"$R/lib/modules/6.12.0-test/have/$m"; done
  for t in lsblk jq findmnt mountpoint; do
    printf '#!/bin/sh\n' >"$R/usr/bin/$t"
    chmod 755 "$R/usr/bin/$t"
  done
  cat >"$BATS_TEST_TMPDIR/bin/modinfo" <<'STUB'
#!/bin/sh
base=/ rel="" mod=""
while [ $# -gt 0 ]; do
  case "$1" in
    -b) base=$2; shift ;;
    -k) rel=$2; shift ;;
    *) mod=$1 ;;
  esac
  shift
done
[ -e "$base/lib/modules/$rel/have/$mod" ]
STUB
  chmod 755 "$BATS_TEST_TMPDIR/bin/modinfo"
  PATH="$BATS_TEST_TMPDIR/bin:$PATH"
}

# shared_lines <script> : the output lines of the shared checks (from "sshd drop-in" on)
shared_lines() {
  local script=$1
  shift
  "$ROOT/test/$script" --stage app "$@" --root "$R" 2>&1 </dev/null | sed -n '/^[A-Z]*: sshd drop-in: Port/,$p' || true
}

@test "rpi4-static: all shared app checks pass on a good fake root" {
  run shared_lines rpi4-static.sh
  [ "$status" -eq 0 ]
  [[ $output == *"PASS: sshd drop-in: Port 11911"* ]]
  [[ $output == *"PASS: fail2ban jail emulationstation-web"* ]]
  [[ $output == *"PASS: first boot script installed and set to /boot/firmware"* ]]
  [[ $output == *"PASS: es_settings.cfg: owner emulationstation"* ]]
  [[ $output == *"PASS: theme es-epicnoir: owner emulationstation"* ]]
  [[ $output != *"FAIL:"* ]]
}

@test "vm-static: all shared app checks pass on a good fake root (ESP boot/efi)" {
  printf 'TARGET_KIND=vm\nBOOT_DIR=/boot/efi\n' >"$R/usr/local/lib/emulationstation/firstboot.conf"
  rm -f "$CPU_LINK"
  run shared_lines vm-static.sh --arch amd64
  [ "$status" -eq 0 ]
  [[ $output == *"PASS: first boot script installed and set to /boot/efi"* ]]
  [[ $output != *"FAIL:"* ]]
}

@test "shared checks: autostart.sh yes and no forms pass, another command fails" {
  run shared_lines rpi4-static.sh
  [[ $output == *"PASS: autostart.sh: es-session (yes) or the off hint (no)"* ]]
  (. "$ROOT/provision/common/app-20-es.sh" && write_autostart "$AUTOSTART" yes)
  run shared_lines rpi4-static.sh
  [[ $output == *"PASS: autostart.sh: es-session (yes) or the off hint (no)"* ]]
  printf 'emulationstation #auto\n' >"$R/opt/retropie/configs/all/autostart.sh"
  run shared_lines rpi4-static.sh
  [[ $output == *"FAIL: autostart.sh: es-session (yes) or the off hint (no)"* ]]
  printf '# EmulationStation autostart is off\nexec emulationstation\n' >"$R/opt/retropie/configs/all/autostart.sh"
  run shared_lines rpi4-static.sh
  [[ $output == *"FAIL: autostart.sh: es-session (yes) or the off hint (no)"* ]]
}

@test "shared checks: c64music passes without vsid, with a full setup, and fails when broken" {
  local line="c64music: .sid system with vsid (when VICE is installed)"
  run shared_lines rpi4-static.sh
  [[ $output == *"PASS: $line"* ]]
  mkdir -p "$R/opt/retropie/emulators/vice/bin" "$R/etc/emulationstation"
  printf '#!/bin/sh\n' >"$R/opt/retropie/emulators/vice/bin/vsid"
  chmod 755 "$R/opt/retropie/emulators/vice/bin/vsid"
  cp "$ROOT/test/fixtures/es_systems/es_systems.cfg" "$R/etc/emulationstation/es_systems.cfg"
  run shared_lines rpi4-static.sh
  [[ $output == *"FAIL: $line"* ]]
  mkdir -p "$R/opt/retropie/configs/c64music" "$R/home/emulationstation/RetroPie/roms/c64music"
  # shellcheck source=provision/common/app-25-c64music.sh
  (. "$ROOT/provision/common/app-25-c64music.sh" &&
    write_emulators "$R/opt/retropie/configs/c64music/emulators.cfg" &&
    add_system "$R/etc/emulationstation/es_systems.cfg")
  run shared_lines rpi4-static.sh
  [[ $output == *"PASS: $line"* ]]
}

@test "shared checks: each broken item is reported" {
  sed -i 's/^Port 11911/Port 22/' "$R/etc/ssh/sshd_config.d/10-emulationstation.conf"
  sed -i 's/maxretry = 5/maxretry = 9/' "$R/etc/fail2ban/jail.d/emulationstation.conf"
  sed -i 's/policy drop;/policy accept;/' "$R/etc/nftables.conf"
  echo key >"$R/etc/ssh/ssh_host_ed25519_key"
  echo 1234 >"$R/etc/machine-id"
  mkdir -p "$R/root/provision"
  rm "$R/etc/systemd/system/multi-user.target.wants/fail2ban.service"
  : >"$R/boot/firmware/emulationstation.txt"
  rm "$R/opt/retropie/configs/all/emulationstation/es_settings.cfg"
  run shared_lines rpi4-static.sh
  [[ $output == *"FAIL: sshd drop-in: Port 11911"* ]]
  [[ $output == *"FAIL: fail2ban jail sshd"* ]]
  [[ $output == *"FAIL: nftables.conf"* ]]
  [[ $output == *"FAIL: no SSH host keys"* ]]
  [[ $output == *"FAIL: machine-id"* ]]
  [[ $output == *"FAIL: no /root/provision"* ]]
  [[ $output == *"FAIL: fail2ban.service enabled"* ]]
  [[ $output == *"FAIL: no emulationstation.txt on the boot partition"* ]]
  [[ $output == *"FAIL: es_settings.cfg: ThemeSet"* ]]
}

@test "shared checks: a changed example file or a sudoers rule with more commands fails" {
  echo "# changed" >>"$R/boot/firmware/emulationstation.txt.example"
  sed -i 's#/usr/sbin/poweroff,#/usr/sbin/poweroff, /bin/bash,#' "$R/etc/sudoers.d/emulationstation"
  run shared_lines rpi4-static.sh
  [[ $output == *"FAIL: emulationstation.txt.example is on the boot partition"* ]]
  [[ $output == *"FAIL: sudoers"* ]]
}

@test "shared checks: es_settings.cfg and theme not owned by emulationstation fail (through the symlink)" {
  # emulationstation gets other numbers than the owner of the files (the test user)
  printf 'emulationstation:x:%s:%s::/home/emulationstation:/bin/bash\n' $(($(id -u) + 1)) $(($(id -g) + 1)) >"$R/etc/passwd"
  run shared_lines rpi4-static.sh
  [[ $output == *"FAIL: es_settings.cfg: owner emulationstation"* ]]
  [[ $output == *"FAIL: theme es-epicnoir: owner emulationstation"* ]]
}

# --- USB import (spec 2026-10-28 section 5) ---

@test "usb checks: modules, tools and helper pass on a good fake root" {
  run shared_lines rpi4-static.sh
  [[ $output == *"PASS: USB: kernel 6.12.0-test has vfat"* ]]
  [[ $output == *"PASS: USB: kernel 6.12.0-test has exfat"* ]]
  [[ $output == *"PASS: USB: kernel 6.12.0-test has ntfs3"* ]]
  [[ $output == *"PASS: USB: lsblk is in the image"* ]]
  [[ $output == *"PASS: USB: jq is in the image"* ]]
  [[ $output == *"PASS: USB: the helper has usb-list"* ]]
  [[ $output != *"FAIL:"* ]]
}

@test "usb checks: a missing exfat module fails, also with a report line" {
  rm "$R/lib/modules/6.12.0-test/have/exfat"
  echo "usb exfat missing for kernel 6.12.0-test" >"$BATS_TEST_TMPDIR/report.txt"
  run shared_lines rpi4-static.sh --report "$BATS_TEST_TMPDIR/report.txt"
  [[ $output == *"FAIL: USB: kernel 6.12.0-test has exfat"* ]]
  [[ $output == *"PASS: USB: kernel 6.12.0-test has vfat"* ]]
}

@test "usb checks: a missing vfat module fails (vm-static)" {
  rm "$R/lib/modules/6.12.0-test/have/vfat"
  rm -f "$CPU_LINK"
  run shared_lines vm-static.sh --arch amd64
  [[ $output == *"FAIL: USB: kernel 6.12.0-test has vfat"* ]]
}

@test "usb checks: ntfs3 missing without a report line fails" {
  rm "$R/lib/modules/6.12.0-test/have/ntfs3"
  run shared_lines rpi4-static.sh
  [[ $output == *"FAIL: USB: kernel 6.12.0-test has ntfs3"* ]]
}

@test "usb checks: ntfs3 missing with a report line only warns (report file)" {
  rm "$R/lib/modules/6.12.0-test/have/ntfs3"
  echo "usb ntfs3 missing for kernel 6.12.0-test" >"$BATS_TEST_TMPDIR/report.txt"
  run shared_lines rpi4-static.sh --report "$BATS_TEST_TMPDIR/report.txt"
  [ "$status" -eq 0 ]
  [[ $output == *"WARN: USB: kernel 6.12.0-test has no ntfs3"* ]]
  [[ $output != *"FAIL:"* ]]
}

@test "usb checks: ntfs3 missing with a report line in the image only warns" {
  rm "$R/lib/modules/6.12.0-test/have/ntfs3"
  echo "usb ntfs3 missing for kernel 6.12.0-test" >"$R/usr/local/lib/emulationstation/build-report.txt"
  run shared_lines rpi4-static.sh
  [[ $output == *"WARN: USB: kernel 6.12.0-test has no ntfs3"* ]]
  [[ $output != *"FAIL:"* ]]
}

@test "usb checks: a report line for another kernel does not help" {
  rm "$R/lib/modules/6.12.0-test/have/ntfs3"
  echo "usb ntfs3 missing for kernel 1.2.3" >"$BATS_TEST_TMPDIR/report.txt"
  run shared_lines rpi4-static.sh --report "$BATS_TEST_TMPDIR/report.txt"
  [[ $output == *"FAIL: USB: kernel 6.12.0-test has ntfs3"* ]]
}

@test "usb checks: a module built into the kernel counts as present" {
  rm "$R/lib/modules/6.12.0-test/have/exfat"
  echo "kernel/fs/exfat/exfat.ko" >"$R/lib/modules/6.12.0-test/modules.builtin"
  run shared_lines rpi4-static.sh
  [[ $output == *"PASS: USB: kernel 6.12.0-test has exfat"* ]]
}

@test "usb checks: a missing tool or a helper without usb-list fails" {
  rm "$R/usr/bin/jq" "$R/usr/bin/mountpoint"
  sed -i 's/usb-list/xx/g' "$R/usr/local/lib/emulationstation/priv"
  run shared_lines rpi4-static.sh
  [[ $output == *"FAIL: USB: jq is in the image"* ]]
  [[ $output == *"FAIL: USB: mountpoint is in the image"* ]]
  [[ $output == *"PASS: USB: lsblk is in the image"* ]]
  [[ $output == *"FAIL: USB: the helper has usb-list"* ]]
}

@test "usb checks: every installed kernel is checked" {
  mkdir -p "$R/lib/modules/6.12.1-two/kernel" "$R/lib/modules/6.12.1-two/have"
  : >"$R/lib/modules/6.12.1-two/have/vfat"
  run shared_lines rpi4-static.sh
  [[ $output == *"PASS: USB: kernel 6.12.0-test has exfat"* ]]
  [[ $output == *"PASS: USB: kernel 6.12.1-two has vfat"* ]]
  [[ $output == *"FAIL: USB: kernel 6.12.1-two has exfat"* ]]
}

@test "app-35-usb: writes a report line for each missing module" {
  export PROVISION_ROOT="$R"
  rm "$R/lib/modules/6.12.0-test/have/ntfs3"
  run bash "$ROOT/provision/common/app-35-usb.sh"
  [ "$status" -eq 0 ]
  grep -qxF "usb ntfs3 missing for kernel 6.12.0-test" "$R/usr/local/lib/emulationstation/build-report.txt"
  [ "$(wc -l <"$R/usr/local/lib/emulationstation/build-report.txt")" -eq 1 ]
}

@test "app-35-usb: all modules present -> no report file" {
  export PROVISION_ROOT="$R"
  echo "usb ntfs3 missing for kernel old" >"$R/usr/local/lib/emulationstation/build-report.txt"
  run bash "$ROOT/provision/common/app-35-usb.sh"
  [ "$status" -eq 0 ]
  [ ! -e "$R/usr/local/lib/emulationstation/build-report.txt" ]
}

@test "usb checks: the line from out/build-report.txt (finalize block, even indented) gives the WARN" {
  rm "$R/lib/modules/6.12.0-test/have/ntfs3"
  printf 'Notes from the image build (rpi4):\n  usb ntfs3 missing for kernel 6.12.0-test\n\n' >"$BATS_TEST_TMPDIR/report.txt"
  run shared_lines rpi4-static.sh --report "$BATS_TEST_TMPDIR/report.txt"
  [[ $output == *"WARN: USB: kernel 6.12.0-test has no ntfs3"* ]]
  [[ $output != *"FAIL:"* ]]
}

@test "shared checks: the custom web UI folder and UI_DIR are checked" {
  run shared_lines rpi4-static.sh
  [[ $output == *"PASS: webui-custom folder: emulationstation, no symlinks"* ]]
  [[ $output == *"PASS: web.conf: UI_DIR=/home/emulationstation/webui-custom"* ]]
  ln -s /etc/passwd "$R/home/emulationstation/webui-custom/static/pw"
  sed -i '/^UI_DIR=/d' "$R/etc/emulationstation-web/web.conf"
  rm -f "$CPU_LINK"
  run shared_lines vm-static.sh --arch amd64
  [[ $output == *"FAIL: webui-custom folder: emulationstation, no symlinks"* ]]
  [[ $output == *"FAIL: web.conf: UI_DIR=/home/emulationstation/webui-custom"* ]]
  rm -rf "$R/home/emulationstation/webui-custom"
  run shared_lines rpi4-static.sh
  [[ $output == *"FAIL: webui-custom folder: emulationstation, no symlinks"* ]]
}

@test "cpu unit: enabled on rpi4, not enabled on the VM, oneshot with priv cpu-apply" {
  rm "$CPU_LINK"
  run shared_lines rpi4-static.sh
  [[ $output == *"FAIL: emulationstation-cpu.service enabled"* ]]
  [[ $output == *"PASS: emulationstation-cpu.service: oneshot priv cpu-apply"* ]]
  rm -f "$CPU_LINK"
  run shared_lines vm-static.sh --arch amd64
  [[ $output == *"PASS: emulationstation-cpu.service not enabled (Pi 4 only)"* ]]
  ln -s /etc/systemd/system/emulationstation-cpu.service "$CPU_LINK"
  run shared_lines rpi4-static.sh
  [[ $output == *"PASS: emulationstation-cpu.service enabled"* ]]
  run shared_lines vm-static.sh --arch amd64
  [[ $output == *"FAIL: emulationstation-cpu.service not enabled (Pi 4 only)"* ]]
  echo 'ListenStream=1234' >>"$R/etc/systemd/system/emulationstation-cpu.service"
  run shared_lines rpi4-static.sh
  [[ $output == *"FAIL: emulationstation-cpu.service: oneshot priv cpu-apply"* ]]
}
