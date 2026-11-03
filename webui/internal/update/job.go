// EmuStation Builder
// Copyright (C) 2026  Marcel Joachim Kloubert
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"emulationstation/webui/internal/jobs"
)

// Errors that the UI shows with fixed texts (see Message).
var (
	ErrBusy    = errors.New("update already running")
	ErrNoSpace = errors.New("not enough free space")
)

const (
	minFree       = 1536 << 20 // 1.5 GB
	maxStaged     = 100 << 20  // the root helper refuses more
	downloadLimit = 15 * time.Minute
	buildLimit    = 60 * time.Minute
	tailLines     = 200
)

// Updater builds a new web UI version from a git tag and hands it to the
// root helper. Only one Run at a time.
type Updater struct {
	Git     Git
	Fetch   Fetcher
	R       Runner
	Install func(ctx context.Context, v string) error
	Statfs  func(path string) (uint64, error)
	Base    string // update folder, e.g. /var/lib/emulationstation-web/update
	UIDir   string // folder with templates/ (UI_DIR)
	GOARCH  string

	mu      sync.Mutex
	running bool
}

// DiskFree returns the free bytes under path. It fits Updater.Statfs.
func DiskFree(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil //nolint:unconvert
}

// Busy tells if a Run is active.
func (u *Updater) Busy() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.running
}

func (u *Updater) lock() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.running {
		return false
	}
	u.running = true
	return true
}

func (u *Updater) unlock() {
	u.mu.Lock()
	u.running = false
	u.mu.Unlock()
}

// Message gives the fixed text for the user. It never returns err text.
func Message(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrUnsigned):
		return "The update is not signed by a trusted key."
	case errors.Is(err, ErrBusy):
		return "Another update is running."
	case errors.Is(err, ErrNoSpace):
		return "Not enough free space. Free at least 1.5 GB and try again."
	case errors.Is(err, context.DeadlineExceeded):
		return "The update took too long. Try again later."
	case errors.Is(err, context.Canceled):
		return "The update was stopped."
	}
	return "The update failed. The old version runs again."
}

// versionTimeout limits the run of "<binary> version". A var for tests.
var versionTimeout = 60 * time.Second

// capBuf keeps the first max bytes and drops the rest.
type capBuf struct {
	buf bytes.Buffer
	max int
}

func (b *capBuf) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// tail keeps the last lines of the build output.
type tail struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// MergeStderr tells ExecRunner to send stderr to the tail as well.
func (t *tail) MergeStderr() {}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.buf.Len() > 1<<20 { // keep memory small; the lines are cut later
		b := t.buf.Bytes()
		t.buf.Reset()
		t.buf.Write(b[len(b)/2:])
	}
	return t.buf.Write(p)
}

func (t *tail) lines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	l := strings.Split(strings.TrimRight(t.buf.String(), "\n"), "\n")
	if len(l) > tailLines {
		l = l[len(l)-tailLines:]
	}
	return l
}

// removeAll deletes dir. The Go module cache is read only, so it makes
// the folders writable first. Symlinks are not followed.
func removeAll(dir string) {
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error { //nolint:errcheck
		if d != nil && d.IsDir() {
			os.Chmod(p, 0o700)
		}
		return nil
	})
	os.RemoveAll(dir)
}

// copyTemplates copies the regular files of src to dst. Links are skipped.
func copyTemplates(src, dst string) error {
	if fi, err := os.Lstat(src); err != nil || !fi.IsDir() {
		return nil
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(target, b, 0o644)
		}
		return nil
	})
}

// Run does the whole update for v. On success the root helper has been
// started and the service will restart soon.
func (u *Updater) Run(ctx context.Context, r jobs.Reporter, v Version) error {
	if !u.lock() {
		return ErrBusy
	}
	defer u.unlock()
	err := u.run(ctx, r, v)
	if err != nil {
		log.Printf("web UI update to %s failed: %v", v, err)
	}
	return err
}

func (u *Updater) run(ctx context.Context, r jobs.Reporter, v Version) error {
	ver := v.String()
	// A crash can leave work folders and a staged binary behind.
	if old, _ := filepath.Glob(filepath.Join(u.Base, "work.*")); len(old) > 0 {
		for _, d := range old {
			removeAll(d)
		}
	}
	os.Remove(filepath.Join(u.Base, "emulationstation-web"))
	free, err := u.Statfs(u.Base)
	if err != nil {
		return err
	}
	if free < minFree {
		return ErrNoSpace
	}
	staged := filepath.Join(u.Base, "emulationstation-web")
	os.Remove(staged)
	work, err := os.MkdirTemp(u.Base, "work.")
	if err != nil {
		return err
	}
	defer removeAll(work)
	if err := os.Chmod(work, 0o700); err != nil {
		return err
	}

	r.Progress(0.05)
	r.Logf("Getting version %s…", ver)
	src := filepath.Join(work, "src")
	if err := u.Git.Clone(ctx, "webui-v"+ver, src); err != nil {
		return err
	}
	r.Progress(0.15)
	df, err := os.ReadFile(filepath.Join(src, "builder", "Dockerfile"))
	if err != nil {
		return err
	}
	pins, err := ParsePins(df, u.GOARCH)
	if err != nil {
		return err
	}

	r.Logf("Downloading tools…")
	goURL, twURL := ToolURLs(pins, u.GOARCH)
	goTar := filepath.Join(work, "go.tar.gz")
	tw := filepath.Join(work, "tailwindcss")
	if err := u.fetch(ctx, goURL, goTar, pins.GoSHA); err != nil {
		return err
	}
	if err := u.fetch(ctx, twURL, tw, pins.TailwindSHA); err != nil {
		return err
	}
	if err := os.Chmod(tw, 0o700); err != nil {
		return err
	}
	if err := u.R.Run(ctx, "", nil, nil, "tar", "-xzf", goTar, "--no-same-owner", "-C", work); err != nil {
		return err
	}
	r.Progress(0.3)

	if err := copyTemplates(filepath.Join(u.UIDir, "templates"), filepath.Join(src, "webui", "custom", "templates")); err != nil {
		return err
	}

	out := filepath.Join(work, "emulationstation-web")
	if err := u.build(ctx, r, work, src, tw, out, ver); err != nil {
		return err
	}
	r.Progress(0.9)

	vout := &capBuf{max: 4096}
	vctx, vcancel := context.WithTimeout(ctx, versionTimeout)
	err = u.R.Run(vctx, "", nil, vout, out, "version")
	vcancel()
	if err != nil {
		return err
	}
	if got := strings.TrimSpace(vout.buf.String()); got != ver {
		return fmt.Errorf("new binary says version %q, want %q", got, ver)
	}
	fi, err := os.Lstat(out)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxStaged {
		return errors.New("new binary is not a normal file or is too big")
	}
	if err := os.Chmod(out, 0o755); err != nil {
		return err
	}
	if err := os.Rename(out, staged); err != nil {
		return err
	}

	r.Progress(0.95)
	r.Logf("Installing…")
	// The root helper can restart the service before Install returns, so the
	// job must show the restart link before that.
	r.Logf("Restarting…")
	l, hasLink := r.(jobs.Linker)
	if hasLink {
		l.SetLink("#restart")
	}
	if err := u.Install(ctx, ver); err != nil {
		if hasLink {
			l.SetLink("")
		}
		os.Remove(staged)
		return err
	}
	return nil
}

// fetch downloads one file with the 15 minute limit.
func (u *Updater) fetch(ctx context.Context, url, dst, sha string) error {
	ctx, cancel := context.WithTimeout(ctx, downloadLimit)
	defer cancel()
	return u.Fetch.Fetch(ctx, url, dst, sha)
}

// build runs webui/scripts/build.sh under nice with the pinned Go.
func (u *Updater) build(ctx context.Context, r jobs.Reporter, work, src, tw, out, ver string) error {
	r.Progress(0.4)
	r.Logf("Building. This takes a while…")
	ctx, cancel := context.WithTimeout(ctx, buildLimit)
	defer cancel()
	env := []string{
		"GOTOOLCHAIN=local",
		"GOFLAGS=-mod=readonly -buildvcs=false -p=1",
		"GOPATH=" + filepath.Join(work, "gopath"),
		"GOCACHE=" + filepath.Join(work, "gocache"),
		"GOMODCACHE=" + filepath.Join(work, "gomod"),
		"TAILWINDCSS=" + tw,
		"PATH=" + filepath.Join(work, "go", "bin") + ":" + os.Getenv("PATH"),
	}
	var t tail
	err := u.R.Run(ctx, filepath.Join(src, "webui"), env, &t, "nice", "-n", "10", "sh",
		filepath.Join(src, "webui", "scripts", "build.sh"), u.GOARCH, out, ver)
	if err != nil {
		for _, l := range t.lines() {
			log.Printf("build: %s", l)
		}
	}
	return err
}
