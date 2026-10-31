# Wi-Fi on from the start (Raspberry Pi) – Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this
> plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wi-Fi is on at the first boot of the Pi image (rfkill state + NetworkManager.state).

**Spec:** `docs/superpowers/specs/2026-10-31-rpi-wifi-on-design.md`

## Global Constraints

- Git is read only. Simple English. No base-layer file changes (app layer and overlay only).
- No new ports or services. `rfkill_default.conf` stays unchanged.

## Tasks

- [ ] 1. `nmwifi_radio_on` in `overlay/usr/local/lib/emulationstation/nmwifi.sh` (spec 4.1).
- [ ] 2. `app-15-network.sh`: replace `unblock_wlan` by `nmwifi_radio_on` on rpi4, always (4.2).
- [ ] 3. `firstboot`: `wifi_on_once` with marker (4.3).
- [ ] 4. bats in `network.bats` and `firstboot.bats`; static check in `test/rpi4-static.sh`.
- [ ] 5. `docs/hardware-checklist.md`, `README.md`.
- [ ] 6. `./build.sh test`.

## Review Focus

1. Existing tests that expect "vm: rfkill untouched" must stay green.
2. `NetworkManager.state` edits keep all other lines.
3. firstboot with the marker must not touch the files again.
