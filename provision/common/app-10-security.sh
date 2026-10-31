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

# App layer, in the chroot: security. SSH, firewall, fail2ban, sudo helper.
# The files come from overlay/ (already copied by step 40). This script
#  - sets owner and mode of the overlay files and checks the sudoers file with visudo,
#  - enables ssh, nftables and fail2ban (systemctl enable works offline in a chroot),
#  - makes sure sshd uses port 11911 (no ssh.socket on port 22) and has no host keys yet,
#  - removes services that listen on the network (avahi, cups, rpcbind, triggerhappy, exim4).
# Only 11911 (SSH) and 11944 (web UI) may listen. The smoke test (Task 12) checks this on a real boot.
# Idempotent. PROVISION_ROOT (tests only): path prefix.
set -euo pipefail

log() { printf '[app-10-security] %s\n' "$*" >&2; }
die() {
  printf '[app-10-security] ERROR: %s\n' "$*" >&2
  exit 1
}

R=${PROVISION_ROOT:-}

# --- Owner and mode of the overlay files ------------------------------------------
# set_file <mode> <path> : root:root, given mode; the file must exist
set_file() {
  [ -f "$R$2" ] || die "Overlay file is missing: $2"
  chown root:root "$R$2"
  chmod "$1" "$R$2"
}

set_file 0644 /etc/ssh/sshd_config.d/10-emulationstation.conf
set_file 0644 /etc/nftables.conf
set_file 0644 /etc/fail2ban/jail.d/emulationstation.conf
set_file 0644 /etc/fail2ban/filter.d/emulationstation-web.conf
set_file 0644 /etc/systemd/system/fail2ban.service.d/after-nftables.conf
set_file 0644 /etc/systemd/system/nftables.service.d/keep-f2b.conf
set_file 0644 /etc/systemd/system/ssh.socket.d/10-emulationstation.conf
set_file 0644 /etc/systemd/resolved.conf.d/10-emulationstation.conf
set_file 0755 /usr/local/lib/emulationstation/priv
set_file 0440 /etc/sudoers.d/emulationstation
# A broken sudoers file would lock out sudo completely, so stop the build here.
visudo -cf "$R/etc/sudoers.d/emulationstation" >/dev/null || die "sudoers file is invalid"
log "Overlay files: owner and mode set, sudoers file is valid"

# Other NOPASSWD rules would weaken "sudo needs the password" (warning only)
other=$(grep -rlE '^[^#]*NOPASSWD' "$R/etc/sudoers" "$R/etc/sudoers.d" 2>/dev/null |
  grep -v '/etc/sudoers.d/emulationstation$' || true)
if [ -n "$other" ]; then
  log "WARNING: other NOPASSWD rules found in: $(printf '%s' "$other" | tr '\n' ' ')"
fi

# --- SSH ------------------------------------------------------------------------
# Debian (bookworm and later) can run sshd with socket activation: ssh.socket (off by default)
# listens on port 22 and ignores "Port" in sshd_config. We want ssh.service with our config
# (port 11911), so the socket is disabled. The drop-in ssh.socket.d moves it to 11911 if it is
# enabled by mistake.
systemctl disable ssh.socket >/dev/null 2>&1 || true
systemctl enable ssh.service
# No host keys in the image: every system makes its own at first boot (ssh-keygen -A).
rm -f "$R"/etc/ssh/ssh_host_*
log "ssh.service enabled, ssh.socket disabled, no host keys"

# --- Firewall and fail2ban -------------------------------------------------------
systemctl enable nftables.service
systemctl enable fail2ban.service
log "nftables and fail2ban enabled"

# --- Services that listen on the network -----------------------------------------
# installed <package> : the package is installed (not only configuration files left)
installed() {
  dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q '^install ok installed$'
}

purge=()
for p in avahi-daemon cups cups-daemon cups-browsed rpcbind triggerhappy exim4 exim4-base exim4-daemon-light; do
  if installed "$p"; then purge+=("$p"); fi
done
if [ "${#purge[@]}" -gt 0 ]; then
  log "Removing: ${purge[*]}"
  apt-get -y purge "${purge[@]}"
fi
# Mask the units, so nothing starts them again (for example after a later package install)
systemctl mask avahi-daemon.service avahi-daemon.socket cups.service cups.socket cups.path \
  cups-browsed.service rpcbind.service rpcbind.socket triggerhappy.service triggerhappy.socket \
  exim4.service >/dev/null
log "Services masked: avahi, cups, rpcbind, triggerhappy, exim4"
# systemd-resolved: LLMNR/mDNS are switched off by resolved.conf.d/10-emulationstation.conf
