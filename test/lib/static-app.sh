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

# shellcheck shell=bash
# Shared app-layer checks for test/rpi4-static.sh and test/vm-static.sh (spec 8): SSH, firewall,
# fail2ban, theme, units, first boot files and leftovers. Sourced by both scripts.
# The caller defines R (root of the image), BOOT (boot directory in the image without a leading
# slash, boot/firmware or boot/efi), and the helpers check, is_wanted, has_line, passwd_field and
# root_path.
# The predicates are called indirectly through check():
# shellcheck disable=SC2317

# ini_value <file> <section> <key> : value of "key = value" inside [section]
ini_value() {
  awk -v s="$2" -v k="$3" '
    /^[[:space:]]*\[/ { cur = $0; gsub(/[][[:space:]]/, "", cur); next }
    cur == s {
      line = $0
      sub(/^[[:space:]]+/, "", line)
      if (index(line, k) == 1) {
        rest = substr(line, length(k) + 1)
        if (rest ~ /^[[:space:]]*=/) {
          sub(/^[[:space:]]*=[[:space:]]*/, "", rest)
          sub(/[[:space:]]+$/, "", rest)
          print rest
          found = 1
        }
      }
    }
    END { exit !found }' "$1" 2>/dev/null
}
# ini_is <file> <section> <key> <expected>
ini_is() { [ "$(ini_value "$1" "$2" "$3" || true)" = "$4" ]; }
# jail_ok <jail> <port> <filter> : the jail has the values of the spec (section 5.3)
jail_ok() {
  local f=$R/etc/fail2ban/jail.d/emulationstation.conf
  ini_is "$f" "$1" enabled true &&
    ini_is "$f" "$1" filter "$3" &&
    ini_is "$f" "$1" port "$2" &&
    ini_is "$f" "$1" maxretry 5 &&
    ini_is "$f" "$1" findtime 1h &&
    ini_is "$f" "$1" bantime 1h
}
sshd_line() { has_line etc/ssh/sshd_config.d/10-emulationstation.conf "$1"; }
nft_conf_ok() {
  local f=$R/etc/nftables.conf
  [ -f "$f" ] &&
    grep -qE '^[[:space:]]*tcp dport \{ 11911, 11944 \} accept[[:space:]]*$' "$f" &&
    grep -qE 'hook input priority filter; policy drop;' "$f" &&
    [ "$(grep -c 'dport' "$f")" -eq 1 ]
}
es_theme_ok() {
  local dir
  dir=$(root_path /home/emulationstation/.emulationstation)
  [ -d "$dir/themes/es-epicnoir" ] && [ -n "$(find "$dir/themes/es-epicnoir" -maxdepth 1 -name 'theme.xml' -print -quit 2>/dev/null)" ]
}
es_themeset_ok() {
  local dir
  dir=$(root_path /home/emulationstation/.emulationstation)
  grep -q '<string name="ThemeSet" value="es-epicnoir" />' "$dir/es_settings.cfg" 2>/dev/null
}
# es_owned <path below ~/.emulationstation> : the path and everything below it belong to
# emulationstation (numbers from the image's passwd); the ~/.emulationstation symlink is followed
es_owned() {
  local dir uid gid
  dir=$(root_path /home/emulationstation/.emulationstation)
  uid=$(passwd_field emulationstation 3) && gid=$(passwd_field emulationstation 4) || return 1
  [ -e "$dir/$1" ] && [ -z "$(find "$dir/$1" \( ! -uid "$uid" -o ! -gid "$gid" \) -print -quit 2>/dev/null)" ]
}
# autostart_ok : RetroPie's autostart.sh is the "yes" form (only the es-session exec) or the "no"
# form (the off hint and no exec line), spec 2026-10-31-es-autostart-design.md 3.2
autostart_ok() {
  local f="$R/opt/retropie/configs/all/autostart.sh"
  [ -f "$f" ] || return 1
  [ "$(cat "$f")" = 'exec /usr/local/lib/emulationstation/es-session' ] && return 0
  grep -q 'EmulationStation autostart is off' "$f" && ! grep -qE '^[[:space:]]*exec[[:space:]]' "$f"
}
machine_id_unset() { [ "$(cat "$R/etc/machine-id" 2>/dev/null)" = uninitialized ]; }
no_host_keys() { ! compgen -G "$R/etc/ssh/ssh_host_*" >/dev/null; }
no_ssh_socket() { [ ! -e "$R/etc/systemd/system/sockets.target.wants/ssh.socket" ]; }
no_build_leftovers() {
  [ ! -e "$R/root/provision" ] &&
    [ -z "$(find "$R/root" "$R/tmp" "$R/var/tmp" "$R/home" "$R/etc" -name 'build.env' -print -quit 2>/dev/null)" ]
}
sudoers_ok() {
  local f=$R/etc/sudoers.d/emulationstation
  [ "$(stat -c %a "$f" 2>/dev/null)" = 440 ] &&
    grep -qxF 'emulationstation ALL=(root) NOPASSWD: /usr/sbin/reboot, /usr/sbin/poweroff, /usr/local/lib/emulationstation/priv' "$f"
}
boot_example_ok() {
  [ -s "$R/$BOOT/emulationstation.txt.example" ] &&
    cmp -s "$R/$BOOT/emulationstation.txt.example" "$ROOT/overlay/boot-template/emulationstation.txt.example"
}
firstboot_ok() {
  [ -x "$R/usr/local/lib/emulationstation/firstboot" ] &&
    [ -f "$R/usr/local/lib/emulationstation/firstboot.conf" ] &&
    grep -qx "BOOT_DIR=/$BOOT" "$R/usr/local/lib/emulationstation/firstboot.conf"
}
no_boot_user_file() { [ ! -e "$R/$BOOT/emulationstation.txt" ] && [ ! -e "$R/$BOOT/emulationstation.txt.applied" ]; }

# --- USB import (spec 2026-10-28, section 5) ---
warn() { printf 'WARN: %s\n' "$1"; }
# usb_module_present <release> <module> : a module file or built into the kernel. modinfo reads
# modules.builtin(.modinfo) too; modules.builtin is checked as well for an older kmod.
usb_module_present() {
  modinfo -b "$R" -k "$1" "$2" >/dev/null 2>&1 ||
    grep -q "/$2\.ko" "$R/lib/modules/$1/modules.builtin" 2>/dev/null
}
# usb_reported <release> <module> : the line "usb <module> missing for kernel <release>" is in the
# module report (--report) or in the report that app-35-usb.sh wrote into the image
usb_reported() {
  local f line="usb $2 missing for kernel $1"
  for f in "$REPORT" "$R/usr/local/lib/emulationstation/build-report.txt"; do
    if [ -n "$f" ] && [ -f "$f" ] && sed 's/^[[:space:]]*//; s/[[:space:]]*$//' "$f" | grep -qxF "$line"; then return 0; fi
  done
  return 1
}
# in_image <tool> : an executable file in a normal bin directory of the image
in_image() {
  local d
  for d in usr/local/bin usr/bin bin usr/sbin sbin; do
    if [ -f "$R/$d/$1" ] && [ -x "$R/$d/$1" ]; then return 0; fi
  done
  return 1
}
helper_has_usb_list() { grep -q 'usb-list' "$R/usr/local/lib/emulationstation/priv" 2>/dev/null; }

checks_usb() {
  local d k m t
  for d in "$R"/lib/modules/*/; do
    [ -d "$d/kernel" ] || continue
    k=$(basename "$d")
    for m in vfat exfat ntfs3; do
      if usb_module_present "$k" "$m"; then
        pass "USB: kernel $k has $m"
      elif [ "$m" = ntfs3 ] && usb_reported "$k" "$m"; then
        warn "USB: kernel $k has no ntfs3, listed in the report (NTFS sticks do not work)"
      else
        fail "USB: kernel $k has $m"
      fi
    done
  done
  for t in lsblk jq findmnt mountpoint; do
    check "USB: $t is in the image" in_image "$t"
  done
  check "USB: the helper has usb-list" helper_has_usb_list
}

checks_security() {
  check "sshd drop-in: Port 11911" sshd_line "Port 11911"
  check "sshd drop-in: ListenAddress 0.0.0.0" sshd_line "ListenAddress 0.0.0.0"
  check "sshd drop-in: PermitRootLogin no" sshd_line "PermitRootLogin no"
  check "sshd drop-in: PasswordAuthentication yes" sshd_line "PasswordAuthentication yes"
  check "sshd drop-in: PubkeyAuthentication yes" sshd_line "PubkeyAuthentication yes"
  check "sshd drop-in: MaxAuthTries 5" sshd_line "MaxAuthTries 5"
  check "nftables.conf: policy drop, only tcp 11911 and 11944" nft_conf_ok
  check "fail2ban: backend systemd" ini_is "$R/etc/fail2ban/jail.d/emulationstation.conf" DEFAULT backend systemd
  check "fail2ban: banaction nftables-multiport" ini_is "$R/etc/fail2ban/jail.d/emulationstation.conf" DEFAULT banaction nftables-multiport
  check "fail2ban jail sshd (11911, 5 tries, 1h, 1h)" jail_ok sshd 11911 sshd
  check "fail2ban jail emulationstation-web (11944, 5 tries, 1h, 1h)" jail_ok emulationstation-web 11944 emulationstation-web
  check "fail2ban filter emulationstation-web exists" test -s "$R/etc/fail2ban/filter.d/emulationstation-web.conf"
  check "sudoers: NOPASSWD only for reboot, poweroff and the helper (mode 440)" sudoers_ok
  check "priv helper is executable" test -x "$R/usr/local/lib/emulationstation/priv"
  check "theme es-epicnoir is installed" es_theme_ok
  check "es_settings.cfg: ThemeSet es-epicnoir" es_themeset_ok
  check "es_settings.cfg: owner emulationstation (ES saves its settings)" es_owned es_settings.cfg
  check "theme es-epicnoir: owner emulationstation" es_owned themes/es-epicnoir
}

checks_units() {
  check "ssh.service enabled" is_wanted multi-user.target ssh.service
  check "ssh.socket not enabled" no_ssh_socket
  check "nftables.service enabled" is_wanted multi-user.target nftables.service
  check "fail2ban.service enabled" is_wanted multi-user.target fail2ban.service
  check "emulationstation-firstboot.service enabled" is_wanted multi-user.target emulationstation-firstboot.service
  check "autostart.sh: es-session (yes) or the off hint (no)" autostart_ok
}

checks_firstboot() {
  check "first boot script installed and set to /$BOOT" firstboot_ok
  check "emulationstation.txt.example is on the boot partition (/$BOOT)" boot_example_ok
  check "no emulationstation.txt on the boot partition" no_boot_user_file
  check "machine-id is 'uninitialized' (set at first boot)" machine_id_unset
  check "no SSH host keys in the image" no_host_keys
  check "no /root/provision and no build.env left" no_build_leftovers
}
