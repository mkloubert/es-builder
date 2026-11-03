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
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const secretText = "SECRET-RAW-TEXT"

// rep is a Reporter and Linker that records what it gets.
type rep struct {
	mu   sync.Mutex
	prog []float64
	logs []string
	link string
}

func (r *rep) Progress(p float64) { r.mu.Lock(); r.prog = append(r.prog, p); r.mu.Unlock() }
func (r *rep) Logf(f string, a ...any) {
	r.mu.Lock()
	r.logs = append(r.logs, fmt.Sprintf(f, a...))
	r.mu.Unlock()
}
func (r *rep) SetLink(u string) { r.link = u }

// rig builds an Updater with fakes. fail names a step that fails.
type rig struct {
	u       *Updater
	base    string
	events  []string
	fail    string
	free    uint64
	wrongV  bool
	install chan struct{}
	runEnvs []call
	tpl     string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	g := &rig{base: t.TempDir(), free: 2 << 30}
	df, err := os.ReadFile("testdata/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	gitR := &fakeRunner{fn: func(c call, out io.Writer) error {
		sub := c.Args[len(c.Args)-1]
		for _, a := range c.Args {
			if a == "clone" {
				g.events = append(g.events, "clone")
				if g.fail == "clone" {
					os.MkdirAll(sub, 0o755) // leaves a folder behind
					return errors.New(secretText)
				}
				os.MkdirAll(filepath.Join(sub, "builder"), 0o755)
				os.MkdirAll(filepath.Join(sub, "webui", "scripts"), 0o755)
				return os.WriteFile(filepath.Join(sub, "builder", "Dockerfile"), df, 0o644)
			}
			if a == "cat-file" {
				io.WriteString(out, "commit\n")
				return nil
			}
			if a == "rev-parse" {
				io.WriteString(out, "abc\n")
				return nil
			}
		}
		return nil
	}}
	runR := &fakeRunner{fn: func(c call, out io.Writer) error {
		switch {
		case c.Name == "tar":
			g.events = append(g.events, "tar")
			return os.MkdirAll(filepath.Join(c.Args[len(c.Args)-1], "go", "bin"), 0o755)
		case c.Name == "nice":
			g.events = append(g.events, "build")
			if g.fail == "build" {
				mc := envVal(c.Env, "GOMODCACHE")
				os.MkdirAll(filepath.Join(mc, "m"), 0o755)
				os.WriteFile(filepath.Join(mc, "m", "ro.go"), nil, 0o444)
				os.Chmod(filepath.Join(mc, "m"), 0o555)
				io.WriteString(out, secretText)
				return errors.New(secretText)
			}
			return os.WriteFile(c.Args[len(c.Args)-2], []byte("ELF"), 0o600)
		default:
			g.events = append(g.events, "version")
			if g.wrongV {
				io.WriteString(out, "9.9.9\n")
			} else {
				io.WriteString(out, "1.2.3\n")
			}
			return nil
		}
	}}
	fetch := fetchFunc(func(_ context.Context, url, dst, sha string) error {
		g.events = append(g.events, "fetch")
		if g.fail == "fetch" {
			return errors.New(secretText)
		}
		return os.WriteFile(dst, []byte("x"), 0o600)
	})
	g.tpl = t.TempDir()
	os.WriteFile(filepath.Join(g.tpl, "page.html"), []byte("custom"), 0o644)
	g.u = &Updater{
		Git:   Git{R: gitR, S: Settings{URL: "https://example.com/r"}},
		Fetch: fetch,
		R:     runR,
		Install: func(_ context.Context, v string) error {
			g.events = append(g.events, "install "+v)
			if g.install != nil {
				<-g.install
			}
			return nil
		},
		Statfs: func(p string) (uint64, error) {
			g.events = append(g.events, "statfs")
			return g.free, nil
		},
		Base:   g.base,
		UIDir:  filepath.Dir(g.tpl),
		GOARCH: "amd64",
	}
	// UIDir/templates must exist
	os.Rename(g.tpl, filepath.Join(filepath.Dir(g.tpl), "templates"))
	g.tpl = filepath.Join(filepath.Dir(g.tpl), "templates")
	return g
}

type fetchFunc func(ctx context.Context, url, dst, sha string) error

func (f fetchFunc) Fetch(ctx context.Context, url, dst, sha string) error {
	return f(ctx, url, dst, sha)
}

func envVal(env []string, k string) string {
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, k+"="); ok {
			return v
		}
	}
	return ""
}

func (g *rig) clean(t *testing.T) {
	t.Helper()
	ents, _ := os.ReadDir(g.base)
	for _, e := range ents {
		t.Errorf("left in base: %s", e.Name())
	}
}

func TestRunOK(t *testing.T) {
	g := newRig(t)
	r := &rep{}
	if err := g.u.Run(context.Background(), r, Version{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	want := "statfs clone fetch fetch tar build version install 1.2.3"
	if got := strings.Join(g.events, " "); got != want {
		t.Errorf("events:\n got %s\nwant %s", got, want)
	}
	fi, err := os.Stat(filepath.Join(g.base, "emulationstation-web"))
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("staged: %v %v", fi, err)
	}
	ents, _ := os.ReadDir(g.base)
	if len(ents) != 1 {
		t.Errorf("work folder left: %v", ents)
	}
	if r.link != "#restart" {
		t.Errorf("link = %q", r.link)
	}
	if len(r.logs) == 0 || r.logs[len(r.logs)-1] != "Restarting…" {
		t.Errorf("logs = %v", r.logs)
	}
	if r.prog[len(r.prog)-1] != 0.95 {
		t.Errorf("progress = %v", r.prog)
	}
}

func TestRunBuildCall(t *testing.T) {
	g := newRig(t)
	if err := g.u.Run(context.Background(), &rep{}, Version{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	var b, tar call
	for _, c := range g.u.R.(*fakeRunner).calls {
		switch c.Name {
		case "nice":
			b = c
		case "tar":
			tar = c
		}
	}
	a := b.Args
	if len(a) != 7 || strings.Join(a[:3], " ") != "-n 10 sh" || !strings.HasSuffix(a[3], "/src/webui/scripts/build.sh") ||
		a[4] != "amd64" || a[6] != "1.2.3" || !strings.HasSuffix(b.Dir, "/src/webui") {
		t.Errorf("build call: %+v", b)
	}
	for _, k := range []string{"GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly -buildvcs=false -p=1"} {
		if !has(b.Env, k) {
			t.Errorf("env %s missing: %v", k, b.Env)
		}
	}
	work := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(a[3]))))
	for _, k := range []string{"GOPATH", "GOCACHE", "GOMODCACHE"} {
		if !strings.HasPrefix(envVal(b.Env, k), work+"/") {
			t.Errorf("%s = %q, work %q", k, envVal(b.Env, k), work)
		}
	}
	if !strings.HasPrefix(envVal(b.Env, "TAILWINDCSS"), work+"/") ||
		!strings.HasPrefix(envVal(b.Env, "PATH"), work+"/go/bin:") {
		t.Errorf("env: %v", b.Env)
	}
	if strings.Join(tar.Args[:3], " ") != "-xzf "+tar.Args[1]+" --no-same-owner" || tar.Args[3] != "-C" {
		t.Errorf("tar: %v", tar.Args)
	}
}

func TestRunNoSpace(t *testing.T) {
	g := newRig(t)
	g.free = 1 << 30
	err := g.u.Run(context.Background(), &rep{}, Version{1, 2, 3})
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("err = %v", err)
	}
	if strings.Join(g.events, " ") != "statfs" {
		t.Errorf("events: %v", g.events)
	}
	g.clean(t)
}

func TestRunWrongVersion(t *testing.T) {
	g := newRig(t)
	g.wrongV = true
	if err := g.u.Run(context.Background(), &rep{}, Version{1, 2, 3}); err == nil {
		t.Fatal("want error")
	}
	for _, e := range g.events {
		if strings.HasPrefix(e, "install") {
			t.Error("install ran")
		}
	}
	g.clean(t)
}

func TestRunFailures(t *testing.T) {
	for _, step := range []string{"clone", "fetch", "build"} {
		t.Run(step, func(t *testing.T) {
			g := newRig(t)
			g.fail = step
			err := g.u.Run(context.Background(), &rep{}, Version{1, 2, 3})
			if err == nil {
				t.Fatal("want error")
			}
			if strings.Contains(Message(err), secretText) {
				t.Errorf("message leaks: %q", Message(err))
			}
			for _, e := range g.events {
				if strings.HasPrefix(e, "install") {
					t.Error("install ran")
				}
			}
			g.clean(t)
		})
	}
}

func TestRunUnsigned(t *testing.T) {
	g := newRig(t)
	g.u.Git.R = &fakeRunner{fn: func(c call, _ io.Writer) error {
		for _, a := range c.Args {
			if a == "clone" {
				return ErrUnsigned
			}
		}
		return nil
	}}
	err := g.u.Run(context.Background(), &rep{}, Version{1, 2, 3})
	if !errors.Is(err, ErrUnsigned) || Message(err) != "The update is not signed by a trusted key." {
		t.Errorf("err = %v, msg = %q", err, Message(err))
	}
	g.clean(t)
}

func TestRunBusy(t *testing.T) {
	g := newRig(t)
	g.install = make(chan struct{})
	done := make(chan error)
	go func() { done <- g.u.Run(context.Background(), &rep{}, Version{1, 2, 3}) }()
	for !g.u.Busy() {
	}
	if err := g.u.Run(context.Background(), &rep{}, Version{1, 2, 4}); !errors.Is(err, ErrBusy) {
		t.Errorf("err = %v", err)
	}
	close(g.install)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if g.u.Busy() {
		t.Error("still busy")
	}
}

func TestRunCopiesTemplates(t *testing.T) {
	g := newRig(t)
	var seen string
	inner := g.u.R.(*fakeRunner).fn
	g.u.R.(*fakeRunner).fn = func(c call, out io.Writer) error {
		if c.Name == "nice" {
			b, err := os.ReadFile(filepath.Join(c.Dir, "custom", "templates", "page.html"))
			if err == nil {
				seen = string(b)
			}
		}
		return inner(c, out)
	}
	if err := g.u.Run(context.Background(), &rep{}, Version{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if seen != "custom" {
		t.Errorf("template copy = %q", seen)
	}
}

func TestMessage(t *testing.T) {
	for _, err := range []error{ErrBusy, ErrNoSpace, ErrUnsigned, context.DeadlineExceeded, errors.New(secretText)} {
		m := Message(err)
		if m == "" || strings.Contains(m, secretText) {
			t.Errorf("%v -> %q", err, m)
		}
	}
	if Message(ErrNoSpace) != "Not enough free space. Free at least 1.5 GB and try again." {
		t.Error(Message(ErrNoSpace))
	}
}

func TestRunVersionHangs(t *testing.T) {
	old := versionTimeout
	versionTimeout = 50 * time.Millisecond
	defer func() { versionTimeout = old }()
	g := newRig(t)
	inner := g.u.R.(*fakeRunner).fn
	g.u.R.(*fakeRunner).fn = func(c call, out io.Writer) error {
		if c.Name != "nice" && c.Name != "tar" {
			return errors.New("hung")
		}
		return inner(c, out)
	}
	// block until the context is done
	g.u.R = ctxRunner{g.u.R, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	err := g.u.Run(context.Background(), &rep{}, Version{1, 2, 3})
	if err == nil {
		t.Fatal("want error")
	}
	for _, e := range g.events {
		if strings.HasPrefix(e, "install") {
			t.Error("install ran")
		}
	}
	g.clean(t)
}

// ctxRunner blocks the "version" call (not nice, not tar) until ctx is done.
type ctxRunner struct {
	Runner
	block func(ctx context.Context) error
}

func (c ctxRunner) Run(ctx context.Context, dir string, env []string, out io.Writer, name string, args ...string) error {
	if name != "nice" && name != "tar" {
		return c.block(ctx)
	}
	return c.Runner.Run(ctx, dir, env, out, name, args...)
}

func TestCapBuf(t *testing.T) {
	b := &capBuf{max: 4}
	n, _ := b.Write([]byte("abcdefgh"))
	if n != 8 || b.buf.String() != "abcd" {
		t.Errorf("n=%d buf=%q", n, b.buf.String())
	}
}

func TestRunCleansLeftovers(t *testing.T) {
	g := newRig(t)
	old := filepath.Join(g.base, "work.OLD")
	os.MkdirAll(filepath.Join(old, "gomod", "m"), 0o755)
	os.WriteFile(filepath.Join(old, "gomod", "m", "ro.go"), nil, 0o444)
	os.Chmod(filepath.Join(old, "gomod", "m"), 0o555)
	os.Chmod(filepath.Join(old, "gomod"), 0o555)
	os.WriteFile(filepath.Join(g.base, "emulationstation-web"), []byte("stale"), 0o755)
	g.u.Statfs = func(string) (uint64, error) {
		g.events = append(g.events, "statfs")
		if _, err := os.Lstat(old); err == nil {
			t.Error("work folder still there at the space check")
		}
		if _, err := os.Lstat(filepath.Join(g.base, "emulationstation-web")); err == nil {
			t.Error("old staged binary still there at the space check")
		}
		return g.free, nil
	}
	if err := g.u.Run(context.Background(), &rep{}, Version{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.Join(g.events, " "), "statfs") {
		t.Errorf("events: %v", g.events)
	}
	if _, err := os.Lstat(old); err == nil {
		t.Error("work folder left")
	}
}

func TestRunCleansBeforeNoSpace(t *testing.T) {
	g := newRig(t)
	g.free = 1 << 30
	old := filepath.Join(g.base, "work.OLD")
	os.MkdirAll(old, 0o755)
	if err := g.u.Run(context.Background(), &rep{}, Version{1, 2, 3}); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("err = %v", err)
	}
	g.clean(t)
}

func TestRunInstallFailClearsLink(t *testing.T) {
	g := newRig(t)
	var linkAtInstall string
	r := &rep{}
	g.u.Install = func(context.Context, string) error {
		linkAtInstall = r.link
		return errors.New("boom")
	}
	if err := g.u.Run(context.Background(), r, Version{1, 2, 3}); err == nil {
		t.Fatal("want error")
	}
	if linkAtInstall != "#restart" {
		t.Errorf("link at install = %q", linkAtInstall)
	}
	if r.link != "" {
		t.Errorf("link after failure = %q", r.link)
	}
	for _, l := range r.logs {
		if l == "Restarting…" {
			// logged before Install
			return
		}
	}
	t.Errorf("logs = %v", r.logs)
}

func TestRunBuildStderrGoesToTail(t *testing.T) {
	g := newRig(t)
	g.fail = "build"
	inner := g.u.R.(*fakeRunner).fn
	var merged bool
	g.u.R.(*fakeRunner).fn = func(c call, out io.Writer) error {
		if c.Name == "nice" {
			_, merged = out.(interface{ MergeStderr() })
		}
		return inner(c, out)
	}
	g.u.Run(context.Background(), &rep{}, Version{1, 2, 3})
	if !merged {
		t.Error("build stdout writer does not ask for stderr")
	}
}
