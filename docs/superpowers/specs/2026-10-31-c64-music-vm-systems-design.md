# EmuStation Builder – C64, Dreamcast and PS1 in the VM, C64 music (.sid), shell tools – Design

License: AGPL-3.0-or-later, see LICENSE

As of: 2026-10-31 · Status: approved by the owner (chat, 2026-10-31)

This spec changes the main spec `2026-10-27-emulationstation-image-design.md` (emulator list of
the VM images). Where the two disagree, this spec wins.

## 1. Goal and success criteria

The owner uploads `.d64` and `.sid` files of C64 games in the web UI. Today the VM image has no
C64 system, so `.d64` is not known there, and no image knows `.sid` (RetroPie's C64 system does
not list it). The owner also wants Dreamcast and PlayStation (PS1) in the VM, and the shell
tools `mc` and `net-tools` (`ifconfig`) in every image.

1. The VM images (`vm-amd64`, `vm-arm64`) install the RetroPie modules `vice` (C64),
   `flycast` (Dreamcast, standalone) and `lr-pcsx-rearmed` (PS1). The web UI then knows `.d64`, `.prg`,
   `.t64`, `.cdi`, `.gdi`, `.cue`, `.chd`, `.pbp`, … in the VM.
2. Every image with VICE has a new ES system `c64music` ("Commodore 64 Music") for `.sid` files.
   An upload of a `.sid` file goes to `~/RetroPie/roms/c64music/`. In ES, the file starts with
   VICE's SID player `vsid`.
3. Scraping is not offered for `c64music`: the web UI says so and does not start a job.
4. A failed `vice` build does not stop the build. Then there is no `c64music` system (the module
   is already listed in the build report).
5. Every image (rpi4 and VM) has the Debian packages `mc` and `net-tools`. In a login shell
   (SSH, tty) the user `emulationstation` can type `ifconfig` without the full path.

## 2. Decisions

| Topic | Decision | Reason |
|---|---|---|
| VM modules | add `vice`, `flycast` and `lr-pcsx-rearmed` to `provision/vm/modules.list` | owner's wish; RetroPie builds them from source for x86 and aarch64; same PS1 core as rpi4 |
| Shell tools | `mc` and `net-tools` in a new app script `provision/common/app-12-tools.sh` | CLAUDE.md: new packages go into the app layer, so the rpi4 base cache stays valid |
| `.sid` place | own ES system `c64music`, not more extensions for `c64` | `c64` starts every file with `x64`, and `x64` cannot start a `.sid` (VICE 3.7.1 `autostart.c` has no PSID support); a per-file emulator choice would be needed for every `.sid` |
| Player | `/opt/retropie/emulators/vice/bin/vice.sh vsid %ROM%` | `vsid` is part of VICE 3.7.1 (`bin_PROGRAMS` in `src/Makefile.am`), `vice.sh` is RetroPie's start script with zip support |
| Theme | `<theme>c64</theme>` | ES uses the C64 art of the active theme; no theme files are copied (EpicNoir stays an aggregate) |
| Where | new app script `provision/common/app-25-c64music.sh` | app layer, no change to the base cache key for rpi4 |
| Scraping | `scrape.Supported(system)` is false for `c64music` | ScreenScraper has no SID music; Skyscraper would fail with an unclear error |

Ruling: put `.sid` into an own system and not into `c64` — `x64` cannot play it, and a
per-file emulator would need an entry per upload — cost if wrong: one more system in the ES menu.

Ruling: the VM change goes into the base layer (`modules.list`) — RetroPie modules can only be
installed there — cost if wrong: the next VM build of each arch takes hours (no cache).

Ruling: `vice.sh` gets `-chdir <dir>` before the file, also for `vsid` — `-chdir` is a common
VICE option of every VICE program — cost if wrong: `.sid` does not start in ES (the hardware
checklist, section 6, shows it).

Ruling: use the standalone `flycast` module in the VM, not `lr-flycast` — `lr-flycast` builds the
moving `master` branch of libretro/flycast, and its RetroPie patch `02_gcc14_unistd_fix.diff` no
longer applies there (vm-arm64 build, 2026-10-31); `flycast` has no patches — cost if wrong: a
different Dreamcast emulator than on the Pi (rpi4 keeps the `lr-flycast` binary).

Ruling: Dreamcast in the VM runs with software rendering (no GPU in most VMs) — the VM is for
testing, playing is not a goal (README) — cost if wrong: slow games in the VM.

## 3. Changes

### 3.1 VM emulator list (`provision/vm/modules.list`)

Append `vice`, `flycast` and `lr-pcsx-rearmed`. This changes the base cache key of both VM targets (rpi4 is not
affected). A failure is handled like every other module (report line, build goes on).

### 3.2 C64 music system (`provision/common/app-25-c64music.sh`)

Runs in the chroot as root, on every target. Idempotent. Test overrides: `PROVISION_ROOT`.

1. If `/opt/retropie/emulators/vice/bin/vsid` is not an executable file: log
   `vsid is missing (vice is not installed), no c64music system` and exit 0.
2. If `/etc/emulationstation/es_systems.cfg` is missing: `die` (RetroPie core is broken).
3. Write `/opt/retropie/configs/c64music/emulators.cfg` (temp file + rename, mode 0644, owner
   `emulationstation`, folder too):
   ```
   vice-vsid = "/opt/retropie/emulators/vice/bin/vice.sh vsid %ROM%"
   default = "vice-vsid"
   ```
4. Create `/home/emulationstation/RetroPie/roms/c64music` (owner `emulationstation`, 0755).
5. In `es_systems.cfg` (with `xmlstarlet`, temp file + rename, mode kept at 0644): remove every
   `system` with `name` = `c64music`, then add one new `system` directly after the `c64` system
   (or at the end when there is no `c64` system):
   ```
   <system>
     <name>c64music</name>
     <fullname>Commodore 64 Music</fullname>
     <path>/home/emulationstation/RetroPie/roms/c64music</path>
     <extension>.sid .SID</extension>
     <command>/opt/retropie/supplementary/runcommand/runcommand.sh 0 _SYS_ c64music %ROM%</command>
     <platform>c64</platform>
     <theme>c64</theme>
   </system>
   ```
   The result must be valid XML (`xmlstarlet val -w`), else `die`.

### 3.2a Shell tools (`provision/common/app-12-tools.sh`)

Runs in the chroot as root, on every target. For each package of `mc net-tools` that `dpkg -s`
does not know: one `apt-get update`, then one `apt-get install -y --no-install-recommends` with
the missing packages (stdin `/dev/null`, `DEBIAN_FRONTEND=noninteractive`). When all are there,
apt is not called. A failed install stops the build.

### 3.2b sbin in PATH (`overlay/etc/profile.d/20-emulationstation-sbin.sh`)

Debian 12 gives normal users no sbin folder in `PATH`, so `ifconfig` (in `/usr/sbin`) is not
found. The new file (root:root, 0644, read by every login shell) appends `/usr/local/sbin`,
`/usr/sbin` and `/sbin` to the end of `PATH`, each only when it is missing.

Ruling: append, never prepend — a folder at the end cannot hide a program of the same name in
an earlier folder; the sbin folders are root-owned and no right changes (root tools still need
`sudo`); Debian 13 does the same for all users — cost if wrong: none for security, a user tool
named like a sbin tool keeps winning.

### 3.3 Web UI (`webui/internal/scrape`, `webui/internal/web`)

- `scrape.Supported(system string) bool`: false for `c64music`, true for all other systems.
- `POST /games/{system}/scrape` on an unsupported system: HTTP 400,
  `Scraping is not available for this system.` No job is started.
- Upload with "Scrape after applying": files of unsupported systems are left out of the scrape
  job. When no file is left, no job starts and the normal redirect follows.
- The games page of an unsupported system shows no "Scrape all" button and no "Scrape" menu
  item (`Scrape` flag in the view).

### 3.4 Tests and docs

- bats `test/bats/c64music.bats`: no vsid → nothing changes; with vsid → system after `c64`,
  emulators.cfg and ROM folder; second run gives the same file (one `c64music`); no `c64` →
  system at the end; missing `es_systems.cfg` → error.
- Go: table test for `scrape.Supported`; handler test for the 400; upload test that a
  `c64music` file starts no scrape job.
- bats `test/bats/tools.bats`: apt is called only for missing packages; nothing is called when
  all are there.
- `test/lib/static-app.sh`: `mc` and `ifconfig` are in the image; the profile.d file exists.
- bats `test/bats/tools.bats`: the profile.d file appends missing sbin folders at the end,
  adds nothing twice, and keeps the order of the old `PATH`.
- `test/lib/static-app.sh`: when `vsid` is in the image, `es_systems.cfg` has `c64music` with
  `.sid` and the `emulators.cfg` default is `vice-vsid`.
- `docs/hardware-checklist.md`: a `.sid` plays in ES on the Pi, and you can leave the player.
- README: `.sid` files, the new system, and the VM emulators.
