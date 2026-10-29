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

# Pure parts of test/vm-smoke.sh: arguments, accelerator, firmware, QEMU arguments,
# the listener filter, the nft check and the config reader. The QEMU run itself is
# not tested here (it needs QEMU and a built image: see docs/hardware-checklist.md).
# shellcheck disable=SC2030,SC2031,SC2034

setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  # shellcheck source=/dev/null
  . "$ROOT/test/vm-smoke.sh"
  STUBS="$BATS_TEST_TMPDIR/stubs"
  mkdir -p "$STUBS"
  cat >"$BATS_TEST_TMPDIR/nft-good.txt" <<'NFT'
table inet filter {
	chain input {
		type filter hook input priority filter; policy drop;
		iif "lo" accept
		ct state established,related accept
		ct state invalid drop
		meta l4proto icmp accept
		meta l4proto ipv6-icmp accept
		tcp dport { 11911, 11944 } accept
	}
	chain forward {
		type filter hook forward priority filter; policy drop;
	}
	chain output {
		type filter hook output priority filter; policy accept;
	}
}
NFT
}

# --- arguments ----------------------------------------------------------------

@test "parse_args: image and defaults" {
  parse_args out/emulationstation-vm-arm64-20261028.qcow2
  [ "$IMAGE" = out/emulationstation-vm-arm64-20261028.qcow2 ]
  [ "$ARCH" = arm64 ]
  [ "$KEEP" = 0 ]
}

@test "parse_args: --arch wins over the file name" {
  parse_args --arch amd64 out/emulationstation-vm-arm64-20261028.qcow2
  [ "$ARCH" = amd64 ]
}

@test "parse_args: options --config --ca --keep" {
  parse_args --config c.conf --ca ca.crt --keep disk.raw
  [ "$CONFIG" = c.conf ]
  [ "$CA" = ca.crt ]
  [ "$KEEP" = 1 ]
  [ "$IMAGE" = disk.raw ]
}

@test "parse_args: no image, bad arch, unknown option and two images are usage errors (2)" {
  run parse_args
  [ "$status" -eq 2 ]
  run parse_args --arch mips disk.raw
  [ "$status" -eq 2 ]
  run parse_args --bogus disk.raw
  [ "$status" -eq 2 ]
  run parse_args a.raw b.raw
  [ "$status" -eq 2 ]
  run parse_args --arch
  [ "$status" -eq 2 ]
}

@test "normalize_arch: uname names" {
  [ "$(normalize_arch x86_64)" = amd64 ]
  [ "$(normalize_arch aarch64)" = arm64 ]
  [ "$(normalize_arch arm64)" = arm64 ]
}

# --- accelerator ----------------------------------------------------------------

@test "pick_accel: kvm on Linux only for the same architecture and a usable /dev/kvm" {
  [ "$(pick_accel Linux amd64 amd64 1)" = kvm ]
  [ "$(pick_accel Linux amd64 amd64 0)" = tcg ]
  [ "$(pick_accel Linux amd64 arm64 1)" = tcg ]
  [ "$(pick_accel Linux arm64 arm64 1)" = kvm ]
}

@test "pick_accel: hvf on macOS only for the same architecture" {
  [ "$(pick_accel Darwin arm64 arm64 0)" = hvf ]
  [ "$(pick_accel Darwin amd64 amd64 0)" = hvf ]
  [ "$(pick_accel Darwin arm64 amd64 0)" = tcg ]
}

# --- firmware ----------------------------------------------------------------

@test "find_firmware: finds the Debian/Ubuntu OVMF and AAVMF files" {
  FW_ROOT="$BATS_TEST_TMPDIR/fs"
  mkdir -p "$FW_ROOT/usr/share/OVMF" "$FW_ROOT/usr/share/AAVMF"
  : >"$FW_ROOT/usr/share/OVMF/OVMF_CODE_4M.fd"
  : >"$FW_ROOT/usr/share/AAVMF/AAVMF_CODE.fd"
  [ "$(find_firmware amd64)" = "$FW_ROOT/usr/share/OVMF/OVMF_CODE_4M.fd" ]
  [ "$(find_firmware arm64)" = "$FW_ROOT/usr/share/AAVMF/AAVMF_CODE.fd" ]
}

@test "find_firmware: Homebrew share dir (brew --prefix)" {
  FW_ROOT="$BATS_TEST_TMPDIR/fs"
  mkdir -p "$FW_ROOT/opt/hb/share/qemu"
  : >"$FW_ROOT/opt/hb/share/qemu/edk2-aarch64-code.fd"
  printf '#!/bin/sh\necho /opt/hb\n' >"$STUBS/brew"
  chmod +x "$STUBS/brew"
  PATH="$STUBS:$PATH"
  [ "$(find_firmware arm64)" = "$FW_ROOT/opt/hb/share/qemu/edk2-aarch64-code.fd" ]
}

@test "find_firmware: SMOKE_FIRMWARE wins; a missing file is an error" {
  : >"$BATS_TEST_TMPDIR/my.fd"
  SMOKE_FIRMWARE="$BATS_TEST_TMPDIR/my.fd"
  [ "$(find_firmware amd64)" = "$BATS_TEST_TMPDIR/my.fd" ]
  SMOKE_FIRMWARE="$BATS_TEST_TMPDIR/none.fd"
  run find_firmware amd64
  [ "$status" -ne 0 ]
}

@test "find_firmware: nothing found -> error; the hint names the package" {
  FW_ROOT="$BATS_TEST_TMPDIR/empty"
  mkdir -p "$FW_ROOT"
  run find_firmware amd64
  [ "$status" -ne 0 ]
  [[ $(firmware_hint amd64) == *"apt install ovmf"* ]]
  [[ $(firmware_hint arm64) == *"qemu-efi-aarch64"* ]]
  [[ $(firmware_hint arm64) == *"brew install qemu"* ]]
}

# --- QEMU arguments ----------------------------------------------------------

@test "build_qemu_args: amd64 kvm, ports, firmware and disk" {
  build_qemu_args amd64 kvm /fw/OVMF.fd /w/disk.qcow2 qcow2 /w/serial.log
  local all="${QEMU_ARGS[*]}"
  [ "${QEMU_ARGS[0]}" = qemu-system-x86_64 ]
  [[ $all == *"-accel kvm"* ]]
  [[ $all == *"-cpu host"* ]]
  [[ $all == *"-machine q35"* ]]
  [[ $all == *"-bios /fw/OVMF.fd"* ]]
  [[ $all == *"hostfwd=tcp:127.0.0.1:21911-:11911,hostfwd=tcp:127.0.0.1:21944-:11944"* ]]
  [[ $all == *"file=/w/disk.qcow2,format=qcow2,if=virtio"* ]]
  [[ $all == *"-display none"* ]]
}

@test "build_qemu_args: arm64 tcg uses -cpu max and the virt machine" {
  build_qemu_args arm64 tcg /fw/AAVMF.fd /w/disk.raw raw /w/serial.log
  local all="${QEMU_ARGS[*]}"
  [ "${QEMU_ARGS[0]}" = qemu-system-aarch64 ]
  [[ $all == *"-machine virt"* ]]
  [[ $all == *"-cpu max"* ]]
  [[ $all == *"-accel tcg"* ]]
  [[ $all == *"format=raw"* ]]
}

# --- listener filter ---------------------------------------------------------

@test "listen_ports: only non-loopback ports, sorted, unique" {
  run listen_ports <<'EOF'
LISTEN 0 128   0.0.0.0:11911   0.0.0.0:*
LISTEN 0 128      [::]:11911      [::]:*
LISTEN 0 4096        *:11944         *:*
LISTEN 0 4096 127.0.0.54:53    0.0.0.0:*
LISTEN 0 4096 127.0.0.53%lo:53 0.0.0.0:*
LISTEN 0 128      [::1]:631       [::]:*
LISTEN 0 128  127.0.0.1:6010   0.0.0.0:*
EOF
  [ "$status" -eq 0 ]
  [ "$output" = $'11911\n11944' ]
}

@test "listen_ports: an extra port on 0.0.0.0 or an interface address shows up" {
  run listen_ports <<'EOF'
LISTEN 0 128   0.0.0.0:11911   0.0.0.0:*
LISTEN 0 128   0.0.0.0:22      0.0.0.0:*
LISTEN 0 128   10.0.2.15:8080  0.0.0.0:*
LISTEN 0 4096     [::]:11944      [::]:*
EOF
  [ "$output" = $'22\n8080\n11911\n11944' ]
}

@test "listen_ports: ignores a header line; empty input gives no output" {
  run listen_ports <<'EOF'
State  Recv-Q Send-Q Local Address:Port Peer Address:Port
EOF
  [ -z "$output" ]
}

# --- nft check ------------------------------------------------------------------

@test "nft_input_ok: the shipped rule set passes" {
  nft_input_ok <"$BATS_TEST_TMPDIR/nft-good.txt"
}

@test "nft_input_ok: an extra port in the set fails" {
  sed 's/11944 }/11944, 8080 }/' "$BATS_TEST_TMPDIR/nft-good.txt" >"$BATS_TEST_TMPDIR/bad.txt"
  run nft_input_ok <"$BATS_TEST_TMPDIR/bad.txt"
  [ "$status" -ne 0 ]
}

@test "nft_input_ok: an extra accept rule for another port fails" {
  sed 's/^\t\ttcp dport { 11911, 11944 } accept/&\n\t\ttcp dport 22 accept/' "$BATS_TEST_TMPDIR/nft-good.txt" >"$BATS_TEST_TMPDIR/bad.txt"
  grep -q 'dport 22' "$BATS_TEST_TMPDIR/bad.txt"
  run nft_input_ok <"$BATS_TEST_TMPDIR/bad.txt"
  [ "$status" -ne 0 ]
}

@test "nft_input_ok: input policy accept fails" {
  sed 's/hook input priority filter; policy drop;/hook input priority filter; policy accept;/' "$BATS_TEST_TMPDIR/nft-good.txt" >"$BATS_TEST_TMPDIR/bad.txt"
  run nft_input_ok <"$BATS_TEST_TMPDIR/bad.txt"
  [ "$status" -ne 0 ]
}

@test "nft_input_ok: missing web port fails" {
  sed 's/11911, 11944/11911/' "$BATS_TEST_TMPDIR/nft-good.txt" >"$BATS_TEST_TMPDIR/bad.txt"
  run nft_input_ok <"$BATS_TEST_TMPDIR/bad.txt"
  [ "$status" -ne 0 ]
}

# --- config reader -----------------------------------------------------------------

@test "cfg_value: last value wins; quotes and CR are removed; default" {
  printf 'A=1\r\nB="two words"\nA=3\n# C=9\n' >"$BATS_TEST_TMPDIR/c.conf"
  [ "$(cfg_value "$BATS_TEST_TMPDIR/c.conf" A dflt)" = 3 ]
  [ "$(cfg_value "$BATS_TEST_TMPDIR/c.conf" B dflt)" = "two words" ]
  [ "$(cfg_value "$BATS_TEST_TMPDIR/c.conf" C dflt)" = dflt ]
  [ "$(cfg_value "$BATS_TEST_TMPDIR/none.conf" C dflt)" = dflt ]
}

@test "the fixtures fit together: smoke config, boot file, certificate name" {
  local h smoke="$ROOT/test/fixtures/build.conf.smoke" txt="$ROOT/test/fixtures/emulationstation.txt"
  h=$(cfg_value "$txt" HOSTNAME "")
  [ -n "$h" ]
  [[ ,$(cfg_value "$smoke" TLS_EXTRA_SANS ""), == *",$h,"* ]]
  [ "$(cfg_value "$txt" USER_PASSWORD x)" = "$(cfg_value "$smoke" USER_PASSWORD y)" ]
  [ -z "$(cfg_value "$smoke" USER_SSH_PUBKEY "")" ]
}

# --- banner probe --------------------------------------------------------------------

@test "tcp_banner: reads the first line; a closed connection gives nothing" {
  command -v python3 >/dev/null || skip "python3 missing (runs in the builder container)"
  local port
  port=$(python3 -I -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
  python3 -I -c '
import socket, sys
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", int(sys.argv[1]))); s.listen(5)
c, _ = s.accept(); c.sendall(b"SSH-2.0-test\r\n"); c.close()
c, _ = s.accept(); c.close()
' "$port" &
  sleep 1
  [ "$(tcp_banner "$port")" = "SSH-2.0-test" ]
  [ -z "$(tcp_banner "$port")" ]
  wait
}

# --- pflash for code-only firmware ----------------------------------------------------

@test "build_qemu_args: code-only firmware uses pflash with a vars file, no -bios" {
  build_qemu_args amd64 kvm /fw/OVMF_CODE_4M.fd /w/disk.qcow2 qcow2 /w/serial.log /w/vars.fd
  local all="${QEMU_ARGS[*]}"
  [[ $all == *"-drive if=pflash,format=raw,readonly=on,file=/fw/OVMF_CODE_4M.fd"* ]]
  [[ $all == *"-drive if=pflash,format=raw,file=/w/vars.fd"* ]]
  [[ $all != *"-bios"* ]]
  build_qemu_args arm64 tcg /fw/edk2-aarch64-code.fd /w/disk.raw raw /w/serial.log /w/vars.fd
  all="${QEMU_ARGS[*]}"
  [[ $all == *"readonly=on,file=/fw/edk2-aarch64-code.fd"* ]]
  [[ $all != *"-bios"* ]]
}

@test "build_qemu_args: monolithic firmware keeps -bios" {
  build_qemu_args amd64 kvm /fw/OVMF.fd /w/disk.raw raw /w/serial.log /w/vars.fd
  [[ ${QEMU_ARGS[*]} == *"-bios /fw/OVMF.fd"* ]]
  [[ ${QEMU_ARGS[*]} != *pflash* ]]
}

@test "make_vars: copies the matching VARS file, else a zero file of the code size" {
  mkdir -p "$BATS_TEST_TMPDIR/fw"
  printf 'code' >"$BATS_TEST_TMPDIR/fw/OVMF_CODE_4M.fd"
  printf 'vars' >"$BATS_TEST_TMPDIR/fw/OVMF_VARS_4M.fd"
  make_vars "$BATS_TEST_TMPDIR/fw/OVMF_CODE_4M.fd" "$BATS_TEST_TMPDIR/v1.fd"
  [ "$(cat "$BATS_TEST_TMPDIR/v1.fd")" = vars ]
  head -c 1000 /dev/zero >"$BATS_TEST_TMPDIR/fw/edk2-aarch64-code.fd"
  make_vars "$BATS_TEST_TMPDIR/fw/edk2-aarch64-code.fd" "$BATS_TEST_TMPDIR/v2.fd"
  [ "$(wc -c <"$BATS_TEST_TMPDIR/v2.fd" | tr -d ' ')" = 1000 ]
}

@test "find_firmware: Arch monolithic OVMF.4m.fd wins over the CODE file" {
  FW_ROOT="$BATS_TEST_TMPDIR/arch"
  mkdir -p "$FW_ROOT/usr/share/edk2/x64"
  : >"$FW_ROOT/usr/share/edk2/x64/OVMF.4m.fd"
  : >"$FW_ROOT/usr/share/edk2/x64/OVMF_CODE.4m.fd"
  [ "$(find_firmware amd64)" = "$FW_ROOT/usr/share/edk2/x64/OVMF.4m.fd" ]
}
