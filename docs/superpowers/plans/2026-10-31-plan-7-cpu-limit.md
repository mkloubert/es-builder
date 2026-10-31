# CPU speed limit and heat status (Pi 4) – Implementation Plan

License: AGPL-3.0-or-later, see LICENSE

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On a Pi 4 the web UI shows the firmware throttle state and lets the owner pick a top
CPU speed (Max/1200/1000/800/600 MHz) that is applied at once and again at every boot.

**Architecture:** The root helper `priv` gets `cpu-set <value>` and `cpu-apply` (strict list,
writes `scaling_max_freq`, saves to `/var/lib/emulationstation-cpu/max-mhz`). A oneshot unit,
enabled only on rpi4, runs `cpu-apply` at boot. The web service reads `/sys` and `/proc` itself
through a new package `internal/cpu`.

**Tech Stack:** Bash 5 (helper), systemd, Go 1.25 stdlib, bats, html/template + htmx.

**Spec:** `docs/superpowers/specs/2026-10-31-cpu-limit-design.md`

## Global Constraints

- Simple English everywhere; AGPL header on every own source file; Git read only (owner commits).
- Only ports 11911/11944 + ping; the new unit opens nothing. Sudoers does not change.
- Allowed values (exact): `max`, `1200`, `1000`, `800`, `600`. Nothing else, in Bash and in Go.
- Supported = `/proc/device-tree/model` starts with `Raspberry Pi 4` **and**
  `/sys/devices/system/cpu/cpufreq/policy0/scaling_max_freq` exists.
- Saved file `/var/lib/emulationstation-cpu/max-mhz`, folder 0755 root:root, file 0644.
- `config.txt` is never read or written.
- Exact texts: "Too hot: the CPU is slowed down now.", "Power is too low: the CPU is slowed down
  now. Use a stronger power supply.", "The CPU was slowed down since the last start (heat or
  power).", "Not throttled.", "Top CPU speed: <n> MHz (board top speed: <m> MHz).",
  "CPU speed limit saved.", "The CPU speed could not be set.", "Unknown CPU speed.",
  "This device does not support a CPU speed limit.", the hint "The Pi also slows down by itself
  when it gets too hot. A heat sink or a fan helps more than a lower speed.", helper message
  "cpu limit is not supported on this device".
- Base files stay untouched (the new rpi4 script is `app-*`).

## Review Focus

1. `/proc/device-tree/model` ends with a NUL byte on real hardware — the prefix check must work
   with it (Bash and Go). → Task 1 and Task 2 fixtures end with `\0`.
2. A `policy*` entry that is a symlink, or a `scaling_max_freq` that is a symlink, must not be
   written. → Task 1 test.
3. A broken saved file must never stop the boot (`cpu-apply` exit 0). → Task 1 test.
4. A value above the board's top speed (e.g. 1200 on a board whose `cpuinfo_max_freq` is 1000000)
   is refused in the helper and hidden in the UI. → Task 1 and Task 4 tests.
5. `get_throttled` with junk, empty, or `0x` prefix forms (`0x50005`, `50005`) → parsed or
   `Known=false`, never a panic. → Task 2 table rows.

---

### Task 1: Root helper `cpu-set` / `cpu-apply`

**Files:** Modify `overlay/usr/local/lib/emulationstation/priv`; Test `test/bats/priv.bats`

**Interfaces:** Produces the commands `priv cpu-set <max|1200|1000|800|600>` (exit 0 ok, 1 write
failed, 2 bad value / not supported / above board top) and `priv cpu-apply` (always exit 0 unless
a write fails → 1). Test overrides, only when not root: `PRIV_TEST_SYS` (prefix for `/sys` and
`/proc`), `PRIV_TEST_STATE` (instead of `/var/lib/emulationstation-cpu`).

- [ ] Step 1: bats helper `cpu_fake <model> <hwmax-khz>` builds `$S/proc/device-tree/model`
  (with trailing NUL) and `$S/sys/devices/system/cpu/cpufreq/policy0/{cpuinfo_max_freq,scaling_max_freq}`.
  Tests: `cpu-set 1200` → policy0 `scaling_max_freq` = `1200000`, state file = `1200`, mode 0644;
  `cpu-set max` → `1500000` and file `max`; with two policies both written; each of `1100`,
  `1200000`, `''`, `'1200;x'`, `MAX` → exit 2 and nothing changed; `cpu-set 1200` with hwmax
  1000000 → exit 2; model `Raspberry Pi 5` → exit 2 and the message; `policy1` as symlink to a
  folder outside → that target is not written; `cpu-apply`: no file → exit 0, nothing written;
  file `800` → `800000`; file `junk` → exit 0, message on stderr, nothing written; non-Pi-4 →
  exit 0, nothing written; usage text lists the new commands.
- [ ] Step 2: run `bats -f cpu test/bats/priv.bats` → all FAIL (unknown command).
- [ ] Step 3: implement in the helper: `cpu_supported`, `cpu_khz <value>` (case on the fixed
  list), `cpu_write <khz>` (loop over `$SYS/sys/devices/system/cpu/cpufreq/policy[0-9]*`, skip
  `-L` entries and `-L` files), save with `install -d -m 0755` + temp file + `mv`. Header comment
  and `usage` updated.
- [ ] Step 4: `bats test/bats/priv.bats` → 0 not ok; `shellcheck -x` clean.

### Task 2: Go reader `internal/cpu`

**Files:** Create `webui/internal/cpu/cpu.go`, `webui/internal/cpu/cpu_test.go`

**Interfaces:** Produces
`type Throttle struct{ Known, Now, NowHeat, NowPower, SinceBoot bool }`,
`type State struct{ Supported bool; MaxMHz, HWMaxMHz int; Saved string; Throttle Throttle }`,
`func Read(root string) State` (root "" = "/"; reads `proc/device-tree/model`,
`sys/devices/system/cpu/cpufreq/policy0/{scaling_max_freq,cpuinfo_max_freq}`,
`sys/devices/platform/soc/soc:firmware/get_throttled`, `var/lib/emulationstation-cpu/max-mhz`),
`var Values = []string{"max", "1200", "1000", "800", "600"}`,
`func ValidValue(v string) bool`, `func (s State) Choices() []string` (values whose MHz ≤
HWMaxMHz; `max` always).

- [ ] Step 1: table tests: supported Pi 4 (model with NUL); Pi 5 → not supported; missing policy0
  → not supported; throttle `0x0` → Known, nothing; `0x50005` → Now, NowPower, SinceBoot;
  `0x20002` → NowHeat; `0x80000` → SinceBoot only; `50005` (no prefix) parsed; `junk`/empty/missing
  → Known=false; saved file `1000` → Saved "1000", `junk` → ""; `Choices` with hwmax 1000 →
  `max,1000,800,600`; `ValidValue` table.
- [ ] Step 2: `go test ./internal/cpu/` → FAIL (no package).
- [ ] Step 3: implement (`strconv.ParseUint(strings.TrimPrefix(v, "0x"), 16, 32)`; kHz/1000).
- [ ] Step 4: tests pass with `-race`; `gofmt -l` empty.

### Task 3: `priv.CPUSet` and the dashboard line

**Files:** Modify `webui/internal/priv/priv.go`, `webui/internal/web/handlers_dashboard.go`,
`webui/internal/web/templates/partials/status.html`; Test `webui/internal/priv/priv_test.go`,
`webui/internal/web/cpu_test.go` (new)

**Interfaces:** Consumes `cpu.Read`, `cpu.ValidValue`. Produces
`func (p Priv) CPUSet(ctx context.Context, value string) error` (returns `ErrBadValue`-style
error without running anything when `!cpu.ValidValue`). The dashboard view gets `CPU cpu.State`
read with `cpu.Read(s.d.Sysinfo.Root)`.

- [ ] Step 1: tests: `CPUSet("1100")` → error, runner not called; `CPUSet("800")` → runner sees
  `sudo -n <priv> cpu-set 800`. Web: fake root with each throttle case → the exact dashboard
  text; `Known=false` → none of the four texts.
- [ ] Step 2: run → FAIL.
- [ ] Step 3: implement; the line sits under "CPU temperature" in `status.html`.
- [ ] Step 4: `go test -race ./internal/priv/ ./internal/web/` pass.

### Task 4: System tab "CPU" and `POST /system/cpu`

**Files:** Modify `webui/internal/web/handlers_system.go`, `templates/system.html`,
`templates/partials/system_shared.html` (new `define "cpu"`); Test `webui/internal/web/cpu_test.go`

**Interfaces:** Consumes `cpu.Read`, `State.Choices`, `priv.CPUSet`. Produces route
`POST /system/cpu` (form `value`) and partial `cpu` (data `cpu.State`).

- [ ] Step 1: tests: tab and texts shown when supported, hidden when not; choices above board top
  not in the HTML; current choice `checked`; POST `value=800` → runner call, toast "CPU speed
  limit saved.", card refreshed; `value=1100` → 400 "Unknown CPU speed.", no call; not supported
  → 409 text, no call; runner error → toast "The CPU speed could not be set."; cross-site POST →
  403.
- [ ] Step 2: run → FAIL.
- [ ] Step 3: implement (form `hx-post`, `hx-target="#cpu"`, `hx-swap="outerHTML"`, like
  `custom_ui`).
- [ ] Step 4: `go vet ./... && go test -race -count=1 ./...`; `gofmt -l .` empty.

### Task 5: Boot unit, rpi4 app script, checks, docs

**Files:** Create `overlay/etc/systemd/system/emulationstation-cpu.service`,
`provision/rpi4/app-10-cpu.sh`; Modify `test/rpi4-static.sh`, `test/vm-static.sh`,
`test/bats/static-app.bats` (or `app.bats`), `README.md`, `docs/hardware-checklist.md`,
`docs/superpowers/specs/2026-10-27-emulationstation-image-design.md` (helper command list)

**Interfaces:** Consumes `priv cpu-apply`.

- [ ] Step 1: bats: `app-10-cpu.sh` with a `systemctl` stub → `enable emulationstation-cpu.service`,
  unit file must exist (else die), second run the same; the unit file has `Type=oneshot`,
  `ExecStart=/usr/local/lib/emulationstation/priv cpu-apply`, no `Listen`/`Socket`;
  rpi4-static: check "emulationstation-cpu.service enabled" passes/fails; vm-static: check
  "emulationstation-cpu.service not enabled".
- [ ] Step 2: run → FAIL.
- [ ] Step 3: implement (unit text from spec 3.2; script with `PROVISION_ROOT` override like the
  others). Docs: README section "CPU speed and heat (Pi 4)", hardware checklist items from spec 4,
  main spec helper list.
- [ ] Step 4: full run: `bats test/bats` 0 not ok; `(cd webui && go vet ./... && go test -race
  -count=1 ./...)`; `gofmt -l webui` empty; license check empty; shellcheck clean. Not run here:
  `./build.sh test`, image build, hardware.

### Task 6 (after review): throttle state through `vcgencmd`

The final review found that `get_throttled` is gone from sysfs in the rpi-6.12 kernel. The owner
chose: a helper command that runs `vcgencmd`, no "since boot" line (spec updated: 2, 3.1, 3.3).

**Files:** `priv`, `test/bats/priv.bats`, `internal/cpu`, `internal/priv`, `handlers_dashboard.go`,
`partials/status.html`, the web tests, README, hardware checklist.

**Interfaces:** `priv cpu-throttled` (exit 0 + `0x<hex>`, 2 not supported, 3 bad output);
`func ParseThrottled(raw string) Throttle` (no `SinceBoot`); `func (p Priv) CPUThrottled(ctx) (string, error)`.

- [ ] Step 1: tests first (bats with a `vcgencmd` stub; Go: `ParseThrottled` table incl. `50005`
  and `0`, `CPUThrottled` junk rejected, dashboard lines from the fake runner output, no helper
  call when not supported, the "since boot" text never shown).
- [ ] Step 2: run → FAIL. Step 3: implement. Step 4: full suite green.
