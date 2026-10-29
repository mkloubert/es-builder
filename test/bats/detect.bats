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


setup() {
  ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  # shellcheck source=/dev/null
  . "$ROOT/builder/lib/log.sh"
  # shellcheck source=/dev/null
  . "$ROOT/builder/host/detect.sh"
}

@test "Darwin/arm64 -> macos/arm64" {
  uname() { case "$1" in -s) echo Darwin ;; -m) echo arm64 ;; esac; }
  [ "$(host_os)" = macos ]
  [ "$(host_arch)" = arm64 ]
}

@test "Linux/x86_64 -> linux/amd64" {
  uname() { case "$1" in -s) echo Linux ;; -m) echo x86_64 ;; esac; }
  [ "$(host_os)" = linux ]
  [ "$(host_arch)" = amd64 ]
}

@test "Linux/aarch64 -> arm64" {
  uname() { case "$1" in -s) echo Linux ;; -m) echo aarch64 ;; esac; }
  [ "$(host_arch)" = arm64 ]
}

@test "unknown architecture aborts" {
  uname() { echo riscv64; }
  run host_arch
  [ "$status" -ne 0 ]
}

@test "check_docker_space: 20G free, 40 GB needed -> error with GB and hint" {
  docker() { printf 'Filesystem 1G-blocks Used Available Use%% Mounted on\noverlay 60G 40G 20G 67%% /cache\n'; }
  run check_docker_space 40
  [ "$status" -ne 0 ]
  [[ "$output" == *"40 GB"* ]]
  [[ "$output" == *"Disk image size"* ]]
}

@test "check_docker_space: enough space -> success" {
  docker() { printf 'Filesystem 1G-blocks Used Available Use%% Mounted on\noverlay 100G 10G 90G 10%% /cache\n'; }
  run check_docker_space 40
  [ "$status" -eq 0 ]
}

@test "check_docker_space: unreadable output -> error" {
  docker() { echo broken; }
  run check_docker_space 40
  [ "$status" -ne 0 ]
}

@test "ensure_binfmt Linux: existing entry -> no docker call" {
  uname() { case "$1" in -s) echo Linux ;; -m) echo x86_64 ;; esac; }
  docker() { echo "docker called"; return 1; }
  d="$BATS_TEST_TMPDIR/binfmt"; mkdir -p "$d"; : > "$d/qemu-aarch64"
  BINFMT_DIR="$d" run ensure_binfmt arm64
  [ "$status" -eq 0 ]
  [[ "$output" != *"docker called"* ]]
}

@test "ensure_binfmt Linux: missing entry -> tonistiigi/binfmt is installed" {
  uname() { case "$1" in -s) echo Linux ;; -m) echo x86_64 ;; esac; }
  docker() { echo "docker $*" >>"$BATS_TEST_TMPDIR/calls"; }
  d="$BATS_TEST_TMPDIR/binfmt"; mkdir -p "$d"
  BINFMT_DIR="$d" run ensure_binfmt arm64
  [ "$status" -eq 0 ]
  output=$(cat "$BATS_TEST_TMPDIR/calls")
  [[ "$output" == *"tonistiigi/binfmt:qemu-v8.1.5 --install arm64"* ]]
}

@test "ensure_binfmt macOS: checks with docker run --platform" {
  uname() { case "$1" in -s) echo Darwin ;; -m) echo arm64 ;; esac; }
  docker() { echo "docker $*" >>"$BATS_TEST_TMPDIR/calls"; }
  run ensure_binfmt amd64
  [ "$status" -eq 0 ]
  output=$(cat "$BATS_TEST_TMPDIR/calls")
  [[ "$output" == *"--platform linux/amd64 debian:trixie true"* ]]
}

@test "require_docker without docker -> error" {
  uname() { echo Linux; }
  PATH=/nonexistent run require_docker
  [ "$status" -ne 0 ]
  [[ "$output" == *"Docker"* ]]
}
