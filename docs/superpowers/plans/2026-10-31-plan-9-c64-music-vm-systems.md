# C64 + Dreamcast in the VM, C64 music (.sid) – Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this
> plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The VM images get `vice` and `lr-flycast`. Every image with VICE gets an ES system
`c64music` for `.sid` files. The web UI does not offer scraping for it.

**Spec:** `docs/superpowers/specs/2026-10-31-c64-music-vm-systems-design.md`

## Global Constraints

- Git is read only. Simple English. AGPL header on every new file.
- No new ports, no root helper change. Only `provision/vm/modules.list` touches the base layer.
- Exact texts: web error `Scraping is not available for this system.`; system full name
  `Commodore 64 Music`.

## Tasks

- [ ] 1. `provision/vm/modules.list`: append `vice` and `lr-flycast`.
- [ ] 2. `provision/common/app-25-c64music.sh` (spec 3.2), with functions `write_emulators`,
  `add_system` and `main`; only `main` runs when the file is executed.
- [ ] 3. `test/bats/c64music.bats` (spec 3.4) with a fixture `test/fixtures/es_systems/`.
- [ ] 4. `webui/internal/scrape`: `Supported`; table test.
- [ ] 5. `webui/internal/web`: 400 in `handleScrape`, filter in the upload commit, `Scrape` flag
  in the games view and template; tests.
- [ ] 6. `test/lib/static-app.sh`: `c64music_ok` check; bats case in `static-app.bats`.
- [ ] 7. `docs/hardware-checklist.md`, `README.md`.
- [ ] 8. `./build.sh test` (or bats + go vet + go test -race + gofmt + license check).

## Review Focus

1. `es_systems.cfg` must stay valid XML and keep all other systems unchanged.
2. A second run must not add a second `c64music`.
3. Scrape-after-upload with only `.sid` files must not start an empty job.
