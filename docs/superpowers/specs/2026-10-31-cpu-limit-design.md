# EmuStation Builder – CPU speed limit and heat status (Pi 4) – Design

License: AGPL-3.0-or-later, see LICENSE

As of: 2026-10-31 · Status: draft for approval

This spec adds to the main spec `2026-10-27-emulationstation-image-design.md` (web UI and root
helper parts). Where the two disagree, this spec wins.

## 1. Goal and success criteria

On a Raspberry Pi 4 the owner can see in the web UI if the Pi got too hot (or had too little
power), and can set a lower top CPU speed, so it gets less hot.

1. The dashboard shows, next to the temperature that is shown today, the throttle state of the
   firmware: "not throttled" or "throttled now" (heat or power).
2. The System page has a tab "CPU" with a choice of the top CPU speed: **Max**, **1200 MHz**,
   **1000 MHz**, **800 MHz**, **600 MHz**. A click sets it at once, without a restart.
3. The choice stays after a reboot.
4. The web UI can never set a speed above the board's own top speed (no overclocking), and can
   never touch `config.txt`.
5. On the VM images, and on a board where the needed files are missing, the tab and the throttle
   line are not shown. Nothing fails.

## 2. Decisions

| Topic | Decision | Reason |
|---|---|---|
| How the speed is set | write `scaling_max_freq` of every cpufreq policy in `/sys/devices/system/cpu/cpufreq/` | works at once; the firmware still throttles on its own when it is hot |
| `config.txt` (`arm_freq`) | not used | a wrong value can stop the boot, and each change needs a reboot |
| Values | fixed list: `max`, 1200, 1000, 800, 600 (MHz) | strict check in the helper; no free number from the network |
| Saved choice | `/var/lib/emulationstation-cpu/max-mhz` (root:root, folder 0755, file 0644) | `/etc` is read-only for the helper (the web service has `ProtectSystem=full`, and `priv` runs in its namespace); `/var/lib` is writable |
| Apply at boot | new oneshot unit `emulationstation-cpu.service` → `priv cpu-apply` | same check code as the web UI path |
| Read speed | the web service reads `/sys` itself (no root needed) | no helper call for the speed |
| Throttle state | new helper command `cpu-throttled` runs `vcgencmd get_throttled` as root and prints only the checked hex value | the sysfs file `get_throttled` is gone in the Raspberry Pi kernel 6.12 (bookworm image); `vcgencmd` needs `/dev/vcio`, which the service user has no access to |
| "Since boot" bits | not shown | the kernel hwmon driver of the Pi asks the firmware every few seconds and clears these bits, so they are not reliable |
| Where it works | only when `/proc/device-tree/model` starts with `Raspberry Pi 4` and `policy0/scaling_max_freq` exists | VM and other boards stay as they are |
| `build.conf` / `emulationstation.txt` key | not part of this spec | the owner asked for the web UI; can be a later spec |

Ruling: save the choice in `/var/lib`, not in `/etc` — `ProtectSystem=full` makes `/etc`
read-only for the helper, and a file in the service's own folder could be changed by the
service user — cost if wrong: one more small folder in the image.

Ruling: a fixed list of speeds, not every value of `scaling_available_frequencies` — fewer UI
choices, and the helper can check against a list that does not depend on the board — cost if
wrong: the owner cannot pick, for example, 1100 MHz.

Ruling (changed after review, owner's choice): read the throttle state with `vcgencmd` through the
helper, not from sysfs — the sysfs attribute was removed in the rpi-6.12 kernel — cost if wrong: one
more sudo call each time the dashboard status is loaded (a few milliseconds).

## 3. Changes

### 3.1 Root helper (`overlay/usr/local/lib/emulationstation/priv`)

New commands (usage text and header comment updated):

- `cpu-set <value>`: `<value>` must match exactly `max|1200|1000|800|600`, else exit 2. The
  board check (section 2, "Where it works") must pass, else exit 2 with "cpu limit is not
  supported on this device". For a number, `<value> * 1000` must be ≤ `cpuinfo_max_freq` of
  `policy0`, else exit 2. `max` uses `cpuinfo_max_freq`. The helper writes the kHz value to
  `scaling_max_freq` of each `policy*` folder (no symlinks followed: the policy folders are
  found by name with a fixed glob under `/sys/devices/system/cpu/cpufreq/`). Then it saves
  `<value>` to `max-mhz` (temp file + rename in the same folder, folder made with `install -d -m
  0755 -o root -g root`). Exit 1 if writing fails.
- `cpu-apply`: reads `max-mhz`. A missing file means `max` and changes nothing (exit 0). A file
  whose content is not in the list is ignored with a message on stderr (exit 0), so a broken file
  never stops the boot. Otherwise it does the same as `cpu-set` without saving again. On a board
  that is not supported it does nothing (exit 0).
- `cpu-throttled`: no argument. On a board that is not supported, exit 2. It runs
  `vcgencmd get_throttled` (fixed `PATH`) and prints only `0x<hex>` when the output is exactly
  `throttled=0x<1-8 hex digits>`; anything else, or a failing `vcgencmd`, gives exit 3.
- Test overrides (only when not root, like `PRIV_TEST_PATH`): `PRIV_TEST_SYS` (prefix for `/sys`
  and `/proc`), `PRIV_TEST_STATE` (folder instead of `/var/lib/emulationstation-cpu`).

Sudoers does not change; the helper is already allowed.

### 3.2 Boot unit (`overlay/etc/systemd/system/emulationstation-cpu.service`)

```
[Unit]
Description=EmuStation CPU speed limit
After=sysinit.target

[Service]
Type=oneshot
ExecStart=/usr/local/lib/emulationstation/priv cpu-apply

[Install]
WantedBy=multi-user.target
```

It is enabled only on `rpi4` (`provision/rpi4/app-*.sh`, new script or an existing one; app
layer, the base cache stays valid). It opens no port and runs once.

### 3.3 Web UI

- New package `internal/cpu` (or in `sysinfo`, same style): `Read(root string) State` with
  `Supported bool`, `MaxMHz int` (current `scaling_max_freq`), `HWMaxMHz int`
  (`cpuinfo_max_freq`), `Saved string` (`max-mhz`, "" = max). All paths go through `root`
  (tests use `testdata`). `ParseThrottled(raw string) Throttle` with `Known`, `Now`, `NowHeat`,
  `NowPower`.
- Bits used: 0 under-voltage now, 1 frequency capped now, 2 throttled now, 3 soft temperature
  limit now. "Heat" = bit 1, 2 or 3 without bit 0; "power" = bit 0.
- `priv` package: `CPUSet(ctx, value string) error` (runs `cpu-set`; the value is checked
  against the same list in Go before the call) and `CPUThrottled(ctx) (string, error)` (checks
  the output again: `^0x[0-9a-fA-F]{1,8}$`).
- The dashboard calls `CPUThrottled` only when `Supported`, with a timeout of 2 seconds. An
  error gives `Known=false` (no line) and one log line.
- Dashboard (`partials/status.html` or the system card that shows the temperature):
  - throttled now, heat: "Too hot: the CPU is slowed down now."
  - throttled now, power: "Power is too low: the CPU is slowed down now. Use a stronger power
    supply."
  - else: "Not throttled."
  - `Known == false`: no line.
- System page, new tab "CPU" (only when `Supported`):
  - text "Top CPU speed: <MaxMHz> MHz (board top speed: <HWMaxMHz> MHz)."
  - one form per choice (radio buttons and a "Save" button), the current one is marked; choices
    above `HWMaxMHz` are not shown.
  - `POST /system/cpu` with `value`; toast "CPU speed limit saved." or "The CPU speed could not
    be set."; bad value → 400 "Unknown CPU speed."; not supported → 409 "This device does not
    support a CPU speed limit.".
  - hint: "The Pi also slows down by itself when it gets too hot. A heat sink or a fan helps
    more than a lower speed."

## 4. Tests

- `test/bats/priv.bats` (stubs, fake `/sys` and `/proc` in a temp folder):
  `cpu-set 1200` writes 1200000 to each policy and saves `1200`; `cpu-set max` writes
  `cpuinfo_max_freq`; `cpu-set 1100`, `cpu-set 1200000`, `cpu-set ''`, `cpu-set '1200;x'` → exit 2,
  nothing written; a value above `cpuinfo_max_freq` → exit 2; a non-Pi-4 model → exit 2;
  a policy folder that is a symlink is not written; `cpu-apply` with no file, a good file, a
  broken file, and on a non-Pi board → exit 0 and the right writes; `cpu-throttled` with a stub
  `vcgencmd` → `0x50005`; junk, extra text or a failing stub → exit 3; non-Pi-4 → exit 2.
- `test/bats/static-app.bats` + `test/rpi4-static.sh`: the unit exists and is enabled on rpi4;
  `test/vm-static.sh`: the unit is not enabled.
- Go: `internal/cpu` table tests for every bit case, missing files, junk content; `priv.CPUSet`
  rejects bad values without a call; `priv.CPUThrottled` rejects junk output; the dashboard does
  not call the helper on a board that is not supported; web tests for the dashboard lines, the tab shown/hidden,
  `POST /system/cpu` (ok, bad value 400, not supported 409, helper error), choices above the
  board top speed hidden.
- `docs/hardware-checklist.md`: on a Pi 4, set 800 MHz and see `scaling_max_freq` = 800000 and
  "Top CPU speed: 800 MHz"; reboot and see it again; `sudo vcgencmd get_throttled` works and the
  dashboard shows "Not throttled."; with a weak power supply the power line shows; set Max again.

## 5. Docs

- `README.md`: a short section "CPU speed and heat (Pi 4)": the tab, the values, that the Pi
  slows down by itself above about 80 °C, that cooling helps more, and the saved file.
- Main spec: the helper command list gets `cpu-set` and `cpu-apply`.

## 6. Risks

- A very low speed (600 MHz) makes games slow. This is the owner's choice; "Max" is one click
  away. Nothing is lost.
- The firmware can still slow the CPU below the chosen speed when it is hot. This is wanted.
- If a later firmware changes the `vcgencmd` output or a kernel moves the cpufreq files, the
  line or the tab is hidden (no failure). The hardware checklist catches it.
