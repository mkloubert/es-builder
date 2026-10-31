# EmulationStation autostart switch – Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ES_AUTOSTART=yes|no` in `build.conf` (default `no`) decides if ES starts after boot.
The web UI works without a running session.

**Architecture:** A new config key is checked in `config_load` and reaches `app-20-es.sh`
through `build.env`. That script writes one of two `autostart.sh` forms. `es-session` writes and
removes `session.pid`. The Go `es.Controller` uses it to tell "no session" (`off`) from a running
session.

**Tech Stack:** Bash (bats), Go 1.26 (`internal/es`, `internal/web`, html/template).

**Spec:** `docs/superpowers/specs/2026-10-31-es-autostart-design.md`

## Global Constraints

- Git is read only. No commits; the owner commits.
- Simple English. AGPL header on every new file (copy it from a `.bats` or `.go` file).
- No new ports, no new root helper command, no change to sudoers or to base-layer files.
- Exact texts from the spec:
  - config error: `ES_AUTOSTART must be yes or no (found: <value>)`
  - `autostart.sh` for `yes`: `exec /usr/local/lib/emulationstation/es-session`
  - `autostart.sh` for `no`: the three lines of spec 3.2
  - web error: `EmulationStation is not running on the device. Start it on the device screen.`
  - status `off`: title `EmulationStation is off`, hint `Autostart is off. Start it on the device screen.`
- Go: gofmt, table tests, temp dirs, no real system calls except `kill(pid, 0)` on own processes.

## Review Focus

1. Existing Go tests assume a running session. Without a `session.pid` they would all see `off`.
   The test helpers must write one with a live PID (Task 4, Task 5).
2. `es-session` ends through `MAX_ITER` with `clean_exit=1`, and the EXIT trap returns early
   then. The cleanup must still run (Task 3).
3. A second `es-session` (started by hand while one runs) must not delete the first one's
   `session.pid` (Task 3).
4. `WithESStopped` without a session must not leave a `hold` file behind. Otherwise a session
   started later by hand would come up "stopped" (Task 4).
5. A quoted value `ES_AUTOSTART="yes"` must be accepted (config strips one pair of quotes) (Task 1).

---

### Task 1: Config key

**Files:** Modify `builder/lib/config.sh`, `build.conf.example`, `test/fixtures/build.conf.smoke`.
Test: `test/bats/config.bats`.

**Interfaces:** Produces `CFG_ES_AUTOSTART` (`yes|no`), default `no`.

- [ ] **Step 1: Tests** in `config.bats` (temp config files as the other tests there):
  - `"config_load: ES_AUTOSTART defaults to no"` → `[ "$CFG_ES_AUTOSTART" = no ]`.
  - `"config_load: ES_AUTOSTART yes, no and \"yes\" are accepted"`.
  - `"config_load: ES_AUTOSTART Yes, 1 or empty -> error with the value"`: `run config_load …`
    → status 1, output contains `ES_AUTOSTART must be yes or no (found: Yes)`.
- [ ] **Step 2:** Run `bats test/bats/config.bats`. Expected: the new tests fail.
- [ ] **Step 3:** Add `ES_AUTOSTART` to `CONFIG_KEYS`, `printf -v CFG_ES_AUTOSTART '%s' no` with
  the other defaults. After the read loop: `case "$CFG_ES_AUTOSTART" in yes | no) ;; *) log_error
  "ES_AUTOSTART must be yes or no (found: $CFG_ES_AUTOSTART)"; return 1 ;; esac`. An empty value
  in the file counts as invalid (the default applies only when the key is missing).
  `build.conf.example`: a block near the web settings:
  `# Start EmulationStation by itself after boot: yes or no (default: no).` /
  `# With no, tty1 shows a shell and a hint how to start it.` / `ES_AUTOSTART=no`.
  Smoke fixture: `ES_AUTOSTART=yes`.
- [ ] **Step 4:** Run `bats test/bats/config.bats test/bats/app.bats`. Expected: 0 `not ok`.
  Check that `40-provision-app.sh` puts `CFG_ES_AUTOSTART` into `build.env`. It writes all keys
  except the pins (line 45 ff.), so no change is expected there; if a test lists the keys, add it.

### Task 2: `autostart.sh` in the image

**Files:** Modify `provision/common/app-20-es.sh`, `test/lib/static-app.sh` (`checks_units`).
Create `test/bats/es-autostart.bats`.

**Interfaces:** Consumes `CFG_ES_AUTOSTART`. Produces
`write_autostart <file> <yes|no>` in `app-20-es.sh` (sourced by tests).

- [ ] **Step 1: Tests** in the new `es-autostart.bats` (source the script, as `es-theme.bats` does):
  - `"write_autostart yes: exec line only"`: file is exactly
    `exec /usr/local/lib/emulationstation/es-session`.
  - `"write_autostart no: hint, no exec"`: file equals the three spec lines;
    `run ! grep -q '^exec' "$f"`; `bash "$f"` prints both `echo` lines and exits 0.
  - `"write_autostart: wrong or missing value -> error"`: `run write_autostart "$f" maybe` →
    status ≠ 0; `run write_autostart "$f" ""` → status ≠ 0; the file is not created.
  - `"write_autostart: second run gives the same file"`.
- [ ] **Step 2:** Run `bats test/bats/es-autostart.bats`. Expected: fail (function missing).
- [ ] **Step 3:** Implement `write_autostart` (temp file + `mv`, mode 0644). `install_autostart`
  calls `write_autostart "$R/opt/retropie/configs/all/autostart.sh" "${CFG_ES_AUTOSTART:-}"`
  instead of the `printf`, keeps the `chown`, and logs `Autostart: ES starts at boot` or
  `Autostart: off, tty1 shows a shell`. Update the header comment and the environment list.
- [ ] **Step 4:** Static check: in `checks_units`, add
  `check "autostart.sh: es-session (yes) or the off hint (no)" autostart_ok` with
  `autostart_ok` = the file under `opt/retropie/configs/all/autostart.sh` is exactly the `yes` form,
  or has `EmulationStation autostart is off` and no line starting with `exec`. Update the fake
  roots in `test/bats/static-app.bats` if needed, and add a FAIL case with
  `exec emulationstation`.
- [ ] **Step 5:** Run `bats test/bats/es-autostart.bats test/bats/es-theme.bats test/bats/static-app.bats`
  and `shellcheck -x provision/common/app-20-es.sh test/lib/static-app.sh`. Expected: 0 `not ok`, clean.

### Task 3: `session.pid` in `es-session`

**Files:** Modify `overlay/usr/local/lib/emulationstation/es-session`. Test: `test/bats/es-session.bats`.

**Interfaces:** Produces `$RUN_DIR/session.pid` (one line, the PID of `es-session`), present
while the wrapper runs; `session.pid` removed at the end (`state` stays, see spec ruling 3.3).

- [ ] **Step 1: Tests:**
  - `"session.pid holds the wrapper PID while ES runs"`: the ES stub copies
    `$RUN_DIR/session.pid` to `$T/seen-session` (`ES_EXIT_ACTION`); `MAX_ITER=1 run "$SESSION"` →
    the file holds a number, and it was there before ES started.
  - `"after the end session.pid and state are gone"`: after `MAX_ITER=1 run "$SESSION"` →
    `[ ! -e "$RUN_DIR/session.pid" ] && [ ! -e "$RUN_DIR/state" ]`.
  - `"a session.pid of another wrapper is not removed"`: the ES stub writes `999999` into
    `session.pid` → after the end the file still holds `999999`.
  - `"abnormal end also removes session.pid"`: make the loop fail (as an existing abnormal-end
    test does, with `EXIT_HUP_TARGET` set to a dummy PID) → `session.pid` is gone.
- [ ] **Step 2:** Run `bats test/bats/es-session.bats`. Expected: the new tests fail.
- [ ] **Step 3:** In `main`, right after `mkdir -p "$RUN_DIR"`: `write_pid session $$`. New
  function `# cleanup_session : remove session.pid and state if session.pid is ours` (compare the
  file content with `$$`). Call it first in `on_exit`, before the `clean_exit` check. Add
  `session.pid` to the file contract in the header comment.
- [ ] **Step 4:** Run `bats test/bats/es-session.bats` and `shellcheck -x
  overlay/usr/local/lib/emulationstation/es-session`. Expected: 0 `not ok`, clean.

### Task 4: `es.Controller` without a session

**Files:** Modify `webui/internal/es/es.go`, `webui/internal/es/es_test.go`.

**Interfaces:** Produces `func (c *Controller) SessionRunning() bool`, `ErrNoSession`, state kind
`"off"`. Other behaviour is unchanged while a session runs.

- [ ] **Step 1: Test helper.** `newCtl` writes `session.pid` with `os.Getpid()`, so all existing
  tests run with a session. New `newCtlNoSession(t)` without it.
- [ ] **Step 2: Tests** (table where it fits):
  - `TestSessionRunning`: no file → false; `"junk"` → false; PID of a reaped process → false;
    own PID → true.
  - `TestStateOffWithoutSession`: `state` says `es` but no session → `Kind == "off"`.
  - `TestNoSessionActions`: `Start`, `Restart`, `Launch("snes", "/r/x.sfc")` →
    `errors.Is(err, ErrNoSession)`; afterwards no `hold`, no `launch` file.
  - `TestStopWithoutSession`: nil, no `hold`.
  - `TestStopGameWithoutSession`: `ErrNoGame`.
  - `TestWithESStoppedWithoutSession`: `fn` runs once, returns its error, no `hold`, and the call
    takes less than 500 ms (StopTimeout is 2 s).
- [ ] **Step 3:** Run `cd webui && go test ./internal/es/`. Expected: the new tests fail; the old
  ones pass.
- [ ] **Step 4: Implement.** `stateOff = "off"`; doc of `State.Kind` gets `"off"`. `State()`
  checks `SessionRunning()` first. `Start`, `Restart`, `Launch` (after `TryLock`) return
  `ErrNoSession` without a session. `Stop` returns nil without a session. `WithESStopped`,
  inside the lock: without a session it returns `fn()`. `StopGame` keeps working through
  `State()` (`off` ≠ `game` → `ErrNoGame`).
- [ ] **Step 5:** Run `cd webui && gofmt -l . && go vet ./... && go test -race -count=1 ./internal/es/`.
  Expected: no gofmt output, ok.

### Task 5: Web UI texts and status

**Files:** Modify `webui/internal/web/handlers_dashboard.go` (`actionError`),
`webui/internal/web/handlers_controllers.go:95`, `webui/internal/web/templates/partials/status.html`,
and the test setups `webui/internal/web/server_test.go:55`, `webui/internal/web/games_test.go:91`.
Test: `webui/internal/web/server_test.go`.

**Interfaces:** Consumes `ErrNoSession` and kind `"off"` from Task 4.

- [ ] **Step 1: Test setup.** The two places that build an `es.Controller` write `session.pid`
  with `os.Getpid()` into the run dir. Add a helper option or a small function
  `noSession(t, d)` that removes it.
- [ ] **Step 2: Tests:**
  - `TestDashboardOff`: no session → body has `EmulationStation is off` and
    `Autostart is off. Start it on the device screen.`, and none of `/es/start`, `/es/stop`,
    `/es/restart`.
  - `TestESStartWithoutSession`: POST `/es/start` (htmx) → 409 and the body or toast has
    `EmulationStation is not running on the device. Start it on the device screen.`
  - `TestControllersActionWithoutSession`: a controller change runs without a session (no
    timeout error).
- [ ] **Step 3:** Run `cd webui && go test ./internal/web/`. Expected: the new tests fail.
- [ ] **Step 4: Implement.** In `actionError`, put `ErrNoSession` first: 409 and the text. In the
  controllers mapping, add the same case. In `status.html`, add an `off` branch (grey dot,
  title and hint as in Global Constraints) and wrap the whole button block in
  `{{if ne .State.Kind "off"}}`.
- [ ] **Step 5:** Build the CSS if it is missing (`tailwindcss -i assets/input.css -o
  internal/web/static/app.css --minify`), then `cd webui && gofmt -l . && go vet ./... && go test
  -race -count=1 ./...`. Expected: ok.

### Task 6: Docs and full check

**Files:** Modify `README.md` (the `build.conf` section), the main spec section 1 (criterion 1),
`docs/hardware-checklist.md`.

- [ ] **Step 1:** README: the key, the default `no`, what you see on tty1, the start command, and
  that the web UI shows "off" and cannot start ES. Main spec criterion 1: the text from spec
  section 5. Checklist: the two items from spec section 4.
- [ ] **Step 2:** Run the full checks as `./build.sh test` would: `shellcheck -x` over all bash
  files, `bats test/bats`, `go vet` + `go test -race`, `gofmt -l webui`,
  `scripts/check-license-headers.sh`. Expected: all clean, 0 `not ok`.
- [ ] **Step 3:** Report what was not run: `./build.sh test` in Docker, image builds, the VM smoke
  test with `ES_AUTOSTART=yes`, and hardware.
