# EmuStation Builder – Better EmulationStation detection in the web UI – Design

License: AGPL-3.0-or-later, see LICENSE

As of: 2026-10-31 · Status: approved by the owner (chat, 2026-10-31)

This spec changes `2026-10-31-es-autostart-design.md` (status `off`) and the ES part of the main
spec. Where they disagree, this spec wins.

## 1. Problem

The web UI knows ES only through the wrapper `es-session` (file contract in
`/run/emulationstation`). The owner found three gaps (UTM VM, `ES_AUTOSTART=no`):

1. On tty1 the owner typed `emulationstation`. That starts ES without `es-session`, so the web UI
   shows "off" and cannot start, stop or restart ES.
2. A game started from the ES menu is never shown. `es-session` writes the state `game` only for
   games that the web UI starts (launch file). For a menu game the state stays `es`, and
   "Stop game" says "No game is running". Worse: an action that stops ES (for example "Apply" of
   uploads) sends SIGTERM to ES while the game still runs.
3. When ES runs without `es-session` (for example started over SSH), the web UI says "off" and
   applies uploads at once, while ES runs and may write its gamelists.

## 2. Goal and success criteria

1. On tty1, `emulationstation` (no arguments) for the user `emulationstation` starts `es-session`.
   With arguments, or in any other shell (SSH, other tty), the normal command runs.
2. The web UI shows a game started from the ES menu as "Game running" with system and ROM, and
   "Stop game" ends it. ES stays alive and comes back.
3. ES that runs without `es-session` is shown as "EmulationStation runs outside its session".
   Actions that need ES stopped are refused with a clear message instead of running at once.
4. With no ES and no game, "off" stays as before.

## 3. Decisions

| Topic | Decision | Reason |
|---|---|---|
| tty1 command | shell function `emulationstation` in `/etc/profile.d/30-emulationstation-session.sh` | only login shells read it; non-interactive scripts (autostart.sh, es-session) do not see it |
| Menu game | a process of the own user whose argument list has the exact element `/opt/retropie/supplementary/runcommand/runcommand.sh` | runcommand runs every game; exact match skips the `sh -c "<command line>"` parent |
| Game names | `/dev/shm/runcommand.info` (line 1 system, line 3 ROM), only when its mtime is not older than the runcommand process start (2 s slack) | runcommand writes it at every start and leaves it after the end; an old file must not name a new game |
| Stop a menu game | SIGTERM to all descendants of the runcommand process and to runcommand itself; after 5 s SIGKILL; never the process group | the menu game is in the process group of ES (ES uses `system()`), so a group signal would kill ES |
| Unmanaged ES | a process of the own user whose `/proc/<pid>/exe` is `/opt/retropie/supplementary/emulationstation/emulationstation`, while no session runs | the same binary that `es-session` starts (`ES_BIN`) |
| Detection on/off | `Controller.Procs` (interface); nil = no `/proc` scan | tests stay deterministic; `main.go` sets the real one |

Ruling: find menu games through `/proc`, not through a new hook in ES or runcommand — RetroPie
files stay unchanged (base layer, vendor code) — cost if wrong: a game started by a program
other than runcommand is not seen.

Ruling: a web-UI game (state `game`, `game.pid`) wins over a menu game — its state is exact and
"Stop game" keeps the old group kill — cost if wrong: none; both ways stop the game.

Ruling: an unmanaged ES blocks "Apply" and other actions that need ES stopped
(`ErrUnmanaged`) — ES writes gamelists when it quits, so changing them under a running ES can
lose data — cost if wrong: the owner must quit ES on the device first.

## 4. Changes

### 4.1 Image: `overlay/etc/profile.d/30-emulationstation-session.sh`

POSIX `sh`, root:root 0644. Defines the function only when all of these hold: the user is
`emulationstation` (`id -un`), the shell is interactive (`$-` has `i`), and `tty` prints
`/dev/tty1`.

```
emulationstation() {
  if [ "$#" -gt 0 ]; then command emulationstation "$@"; return; fi
  if <session.pid holds a live PID>; then
    echo "EmulationStation runs already in its session."; return 1
  fi
  exec /usr/local/lib/emulationstation/es-session
}
```

The `autostart.sh` hint for `ES_AUTOSTART=no` stays (it shows the `es-session` line; the
function makes `emulationstation` work too). README: tty1 section.

### 4.2 Web UI: `webui/internal/es`

- New interface `Procs` with `List() ([]Proc, error)`, `Proc{PID, PPID, UID int; Start time.Time;
  Args []string; Exe string}`, and the real `ProcFS{Root: "/proc"}` (reads `cmdline`, `status`
  (PPid, Uid), `stat` (field 22, start time in clock ticks, 100 per second) and `exe`; boot time
  from `/proc/stat` `btime`). Processes that vanish while reading are skipped.
- `Controller` gets `Procs Procs`, `InfoFile string` (default `/dev/shm/runcommand.info`),
  `RunCommand string` (default runcommand path) and `ESBinary string` (default ES path).
- `State()`:
  1. Session runs and the state file says `game` → as before.
  2. Else, with `Procs`: a menu game of the own UID (`os.Getuid()`) → `Kind "game"`,
     `Menu true`, system and ROM from the info file (empty when the file is old or missing).
  3. Session runs → state file as before.
  4. No session, with `Procs`: ES binary of the own UID runs → `Kind "unmanaged"`.
  5. Else `off`.
- `StopGame()`: state `game` with `Menu` → the tree stop of section 3; else as before.
  It never signals PID ≤ 1, the own PID or a process of another UID.
- `WithESStopped`, `Stop`, `Launch`: a menu game gives `ErrGameRunning` (through `State()`).
  Without a session: `unmanaged` gives the new `ErrUnmanaged`; otherwise as before.
- `Start`, `Restart`: unchanged (`ErrNoSession` without a session).

### 4.3 Web UI: `webui/internal/web`

- `actionError`: `ErrUnmanaged` → 409, `EmulationStation runs, but not in its session. Quit it on
  the device, then start it again with "emulationstation" on the device screen.`
- Status partial: new branch `unmanaged` (title `EmulationStation runs outside its session`,
  hint `The web UI cannot control it. Quit it and start it again on the device screen.`), no ES
  buttons. A game (also a menu game) shows "Stop game" also without a session.
- `main.go`: `Procs: es.ProcFS{Root: "/proc"}`.

### 4.4 Tests and docs

- Go (`internal/es`): fake `Procs` (table tests): menu game found by exact argument, `sh -c`
  parent ignored, other UID ignored; info file fresh → names, stale → empty; web-UI game wins;
  unmanaged ES only without a session; `WithESStopped` with a menu game → `ErrGameRunning`,
  with unmanaged ES → `ErrUnmanaged`; tree stop signals the descendants and runcommand but not
  the parent (a real `sleep` process tree; ES stand-in survives). `ProcFS` against a fake `/proc`
  folder in `testdata`-like temp files.
- Go (`internal/web`): status shows "Game running" for a menu game and the unmanaged text.
- bats: the profile.d function (tty and user faked through stubs of `tty` and `id`): runs
  es-session only on tty1 for the right user, passes arguments through, refuses a second session.
- `docs/hardware-checklist.md`: menu game shown and stopped from the web UI; `emulationstation`
  on tty1 gives the session; ES over SSH shows "outside its session".
