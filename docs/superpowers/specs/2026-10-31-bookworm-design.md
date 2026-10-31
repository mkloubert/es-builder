# EmuStation Builder – Move all targets to Debian 12 "bookworm" – Design

License: AGPL-3.0-or-later, see LICENSE

As of: 2026-10-31 · Status: draft for approval

This spec changes the main spec `2026-10-27-emulationstation-image-design.md`. Where the two
disagree, this spec wins. All other parts of the main spec stay as they are.

## 1. Goal and success criteria

All three targets (`rpi4`, `vm-amd64`, `vm-arm64`) are built on Debian 12 "bookworm" instead of
Debian 13 "trixie". RetroPie, EpicNoir, the web UI, the ports, the users and the security rules
stay the same.

Why: RetroPie does not support trixie, and on trixie every module is built from source. RetroPie
ships signed, prebuilt binaries for Raspberry Pi OS 12 on the Pi 4 (64 bit, KMS). The owner also
looked at Batocera and decided against it: it has no nftables tool, no fail2ban, runs everything
as root, has no generic arm64 image, and its upgrade would undo our changes.

Success criteria:

1. All success criteria of the main spec (section 1) still hold on bookworm.
2. On `rpi4`, every module in `provision/rpi4/modules.list`, plus the core modules and
   `skyscraper`, is installed from a RetroPie binary. A module that falls back to a source build
   gets a line in `out/build-report.txt`.
3. On a fresh build with a cold cache, the `rpi4` base layer takes much less time than before,
   because it no longer compiles the emulators. Target: under 1 hour on an arm64 host.
4. `./build.sh test` is green.

## 2. Decisions

| Topic | Decision | Reason |
|---|---|---|
| Pi base | Raspberry Pi OS Lite **bookworm** arm64 ("oldstable"), release 2026-10-06, pinned | RetroPie binaries exist for bookworm-64/rpi4/kms |
| VM base | Debian **12** cloud image `nocloud`, raw, UEFI (amd64/arm64), 20261026-2643, pinned | same Debian release on all targets |
| RetroPie install mode, Pi | `_binary_` first, `_source_` as a fallback | fast build; fallback keeps the build working |
| RetroPie install mode, VM | `_source_` (as now) | RetroPie has no binaries for x86 or generic aarch64 |
| Builder container | stays `debian:trixie` | it is only a tool on the host, not part of the image |
| Support time | Debian 12 LTS until 2028-06-30; Raspberry Pi OS bookworm "Legacy" gets updates | documented in the README |

Pinned images:

- `https://downloads.raspberrypi.com/raspios_oldstable_lite_arm64/images/raspios_oldstable_lite_arm64-2026-10-06/2026-10-06-raspios-bookworm-arm64-lite.img.xz`
  (checksum from the `.sha256` file next to it; pi-gen 850ee74b, kernel 6.12.109, OpenSSH 9.2p1,
  nftables 1.0.6, systemd 252, NetworkManager 1.42)
- `https://cloud.debian.org/images/cloud/bookworm/20261026-2643/debian-12-nocloud-amd64-20261026-2643.raw`
- `https://cloud.debian.org/images/cloud/bookworm/20261026-2643/debian-12-nocloud-arm64-20261026-2643.raw`
  (checksums from `SHA512SUMS` in the same folder; kernel 6.1)

## 3. Changes

### 3.1 Configuration

- `build.conf.example`: the three image URLs and checksums above.
- The cache key already covers the image checksum (`builder/lib/cache.sh: cache_key`), so the
  first build after the change rebuilds the base layer. Nothing else is needed.

### 3.2 Base layer: RetroPie binaries on the Pi (`base-30-retropie.sh`)

- Binary mode is on when `CFG_TARGET_KIND=rpi4` (already passed by step 30). There is no new
  setting.
- In binary mode, `install_module <m>` runs `retropie_packages.sh <m> _binary_`.
  If that fails, it cleans up as now and runs `retropie_packages.sh <m>` (`_source_`). If the
  source build works, it writes the report line `<m> 0 built from source, no binary`. If both fail,
  it writes the normal error line.
- `runcommand` is a script module without a binary. It always uses the normal call.
- The core modules `retroarch` and `emulationstation` must come from a binary on `rpi4`. They get
  no source fallback: if `_binary_` fails, the build stops with the message "No RetroPie binary
  for <m>. Check the network and files.retropie.org.uk." If RetroPie does not see this OS as one
  with binaries (`__has_binaries=0`, for example a 32-bit or non-KMS path), `install_bin` has no URL
  and fails, so this rule also catches a wrong platform.

Ruling: detect "no binaries" through the core modules, not through a helper that sources
RetroPie's internals — less code and no tie to RetroPie's variable names — cost if wrong: a short
network error stops the build, and the owner must start it again.
- RetroPie checks every binary with GPG (`downloadAndVerify`). We keep that and do not turn it off.
- The OS check text "RetroPie rejects trixie" becomes "RetroPie rejects this OS".

Ruling: `_binary_` with our own fallback, not `_auto_` — `_auto_` needs `ip route get`, which is
not reliable under qemu-user in the chroot — cost if wrong: a few modules are built from source
although a binary exists; the report shows it.

Ruling: no binaries on the VMs — RetroPie has none for x86 or generic aarch64 — cost if wrong:
none, the VM keeps its short module list.

### 3.3 Raspberry Pi first boot (`provision/rpi4/base-10-system.sh`)

Bookworm (pi-gen 850ee74b) works differently from trixie:

- `cmdline.txt` contains `init=/usr/lib/raspberrypi-sys-mods/firstboot`. This script grows the root
  partition at the first boot and then removes itself from `cmdline.txt`. The init script
  `resize2fs_once` grows the file system. There is no `resize` word, no `rpi-resize.service`, no
  cloud-init and no `rpi-swap`.
- The check "cmdline.txt has `resize`" becomes "cmdline.txt has
  `init=/usr/lib/raspberrypi-sys-mods/firstboot`". `systemctl enable rpi-resize.service` is
  removed. If `/etc/init.d/resize2fs_once` exists, it stays enabled.
- Swap: `dphys-swapfile` is installed in bookworm. It is disabled (`systemctl disable
  dphys-swapfile.service`), because swap comes from zram-tools (main spec 5.1). The `rpi-swap`
  drop-in code stays, because it does nothing when `/etc/rpi/swap.conf` is missing.
- The cloud-init code stays, because it does nothing when `/etc/cloud` is missing.
- User rename: pi-gen still runs `rename-user -f -s`, so `userconfig.service` is active. The
  current handling in `base-20-user.sh` stays.
- `/etc/machine-id`: pi-gen leaves it empty. Our finalize step writes `uninitialized`. systemd 252
  treats both as "first boot", so this stays.

Ruling: keep the trixie-only code paths where they are harmless no-ops — fewer branches, and a
later move back to trixie is easier — cost if wrong: a few dead lines.

### 3.4 Firewall (`overlay/etc/nftables.conf`, `nftables.service.d/keep-f2b.conf`)

`destroy table` needs Linux 6.3 or newer. The Debian 12 VM has Linux 6.1. So:

- `nftables.conf` starts with `table inet filter` followed by `delete table inet filter`, then the
  full table. The first line makes sure the table exists, so the delete never fails.
- The `ExecStop` of `keep-f2b.conf` is
  `ExecStop=/usr/sbin/nft "table inet filter; delete table inet filter"` (systemd keeps the quoted
  text as one argument).
- The fail2ban table `inet f2b-table` is never touched, as now.

### 3.5 SSH and fail2ban

- OpenSSH 9.2 has no `sshd-session`. Login lines come from `sshd`. The jail keeps its wide
  `journalmatch`, which also matches `sshd`. The comments are changed.
- `test/bats/fail2ban-filter.bats` checks an `sshd` line from OpenSSH 9.2 (fixture).
- The `ssh.socket` drop-in and `systemctl disable ssh.socket` stay as a safety net. Bookworm has
  `ssh.socket`, but it is off by default.

### 3.6 Packages (`base-10-packages.sh`)

The package list (`git dialog unzip xmlstarlet ca-certificates curl openssh-server nftables
fail2ban python3-systemd bluez zram-tools dkms network-manager rfkill`) exists in bookworm under
the same names. The Pi image ships `linux-headers-rpi-v8` and `linux-headers-rpi-2712` already.
Only the comments that name the Debian 13 image change.

### 3.7 Controllers

Bookworm has SDL 2.26.5. It still writes the CRC into the GUID. The templates stay. The comments
that name "sdl2-compat on SDL3 in trixie" change. The hardware checklist gets an entry: ES on
bookworm writes the expected SDL names and GUIDs for the template controllers.

### 3.8 Docs

- `README.md`, `CLAUDE.md` and the main spec: replace "Debian 13 trixie" with "Debian 12 bookworm",
  update the build time text, add the support end date (2028-06-30), and replace the trixie
  gotcha with: "On the Pi most modules come as RetroPie binaries; a module without a binary is
  built from source and listed in `out/build-report.txt`."
- Main spec section 9: the risk "RetroPie on trixie" is replaced by a link to this spec.
- `docs/hardware-checklist.md`: replace trixie items with bookworm items (Wi-Fi country at
  first boot, `sshd` log lines, SDL names).

## 4. Tests

- `test/bats/base.bats`, `base-30-retropie.sh` with a stub `retropie_packages.sh`:
  binary works → no report line; binary fails, source works → line "built from source";
  both fail → error line; `_binary_` of a core module fails on `rpi4` → the build stops;
  `runcommand` never gets `_binary_`; VM → only `_source_`.
- `test/bats/base.bats`, `rpi4/base-10-system.sh`: the `cmdline.txt` fixture changes to the
  bookworm form with `init=…/firstboot` → ok; without it → fail; `dphys-swapfile` is disabled.
- nftables: the existing `nft -c -f` test in `test/bats/fail2ban-filter.bats` stays. The builder
  container runs trixie (nft 1.1), so it cannot show problems with Linux 6.1. A new test therefore
  checks that `nftables.conf` and `keep-f2b.conf` contain no `destroy`.
- Fixtures: rename the trixie names in `test/fixtures/sfdisk-*.json` and `test/bats/cache.bats`.
- `test/rpi4-static.sh` and `test/vm-static.sh` (`checks_base`): check `/etc/os-release` has
  `VERSION_CODENAME=bookworm`.
  `test/rpi4-static.sh`: check `cmdline.txt` has `init=/usr/lib/raspberrypi-sys-mods/firstboot`
  and `dphys-swapfile` is not enabled.
- Not possible in CI: a real image build, the VM smoke test with the new images, and the hardware
  checklist. These are run by the owner.

## 5. Risks

- **Binaries can change** on files.retropie.org.uk (they were rebuilt on 2026-10-31). They are
  signed, but not pinned by checksum. A new binary can behave differently from the last build.
  Accepted: the base layer is cached, so a built image does not change.
- **RetroPie and bookworm:** a RetroPie moderator wrote that bookworm is "not fully supported".
  RetroPie still builds and signs binaries for it, which is the best support it gives today.
- **Support end:** Debian 12 LTS ends on 2028-06-30. The move to Debian 13 or 14 is a new spec,
  when RetroPie ships binaries for it.
- **VM kernel 6.1** is older than the Pi kernel 6.12. xpadneo builds with DKMS against both. A
  failure is listed in the build report (main spec 9).
