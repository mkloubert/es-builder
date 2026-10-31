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

# Step 40: provision the app layer. Copies the final base layer to the work image
# ($CACHE_DIR/work/<target>.img), mounts it, copies overlay/ into the root, stages provision/,
# controllers/ and the build values in /root/provision, runs provision/common/app-*.sh and then
# provision/<kind>/app-*.sh (each sorted) in the chroot, removes /root/provision and unmounts.
# The work image is finished by later steps (Task 11).
# Usage: bash builder/steps/40-provision-app.sh <rpi4|vm-amd64|vm-arm64>; configuration from $BUILD_CONFIG.
#
# Secrets (ruling R18): passwords and keys are never passed as command-line arguments or
# environment variables. They are written to the 0600 file /root/provision/build.env and read
# by the app scripts with config_load (builder/lib/config.sh, copied to /root/provision/lib).
# The file is shredded and removed together with /root/provision, also when the step aborts.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
# shellcheck source=builder/lib/log.sh
. "$ROOT/builder/lib/log.sh"
# shellcheck source=builder/lib/config.sh
. "$ROOT/builder/lib/config.sh"
# shellcheck source=builder/lib/image.sh
. "$ROOT/builder/lib/image.sh"
# shellcheck source=builder/lib/chroot.sh
. "$ROOT/builder/lib/chroot.sh"
# shellcheck source=builder/lib/cache.sh
. "$ROOT/builder/lib/cache.sh"
# shellcheck source=builder/lib/tls.sh
. "$ROOT/builder/lib/tls.sh"

# Values the app scripts may need (all CFG_* keys without the upstream pins and image URLs).
APP_ENV_KEYS="USER_PASSWORD USER_SSH_PUBKEY WEB_USER WEB_PASSWORD SCREENSCRAPER_USER \
SCREENSCRAPER_PASSWORD HOSTNAME LOCALE TIMEZONE KEYMAP WIFI_SSID WIFI_PASSWORD WIFI_COUNTRY \
TLS_EXTRA_SANS SOURCE_URL ES_AUTOSTART"

# app_write_env <file> : KEY="VALUE" lines, mode 0600 from the start. Values are always
# quoted: config_load removes exactly one pair of quotes, so a value that itself starts and
# ends with a quote stays intact.
app_write_env() {
  local file=$1 k var
  for k in $APP_ENV_KEYS; do
    var="CFG_$k"
    case "${!var-}" in
      *$'\n'* | *$'\r'*) die "$k must not contain a line break" ;;
    esac
  done
  (
    umask 077
    : >"$file"
    for k in $APP_ENV_KEYS; do
      var="CFG_$k"
      printf '%s="%s"\n' "$k" "${!var-}" >>"$file"
    done
  ) || return 1
  chmod 0600 "$file"
}

# app_shred_env <file> : overwrite build.env before it is deleted (ruling R24); the deleted
# blocks are also zeroed by 50-finalize.sh (zerofree)
app_shred_env() {
  if [ -e "$1" ]; then
    shred -u "$1" || rm -f "$1"
  fi
}

# app_copy_overlay <overlay-dir> <root> : copies the tree with owner root:root and the modes of
# the repository files. Existing directories in the target keep their owner and mode.
# (tar: the repository checkout may belong to another user.)
app_copy_overlay() {
  local src=$1 dst=$2
  [ -d "$src" ] || die "overlay directory is missing: $src"
  tar -C "$src" --owner=0 --group=0 --numeric-owner -cf - . |
    tar -C "$dst" --no-overwrite-dir --numeric-owner -xpf -
}

# app_stage_provision <root> <kind> : <root>/root/provision with the scripts, the lib, build.env and
# controllers/ (templates and custom)
app_stage_provision() {
  local prov="${1:?}/root/provision" kind=${2:?}
  rm -rf "${prov:?}"
  mkdir -p "$prov/lib"
  chmod 0700 "$prov"
  cp -a "$ROOT/provision/common" "$prov/common"
  if [ -d "$ROOT/provision/$kind" ]; then
    cp -a "$ROOT/provision/$kind" "$prov/$kind"
  else
    mkdir -p "$prov/$kind"
  fi
  # the base scripts are not needed (and not run) in the app layer
  rm -f "${prov:?}"/common/base-*.sh "${prov:?}/${kind:?}"/base-*.sh
  # controller templates and own exports for app-50-controllers.sh
  mkdir -p "$prov/controllers"
  cp -a "$ROOT/controllers/templates" "$prov/controllers/templates"
  if [ -d "$ROOT/controllers/custom" ]; then
    cp -a "$ROOT/controllers/custom" "$prov/controllers/custom"
  else
    mkdir -p "$prov/controllers/custom"
  fi
  cp "$ROOT/builder/lib/log.sh" "$ROOT/builder/lib/config.sh" "$ROOT/builder/lib/webconf.sh" "$prov/lib/"
  app_write_env "$prov/build.env"
}

# app_build_web_and_tls <arch> : runs on the build host (builder container) after the overlay is
# copied and before the chroot scripts.
#  - builds the web binary for <arch> (arm64|amd64) with webui/scripts/build.sh (from a temporary
#    copy of webui/, see app_build_web) into
#    $OUT_TMP (default $ROOT/out/tmp) and installs it as $TARGET_MNT/usr/local/bin/emulationstation-web (0755),
#  - makes (or reuses) the local CA in $ROOT/secrets, issues the server certificate for
#    the host name into a temporary directory and installs server.key/server.crt in
#    $TARGET_MNT/etc/emulationstation-web/tls,
#  - copies the CA certificate (not the key) to $ROOT/out/emulationstation-ca.crt.
# Owner and mode of the web files are set later by provision/common/app-30-webui.sh in the chroot
# (the user emulationstation does not exist on the host). The CA key is never copied to the target.
# app_build_web <arch> <out> : builds the web binary from a temporary copy of webui/. The build
# writes internal/web/static/app.css; in the repository (a bind mount) that file would then belong
# to root on a Linux host, and a later local build would fail.
app_build_web() {
  local arch=${1:?} out=${2:?} tmp="${OUT_TMP:-$ROOT/out/tmp}" src
  mkdir -p "$tmp"
  src=$(mktemp -d "$tmp/webui-src.XXXXXX")
  cleanup_push rm -rf "$src"
  cp -a "$ROOT/webui/." "$src/"
  sh "$src/scripts/build.sh" "$arch" "$out" ||
    die "Building the web service failed (webui/scripts/build.sh $arch)"
  rm -rf "${src:?}"
}

app_build_web_and_tls() {
  local arch=${1:?} tmp="${OUT_TMP:-$ROOT/out/tmp}" tlsdir tls_tmp
  local etc="${TARGET_MNT:?}/etc/emulationstation-web"
  mkdir -p "$tmp" "$ROOT/out"
  log_info "Building the web service for $arch"
  app_build_web "$arch" "$tmp/emulationstation-web-$arch"
  install -D -m 0755 -o root -g root "$tmp/emulationstation-web-$arch" "$TARGET_MNT/usr/local/bin/emulationstation-web"

  tls_ensure_ca "$ROOT/secrets"
  tls_tmp=$(mktemp -d "$tmp/tls.XXXXXX")
  tlsdir="$tls_tmp/out"
  cleanup_push rm -rf "$tls_tmp"
  tls_issue_server "$ROOT/secrets" "$tlsdir" "${CFG_HOSTNAME:?}" "${CFG_TLS_EXTRA_SANS:-}"
  install -d -m 0750 -o root -g root "$etc" "$etc/tls"
  install -m 0600 -o root -g root "$tlsdir/server.key" "$etc/tls/server.key"
  install -m 0644 -o root -g root "$tlsdir/server.crt" "$etc/tls/server.crt"
  rm -rf "${tls_tmp:?}"
  cp "$ROOT/secrets/ca.crt" "$ROOT/out/emulationstation-ca.crt"
  log_info "CA certificate for your browser or OS: out/emulationstation-ca.crt"
}

main() {
  export LC_COLLATE=C
  local target=${1:-} kind arch base work prov_rel=root/provision prov s
  [ -n "$target" ] || die "Usage: $0 <target>" 2
  [ -n "${BUILD_CONFIG:-}" ] || die "BUILD_CONFIG is not set" 2
  cleanup_install
  config_load "$BUILD_CONFIG"
  config_require USER_PASSWORD || die "USER_PASSWORD is required for the app layer"

  kind=$(target_kind "$target")
  arch=$(target_arch "$target")
  base=$(cache_base_path "$target")
  [ -f "$base" ] || die "base layer missing: $base (step 30-provision-base.sh)"

  work=$(cache_work_path "$target")
  mkdir -p "$(dirname "$work")"
  rm -f "${work:?}"
  log_info "Copying the base layer to $work"
  cp --sparse=always "$base" "$work"

  mount_target "$work" "$target"
  chroot_prepare "$TARGET_MNT"
  prov="${TARGET_MNT:?}/$prov_rel"
  # cleanup stack (LIFO): shred build.env, provision directory (also on abort), then chroot, then mounts
  cleanup_push rm -rf "$prov"
  cleanup_push app_shred_env "$prov/build.env"

  log_info "Copying overlay/"
  app_copy_overlay "$ROOT/overlay" "$TARGET_MNT"
  app_stage_provision "$TARGET_MNT" "$kind"
  app_build_web_and_tls "$arch"

  local scripts=()
  shopt -s nullglob
  for s in "$prov"/common/app-*.sh; do scripts+=("common/${s##*/}"); done
  for s in "$prov/$kind"/app-*.sh; do scripts+=("$kind/${s##*/}"); done
  shopt -u nullglob

  for s in "${scripts[@]}"; do
    log_info "Provisioning: $s"
    chroot_run env "CFG_TARGET=$target" "CFG_TARGET_KIND=$kind" "CFG_TARGET_ARCH=$arch" \
      "CFG_EPICNOIR_REF=${CFG_EPICNOIR_REF:?}" "PROVISION_DIR=/$prov_rel" bash "/$prov_rel/$s" || die "Provisioning failed: $s"
  done

  # Tear down explicitly (provision directory with build.env, chroot, umount, loop)
  # shellcheck disable=SC2119  # without exit code: returns instead of exiting
  cleanup_run || die "Cleanup failed; the work image may still hold /root/provision"
  log_info "App layer ready: $work"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
