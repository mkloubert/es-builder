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

# Base layer, in the chroot: install RetroPie-Setup at CFG_RETROPIE_SETUP_REF and its modules.
#
# Checked at pin f5ebfba6 (RetroPie-Setup 4.8.12):
# - retropie_packages.sh takes the user from SUDO_USER (must exist) and must run as root;
#   home comes from ~$SUDO_USER, not from $HOME (HOME=/root as under sudo).
# - Platform: get_platform() detects it from /proc/cpuinfo or the device tree - in the chroot these
#   are the data of the build host, so __platform is preset (rpi4). platform_rpi4 calls
#   cpu_armv8, which sets the flag aarch64 when "getconf LONG_BIT" = 64; so rpi4 is also correct for
#   64-bit. In the chroot (__chroot=1) get_rpi_video sets KMS (mesa kms) for rpi4.
# - OS check (scriptmodules/system.sh get_os_version): Debian/Raspbian is rejected only below 8;
#   trixie (13) is allowed. A rejection ends in fatalError (exit 1) during
#   setup_env, so before any module. Prebuilt binaries exist only for Debian 10-12
#   (__has_binaries), for trixie every module is built from source.
# - A call without a mode ("retropie_packages.sh <module>") = _source_:
#   depends sources build install configure clean (if install is missing, install_bin is used).
#   _auto_ would be equivalent on trixie, but also needs a route to the internet
#   ("ip route get"), which is unreliable under qemu-user in the chroot.
# - Exit: 0 ok, 1 error (also __ERRMSGS), 2 module unknown, 3 not available for the platform.
# - __nodialog=1, so no dialog boxes are opened on /dev/tty.
#
# Core modules (retroarch emulationstation runcommand) must succeed, otherwise abort. Then
# skyscraper and the modules from <kind>/modules.list: error -> line in the report, continue.
# Environment: CFG_RETROPIE_SETUP_REF, CFG_RETROPIE_PLATFORM (empty = auto-detect),
# CFG_RETROPIE_CPU_FLAGS and CFG_RETROPIE_KMS=1 (optional, VM only),
# CFG_TARGET_KIND, PROVISION_DIR, PROVISION_REPORT. PROVISION_ROOT (tests only): path prefix.
#
# Space: _source_ stops at the first error, "clean" does not run then and the build tree
# ($__builddir/<module> = RetroPie-Setup/tmp/build/<module>, up to GB for big cores) would stay in the
# image. So delete it after every failure, and RetroPie-Setup/tmp completely at the end; after every
# module log "df -h /".
#
# dpkg: RetroPie's own apt calls get --force-confdef/--force-confold through a temporary apt.conf.d
# file, modules get no stdin, and after every failed module dpkg is repaired (dpkg_repair). If dpkg
# stays broken, the script stops, so a broken base layer is never cached.
set -euo pipefail

log() { printf '[base-30-retropie] %s\n' "$*" >&2; }
die() {
  printf '[base-30-retropie] ERROR: %s\n' "$*" >&2
  exit 1
}

U=emulationstation
R=${PROVISION_ROOT:-}
RP=$R/home/$U/RetroPie-Setup
OPT=$R/opt/retropie
LOW_KB=$((1024 * 1024))
RP_URL=https://github.com/RetroPie/RetroPie-Setup.git
ref=${CFG_RETROPIE_SETUP_REF:?CFG_RETROPIE_SETUP_REF is missing}
platform=${CFG_RETROPIE_PLATFORM:-}
kind=${CFG_TARGET_KIND:?CFG_TARGET_KIND is missing}
report=${PROVISION_REPORT:?PROVISION_REPORT is missing}
modlist=${PROVISION_DIR:?PROVISION_DIR is missing}/$kind/modules.list
CORE="retroarch emulationstation runcommand"

getent passwd "$U" >/dev/null || die "User $U is missing (base-20-user.sh)"

# --- apt/dpkg for the RetroPie modules --------------------------------------------
# RetroPie calls apt-get itself without conffile options. A conffile question would wait for an
# answer (or fail without a terminal). This file gives every apt call of this script the same
# options as our own calls. It is removed at the end, also when the script aborts.
APT_CONF=$R/etc/apt/apt.conf.d/90-emulationstation-build
mkdir -p "$(dirname "$APT_CONF")"
printf 'Dpkg::Options { "--force-confdef"; "--force-confold"; };\n' >"$APT_CONF"
trap 'rm -f "$APT_CONF"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# dpkg_repair : a module that fails in the middle of an apt call can leave dpkg half done
# ("dpkg was interrupted"). Then every later apt call fails, also in the app layer. So repair
# after every failed module and once at the end. If dpkg is still broken, stop: a broken base
# layer must never be saved in the cache.
dpkg_repair() {
  local audit
  log "Checking and repairing the dpkg state ($1)"
  dpkg --configure -a </dev/null || true
  apt-get -f install -y -q -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold </dev/null || true
  audit=$(dpkg --audit </dev/null 2>&1 || true)
  if [ -n "$audit" ]; then
    printf '%s\n' "$audit" >&2
    die "dpkg is still broken after the repair ($1) - the base layer is not saved"
  fi
}

# --- Fetch RetroPie-Setup ----------------------------------------------------
git_rp() { git -c safe.directory="$RP" -C "$RP" "$@"; }
if [ ! -d "$RP/.git" ]; then
  rm -rf "$RP"
  log "Cloning RetroPie-Setup"
  git clone -q "$RP_URL" "$RP"
fi
if ! git_rp rev-parse -q --verify "$ref^{commit}" >/dev/null; then
  git_rp fetch -q --tags origin
fi
git_rp checkout -q --detach "$ref"
log "RetroPie-Setup at $(git_rp rev-parse HEAD)"
chown -R "$U:$U" "$RP"

# rp [module [mode]] : retropie_packages.sh as root for user emulationstation
rp() {
  local penv=()
  [ -z "$platform" ] || penv=("__platform=$platform")
  # VM: generic CPU flags instead of -march=native, and KMS (see 30-provision-base.sh)
  [ -z "${CFG_RETROPIE_CPU_FLAGS:-}" ] || penv+=("__cpu_flags=$CFG_RETROPIE_CPU_FLAGS")
  [ "${CFG_RETROPIE_KMS:-}" != 1 ] || penv+=("__has_kms=1")
  (cd "$RP" && env SUDO_USER="$U" __nodialog=1 "${penv[@]}" bash ./retropie_packages.sh "$@")
}

avail_kb() { df -Pk "${R:-/}" | awk 'NR == 2 { print $4 }'; }

report_fail() { printf '%s %s %s\n' "$1" "$2" "$3" >>"$report"; }

hint_for() {
  case "$1" in
    2) echo "module unknown in RetroPie-Setup" ;;
    3) echo "not available for platform ${platform:-auto}" ;;
    *)
      if [ "$(avail_kb)" -lt "$LOW_KB" ]; then
        echo "installation failed, little space left on / ($(avail_kb) KiB free), see log 30-provision-base"
      else
        echo "installation failed, see log 30-provision-base"
      fi
      ;;
  esac
}

# --- OS/environment check ---------------------------------------------------
# Without arguments only setup_env runs (platform, OS version, base packages) + help output.
log "Checking RetroPie-Setup environment (platform ${platform:-auto})"
rc=0
out=$(rp 2>&1 </dev/null) || rc=$?
if [ "$rc" -ne 0 ]; then
  printf '%s\n' "$out" | tail -n 40 >&2
  if grep -qE 'Unsupported OS|You need .* or newer|We do not support' <<<"$out"; then
    die "RetroPie rejects trixie – please ask the user, spec section 9"
  fi
  die "RetroPie-Setup cannot be initialised (exit $rc), see the output above"
fi

install_module() {
  local m=$1 rc=0
  log "Installing module $m"
  # no stdin: nothing in a module build may wait for an answer
  rp "$m" </dev/null || rc=$?
  if [ "$rc" -ne 0 ] && [ -e "$RP/tmp/build/$m" ]; then
    log "Removing build tree of $m"
    rm -rf "${RP:?}/tmp/build/${m:?}"
  fi
  df -h "${R:-/}" >&2 || true
  return "$rc"
}

# --- Core modules --------------------------------------------------------------
for m in $CORE; do
  rc=0
  install_module "$m" || rc=$?
  if [ "$rc" -ne 0 ]; then
    report_fail "$m" "$rc" "$(hint_for "$rc")"
    die "Core module $m failed (exit $rc) – aborting the base layer"
  fi
done
[ -x "$OPT/supplementary/emulationstation/emulationstation" ] ||
  die "EmulationStation binary is missing after the installation"

# --- Scraper -----------------------------------------------------------------
rc=0
install_module skyscraper || rc=$?
if [ "$rc" -ne 0 ]; then
  report_fail skyscraper "$rc" "$(hint_for "$rc")"
  dpkg_repair "after skyscraper"
fi

# --- Emulators --------------------------------------------------------------
[ -f "$modlist" ] || die "Module list is missing: $modlist"
while IFS= read -r m || [ -n "$m" ]; do
  m=${m%%#*}
  m=${m//[[:space:]]/}
  [ -n "$m" ] || continue
  rc=0
  install_module "$m" || rc=$?
  if [ "$rc" -ne 0 ]; then
    report_fail "$m" "$rc" "$(hint_for "$rc")"
    log "Module $m failed (exit $rc), continuing"
    dpkg_repair "after $m"
  elif [ ! -d "$OPT/libretrocores/$m" ] && [ ! -d "$OPT/emulators/$m" ]; then
    report_fail "$m" 0 "no installation directory under /opt/retropie"
  fi
done <"$modlist"

dpkg_repair "end of the RetroPie modules"

# Do not keep leftovers (build trees, downloads) in the image
rm -rf "${RP:?}/tmp"
df -h "${R:-/}" >&2 || true
log "RetroPie done"
