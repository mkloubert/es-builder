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

# Host detection and Docker checks. Runs under Bash 3.2 (macOS) and Linux.
# Expects builder/lib/log.sh to be loaded.
set -euo pipefail

BINFMT_IMAGE="tonistiigi/binfmt:qemu-v8.1.5"
: "${BINFMT_DIR:=/proc/sys/fs/binfmt_misc}"

# host_os -> linux|macos
host_os() {
  case "$(uname -s)" in
    Linux) echo linux ;;
    Darwin) echo macos ;;
    *) log_error "Unsupported operating system: $(uname -s)"; return 1 ;;
  esac
}

# host_arch -> amd64|arm64
host_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo amd64 ;;
    arm64 | aarch64) echo arm64 ;;
    *) log_error "Unsupported architecture: $(uname -m)"; return 1 ;;
  esac
}

require_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    log_error "Docker was not found. Please install Docker (Linux: Docker Engine; macOS: Docker Desktop, OrbStack or Colima)."
    return 1
  fi
  if ! docker info >/dev/null 2>&1; then
    log_error "Docker does not respond. Please start the Docker daemon or Docker Desktop/OrbStack/Colima and check your access rights."
    return 1
  fi
}

# ensure_binfmt <amd64|arm64> : makes sure containers of this architecture can run.
ensure_binfmt() {
  local arch=$1 name
  case "$arch" in
    arm64) name=qemu-aarch64 ;;
    amd64) name=qemu-x86_64 ;;
    *) log_error "Unknown architecture '$arch'"; return 1 ;;
  esac
  if [ "$(host_os)" = linux ]; then
    if [ -e "$BINFMT_DIR/$name" ]; then
      return 0
    fi
    log_info "Registering QEMU binfmt for $arch (tonistiigi/binfmt) ..."
    docker run --privileged --rm "$BINFMT_IMAGE" --install "$arch" >/dev/null ||
      { log_error "binfmt registration for $arch failed."; return 1; }
  else
    if ! docker run --rm --platform "linux/$arch" debian:trixie true >/dev/null 2>&1; then
      log_error "Docker cannot start linux/$arch containers. Docker Desktop: enable Settings -> General -> 'Use Rosetta/QEMU emulation'; OrbStack/Colima: emulation is usually built in (Colima: check --arch or --vz-rosetta)."
      return 1
    fi
  fi
}

# check_docker_space <GB> : free space in the volume emulationstation-cache
check_docker_space() {
  local need=$1 out avail
  out=$(docker run --rm -v emulationstation-cache:/cache debian:trixie df -BG /cache 2>/dev/null) ||
    { log_error "Could not determine the free Docker storage."; return 1; }
  avail=$(printf '%s\n' "$out" | tail -n 1 | awk '{ gsub(/G/, "", $4); print $4 }')
  case "$avail" in
    "" | *[!0-9]*)
      log_error "Cannot read the output of 'df': $out"
      return 1
      ;;
  esac
  if [ "$avail" -lt "$need" ]; then
    log_error "Not enough Docker storage: ${avail} GB free, ${need} GB needed. Increase Docker storage: Docker Desktop: Settings -> Resources -> Disk image size; OrbStack: Settings -> System (storage grows automatically, check the host disk); Colima: 'colima start --disk <GB>' (or 'colima stop && colima start --disk 100')."
    return 1
  fi
  log_info "Docker storage ok: ${avail} GB free (${need} GB needed)."
}
