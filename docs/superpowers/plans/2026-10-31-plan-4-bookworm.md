# Move to Debian 12 "bookworm" – Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build all three targets on Debian 12 "bookworm". On the Pi, RetroPie modules come from
the signed RetroPie binaries.

**Architecture:** New image pins in `build.conf.example`. A binary-first install mode in
`base-30-retropie.sh` for `rpi4`. Bookworm first-boot handling in `provision/rpi4/base-10-system.sh`.
nftables files without `destroy`. Static checks and docs follow.

**Tech Stack:** Bash 5 (container), bats, shellcheck, nftables, systemd units.

**Spec:** `docs/superpowers/specs/2026-10-31-bookworm-design.md` (changes
`docs/superpowers/specs/2026-10-27-emulationstation-image-design.md`)

## Global Constraints

- Git is read only. No step runs `git add`/`commit`/…. The owner commits after each task.
- Simple English in code, comments, logs and docs. Keep the AGPL header in every changed file.
- Ports stay 11911 + 11944 + ping. Names use `emulationstation`.
- Bash: `set -euo pipefail`, clean under `shellcheck -x`, usage comment above each new function.
- Pins (verbatim from the spec, checksums fetched on 2026-10-31):
  - `RPI_IMAGE_URL=https://downloads.raspberrypi.com/raspios_oldstable_lite_arm64/images/raspios_oldstable_lite_arm64-2026-10-06/2026-10-06-raspios-bookworm-arm64-lite.img.xz`
  - `RPI_IMAGE_CHECKSUM=sha256:9e938c5a6981c3f498b3b564c527f3e1fd58ef20bbf703be0eea748384a6c1af`
  - `VM_IMAGE_URL_AMD64=https://cloud.debian.org/images/cloud/bookworm/20261026-2643/debian-12-nocloud-amd64-20261026-2643.raw`
  - `VM_IMAGE_CHECKSUM_AMD64=sha512:728546720db094ab840752214b83cf834e08b9848b5f4b2a9aa4bfbf740cf9b7ab95cc414172d8a729555bccd8c4995ab8a6223b0c19b769bd83fe3897570667`
  - `VM_IMAGE_URL_ARM64=https://cloud.debian.org/images/cloud/bookworm/20261026-2643/debian-12-nocloud-arm64-20261026-2643.raw`
  - `VM_IMAGE_CHECKSUM_ARM64=sha512:e564abc9f96f9e691cb212227bab576bae6fce3b2ad2c7918734ae056278cb0f6f8e47bbda4f30ba4a00d6ffc06db86c8f132889889b737c81c40fdb6f64954f`
- The builder container stays `debian:trixie`. Do not touch `builder/Dockerfile` or `builder/host/detect.sh`.
- `RETROPIE_SETUP_REF` stays `f5ebfba638179631d4619f2b52075dec33d4bca8`.

## Review Focus

1. A module whose binary download fails on the Pi must not leave a half install behind before the
   source fallback. Test: the fallback starts with no build tree and the report line is right
   (Task 2).
2. A `skyscraper` binary failure must use the fallback too, not stop the build (Task 2).
3. `runcommand` must never be called with `_binary_`, or the core install breaks (Task 2).
4. `systemctl stop nftables` must work when our table is already gone (second stop). The
   `table …; delete …` form covers this. Test: the drop-in line is exact (Task 4).
5. An image that still has the trixie `resize` cmdline (old cache, wrong pin) must fail the static
   check, not pass quietly (Task 3).

---

### Task 1: Image pins

**Files:**
- Modify: `build.conf.example:53-65`
- Modify: `test/bats/cache.bats:118-121`, `test/fixtures/sfdisk-rpi.json`, `test/fixtures/sfdisk-vm.json`, `test/fixtures/sfdisk-vm-arm64.json` (names only)

**Interfaces:** Produces the pinned values in Global Constraints. No code interfaces.

- [ ] **Step 1:** In `build.conf.example`, replace the three URL/checksum pairs with the pins
  above. Change the comments to "Raspberry Pi OS Lite arm64 (bookworm, oldstable), Release
  2026-10-06" and "Debian 12 (bookworm) nocloud, UEFI, format .raw, dated directory 20261026-2643".
  Change "state of research" to 2026-10-31.
- [ ] **Step 2:** In `cache.bats` and the three sfdisk fixtures, replace `raspios-trixie` with
  `raspios-bookworm` and `debian-13` with `debian-12`. Change nothing else.
- [ ] **Step 3:** Run `bats test/bats/cache.bats test/bats/config.bats test/bats/image.bats`.
  Expected: 0 `not ok`.

### Task 2: RetroPie binary mode on the Pi

**Files:**
- Modify: `provision/common/base-30-retropie.sh` (header comment, `install_module`, core loop, OS check text)
- Test: `test/bats/base.bats` (section "base-30-retropie.sh", stub `retropie_root`)

**Interfaces:**
- Consumes: `CFG_TARGET_KIND` (`rpi4|vm`), already passed by step 30.
- Produces: report line format `<m> 0 built from source, no binary` (read by `50-finalize.sh` as
  any other report line).

- [ ] **Step 1: Extend the stub.** In `retropie_root`, the fake `retropie_packages.sh` logs its
  mode: `echo "rp $m ${2:-_source_} present:[…]"`. New env `RP_FAIL_BIN` = modules whose
  `_binary_` call fails (exit 1, leaves `tmp/build/$m`). `RP_FAIL` keeps its meaning for the
  source call. Update the existing `grep -qx 'rp vice present:\[\]'` assertion to the new log form.
- [ ] **Step 2: Write the failing tests** (all with `CFG_TARGET_KIND=rpi4` unless named):
  - `"base-30-retropie.sh rpi4: modules use _binary_, runcommand uses the normal call"`:
    `grep -qx 'rp lr-fceumm _binary_ present:\[\]'`, `grep -qx 'rp runcommand _source_ …'`,
    `run ! grep -q 'rp runcommand _binary_' "$CALLS"`, report file empty.
  - `"base-30-retropie.sh rpi4: binary fails, source works -> report line, no build tree left"`:
    `RP_FAIL_BIN=vice` → status 0, report is exactly `vice 0 built from source, no binary`,
    `grep -qx 'rp vice _source_ present:\[\]'`.
  - `"base-30-retropie.sh rpi4: binary and source fail -> normal error line"`:
    `RP_FAIL_BIN=vice RP_FAIL=vice` → report is exactly
    `vice 1 installation failed, see log 30-provision-base`.
  - `"base-30-retropie.sh rpi4: skyscraper binary fails -> source fallback"`:
    `RP_FAIL_BIN=skyscraper` → status 0, report `skyscraper 0 built from source, no binary`.
  - `"base-30-retropie.sh rpi4: core module without binary -> abort"`:
    `RP_FAIL_BIN=emulationstation` → status ≠ 0, output contains
    `No RetroPie binary for emulationstation. Check the network and files.retropie.org.uk.`,
    `run ! grep -q 'rp emulationstation _source_' "$CALLS"`.
  - `"base-30-retropie.sh vm: only source calls"`: `CFG_TARGET_KIND=vm` →
    `run ! grep -q '_binary_' "$CALLS"`.
  `run_retropie` gets the kind from `${KIND:-rpi4}`.
- [ ] **Step 3:** Run `bats test/bats/base.bats -f base-30-retropie`. Expected: the new tests fail.
- [ ] **Step 4: Implement.**
  - `BINARY=0; [ "$kind" = rpi4 ] && BINARY=1` after the `kind=` line, with a comment that
    RetroPie has binaries only for Raspberry Pi OS 12 (bookworm-64/rpi4/kms).
  - `install_module <m> [mode]` : runs `rp "$m" $mode` (mode empty = `_source_`); cleanup of
    `tmp/build/$m` as now.
  - New `install_auto <m>` : binary mode off or `m = runcommand` → `install_module "$m"`.
    Otherwise `install_module "$m" _binary_`; on failure `install_module "$m"`; if that works,
    `report_fail "$m" 0 "built from source, no binary"` and return 0; else return its code.
  - Core loop: `runcommand` and VM → `install_module`. `retroarch`/`emulationstation` with
    `BINARY=1` → `install_module "$m" _binary_` only; on failure `report_fail` as now, then
    `die "No RetroPie binary for $m. Check the network and files.retropie.org.uk."`.
  - `skyscraper` and the modules list use `install_auto`.
  - Header comment: replace the trixie lines with the bookworm facts from spec 3.2, and the OS
    check text with `die "RetroPie rejects this OS – see spec 2026-10-31-bookworm-design.md"`.
- [ ] **Step 5:** Run `bats test/bats/base.bats test/bats/vm.bats` and
  `shellcheck -x provision/common/base-30-retropie.sh`. Expected: 0 `not ok`, no shellcheck output.

### Task 3: Pi first boot on bookworm

**Files:**
- Modify: `provision/rpi4/base-10-system.sh` (header comment, "Root growing" block, swap block)
- Modify: `test/rpi4-static.sh:175-176` (`checks_base`)
- Test: `test/bats/base.bats` (section "provision/rpi4/base-10-system.sh", `pi_root`)

**Interfaces:** none new.

- [ ] **Step 1: Change the fixture.** `pi_root` writes the bookworm cmdline
  `console=serial0,115200 console=tty1 root=PARTUUID=27b94958-02 rootfstype=ext4 fsck.repair=yes rootwait quiet init=/usr/lib/raspberrypi-sys-mods/firstboot`,
  creates `etc/init.d/resize2fs_once`, and creates no `etc/rpi` and no `etc/cloud` (bookworm has
  neither). Keep one extra test that has `etc/rpi/swap.conf` and `etc/cloud`, so the no-op paths
  stay covered.
- [ ] **Step 2: Update and add tests.**
  - Main test: replace `grep -q 'resize' …` with `grep -q 'init=/usr/lib/raspberrypi-sys-mods/firstboot'`,
    replace `systemctl enable rpi-resize.service` with
    `grep -qx 'systemctl disable dphys-swapfile.service' "$CALLS"` and
    `grep -qx 'systemctl enable resize2fs_once' "$CALLS"`; `run ! grep -q rpi-resize "$CALLS"`.
    The idempotency check stays.
  - Rename the abort test to `"base-10-system.sh: firstboot init missing in cmdline.txt -> abort (root would not grow)"`,
    with the old trixie cmdline (`… rootwait resize`) as the input. Expected: status ≠ 0.
  - `"base-10-system.sh: trixie files present (rpi-swap, cloud-init) -> still handled"`:
    with `etc/rpi/swap.conf` and `etc/cloud` → `Mechanism=none` drop-in and
    `cloud-init.disabled` exist.
- [ ] **Step 3:** Run `bats test/bats/base.bats -f base-10-system`. Expected: the changed tests fail.
- [ ] **Step 4: Implement.**
  - The check: `grep -qE '(^|[[:space:]])init=/usr/lib/raspberrypi-sys-mods/firstboot([[:space:]]|$)'`,
    else `die "cmdline.txt has no init=/usr/lib/raspberrypi-sys-mods/firstboot: the root partition would not grow at first boot"`.
  - Remove `systemctl enable rpi-resize.service`. If `$R/etc/init.d/resize2fs_once` exists,
    run `systemctl enable resize2fs_once`.
  - Swap block: add `systemctl disable dphys-swapfile.service` (stubbed `systemctl` never fails;
    in the chroot it is installed). Keep the `rpi-swap` and cloud-init code.
  - Header comment: describe the bookworm mechanism from spec 3.3 (pi-gen 850ee74b). Drop the
    trixie text.
- [ ] **Step 5:** In `test/rpi4-static.sh` `checks_base`, replace the two lines with:
  `check "cmdline.txt: firstboot init (root grows at first boot)" has_word boot/firmware/cmdline.txt init=/usr/lib/raspberrypi-sys-mods/firstboot`
  and `check "dphys-swapfile not enabled" test ! -e "$R/etc/systemd/system/multi-user.target.wants/dphys-swapfile.service"`.
  Add `check "Debian 12 bookworm" has_line etc/os-release VERSION_CODENAME=bookworm` at the start
  of `checks_base`, and the same line in `test/vm-static.sh` `checks_base`.
- [ ] **Step 6:** Run `bats test/bats/base.bats test/bats/static-app.bats` and
  `shellcheck -x provision/rpi4/base-10-system.sh test/rpi4-static.sh test/vm-static.sh`.
  Expected: 0 `not ok`, no shellcheck output.

### Task 4: Firewall and SSH logs on bookworm

**Files:**
- Modify: `overlay/etc/nftables.conf:17-28`, `overlay/etc/systemd/system/nftables.service.d/keep-f2b.conf`
- Modify: `overlay/etc/fail2ban/jail.d/emulationstation.conf:19-25` (comment only)
- Modify: `provision/common/app-10-security.sh:66-68`, `overlay/etc/systemd/system/ssh.socket.d/10-emulationstation.conf:17-18` (comments only)
- Test: `test/bats/app.bats:211-226`, `test/bats/fail2ban-filter.bats:61-75`

**Interfaces:** none new.

- [ ] **Step 1: Update tests.** In `app.bats`:
  - nftables test: `grep -qx 'table inet filter' "$n"` and `grep -qx 'delete table inet filter' "$n"`,
    and the first is on the line before the second (`grep -n` + compare line numbers);
    `run ! grep -q destroy <(grep -v '^#' "$n")`.
  - drop-in test: `grep -qxF 'ExecStop=/usr/sbin/nft "table inet filter; delete table inet filter"' "$d"`;
    `run ! grep -q destroy <(grep -v '^#' "$d")`.
  In `fail2ban-filter.bats`: rename the `sshd-session` test to say OpenSSH 9.8+ is a later case
  that stays covered; make the `sshd[4242]` test name say "OpenSSH 9.2 (bookworm)". The lines stay.
- [ ] **Step 2:** Run `bats test/bats/app.bats -f nftables`. Expected: 2 failures.
- [ ] **Step 3: Implement.** In `nftables.conf`, replace `destroy table inet filter` with the
  two lines `table inet filter` and `delete table inet filter`. Rewrite the comment: `destroy`
  needs Linux 6.3, the Debian 12 VM has 6.1; the first line makes the table exist, so the delete
  never fails; `inet f2b-table` is not touched. Change the drop-in `ExecStop` line and its comment
  the same way. Rewrite the OpenSSH comments in the jail, `app-10-security.sh` and the socket
  drop-in: bookworm has OpenSSH 9.2 (logs as `sshd`), `ssh.socket` exists but is off by default.
- [ ] **Step 4:** Run `bats test/bats/app.bats test/bats/fail2ban-filter.bats`. In the builder
  container the `nft -c -f` test runs too. Expected: 0 `not ok`.

### Task 5: Comments, controllers and docs

**Files:**
- Modify: `provision/common/base-10-packages.sh:23-24` (Debian 12 image name in the comment)
- Modify: the `controllers/templates/*.xml` comments and `controllers/templates/README.md`: "sdl2-compat on SDL3 in trixie" → "SDL 2.26.5 in bookworm"
- Modify: `README.md` (lines 6, 33, 379-381), `CLAUDE.md` (line 3, Gotchas line 161), `docs/superpowers/specs/2026-10-27-emulationstation-image-design.md` (lines 11-12, 30-31, 317, 320)
- Modify: `docs/hardware-checklist.md` (lines 67, 86, 143)

**Interfaces:** none.

- [ ] **Step 1:** Change the comments and texts. The content comes from spec 3.6–3.8:
  - README: bookworm in the intro; build time: "On the Pi most emulators come as RetroPie
    binaries, so the base layer is much faster. The VMs still build from source."; support end
    "Debian 12 gets LTS updates until 2028-06-30."; the trixie gotchas are replaced with the
    binary/report text from spec 3.8.
  - CLAUDE.md: line 3 "Debian 12 \"bookworm\""; the trixie gotcha replaced with the spec 3.8 text.
  - Main spec: 1 and 2 name bookworm and link the new spec; the risk in section 9 points to it.
  - Hardware checklist: Wi-Fi country item on bookworm; `sshd` (OpenSSH 9.2) log lines; new item
    "ES on bookworm (SDL 2.26.5) writes the expected names and GUIDs for the template controllers".
- [ ] **Step 2:** Run `grep -rniE 'trixie|debian.?13' --exclude-dir=.git --exclude-dir=out . | grep -vE 'builder/Dockerfile|builder/host/detect.sh|test/bats/detect.bats|test/bats/finalize.bats|docs/superpowers/(plans|specs)/2026-10-2'`.
  Expected: only lines that talk about the builder container or about the old decision on purpose.
- [ ] **Step 3:** Run `scripts/check-license-headers.sh`. Expected: no output.

### Task 6: Full check

- [ ] **Step 1:** Run `./build.sh test`. Expected: shellcheck, license, bats (0 `not ok`),
  `go vet`, `go test -race` all green.
- [ ] **Step 2:** Report what was **not** run: a real image build (`./build.sh all`), the static
  checks against real images, `test/vm-smoke.sh`, and the hardware checklist. The owner runs
  `./build.sh rpi4` and checks that `out/build-report.txt` has few or no "built from source" lines.
