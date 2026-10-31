# Custom web UI files – Implementation Plan

License: AGPL-3.0-or-later, see LICENSE

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The web UI takes templates and static files from a custom folder first and falls back
to the embedded files; a System page button reloads the templates.

**Architecture:** A lazy overlay `fs.FS` (custom folder through `os.Root`, then `embed`) feeds
`parseTemplates` and the static file server. Parsed templates sit behind an `atomic.Pointer`, so
reload swaps them. The builder copies `webui-custom/` into the image (app layer) and lets
Tailwind scan its templates.

**Tech Stack:** Go 1.25 (stdlib `os.Root`, `io/fs`, `sync/atomic`), Bash 5, bats, Tailwind v4.

**Spec:** `docs/superpowers/specs/2026-10-31-webui-custom-design.md`

## Global Constraints

- Simple English in code, comments, logs, UI texts and docs. AGPL header on every own source file.
- Git is read only. The owner commits; "Commit" steps mean: stop at a clean, tested state.
- No new Go modules. No new ports or listeners. Base files stay untouched (base cache stays valid).
- Device folder `/home/emulationstation/webui-custom/`, owner `emulationstation`, dirs 0755, files 0644.
- `web.conf` key `UI_DIR`, default `<HOME_DIR>/webui-custom`, empty = off.
- UI texts (exact): "Custom web UI is off.", "<n> custom files in use from <dir>.",
  "The custom templates have an error. The built-in web UI is in use.", button "Reload web UI",
  toast "Web UI reloaded.".
- Build errors (exact): "webui-custom/<path> must be a regular file.",
  "webui-custom/<path>: only templates/ and static/ are allowed."
- Log line on fallback: `custom UI: <error>; using built-in templates`.

## Review Focus

1. A custom folder created or removed **after** the service started — Open must look at the disk
   each time (lazy `os.OpenRoot`), not a root opened once. → Task 2 test `TestOverlayLazyFolder`.
2. A symlink inside the custom folder pointing outside, or `..` in a name — must fall back or
   fail, never read outside. → Task 2 table rows.
3. Reload while requests render (`go test -race`). → Task 3 `TestReloadConcurrent`.
4. A custom file only in `static/` but none in `templates/` — templates stay built-in, count
   still right. → Task 3 `TestUIStateCountsAll`.
5. A broken custom partial (not page) also triggers the full built-in fallback. → Task 3 row.

---

### Task 1: Config key `UI_DIR`

**Files:** Modify `webui/internal/config/config.go`; Test `webui/internal/config/config_test.go`

**Interfaces:** Produces `Config.UIDir string`.

- [ ] Step 1: table test `TestUIDir`: no key → `/home/emulationstation/webui-custom`;
  `HOME_DIR=/h` → `/h/webui-custom`; `UI_DIR=` → `""`; `UI_DIR=/x` → `/x`; Save+Load round trip
  keeps `""` and `/x`.
- [ ] Step 2: run `go test ./internal/config/ -run TestUIDir` → FAIL (no field).
- [ ] Step 3: field `UIDir` on `Config` (not `Paths`, it is not derived after load); default set
  in `Default()` after `setHome`, and re-derived when `HOME_DIR` is read but `UI_DIR` is not
  given; `set("UI_DIR", &c.UIDir)`; `{"UI_DIR", c.UIDir}` in `Save`.
- [ ] Step 4: test passes; whole package passes.

### Task 2: Overlay file system

**Files:** Create `webui/internal/web/overlay.go`, `webui/internal/web/overlay_test.go`

**Interfaces:**
- Produces `newOverlayFS(dir string, base fs.FS) fs.FS` (returns `base` when `dir == ""`),
  implementing `fs.ReadDirFS`.
- Produces `countCustomFiles(dir string) int` (regular files under `templates/` and `static/`,
  dot names skipped, 0 when the folder is missing).

- [ ] Step 1: `TestOverlay` table (temp dir + `fstest.MapFS` base): override; fallback;
  custom-only file; `ReadDir` union sorted without duplicates; `fs.Glob("templates/*.html")`
  union; `dir == ""`; missing dir; name `../x` → `fs.ErrInvalid`/not found, no read outside;
  symlink to a file outside → base or error, never the outside content; custom entry that is a
  folder where base has a file → base file; `.hidden` ignored. `TestOverlayLazyFolder`: folder
  created after `newOverlayFS` → used; removed → base. `TestCountCustomFiles`.
- [ ] Step 2: run → FAIL (undefined).
- [ ] Step 3: implement. `Open`: `fs.ValidPath`, skip if any element starts with `.`,
  `os.OpenRoot(dir)` per call, `root.Open`, keep only regular files (`Stat().Mode().IsRegular()`),
  return a file wrapper whose `Close` closes the file and the root.
  Directories always come from `base` merged via `ReadDir`. `ReadDir`: entries of
  `root.Open(name)`+`ReadDir` (regular files and dirs only) merged with `fs.ReadDir(base, name)`;
  missing in one side is fine, missing in both → error from base.
- [ ] Step 4: `go test -race ./internal/web/ -run 'Overlay|CountCustom'` passes.

### Task 3: Templates reload and static overlay

**Files:** Modify `webui/internal/web/render.go`, `server.go`, `handlers_games.go`,
`handlers_bluetooth.go`; Test `webui/internal/web/custom_test.go` (new)

**Interfaces:**
- Consumes `newOverlayFS`, `countCustomFiles`, `Config.UIDir`.
- Produces `type uiState struct{ Dir string; Files int; Err string }` (Dir `""` = off);
  `func (s *Server) loadTemplates() uiState`; `Server.tpl atomic.Pointer[templates]`;
  `Server.ui atomic.Pointer[uiState]`.

- [ ] Step 1: tests: custom `templates/dashboard.html` text shows on `GET /`; broken custom
  page and broken custom partial → built-in page renders, `ui.Err != ""`, log has
  `using built-in templates`; `TestReloadConcurrent` (requests in goroutines while
  `loadTemplates` runs in a loop, `-race`); `GET /static/x.css` served from custom;
  `TestUIStateCountsAll` (only a static file → `Files == 1`, templates built-in); missing folder →
  `Dir == ""`.
- [ ] Step 2: run → FAIL.
- [ ] Step 3: `loadTemplates` reads `s.d.Config().UIDir`; if the folder is missing it is "off".
  Parse with the overlay; on error log and parse `assets` (panic only if that fails). Store both
  pointers. `NewHandler` calls it. Readers use `s.tpl.Load()`. `routes` builds the static
  server from `fs.Sub(newOverlayFS(dir, assets), "static")` (the overlay is lazy, so it never
  needs a rebuild). Err keeps only the first line of the error.
- [ ] Step 4: all web tests pass with `-race`.

### Task 4: System page card and reload route

**Files:** Modify `handlers_system.go`, `templates/system.html`; Create
`templates/partials/custom_ui.html`; Test `pages_test.go`

**Interfaces:** Consumes `loadTemplates`, `Server.ui`. Produces route `POST /system/ui-reload`,
partial `custom_ui` (data: `uiState`).

- [ ] Step 1: tests: `/system` shows each of the three exact texts and the button; POST reload
  after changing a custom file → 200, `HX-Trigger` toast "Web UI reloaded.", new text on the next
  `GET /`; POST reload with a broken file → toast with the error text, card shows error; POST
  without CSRF/auth → rejected like the other System POSTs.
- [ ] Step 2: run → FAIL.
- [ ] Step 3: `sysPageView` gets `UI uiState`; handler answers `s.partial(w, 200, "custom_ui", st)`
  with `toast(...)`; the card uses `hx-post` and `hx-target` on itself, like the bans card.
- [ ] Step 4: `go vet ./... && go test -race -count=1 ./...` and `gofmt -l .` empty.

### Task 5: Build side (`webui-custom/`, step 40, Tailwind)

**Files:** Create `webui-custom/.gitkeep`, `webui-custom/README.md`,
`webui/custom/templates/.gitkeep`; Modify `builder/steps/40-provision-app.sh`,
`webui/assets/input.css`, `scripts/check-license-headers.sh`; Test `test/bats/app.bats`,
`test/bats/license.bats`

**Interfaces:** Produces `app_stage_webui_custom <src> <dst>` (copies `templates/` and `static/`,
skips `.gitkeep`/`README.md` at top level, dies with the exact messages) and the provision
folder `$prov/webui-custom/` (always exists).

Ruling: a tracked `webui/custom/templates/.gitkeep` as the Tailwind source folder — the
`@source` path then always exists, also in `./build.sh test` and on the host, so there is no
doubt how Tailwind treats a missing path — cost if wrong: one empty tracked folder.

- [ ] Step 1: bats: ok copy (nested template + static image); missing src → empty dst; symlink →
  exact error; top-level `other/` → exact error; `.gitkeep`/`README.md` not copied;
  `app_stage_provision` creates `$prov/webui-custom`; `app_build_web` puts custom templates into
  `<copy>/custom/templates` (stub `build.sh` lists it); license check skips `webui-custom/`.
- [ ] Step 2: run `bats test/bats/app.bats test/bats/license.bats` → FAIL.
- [ ] Step 3: implement; `input.css` gets `@source "../custom/templates";`; license script
  excludes `./webui-custom` like `./controllers/custom`.
- [ ] Step 4: bats passes; shellcheck clean.

### Task 6: Image side, checks, docs

**Files:** Modify `provision/common/app-30-webui.sh`, `test/lib/static-app.sh`, `README.md`,
`docs/hardware-checklist.md`; Test `test/bats/app.bats`, `test/bats/static-app.bats`

**Interfaces:** Consumes `$PROVISION_DIR/webui-custom/`.

- [ ] Step 1: bats: `app-30-webui.sh` copies to `$R/home/emulationstation/webui-custom`
  (modes 0755/0644, `chown` stub sees `emulationstation`), removes a stale file from an earlier
  run, creates the folder when the source is empty, writes `UI_DIR=/home/emulationstation/webui-custom`;
  second run gives the same result. static-app: folder missing or wrong owner → check fails;
  `web.conf` without `UI_DIR` → fails.
- [ ] Step 2: run → FAIL.
- [ ] Step 3: implement (rm -rf target, `cp -a`, then `chown -R`, `find -type d -exec chmod 0755`,
  `find -type f -exec chmod 0644`; `webconf_set "$CONF" UI_DIR …`). Docs: README section
  "Custom web UI" (layout, fallback, reload button, `UI_DIR`, Tailwind note, new image replaces
  the folder, data may change with updates); hardware checklist item.
- [ ] Step 4: full run: `bats test/bats` 0 not ok; `cd webui && go vet ./... && go test -race
  -count=1 ./...`; `gofmt -l webui` empty; `scripts/check-license-headers.sh` empty. Not run
  here: `./build.sh test` (Docker), a real image, hardware.
