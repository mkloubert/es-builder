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

# Shared Wi-Fi helpers. Sourced (never run) by the first boot script (firstboot, same directory)
# and by provision/common/app-15-network.sh at build time. Bash 5. Only functions and constants.
# The values may be hostile (emulationstation.txt): they are only written as escaped text.

# NetworkManager connection made by the image (build.conf or emulationstation.txt)
# shellcheck disable=SC2034  # used by the scripts that source this file
NMWIFI_FILE=/etc/NetworkManager/system-connections/emulationstation-wifi.nmconnection

# nmwifi_glib_escape <string> : escapes a string for a GLib key file value (NetworkManager keyfile).
# GLib removes spaces around a value and reads \s \n \t \r \\ as escapes; so \ becomes \\,
# TAB becomes \t, and leading or trailing spaces become \s.
nmwifi_glib_escape() {
  local s=${1//\\/\\\\} pre='' post=''
  s=${s//$'\t'/\\t}
  while [[ $s == ' '* ]]; do s=${s# }; pre+='\s'; done
  while [[ $s == *' ' ]]; do s=${s% }; post+='\s'; done
  printf '%s' "$pre$s$post"
}

# nmwifi_ssid_value <ssid> : text after "ssid=". NetworkManager reads a value made only of digits
# and semicolons as a list of byte values, so such an SSID is written as a byte list.
nmwifi_ssid_value() {
  local s=$1 out='' i
  if [[ $s =~ ^[0-9\;]+$ ]]; then
    for ((i = 0; i < ${#s}; i++)); do out+="$(printf '%d' "'${s:i:1}");"; done
    printf '%s' "$out"
  else
    nmwifi_glib_escape "$s"
  fi
}

# nmwifi_ssid_ok <ssid> : 1 to 32 bytes
nmwifi_ssid_ok() {
  local LC_ALL=C
  [ -n "$1" ] && [ "${#1}" -le 32 ]
}

# nmwifi_psk_ok <password> : 8 to 63 printable ASCII characters, or 64 hex digits
nmwifi_psk_ok() {
  local LC_ALL=C psk=$1
  [[ $psk =~ ^[0-9A-Fa-f]{64}$ ]] || { [ "${#psk}" -ge 8 ] && [ "${#psk}" -le 63 ] && [[ $psk != *[^\ -~]* ]]; }
}

# nmwifi_country_ok <code> : two capital letters (ISO 3166, for example DE, US, GB)
nmwifi_country_ok() { [[ $1 =~ ^[A-Z]{2}$ ]]; }

# nmwifi_write <file> <ssid> <password> : writes the WPA-PSK connection (DHCP, IPv6 auto) with
# mode 0600 through a temporary file. Returns 1 (and leaves no temporary file) on failure.
# The caller checks the values first (nmwifi_ssid_ok, nmwifi_psk_ok).
nmwifi_write() {
  local nm=$1 ssid=$2 psk=$3 tmp
  mkdir -p "$(dirname "$nm")" || return 1
  tmp=$nm.new
  if ! (
    umask 077
    {
      printf '[connection]\nid=emulationstation-wifi\ntype=wifi\nautoconnect=true\n\n'
      printf '[wifi]\nssid=%s\n\n' "$(nmwifi_ssid_value "$ssid")"
      printf '[wifi-security]\nkey-mgmt=wpa-psk\npsk=%s\n\n' "$(nmwifi_glib_escape "$psk")"
      printf '[ipv4]\nmethod=auto\n\n[ipv6]\nmethod=auto\n'
    } >"$tmp"
  ) || ! chmod 0600 "$tmp" || ! mv -f "$tmp" "$nm"; then
    rm -f "$tmp"
    return 1
  fi
}
