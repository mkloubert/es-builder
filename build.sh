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

# Entry point: ./build.sh <rpi4|vm-amd64|vm-arm64|all|cert|test> [--rebuild-base] [--base-only] [--config FILE]
# Runs on the host (Linux/macOS, Bash 3.2). builder/run.sh does the real work inside the container.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")" && pwd)
cd "$ROOT"

# shellcheck source=builder/lib/log.sh
. "$ROOT/builder/lib/log.sh"
# shellcheck source=builder/lib/config.sh
. "$ROOT/builder/lib/config.sh"
# shellcheck source=builder/host/detect.sh
. "$ROOT/builder/host/detect.sh"

usage() {
  cat >&2 <<'USAGE'
Usage: ./build.sh <target> [options]

Targets:
  rpi4        Raspberry Pi 4 image
  vm-amd64    UEFI VM image (amd64)
  vm-arm64    UEFI VM image (arm64)
  all         all three images, one after the other
  cert        issue a new server certificate from the existing CA
  test        shellcheck, bats and Go tests in the builder container

Options:
  --rebuild-base   rebuild the base layer in the cache
  --base-only      build only the base layer (development/tests)
  --config FILE    configuration file (default: ./build.conf)
USAGE
}

TARGET=""
CONFIG="$ROOT/build.conf"
PASS_OPTS=""

while [ $# -gt 0 ]; do
  case "$1" in
    -h | --help)
      usage
      exit 0
      ;;
    --rebuild-base | --base-only)
      PASS_OPTS="$PASS_OPTS $1"
      ;;
    --config)
      [ $# -ge 2 ] || { usage; die "--config needs a file name" 2; }
      CONFIG=$2
      shift
      ;;
    --config=*)
      CONFIG=${1#--config=}
      ;;
    -*)
      usage
      die "Unknown option: $1" 2
      ;;
    *)
      [ -z "$TARGET" ] || { usage; die "Only one target allowed (already '$TARGET')" 2; }
      TARGET=$1
      ;;
  esac
  shift
done

case "$TARGET" in
  rpi4) NEED_GB=40 ;;
  vm-amd64 | vm-arm64) NEED_GB=20 ;;
  all) NEED_GB=80 ;;
  cert | test) NEED_GB=0 ;;
  "")
    usage
    exit 2
    ;;
  *)
    usage
    die "Unknown target: $TARGET" 2
    ;;
esac

# Configuration (required only for image targets)
CONFIG_ABS=""
case "$TARGET" in
  rpi4 | vm-amd64 | vm-arm64 | all)
    [ -f "$CONFIG" ] || die "Configuration '$CONFIG' is missing. Please run 'cp build.conf.example build.conf' and edit it." 2
    config_load "$CONFIG"
    config_require USER_PASSWORD || exit 2
    ;;
  cert)
    if [ -f "$CONFIG" ]; then config_load "$CONFIG"; fi
    ;;
esac
if [ -f "$CONFIG" ]; then
  CONFIG_ABS=$(cd "$(dirname "$CONFIG")" && pwd)/$(basename "$CONFIG")
fi

require_docker || exit 1

HOST_ARCH=$(host_arch)
needs_arch=""
case "$TARGET" in
  rpi4 | vm-arm64) needs_arch="arm64" ;;
  vm-amd64) needs_arch="amd64" ;;
  all) needs_arch="arm64 amd64" ;;
esac
for a in $needs_arch; do
  if [ "$a" != "$HOST_ARCH" ]; then
    ensure_binfmt "$a" || exit 1
  fi
done

if [ "$NEED_GB" -gt 0 ]; then
  check_docker_space "$NEED_GB" || exit 1
fi

log_info "Building builder image 'emulationstation-builder' ..."
docker build -t emulationstation-builder builder/

mkdir -p out secrets

# HOST_UID/HOST_GID: run.sh gives out/ and secrets/ to this user at the end (Linux: the
# container writes as root). Numbers only, so the unquoted use below is safe.
# --init: a small init is PID 1 and passes Ctrl+C / docker stop on to run.sh, which passes it
# on to the running step, so the step can clean up (loop devices, mounts).
# -i -t only on a terminal: then Ctrl+C goes to the container like a normal program.
DOCKER_ARGS="--rm --privileged --init -e HOST_UID=$(id -u) -e HOST_GID=$(id -g)"
if [ -t 0 ] && [ -t 1 ]; then
  DOCKER_ARGS="$DOCKER_ARGS -i -t"
fi
RUN_ARGS=("$TARGET")
for o in $PASS_OPTS; do RUN_ARGS+=("$o"); done

# build.conf goes to /config, not below /run: the container's /run is bind-mounted into the
# chroot, and the build scripts of other projects must not see the secrets.
CONFIG_MOUNT=()
if [ -n "$CONFIG_ABS" ]; then
  RUN_ARGS+=(--config /config/build.conf)
  CONFIG_MOUNT=(-v "$CONFIG_ABS:/config/build.conf:ro")
fi
log_info "Starting target '$TARGET' in the builder container ..."
# ${CONFIG_MOUNT[@]+...}: Bash 3.2 treats an empty array as unset under set -u
# shellcheck disable=SC2086
exec docker run $DOCKER_ARGS \
  -v emulationstation-cache:/cache -v "$PWD:/work" -w /work \
  ${CONFIG_MOUNT[@]+"${CONFIG_MOUNT[@]}"} \
  emulationstation-builder builder/run.sh "${RUN_ARGS[@]}"
