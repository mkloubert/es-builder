# EmuStation Builder – Image for Raspberry Pi 4 (2 GB) – Design

License: AGPL-3.0-or-later, see LICENSE

As of: 2026-10-27 · Status: draft for approval

## 1. Goal and success criteria

Bash scripts that build a finished, bootable image on **Linux and macOS** (each on **amd64 and arm64**):

- **`rpi4`** – Raspberry Pi 4 (2 GB RAM), Debian 12 "bookworm" (Raspberry Pi OS Lite arm64, see `2026-10-31-bookworm-design.md`), EmulationStation (RetroPie) with the theme **EpicNoir**, preinstalled emulators and a web UI.
- **`vm-amd64` / `vm-arm64`** – generic UEFI image (Debian 12) with an identical setup **for testing** the web UI, SSH, firewall and so on. EmulationStation runs there with software rendering only; playing games is not a goal.

Success criteria:

1. With `ES_AUTOSTART=yes` the Pi boots straight into EmulationStation with EpicNoir, without any manual step (default `no`, see `2026-10-31-es-autostart-design.md`).
2. The web UI is reachable at `https://<device>:11944` (Basic Auth `zocken`/`zocken`, changeable).
3. SSH on port `11911`; from outside **only** 11911 and 11944 are open.
4. 5 failed logins (SSH **or** web) → IP banned for 1 hour.
5. ROMs uploaded through the web UI (also ZIP) show up after an ES restart with a clean display name.

Note: The output format is a disk image (`.img`), not an ISO – Pi and VM boot from disk images.

## 2. Decisions

| Topic | Decision | Reason |
|---|---|---|
| ES variant | RetroPie EmulationStation via RetroPie-Setup | EpicNoir is a legacy ES theme; ES-DE ≥ 2.2 does not load legacy themes |
| Build approach | Customise official images (loop mount + chroot) in Docker | proven bootloader/firmware/kernel, little own code |
| Pi base | Raspberry Pi OS Lite bookworm arm64 (version pinned) | official Pi support, RetroPie binaries (`2026-10-31-bookworm-design.md`) |
| VM base | Debian 12 cloud image `nocloud`, raw, UEFI (amd64/arm64) | official, slim UEFI image |
| Build speed-up | two stages: cached base layer (RetroPie + emulators) + fast app layer | compiling RetroPie under QEMU takes hours |
| Display names | automatic cleanup + manually editable + optional scraping (Skyscraper/ScreenScraper) | |
| Network config | `build.conf` at build time + `emulationstation.txt` on the boot partition at first boot | one image for different networks |
| Bans | fail2ban for SSH **and** web (one shared ban list) | the ban works in the firewall and can be shown/lifted in the UI |
| Web transport | HTTPS only, certificate created at build time, own reusable CA | |
| Frontend | `html/template` + htmx + Alpine.js + Tailwind (standalone CLI), all `embed`ded | no Node.js, one binary |
| mDNS | off (port 5353 closed) | requirement "only 2 ports open" |
| Controllers | templates at build time only for controllers with stable IDs (Xbox, PlayStation, 8BitDo X-Input) + own exports from `controllers/custom/`; unknown ones → ES wizard at start; web UI: overview, reset, export/import, Bluetooth pairing | cheap SNES clones share IDs with different mappings; mapping needs a button press on the device anyway |

## 3. Repo structure

```
build.sh                  # entry point
build.conf.example        # configuration template
builder/
  Dockerfile              # debian:trixie + qemu-user-static, parted, e2fsprogs, dosfstools, xz-utils,
                          # qemu-utils, openssl, Go (pinned), Tailwind standalone CLI (pinned)
  steps/
    10-fetch.sh           # download upstream image + check the checksum (sha256/sha512) → cache/
    20-prepare-image.sh   # grow image, mount partitions/loop
    30-provision-base.sh  # base layer (cached)
    40-provision-app.sh   # app layer
    50-finalize.sh        # clean up, shrink, compress/convert
  lib/                    # shared Bash functions (log, mount/umount, trap cleanup, chroot run)
provision/                # runs in the chroot of the target system
  common/
    base-*.sh             # packages, user, RetroPie-Setup + emulators
    app-*.sh              # SSH, nftables, fail2ban, theme, ES wrapper, web service, first boot, TLS
  rpi4/                   # config.txt (vc4-kms-v3d), zram, Autologin tty1
  vm/                     # GRUB, serial console, virtio-gpu/Mesa
overlay/                  # files copied 1:1 into the image (systemd units, configs, helper scripts)
controllers/
  templates/              # bundled templates (ES <inputConfig> XML), per controller and bus (USB/BT)
  custom/                 # own exports from the web UI, also included in the image
webui/                    # Go module (section 6)
test/
  vm-smoke.sh             # automatic smoke test of the VM image
  rpi4-static.sh          # static check of the Pi image
docs/
  hardware-checklist.md   # manual test on a real Pi
cache/  out/  secrets/    # gitignored
```

## 4. Build flow

Usage:

```
./build.sh rpi4|vm-amd64|vm-arm64|all [--rebuild-base] [--config build.conf]
./build.sh cert           # only issue a new server certificate from the existing CA
```

1. **Host check** (`build.sh`, runs on the host): detects OS (Linux/macOS) and arch (amd64/arm64), checks Docker (Docker Desktop/OrbStack/Colima on macOS), registers binfmt/QEMU for a foreign arch (`tonistiigi/binfmt`), builds the builder image. After that, everything else runs in the **privileged builder container** (for loop devices).
2. **Fetch:** download the upstream image, check the checksum (`sha256:`/`sha512:`) against the pin in `build.conf`, store it in `cache/`.
3. **Base layer:** grow the image (buffer for RetroPie), chroot provisioning `provision/common/base-*` + target-specific scripts. Result `cache/base-<target>-<hash>.img`. `<hash>` = hash over the base scripts, version pins and upstream version. If the file exists, the step is skipped (except with `--rebuild-base`).
4. **App layer:** copy of the base layer; the web UI is **cross-compiled** in the container (`GOOS=linux GOARCH=arm64|amd64`, `CGO_ENABLED=0`) after the Tailwind build; create the TLS certificate (section 7); apply the overlay + `provision/common/app-*`.
5. **Finalize:** clear apt cache/logs/machine-id/SSH host keys, shrink the file system (root grows to the medium size at first boot).
   - Pi: `out/emulationstation-rpi4-<date>.img.xz` + `.sha256` (can be flashed with Raspberry Pi Imager or `dd`).
   - VM: `out/emulationstation-vm-<arch>-<date>.qcow2` (QEMU/UTM) + `.raw` (e.g. `VBoxManage convertfromraw`).
   - `out/emulationstation-ca.crt` (for import into browser/OS).

Error handling: all scripts use `set -euo pipefail`; a central `trap` cleanly releases chroot bind mounts, partitions and loop devices on every abort; logs per step in `out/logs/`. Abort with a clear message if Docker is missing, there is not enough disk space (checked up front) or a checksum is wrong.

### 4.1 `build.conf`

```
HOSTNAME=emulationstation
LOCALE=de_DE.UTF-8
TIMEZONE=Europe/Berlin
KEYMAP=de
USER_PASSWORD=            # required, password for user emulationstation
USER_SSH_PUBKEY=          # optional
WEB_USER=zocken
WEB_PASSWORD=zocken
WIFI_SSID=                # optional; empty = LAN/DHCP only
WIFI_PASSWORD=
WIFI_COUNTRY=DE           # Raspberry Pi: needed for Wi-Fi (Pi OS blocks Wi-Fi without a country)
TLS_EXTRA_SANS=           # optional, e.g. "emulationstation.fritz.box,192.168.178.50"
SCREENSCRAPER_USER=       # optional
SCREENSCRAPER_PASSWORD=
RPI_IMAGE_URL= / RPI_IMAGE_CHECKSUM=          # version pins; checksum "sha256:<hex>" or "sha512:<hex>"
VM_IMAGE_URL_AMD64= / VM_IMAGE_CHECKSUM_AMD64=  # (Debian publishes only SHA512SUMS)
VM_IMAGE_URL_ARM64= / VM_IMAGE_CHECKSUM_ARM64=
RETROPIE_SETUP_REF=       # Git commit/tag of RetroPie-Setup
EPICNOIR_REF=             # Git commit of the theme
XPADNEO_REF=              # Git tag/commit of xpadneo (Xbox controllers over Bluetooth)
SOURCE_URL=https://github.com/mkloubert/es-builder   # source code link in the web footer (AGPL §13); http(s) only, empty = no link; ends up as SOURCE_URL in web.conf
```

## 5. Target system

### 5.1 User and boot

- User `emulationstation`, groups `sudo video render input audio bluetooth`. Password from `build.conf`, optional SSH key. Root login locked.
- Autologin on tty1 → RetroPie default `autostart.sh` → **ES wrapper** (`/usr/local/lib/emulationstation/es-session`), a loop that:
  - starts EmulationStation and restarts it after it exits,
  - if `/run/emulationstation/launch` exists (job from the web UI: system + ROM path), starts the game with `/opt/retropie/supplementary/runcommand/runcommand.sh 0 _SYS_ <system> <rom>`, deletes the job file and then returns to ES,
  - writes the state (`es`, `game:<system>:<rom>`, `stopped`) to `/run/emulationstation/state`,
  - does not restart ES while `/run/emulationstation/hold` exists (for maintenance by the web UI).
- Pi: `dtoverlay=vc4-kms-v3d`, zram swap (zram-tools), overscan off. The Raspberry Pi OS setup wizard for the first user (`userconfig`) is disabled, because `emulationstation` is already created at build time.
- VM: virtio-gpu, Mesa (llvmpipe), serial console active, GRUB EFI.

### 5.2 Emulators (RetroPie modules)

| System | Module |
|---|---|
| NES / Famicom | lr-fceumm |
| SNES / Super Famicom | lr-snes9x |
| Game Boy / Color | lr-gambatte |
| Game Boy Advance | lr-mgba |
| Virtual Boy | lr-beetle-vb |
| Game & Watch | lr-gw |
| Master System, Game Gear, Mega Drive/Genesis, Mega-CD, SG-1000 | lr-genesis-plus-gx |
| 32X | lr-picodrive |
| PC Engine / TG-16 / PCE-CD | lr-beetle-pce-fast |
| SuperGrafx | lr-beetle-supergrafx |
| Arcade, Neo Geo | lr-fbneo, lr-mame2003-plus |
| Neo Geo Pocket | lr-beetle-ngp |
| WonderSwan / Color | lr-beetle-wswan |
| Atari 2600 / 5200 / 800 / 7800 / Lynx | lr-stella2014, lr-atari800, lr-prosystem, lr-handy |
| Atari ST | hatari |
| C64 | vice |
| Amiga | amiberry |
| Amstrad CPC | lr-caprice32 |
| ZX Spectrum | lr-fuse |
| MSX / MSX2, ColecoVision | lr-bluemsx |
| Apple II | linapple |
| X68000 | lr-px68k |
| Intellivision | jzintv |
| Vectrex | lr-vecx |
| DOS | dosbox |
| ScummVM | scummvm |
| PlayStation | lr-pcsx-rearmed |
| Dreamcast | lr-flycast |
| PSP | ppsspp |
| N64 (depends on the game) | mupen64plus, lr-mupen64plus-next |

Deliberately **not** installed: Saturn, 3DO, Jaguar, PS2, GameCube, Wii, Wii U, NDS, Steam, Daphne.

In addition: Skyscraper (scraper), EpicNoir in `~/.emulationstation/themes/es-epicnoir` (commit pinned) and set as the active theme in `es_settings.cfg`.

Note: EpicNoir is licensed under CC BY-NC-SA 4.0 – the image must not be distributed commercially with it.

### 5.3 Network and security

- **SSH** (`/etc/ssh/sshd_config.d/10-emulationstation.conf`): `Port 11911`, `ListenAddress 0.0.0.0`, `PermitRootLogin no`, password + key allowed. Host keys are created at first boot.
- **nftables** (`/etc/nftables.conf`): table `inet filter`, `input` policy `drop`; allowed: `lo`, `ct state established,related`, `tcp dport {11911, 11944}`, ICMP and ICMPv6 (for ping, IPv6 neighbor discovery/SLAAC). `forward` drop, `output` accept. Other services with network ports (e.g. avahi, rpcbind, cups) are uninstalled or disabled.
- **fail2ban** (Backend `systemd`, `banaction = nftables-multiport`):
  - Jail `sshd`: `port = 11911`, `maxretry = 5`, `findtime = 1h`, `bantime = 1h`.
  - Jail `emulationstation-web`: reads the journal of `emulationstation-web.service`, filter on the line `auth failure from <HOST>`, `port = 11944`, same values.

### 5.4 First-Boot

systemd oneshot `emulationstation-firstboot.service` (runs before network-online and before the web service):

1. Grow the root partition to the medium size (Pi OS does this itself; for the VM, its own `growpart`/`resize2fs`).
2. Create SSH host keys, create `machine-id`.
3. If present, read `emulationstation.txt` from the boot partition (Pi: `/boot/firmware/`, VM: `/boot/efi/`), format `KEY=VALUE`, allowed keys: `HOSTNAME`, `WIFI_SSID`, `WIFI_PASSWORD`, `WIFI_COUNTRY`, `TIMEZONE`, `KEYMAP`, `USER_PASSWORD`, `WEB_USER`, `WEB_PASSWORD`. Unknown keys are logged and ignored.
4. Apply the values (Wi-Fi through a NetworkManager connection file), then rename the file to `emulationstation.txt.applied` and replace the password values in it with `***`.
5. The file is checked again at every boot (not only the first one), so you can change the Wi-Fi later, for example.

A template `emulationstation.txt.example` is placed on the boot partition.

### 5.5 Controller

**Background:** ES stores mappings in `~/.emulationstation/es_input.cfg`, matched by the SDL GUID (bus, vendor ID, product ID, version) and the device name. From this, RetroPie creates the emulator configs with `/opt/retropie/supplementary/emulationstation/scripts/inputconfiguration.sh` (including the RetroArch autoconfig in `/opt/retropie/configs/all/retroarch-joypads/`). A preset must therefore create **both**.

**At build time (app layer):**

- Bundled templates in `controllers/templates/` for controllers with stable IDs: Xbox 360, Xbox One, Xbox Series (USB and Bluetooth), DualShock 4 and DualSense (USB and Bluetooth), 8BitDo in X-Input mode.
- In addition, all files from `controllers/custom/` (exports from the web UI).
- For each template in the chroot: store the template as `es_temporaryinput.cfg`, run `inputconfiguration.sh`, take the entry over into `es_input.cfg`. For duplicates (same GUID), `custom/` wins over `templates/`.
- A keyboard entry is **not** preset, so ES shows the wizard when no known controller is present.

**At start:** For unknown controllers ES shows the built-in wizard "Configure Input" (unchanged RetroPie default).

**Bluetooth:**

- Pi 4: onboard Bluetooth active through BlueZ (no `disable-bt` overlay). Driver `xpadneo` (DKMS, version pinned) for Xbox controllers over Bluetooth; `hid-playstation` from the kernel for PlayStation controllers.
- Bluetooth uses no IP ports – the firewall rules stay unchanged.
- VM: usually no adapter; the web UI shows this as "no Bluetooth adapter found".

## 6. Web service `emulationstation-web`

### 6.1 Runtime

- One Go binary (`/usr/local/bin/emulationstation-web`), systemd unit, runs as user `emulationstation`, listens on `0.0.0.0:11944` with **TLS only** (TLS 1.2+). Plain HTTP on the port gets 400.
- Configuration `/etc/emulationstation-web/web.conf` (owner `emulationstation`, mode 0600): user name, **bcrypt hash** of the password, ScreenScraper credentials, upload limits.
- Password change through the UI or `emulationstation-web passwd`.
- Privileged actions only through `sudo` with a fixed command list (`/etc/sudoers.d/emulationstation-web`) for a helper script `/usr/local/lib/emulationstation/priv` with fixed subcommands: `reboot`, `poweroff`, `apt-update`, `apt-upgrade`, `f2b-list`, `f2b-unban <ip>`, `journal <unit>`. Arguments are validated in the helper (e.g. IP format).
- Every failed login is written to the journal as `auth failure from <ip>` (for fail2ban); successful logins are not logged.

### 6.2 Code layout

```
webui/
  cmd/emulationstation-web/   # main, CLI (serve, passwd)
  internal/
    config/    # read/write web.conf
    auth/      # Basic Auth middleware, bcrypt, failed-attempt logging
    es/        # ES state, stop/start/restart through wrapper files, start/stop a game
    systems/   # parse es_systems.cfg: systems, ROM paths, extensions, "keep archive" rule
    roms/      # list, upload streaming, ZIP handling, delete
    names/     # name cleanup
    gamelist/  # read/write gamelist.xml (unknown fields are kept)
    bios/      # list of required BIOS files per system incl. MD5, check, upload
    scrape/    # Skyscraper call
    jobs/      # queue for background jobs (one at a time), progress via SSE
    sysinfo/   # temperature, load, RAM, disk, IP, certificate expiry
    controllers/ # connected input devices (/sys/class/input) + es_input.cfg: status, reset, export/import
    bluetooth/ # BlueZ over D-Bus (godbus): scan, pairing with an own agent (NoInputNoOutput), trust, connect, remove
    priv/      # calls of the sudo helper
    web/       # handlers, templates, static assets (embed)
  assets/      # Tailwind input, htmx/Alpine (vendored, versions pinned)
```

### 6.3 User interface

Dark, calm design that fits EpicNoir, responsive for phone and desktop.

- **Dashboard:** ES state / running game, CPU temperature, load, RAM, storage, IP, certificate warning (< 30 days); quick actions: restart/stop ES, stop game.
- **Games:** systems with counts; per system a list with search; actions: rename, delete, start, scrape. Changes are collected and applied with "Apply".
- **Upload:** drag & drop (several files/ZIPs) with progress → review step → apply.
- **BIOS:** list of required files per system with status ✓ / missing / wrong MD5; upload.
- **Controller:**
  - List of connected controllers with name, bus (USB/Bluetooth), GUID and status "configured" / "not configured" (the wizard appears on the TV).
  - Reset: removes the entry from `es_input.cfg` (with an ES restart as in 6.5); ES then asks again.
  - Export: `<inputConfig>` XML of a controller to download (to put into `controllers/custom/`).
  - Import: upload XML → take it over into `es_input.cfg` and run `inputconfiguration.sh` (with an ES restart).
  - Bluetooth: start a scan (live list via SSE), pair (pair + trust + connect), disconnect, remove; list of paired devices with connection status. A hint text per controller type explains how to enable pairing mode.
- **System:** logs (ES, web, SSH, fail2ban), banned IPs with "Unban", apt update/upgrade as a job, restart, shut down (with a confirmation dialog in the page).
- **Settings:** web credentials, ScreenScraper account.
- **Footer (every page):** "EmuStation Builder · AGPL-3.0 · Source code". "AGPL-3.0" links to https://www.gnu.org/licenses/agpl-3.0.html, "Source code" to `SOURCE_URL` from `web.conf` (default `https://github.com/mkloubert/es-builder`; `rel="noopener"`, new tab). Only http/https URLs are accepted; if `SOURCE_URL` is empty or invalid, the source code link is omitted (AGPL-3.0 §13).

### 6.4 ROM upload

1. The file is streamed into a staging directory on the SD card (never fully into RAM); the size limit is configurable.
2. **System detection** by file extension from `es_systems.cfg`. If it is ambiguous (e.g. `.zip`, `.bin`, `.iso`), the system is chosen in the review step.
3. **ZIP handling:**
   - Systems with an archive as ROM (arcade, Neo Geo, FBNeo, MAME): the ZIP is taken **unchanged**.
   - Otherwise: the ZIP is unpacked; files inside with matching extensions become ROMs, the rest is discarded and listed.
   - Protection: paths with `..` and absolute paths are rejected (zip slip), limit for the total unpacked size and the number of files (zip bomb).
4. **Review step:** per ROM the target system and a suggested display name (from the name cleanup), both editable; optional "scrape after applying".
5. **Apply:** move the files to `~/RetroPie/roms/<system>/` (an existing file with the same name → ask: overwrite/skip), set entries in `gamelist.xml`, restart ES.

**Name cleanup:** remove the extension; remove tags in round and square brackets (`(USA)`, `(Rev 1)`, `[!]`, `(Disc 1)` → the disc info is kept as "– Disc 1"); underscores/dots to spaces; "Title, The" → "The Title"; merge repeated spaces.

### 6.5 gamelist.xml and ES

ES overwrites `gamelist.xml` when it exits. So every change to `gamelist.xml` follows these steps: set `hold` → stop ES and wait for it to end → write the file (atomically: temporary file + rename, backup `gamelist.xml.bak` first) → remove `hold` → ES restarts. The UI announces the short restart. If a game is running, it asks whether to stop it.

### 6.6 Background jobs

Unpacking, scraping (Skyscraper: first `-s screenscraper` to collect, then generating the `gamelist.xml` – also with ES stopped) and apt update/upgrade run in a queue, only one at a time. Progress and log are shown live through Server-Sent Events. Jobs do not survive a restart of the service; interrupted jobs are marked "aborted" at start and their staging is cleaned up.

### 6.7 USB import

Games and BIOS files can also be imported from a USB stick (page "USB", helper sub commands
`usb-list`, `usb-mount`, `usb-umount`). This is described in
[2026-10-28-usb-import-design.md](2026-10-28-usb-import-design.md). The extra `web.conf` key is
`USB_MAX_FILES` (maximum number of files per import, default `20000`).

## 7. TLS

- At build time: if `secrets/ca.key` + `secrets/ca.crt` do not exist, a CA is created (ECDSA P-256, 10 years, CN "EmuStation Builder Local CA"). The CA is reused across builds.
- Each build issues a new server certificate (ECDSA P-256, **825 days**); SANs: `HOSTNAME`, `HOSTNAME.local`, entries from `TLS_EXTRA_SANS`.
- Only `server.crt` (incl. chain) and `server.key` go into the image, under `/etc/emulationstation-web/tls/` (key 0600, owner `emulationstation`). The CA key never leaves `secrets/`.
- `out/emulationstation-ca.crt` is provided for a one-time import.
- `./build.sh cert` issues a new server certificate without building an image (renewal through `scp` + service restart).
- Known consequences: all devices from the same image share the server key; a host name changed at first boot is valid only if it is in `TLS_EXTRA_SANS`.

## 8. Testing

- **Go:** table tests for name cleanup, system detection, `gamelist.xml` round trip (unknown fields are kept); tests for ZIP handling incl. zip slip and zip bomb; `httptest` tests for auth, failed-attempt logging, HTTPS requirement, upload flow; tests for reading/merging/removing `es_input.cfg` and GUID calculation against known reference values; Bluetooth logic against a D-Bus interface mock. `go vet`, `go test -race ./...`.
- **Bash:** `shellcheck` and `bash -n` over all scripts.
- **VM smoke test** (`test/vm-smoke.sh <image>`): boots the VM image headless in QEMU (KVM or HVF if available), with port forwarding, and checks:
  - HTTPS 11944 with valid auth → 200; wrong auth → 401; after 5 failed attempts → no connection any more (ban);
  - SSH login on 11911 works;
  - port scan (`nmap`) from the host's point of view: only 11911 and 11944 open;
  - the ES process runs; `emulationstation.txt.applied` exists if a file was provided.
- **Pi image, static** (`test/rpi4-static.sh <image>`): mount the image and check: users and groups, enabled units, configs (sshd, nftables, fail2ban), theme, emulator directories under `/opt/retropie/`, web binary for arm64.
- **Hardware:** `docs/hardware-checklist.md` – boot into ES, theme, template controllers usable without the wizard (USB and Bluetooth), unknown controller → wizard, Bluetooth pairing through the web UI, controller export/import, one game per system group, web functions, temperature under load.

## 9. Risks and open points

- **RetroPie on bookworm:** see `2026-10-31-bookworm-design.md` section 5 (moved from trixie on 2026-10-31).
- **Build time** of the base layer: on arm64 hosts natively about 1–2 h, on amd64 hosts under QEMU much longer (≥ 10 h possible). Precompiled RetroPie binaries are used where available. After that, the cache applies.
- **N64/PSP** depend on the game; this is documented and not a bug.
- **Controller GUIDs:** SDL 2.26 and newer (bookworm has 2.26.5) writes a CRC field into the GUID. The GUIDs of the templates and the calculation in the web UI must match the SDL version in the image; this is verified on real hardware. Fallback: additional matching by device name.
- **xpadneo** is built with DKMS against the Pi kernel; if that fails, it is listed in the build report and Xbox controllers work over USB only.
- **Basic Auth** is limited to HTTPS; without importing the CA the browser shows a warning.
