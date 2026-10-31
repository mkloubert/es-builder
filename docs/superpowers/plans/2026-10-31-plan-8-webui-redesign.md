# Web UI redesign "Noir Grid" Implementation Plan

License: AGPL-3.0-or-later, see LICENSE

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the web UI the "Noir Grid" look and add the page "About & licenses" with complete third-party notices.

**Architecture:** Tailwind v4 `@theme` tokens and a small `@layer components` set in
`webui/assets/input.css`; templates are rewritten page by page on top of them. Fonts, a Lucide
SVG sprite and updated vendor JS are embedded. A new package `internal/legal` holds the license
texts and the component list, which the new `/about` page and the coverage tests use.

**Tech Stack:** Go 1.26 (`html/template`, `embed`), Tailwind CSS CLI v4.3.3, htmx 2.0.11, Alpine.js 3.17.5, Lucide 1.66.0, bats, Bash.

**Spec:** `docs/superpowers/specs/2026-10-31-webui-redesign-design.md`

## Global Constraints

- Hard rules of `CLAUDE.md` apply. Git is read only for the agent: the "Commit" steps are done by the owner; the agent stops and lists the files instead.
- Every own new file starts with the AGPL header copied from a file of the same type. Third-party files (vendor JS, fonts, `icons.svg`, license texts) get no header and stay byte-for-byte as upstream ships them.
- No resource is loaded from the internet. No `<script src>`, `<link href>` or CSS `url()` points to `http://` or `https://`.
- Routes, form field names, htmx attributes (`hx-*` targets, triggers, swaps), element ids used by htmx/JS (`#status`, `#games-list`, `#q`, `#modal-slot`, …) and the user-visible texts of actions stay as they are. Existing Go tests that check texts must keep passing without changes, except `footer_test.go` (Task 4).
- UI texts: simple English, short.
- Tokens (spec 3.1): bg `#0a0a0b`, surface `#111113`, raised `#141416`, line `#232326`, line-strong `#2c2c30`, fg `#ededed`, muted `#a1a1a6`, dim `#86868c`, accent `#e5484d`, accent-fg `#ff6b6e`, ok `#4ade80` (border `#1f4d33`, fill `#0d1f15`), warn `#fcd34d` (border `#5a3a12`, fill `#1a1206`). Radius 0.
- Fonts: `--font-display` "Bebas Neue", `--font-sans` "Inter", `--font-mono` "Geist Mono".
- Allowed SPDX ids (CLAUDE.md rule 8): `MIT`, `BSD-2-Clause`, `BSD-3-Clause`, `ISC`, `0BSD`, `Apache-2.0`, `OFL-1.1`, `GPL-3.0-or-later`, `AGPL-3.0-or-later`.
- Copy that is fixed by the spec: "Copyright (C) 2026  Marcel Joachim Kloubert" (two spaces); "This program comes with ABSOLUTELY NO WARRANTY. It is free software, and you are welcome to redistribute it under the terms of the GNU Affero General Public License, version 3 or later."; "No source code link is set. Set SOURCE_URL in build.conf."
- "Done" per CLAUDE.md: `./build.sh test` green (or bats 0 `not ok`, `go vet`, `go test -race`, `gofmt -l` empty, license check clean). `app.css` must be generated before `go test` (`webui/scripts/build.sh` or the tailwindcss command in CLAUDE.md).

## Review Focus

1. A `SOURCE_URL` that is empty or not http(s) must still give a working About page (hint text, no link, no leaked value). → test in Task 5.
2. A custom template set in `webui-custom/` that still uses the old layout blocks must not break rendering: the overlay falls back per file, and the new layout must not require blocks that old page templates lack (`subtitle`, `actions` need defaults). → test in Task 4.
3. Long names (game names of 200 chars, long system names, long file names) must not break rows or the grid on a 360 px screen: `min-w-0` + `truncate` with `title`. → test in Task 6 (rows carry `title` and `truncate`).
4. The "…" menu and the phone menu must work with keyboard only and must close on Escape; while the confirm dialog is open the 5 s status polling stays paused (`body.confirming`). → test in Task 4 (markup: `aria-expanded`, `aria-controls`, `@keydown.escape`) and Task 6 (menu markup).
5. A page name without a nav entry (`job`, `games_system`, `upload_review`) must still render a breadcrumb and mark the right nav entry. → test in Task 4.

---

### Task 1: Package `internal/legal` with license texts and component list

**Files:**
- Create: `webui/internal/legal/legal.go`, `webui/internal/legal/legal_test.go`
- Create: `webui/internal/legal/LICENSE` (byte copy of repo `LICENSE`)
- Create: `webui/internal/legal/THIRD-PARTY-NOTICES`
- Create: `webui/internal/legal/licenses/{htmx,alpinejs,tailwindcss,lucide,bebas-neue,inter,geist-mono,go,x-crypto,godbus}.txt`
- Modify: `scripts/check-license-headers.sh` (exclude `webui/internal/legal/LICENSE`, `THIRD-PARTY-NOTICES`, `licenses/`)

**Interfaces:**
- Produces:
  - `type Component struct { Name, Version, SPDX, URL string; Files []string; LicenseFile string }` — `Files` are paths relative to the web `static/` folder that this component covers (for example `"vendor/htmx.min.js"`); empty for compiled-in Go code.
  - `var Components []Component` in the order of spec 5.2.
  - `func LicenseText() string` — the AGPL text.
  - `func ComponentLicense(c Component) (string, error)` — reads `licenses/<LicenseFile>` from the embedded FS.
  - `var AllowedSPDX = map[string]bool{…}` (Global Constraints list).
  - `func ModuleVersion(path, fallback string) string` — version from `debug.ReadBuildInfo()`, else `fallback`.

- [ ] **Step 1: Write the failing tests** in `legal_test.go`:
  - `TestComponentsComplete`: for each `c` in `Components`: `c.Name`, `c.Version`, `c.URL` non-empty, `AllowedSPDX[c.SPDX]`, `ComponentLicense(c)` returns non-empty text without error.
  - `TestLicenseEqualsRepoLicense`: `os.ReadFile("../../../LICENSE")` equals `LicenseText()` byte for byte.
  - `TestEveryGoModuleListed`: parse `../../go.mod` `require` lines (module paths); each must equal the `Name` of a component (`golang.org/x/crypto`, `github.com/godbus/dbus/v5`).
  - `TestNoticesFileNamesEveryComponent`: the embedded `THIRD-PARTY-NOTICES` contains every `c.Name` and `c.SPDX`.
- [ ] **Step 2: Run** `cd webui && go test ./internal/legal/` — expected: FAIL (package does not exist).
- [ ] **Step 3: Implement** `legal.go` with `//go:embed LICENSE THIRD-PARTY-NOTICES licenses`. Fill the license texts from upstream: htmx `LICENSE` (0BSD), Alpine `LICENSE.md` (MIT), Tailwind `LICENSE` (MIT), Lucide `LICENSE` (ISC; includes the Feather MIT part), the three font `OFL.txt` / `LICENSE.txt` files from the exact releases in spec 3.5, Go `LICENSE` (BSD-3-Clause, go.dev), `x/crypto` and `godbus` `LICENSE` from the module cache (`go env GOMODCACHE`). Go component version: `runtime.Version()`; module versions via `ModuleVersion`. `THIRD-PARTY-NOTICES`: one section per component: name, version, SPDX, URL, covered files with sha256 (filled in Task 2 for asset files), then the full license text.
- [ ] **Step 4: Run** `cd webui && go test ./internal/legal/ && cd .. && scripts/check-license-headers.sh` — expected: PASS, no file printed.
- [ ] **Step 5: Commit** (owner): `feat(webui): add legal package with license texts`.

### Task 2: Embedded assets — vendor updates, fonts, icon sprite, Tailwind bump

**Files:**
- Modify: `webui/internal/web/static/vendor/htmx.min.js` (2.0.11, `dist/htmx.min.js` from npm `htmx.org`), `webui/internal/web/static/vendor/alpine.min.js` (3.17.5, `dist/cdn.min.js` from npm `alpinejs`)
- Create: `webui/internal/web/static/fonts/BebasNeue-Regular.woff2`, `InterVariable.woff2`, `GeistMono-wght.woff2` (sources in spec 3.5)
- Create: `webui/scripts/icons.sh`, `webui/assets/icons.txt`, `webui/internal/web/static/icons.svg`
- Modify: `builder/Dockerfile` (`TAILWIND_VERSION=v4.3.3`, `TAILWIND_SHA256_X64`, `TAILWIND_SHA256_ARM64` from the release's `sha256sums.txt`)
- Modify: `webui/internal/legal/legal.go` (`Files` of each asset component), `webui/internal/legal/THIRD-PARTY-NOTICES` (sha256 per file)
- Modify: `scripts/check-license-headers.sh` (exclude `static/fonts/`, `static/icons.svg`)
- Test: `webui/internal/web/assets_test.go`

**Interfaces:**
- Consumes: `legal.Components` (Task 1).
- Produces: `/static/icons.svg` with `<symbol id="<lucide-name>" viewBox="0 0 24 24">` per name in `icons.txt`; `/static/fonts/*.woff2`. `webui/scripts/icons.sh <lucide-static-dir> <icons.txt> <out.svg>` builds the sprite from `<dir>/icons/<name>.svg`, keeping the inner path data unchanged and setting `fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"` on the symbol.

- [ ] **Step 1: Write the failing tests** in `assets_test.go`:
  - `TestEveryEmbeddedThirdPartyFileIsListed`: walk `fs.Sub(assets, "static")`; every file under `vendor/`, `fonts/` and `icons.svg` must appear in some `legal.Components[i].Files`; and every listed file must exist.
  - `TestNoExternalResources`: for every embedded file under `templates/` and `static/` (except `vendor/` and fonts): no match of `(?i)(src|href)\s*=\s*["']https?://` inside `<script` / `<link` tags, and no `url\(\s*["']?https?://`. `<a href="https://…">` is allowed.
- [ ] **Step 2: Run** `cd webui && go test ./internal/web/ -run 'TestEveryEmbedded|TestNoExternal'` — expected: FAIL (fonts and sprite missing from the list).
- [ ] **Step 3: Implement**: put the files in place unchanged (check each download against the release; record sha256 with `sha256sum`). `icons.txt` starts with the names the plan uses: `layout-dashboard gamepad-2 upload usb cpu joystick server settings info menu x play pencil image trash-2 ellipsis search hard-drive check triangle-alert circle-alert folder-open refresh-cw power rotate-ccw download file-up bluetooth chevron-left arrow-up-right external-link loader-circle`. Run `icons.sh` (Bash 5, `set -euo pipefail`, shellcheck clean, usage comment, fails with a clear message when an icon file is missing). Update the Dockerfile pins.
- [ ] **Step 4: Run** `cd webui && go test ./internal/web/ ./internal/legal/ && cd .. && shellcheck -x webui/scripts/icons.sh && scripts/check-license-headers.sh` — expected: PASS, nothing printed. Then `./build.sh test` once (new Tailwind in the container) — expected: green.
- [ ] **Step 5: Commit** (owner): `feat(webui): embed fonts, lucide sprite, update htmx/alpine/tailwind`.

### Task 3: Design system — tokens, components, `icon` and `pct`, nav icons

**Files:**
- Modify: `webui/assets/input.css`
- Modify: `webui/internal/web/render.go` (funcs `icon`, `pct`; `navItem.Icon` holds Lucide names)
- Test: `webui/internal/web/design_test.go`

**Interfaces:**
- Produces:
  - template func `icon(name string) (template.HTML, error)` → `<svg class="icon" aria-hidden="true"><use href="/static/icons.svg#NAME"></use></svg>`; returns an error for a name that is not in the embedded sprite (`iconNames` set, parsed once from `static/icons.svg`).
  - template func `pct(used, total uint64) int` → 0 when `total == 0`, else `used*100/total` capped at 100.
  - CSS classes of spec 3.3: `.btn .btn-sm .btn-primary .btn-danger .btn-icon .panel .panel-head .chip-num .label-mono .list .row .input .select .check .radio .dropzone .badge .badge-ok .badge-warn .badge-danger .banner .banner-warn .banner-danger .banner-info .bar .menu .tabs .empty .icon`, plus `@font-face` for the three fonts (`font-display: swap`) and a `prefers-reduced-motion` rule.
- [ ] **Step 1: Write the failing tests** in `design_test.go`:
  - `TestPct`: table `{0,0→0}, {1,0→0}, {50,200→25}, {300,200→100}, {200,200→100}`.
  - `TestIconUnknownFails`: executing `{{icon "no-such-icon"}}` with `funcs` returns an error; `{{icon "play"}}` contains `href="/static/icons.svg#play"` and `aria-hidden="true"`.
  - `TestEveryTemplateIconExists`: regex `icon "([a-z0-9-]+)"` over all embedded templates plus every `navItems[i].Icon`; each name must be in the sprite.
  - `TestTokenContrast`: read `--color-<name>: #xxxxxx` from `../../assets/input.css`; WCAG contrast ≥ 4.5 for `fg/bg muted/bg dim/bg accent-fg/bg ok/bg warn/bg bg/fg` and for `fg/raised muted/raised dim/raised`.
- [ ] **Step 2: Run** `cd webui && go test ./internal/web/ -run 'TestPct|TestIcon|TestEveryTemplateIcon|TestTokenContrast'` — expected: FAIL.
- [ ] **Step 3: Implement** the funcs in `render.go`, the tokens/components/@font-face in `input.css` (font paths `/static/fonts/…`), and set nav icons: dashboard `layout-dashboard`, games `gamepad-2`, upload `upload`, usb `usb`, bios `cpu`, controllers `joystick`, system `server`, settings `settings`. Rename the label "Controller" to "Controllers" (spec 4.1 list).
- [ ] **Step 4: Run** `tailwindcss -i assets/input.css -o internal/web/static/app.css --minify && go test -race ./...` in `webui` — expected: PASS (existing tests still pass; pages still use old markup).
- [ ] **Step 5: Commit** (owner): `feat(webui): noir grid tokens, components and icon helper`.

### Task 4: Layout — sidebar, top bar, phone menu, toasts, dialog, legal line

**Files:**
- Modify: `webui/internal/web/templates/layout.html`, `webui/internal/web/render.go` (`Page` fields), `webui/internal/web/static/app.js` (danger flag for the confirm dialog only)
- Modify: `webui/internal/web/footer_test.go`
- Test: `webui/internal/web/layout_test.go`

**Interfaces:**
- Consumes: `icon`, CSS classes (Task 3).
- Produces:
  - `Page.Crumb []string` (for example `["02", "GAMES", "SNES"]`), `Page.Section string` (nav key that is active).
  - `func navKeyFor(page string) string` — `games_system→games`, `upload_review→upload`, `job→""`, else the page name itself.
  - Template blocks with defaults in `layout.html`: `{{block "subtitle" .}}{{end}}`, `{{block "actions" .}}{{end}}`, so old custom page templates without them still render.
  - Nav gains entry `about` (Task 5 adds the route; until then the link may 404 in manual testing only).
  - The `ask-confirm` event detail gets `danger bool`, set by `app.js` when `d.elt` (or its form's submit button) has class `btn-danger` or attribute `data-danger`.
- [ ] **Step 1: Write the failing tests** in `layout_test.go` (use `newEnv`):
  - `TestEveryPageHasLegalLinksAndSkipLink`: for `/ /games /upload /usb /bios /controllers /system /settings`: body contains `href="/about"`, `href="#content"` ("Skip to content"), `id="content"`, `https://www.gnu.org/licenses/agpl-3.0.html`.
  - `TestPhoneMenuMarkup`: `/` contains a button with `aria-controls="mobile-menu"` and `:aria-expanded`, an element `id="mobile-menu"` with `@keydown.escape`.
  - `TestNavKeyFor`: table for the four cases above.
  - `TestBreadcrumbAndActiveNav`: `/games/<sys>` (use the games fixture system of `games_test.go`) contains `02` and `GAMES` in the crumb and `aria-current="page"` on the Games entry.
  - `TestCustomPageWithoutNewBlocksRenders`: write a custom `templates/dashboard.html` with only `{{define "content"}}x{{end}}` into the env's UI dir, reload, `GET /` = 200 and contains the new sidebar.
  - In `footer_test.go`: change `footerOf` to read the legal line by `id="legal"` (present in sidebar and page footer); keep the three existing assertions.
- [ ] **Step 2: Run** `cd webui && go test ./internal/web/ -run 'TestEveryPage|TestPhoneMenu|TestNavKeyFor|TestBreadcrumb|TestCustomPageWithout|TestFooter'` — expected: FAIL.
- [ ] **Step 3: Implement** `layout.html` per spec 4 (sidebar 220 px, logo, numbered nav `printf "%02d"`, legal line `id="legal"` in sidebar and page footer, top bar with crumb and `actions` block, cert `.banner-warn`, page head with Bebas title and `subtitle` block, phone top bar + full-screen menu (focus trap: a small `keydown.tab` handler in `app.js`, because the Alpine focus plugin is not embedded; scroll lock via a class on `body`), toasts, confirm dialog using `dialog.danger` to switch `.btn-danger`/`.btn-primary`, `<link rel="preload" as="font" type="font/woff2" crossorigin>` for Inter and Geist Mono). In `app.js`, set `aria-busy="true"` on the element of an htmx request (`htmx:beforeRequest`) and remove it on `htmx:afterRequest`. Remove the bottom tab bar.
- [ ] **Step 4: Run** `cd webui && go test -race ./...` — expected: PASS.
- [ ] **Step 5: Commit** (owner): `feat(webui): noir grid layout and phone menu`.

### Task 5: About & licenses page

**Files:**
- Create: `webui/internal/web/handlers_about.go`, `webui/internal/web/templates/about.html`, `webui/internal/web/about_test.go`
- Modify: `webui/internal/web/server.go` (`s.aboutRoutes(mux)`), `webui/internal/web/render.go` (nav entry `{Key: "about", Label: "About & licenses", Href: "/about", Icon: "info"}`)

**Interfaces:**
- Consumes: `legal.Components`, `legal.LicenseText`, `legal.ComponentLicense` (Task 1); `Page.SourceURL` (exists).
- Produces: `GET /about` → page `about`, data `aboutView{ License string; Components []aboutRow }`, `aboutRow{ legal.Component; Text string }`.
- [ ] **Step 1: Write the failing tests** in `about_test.go`:
  - `TestAboutShowsLegalNotices`: `GET /about` = 200; contains "Copyright (C) 2026  Marcel Joachim Kloubert", "ABSOLUTELY NO WARRANTY", "GNU AFFERO GENERAL PUBLIC LICENSE", `href="https://github.com/mkloubert/es-builder"` (with `cfg.SourceURL` set), and every `legal.Components[i].Name` and SPDX id.
  - `TestAboutWithoutSourceURL`: for `SourceURL` in `"", "javascript:alert(1)", "ftp://x/y"`: 200, contains "No source code link is set. Set SOURCE_URL in build.conf.", does not contain `javascript` or `ftp:`.
  - `TestAboutNeedsAuth`: request without credentials = 401.
  - `TestAboutIsGetOnly`: `POST /about` = 405.
- [ ] **Step 2: Run** `cd webui && go test ./internal/web/ -run TestAbout` — expected: FAIL (404).
- [ ] **Step 3: Implement** the handler and template per spec 5.1 (license text and each component text inside `<details><summary>…</summary><pre class="…">…</pre></details>`, html/template escapes them).
- [ ] **Step 4: Run** `cd webui && go test -race ./...` — expected: PASS.
- [ ] **Step 5: Commit** (owner): `feat(webui): about and licenses page`.

### Task 6: Dashboard and games pages

**Files:**
- Modify: `webui/internal/web/templates/partials/status.html`, `templates/dashboard.html`, `templates/games.html`, `templates/games_system.html`, `templates/partials/games_shared.html`, `templates/partials/flash.html`
- Test: `webui/internal/web/redesign_pages_test.go`

**Interfaces:**
- Consumes: CSS classes, `icon`, `pct` (Task 3); blocks `subtitle`, `actions` (Task 4).
- [ ] **Step 1: Write the failing tests** in `redesign_pages_test.go`:
  - `TestDashboardPanels`: `/` contains `panel-head`, "Status", "Device", `class="bar` (RAM and storage), and still `id="status"` with `hx-trigger="every 5s`.
  - `TestGameRowsUseMenu`: `/games/<sys>` with a game whose display name is 200 chars: the row contains `truncate`, `title="<name>"`, a "Start" button, a menu button with `aria-haspopup="menu"` and `aria-expanded`, and the menu holds "Rename", "Scrape", "Delete"; `hx-post="/games/<sys>/delete"` keeps its `hx-confirm`.
  - `TestGamesGridAndSegmented`: `/games` and `/games?all=1` both contain the two segment links `href="/games"` and `href="/games?all=1"`, with `aria-current` on the active one.
- [ ] **Step 2: Run** `cd webui && go test ./internal/web/ -run 'TestDashboardPanels|TestGameRows|TestGamesGrid'` — expected: FAIL.
- [ ] **Step 3: Implement** the templates per spec 5 table (rows: 3-digit index via `printf "%03d"`, menu with `.menu`, Delete marked `btn-danger` / `data-danger`; "Scrape all" in the `actions` block; changes banner and force dialog in `.banner-danger` / dialog style; `.empty` states).
- [ ] **Step 4: Run** `cd webui && go test -race ./...` — expected: PASS (including the old `games_test.go`).
- [ ] **Step 5: Commit** (owner): `feat(webui): noir grid dashboard and games`.

### Task 7: Upload, upload review, job and USB pages

**Files:**
- Modify: `templates/upload.html`, `templates/upload_review.html`, `templates/job.html`, `templates/usb.html` (all in `webui/internal/web/`), `static/app.js` only if class names in `uploader()` / `jobView()` change (state logic stays)
- Test: extend `webui/internal/web/redesign_pages_test.go`
- [ ] **Step 1: Write the failing tests**:
  - `TestUploadDropzone`: `/upload` contains `dropzone`, "Drop ROMs or ZIP archives here", "Choose files", `x-data="uploader()"`.
  - `TestJobPageLook`: a finished job page contains `font-display` (big percent), `class="bar`, `role="progressbar"`, the log `<pre` with `x-ref="log"`.
  - `TestUSBRows`: with the lsblk fixture: each stick row has `badge` and the Import button `btn-primary`.
- [ ] **Step 2: Run** `cd webui && go test ./internal/web/ -run 'TestUploadDropzone|TestJobPageLook|TestUSBRows'` — expected: FAIL.
- [ ] **Step 3: Implement** per spec 5 table.
- [ ] **Step 4: Run** `cd webui && go test -race ./...` — expected: PASS (including `upload_test.go`, `usb_test.go`).
- [ ] **Step 5: Commit** (owner): `feat(webui): noir grid upload, job and usb`.

### Task 8: BIOS, controllers, system and settings pages

**Files:**
- Modify: `templates/bios.html`, `templates/controllers.html`, `templates/partials/bluetooth.html`, `templates/system.html`, `templates/partials/system_shared.html`, `templates/partials/custom_ui.html`, `templates/partials/confirm.html`, `templates/settings.html` (all in `webui/internal/web/`)
- Test: extend `webui/internal/web/redesign_pages_test.go`
- [ ] **Step 1: Write the failing tests**:
  - `TestBiosBadges`: `/bios` shows `badge-ok` for a present file and `badge-warn` with "MISSING" for a missing one (use the fixture of `TestBiosPageShowsStates`).
  - `TestSystemTabs`: `/system` contains `class="tabs` and `role="tab"` six times (Power, Updates, CPU, Logs, Bans, Web UI).
  - `TestSettingsPanels`: `/settings` contains `panel-head` twice and `label-mono`.
  - `TestNoOldLookLeft`: no embedded template contains `rounded-xl`, `rounded-lg` or `bg-zinc-900` (spec 1.1: no page keeps the old look).
- [ ] **Step 2: Run** `cd webui && go test ./internal/web/ -run 'TestBiosBadges|TestSystemTabs|TestSettingsPanels|TestNoOldLookLeft'` — expected: FAIL.
- [ ] **Step 3: Implement** per spec 5 table.
- [ ] **Step 4: Run** `cd webui && gofmt -l . ; go vet ./... && go test -race -count=1 ./...` — expected: no gofmt output, PASS (all old page tests too).
- [ ] **Step 5: Commit** (owner): `feat(webui): noir grid bios, controllers, system and settings`.

### Task 9: Docs, image smoke test, hardware checklist

**Files:**
- Modify: `README.md` (new section "About & licenses"; "Custom web UI": custom templates from before the redesign must be adapted; "License": name `webui/internal/legal/THIRD-PARTY-NOTICES`)
- Modify: `webui-custom/README.md` (same note on adapting templates)
- Modify: `test/vm-smoke.sh` (check `GET /about` = 200 and body contains "ABSOLUTELY NO WARRANTY", same curl options as the existing web checks around line 486)
- Modify: `docs/hardware-checklist.md` (phone: menu opens/closes and Escape works with a keyboard, "…" menus, dropzone, fonts render as Bebas/Inter/Geist and not as fallback)
- [ ] **Step 1: Write the change to `test/vm-smoke.sh`**; run `shellcheck -x test/vm-smoke.sh` — expected: clean.
- [ ] **Step 2: Run** `./build.sh test` — expected: green (shellcheck, license headers, bats 0 `not ok`, go vet, go test -race).
- [ ] **Step 3: Report** what was not run: a real image build (`./build.sh vm-amd64`), `test/vm-smoke.sh` on that image, and the hardware checklist on a phone.
- [ ] **Step 4: Commit** (owner): `docs: web ui redesign and licenses`.
