# EmuStation Builder – USB stick import – Design

License: AGPL-3.0-or-later, see LICENSE

As of: 2026-10-28 · Status: draft for approval

Builds on: `docs/superpowers/specs/2026-10-27-emulationstation-image-design.md` (the main spec). Everything there stays valid. This spec only adds the USB import and one fix from Plan 2.

## 1. Goal and success criteria

The owner puts ROMs and BIOS files on a USB stick on a PC. The owner plugs the stick into the running device (Pi or VM), opens the web UI and clicks **Import**. The device then imports the files through the same pipeline as the web upload: unpack ZIPs, clean names, detect duplicates, match systems, write the gamelist.

Success:

- A stick with FAT32, exFAT or NTFS works without extra steps on the device.
- An empty stick (no `emulationstation/` folder) gets the folder layout on the first **Import**, like RetroPie's USB service. The owner fills it on the PC and imports again.
- Files in `roms/<system>/` land in that system without a question, if the system accepts the file extension.
- Files that cannot be matched without a guess go to the existing review page. Nothing is guessed.
- Importing the same stick again only adds new files ("already imported" files are skipped without a question).
- A report `import-report.txt` is written to the stick.
- Nothing is ever deleted from the stick.
- Nothing happens without a click in the web UI (no automatic import on plug-in).

Not a goal: automatic import on plug-in, build-time ROM import, network transfer (rsync/SMB), syncing configs (RetroPie's `configs/to_retropie` is left out on purpose: it would let a stick write runcommand scripts that run as the user).

## 2. Stick layout

```
emulationstation/
  README.txt              written by the device (how to use the stick)
  roms/<system>/...       e.g. roms/snes/Zelda.zip, roms/psx/Game/Game.cue
  bios/...                BIOS files
  import-report.txt       written by the device after each import
```

- `<system>` is a system name from `es_systems.cfg` (the same names as RetroPie's `~/RetroPie/roms/<system>`).
- Sub folders below `roms/<system>/` are allowed (deep scan). The system hint comes from the first folder below `roms/`.
- Files directly in `roms/` (no system folder) get no hint.
- A stick prepared for RetroPie's USB service (`retropie/roms/...`, `retropie/BIOS/...`) is read too. If both folders exist, `emulationstation/` wins and the report says that `retropie/` was ignored. The device never creates `retropie/`.
- Ignored (junk): names starting with `._`, `.DS_Store`, `Thumbs.db`, `desktop.ini`, `System Volume Information/`, `$RECYCLE.BIN/`, `.Trashes/`, `.Spotlight-V100/`, `.fseventsd/`, and the device's own `README.txt` and `import-report.txt`.
- Symlinks on the stick are not followed (FAT/exFAT have none; NTFS reparse points are skipped).

## 3. Privileged helper (`/usr/local/lib/emulationstation/priv`)

Three new sub commands. sudoers does not change (the helper is already allowed NOPASSWD).

- `usb-list` — prints one line per candidate partition, TAB-separated: `<path>\t<fstype>\t<size-bytes>\t<label>`. Source: `lsblk -J -b -o PATH,TRAN,FSTYPE,SIZE,LABEL,TYPE,RM,MOUNTPOINTS` (parsed with `jq`). A candidate is a partition (or a whole disk without partitions) whose disk has `TRAN=usb`, with `FSTYPE` in `vfat`, `exfat`, `ntfs`. The root disk (the disk that holds `/`) is never listed, even if it is on USB (Pi booting from a USB SSD). Labels with control characters or TABs are replaced by `?`.
- `usb-mount <path>` — `<path>` must match `^/dev/[a-z0-9]+$` and must be in the current `usb-list` output (checked again inside the helper, no trust in the caller). Mount point: `/run/emulationstation-usb`, owned by root (`0755 root:root`, created by the helper; refuse if it is a symlink or not a plain directory). It must not live below `/run/emulationstation/`, because that folder belongs to the web user, who could replace the mount point with a symlink that root then follows. Partitions that are already mounted somewhere else are not listed. Only one stick at a time: if something is mounted there, it is unmounted first. Options: `rw,nodev,nosuid,noexec,noatime,uid=<emulationstation>,gid=<emulationstation>` plus per type: `vfat` and `exfat` `fmask=0137,dmask=0027`; `ntfs` uses the kernel driver `-t ntfs3` with `fmask=0137,dmask=0027`. Prints the mount point.
- `usb-umount` — `sync`, then `umount /run/emulationstation-usb`; if the device is gone, `umount -l`. Exit 0 when nothing is mounted.

Mount namespace: the web service runs with `ProtectSystem=full`, so it has its own mount namespace. The helper runs as a child of the service (via sudo) and mounts inside that namespace. Only the web service sees the stick. This is wanted. It must be checked on hardware (checklist).

Validation and logging follow the existing helper style (`usage`, exit 2 on bad input).

## 4. Web service

### 4.1 New config key (web.conf)

- `USB_MAX_FILES` — maximum number of files on one stick per import, default `20000`. Above it the import stops before copying, with a clear message.

`MAX_EXTRACT_MB` and `MAX_ZIP_ENTRIES` apply per ZIP as for uploads. `MAX_UPLOAD_MB` does not apply to stick files (CD images are large); the free space check (4.3) replaces it.

### 4.2 Page "USB" (`GET /usb`)

- Nav entry "USB" next to "Upload".
- Shows the sticks from `usb-list`: label, size, file system. Buttons per stick: **Import**. Global buttons: **Refresh**, **Eject** (calls `usb-umount`).
- Empty state: "No USB stick found. Plug in a stick with FAT32, exFAT or NTFS and click Refresh."
- Texts in simple English, like the rest of the UI.

### 4.3 Import (`POST /usb/import`, form field `device`)

Runs as a job (existing `jobs` package, progress over SSE on `/jobs/{id}`):

1. `usb-mount <device>`.
2. No `emulationstation/` and no `retropie/` folder → **prepare**: create `emulationstation/roms/<system>/` for every system in `es_systems.cfg`, `emulationstation/bios/`, and `emulationstation/README.txt` (layout, supported file systems, "nothing is deleted from this stick"). `usb-umount`. Job result: "Stick prepared. Fill it on your PC, then import again." End.
3. Scan the tree (no symlinks, skip junk). Count files; above `USB_MAX_FILES` → stop with a message.
4. Free space check: sum of file sizes plus, for ZIPs that will be unpacked, their uncompressed size (from the ZIP directory) plus 5 % reserve, against free space of the file system that holds the staging folder and the ROM folders. Too little → stop with "Not enough space on the SD card: need X, free Y."
5. Stage: `Stager.StageTree(...)` — one `Upload` per top-level folder below `roms/` plus one for loose files in `roms/` (a staged file name is its path below that folder, because the commit keeps that path below `~/RetroPie/roms/<system>/`; one upload per folder keeps `snes/Mario.zip` and `nes/Mario.zip` apart). All uploads of one import open together on the review page (`/upload/<id>,<id>,…`). Each file is copied (streamed) into the staging folder; ZIPs are handled like uploads (`shouldUnpack`). The system hint for a file:
   - hint system accepts the extension → `Candidates = [hint]` (no question);
   - hint system does not accept it → normal candidates, the report says "folder snes does not accept .iso";
   - no hint → normal candidates.
   Companion files (cue/bin, m3u, gdi) stay together as in the upload flow.
6. "Already imported" (checked **before** copying for every file that is not a ZIP to be unpacked, so a re-import copies nothing and needs no free space; unpacked ZIPs are checked after extraction): a primary file whose target path (`~/RetroPie/roms/<system>/<cleaned name>`) already exists with the same size is marked `Discarded` with reason `already imported`, together with its companions. It is not a conflict and needs no question.
7. BIOS: every file directly in `bios/`, plus every file directly in `bios/dc/` (Dreamcast, as `bios.Save` supports `dc/<name>`), is saved with the existing `bios.Save` (same name rules and size limits as the BIOS upload). Existing BIOS files with the same name and size are skipped as "already imported"; a different size is overwritten (the stick is the newer source) and reported.
8. Write `import-report.txt` (overwrite) with: date, counts, one line per file: `staged`, `bios saved`, `skipped: <reason>`, `needs review`. Then `usb-umount`.
9. Job result links to the review page for the staged upload, if it has files that are not discarded. If every file was already imported, the job says so and nothing is staged.

The review page then works as today (choose system for open files, edit names, commit). The commit works exactly as in the upload flow today: ES is stopped, the files are moved from staging to the ROM folders (same file system, rename), the gamelists are written, and ES starts again. Imported BIOS files need no ES restart.

Report lines for files decided on the review page are not written back to the stick (the stick is already unmounted). The web UI shows them as today.

### 4.4 Review page for many files

A stick can stage thousands of files. The review page (`GET /upload/{ids}`) gets:

- A summary at the top: "N files ready, M need a decision, K skipped".
- Files that need a decision (more than one candidate, no candidate, conflict) are shown first and open.
- Files that are ready (one candidate) are in a collapsed group per system, with their cleaned names. They can be opened and edited as today.
- Discarded files (junk, duplicate, already imported) are in one collapsed group with their reasons.

The commit form must stay small for thousands of files: inputs of ready files are disabled (not sent) until the owner opens a file to edit it; the server already uses the first candidate and the suggested name for a file without form fields.

### 4.5 Errors

- Stick removed during the job → the job fails with "The USB stick was removed.", the staged upload is discarded, `usb-umount` (lazy) runs.
- Read error on one file → that file is reported as `skipped: read error`, the import goes on.
- Disk full during staging → job fails with the existing "Not enough space on the SD card." message; the staged upload is discarded.
- Only one USB job at a time; a second click shows "A USB import is already running."
- Every path from the stick is checked with the existing `SafeName` / path rules; names that fail are reported as `skipped: unsafe name`.

### 4.6 CLI

No new CLI command. (The import needs the review page; a CLI without review is out of scope.)

## 5. Image

- Kernel modules `vfat`, `exfat`, `ntfs3` must exist for every installed kernel (Pi: v8 and 2712; VM: the installed kernel). `test/rpi4-static.sh` and `test/vm-static.sh` check this with `modinfo -k <release>` in the chroot. If one is missing on the Pi kernel, the build report says so and the README says which file systems work.
- `lsblk` (util-linux) and `jq` must be in the image. `jq` is not installed yet: add it to the **app** layer (an `apt-get install` in the app stage), not to the base package list, so the base cache key does not change.
- No new ports, no new services, no udev rules.

## 6. Fix from Plan 2 (N1)

`webconf_no_plain_password` (`builder/lib/webconf.sh`) makes the build fail for short web passwords like `0000`, `home`, `station`, `github` or the owner's GitHub name in `SOURCE_URL`, with the wrong message "The web password is in web.conf as plain text".

New rule: the exact-value check stays on every line (a value equal to the password fails). The substring check runs only on the **value** part (never the key name) and only for passwords of 8 or more characters. Tests in `test/bats/app.bats`: `0000`, `home`, the `SOURCE_URL` user name → pass; a value equal to the password → fail; an 8+ character password inside a value → fail.

## 7. Testing

- bats for the helper: stubs for `lsblk` (JSON fixtures: USB stick, USB root disk, SATA disk, label with TAB), `mount`, `umount`, `findmnt`. Cases: list filters, mount refuses a device not in the list, refuses bad paths, picks `ntfs3` for `ntfs`, unmount when nothing is mounted.
- Go tests with a temp folder as the stick: layout detection (`emulationstation/`, `retropie/`, both, none), prepare, hints (match, mismatch, none), deep folders, junk, already imported, companions across sub folders, file limit, free space check (injected `statfs`), BIOS skip/overwrite, report content, unsafe names, review page grouping.
- Static image checks: kernel modules and tools as in section 5.
- Hardware checklist (user): FAT32, exFAT and NTFS stick on the Pi; pull the stick during an import; re-import the same stick; Pi booting from a USB SSD does not list the root disk; the mount is not visible in an SSH shell (namespace) and the import still works.

## 8. Risks

- Kernel module names or availability on the Pi kernel (`ntfs3`) — checked by the static test, documented if missing.
- Mount inside the service namespace — expected to work (same namespace for the sudo child); hardware check.
- Very large sticks: staging copies the data once onto the SD card; commit is a rename. Time is bound by the stick and SD card speed.

## 9. Fix: flat `lsblk` JSON (2026-10-31)

Found in a UTM VM (Debian 12, util-linux 2.38): `lsblk -J -o PATH,PKNAME,…` without the `NAME`
column prints a **flat** list. A partition is its own entry with `"pkname": "sda"`, not a
`children` entry of its disk. lsblk draws the tree in the `NAME` column, so without it there is
no tree. The `usb-list` filter only read `children`, so it never listed a partition, and the web
UI said "No USB stick found" (also on the Pi, which has the same Debian).

- `usb-list` accepts both forms. When no entry has `children`, the filter builds them: every entry
  without `pkname` is a top-level device, and its children are the entries whose `pkname` is its
  name (`path` without `/dev/`). Deeper levels (for example LVM or crypt on a partition) are not
  used, as before.
- The rest of the filter (USB, file system, root disk, mount points, path) is unchanged.
- Tests: a fixture with the flat output from the VM (`test/fixtures/lsblk/flat-usb-stick.json`),
  a flat fixture where `/` is on the USB disk, and the old nested fixtures still pass.

Ruling: read both forms instead of adding `NAME` to the columns — the flat form is what the real
system printed, and both forms stay covered by tests — cost if wrong: none; a nested output
works as before.
