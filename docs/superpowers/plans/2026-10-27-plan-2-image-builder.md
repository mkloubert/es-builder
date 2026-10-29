# Plan 2: Image Builder & Target System – Implementation Plan

License: AGPL-3.0-or-later, see LICENSE

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `./build.sh rpi4|vm-amd64|vm-arm64|all|cert|test` builds bootable images (Raspberry Pi 4 and UEFI VM) on Linux/macOS (amd64/arm64) with RetroPie EmulationStation, EpicNoir, hardened SSH/firewall/fail2ban, first-boot configuration, controller templates and the web service from Plan 1.

**Architecture:** A thin host script (`build.sh`, Bash 3.2 compatible) checks the environment and starts a privileged Docker builder. Inside it, official upstream images are mounted through loop devices (using byte offsets, without udev) and provisioned by chroot (binfmt/QEMU for a foreign arch) in two layers: a cached base layer (packages, RetroPie, emulators) and a fast app layer (overlay, configuration, web service).

**Tech Stack:** Bash, Docker, `debian:trixie` builder with `qemu-user-static`, `sfdisk`, `e2fsprogs`, `dosfstools`, `xz-utils`, `qemu-utils`, `openssl`, `shellcheck`, `bats`, Go 1.26.x, Tailwind standalone CLI v4.1.11; target: Raspberry Pi OS Lite trixie arm64, Debian 13 nocloud; RetroPie-Setup, nftables, fail2ban, BlueZ, xpadneo.

**Spec:** `docs/superpowers/specs/2026-10-27-emulationstation-image-design.md` · **Prerequisite:** Plan 1 (`docs/superpowers/plans/2026-10-27-plan-1-webui.md`) is implemented.

## Global Constraints

- **No git commands that write.** "Checkpoint" = list the changed files for the user.
- All code, comments, messages, UI and docs in simple English.
- Host-side scripts (`build.sh`, `builder/host/*.sh`) run under **Bash 3.2** (macOS): no associative arrays, no `mapfile`, no `${v,,}`. Everything in the container may use Bash 5.
- All scripts: `#!/usr/bin/env bash`, `set -euo pipefail`, clean under `shellcheck`.
- Ports: SSH `11911`, web `11944`; incoming only these + ICMP/ICMPv6.
- User `emulationstation`, groups `sudo video render input audio bluetooth`; root login locked.
- fail2ban jails `sshd` and `emulationstation-web`: `maxretry = 5`, `findtime = 1h`, `bantime = 1h`, `backend = systemd`, `banaction = nftables-multiport`.
- Web service contracts from Plan 1 (file contract `/run/emulationstation/*`, helper subcommands, CLI `credentials`/`guid`, log line `auth failure from <ip>`) apply unchanged.
- Upstream pins are in `build.conf`; checksums in the format `sha256:<hex>` or `sha512:<hex>` (Debian publishes only `SHA512SUMS`).
- Outputs: `out/emulationstation-rpi4-<YYYYMMDD>.img.xz` (+`.sha256`), `out/emulationstation-vm-<arch>-<YYYYMMDD>.qcow2` and `.raw`, `out/emulationstation-ca.crt`, `out/logs/`, `out/build-report.txt`.
- The CA lives only in `secrets/` (gitignored); never in the image.

### Deviations from the spec (to be confirmed by the user)

1. **Cache in a Docker volume instead of `cache/`:** Loop devices on files in macOS bind mounts (virtiofs) are unreliable and slow. Downloads and base images live in the named volume `emulationstation-cache` (`/cache` in the container); only `out/` and `secrets/` are bind mounts.
2. **VM image with a reduced emulator list** (EmulationStation, theme, lr-fceumm, lr-snes9x, lr-gambatte): The VM image is for testing only; compiling all emulators for x86_64/arm64 would cost hours without benefit.
3. **Checksum format** `sha256:`/`sha512:` instead of `*_SHA256` (see above).
4. **sudo:** `emulationstation` needs a password for `sudo` in general; without a password only `/usr/sbin/reboot`, `/usr/sbin/poweroff` (ES menu) and the helper `/usr/local/lib/emulationstation/priv` are allowed.

## Review Focus

1. **Abort in the middle of a build** (Ctrl+C, full disk, network error): no dangling loop devices/mounts in the container VM, no half-finished base image in the cache that is later treated as valid. → Test in Task 2.
2. **`emulationstation.txt` with special characters/malicious code** (`WIFI_PASSWORD=a"b$(reboot)`, CRLF line endings from Windows, BOM): values are taken literally, never executed. → Tests in Task 9.
3. **fail2ban rules do not survive an `nftables` restart** if `nftables.conf` is reloaded with `flush ruleset` after fail2ban is running. → Test in Task 6.
4. **ES menu "Restart/Shutdown"** must work despite the own wrapper (`/tmp/es-shutdown`, `/tmp/es-sysrestart`, `/tmp/es-restart`). → Test in Task 7.
5. **Not enough disk space in the Docker VM** (macOS: the default is often 64 GB): the build aborts up front with a clear message instead of after hours. → Test in Task 1.

---

### Task 1: Host entry point, configuration, builder container, test run

**Files:**
- Create: `build.sh`, `build.conf.example`, `builder/host/detect.sh`, `builder/Dockerfile`, `builder/run.sh`, `builder/lib/config.sh`, `builder/lib/log.sh`
- Modify: `.gitignore` (`build.conf`, `secrets/`, `out/`)
- Test: `test/bats/config.bats`, `test/bats/detect.bats`, `test/fixtures/build.conf.*`

**Interfaces:**
- Produces:
  - `build.sh <rpi4|vm-amd64|vm-arm64|all|cert|test> [--rebuild-base] [--base-only] [--config FILE]` (default config `./build.conf`; `--base-only` builds only the base layer into the cache – for development and tests).
  - `builder/host/detect.sh`: functions `host_os` (→ `linux|macos`), `host_arch` (→ `amd64|arm64`), `require_docker`, `ensure_binfmt <arch>` (Linux: `docker run --privileged --rm tonistiigi/binfmt:qemu-v8.1.5 --install <arch>`, only if `/proc/sys/fs/binfmt_misc/qemu-<aarch64|x86_64>` is missing; macOS: Docker Desktop/OrbStack/Colima bring binfmt with them → only check with `docker run --rm --platform linux/<arch> debian:trixie true`), `check_docker_space <GB>` (through `docker run --rm -v emulationstation-cache:/cache debian:trixie df -BG /cache`).
  - Container call: `docker run --rm --privileged -v emulationstation-cache:/cache -v "$PWD:/work" -w /work emulationstation-builder builder/run.sh <target> [opts]`.
  - `builder/lib/config.sh`: `config_load <file>` reads lines `KEY=VALUE` **without `source`** (keys from spec 4.1 + `RPI_IMAGE_CHECKSUM`, `VM_IMAGE_CHECKSUM_AMD64`, `VM_IMAGE_CHECKSUM_ARM64`, `XPADNEO_REF`) into variables `CFG_<KEY>`; `config_require <KEY>...` aborts with a message.
  - Dockerfile: pins `GO_VERSION=1.25.x` (latest patch, enter the SHA256 from go.dev/dl), `TAILWIND_VERSION=v4.1.11` (enter the SHA256 from the release page, binary per `TARGETARCH`), packages from the tech stack + `bats`, `shellcheck`, `git`, `ca-certificates`, `curl`, `fdisk`, `parted`, `rsync`.
  - `build.sh test`: in the container `shellcheck` over all `*.sh` + `build.sh`, `bats test/bats`, `cd webui && go vet ./... && go test -race ./...`.

- [ ] **Step 1: Failing tests** – `config.bats`: reads simple values; `WIFI_PASSWORD=a"b$(touch /tmp/pwned)` → value literal, `/tmp/pwned` does not exist; comments/empty lines ignored; CRLF is removed; missing `USER_PASSWORD` → `config_require` exit ≠ 0 with "USER_PASSWORD is missing". `detect.bats`: `uname` stubs (`Darwin`/`arm64` → `macos`/`arm64`; `Linux`/`x86_64` → `linux`/`amd64`; `aarch64` → `arm64`); `check_docker_space 40` with a `docker` stub that reports 20G → exit ≠ 0, the message names 40 GB and where to increase the Docker storage.
- [ ] **Step 2:** `bats test/bats` (locally or in the container) → FAIL.
- [ ] **Step 3:** Implement. Required space: `rpi4` 40 GB, `vm-*` 20 GB, `all` 80 GB.
- [ ] **Step 4:** `./build.sh test` → shellcheck clean, all bats and Go tests PASS.
- [ ] **Step 5: Checkpoint.**

### Task 2: Image library (fetch, loop, chroot, cleanup, cache)

**Files:**
- Create: `builder/lib/image.sh`, `builder/lib/chroot.sh`, `builder/lib/cache.sh`, `builder/steps/10-fetch.sh`, `builder/steps/20-prepare-image.sh`
- Test: `test/bats/image.bats`, `test/bats/cache.bats`, `test/fixtures/sfdisk-rpi.json`, `test/fixtures/sfdisk-vm.json`

**Interfaces:**
- Produces (all in the container):
  ```
  fetch_verified <url> <checksum> <dest>      # download to <dest>.part, check, then mv; .xz is unpacked
  partitions_json <img>                        # sfdisk -J
  part_offset <json> <n> / part_size <json> <n># in bytes (sector size from the JSON)
  loop_attach <img> <offset> <size>            # losetup -f --show -o … --sizelimit …; registers cleanup
  image_grow <img> <GiB>                       # truncate, extend the last partition with sfdisk, e2fsck + resize2fs
  mount_target <img> <target: rpi4|vm>         # root to /mnt/target, boot to /mnt/target/boot/firmware (rpi4) or /boot/efi (vm)
  chroot_prepare / chroot_run <cmd...> / chroot_teardown
      # bind /dev /dev/pts /proc /sys /run; temporary resolv.conf; usr/sbin/policy-rc.d → exit 101
  cleanup_push <cmd> / cleanup_run             # LIFO; trap 'cleanup_run' EXIT INT TERM
  cache_key <target>                           # sha256 over: provision/common/base-*.sh, provision/<target>/base-*.sh,
                                               # builder/steps/30-provision-base.sh, image checksum, RETROPIE_SETUP_REF, XPADNEO_REF
  cache_base_path <target>                     # /cache/base-<target>-<key>.img
  ```
  The base image is built as `…img.tmp` and becomes `…img` through `mv` only after success (Review Focus 1).

- [ ] **Step 1: Failing tests** – `part_offset`/`part_size` against fixtures (rpi: partition 2 start 1056768 × 512); `cleanup_run` runs registered commands in reverse order, also on `exit 1` in the script; `fetch_verified` with a local file (`file://`) and a wrong checksum → exit ≠ 0, no `<dest>` present; `cache_key` changes when a base file is changed, and does not change when an `app-*` file is changed; an aborted base build leaves no `…img` (only `.tmp`, which is deleted on the next run).
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS; in addition manually in the container: `20-prepare-image.sh` with the real Pi image, afterwards `losetup -a` is empty.
- [ ] **Step 5: Checkpoint.**

### Task 3: Base layer Raspberry Pi 4

**Files:**
- Create: `builder/steps/30-provision-base.sh`, `provision/common/base-10-packages.sh`, `provision/common/base-20-user.sh`, `provision/common/base-30-retropie.sh`, `provision/common/base-40-xpadneo.sh`, `provision/rpi4/base-10-system.sh`, `provision/rpi4/modules.list`
- Modify: `build.conf.example` (pins)

**Interfaces:**
- Consumes: Task 2 (`chroot_run`, `cache_*`, `image_grow`, growth `rpi4` = 8 GiB).
- Produces: `/cache/base-rpi4-<key>.img` with:
  - Packages: `git dialog unzip xmlstarlet ca-certificates curl openssh-server nftables fail2ban python3-systemd bluez zram-tools dkms network-manager rfkill` (+ kernel headers `linux-headers-<release>` for **every** installed kernel, rpi-v8 and rpi-2712; if the archive no longer has them, the image and headers meta packages of that flavour together).
  - User `emulationstation` (groups per Global Constraints), `userconfig.service` masked, default users `pi`/`rpi-first-boot-wizard` removed if present. Password (`chpasswd`) and `authorized_keys` are **not** set in the base layer: they are user values outside the cache key, so `provision/common/app-05-identity.sh` sets them in the app layer (ruling R16).
  - `config.txt`: `dtoverlay=vc4-kms-v3d`, `disable_overscan=1`, no `disable-bt`.
  - `/etc/default/zramswap`: `ALGO=zstd`, `PERCENT=50`.
  - RetroPie-Setup in `/home/emulationstation/RetroPie-Setup` at `RETROPIE_SETUP_REF`; each module from `modules.list` through `chroot_run env __platform=rpi4 SUDO_USER=emulationstation bash retropie_packages.sh <module>` (core modules first: `retroarch emulationstation runcommand`; then `skyscraper`; then all emulators from spec 5.2). Failed modules → line in `/cache/report-rpi4-<key>.txt`, the build continues; **abort only** if a core module fails or RetroPie-Setup rejects the OS (then the message: "RetroPie rejects trixie – please ask the user, spec section 9").
  - xpadneo (`XPADNEO_REF`) through DKMS for **every** version in `/lib/modules/*` (`dkms install -k <ver>`), not for the host's `uname -r`; failure → report, no abort.
- `modules.list` content (one line per module): `lr-fceumm lr-snes9x lr-gambatte lr-mgba lr-beetle-vb lr-gw lr-genesis-plus-gx lr-picodrive lr-beetle-pce-fast lr-beetle-supergrafx lr-fbneo lr-mame2003-plus lr-beetle-ngp lr-beetle-wswan lr-stella2014 lr-atari800 lr-prosystem lr-handy hatari vice amiberry lr-caprice32 lr-fuse lr-bluemsx linapple lr-px68k jzintv lr-vecx dosbox scummvm lr-pcsx-rearmed lr-flycast ppsspp mupen64plus lr-mupen64plus-next`.

- [ ] **Step 1: Failing test** – `test/rpi4-static.sh --stage base <img>` (built completely in Task 12; create it here first with the base checks): user + groups, `userconfig.service` masked, `vc4-kms-v3d` in `config.txt`, `/opt/retropie/supplementary/emulationstation/emulationstation` exists, for each module from `modules.list` a directory under `/opt/retropie/{libretrocores,emulators}/` **or** an entry in the report. Run against the unchanged Pi OS image → FAIL.
- [ ] **Step 2:** Implement.
- [ ] **Step 3:** `./build.sh rpi4 --base-only` → `test/rpi4-static.sh --stage base` → PASS; look at the report and tell the user about failed modules.
- [ ] **Step 4:** A second call of `./build.sh rpi4 --base-only` skips the base layer (log: "Base layer from cache") in < 1 min.
- [ ] **Step 5: Checkpoint.**

### Task 4: Base layer VM (amd64/arm64)

**Files:**
- Create: `provision/vm/base-10-system.sh`, `provision/vm/modules.list` (`lr-fceumm lr-snes9x lr-gambatte`)
- Modify: `builder/steps/30-provision-base.sh` (target `vm`)

**Interfaces:**
- Produces: `/cache/base-vm-<arch>-<key>.img`: shared base scripts from Task 3 (without the kernel headers package for the Pi; `linux-headers-<arch>` instead), plus `cloud-guest-utils`, `mesa-utils`, `libgl1-mesa-dri`; GRUB kernel parameters `console=tty0 console=ttyS0,115200` (amd64) or `console=ttyAMA0` (arm64); RetroPie with `__platform` = `x86` (amd64) or the generic armv8 entry from `scriptmodules/system/platforms.sh` of the pinned version (look it up while implementing, comment it in the code). Growth 4 GiB.

- [ ] **Step 1:** `test/vm-static.sh --stage base <img>` (user, ES binary, kernel parameters) against the unchanged Debian image → FAIL.
- [ ] **Step 2:** Implement. **Step 3:** `./build.sh vm-amd64 --base-only` and `./build.sh vm-arm64 --base-only` → static check PASS.
- [ ] **Step 4: Checkpoint.**

### Task 5: TLS (CA + server certificate)

**Files:**
- Create: `builder/lib/tls.sh`
- Test: `test/bats/tls.bats`

**Interfaces:**
- Produces:
  ```
  tls_ensure_ca <dir>                     # creates <dir>/ca.key (0600) + ca.crt only if missing; ECDSA P-256, 3650 days,
                                          # CN "EmuStation Builder Local CA", basicConstraints=critical,CA:TRUE, keyUsage=keyCertSign,cRLSign
  tls_issue_server <cadir> <outdir> <hostname> <extra-sans>
                                          # server.key (0600) + server.crt (leaf + CA chain), ECDSA P-256, 825 days,
                                          # SAN: DNS:<host>, DNS:<host>.local, + extra (IP addresses → IP:, the rest → DNS:),
                                          # extendedKeyUsage=serverAuth
  ```
  `./build.sh cert`: `tls_ensure_ca secrets/` + `tls_issue_server secrets/ out/cert/ …` and a hint about `scp` + `sudo systemctl restart emulationstation-web`.

- [ ] **Step 1: Failing tests** – a second `tls_ensure_ca` call does not change `ca.crt` (same hash); the SAN list for `emulationstation` + `"box.fritz.box,192.168.178.50"` contains exactly `DNS:emulationstation, DNS:emulationstation.local, DNS:box.fritz.box, IP Address:192.168.178.50`; `openssl verify -CAfile ca.crt server.crt` → OK; validity ≤ 825 days; `server.key` mode 600.
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS. **Step 5: Checkpoint.**

### Task 6: App layer – SSH, firewall, fail2ban, sudo helper

**Files:**
- Create: `builder/steps/40-provision-app.sh`, `provision/common/app-10-security.sh`
- Create: `overlay/etc/ssh/sshd_config.d/10-emulationstation.conf`, `overlay/etc/nftables.conf`, `overlay/etc/fail2ban/jail.d/emulationstation.conf`, `overlay/etc/fail2ban/filter.d/emulationstation-web.conf`, `overlay/etc/sudoers.d/emulationstation`, `overlay/usr/local/lib/emulationstation/priv`, `overlay/etc/systemd/system/fail2ban.service.d/after-nftables.conf`
- Test: `test/bats/priv.bats`, `test/bats/fail2ban-filter.bats`

**Interfaces:**
- Values:
  - sshd: `Port 11911`, `ListenAddress 0.0.0.0`, `PermitRootLogin no`, `PasswordAuthentication yes`, `PubkeyAuthentication yes`, `MaxAuthTries 5`.
  - `nftables.conf`: **no** `flush ruleset`; instead `table inet filter` through `destroy table inet filter` (nft ≥ 1.0.6) + redefinition, so `inet f2b-table` from fail2ban is kept (Review Focus 3). Input chain policy `drop`: `iif lo accept`, `ct state established,related accept`, `ct state invalid drop`, `meta l4proto icmp accept`, `meta l4proto ipv6-icmp accept`, `tcp dport { 11911, 11944 } accept`. Forward `drop`, output `accept`.
  - Jails per Global Constraints; `sshd` with `port = 11911`; `emulationstation-web` with `port = 11944`, `journalmatch = _SYSTEMD_UNIT=emulationstation-web.service`, filter `failregex = ^.*auth failure from <HOST>$`.
  - sudoers (mode 0440, checked with `visudo -cf`): `emulationstation ALL=(root) NOPASSWD: /usr/sbin/reboot, /usr/sbin/poweroff, /usr/local/lib/emulationstation/priv`.
  - Helper `priv`: subcommands exactly as in Plan 1; `f2b-list` prints `<jail> <ip>` for each banned IP (from `fail2ban-client status <jail>`); `f2b-unban` checks IPv4/IPv6 with a regex, otherwise exit 2; `journal` only `emulationstation-web|ssh|fail2ban` → `journalctl -u <unit> -n 500 --no-pager`; `apt-update` = `apt-get update`; `apt-upgrade` = `apt-get -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold upgrade` with `DEBIAN_FRONTEND=noninteractive`; `apt-update`/`apt-upgrade` run through `systemd-run --wait --pipe --collect` outside the web service's sandbox (ruling R7); unknown → exit 2.
  - Uninstall/mask: `avahi-daemon`, `cups`, `rpcbind`, `triggerhappy` (if present).

- [ ] **Step 1: Failing tests** – `priv.bats` with stubs for `fail2ban-client`/`journalctl`/`apt-get` in `PATH`: `f2b-unban "1.2.3.4;id"` → exit 2, stub not called; `f2b-unban 2001:db8::1` → `fail2ban-client unban 2001:db8::1`; `journal nginx` → exit 2; `f2b-list` formats the stub output correctly. `fail2ban-filter.bats`: `fail2ban-regex "Oct 27 12:00:00 host emulationstation-web[1]: auth failure from 10.0.0.9" overlay/etc/fail2ban/filter.d/emulationstation-web.conf` → 1 match, IP `10.0.0.9`; an IPv6 line likewise.
- [ ] **Step 2:** FAIL. **Step 3:** Implement; `nft -c -f overlay/etc/nftables.conf` in the container → OK. **Step 4:** PASS. **Step 5: Checkpoint.**

### Task 7: App layer – ES session wrapper, autostart, theme

**Files:**
- Create: `provision/common/app-20-es.sh`, `overlay/usr/local/lib/emulationstation/es-session`, `overlay/etc/tmpfiles.d/emulationstation.conf`, `overlay/etc/systemd/system/getty@tty1.service.d/autologin.conf`
- Test: `test/bats/es-session.bats`

**Interfaces:**
- `tmpfiles.d`: `d /run/emulationstation 0755 emulationstation emulationstation -`.
- Autologin: `ExecStart=-/sbin/agetty --autologin emulationstation --noclear %I $TERM`.
- `/opt/retropie/configs/all/autostart.sh` → content `exec /usr/local/lib/emulationstation/es-session` (enable the RetroPie module `autostart` first, so `.bash_profile` has the hook).
- Theme: `git clone https://github.com/c64-dev/es-theme-epicnoir.git` at `EPICNOIR_REF` to `/home/emulationstation/.emulationstation/themes/es-epicnoir`; set `<string name="ThemeSet" value="es-epicnoir" />` in `es_settings.cfg` (create the file if missing; keep existing entries).
- `es-session` (overridable by environment variables for tests: `ES_BIN`, `RUNCOMMAND`, `RUN_DIR`, `TMP_DIR`, `SUDO`), endless loop:
  1. `hold` exists → `state`=`stopped`, wait 1 s, continue.
  2. `launch` exists → read it, delete it, `state`=`game\t<sys>\t<rom>`, start `"$RUNCOMMAND" 0 _SYS_ <sys> <rom>` as a job of its own process group (job control, `set -m`, not `setsid`: `setsid` would take the game away from the tty), PGID to `game.pid`, wait, delete `game.pid`.
  3. Otherwise `state`=`es`, start `$ES_BIN` in the background, PID to `es.pid`, wait, delete `es.pid`; afterwards `$TMP_DIR/es-shutdown` → `$SUDO poweroff`; `$TMP_DIR/es-sysrestart` → `$SUDO reboot`; `$TMP_DIR/es-restart` → only delete the file (the loop restarts ES).
  - `state` is always written atomically (tmp + `mv`).

- [ ] **Step 1: Failing tests** (stubs: ES = script that runs `$ES_EXIT_ACTION` and ends; RUNCOMMAND writes its arguments to a file; SUDO = stub that logs its arguments; loop limited by the variable `MAX_ITER`): ES ends without flags → ES is started again; `launch` with `snes\t/r/Super Mario.sfc` → RUNCOMMAND arguments exactly `0 _SYS_ snes /r/Super Mario.sfc`, `launch` deleted, `state` during the game `game\tsnes\t/r/Super Mario.sfc`; `hold` → `state` = `stopped`, ES is not started; `es-shutdown` → the SUDO stub receives `poweroff`; `es-sysrestart` → `reboot`.
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS. **Step 5: Checkpoint.**

### Task 8: App layer – install the web service

**Files:**
- Create: `provision/common/app-30-webui.sh`, `overlay/etc/systemd/system/emulationstation-web.service`
- Modify: `builder/steps/40-provision-app.sh` (build the web binary + TLS)

**Interfaces:**
- Consumes: Plan 1 `webui/scripts/build.sh <GOARCH> <out>`, CLI `credentials`; Task 5.
- Flow in `40-provision-app.sh` (in the container, not in the chroot): `webui/scripts/build.sh <arm64|amd64> /work/out/tmp/emulationstation-web` → to `/usr/local/bin/` in the target (0755); `tls_ensure_ca /work/secrets`, `tls_issue_server … "$CFG_HOSTNAME" "$CFG_TLS_EXTRA_SANS"` → `/etc/emulationstation-web/tls/` (owner `emulationstation`); `secrets/ca.crt` → `out/emulationstation-ca.crt`.
- `web.conf` additionally contains `SOURCE_URL=$CFG_SOURCE_URL` (source code link in the footer, AGPL §13; empty = no link).
- In the chroot: `printf '%s\n' "$CFG_WEB_PASSWORD" | emulationstation-web credentials --config /etc/emulationstation-web/web.conf --user "$CFG_WEB_USER" --password-stdin` (runs through binfmt), add the ScreenScraper values, `chown emulationstation:emulationstation`, `chmod 600`.
- Unit: `User=emulationstation`, `ExecStart=/usr/local/bin/emulationstation-web serve --config /etc/emulationstation-web/web.conf`, `StateDirectory=emulationstation-web`, `Restart=on-failure`, `After=network-online.target emulationstation-firstboot.service`, `AmbientCapabilities=` empty, `NoNewPrivileges=no` (the sudo helper needs it), `ProtectSystem=full`, `ReadWritePaths=/etc/emulationstation-web /home/emulationstation /run/emulationstation`.

- [ ] **Step 1:** Extend `test/rpi4-static.sh --stage app` with checks: the binary is `ARM aarch64` (through `file`), unit enabled, `web.conf` mode 600 and without a plain-text password, `openssl x509 -in server.crt -noout -ext subjectAltName` contains the host name → FAIL against the base image.
- [ ] **Step 2:** Implement. **Step 3:** `./build.sh rpi4` (base from cache) → static check PASS. **Step 4: Checkpoint.**

### Task 9: First boot

**Files:**
- Create: `overlay/usr/local/lib/emulationstation/firstboot`, `overlay/etc/systemd/system/emulationstation-firstboot.service`, `overlay/boot-template/emulationstation.txt.example`, `provision/common/app-40-firstboot.sh`
- Test: `test/bats/firstboot.bats`

**Interfaces:**
- Unit: `Type=oneshot`, `Before=network-pre.target emulationstation-web.service ssh.service`, `Wants=network-pre.target`, `WantedBy=multi-user.target`, runs at **every** boot.
- Script variables (overridable for tests): `ROOT` (default `/`), `BOOT_DIR` (`/boot/firmware` or `/boot/efi`, fixed in the image), commands `CHPASSWD`, `EW_BIN`, `WIFI_COUNTRY_CMD`.
- Flow: (1) once: `ssh-keygen -A` if there are no host keys; VM: `growpart` + `resize2fs` if the marker `/var/lib/emulationstation/grown` is missing. (2) If `emulationstation.txt` exists: remove BOM and `\r`, parse lines `KEY=VALUE` (**never `source`**), only allowed keys (spec 5.4); unknown → log "unknown key <KEY> ignored". (3) Apply: `HOSTNAME` (regex `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`, otherwise ignored + log) → `/etc/hostname`, `/etc/hosts` line `127.0.1.1`; Wi-Fi → `/etc/NetworkManager/system-connections/emulationstation-wifi.nmconnection` (0600, `[wifi] ssid=…`, `[wifi-security] key-mgmt=wpa-psk psk=…`, values literal); `WIFI_COUNTRY` → `raspi-config nonint do_wifi_country <CC>` (rpi4) / ignored (vm); `TIMEZONE` only if `/usr/share/zoneinfo/<TZ>` exists; `KEYMAP` → `/etc/default/keyboard` `XKBLAYOUT`; `USER_PASSWORD` → `chpasswd`; `WEB_USER`/`WEB_PASSWORD` → `emulationstation-web credentials`. (4) File → `emulationstation.txt.applied`, with the values of all keys with `PASSWORD` in the name replaced by `***`.

- [ ] **Step 1: Failing tests** (fake root in `$BATS_TEST_TMPDIR`, stubs for commands): host name set; invalid host name `../x` ignored; Wi-Fi file with `psk=a"b$(reboot)` literal, mode 600, no stub call of `reboot`; CRLF + BOM handled correctly; `.applied` contains `WIFI_PASSWORD=***` and `USER_PASSWORD=***`, original file gone; without a file → only step 1, exit 0; unknown key → log line, the rest is applied.
- [ ] **Step 2:** FAIL. **Step 3:** Implement; `emulationstation.txt.example` (all keys commented out, explained in simple English) is copied to `BOOT_DIR`. **Step 4:** PASS. **Step 5: Checkpoint.**

### Task 10: Controller templates & Bluetooth

**Files:**
- Create: `controllers/templates/README.md`, `controllers/templates/*.xml` (per device/bus), `controllers/custom/.gitkeep`, `provision/common/app-50-controllers.sh`

**Interfaces:**
- Consumes: Plan 1 CLI `guid`; RetroPie hook `inputconfiguration.sh`.
- Template format: a complete `<inputConfig type="joystick" deviceName="…" deviceGUID="…">` node. Positional SNES mapping (RetroPie convention): ES `a` = right button (east), `b` = bottom (south), `x` = top (north), `y` = left (west); `hotkeyenable` = Select/Back/Share; D-pad through `hat 0` (values 1/4/8/2 for up/down/left/right); analog left axes 0/1, right 3/4.
- Devices (vendor:product, verify the kernel name on hardware or in the driver sources while implementing; GUIDs with `emulationstation-web guid`, per template **with and without** CRC as two nodes):

  | File | Bus | IDs | Button codes south/east/west/north, L/R, Select/Start | Triggers |
  |---|---|---|---|---|
  | `xbox360-usb.xml` (covers 8BitDo X-Input) | USB | 045e:028e | 0/1/2/3, 4/5, 6/7 | axes 2/5 |
  | `xboxone-usb.xml` | USB | 045e:02ea | like 360 | axes 2/5 |
  | `xboxseries-usb.xml` | USB | 045e:0b12 | like 360 | axes 2/5 |
  | `xboxone-bt.xml`, `xboxseries-bt.xml` | BT | 045e:02fd, 045e:0b13 | per the xpadneo docs of the pinned version | per xpadneo |
  | `ds4-usb.xml`, `ds4-bt.xml` | USB/BT | 054c:09cc (+ 054c:05c4) | 0/1/3/2, 4/5, 8/9 | axes 2/5 |
  | `dualsense-usb.xml`, `dualsense-bt.xml` | USB/BT | 054c:0ce6 | 0/1/3/2, 4/5, 8/9 | axes 2/5 |

- `app-50-controllers.sh`: for each file from `controllers/templates/` and then `controllers/custom/` (custom wins for the same GUID): write the node as `es_temporaryinput.cfg`, run `inputconfiguration.sh` in the chroot as `emulationstation`, take the node over into `es_input.cfg`. No keyboard node. `systemctl enable bluetooth`.

- [ ] **Step 1:** Extend `test/rpi4-static.sh --stage app`: `es_input.cfg` contains one node per template (GUID list), `/opt/retropie/configs/all/retroarch-joypads/` contains one file per device name, no `type="keyboard"`, `bluetooth.service` enabled → FAIL.
- [ ] **Step 2:** Implement; `xmllint --noout controllers/templates/*.xml` in the container → OK.
- [ ] **Step 3:** `./build.sh rpi4` → static check PASS. **Step 4: Checkpoint.**

### Task 11: Finalize & outputs

**Files:**
- Create: `builder/steps/50-finalize.sh`
- Modify: `builder/run.sh` (target flow, `all`)

**Interfaces:**
- Flow: remove apt caches, the contents of `/var/log/*`, `/etc/machine-id` (write `uninitialized`, do not delete; ruling R19), SSH host keys, Bash history, `policy-rc.d`, the temporary `resolv.conf`; tear down the chroot; `e2fsck -fy`, `resize2fs -M`, set the partition to the new size + 16 MiB buffer with `sfdisk`, shorten the image with `truncate`.
  - rpi4: `xz -T0 -6` → `.img.xz`, `sha256sum` → `.sha256`.
  - vm: `.raw` + `qemu-img convert -O qcow2`.
  - `out/build-report.txt`: target, date, upstream pins, cache key, failed modules (from the report of the base layer), hint about `emulationstation-ca.crt`.
- `all` = `rpi4`, `vm-amd64`, `vm-arm64` one after the other; a failure aborts.

- [ ] **Step 1:** `test/bats/finalize.bats` with a small test image (100 MiB, ext4 with `mkfs.ext4` in a file, in the container): after finalize it is smaller than before, `e2fsck -fn` is clean, the partition table matches the file size → FAIL.
- [ ] **Step 2:** Implement. **Step 3:** PASS; `./build.sh all` creates all outputs from Global Constraints. **Step 4: Checkpoint.**

### Task 12: Image tests & hardware checklist

**Files:**
- Create/Modify: `test/rpi4-static.sh` (all stages), `test/vm-static.sh`, `test/vm-smoke.sh`, `test/fixtures/emulationstation.txt`, `docs/hardware-checklist.md`, `README.md` (building, flashing, importing the CA, `emulationstation.txt`, ports, changing the default credentials)

**Interfaces:**
- `test/vm-smoke.sh <qcow2> [--arch amd64|arm64]`: copy of the image, `emulationstation.txt` from the fixture into the EFI partition without `guestfish` (loop mount in the builder container); QEMU (`-accel kvm|hvf|tcg`, `-netdev user,hostfwd=tcp::21911-:11911,hostfwd=tcp::21944-:11944`, UEFI firmware `OVMF`/`AAVMF`), wait until port 21944 answers (timeout 10 min). Checks:
  1. `curl --cacert out/emulationstation-ca.crt --resolve <host>:21944:127.0.0.1 -u zocken:zocken https://<host>:21944/` → 200.
  2. Wrong credentials → 401; 5 failed attempts → the 6th connection fails or times out (ban). Because the QEMU user network shows all requests from `10.0.2.2`, afterwards use SSH to run `sudo /usr/local/lib/emulationstation/priv f2b-unban 10.0.2.2`.
  3. `ssh -p 21911 emulationstation@127.0.0.1 true` (key from the test config) → OK; `ssh root@…` → rejected.
  4. In the guest: `sudo nft list ruleset` contains only accept rules for 11911/11944; `ss -tlnp` shows no other ports listening on `0.0.0.0`/`::` except 11911/11944 (a port scan from outside is not meaningful with the user network).
  5. `pgrep -f '^/opt/retropie/supplementary/emulationstation/emulationstation'` → running (not `pgrep -x emulationstatio`: the comm of `emulationstation-web` is also cut to `emulationstatio` and would match by mistake).
  6. Host name from the fixture is set, `emulationstation.txt.applied` exists with `***`.
  7. `sudo systemctl restart nftables && sudo nft list table inet f2b-table` → still exists (Review Focus 3).
- `docs/hardware-checklist.md`: spec section 8 "Hardware" as a checklist, plus: the ES menu entries "Restart System" and "Shutdown System" work, temperature under load (`vcgencmd measure_temp`) after 15 min of N64 < 80 °C, upload of a 700 MB PSX `.chd` over Wi-Fi.

- [ ] **Step 1:** `test/vm-smoke.sh` against an image **without** the app layer (`--base-only` result) → FAIL (port 21944 not reachable).
- [ ] **Step 2:** Write the scripts and docs.
- [ ] **Step 3:** `./build.sh vm-amd64 && test/vm-smoke.sh out/emulationstation-vm-amd64-*.qcow2` → all checks PASS (on arm64 hosts also `vm-arm64`). `./build.sh rpi4 && test/rpi4-static.sh --stage app out/…img.xz` → PASS.
- [ ] **Step 4:** `./build.sh test` → all green.
- [ ] **Step 5: Checkpoint** – in addition, report to the user: report content (failed modules), build time per target, open points of the hardware checklist.
