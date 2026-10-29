# CLAUDE.md — EmuStation Builder

Bash builder that makes ready-to-boot EmulationStation images (Debian 13 "trixie" + RetroPie +
EpicNoir theme) for the **Raspberry Pi 4** and for **UEFI VMs (amd64/arm64)**. Each image contains a
Go web UI (`webui/`) on HTTPS port 11944 to manage ROMs, BIOS files, controllers and the system.
`README.md` is the user guide. The specs in `docs/superpowers/specs/` are the binding design.

## Hard rules (always)

1. **Git: read only.** Never run a git command that writes (`add`, `commit`, `stash`, `checkout`,
   `reset`, `restore`, `rebase`, `merge`, `tag`, `push`, `clean`, `rm`, `mv`, `worktree add`, …).
   Read-only commands like `status`, `diff`, `log` and `show` are allowed. The owner commits.
2. **Language.** Code, comments, log lines, UI texts and docs are written in **simple English**:
   short sentences and common words. Talk to the owner in German.
3. **License header.** Every own source file starts with the AGPL-3.0-or-later header. Copy it
   from an existing file of the same type, e.g. `build.sh`, a `.go` or an `.html` file. Do not change
   the text, and keep the two spaces in `Copyright (C) 2026  Marcel Joachim Kloubert`.
   `scripts/check-license-headers.sh` decides which files need the header. Markdown, JSON,
   go.mod/go.sum, `app.css`, fixtures and testdata do not.
4. **Ports.** Only TCP 11911 (SSH) and 11944 (web UI, HTTPS only) may be open, plus ping. Do not
   add ports, listeners or services that are reachable from outside.
5. **Names.** The user, home, units and paths use `emulationstation`. Never use "emulatorstation".
   The product name is "EmuStation Builder", and `SOURCE_URL` defaults to
   `https://github.com/mkloubert/es-builder`.
6. **Secrets.** Never write passwords into an image as plain text. The one exception is the
   ScreenScraper password, which is stored in `web.conf` (mode 0600) by design. The web password is
   stored only as a hash (`WEB_PASSWORD_HASH`). `builder/lib/webconf.sh` makes the build fail if the
   plain web password appears in `web.conf`. Never put `secrets/` (the CA key) into an image.
7. **Spec first.** For new features or changes to behaviour, change or add a spec in
   `docs/superpowers/specs/` first, then a plan in `docs/superpowers/plans/`. If the code and the
   spec disagree, the spec wins, unless the owner decides otherwise.

## Commands

```bash
cp build.conf.example build.conf   # once; set USER_PASSWORD (required)
./build.sh test                    # shellcheck + license headers + bats + go vet + go test -race (in the container)
./build.sh rpi4                    # also: vm-amd64 | vm-arm64 | all | cert
./build.sh rpi4 --rebuild-base     # ignore the cached base layer
./build.sh vm-amd64 --base-only    # build only the base layer

# Static checks of a finished image (read only)
test/rpi4-static.sh --stage app out/emulationstation-rpi4-<date>.img.xz
test/vm-static.sh  --stage app out/emulationstation-vm-amd64-<date>.qcow2

# VM smoke test (QEMU on the host)
./build.sh vm-amd64 --config test/fixtures/build.conf.smoke
test/vm-smoke.sh out/emulationstation-vm-amd64-<date>.qcow2
```

Quick checks on the host, if the tools are installed:

```bash
bats test/bats                                   # or a single file: bats test/bats/priv.bats
(cd webui && go vet ./... && go test -race -count=1 ./...)
gofmt -l webui                                   # must print nothing
scripts/check-license-headers.sh                 # prints files without a header
```

`./build.sh test` is the reference. Tests that pass only on the host are not enough.

## Architecture

```
build.sh                  host entry (Linux/macOS, **Bash 3.2 compatible**): config, Docker, binfmt
builder/Dockerfile        builder container (pins GO_VERSION, TAILWIND_VERSION + sha256)
builder/run.sh            container entry (Bash 5): runs the steps, lock, signals, tests
builder/steps/NN-*.sh     10-fetch → 20-prepare-image → 30-provision-base → 40-provision-app → 50-finalize; cert.sh
builder/lib/*.sh          shared functions (config, cache, chroot, image, tls, webconf, log)
builder/host/detect.sh    host OS/arch detection
provision/common/         scripts that run in the chroot: base-NN-*.sh (base layer), app-NN-*.sh (app layer)
provision/{rpi4,vm}/      per-target base scripts + modules.list (RetroPie modules)
overlay/                  files copied 1:1 into the image (systemd, nftables, fail2ban, sshd, sudoers, helpers)
overlay/usr/local/lib/emulationstation/priv   the only root helper of the web UI (sudo NOPASSWD)
controllers/templates/    ES input templates; controllers/custom/ = owner's own exports (not checked)
webui/                    Go module "emulationstation/webui"; cmd/emulationstation-web + internal/*
test/bats/, test/fixtures/, test/lib/   bats tests + stubs/fixtures; *-static.sh / vm-smoke.sh image tests
docs/hardware-checklist.md   checks that only real hardware can show
out/, secrets/            build output and CA key (git-ignored, written on the host)
```

### Two-layer cache (important when you add provisioning)

- **Base layer** (slow, hours): the RetroPie build. Its cache key covers
  `provision/common/base-*.sh`, `provision/<kind>/base-*.sh`, `provision/<kind>/modules.list`,
  `builder/steps/30-provision-base.sh`, the image checksum, `RETROPIE_SETUP_REF`, `XPADNEO_REF` and
  the target (`builder/lib/cache.sh: cache_key`).
- **App layer** (fast): `app-*.sh` and the overlay are applied again on every build.
- **Rule:** a new package or setting goes into an `app-*.sh` script (with `apt-get update` first)
  unless it truly belongs in the base layer. Any change to a base file invalidates the cache and
  costs hours. Example: `jq` is installed in `app-30-webui.sh` for exactly this reason.

### Web UI (`webui/`)

- One static binary (`CGO_ENABLED=0`). Templates and static files are embedded
  (`internal/web/render.go`, `//go:embed static templates`). Server-side HTML, Tailwind CSS and a
  small `static/app.js`. There is no JS framework and no npm.
- `internal/web/static/app.css` is **generated and git-ignored**:
  `tailwindcss -i assets/input.css -o internal/web/static/app.css --minify`
  (see `webui/scripts/build.sh`). It must exist before `go build` or `go test`.
- Packages: `auth` (Basic Auth + ban after 5 failures), `config` (web.conf), `web` (handlers, SSE,
  gzip), `jobs` (background jobs with progress), `roms` (staging/commit of uploads), `usb` (stick
  import), `gamelist`, `names` (name cleaning), `systems` (es_systems.cfg), `scrape` (Skyscraper),
  `bios`, `controllers`, `bluetooth` (D-Bus), `es` (start/stop ES), `priv` (calls the root helper),
  `sysinfo`.
- Dependencies: only `golang.org/x/crypto` and `github.com/godbus/dbus/v5`. Add new modules only
  when there is a strong reason.
- Config: `/etc/emulationstation-web/web.conf` (KEY=VALUE, mode 0600, owned by `emulationstation`).
  A new key needs a Go default in `internal/config`, must be written back by the save code, and
  must be documented in the README.
- The service runs as `emulationstation` with `ProtectSystem=full`, which gives it its own mount
  namespace. USB mounts at `/run/emulationstation-usb` are visible only to the service.

### Root helper (`priv`)

- It is the only way the web UI does root work: `sudo /usr/local/lib/emulationstation/priv <cmd>`.
  The commands are `reboot`, `poweroff`, `apt-update`, `apt-upgrade`, `journal`, `usb-list`,
  `usb-mount`, `usb-umount`, and others (see the `case` in the file).
- Treat every argument as hostile. Check it with strict regexes, check it again inside the helper
  (never trust the caller), and use no `eval`. Never follow symlinks in paths the web user can
  change.
- Exit codes: 0 ok, 1 action failed, 2 bad input or not allowed, 3 could not read state (e.g.
  the device list).
- A new command needs bats tests in `test/bats/priv.bats` with stubs. Do not change sudoers (the
  helper is already allowed). `visudo -cf` runs in the tests.

## Conventions

- **Bash:** `set -euo pipefail`. The code must be clean under `shellcheck -x`, with
  `.shellcheckrc` enabling `external-sources` and `source-path=SCRIPTDIR`. Add
  `# shellcheck source=...` above every `.`/`source`. Put a usage comment above each function, like
  `# name <args> : what it does`. Log with `log_info`/`log_warn`/`log_error`/`die <msg> <code>` from
  `builder/lib/log.sh`.
- **`build.sh` and `builder/host/*` run on macOS Bash 3.2.** Do not use associative arrays,
  `mapfile`, `${var,,}` or `declare -n` there. Inside the container (`builder/`, `provision/`)
  Bash 5 is fine.
- **Provisioning scripts** must be idempotent, use `PROVISION_ROOT`/`BUILD_ENV`/… overrides so
  bats can test them without a chroot, and never ask for input (stdin is `/dev/null`).
- **Go:** gofmt, `go vet`, table tests. Tests use temp folders and injected functions or
  interfaces instead of real system calls, e.g. `statfs`, the priv runner, `listROMs`, `openZip`.
  Write files with a temp file + rename. Never follow symlinks from user data (uploads, USB stick).
- **UI texts:** simple English, short. Errors tell the user what to do, and never show raw helper
  or Go error text.
- **Rulings.** When a design question comes up during work, decide it, write it down as
  `Ruling: <what> — <why> — <cost if wrong>` (in the spec or the plan), and keep going.

## Tests

- Every change needs tests. Bash goes in `test/bats/*.bats` with stubs in a temp `PATH` and
  fixtures in `test/fixtures/`, for example the lsblk JSON in `test/fixtures/lsblk/`. Go tests go
  next to the code.
- A change to what the image contains also needs a check in `test/lib/static-app.sh` /
  `test/rpi4-static.sh` / `test/vm-static.sh`, and something only hardware can show needs an entry
  in `docs/hardware-checklist.md`.
- "Done" means `./build.sh test` is green, or at least bats has 0 `not ok`, `go vet` and
  `go test -race` pass, `gofmt -l` is empty, and the license check is clean. Say clearly what was
  **not** run, such as a real image build or hardware.

## Gotchas

- RetroPie does not officially support trixie. A module that fails is skipped and listed in
  `out/build-report.txt`. Do not "fix" this by hiding the failure.
- `50-finalize.sh` copies the in-image report lines into `out/build-report.txt`. A new report line
  in the image must use that path to reach the owner.
- The builder container writes as root into the bind mounts. `owner_fix` in `builder/run.sh`
  gives the files back to the host user, so new output directories must be passed to it.
- Only one build at a time, because of the flock on the cache volume `emulationstation-cache`.
- The boot partition file `emulationstation.txt` (see `overlay/boot-template/`) is read by
  `firstboot` at **every** boot, then renamed to `emulationstation.txt.applied` with passwords
  masked. A new key there needs parsing, validation, masking (if secret) and tests
  (`test/bats/firstboot.bats`).
- The games list mirrors EmulationStation: a `.m3u`/descriptor hides its files only if the system
  accepts the descriptor's extension. Keep the web UI and ES consistent.
- USB import never deletes anything on the stick and never runs without a click in the web UI.
