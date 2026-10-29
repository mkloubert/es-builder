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

# Builder image for EmuStation Builder. Runs privileged (loop devices) and
# cross-compiles the web UI. Pins: Go, Tailwind standalone CLI (each with SHA256).
FROM debian:trixie

ARG TARGETARCH
ARG GO_VERSION=1.26.10
ARG GO_SHA256_AMD64=ccfe7ae822d2c2f41f4fe7c956199967a4dac22d0ee0d2b2bfdb440aab3b20c5
ARG GO_SHA256_ARM64=3f4227ce7b206a513e794f1393adb473a07b46e94d18bd15d1c684bbb7319797
ARG TAILWIND_VERSION=v4.1.11
ARG TAILWIND_SHA256_X64=64805b84af4292e043ea6f86d242f191c0ac75359c1a498455dfe6c642afdbab
ARG TAILWIND_SHA256_ARM64=0409aa4222969f47fa6f4160fe5387e79bf7269e7afe0e8b22f7532c98e1d314

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      qemu-user-static binfmt-support \
      fdisk parted e2fsprogs zerofree dosfstools xz-utils qemu-utils rsync \
      openssl ca-certificates curl git file jq \
      libxml2-utils xmlstarlet nftables fail2ban \
      gcc libc6-dev \
      bats shellcheck \
 && rm -rf /var/lib/apt/lists/*

RUN set -eu; \
    case "${TARGETARCH:-$(dpkg --print-architecture)}" in \
      amd64) go_arch=amd64; go_sha="$GO_SHA256_AMD64"; tw_arch=x64;   tw_sha="$TAILWIND_SHA256_X64" ;; \
      arm64) go_arch=arm64; go_sha="$GO_SHA256_ARM64"; tw_arch=arm64; tw_sha="$TAILWIND_SHA256_ARM64" ;; \
      *) echo "Unsupported architecture: ${TARGETARCH:-?}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /tmp/go.tgz "https://go.dev/dl/go${GO_VERSION}.linux-${go_arch}.tar.gz"; \
    echo "${go_sha}  /tmp/go.tgz" | sha256sum -c -; \
    tar -C /usr/local -xzf /tmp/go.tgz; \
    rm /tmp/go.tgz; \
    curl -fsSL -o /usr/local/bin/tailwindcss \
      "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-linux-${tw_arch}"; \
    echo "${tw_sha}  /usr/local/bin/tailwindcss" | sha256sum -c -; \
    chmod 0755 /usr/local/bin/tailwindcss

ENV PATH=/usr/local/go/bin:$PATH \
    GOMODCACHE=/cache/go/mod \
    GOCACHE=/cache/go/build \
    GOFLAGS=-buildvcs=false \
    TAILWINDCSS=/usr/local/bin/tailwindcss

WORKDIR /work
