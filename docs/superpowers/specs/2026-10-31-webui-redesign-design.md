# EmuStation Builder – Web UI redesign "Noir Grid" – Design

License: AGPL-3.0-or-later, see LICENSE

As of: 2026-10-31 · Status: draft for approval

This spec adds to the main spec `2026-10-27-emulationstation-image-design.md` (web UI part) and
to `2026-10-31-webui-custom-design.md`. Where they disagree, this spec wins.

## 1. Goal and success criteria

The web UI gets a modern, calm and consistent look. It stays dark only, server-side HTML with
Tailwind, htmx and Alpine. The only new function is a page "About & licenses" that closes two
gaps in license compliance.

1. Every page uses the "Noir Grid" design system (section 3). No page keeps the old look.
2. Routes, forms, handlers, htmx requests and texts of actions keep their behaviour. Only
   templates, CSS, `app.js` (as far as the look needs it), embedded assets and the About page change.
3. The web UI loads no file from the internet. Fonts, icons, CSS and JS are embedded in the binary.
4. Every embedded third-party file is listed with its full license text in
   `THIRD-PARTY-NOTICES` and on the About page. A test makes the build fail if one is missing.
5. Every page links to the AGPL legal notices: copyright, no warranty, license text and the
   source code link (AGPL-3.0 section 13, and "Appropriate Legal Notices", sections 0 and 5d).
6. The UI works with keyboard only and on a phone (from 360 px width). Text contrast is at
   least WCAG 2.2 AA.

Not in scope: new data on pages (for example game counts on the dashboard), a light mode,
changes to `priv`, ports, config keys or the boot file.

## 2. Decisions

| Topic | Decision | Reason |
|---|---|---|
| Style | "Noir Grid": the sharp line grid, numbered sections and mono labels of craft-launcher, with the large Bebas Neue titles and red accent of EpicNoir | owner chose option A of 3 mockups |
| Game rows | one "Start" button plus a "…" menu (Rename, Scrape, Delete) | owner chose option B; calmer rows |
| Phone navigation | top bar with logo and ☰; the button opens a full-screen menu with the same numbered list | owner chose option B; 9 entries do not fit a tab bar |
| CSS structure | Tailwind v4 `@theme` tokens + a small `@layer components` set + Go partials for repeated blocks | owner chose approach 1 of 3 |
| Icons | Lucide (ISC), only the used icons, as one SVG sprite `static/icons.svg` | same 24 px grid and 2 px stroke as today; small |
| Fonts | Bebas Neue (titles, big numbers), Inter (text), Geist Mono (labels, buttons, badges), all OFL-1.1 | free, embeddable, close to both references |
| EpicNoir | only the mood is used. No file, image or font of EpicNoir is copied | its license CC BY-NC-SA is not compatible with our code |
| Vendor updates | htmx 2.0.4 → 2.0.11, Alpine 3.14.8 → 3.17.5, Tailwind CLI v4.1.11 → v4.3.3 | current releases; same major versions |

Ruling: no live ES status in the top bar — it needs new data on every page, which is more than a
new look — cost if wrong: the status is only visible on the dashboard.

Ruling: the dashboard has no row of totals (games, systems, BIOS), because this data does not
exist today — cost if wrong: the dashboard shows a bit less.

Ruling: the license files live in a new package `webui/internal/legal/`, because `go:embed`
cannot read files above the package folder, and `web` then only imports a small, testable list.
A Go test keeps `internal/legal/LICENSE` equal to the repo `LICENSE` — cost if wrong: one more
copy of a file in the repo and one more package.

Ruling: fonts are embedded exactly as the upstream release ships them (woff2, not subset, not
converted). The three licenses name no Reserved Font Name, but not changing the files avoids the
question for good — cost if wrong: about 450 KB more in the binary (gzip does not help woff2).

Ruling: custom templates in `webui-custom/` are not migrated. The README says that they must be
adapted to the new markup — cost if wrong: an owner with custom templates sees the old look
mixed with the new one until they adapt them.

Ruling: input and select borders use a new token `line-field` (`#636369`, 3.3:1 on `bg`) and not
`dim`, so that fields stay quieter than text; checkboxes and radios use `dim`, because they are
small — cost if wrong: one more token.

Ruling: the Vue license text comes from the root `LICENSE` of vuejs/core v3.5.41 ("Yuxi (Evan)
You and Vue contributors"), which matches the copyright line in the Alpine bundle — cost if wrong:
the package `LICENSE` files of `@vue/shared` and `@vue/reactivity` omit "and Vue contributors".

Ruling: on the upload review page the "Need a decision" panel keeps its warn look with a
`badge-warn` "Decide" badge, because the number chip replaces the "!" chip — cost if wrong: one
more badge text.

## 3. Design system

### 3.1 Tokens (`webui/assets/input.css`, `@theme`)

| Token | Value | Use |
|---|---|---|
| `--color-bg` | `#0a0a0b` | page |
| `--color-surface` | `#111113` | hover rows, log, inputs |
| `--color-raised` | `#141416` | active nav entry, menus, dialogs |
| `--color-line` | `#232326` | grid lines, dividers |
| `--color-line-strong` | `#2c2c30` | borders of controls |
| `--color-line-field` | `#636369` | borders of `.input` and `.select` (3:1 on `bg`, `surface`, `raised`) |
| `--color-fg` | `#ededed` | main text |
| `--color-muted` | `#a1a1a6` | secondary text |
| `--color-dim` | `#86868c` | labels, hints |
| `--color-accent` | `#e5484d` | active marker, primary danger, focus ring, number chips |
| `--color-accent-fg` | `#ff6b6e` | red text on dark |
| `--color-ok` | `#4ade80` | ok text; border `#1f4d33`, fill `#0d1f15` |
| `--color-warn` | `#fcd34d` | warn text; border `#5a3a12`, fill `#1a1206` |
| `--font-display` | `"Bebas Neue"` | page titles, big numbers |
| `--font-sans` | `"Inter", ui-sans-serif, system-ui, …` | text |
| `--font-mono` | `"Geist Mono", ui-monospace, …` | labels, buttons, badges, numbers, log |

Radius is 0 everywhere. Only status dots are round. There are no shadows except on menus,
dialogs and toasts. The final hex values may move a little during implementation if a contrast
check fails; the contrast test in section 6 decides.

### 3.2 Typography

- Page title: Bebas Neue, about 56 px desktop and 40 px phone, line height 0.9.
- Labels, buttons, badges, nav numbers: Geist Mono, 10–11 px, uppercase, letter spacing 0.12–0.14em.
- Text and names: Inter 13–14 px. Values use `tabular-nums`.

### 3.3 Components (`@layer components`)

| Class | Look |
|---|---|
| `.btn` | mono label, 1 px `line-strong` border, height 38 px (32 px with `.btn-sm`) |
| `.btn-primary` | filled `fg`, text `bg` |
| `.btn-danger` | border dark red, text `accent-fg`; in the confirm dialog of a delete: filled `accent` |
| `.btn-icon` | square 32 px, needs `aria-label` |
| `.panel` | 1 px `line` border, no fill |
| `.panel-head` | number chip ("01", filled `accent`) + mono label left, mono info right. One partial `panel-head` (`{{template "panel-head" (panelHead "01" "Title" "id:…" "info:…")}}`) writes it on every page; the label is an `h2`. In a `<summary class="panel-head">` the partial `panel-head-inner` writes the same inner markup. |
| `.list`, `.row` | rows with `line` dividers, hover `surface` |
| `.input`, `.select`, `.check`, `.radio` | square, focus border `accent`. Border: `line-field` (`#636369`) for `.input`/`.select`, `dim` for `.check`/`.radio`, so that each border reaches 3:1 (WCAG 1.4.11) |
| `.dropzone` | dashed `line-strong` border; while dragging: `accent` border and a light red fill |
| `.badge` (`-ok`, `-warn`, `-danger`, neutral) | mono 9–10 px, 1 px border, light fill |
| `.banner` (`-warn`, `-danger`, `-info`) | full width box with text and optional buttons |
| `.bar` | 3 px track `line`, fill `accent` (progress: `ok`; failed: `accent`) |
| `.menu` | Alpine dropdown: `raised`, opens on click, arrow keys move, Escape and click outside close, focus returns to the button |
| `.tabs` | mono tab bar, active tab with 2 px `accent` underline (an inset shadow, because `overflow-x` clips a negative margin), scrolls sideways on a phone |
| `.empty` | dashed box with icon, one line of text and one action |

All interactive elements have a visible focus ring (2 px `accent`). While an htmx request runs,
the button is dimmed and `aria-busy="true"`. Only the requesting button or form is marked; a region that polls (for example `#status`) is not, so its buttons stay usable. Transitions are 150 ms or less and are switched off
with `prefers-reduced-motion: reduce`.

### 3.4 Icons

- Source: `lucide-static` 1.66.0 (ISC). Only the used icons go into `static/icons.svg` as
  `<symbol id="<lucide name>">`. The symbols keep the upstream path data unchanged.
- Template function `icon "<name>"` → `<svg class="icon" aria-hidden="true"><use href="/static/icons.svg#<name>"></use></svg>`.
  An unknown name makes template execution fail in tests (see 6).
- `render.go` nav entries get Lucide names instead of path strings.
- A script `webui/scripts/icons.sh <lucide-static dir> <names…>` writes the sprite, so the
  owner can add icons later. The list of names lives in `webui/assets/icons.txt`.

### 3.5 Fonts

- Files in `webui/internal/web/static/fonts/`, unchanged from upstream:
  - `BebasNeue-Regular.woff2` from github.com/dharmatype/Bebas-Neue (`fonts/BebasNeue(2018)ByDhamraType/woff2-cffbased/`), OFL-1.1, © 2010 Dharma Type
  - `InterVariable.woff2` from Inter v4.1 (`web/`), OFL-1.1, © 2016 The Inter Project Authors
  - `GeistMono[wght].woff2` from geist-font v1.9.0 (`GeistMono/webfonts/`), stored as
    `GeistMono-wght.woff2` (only the file name changes), OFL-1.1, © 2024 The Geist Project Authors
- `@font-face` with `font-display: swap`. The layout preloads Inter and Geist Mono.
- The sha256 of every font and vendor file is listed in `THIRD-PARTY-NOTICES`.

## 4. Layout and navigation (`templates/layout.html`)

### 4.1 Desktop (from `md`)

- Fixed sidebar, 220 px, right border `line`.
  - Top: logo "EMU**STATION**" in Bebas Neue, "STATION" in `accent`.
  - Nav: entries 01–09 (Dashboard, Games, Upload, USB, BIOS, Controllers, System, Settings,
    About & licenses) with icon, label and the number in mono on the right. The active entry has
    a 2 px `accent` left border and `raised` fill; `aria-current="page"` stays.
  - Bottom: "EmuStation Builder · AGPL-3.0 · Source code · About & licenses" in small mono.
- Top bar, 56 px, bottom border `line`: breadcrumb in mono (for example `02 / GAMES / SNES`)
  on the left, page actions on the right (a template block `actions`, empty by default).
- The certificate warning is a `.banner-warn` right under the top bar, as today on every page.
- Content: max width `6xl`, page head = Bebas title + one line of subtitle (template block
  `subtitle`, optional).

### 4.2 Phone (below `md`)

- Top bar: logo left, ☰ button right (`aria-expanded`, `aria-controls`).
- The button opens a full-screen menu with the same numbered list and the legal links at the
  bottom. It traps focus, closes with Escape and ✕, locks page scroll and returns focus to ☰.
- The legal line is also shown at the bottom of every page.
- The old bottom tab bar is removed. Safe area paddings stay.

### 4.3 Global

- "Skip to content" link as the first focusable element.
- Toasts: bottom right on desktop, bottom center on a phone; square, with a 2 px accent bar on
  the left; same timing as today.
- Confirm dialog: square, title "ARE YOU SURE?" in mono. The confirm button uses `.btn-danger`
  filled when the request comes from a delete action (`hx-confirm` on a `.btn-danger` or an
  element with `data-danger`), else `.btn-primary`. The behaviour of `app.js` stays.

## 5. Pages

Content and functions stay the same. Only the look changes.

| Page | Look |
|---|---|
| Dashboard (`status` partial) | panel "01 · Status": status dot with glow, large state text, game and system, buttons (Stop game = `.btn-danger`). Panel "02 · Device": key/value list; RAM and storage get a `.bar` (template func `pct used total`). Polling every 5 s stays. |
| Games | grid of cells with shared `line` borders (1 col phone, 2 `sm`, 3 `lg`); each cell: number chip, system name, game count in mono. "With games / All systems" becomes a two-part segmented control (still links). |
| Games of a system | breadcrumb back link, title = system name, count in mono; search + "Scrape all" (top bar actions on desktop); list rows: index (3 digits, mono), display name, file name (mono, small), tags RENAMED / WILL BE DELETED, "Start" `.btn-sm` and "…" menu with Rename, Scrape, Delete…. Rename stays inline in the row. Changes banner and force dialog in the new style. |
| Upload | large `.dropzone` with icon, two lines of text and "Choose files" (`.btn-primary`); one `.row` per file with name, state text and a thin `.bar`; the "continue" box becomes `.banner-warn`. |
| Upload review | one `.panel` per upload with `.row`s: checkbox, name input, target. |
| Job | progress in Bebas Neue (large percent), `.bar`, status text; log as a terminal box (`surface`, mono 12 px, auto scroll as today); link buttons below. |
| USB | one `.row` per stick with badges (size, file system) and Import (`.btn-primary`) / Eject (`.btn`). |
| BIOS | panel "01 · Upload" (file + target select); panel "02 · Files": rows with `.badge-ok` OK or `.badge-warn` MISSING. |
| Controllers | numbered panels (templates, import, Bluetooth); Bluetooth devices as rows with Pair / Remove. |
| System | `.tabs` for Power, Updates, CPU, Logs, Bans, Web UI; content in panels. |
| Settings | forms in panels; mono labels above inputs, small help text below. |
| About & licenses | see 5.1 |
| Empty states | `.empty` everywhere (for example "No games yet." + "Upload ROMs"). |

### 5.1 About & licenses (`GET /about`, new)

- Handler `handlers_about.go`, template `about.html`, nav entry 09 (`about`, icon `info`).
  Same auth as every other page. GET only, no state.
- Content, in this order:
  1. "EmuStation Builder", one line what it is.
  2. "Copyright (C) 2026  Marcel Joachim Kloubert".
  3. No warranty: "This program comes with ABSOLUTELY NO WARRANTY. It is free software, and you
     are welcome to redistribute it under the terms of the GNU Affero General Public License,
     version 3 or later."
  4. Source code: link to `SOURCE_URL`. If it is empty: "No source code link is set. Set
     SOURCE_URL in build.conf." (AGPL-3.0 section 13 applies to changed versions.)
  5. "License text": a `<details>` with the full text of `internal/legal/LICENSE` in a `<pre>`.
  6. "Third-party software": a table (name, version, license, link). Each row opens a
     `<details>` with the full license text of that component. Name, version, license and the
     link are visible without opening anything; the `<details>` holds only the license text.
- Data: a Go slice `legal.Components` (name, version, SPDX id, URL, embedded files it covers,
  license text file) in a new package `internal/legal` with `//go:embed LICENSE THIRD-PARTY-NOTICES licenses/*`.
  `THIRD-PARTY-NOTICES` is the plain text form of the same list for people who read the repo.

### 5.2 Components to list

| Component | Version | License | Embedded as |
|---|---|---|---|
| htmx | 2.0.11 | 0BSD | `static/vendor/htmx.min.js` |
| Alpine.js | 3.17.5 | MIT | `static/vendor/alpine.min.js` |
| Vue reactivity (bundled in Alpine.js) | 3.5.41 | MIT | `static/vendor/alpine.min.js` (`@vue/shared` and `@vue/reactivity`) |
| Tailwind CSS | 4.3.3 | MIT | generated `static/app.css` |
| Lucide | 1.66.0 | ISC | `static/icons.svg` |
| Bebas Neue | 2.000 (2018) | OFL-1.1 | `static/fonts/BebasNeue-Regular.woff2` |
| Inter | 4.1 | OFL-1.1 | `static/fonts/InterVariable.woff2` |
| Geist Mono | 1.9.0 | OFL-1.1 | `static/fonts/GeistMono-wght.woff2` |
| Go (standard library and runtime) | from `builder/Dockerfile` `GO_VERSION` | BSD-3-Clause | compiled into the binary |
| golang.org/x/crypto | from `go.mod` | BSD-3-Clause | compiled into the binary |
| github.com/godbus/dbus/v5 | from `go.mod` | BSD-2-Clause | compiled into the binary |

The Go version shown on the page comes from `runtime.Version()`. Module versions come from
`runtime/debug.ReadBuildInfo()`, with the `go.mod` value as the fallback in tests.

## 6. Tests

- `internal/legal`:
  - every component has a non-empty license text, an SPDX id from the allow list (CLAUDE.md
    rule 8: MIT, BSD-2/3-Clause, ISC, 0BSD, Apache-2.0, OFL-1.1, GPL-3.0, AGPL-3.0) and a URL;
  - `internal/legal/LICENSE` equals the repo `LICENSE` (`../../../LICENSE`) byte for byte;
  - every module in `go.mod` `require` has a component.
- `internal/web`:
  - every file under `static/vendor/`, `static/fonts/` and `static/icons.svg` is covered by a
    component (table test that walks the embedded FS);
  - every `icon "<name>"` in the embedded templates exists as a `<symbol id>` in `icons.svg`;
  - every page renders with test data and contains the link to `/about` and the source link;
  - `/about` contains the copyright line, "ABSOLUTELY NO WARRANTY", the source link (and the
    hint when `SOURCE_URL` is empty) and every component name;
  - `pct` table test (0 total, normal, over 100 %);
  - no template or static file contains `http://` or `https://` URLs to load resources
    (`src=`, `href=` of `link`/`script`, `url(` in CSS); only `SOURCE_URL`, gnu.org and
    component homepages in `<a href>` are allowed;
  - contrast test: the token pairs `fg/bg`, `muted/bg`, `dim/bg`, `accent-fg/bg`, `ok/bg`,
    `warn/bg` and `bg/fg` (primary button) reach at least 4.5:1. The non-text pairs
    `line-field` and `dim` on `bg`, `surface` and `raised` reach at least 3:1. The test reads the
    hex values from `input.css`;
  - every vendor file's license blocks (`Bundled license information`, `@license`, `@preserve`)
    name only packages that are a component for that file; one file can be covered by more
    than one component;
  - the sha256 values in `THIRD-PARTY-NOTICES` equal the embedded files;
  - the `ModuleVersion` fallbacks in `legal.go` equal the versions in `go.mod`.
- `test/vm-smoke.sh`: `GET /about` returns 200 and contains "ABSOLUTELY NO WARRANTY".
- `test/lib/static-app.sh`: no change to the image content outside the web binary, so no new check.
- `docs/hardware-checklist.md`: on a real phone, check the menu, the "…" menus, the dropzone and
  the font look.
- `scripts/check-license-headers.sh`: `internal/legal/` (`THIRD-PARTY-NOTICES`, `LICENSE`, `licenses/*`),
  fonts, `icons.svg` and vendor files need no header (third-party or license text).

## 7. Build and docs

- `builder/Dockerfile`: `TAILWIND_VERSION=v4.3.3` with new sha256 values for x64 and arm64.
- Vendor files replaced by the minified upstream builds of htmx 2.0.11 and Alpine 3.17.5,
  unchanged.
- README: new section "About & licenses"; in "Custom web UI" a note that custom templates from
  before the redesign must be adapted; the "License" section names `THIRD-PARTY-NOTICES`.
- `webui-custom/README.md`: the same note.
