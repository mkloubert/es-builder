# EmuStation Builder – Wi-Fi on from the start (Raspberry Pi) – Design

License: AGPL-3.0-or-later, see LICENSE

As of: 2026-10-31 · Status: approved by the owner (chat, 2026-10-31)

This spec changes the Wi-Fi part of the main spec `2026-10-27-emulationstation-image-design.md`
and of `2026-10-31-bookworm-design.md`. Where they disagree, this spec wins.

## 1. Problem

The Raspberry Pi OS image blocks Wi-Fi in two ways. Our build handled neither:

1. `raspberrypi-sys-mods` ships `/etc/modprobe.d/rfkill_default.conf` with
   `options rfkill default_state=0`. Every radio starts soft-blocked, unless systemd-rfkill finds
   a saved state file `/var/lib/systemd/rfkill/<device>:wlan` with `0`.
   The image has no such file for wlan: the rpi4 build log says
   `wlan unblocked in 0 saved rfkill state file(s)`. pi-gen writes files only for Bluetooth.
2. pi-gen (branch `bookworm-arm64`, `stage2/02-net-tweaks/01-run.sh`) writes
   `/var/lib/NetworkManager/NetworkManager.state` with `WirelessEnabled=false` when the image is
   built without a country (true for the official images). NetworkManager then keeps the Wi-Fi
   radio off.

So a Wi-Fi network from `build.conf` may never connect, and the device can only be reached by
cable.

## 2. Goal and success criteria

1. On the Raspberry Pi, Wi-Fi is on at the first boot, with and without `WIFI_SSID` and
   `WIFI_COUNTRY`.
2. The build sets this (step 40, `app-15-network.sh`). The first boot checks it once more
   (`firstboot`), so an image whose base layer changes still gets Wi-Fi on.
3. If the owner turns Wi-Fi off later (`nmcli radio wifi off`), it stays off after a reboot.
4. The VM images are not changed.

## 3. Decisions

| Topic | Decision | Reason |
|---|---|---|
| rfkill | write `0` to every existing `/var/lib/systemd/rfkill/*:wlan` file, and create `platform-fe300000.mmcnr:wlan` (the Pi 4 on-board Wi-Fi, SDIO controller `fe300000.mmcnr`) with `0` | systemd-rfkill restores the saved state when the device appears; this overrides `default_state=0` for the on-board Wi-Fi |
| NetworkManager | `WirelessEnabled=true` in `[main]` of `NetworkManager.state` (the file and the section are created when missing, other lines stay) | NetworkManager then turns the radio on and unblocks wlan itself, also for USB Wi-Fi sticks |
| `rfkill_default.conf` | not changed | a vendor file; the two steps above are enough, and Bluetooth stays as pi-gen set it |
| Without `WIFI_COUNTRY` | Wi-Fi is on anyway | the kernel then uses the world domain `00`, which allows only safe channels; the owner asked for Wi-Fi on |
| First boot | once, marker `/var/lib/emulationstation/wifi-on`; files only, no `rfkill` or `nmcli` call | firstboot runs before NetworkManager (`Before=network-pre.target`), so the state file is read right after; the marker keeps a later "off" of the owner |
| Shared code | `nmwifi_radio_on <root>` in `overlay/usr/local/lib/emulationstation/nmwifi.sh` | build and first boot use the same code (as for the connection file) |

Ruling: turn Wi-Fi on without a country — the world regulatory domain is safe, and the default
`WIFI_COUNTRY=DE` covers the normal case — cost if wrong: weaker Wi-Fi (fewer channels) until
the owner sets a country.

Ruling: the first boot step runs only once — a second run at every boot would undo a "Wi-Fi off"
of the owner — cost if wrong: an image whose state files are broken after the first boot needs
`nmcli radio wifi on` by hand.

Ruling: name the Pi 4 state file `platform-fe300000.mmcnr:wlan` — the same name pi-gen used up to
bullseye and our tests already use — cost if wrong: only the NetworkManager step works, which
also unblocks the radio when NetworkManager starts.

## 4. Changes

### 4.1 `nmwifi.sh`

`nmwifi_radio_on <root>` (root = path prefix, empty for `/`):

1. `mkdir -p <root>/var/lib/systemd/rfkill`; write `0` to every `*:wlan` file there and to
   `platform-fe300000.mmcnr:wlan` (mode 0644).
2. `<root>/var/lib/NetworkManager/NetworkManager.state`: if the file is missing, write
   `[main]` + `WirelessEnabled=true`. If it has a `WirelessEnabled=` line, set it to `true`. If it
   has none, add the line right after `[main]`, or add `[main]` + the line at the end when there
   is no `[main]`. Temp file + rename, mode 0644.

### 4.2 Build (`provision/common/app-15-network.sh`)

On rpi4, after the country step: always call `nmwifi_radio_on "$R"` and log
`Wi-Fi radio on (rfkill state and NetworkManager.state)`. The old `unblock_wlan` function is
replaced by it.

### 4.3 First boot (`overlay/usr/local/lib/emulationstation/firstboot`)

New step `wifi_on_once` in `main`, after `grow_root`, before `process_file`. Only for
`TARGET_KIND=rpi4` and only when `$STATE_DIR/wifi-on` does not exist: `nmwifi_radio_on "$R"`,
then create the marker, log `Wi-Fi radio on (first boot)`. A failure counts as a failed step
(exit 1), but the other steps still run.

### 4.4 Tests and docs

- `test/bats/network.bats`: rpi4 without SSID and country → the state files are `0`, the Pi 4
  file is created, `NetworkManager.state` has `WirelessEnabled=true`; the cases of 4.1 (missing
  file, `false`, no line, no `[main]`, other lines stay); vm → nothing changes.
- `test/bats/firstboot.bats`: rpi4 → radio on and marker; second run with the marker → the files
  are not touched again; vm → nothing.
- `test/rpi4-static.sh`: `WirelessEnabled=true` and `platform-fe300000.mmcnr:wlan` = `0`.
- `docs/hardware-checklist.md`: Wi-Fi connects at the first boot; `rfkill list` shows no soft
  block; after `nmcli radio wifi off` and a reboot it stays off.
- README: Wi-Fi is on from the start on the Pi.
