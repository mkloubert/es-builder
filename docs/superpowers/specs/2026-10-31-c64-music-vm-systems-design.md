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
5. Every image (rpi4 and VM) has the Debian packages `mc` and `net-tools`.
6. VICE (C64) starts with the owner's joystick setup (section 3.2c): port 1 = keyset 2
   (W/S/A/D, fire Return), port 2 = keyset 1 (arrow keys, fire Space). A pad button opens the
   VICE menu (to quit), and pad button 2 is the C64 RETURN key. In a login shell
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

### 3.2c VICE joysticks (`provision/common/app-25-c64music.sh`)

Owner decision (chat, 2026-10-31): the C64 joystick ports use keyboard keysets, not game pads.
This replaces the first version of this section (`JoyDevice1/2=4`, pads on both ports).

Checked in VICE 3.7.1 (`src/joyport/joystick.c`, `src/keyboard.c`, `src/arch/sdl/kbd.c`):

- `JoyDevice<n>`: 0 none, 1 numpad, 2 keyset 1, 3 keyset 2, 4 SDL joystick (pad).
- `KeySetEnable=1` allows keyset joysticks (the VICE default is already 1; it is written anyway).
- `KeySet<1|2><North|South|West|East|Fire>` hold SDL1 key codes; VICE maps SDL2 keys to SDL1
  codes (`SDL2x_to_SDL1x_Keys`): Up 273, Down 274, Right 275, Left 276, Space 32, Return 13,
  a 97, d 100, s 115, w 119.
- "Swap native joystick ports" is no resource (`_sdljoy_swap_ports` in `arch/sdl/joy.c`); VICE
  always starts with it off.
- A key of an active keyset is used by the joystick and is **not** sent to the C64 keyboard
  (`keyboard.c`: `joystick_check_set` returns before `kbd_queue_pushkey`).

So the script sets, in the `[C64]` section of `/opt/retropie/configs/c64/sdl-vicerc` (other lines
stay, the section is added when missing, owner `emulationstation`, mode 0644):

```
JoyDevice1=3
JoyDevice2=2
KeySetEnable=1
KeySet1North=273
KeySet1South=274
KeySet1West=276
KeySet1East=275
KeySet1Fire=32
KeySet2North=119
KeySet2South=115
KeySet2West=97
KeySet2East=100
KeySet2Fire=13
JoyMenuControl=1
```

`JoyMenuControl=1` stays: VICE's default pad map still sends pad button 1 to the menu (this does
not depend on `JoyDevice`). Pads no longer move a C64 joystick.

This runs when `sdl-vicerc`'s folder `/opt/retropie/configs/c64` exists (VICE installed), also
when `vsid` is missing.

Ruling: keysets as the owner asked, also though Return, Space, W/A/S/D and the arrow keys then
do not reach the C64 keyboard — the pad RETURN (joymap, below) writes the C64 key matrix directly
and still works — cost if wrong: typing `RUN` + Return on a keyboard needs the pad button or the
VICE menu to switch the keysets off.

**RETURN on the pad.** Many C64 games need RETURN (to start, or after `LOAD`). VICE loads the
joymap file `~/.config/vice/sdl-joymap-C64.vjm` (RetroPie: `/opt/retropie/configs/c64/`) after
the default map, and reads it as a patch ("A joystick map is read in as patch to the current
map", `src/arch/sdl/joy.c`). So a file without `!CLEAR` changes only the listed buttons. The
script writes, when the file is missing:

```
# EmuStation Builder: RETURN on pad button 2 (patch on the VICE default map)
0 1 2 2 0 1
1 1 2 2 0 1
```

Format: `joynum inputtype(1 = button) inputindex action(2 = keyboard) row col`. RETURN is row 0,
column 1 of the C64 key matrix (`data/C64/sdl_sym.vkm`: `13 0 1 8 /* Return */`). Button 2 had
the action "map" (open the mapping dialog) with `JoyMenuControl=1`, so no game function is lost.
A line for a second pad that is not there only logs an error in VICE.

Ruling: button 2 for RETURN — the only default button without a game function; Start/Select have
different numbers on each pad — cost if wrong: RETURN is on a button the owner did not expect; it
can be changed in the VICE menu.

Ruling: write the joymap only when it is missing — the owner may have saved an own map in the VICE
menu (VICE writes the whole map with `!CLEAR`) — cost if wrong: an old file from a previous image
is kept (only on an updated card, not on a new image).

Ruling: use VICE's default pad map, no own joymap file — the map is the same for every pad and
needs no per-pad file — cost if wrong: on some pads the fire button is not the wanted one; the
owner can change it in the VICE menu (it saves a joymap file).

### 3.2d Flycast OpenGL in the VM (`provision/vm/app-20-flycast.sh`)

In the VM, RetroPie uses `platform_native` (flags `gl vulkan kms`), so `flycast` asks for a
desktop OpenGL 3+ context. With UTM (arm64, virtio-gpu with virgl, Mesa 22.3.6) the context fails
(`Error creating SDL GL context`). With `MESA_GL_VERSION_OVERRIDE=4.3 MESA_GLSL_VERSION_OVERRIDE=430`
Mesa gives "4.3 (Core Profile)" on virgl, and flycast starts (owner test, 2026-10-31).

The script runs on the VM only. In `/opt/retropie/configs/dreamcast/emulators.cfg`, the line
`flycast = "<command>"` gets the prefix
`MESA_GL_VERSION_OVERRIDE=4.3 MESA_GLSL_VERSION_OVERRIDE=430 ` (runcommand runs the command
through the shell, so the variables reach flycast). An old prefix of `NAME=value` words is removed
first, so the line has the prefix once. Other lines stay. When the file or the line is missing
(flycast not installed), the script logs and does nothing. Owner `emulationstation`, mode 0644.

Ruling: override the GL version in the VM only — virgl can run flycast's GL 4.3 code but reports a
lower version; on the Pi, `lr-flycast` uses its own GLES path and needs nothing — cost if wrong: a
VM GPU that lacks a GL 4.3 feature shows wrong graphics or crashes (the VM is for testing).

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
- bats `test/bats/c64music.bats`: the joymap is written when missing (mode 0644, the two lines)
  and an existing file is not changed.
- bats `test/bats/c64music.bats`: `sdl-vicerc` gets the lines of 3.2c in `[C64]`, other lines
  and sections stay, values are replaced (not added twice), a missing `[C64]` is added.
- bats `test/bats/vm-flycast.bats`: the prefix is added once (also on a second run and over an
  old prefix), other lines stay, a missing file or line changes nothing.
- `test/vm-static.sh`: when the dreamcast `emulators.cfg` has a `flycast` line, it has the prefix.
- `test/lib/static-app.sh`: when VICE is in the image, `sdl-vicerc` has `JoyDevice1=3`,
  `JoyDevice2=2`, `KeySetEnable=1`, and the joymap has the RETURN line.
- `test/lib/static-app.sh`: when `vsid` is in the image, `es_systems.cfg` has `c64music` with
  `.sid` and the `emulators.cfg` default is `vice-vsid`.
- `docs/hardware-checklist.md`: a `.sid` plays in ES on the Pi, and you can leave the player.
- README: `.sid` files, the new system, and the VM emulators.
