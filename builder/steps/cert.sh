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

# Step cert: make (or reuse) the local CA and issue a new server certificate.
# Usage: bash builder/steps/cert.sh [target]; the configuration is already loaded by builder/run.sh if build.conf exists.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# shellcheck source=builder/lib/log.sh
. "$ROOT/builder/lib/log.sh"
# shellcheck source=builder/lib/config.sh
. "$ROOT/builder/lib/config.sh"
# shellcheck source=builder/lib/tls.sh
. "$ROOT/builder/lib/tls.sh"

cd "$ROOT"
if [ -n "${BUILD_CONFIG:-}" ] && [ -f "$BUILD_CONFIG" ]; then
  config_load "$BUILD_CONFIG"
fi
HOST=${CFG_HOSTNAME:-emulationstation}
SANS=${CFG_TLS_EXTRA_SANS:-}

tls_ensure_ca secrets
tls_issue_server secrets out/cert "$HOST" "$SANS"
mkdir -p out
cp secrets/ca.crt out/emulationstation-ca.crt

log_info "New certificate: out/cert/server.crt and out/cert/server.key"
log_info "CA certificate for your browser or OS: out/emulationstation-ca.crt"
cat >&2 <<HINT

To use the new certificate on a running device, run these commands on this computer
(replace <device> with the IP address or name of the device):

  scp -P 11911 out/cert/server.crt out/cert/server.key emulationstation@<device>:
  ssh -t -p 11911 emulationstation@<device> 'sudo install -m 644 -o emulationstation -g emulationstation server.crt /etc/emulationstation-web/tls/server.crt && sudo install -m 600 -o emulationstation -g emulationstation server.key /etc/emulationstation-web/tls/server.key && rm -f server.crt server.key && sudo systemctl restart emulationstation-web'

The files go to the home directory of the user first. The ssh -t option is needed, because sudo asks for the password.
If the install fails, delete server.key in the home directory by hand.
The install command sets owner and group emulationstation and mode 600 on the key.
If you prefer to copy the files directly into /etc/emulationstation-web/tls/, then run on the device:

  sudo chown emulationstation:emulationstation /etc/emulationstation-web/tls/server.key
  sudo chmod 600 /etc/emulationstation-web/tls/server.key
  sudo systemctl restart emulationstation-web
HINT
