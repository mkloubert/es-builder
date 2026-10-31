# EmuStation Builder – VM root grow check – Design

License: AGPL-3.0-or-later, see LICENSE

As of: 2026-10-31 · Status: approved by the owner (chat, 2026-10-31)

This spec changes the "grow root" step of `firstboot` (main spec
`2026-10-27-emulationstation-image-design.md`). Where they disagree, this spec wins.

## 1. Problem

`firstboot` grows the VM root partition once and then writes the marker
`/var/lib/emulationstation/grown`. It writes the marker also when nothing could grow. In UTM the
owner used the `.raw` file without making it bigger. The disk stayed at 3.4 GiB, the root file
system filled up to 100 %, and games failed with "No space left on device". After the owner made
the disk bigger, the marker stopped the next boot from growing the root.

## 2. Goal and success criteria

1. The marker is written only when the root partition fills the disk **and** the disk is big
   enough (at least 8 GiB).
2. Otherwise `firstboot` logs a warning with the disk size and what to do, and writes no marker.
   The next boot tries again. So after the owner makes the disk bigger, a reboot is enough.
3. A small disk is not a failed step (exit code stays 0). growpart and resize2fs errors stay
   failures, as before.
4. The Raspberry Pi is not changed.

## 3. Decisions

| Topic | Decision | Reason |
|---|---|---|
| "fills the disk" | after growpart: disk size − (partition start + partition size) < 32768 sectors (16 MiB), read from `$SYS_BLOCK/<part>/{start,size}` and `$SYS_BLOCK/<disk>/size` (512-byte sectors) | GPT keeps a backup header and growpart aligns the end; a few MiB stay free |
| "big enough" | disk size ≥ 8 GiB (16777216 sectors) | the image has 3.5 GiB of system; 8 GiB leaves room for ROMs, the README asks for 16 GiB |
| Sizes cannot be read | warning, no marker | a retry at the next boot does no harm (growpart says NOCHANGE) |
| Warning text | `WARNING: the disk is only <N> GiB (root <M> GiB). Make the VM disk bigger (16 GiB, see README); the root file system grows at the next boot.` | tells the owner what to do |

Ruling: no marker for a small disk — the check runs again at every boot until the disk is big
enough — cost if wrong: one growpart + resize2fs call per boot on a small disk (both are fast
no-ops then).

Ruling: 8 GiB as the limit, not 16 GiB — a disk made bigger by hand to 10 GiB is fine and should
stop the warnings — cost if wrong: an owner with a 8–16 GiB disk gets no warning.

## 4. Changes

- `overlay/usr/local/lib/emulationstation/firstboot`, `grow_root`: after resize2fs, read the
  sizes, then write the marker or log the warning (section 3).
- `test/bats/firstboot.bats`: the fake `$T/sys` gets `vda/size`, `vda2/start` and `vda2/size`
  (a 16 GiB disk filled by the partition). New cases: small disk → warning, no marker, exit 0,
  and a second run calls growpart again; space left after the partition → no marker; sizes
  missing → no marker; a big disk → marker.
- `docs/hardware-checklist.md` (VM): a `.raw` without resize gives the warning in
  `journalctl -u emulationstation-firstboot`; after making the disk bigger, a reboot grows root.
- README (VM images): a reboot is enough after making the disk bigger.
