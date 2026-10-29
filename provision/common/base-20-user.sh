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

# Base layer, in the chroot: user emulationstation (groups per spec 5.1), password LOCKED.
# The app layer sets password and SSH key (app-05-identity.sh), because they are not part of
# the base cache key. Removes the Raspberry Pi OS first-user mechanism:
# users pi and rpi-first-boot-wizard, userconfig.service (masked) and what
# "rename-user -f -s" (pi-gen export-image/01-user-rename) leaves behind: getty@tty1 disabled,
# sshd banner rename_user.conf, piwiz autostart, sudoers entries. Idempotent.
set -euo pipefail

log() { printf '[base-20-user] %s\n' "$*" >&2; }

U=emulationstation
GROUPS_WANTED="sudo video render input audio bluetooth"

# Remove old users first, so emulationstation gets UID 1000
for old in pi rpi-first-boot-wizard; do
  if getent passwd "$old" >/dev/null; then
    log "Removing user $old"
    userdel -r "$old" 2>/dev/null || userdel "$old"
    rm -rf "/home/${old:?}"
  fi
  if getent group "$old" >/dev/null; then
    groupdel "$old" 2>/dev/null || true
  fi
done
rm -f /etc/sudoers.d/010_pi-nopasswd /etc/sudoers.d/010_wiz-nopasswd \
  /etc/xdg/autostart/piwiz.desktop /etc/ssh/sshd_config.d/rename_user.conf
rm -rf /var/lib/userconf-pi

# Create groups that are missing; bluetooth normally comes from bluez (base-10)
for g in $GROUPS_WANTED; do
  if ! getent group "$g" >/dev/null; then
    log "Creating system group $g"
    groupadd -r "$g"
  fi
done

if getent passwd "$U" >/dev/null; then
  log "User $U exists, setting groups/shell"
  usermod -s /bin/bash -d "/home/$U" "$U"
else
  log "Creating user $U"
  useradd -m -d "/home/$U" -s /bin/bash -U "$U"
fi
for g in $GROUPS_WANTED; do
  usermod -aG "$g" "$U"
done
mkdir -p "/home/$U"
chown "$U:$U" "/home/$U"

# Locked until app-05-identity.sh sets the password; root stays locked
passwd -l "$U" >/dev/null
passwd -l root >/dev/null

# Setup wizard off, console on tty1 on again (rename-user had disabled it)
ln -sf /dev/null /etc/systemd/system/userconfig.service
systemctl enable getty@tty1.service
