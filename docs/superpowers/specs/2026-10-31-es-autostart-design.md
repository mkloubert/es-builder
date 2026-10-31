# EmuStation Builder – EmulationStation autostart switch – Design

License: AGPL-3.0-or-later, see LICENSE

As of: 2026-10-31 · Status: draft for approval

This spec changes the main spec `2026-10-27-emulationstation-image-design.md` (section 1, success
criterion 1, and section 5.1). Where the two disagree, this spec wins.

## 1. Goal and success criteria

The owner can choose in `build.conf` if EmulationStation (ES) starts by itself after boot.

1. `ES_AUTOSTART=yes`: the device boots straight into ES, as before.
2. `ES_AUTOSTART=no` (default): tty1 logs in user `emulationstation` by itself and shows a shell
   with a short hint. ES does not run. The owner can start it on the device with one command.
3. With `ES_AUTOSTART=no`, every web UI page and action works. Actions that need ES stopped (for
   example committing uploads or changing controllers) run at once, without a 30 second wait.
   Actions that need a running ES session (start, restart, start a game) show a clear message.
4. A value other than `yes` or `no` stops the build with a clear message.

## 2. Decisions

| Topic | Decision | Reason |
|---|---|---|
| Behaviour with `no` | autologin on tty1 stays; `autostart.sh` prints a hint and does not start `es-session` (owner's choice "C") | keyboard on the device gives a shell at once; small change |
| Default | `no` | owner's wish |
| Where the switch acts | app layer (`app-20-es.sh`) | no change to base files, the base cache stays valid |
| How the web UI knows if a session runs | `es-session` writes `session.pid`; the web UI checks that the PID is alive | exact, no race at boot, no new `web.conf` key |
| Runtime switch (web UI, `emulationstation.txt`) | not part of this spec | YAGNI; can be a later spec |

Ruling: detect the session with a PID file, not with a `web.conf` key — the owner can start
`es-session` by hand when autostart is off, and a key would then be wrong — cost if wrong: one
small file in `/run/emulationstation`.

## 3. Changes

### 3.1 Configuration

- `builder/lib/config.sh`: new key `ES_AUTOSTART` in `CONFIG_KEYS`, default `no`.
- New check in `config_load` (after the file is read): the value is exactly `yes` or `no`. Else
  `log_error "ES_AUTOSTART must be yes or no (found: <value>)"` and return 1. Callers exit with
  code 2, as for other config errors.
- `build.conf.example`: new block with the key, `ES_AUTOSTART=no`, and one comment line per
  value.
- The value reaches the app scripts as `CFG_ES_AUTOSTART` through `build.env`, like the other
  `CFG_*` keys (`40-provision-app.sh`).
- `test/fixtures/build.conf.smoke`: `ES_AUTOSTART=yes`, because `test/vm-smoke.sh` checks that
  the ES process runs.

### 3.2 Image (`provision/common/app-20-es.sh`)

`install_autostart` stays the same (autologin drop-in, RetroPie hook, checks). Only the content
of `/opt/retropie/configs/all/autostart.sh` depends on `CFG_ES_AUTOSTART`:

- `yes`: `exec /usr/local/lib/emulationstation/es-session` (as now).
- `no`:
  ```
  # EmulationStation autostart is off (build.conf ES_AUTOSTART=no).
  echo "EmulationStation autostart is off. To start it, type:"
  echo "  exec /usr/local/lib/emulationstation/es-session"
  ```
  RetroPie's hook runs this file with `bash`, so the login shell stays open after it.
- A missing or wrong `CFG_ES_AUTOSTART` stops the script (`die`). The script is idempotent: a
  second run writes the same file.

### 3.3 Session wrapper (`overlay/usr/local/lib/emulationstation/es-session`)

- The file contract gets a new file: `session.pid` = PID of the running `es-session`.
- At start, before the first ES start, the script writes `$RUN_DIR/session.pid` (temp file +
  rename), with its own PID (`$$`).
- At every end (clean or not, in the EXIT trap), it removes `session.pid`, but only if it still
  holds its own PID (a second session must not remove the file of the first one). `state` stays:
  the web UI checks the session first and ignores `state` without one.

Ruling: keep `state` at the end — `State()` checks the session first, so a left `state` is never
shown, and the existing wrapper tests read the last state — cost if wrong: one stale small file
in `/run` (cleared at reboot).

### 3.4 Web UI (`webui/internal/es`, `webui/internal/web`)

- `func (c *Controller) SessionRunning() bool`: reads `session.pid` with the existing `readPID`
  rules and returns `alive(pid)`.
- `State()`: if no session runs, it returns the new kind `off` (constant `stateOff = "off"`),
  before it reads the state file.
- New error `ErrNoSession = errors.New("es: no EmulationStation session on the device")`.
  `Start`, `Restart` and `Launch` return it when no session runs. They do not change any file.
- `Stop`: without a session there is nothing to stop. It returns nil and does not create `hold`.
- `WithESStopped`: without a session it runs `fn` at once (no stop and no wait). For the time of
  `fn` it puts a `hold` file in place, unless one is there already, and removes it afterwards.

Ruling: a short `hold` during `fn` also without a session — a wrapper can start while `fn` runs
(boot or re-login with `ES_AUTOSTART=yes`) and would start ES in the middle of a gamelist or
input change; the old code covered this by waiting — cost if wrong: none, the hold is removed
afterwards. If a game
  runs, the old rule (`ErrGameRunning`) still comes first, but a game needs a session, so this
  case cannot happen without one.
- `StopGame`: without a session it returns the existing `ErrNoGame`.
- `actionError` (dashboard) and the error mapping in `handlers_controllers.go` map
  `ErrNoSession` to HTTP 409 and the text "EmulationStation is not running on the device.
  Start it on the device screen."
- `templates/partials/status.html`: kind `off` shows "EmulationStation is off" and the hint
  "Autostart is off. Start it on the device screen." The start, stop and restart buttons are not
  shown for `off`.

Ruling: no start button for `off` — the web UI cannot start a session on tty1 without a new root
helper command, and the spec keeps this out of scope — cost if wrong: the owner must use the
device to start ES.

## 4. Tests

- `test/bats/config.bats`: no key → `CFG_ES_AUTOSTART=no`; `yes` and `no` are accepted; `Yes`,
  `1` and an empty value fail with the message.
- `test/bats/app.bats` (or the existing `app-20-es.sh` tests): `CFG_ES_AUTOSTART=yes` → the
  `exec` line; `no` → the hint lines and no `exec`; missing → error; a second run gives the same
  file.
- `test/bats/es-session.bats`: `session.pid` holds the PID while the loop runs (`MAX_ITER`);
  after the end `session.pid` is gone; a `session.pid` with another PID is not removed.
- `webui/internal/es/es_test.go` (table tests, temp dirs): `SessionRunning` with no file, a dead
  PID, a live PID (own process), and junk; `State` gives `off` without a session;
  `Start`/`Restart`/`Launch` give `ErrNoSession` and leave the files alone; `Stop` gives nil
  without `hold`; `WithESStopped` runs `fn` at once without a session (test finishes far below
  the stop timeout) and still stops ES with a session.
- `webui/internal/web`: the dashboard shows the `off` text and no ES buttons; a POST
  `/es/start` without a session gives 409 and the message.
- Static checks (`test/lib/static-app.sh`): `autostart.sh` is one of the two forms above.
- `docs/hardware-checklist.md`: with `no`, tty1 shows the hint, and the command starts ES and
  the web UI then shows "running"; with `yes`, the Pi boots into ES.

## 5. Docs

- `README.md`: the new key in the `build.conf` section; the default is `no`; how to start ES
  by hand.
- Main spec: success criterion 1 becomes "With `ES_AUTOSTART=yes` the Pi boots straight into
  EmulationStation with EpicNoir, without any manual step."

## 6. Risks

- An owner who expects the old behaviour gets a shell after the update, because the default is
  `no`. The README says this clearly.
- If `es-session` is killed with SIGKILL, `session.pid` stays. The web UI then sees a dead PID
  and shows `off`, which is correct. PID reuse can make a dead session look alive. The existing
  `readPID` rules have the same limit. Accepted.
