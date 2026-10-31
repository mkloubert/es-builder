# EmuStation Builder – Custom web UI files – Design

License: AGPL-3.0-or-later, see LICENSE

As of: 2026-10-31 · Status: draft for approval

This spec adds to the main spec `2026-10-27-emulationstation-image-design.md` (web UI part).
Where the two disagree, this spec wins.

## 1. Goal and success criteria

The owner can change the look and the texts of the web UI with own files outside the binary.
Every file that is not given comes from the binary, as now.

1. A file in the custom folder with the same path as a built-in file (for example
   `templates/dashboard.html` or `static/app.css`) is used instead of the built-in one.
2. A file that exists only in the custom folder (for example `templates/partials/my.html` or
   `static/logo.png`) is also available.
3. A missing or empty custom folder gives exactly the built-in web UI.
4. Custom templates that do not parse never stop the web UI. It runs with the built-in
   templates and says so on the System page.
5. The owner can put the files in the repo before the build, and can change them on the device
   over SSH later. A button on the System page loads changed templates without a restart.

## 2. Decisions

| Topic | Decision | Reason |
|---|---|---|
| Source at build time | repo folder `webui-custom/` (only `.gitkeep` in Git), same layout as `webui/internal/web/` (`templates/…`, `static/…`) | same idea as `controllers/custom/` |
| Place on the device | `/home/emulationstation/webui-custom/`, owned by `emulationstation` | the SSH user has no general sudo and must be able to edit it |
| Setting | new `web.conf` key `UI_DIR`, default `<HOME_DIR>/webui-custom`; an empty value turns the feature off | tests and a different place need no code change |
| Fallback | per file (overlay) | the owner changes one page without copying all of them |
| Templates | parsed at start and on the reload button | same speed as now |
| Static files | read on every request | no reload needed; browsers still cache them for one day |
| Tailwind | the build also scans `webui-custom/templates` | new classes in custom templates get CSS |

Ruling: the folder is owned by `emulationstation`, so the web service could also write it — the
SSH user must edit it without sudo, and the service already owns `web.conf` and the whole home,
so this gives it no new power — cost if wrong: a broken web process could change its own pages.

Ruling: no live file watcher — a button is simple and needs no new dependency — cost if wrong:
the owner clicks once after each template change.

## 3. Changes

### 3.1 Build (`builder/steps/40-provision-app.sh`, `webui/`)

- New repo folder `webui-custom/` with `.gitkeep` and a short `README.md` that shows the layout.
  Its other files are the owner's own and are not checked (license headers, tests).
- Step 40 copies `webui-custom/` (if it exists, without `.gitkeep` and `README.md`) into the
  provision folder. A file that is not a regular file or a folder (for example a symlink) stops
  the build with "webui-custom/<path> must be a regular file."
- Only `templates/**/*.html` and `static/**` are allowed. Another top-level entry stops the
  build with "webui-custom/<path>: only templates/ and static/ are allowed."
- The web build (`app_build_web`, temporary copy of `webui/`) puts `webui-custom/templates` into
  the copy as `custom/templates`, and `assets/input.css` gets `@source "../custom/templates";`.
  The folder always exists in the copy (it may be empty), so Tailwind never fails on it.

### 3.2 Image (`provision/common/app-30-webui.sh`)

- Copies the files to `/home/emulationstation/webui-custom/` (folders 0755, files 0644, owner
  `emulationstation`). It first removes the files that came from an older build, so the folder
  matches the repo after each build. The script is idempotent.
- Always creates the folder, also when it is empty, so the owner can add files over SSH.
- `web.conf` gets `UI_DIR=/home/emulationstation/webui-custom`.

Ruling: a new build replaces the device folder — the image is built new anyway, and the repo is
the source — cost if wrong: files added only on the device are lost after a new image; the
README says this.

### 3.3 Config (`webui/internal/config`)

- New field `UIDir string`. Default `<Home>/webui-custom` (set in `setHome`). `Load` reads
  `UI_DIR` (an empty value stays empty = off). `Save` writes it back. README documents it.

### 3.4 Overlay file system (`webui/internal/web`, new file `overlay.go`)

- `newOverlayFS(dir string, base fs.FS) fs.FS`. With `dir == ""` it returns `base`. A missing
  folder acts like an empty one: the folder is opened on every call, so it can appear later.
- `Open(name)`: first the custom folder through `os.Root` (no path can leave the folder, and a
  symlink that points outside fails). Only a regular file counts. If it is missing, `base`.
- `ReadDir` / `Glob`: the union of both, sorted, no duplicates. This makes new partials and new
  static files work.
- Names that start with `.` are ignored.

### 3.5 Templates and reload (`render.go`, `server.go`)

- `Server.tpl` becomes `atomic.Pointer[templates]`; the four readers use `s.tpl.Load()`.
- New `func (s *Server) loadTemplates() uiState`: parses with the overlay. If that fails, it
  logs `custom UI: <error>; using built-in templates` and parses the built-in files only. A
  failure of the built-in files stays a panic at start (build bug).
- `uiState{Dir string; Files int; Err string}`: the custom folder, the number of custom files in
  use, and the parse error for the System page. It is kept next to the templates.
- Static files: `http.FileServerFS` gets the overlay `static` folder. Custom files can seek
  (Range requests, content sniffing).
- The built-in set is always parsed and kept next to a custom set. If a custom set fails when a
  template runs (a page, a block or a partial), the error is logged and that one answer uses the
  built-in set. So a custom template never locks the owner out of the System page.

Ruling: fall back per answer at run time too — a custom layout or a System partial that fails
at run time would else hide the reload button, and the SSH user cannot restart the service —
cost if wrong: a broken custom page silently looks built-in; the journal has the error.

### 3.6 System page

- New card "Custom web UI":
  - off (`UI_DIR` empty or folder missing): "Custom web UI is off."
  - ok: "<n> custom files in use from <dir>."
  - error: "The custom templates have an error. The built-in web UI is in use." plus the first
    line of the parse error (it names the file and the line; it is the owner's own file, not
    helper text).
  - Button "Reload web UI" → `POST /system/ui-reload`. It calls `loadTemplates`, swaps the
    pointer, and answers with a toast "Web UI reloaded." or "Custom templates have an error.
    The built-in web UI is in use." and refreshes the card.
- The route uses the normal auth and CSRF protection, like the other System actions.

## 4. Tests

- `overlay_test.go` (table tests, temp dirs): override, fallback, file only in custom, union in
  `ReadDir`/`Glob`, `dir` empty, folder missing, `../x`, symlink to outside, symlink inside to a
  folder, dot files ignored.
- `render_test.go`: a custom `dashboard.html` is rendered; a broken custom template gives the
  built-in page and `uiState.Err != ""`; reload after a file change shows the new text; reload
  during requests (`-race`).
- `server_test.go`: `GET /static/x.css` from the custom folder; `POST /system/ui-reload` toast
  and card; the System page shows the three states.
- `config_test.go`: default, empty value, round trip through `Save`.
- `test/bats`: step 40 copy (ok, symlink → error, other top-level entry → error);
  `app-30-webui.sh` copies, sets owner and modes, removes old files, writes `UI_DIR`.
- `test/lib/static-app.sh`: the folder exists and is owned by `emulationstation`; `web.conf`
  has `UI_DIR`.
- `docs/hardware-checklist.md`: change a template over SSH, click "Reload web UI", see it.

## 5. Docs

- `README.md`: the folder, the layout, the fallback, the reload button, the `UI_DIR` key, the
  Tailwind note (new classes only at build time), and that a new image replaces the folder.
- `webui-custom/README.md`: a short copy of the layout part.

## 6. Risks

- A custom template can parse but fail when a page renders (for example a wrong field). That
  answer then uses the built-in templates (3.5), and the journal has the error. The owner fixes the
  file and clicks reload.
- Custom templates must use the data the handlers pass. A later update can change that data,
  and an old custom template can then break. The README says this.
- Browsers cache static files for one day. After changing a static file, the owner must reload
  the page without cache.
