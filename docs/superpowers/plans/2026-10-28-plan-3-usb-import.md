# Plan 3: USB stick import – Implementation Plan

License: AGPL-3.0-or-later, see LICENSE

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The owner clicks **Import** on a new web UI page "USB" and the device imports ROMs and BIOS files from a FAT32/exFAT/NTFS stick through the existing upload pipeline (stage → review → commit), prepares an empty stick, skips files that are already imported and writes a report to the stick. Plus the fix N1 from Plan 2.

**Architecture:** The root helper `priv` gets `usb-list`, `usb-mount`, `usb-umount` (strict checks, mount at `/run/emulationstation-usb` inside the web service's mount namespace). A new Go package `usb` detects the stick layout, prepares it, scans it and writes the report. `roms.Stager` gets `StageTree`, which stages a folder of the stick as one upload, with the folder name as system hint. A job runs the import; the existing review page (made usable for thousands of files) and commit finish it.

**Tech Stack:** Bash (helper, bats), Go 1.26.x (web service), `lsblk` + `jq` on the target, Tailwind (existing CSS build), kernel drivers `vfat`, `exfat`, `ntfs3`.

**Spec:** `docs/superpowers/specs/2026-10-28-usb-import-design.md` (main spec: `docs/superpowers/specs/2026-10-27-emulationstation-image-design.md`) · **Prerequisite:** Plans 1 and 2 are implemented.

## Global Constraints

- **No git commands that write.** "Checkpoint" = list the changed files for the user.
- All code, comments, messages, UI and docs in simple English.
- Every new own source file starts with the AGPL-3.0 header (copy it from `builder/run.sh`, after a shebang line if present); `scripts/check-license-headers.sh` must exit 0.
- All scripts: `#!/usr/bin/env bash` (the helper keeps `#!/bin/bash` with its fixed `PATH`), `set -euo pipefail`, clean under `shellcheck -x`.
- Ports stay `11911` and `11944`; no new services, no udev rules, no automatic import.
- sudoers does not change: NOPASSWD stays only for `/usr/sbin/reboot`, `/usr/sbin/poweroff`, `/usr/local/lib/emulationstation/priv`.
- Nothing is ever deleted from the stick. The stick is never mounted with `exec`, `suid` or `dev`.
- The base layer cache key must not change (no edits to `provision/*/base-*.sh` or `modules.list`).
- Existing Plan 1/2 contracts (file contract `/run/emulationstation/*`, helper sub commands, review/commit form fields `system|<id>|<name>`, `name|…`, `skip|…`) stay valid.
- Verify locally with: `bats test/bats </dev/null`, `shellcheck -x` on all shell scripts, `scripts/check-license-headers.sh`, `cd webui && go vet ./... && go test -race -count=1 ./...`, gofmt clean.

## Review Focus

1. **A stick that is the Pi's own boot disk (USB SSD)** must never be listed or mounted. Test: lsblk fixture where `/` is on a USB disk → `usb-list` prints nothing for it (Task 1).
2. **Same file name in two system folders** (`roms/snes/Mario.zip`, `roms/nes/Mario.zip`) must not collide in staging. Test: both end up in their own upload and commit to their own system (Task 2).
3. **Re-import of the same stick** must not create conflicts or questions. Test: stage, commit, stage again → every file `already imported`, no upload with open files, job says "Nothing new" (Tasks 2, 3).
4. **Stick pulled out during the import** must leave no half upload and no stale mount. Test: injected read error `EIO` + device gone → job error "The USB stick was removed.", upload discarded, `USBUmount` called (Task 3).
5. **Thousands of files on the review page** must still commit. Test: 5000 staged ready files, POST with only the fields of 2 edited files → all 5000 committed, body < 1 MiB (Task 3).

---

### Task 0: Fix N1 (plain-text web password check)

**Files:**
- Modify: `builder/lib/webconf.sh` (`webconf_no_plain_password`)
- Test: `test/bats/app.bats`

**Interfaces:** `webconf_no_plain_password <file> <password>` keeps its name, arguments and return codes.

- [ ] **Step 1: Write the failing tests** in `test/bats/app.bats` on a web.conf made with the real writer (`webconf_set` of all keys app-30 writes, `SOURCE_URL=https://github.com/alice/es-builder`, a real bcrypt hash):
  - password `0000` → returns 0
  - password `home` → returns 0
  - password `alice` → returns 0
  - a line `LISTEN=secretpw` with password `secretpw` (exact value) → returns 1
  - password `longpassword` and a line `HOME_DIR=/x/longpassword/y` → returns 1
  - password `longpassword` only inside `WEB_PASSWORD_HASH` by chance → returns 0
- [ ] **Step 2:** Run `bats test/bats/app.bats </dev/null` → the first three FAIL.
- [ ] **Step 3:** Change the loop: exact-value check on every line as before (except the skipped keys); the substring check runs on `${line#*=}` only (never the key name), and only when `${#pw} -ge 8`. Update the comment above the function (the old "whatever its length" sentence is wrong).
- [ ] **Step 4:** `bats test/bats </dev/null` → all green; `shellcheck -x builder/lib/webconf.sh`.
- [ ] **Step 5: Checkpoint.**

### Task 1: Helper sub commands `usb-list`, `usb-mount`, `usb-umount`

**Files:**
- Modify: `overlay/usr/local/lib/emulationstation/priv` (usage line, three new `case` branches, helper functions)
- Modify: `provision/common/app-30-webui.sh` (install `jq` in the app layer: `DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends jq </dev/null`, skip when `dpkg -s jq` succeeds)
- Create: `test/fixtures/lsblk/usb-stick.json`, `usb-root.json`, `sata.json`, `label-tab.json`, `no-partition.json`
- Test: `test/bats/priv.bats`

**Interfaces (produced, used by Task 2):**
- `priv usb-list` → stdout lines `<path>\t<fstype>\t<size-bytes>\t<label>`; exit 0 also when empty.
- `priv usb-mount <path>` → stdout `/run/emulationstation-usb`; exit 2 on bad input, 1 on mount failure.
- `priv usb-umount` → exit 0 (also when nothing is mounted).

Rules (spec section 3):
- Source: `lsblk -J -b -o PATH,PKNAME,TRAN,FSTYPE,SIZE,LABEL,TYPE,MOUNTPOINTS`, parsed with `jq`. A candidate: `TYPE=part` whose parent disk has `TRAN=usb`, or `TYPE=disk` with `TRAN=usb` and no children; `FSTYPE` in `vfat|exfat|ntfs`.
- The root disk is excluded: the disk of `findmnt -n -o SOURCE /` (follow `PKNAME`).
- Labels: control characters and TAB → `?`; empty label → `-`.
- `usb-mount`: `<path>` must match `^/dev/[a-z0-9]+$` **and** be in the helper's own fresh `usb-list` result. Create `/run/emulationstation-usb` owned by root (`0755 root:root`; refuse a symlink or non-directory; never below the web user's `/run/emulationstation/`). If it is a mount point (`mountpoint -q`), unmount first. Options `rw,nodev,nosuid,noexec,noatime,uid=<id -u emulationstation>,gid=<id -g emulationstation>,fmask=0137,dmask=0027`; type `vfat`, `exfat`, or `ntfs3` for `ntfs`. Mount with `mount -t <type> -o <opts> -- <path> <dir>`.
- `usb-umount`: not a mount point → exit 0. Else `sync -f <dir>` (ignore error), `umount <dir>`, on failure `umount -l <dir>`.
- Tests use `PRIV_TEST_PATH` stubs (existing pattern in `priv.bats`) for `lsblk` (prints a fixture), `findmnt`, `mount`/`umount`/`mountpoint`/`sync`/`install`/`id` (record arguments to a file).

- [ ] **Step 1: Write the failing tests:** list of a FAT32 stick (one line, exact TAB format); USB root disk → no line; SATA disk → no line; `ext4` USB partition → no line; label with TAB → `?`; unpartitioned exFAT stick → listed; mount of a listed device → `mount` called with `-t vfat` and the exact options; `ntfs` → `-t ntfs3`; mount of `/dev/sda1` that is not listed → exit 2, `mount` not called; `../`, `/dev/sda1;x`, empty → exit 2; mount when already mounted → `umount` before `mount`; `usb-umount` when not mounted → exit 0, no `umount`; `umount` fails → `umount -l` called. app-30: a bats case with stubbed `dpkg`/`apt-get` checks that `jq` is installed only when missing.
- [ ] **Step 2:** Run `bats test/bats/priv.bats </dev/null` → FAIL.
- [ ] **Step 3:** Implement. Keep the existing helper style (`usage`, `no_args`, fixed `PATH`).
- [ ] **Step 4:** `bats test/bats </dev/null`, `shellcheck -x` on the helper → green.
- [ ] **Step 5: Checkpoint.**

### Task 2: Go – priv wrapper, `usb` package, `Stager.StageTree`, config key

**Files:**
- Modify: `webui/internal/priv/priv.go` (+ test)
- Create: `webui/internal/usb/layout.go`, `scan.go`, `report.go`, `import.go` (+ `_test.go` files)
- Modify: `webui/internal/roms/stage.go` (new `tree.go` is fine), `webui/internal/config/config.go` (+ tests)
- Modify: `webui/internal/jobs/jobs.go` (job link, + test)

**Interfaces (produced, used by Task 3):**
- `priv`: `type USBDevice struct{ Path, FSType, Label string; Size int64 }`; `func (p Priv) USBList(ctx context.Context) ([]USBDevice, error)`; `func (p Priv) USBMount(ctx context.Context, path string) (string, error)`; `func (p Priv) USBUmount(ctx context.Context) error`.
- `config`: `Config.USBMaxFiles int`, key `USB_MAX_FILES`, default `20000`, must be ≥ 1 (same validation and save style as `MAX_ZIP_ENTRIES`).
- `jobs`: `Job.Link string` (`json:"link,omitempty"`); optional interface `type Linker interface{ SetLink(url string) }`, implemented by the queue's reporter (the `Reporter` interface itself does not change).
- `roms`:
  - `StagedFile.Note string` (extra info for the report and the review page, e.g. `folder snes does not accept .iso`).
  - `const reasonImported = "already imported"` (exported as `ReasonImported`).
  - `func (s *Stager) StageTree(ctx context.Context, srcDir, hint string, rels []string, progress func(n int64)) (*Upload, error)` — one upload; `rels` are slash paths below `srcDir`; every file is streamed (`O_EXCL`) to `<staging>/<id>/<rel>`; a ZIP is handled like an upload ZIP (`shouldUnpack` → extract into the ZIP's own folder inside the upload, file names relative to the upload root) under the existing limits; `progress` gets copied bytes. `ctx` cancels between files and inside the copy loop.
  - Hint: if `hint` is a known system and in the file's candidates → `Candidates = [hint]`; if not in its candidates → normal candidates and `Note = "folder <hint> does not accept <ext>"`.
  - Already imported: a primary file is `Discarded` with `ReasonImported` (its companions too) when, for its only candidate (or for any candidate when there are several), `<Roms>/<system>/<Name>` exists as a regular file with the same size.
  - On any error the upload directory is removed (as in `Receive`).
- `usb`:
  - `type Layout struct{ Base, Roms, BIOS string; IgnoredRetropie bool }`; `func Detect(mount string) (Layout, bool, error)` — `emulationstation/` first (BIOS folder `bios/`), else `retropie/` (BIOS folder `BIOS/`); `false` when none.
  - `func Prepare(mount string, systems []string) error` — creates `emulationstation/roms/<s>/`, `emulationstation/bios/`, `emulationstation/README.txt` (text in simple English, see spec section 2/4.3).
  - `type Group struct{ Hint string; Dir string; Rels []string; Bytes int64 }`; `type Skip struct{ Rel, Reason string }`; `func Scan(romsDir string, max int) ([]Group, []Skip, error)` — `filepath.WalkDir`, no symlinks (`skip: symlink`), junk list from spec section 2 (silent, not reported), unsafe names via `roms.SafeName` per segment (`skip: unsafe name`); one group per first-level folder (hint = folder name) plus one group with `Hint ""` for loose files; `ErrTooManyFiles` when the count is above `max`.
  - `func NeedBytes(groups []Group) (int64, error)` — file sizes plus uncompressed size of ZIP entries, plus 5 %; `var freeBytes = func(path string) (int64, error)` (statfs, replaceable in tests).
  - `type Line struct{ Rel, Status string }`; `func WriteReport(base string, now time.Time, lines []Line) error` — overwrites `<base>/import-report.txt`, header with date and counts per status.
  - `type Importer struct{ Priv USBPriv; Stager *roms.Stager; Config func() *config.Config; Systems func() []systems.System; Now func() time.Time }` with `type USBPriv interface{ USBMount(ctx context.Context, path string) (string, error); USBUmount(ctx context.Context) error }`.
  - `type Result struct{ Prepared bool; UploadIDs []string; Staged, Review, Skipped, BIOSSaved int }`; `func (im *Importer) Run(ctx context.Context, r jobs.Reporter, device string) (Result, error)` — the flow of spec section 4.3 steps 1–9 (BIOS via `bios.Save`, same name + size → `bios skipped: already imported`, other size → overwrite, `bios overwritten`). `USBUmount` runs on every exit path (deferred, with a fresh context). Uploads without any non-discarded file are discarded and not returned.
  - `ErrStickRemoved` with message "The USB stick was removed." when a read fails with `EIO`/`ENODEV`/`ENXIO` and `device` no longer exists (`os.Stat`); `ErrNoSpace` message "Not enough space on the SD card: need X, free Y." (sizes in MB, one decimal).

- [ ] **Step 1: Write the failing tests** (temp dirs as stick and home; fake `USBPriv` records calls; systems from a small `es_systems.cfg` fixture with `snes` `.smc .sfc .zip`, `nes` `.nes .zip`, `psx` `.cue .bin .chd .m3u`, `arcade` with KeepArchives):
  - `Detect`: emulationstation only, retropie only, both (`IgnoredRetropie`), none.
  - `Prepare` then `Detect` → found; folders per system exist; README exists.
  - `Scan`: deep folders keep their rel path; junk ignored; symlink skipped; loose files group; `max` exceeded → `ErrTooManyFiles`.
  - `StageTree`: hint match → one candidate; hint mismatch → note; `psx/Game/Game.cue` + `Game.bin` → companion; ZIP with `.smc` in `snes` → unpacked; `arcade` ZIP stays; already imported (same size) → discarded with reason, other size → not discarded; ctx cancel → error and no upload dir left.
  - Review Focus 2: `snes/Mario.zip` and `nes/Mario.zip` → two uploads; commit both → `roms/snes/Mario.zip`, `roms/nes/Mario.zip`.
  - Review Focus 3: run, commit, run again → `Result.UploadIDs` empty, every line `skipped: already imported`.
  - `Run` on an empty stick → `Prepared`, nothing staged, umount called.
  - BIOS skip/overwrite; report content (counts and lines); free space too small (injected `freeBytes`) → `ErrNoSpace`, nothing staged, umount called.
  - Review Focus 4: a reader that fails with `EIO` after the device path is removed → `ErrStickRemoved`, upload dir gone, umount called.
  - `priv`: parse of `usb-list` output (TABs, `-` label, bad lines ignored); `USBMount` passes exactly `sudo -n <helper> usb-mount <path>`.
  - `config`: default 20000, load/save, `0` rejected.
  - `jobs`: `SetLink` shows up in `Get` and in the persisted state.
- [ ] **Step 2:** `go test ./...` → FAIL (missing symbols).
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go vet ./... && go test -race -count=1 ./...`, gofmt clean, `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...`.
- [ ] **Step 5: Checkpoint.**

### Task 3: Web – page "USB", import job, review page for many files

**Files:**
- Create: `webui/internal/web/handlers_usb.go`, `webui/internal/web/templates/usb.html` (+ `usb_test.go`)
- Modify: `webui/internal/web/server.go` (`Deps.USB *usb.Importer`, routes, nav entry "USB" after "Upload"), `cmd/emulationstation-web/main.go` (wire the importer), `webui/internal/web/handlers_upload.go` (`maxReviewIDs` 50 → 200, grouped view data), `webui/internal/web/templates/upload_review.html`, `webui/internal/web/templates/job.html` (show `Job.Link` as a button "Review files"), `webui/internal/web/static/app.js` (enable a file's inputs when its "Edit" button is clicked), `webui/internal/web/static/app.css` (rebuild with Tailwind if new classes are used)

**Interfaces:**
- `GET /usb` — calls `Priv.USBList` (10 s timeout); renders label, size (GB, one decimal), file system, an **Import** button per device (POST `/usb/import`, field `device`), **Refresh** (GET `/usb`), **Eject** (POST `/usb/eject`). Empty state text from spec section 4.2. Helper error → page with "Could not read the USB devices." and the error in the service log.
- `POST /usb/import` — rejects a device that is not in a fresh `USBList` (400 "This USB stick is not available."). If a job of kind `usb-import` is queued or running → 409 "A USB import is already running.". Else `Jobs.Submit("usb-import", "Import from USB stick", …)` and redirect to `/jobs/{id}`. The job calls `Importer.Run`; on success it logs the counts and, when `UploadIDs` is not empty, sets the link `/upload/<ids joined by ,>`; when `Prepared` it logs "Stick prepared. Fill it on your PC, then import again."; when nothing is new it logs "Nothing new on the stick.".
- `POST /usb/eject` — `Priv.USBUmount`; toast "You can remove the USB stick now."; refuses (409) while a `usb-import` job runs.
- Review page (`GET /upload/{ids}`): summary line "N files ready, M need a decision, K skipped"; files with more than one candidate or with a `Note` first, open, inputs enabled; ready files (one candidate) grouped per system in a collapsed `<details>`, their inputs rendered `disabled` with an **Edit** button that enables them (small JS in `app.js`, no inline script — CSP stays); discarded files in one collapsed group with their reasons. Form field names do not change. The commit handler is unchanged (it already uses the first candidate and the suggested name when a field is missing).
- All texts simple English; CSRF/auth middleware as for all other POST routes.

- [ ] **Step 1: Write the failing tests** with a fake runner for the helper and a temp stick:
  - `/usb` lists two devices; empty state; helper error message.
  - `/usb/import` with an unknown device → 400; second import while one runs → 409; a valid import → redirect to `/jobs/<id>`, after the job `Job.Link` is `/upload/<id>,<id>`.
  - `/usb/eject` → helper called; 409 while a job runs.
  - Review page: grouping and summary counts; ready inputs have `disabled`; a file with a `Note` is in the open group.
  - Review Focus 5: 5000 ready files in one upload; POST with only 2 edited files → 5000 committed; request body < 1 MiB.
  - Review Focus 4 at the web level: job error text "The USB stick was removed." is shown on the job page.
  - Existing upload/review tests stay green (a single normal upload looks as before apart from the summary line).
- [ ] **Step 2:** `go test ./internal/web/...` → FAIL.
- [ ] **Step 3:** Implement; rebuild `app.css` with `$TAILWINDCSS` only if new classes are used.
- [ ] **Step 4:** Full Go verification as in Task 2 Step 4.
- [ ] **Step 5: Checkpoint.**

### Task 4: Image checks, docs, checklist

**Files:**
- Modify: `test/lib/static-app.sh` (or `test/rpi4-static.sh` / `test/vm-static.sh` where kernel checks live), `test/bats/static-app.bats`
- Modify: `README.md` (section "Import from a USB stick"), `docs/hardware-checklist.md`, `docs/superpowers/specs/2026-10-27-emulationstation-image-design.md` (one short section pointing to the USB spec, and `USB_MAX_FILES` in the web.conf key list)

**Interfaces:**
- Static check (app stage, chroot): for every release in `/lib/modules`: `modinfo -k <release> vfat exfat ntfs3` succeeds, or the build report has a line `usb <module> missing for kernel <release>` (then the check warns instead of failing for `ntfs3` only; `vfat` and `exfat` missing is a FAIL). `command -v lsblk jq findmnt mountpoint` in the image → found. The helper contains `usb-list`.
- README: stick layout (spec section 2), supported file systems, the first-import "prepare" step, re-import behaviour, "nothing is deleted from the stick", ROM legal note in one sentence ("Only use games you own; an image or stick with ROMs is for you only.").
- Checklist: spec section 7 hardware items.

- [ ] **Step 1: Write the failing tests:** static-app bats cases with a fake root: module missing (`exfat`) → FAIL; `ntfs3` missing with report line → WARN/pass; tools missing → FAIL.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement checks and write the docs.
- [ ] **Step 4:** `bats test/bats </dev/null`, `shellcheck -x`, `scripts/check-license-headers.sh`, Go tests → green.
- [ ] **Step 5: Checkpoint** — report to the user what needs the real build and hardware: `./build.sh rpi4` + `test/rpi4-static.sh --stage app`, `./build.sh vm-amd64` + `test/vm-static.sh`, the hardware checklist items.
