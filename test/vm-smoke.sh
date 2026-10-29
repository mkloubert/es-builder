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

# VM smoke test: boots a VM image headless in QEMU and checks web UI, SSH, firewall,
# fail2ban and the first boot file.
#
#   test/vm-smoke.sh [--arch amd64|arm64] [--config FILE] [--ca FILE] [--keep] <image.qcow2|image.raw>
#
# This script runs on the HOST (Linux or macOS, Bash 3.2). QEMU runs on the host too,
# so you must install it yourself:
#   Debian/Ubuntu: sudo apt install qemu-system-x86 qemu-system-arm ovmf qemu-efi-aarch64
#   macOS:         brew install qemu
# Also needed on the host: docker (the builder image from ./build.sh), curl, ssh (OpenSSH 8.4+).
#
# Build the image with the smoke test configuration. It sets a known password and puts the
# host name of the boot file into the certificate:
#   ./build.sh vm-amd64 --config test/fixtures/build.conf.smoke
#   test/vm-smoke.sh out/emulationstation-vm-amd64-<date>.qcow2
# --config: the configuration the image was built with (default: test/fixtures/build.conf.smoke).
#   The test reads USER_PASSWORD, WEB_USER, WEB_PASSWORD and TLS_EXTRA_SANS from it.
#   If you build with your own build.conf, the host name in test/fixtures/emulationstation.txt
#   must be in its HOSTNAME or TLS_EXTRA_SANS, or the HTTPS check fails.
# SSH login uses the password (through SSH_ASKPASS), so no key and no sshpass is needed.
#   USER_PASSWORD must not contain a quote character.
# --ca: CA certificate (default: out/emulationstation-ca.crt).
# --keep: do not delete the work directory (out/smoke.*) at the end.
#
# What it does:
#  1. Copies the image to out/smoke.XXXXXX, in the builder container it writes
#     test/fixtures/emulationstation.txt into the EFI partition of the COPY (the original is
#     not changed) and makes sure the disk has 16 GiB.
#  2. Starts QEMU (kvm on Linux if /dev/kvm works, hvf on macOS, otherwise tcg = slow) with
#     the ports 21911 -> 11911 and 21944 -> 11944 on 127.0.0.1. The firmware is OVMF (amd64)
#     or AAVMF (arm64) and is loaded with -bios (monolithic file) or as pflash (code-only file); set SMOKE_FIRMWARE=<file> if it is not found
#     or does not boot (a single-file firmware such as OVMF.fd or QEMU_EFI.fd works best).
#  3. Waits up to 10 minutes (SMOKE_TIMEOUT, in seconds) for the web UI on port 21944.
#  4. Runs the checks and prints a PASS/FAIL summary.
# QEMU is always stopped and the copy is deleted at the end (trap).
# Because of the QEMU user network, the guest sees all connections from 10.0.2.2. The ban
# checks ban this address and unban it again: the SSH ban through the web UI, the web ban
# through SSH.
#
# Exit codes: 0 all checks passed, 1 at least one check failed, 2 usage or missing tool.
# The predicates are called indirectly through check():
# shellcheck disable=SC2317
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

SSH_PORT=21911
WEB_PORT=21944
GUEST_SSH=11911
GUEST_WEB=11944
USER_NAME=emulationstation
CLIENT_IP=10.0.2.2

usage() {
  cat >&2 <<'USAGE'
Usage: test/vm-smoke.sh [--arch amd64|arm64] [--config FILE] [--ca FILE] [--keep] <image.qcow2|image.raw>
  --arch    architecture of the image (default: from the file name, else the host)
  --config  build configuration of the image (default: test/fixtures/build.conf.smoke)
  --ca      CA certificate (default: out/emulationstation-ca.crt)
  --keep    keep the work directory out/smoke.*
USAGE
}

# --- Pure helpers (tested by test/bats/vm-smoke.bats) ----------------------------------

# normalize_arch <uname -m or arch name> : amd64 | arm64
normalize_arch() {
  case "$1" in
    x86_64 | amd64) echo amd64 ;;
    aarch64 | arm64) echo arm64 ;;
    *) echo "$1" ;;
  esac
}

# parse_args <args...> : sets IMAGE ARCH CONFIG CA KEEP; usage error = exit 2
parse_args() {
  IMAGE=""
  ARCH=""
  CONFIG="$ROOT/test/fixtures/build.conf.smoke"
  CA="$ROOT/out/emulationstation-ca.crt"
  KEEP=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --arch)
        [ $# -ge 2 ] || {
          usage
          exit 2
        }
        ARCH=$2
        shift
        ;;
      --config)
        [ $# -ge 2 ] || {
          usage
          exit 2
        }
        CONFIG=$2
        shift
        ;;
      --ca)
        [ $# -ge 2 ] || {
          usage
          exit 2
        }
        CA=$2
        shift
        ;;
      --keep) KEEP=1 ;;
      -h | --help)
        usage
        exit 0
        ;;
      -*)
        usage
        exit 2
        ;;
      *)
        [ -z "$IMAGE" ] || {
          usage
          exit 2
        }
        IMAGE=$1
        ;;
    esac
    shift
  done
  [ -n "$IMAGE" ] || {
    usage
    exit 2
  }
  if [ -z "$ARCH" ]; then
    case "$(basename "$IMAGE")" in
      *-vm-arm64-*) ARCH=arm64 ;;
      *-vm-amd64-*) ARCH=amd64 ;;
      *) ARCH=$(normalize_arch "$(uname -m)") ;;
    esac
  fi
  case "$ARCH" in
    amd64 | arm64) ;;
    *)
      usage
      exit 2
      ;;
  esac
}

# pick_accel <OS (uname -s)> <host arch> <guest arch> <kvm usable 0|1> : kvm | hvf | tcg
# Hardware acceleration only works when guest and host have the same CPU architecture.
pick_accel() {
  if [ "$2" = "$3" ]; then
    case "$1" in
      Linux)
        if [ "$4" = 1 ]; then
          echo kvm
          return 0
        fi
        ;;
      Darwin)
        echo hvf
        return 0
        ;;
    esac
  fi
  echo tcg
}

# kvm_usable : 1 if /dev/kvm can be opened for reading and writing, else 0
kvm_usable() {
  if [ -r /dev/kvm ] && [ -w /dev/kvm ]; then echo 1; else echo 0; fi
}

# firmware_candidates <arch> : UEFI firmware files to try, one per line. FW_ROOT (tests only)
# is put in front of the absolute paths.
firmware_candidates() {
  local brew=""
  if command -v brew >/dev/null 2>&1; then brew=$(brew --prefix 2>/dev/null || true); fi
  case "$1" in
    amd64)
      printf '%s\n' \
        /usr/share/ovmf/OVMF.fd \
        /usr/share/edk2/x64/OVMF.4m.fd \
        /usr/share/qemu/OVMF.fd \
        /usr/share/OVMF/OVMF_CODE_4M.fd \
        /usr/share/OVMF/OVMF_CODE.fd \
        /usr/share/edk2/x64/OVMF_CODE.4m.fd
      if [ -n "$brew" ]; then printf '%s\n' "$brew/share/qemu/edk2-x86_64-code.fd"; fi
      ;;
    arm64)
      printf '%s\n' \
        /usr/share/qemu-efi-aarch64/QEMU_EFI.fd \
        /usr/share/AAVMF/AAVMF_CODE.fd \
        /usr/share/edk2/aarch64/QEMU_EFI-pflash.raw \
        /usr/share/edk2/aarch64/QEMU_CODE.fd \
        /usr/share/qemu/edk2-aarch64-code.fd
      if [ -n "$brew" ]; then printf '%s\n' "$brew/share/qemu/edk2-aarch64-code.fd"; fi
      ;;
  esac
}

# find_firmware <arch> : prints the first firmware file that exists. SMOKE_FIRMWARE wins.
find_firmware() {
  local f
  if [ -n "${SMOKE_FIRMWARE:-}" ]; then
    [ -f "$SMOKE_FIRMWARE" ] || return 1
    printf '%s\n' "$SMOKE_FIRMWARE"
    return 0
  fi
  while IFS= read -r f; do
    if [ -f "${FW_ROOT:-}$f" ]; then
      printf '%s\n' "${FW_ROOT:-}$f"
      return 0
    fi
  done < <(firmware_candidates "$1")
  return 1
}

# firmware_hint <arch> : what to install when no firmware was found
firmware_hint() {
  case "$1" in
    amd64) echo "UEFI firmware for amd64 not found. Install it: Debian/Ubuntu: sudo apt install ovmf; macOS: brew install qemu. Or set SMOKE_FIRMWARE=<a monolithic OVMF.fd>. (Fedora: its OVMF_CODE.fd needs SMM and is not used; set SMOKE_FIRMWARE to a non-SMM file.)" ;;
    arm64) echo "UEFI firmware for arm64 not found. Install it: Debian/Ubuntu: sudo apt install qemu-efi-aarch64; macOS: brew install qemu. Or set SMOKE_FIRMWARE=<a monolithic QEMU_EFI.fd>." ;;
  esac
}

# fw_is_split <firmware file> : 0 if it is a code-only image (needs pflash with a vars file)
fw_is_split() {
  case "$(basename "$1")" in
    *CODE* | *-code.fd) return 0 ;;
  esac
  return 1
}

# make_vars <firmware file> <dest> : the variable store for a split firmware. A VARS file next
# to the code file is copied (OVMF_CODE_4M.fd -> OVMF_VARS_4M.fd, edk2-x86_64-code.fd ->
# edk2-i386-vars.fd is not guessed); otherwise a zero-filled file of the code size is used
# (this is what the aarch64 virt machine needs).
make_vars() {
  local fw=$1 dest=$2 dir base cand size
  dir=$(dirname "$fw")
  base=$(basename "$fw")
  cand=$dir/$(printf '%s' "$base" | sed 's/CODE/VARS/; s/-code/-vars/')
  if [ "$cand" != "$fw" ] && [ -f "$cand" ]; then
    cp "$cand" "$dest"
    return 0
  fi
  size=$(wc -c <"$fw" | tr -d ' ')
  dd if=/dev/null of="$dest" bs=1 count=0 seek="$size" 2>/dev/null
}

# build_qemu_args <arch> <accel> <firmware> <disk> <disk format> <serial log> [vars file]
# sets QEMU_ARGS. A split firmware (code only) is loaded as pflash with the vars file;
# a monolithic firmware is loaded with -bios.
build_qemu_args() {
  local arch=$1 accel=$2 fw=$3 disk=$4 fmt=$5 serial=$6 vars=${7:-} cpu machine bin
  case "$accel" in
    kvm | hvf) cpu=host ;;
    *) cpu=max ;;
  esac
  case "$arch" in
    amd64)
      bin=qemu-system-x86_64
      machine=q35
      ;;
    arm64)
      bin=qemu-system-aarch64
      machine=virt
      ;;
  esac
  QEMU_ARGS=("$bin"
    -machine "$machine" -accel "$accel" -cpu "$cpu" -m 2048 -smp 2
    -drive "file=$disk,format=$fmt,if=virtio"
    -netdev "user,id=n0,hostfwd=tcp:127.0.0.1:$SSH_PORT-:$GUEST_SSH,hostfwd=tcp:127.0.0.1:$WEB_PORT-:$GUEST_WEB"
    -device "virtio-net-pci,netdev=n0"
    -device virtio-gpu-pci
    -display none
    -serial "file:$serial")
  if fw_is_split "$fw"; then
    QEMU_ARGS+=(-drive "if=pflash,format=raw,readonly=on,file=$fw" -drive "if=pflash,format=raw,file=$vars")
  else
    QEMU_ARGS+=(-bios "$fw")
  fi
}

# listen_ports : reads `ss -tlnH` output, prints the ports that listen on a non-loopback
# address (127.0.0.0/8 and ::1 are ignored), sorted, unique
listen_ports() {
  awk '$1 == "State" { next }
    NF >= 5 {
      a = $4
      i = match(a, /:[^:]*$/)
      if (i == 0) next
      addr = substr(a, 1, i - 1)
      port = substr(a, i + 1)
      if (addr ~ /^127\./ || addr == "[::1]" || addr == "::1") next
      print port
    }' | sort -un
}

# nft_input_ok : reads `nft list table inet filter`. Passes if the input chain has policy drop
# and the only port rule is "tcp dport { 11911, 11944 } accept".
nft_input_ok() {
  awk '
    /hook input/ { if ($0 ~ /policy drop;/) pol = 1 }
    /dport/ {
      line = $0
      gsub(/^[ \t]+|[ \t]+$/, "", line)
      n++
      if (line == "tcp dport { 11911, 11944 } accept") good++
    }
    END { exit !(pol == 1 && n == 1 && good == 1) }'
}

# cfg_value <file> <key> <default> : value of KEY=VALUE in a build.conf style file
# (last line wins, CR and one pair of quotes removed); the default if the key is missing
cfg_value() {
  local line v
  line=$(grep -E "^$2=" "$1" 2>/dev/null | tail -n 1) || true
  if [ -z "$line" ]; then
    printf '%s\n' "$3"
    return 0
  fi
  v=${line#*=}
  v=${v%$'\r'}
  case "$v" in
    \"*\")
      v=${v#\"}
      v=${v%\"}
      ;;
    \'*\')
      v=${v#\'}
      v=${v%\'}
      ;;
  esac
  printf '%s\n' "$v"
}

# tcp_banner <port> : first line the server on 127.0.0.1:<port> sends (5 s), empty if the
# connection fails or is closed at once. QEMU accepts the connection on the host even if the
# guest refuses it, so "connect works" says nothing; the SSH banner does.
tcp_banner() {
  (
    exec 3<>"/dev/tcp/127.0.0.1/$1" || exit 0
    line=""
    read -r -t 5 line <&3 || true
    printf '%s\n' "${line%$'\r'}"
  ) 2>/dev/null || true
}

# --- Container part: write emulationstation.txt into the EFI partition ----------------

# inject_main <arch> <raw|qcow2> <workdir> <txt> : runs in the builder container as root.
# raw: changes <workdir>/disk.raw in place, then makes it 16 GiB.
# qcow2: converts <workdir>/disk.qcow2 to a temporary raw file, writes the file, converts back.
inject_main() {
  local arch=$1 fmt=$2 dir=$3 txt=$4 tmp img i
  # shellcheck source=../builder/lib/log.sh
  . "$ROOT/builder/lib/log.sh"
  # shellcheck source=../builder/lib/image.sh
  . "$ROOT/builder/lib/image.sh"
  TARGET_MNT=${STATIC_MNT:-/mnt/vm-smoke}
  cleanup_install
  case "$fmt" in
    raw) img=$dir/disk.raw ;;
    qcow2)
      tmp=$(mktemp -d /cache/smoke.XXXXXX)
      cleanup_push rm -rf "$tmp"
      log_info "Converting the qcow2 copy to raw ..."
      qemu-img convert -O raw "$dir/disk.qcow2" "$tmp/disk.raw"
      img=$tmp/disk.raw
      ;;
  esac
  mount_target "$img" "vm-$arch"
  cp "$txt" "$TARGET_MNT/boot/efi/emulationstation.txt"
  sync
  log_info "Wrote emulationstation.txt to the EFI partition"
  # mount_target registered four entries (2 x loop device, 2 x mount): release them now
  for i in 1 2 3 4; do cleanup_pop; done
  case "$fmt" in
    raw) truncate -s 16G "$img" ;;
    qcow2)
      log_info "Converting back to qcow2 ..."
      qemu-img convert -O qcow2 "$img" "$dir/disk.new.qcow2"
      qemu-img resize "$dir/disk.new.qcow2" 16G >/dev/null
      mv "$dir/disk.new.qcow2" "$dir/disk.qcow2"
      ;;
  esac
  if [[ ${HOST_UID:-} =~ ^[0-9]+$ && ${HOST_GID:-} =~ ^[0-9]+$ ]] && [ "$HOST_UID" -ne 0 ]; then
    chown -R "$HOST_UID:$HOST_GID" "$dir"
  fi
}

# --- Host part -------------------------------------------------------------------------

WORK=""
QEMU_PID=""
RESULTS=""
FAILS=0
CHECKS=0

cleanup() {
  local rc=$?
  trap - EXIT INT TERM
  if [ -n "$QEMU_PID" ] && kill -0 "$QEMU_PID" 2>/dev/null; then
    kill "$QEMU_PID" 2>/dev/null || true
    sleep 1
    kill -9 "$QEMU_PID" 2>/dev/null || true
    wait "$QEMU_PID" 2>/dev/null || true
  fi
  if [ -n "$WORK" ] && [ -d "$WORK" ]; then
    if [ "$KEEP" = 1 ]; then
      echo "Work directory kept: $WORK" >&2
    else
      rm -rf "$WORK"
    fi
  fi
  exit "$rc"
}

say() { printf '%s\n' "$*" >&2; }
fatal() {
  say "ERROR: $*"
  exit 2
}

# check <description> <command...> : runs the command; records PASS or FAIL
check() {
  local desc=$1
  shift
  CHECKS=$((CHECKS + 1))
  if "$@"; then
    RESULTS="${RESULTS}PASS: $desc
"
    say "PASS: $desc"
  else
    RESULTS="${RESULTS}FAIL: $desc
"
    say "FAIL: $desc"
    FAILS=$((FAILS + 1))
  fi
}

# gssh <command> : runs a command in the guest as the user, password login through SSH_ASKPASS
gssh() {
  SSH_ASKPASS="$ASKPASS_OK" SSH_ASKPASS_REQUIRE=force DISPLAY=${DISPLAY:-none:0} \
    ssh -p "$SSH_PORT" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    -o PreferredAuthentications=password -o PubkeyAuthentication=no \
    -o NumberOfPasswordPrompts=1 -o ConnectTimeout=10 -o LogLevel=ERROR \
    "$USER_NAME@127.0.0.1" "$@" </dev/null
}

# gssh_as <user> <askpass script> <command> : same, other user or password
gssh_as() {
  local user=$1 askpass=$2
  shift 2
  SSH_ASKPASS="$askpass" SSH_ASKPASS_REQUIRE=force DISPLAY=${DISPLAY:-none:0} \
    ssh -p "$SSH_PORT" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    -o PreferredAuthentications=password -o PubkeyAuthentication=no \
    -o NumberOfPasswordPrompts=1 -o ConnectTimeout=10 -o LogLevel=ERROR \
    "$user@127.0.0.1" "$@" </dev/null
}

# gsudo <command> : command with sudo in the guest (the sudo password is the user password)
gsudo() {
  gssh "echo '$USER_PASSWORD' | sudo -S -p '' $*"
}

# web_code <user> <password> : HTTP status of GET / through the forwarded port; 000 = no answer
web_code() {
  curl -sS -o /dev/null -w '%{http_code}' --max-time 10 --cacert "$CA" \
    --resolve "$BOOT_HOST:$WEB_PORT:127.0.0.1" -u "$1:$2" "https://$BOOT_HOST:$WEB_PORT/" 2>/dev/null || true
}

# web_ready : the web UI answers over HTTPS (certificate is not checked here: check 1 does that)
web_ready() {
  local code
  code=$(curl -ksS -o /dev/null -w '%{http_code}' --max-time 5 \
    --resolve "$BOOT_HOST:$WEB_PORT:127.0.0.1" "https://$BOOT_HOST:$WEB_PORT/" 2>/dev/null || true)
  [ "$code" = 401 ] || [ "$code" = 200 ]
}

# wait_for <seconds> <command...> : repeat the command each second until it succeeds
wait_for() {
  local n=$1 i=0
  shift
  while [ "$i" -lt "$n" ]; do
    if "$@"; then return 0; fi
    sleep 1
    i=$((i + 1))
  done
  return 1
}

web_banned() { [ "$(web_code "$WEB_USER" "$WEB_PASSWORD")" = 000 ]; }
ssh_banned() { [ -z "$(tcp_banner "$SSH_PORT")" ]; }
ssh_open() { case "$(tcp_banner "$SSH_PORT")" in SSH-*) return 0 ;; esac; return 1; }
web_open() { [ "$(web_code "$WEB_USER" "$WEB_PASSWORD")" = 200 ]; }

# --- the checks ---

c_web_ok() {
  local code
  code=$(web_code "$WEB_USER" "$WEB_PASSWORD")
  [ "$code" = 200 ] || {
    say "  HTTP $code (the CA must be $CA; the certificate must contain $BOOT_HOST)"
    return 1
  }
}
c_web_wrong() { [ "$(web_code "$WEB_USER" "wrong-password")" = 401 ]; }
c_ssh_ok() { [ "$(gssh 'echo ok' 2>/dev/null)" = ok ]; }
c_ssh_root() { ! gssh_as root "$ASKPASS_OK" true >/dev/null 2>&1; }
c_nft() { gsudo nft list table inet filter | nft_input_ok; }
c_ports() {
  local got
  got=$(gssh 'ss -tlnH' | listen_ports | tr '\n' ' ')
  [ "$got" = "$GUEST_SSH $GUEST_WEB " ] || {
    say "  non-loopback listeners: $got"
    return 1
  }
}
c_es_running() { gssh "pgrep -f '^/opt/retropie/supplementary/emulationstation/emulationstation' >/dev/null"; }
c_hostname() { [ "$(gssh hostname)" = "$BOOT_HOST" ]; }
c_applied() {
  gssh "test -f /boot/efi/emulationstation.txt.applied && ! test -e /boot/efi/emulationstation.txt && grep -q '^USER_PASSWORD=\*\*\*\$' /boot/efi/emulationstation.txt.applied && ! grep -qF '$USER_PASSWORD' /boot/efi/emulationstation.txt.applied"
}

# c_ssh_ban : 5 failed SSH logins -> the next connection is refused; unban through the web UI
c_ssh_ban() {
  local i
  ssh_open || return 1
  for i in 1 2 3 4 5; do
    gssh_as "$USER_NAME" "$ASKPASS_BAD" true >/dev/null 2>&1 || true
  done
  wait_for 20 ssh_banned || {
    say "  SSH was still open 20 s after 5 failed logins"
    return 1
  }
  curl -sS -o /dev/null --max-time 10 --cacert "$CA" --resolve "$BOOT_HOST:$WEB_PORT:127.0.0.1" \
    -u "$WEB_USER:$WEB_PASSWORD" -X POST -d "ip=$CLIENT_IP" "https://$BOOT_HOST:$WEB_PORT/system/unban" || return 1
  wait_for 20 ssh_open
}

# c_web_ban : 5 failed web logins (one was made by check 2) -> no answer any more;
# while banned, restart nftables and see that the fail2ban table and the ban survive (check 7)
c_web_ban() {
  local i
  for i in 1 2 3 4; do
    web_code "$WEB_USER" "wrong-password" >/dev/null
  done
  wait_for 20 web_banned || {
    say "  the web UI still answered 20 s after 5 failed logins"
    return 1
  }
  BAN_SEEN=1
  return 0
}
c_f2b_survives() {
  gsudo systemctl restart nftables || return 1
  sleep 2
  gsudo nft list table inet f2b-table >/dev/null || {
    say "  table inet f2b-table is gone after the restart of nftables"
    return 1
  }
  web_banned
}
c_web_unban() {
  gsudo /usr/local/lib/emulationstation/priv f2b-unban "$CLIENT_IP" >/dev/null || return 1
  wait_for 20 web_open
}

# stage_image : copies the image into $WORK and writes the boot file in the builder container
stage_image() {
  local fmt
  case "$IMAGE" in
    *.qcow2) fmt=qcow2 ;;
    *.raw | *.img) fmt=raw ;;
    *) fatal "Unknown image type (use .qcow2 or .raw): $IMAGE" ;;
  esac
  DISK_FMT=$fmt
  DISK=$WORK/disk.$fmt
  say "Copying the image ..."
  cp "$IMAGE" "$DISK"
  docker image inspect emulationstation-builder >/dev/null 2>&1 ||
    fatal "The builder image is missing. Run ./build.sh test once (or any build) first."
  say "Writing emulationstation.txt into the EFI partition of the copy (builder container) ..."
  docker run --rm --privileged -e "HOST_UID=$(id -u)" -e "HOST_GID=$(id -g)" \
    -v emulationstation-cache:/cache -v "$ROOT:/work" -w /work emulationstation-builder \
    test/vm-smoke.sh --inject-esp "$ARCH" "$fmt" "/work/${WORK#"$ROOT"/}" /work/test/fixtures/emulationstation.txt ||
    fatal "Could not write emulationstation.txt into the image copy."
}

write_askpass() {
  # write_askpass <file> <password>
  printf '#!/bin/sh\necho %s\n' "'$2'" >"$1"
  chmod 700 "$1"
}

run_smoke() {
  local missing="" bin accel fw host_os host_arch t0 waited timeout

  command -v curl >/dev/null 2>&1 || missing="$missing curl"
  command -v ssh >/dev/null 2>&1 || missing="$missing ssh"
  command -v docker >/dev/null 2>&1 || missing="$missing docker"
  case "$ARCH" in
    amd64) bin=qemu-system-x86_64 ;;
    arm64) bin=qemu-system-aarch64 ;;
  esac
  command -v "$bin" >/dev/null 2>&1 || missing="$missing $bin"
  if [ -n "$missing" ]; then
    fatal "Missing tools:$missing. Install QEMU: Debian/Ubuntu: sudo apt install qemu-system-x86 qemu-system-arm; macOS: brew install qemu."
  fi
  fw=$(find_firmware "$ARCH") || fatal "$(firmware_hint "$ARCH")"
  [ -f "$IMAGE" ] || fatal "Image not found: $IMAGE"
  [ -f "$CA" ] || fatal "CA certificate not found: $CA (./build.sh writes it to out/)"
  [ -f "$CONFIG" ] || fatal "Configuration not found: $CONFIG (the one the image was built with)"

  USER_PASSWORD=$(cfg_value "$CONFIG" USER_PASSWORD "")
  [ -n "$USER_PASSWORD" ] || fatal "USER_PASSWORD is empty in $CONFIG"
  case "$USER_PASSWORD" in
    *\'*) fatal "USER_PASSWORD must not contain a quote character for the smoke test" ;;
  esac
  WEB_USER=$(cfg_value "$CONFIG" WEB_USER zocken)
  WEB_PASSWORD=$(cfg_value "$CONFIG" WEB_PASSWORD zocken)
  BOOT_HOST=$(cfg_value "$ROOT/test/fixtures/emulationstation.txt" HOSTNAME "")
  [ -n "$BOOT_HOST" ] || fatal "HOSTNAME is missing in test/fixtures/emulationstation.txt"

  host_os=$(uname -s)
  host_arch=$(normalize_arch "$(uname -m)")
  accel=$(pick_accel "$host_os" "$host_arch" "$ARCH" "$(kvm_usable)")
  if [ "$accel" = tcg ]; then
    say "WARNING: no hardware acceleration for $ARCH on this $host_os/$host_arch host. QEMU uses software emulation (tcg)."
    say "WARNING: the boot can take much longer than 10 minutes. Raise SMOKE_TIMEOUT (seconds) if it times out."
  fi

  mkdir -p "$ROOT/out"
  WORK=$(mktemp -d "$ROOT/out/smoke.XXXXXX")
  trap cleanup EXIT INT TERM
  ASKPASS_OK=$WORK/askpass-ok
  ASKPASS_BAD=$WORK/askpass-bad
  write_askpass "$ASKPASS_OK" "$USER_PASSWORD"
  write_askpass "$ASKPASS_BAD" "wrong-password"

  stage_image

  if fw_is_split "$fw"; then make_vars "$fw" "$WORK/vars.fd"; fi
  build_qemu_args "$ARCH" "$accel" "$fw" "$DISK" "$DISK_FMT" "$WORK/serial.log" "$WORK/vars.fd"
  say "Starting QEMU ($accel, $ARCH) ..."
  "${QEMU_ARGS[@]}" >"$WORK/qemu.log" 2>&1 &
  QEMU_PID=$!

  timeout=${SMOKE_TIMEOUT:-600}
  say "Waiting up to $timeout s for the web UI on port $WEB_PORT ..."
  t0=$(date +%s)
  waited=0
  while ! web_ready; do
    if ! kill -0 "$QEMU_PID" 2>/dev/null; then
      say "QEMU stopped early:"
      tail -n 20 "$WORK/qemu.log" >&2 || true
      QEMU_PID=""
      check "QEMU keeps running until the web UI answers" false
      finish
    fi
    waited=$(($(date +%s) - t0))
    if [ "$waited" -ge "$timeout" ]; then
      say "Last lines of the serial console:"
      tail -n 30 "$WORK/serial.log" >&2 || true
      check "Web UI answers on port $WEB_PORT within $timeout s" false
      finish
    fi
    sleep 3
  done
  say "Web UI answers after $waited s."

  check "1. HTTPS $GUEST_WEB with valid auth -> 200 (certificate checked with the CA)" c_web_ok
  check "2. Wrong web password -> 401" c_web_wrong
  check "3. SSH login on $GUEST_SSH works" c_ssh_ok
  check "3. SSH login as root is rejected" c_ssh_root
  check "4. nft input chain: policy drop, only 11911 and 11944 accepted" c_nft
  check "4. Only ports 11911 and 11944 listen on non-loopback addresses" c_ports
  check "5. EmulationStation process runs" c_es_running
  check "6. Host name from emulationstation.txt is set ($BOOT_HOST)" c_hostname
  check "6. emulationstation.txt.applied exists, password masked" c_applied
  check "SSH ban: 5 failed logins -> refused; unban in the web UI" c_ssh_ban
  BAN_SEEN=0
  check "Web ban: 5 failed logins -> no answer" c_web_ban
  if [ "$BAN_SEEN" = 1 ]; then
    check "7. nftables restart keeps table inet f2b-table and the ban" c_f2b_survives
  else
    check "7. nftables restart keeps table inet f2b-table (no ban to test: skipped as FAIL)" false
  fi
  check "Web unban through SSH (priv f2b-unban) -> web UI answers again" c_web_unban
  finish
}

finish() {
  say ""
  say "==== Summary ($CHECKS checks) ===="
  printf '%s' "$RESULTS" >&2
  if [ "$FAILS" -gt 0 ]; then
    say "FAILED: $FAILS check(s)."
    if [ -n "$WORK" ] && [ -f "$WORK/serial.log" ]; then
      say "Serial console (last 15 lines):"
      tail -n 15 "$WORK/serial.log" >&2 || true
    fi
    exit 1
  fi
  say "All checks passed."
  exit 0
}

main() {
  if [ "${1:-}" = "--inject-esp" ]; then
    shift
    [ $# -eq 4 ] || fatal "--inject-esp is internal (arch fmt workdir txt)"
    inject_main "$@"
    return 0
  fi
  parse_args "$@"
  run_smoke
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
