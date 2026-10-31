# Better EmulationStation detection in the web UI – Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this
> plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `emulationstation` on tty1 starts the session; the web UI sees menu games and an ES
outside the session.

**Spec:** `docs/superpowers/specs/2026-10-31-es-session-detect-design.md`

## Global Constraints

- Git is read only. Simple English. No new ports, no root helper change, no base-layer change.
- `Controller.Procs == nil` keeps the old behaviour (all existing tests stay valid).
- Never signal PID <= 1, the own PID, or a process of another UID. Never a process group for a
  menu game.

## Tasks

- [ ] 1. `overlay/etc/profile.d/30-emulationstation-session.sh` + `test/bats/es-tty1.bats`.
- [ ] 2. `webui/internal/es/procs.go`: `Proc`, `Procs`, `ProcFS`; tests with a fake `/proc`.
- [ ] 3. `es.go`: `State()` with menu game and `unmanaged`, `StopGame` tree stop, `ErrUnmanaged`
  in `WithESStopped`/`Stop`/`Launch`; tests with a fake `Procs`.
- [ ] 4. `web`: `actionError`, status template, `main.go` wiring; tests.
- [ ] 5. README, hardware checklist.
- [ ] 6. `./build.sh test`.

## Review Focus

1. The tree stop must not reach ES (the parent of the runcommand `sh -c`).
2. A stale `runcommand.info` must not name the wrong game.
3. `WithESStopped` without a session but with an unmanaged ES must not run `fn`.
