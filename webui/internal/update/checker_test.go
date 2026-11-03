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
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newChecker(t *testing.T, url string, fn func(call, io.Writer) error) (*Checker, *fakeRunner) {
	f := &fakeRunner{fn: fn}
	return &Checker{
		Git:  Git{R: f, S: Settings{URL: url}},
		File: filepath.Join(t.TempDir(), "sub", "check.json"),
		Now:  func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	}, f
}

func TestCheckOK(t *testing.T) {
	c, _ := newChecker(t, "https://example.com/x", func(_ call, w io.Writer) error {
		io.WriteString(w, "a\trefs/tags/webui-v0.9.0\nb\trefs/tags/webui-v1.10.0\nc\trefs/tags/other\n")
		return nil
	})
	s := c.Check(context.Background())
	if s.Err != "" || s.Latest != "1.10.0" || s.CheckedAt.IsZero() {
		t.Fatalf("%+v", s)
	}
	if got := c.Load(); got != s {
		t.Errorf("load %+v want %+v", got, s)
	}
	b, _ := os.ReadFile(c.File)
	for _, k := range []string{"checked_at", "latest", "error"} {
		if !strings.Contains(string(b), k) {
			t.Errorf("no key %s in %s", k, b)
		}
	}
	m, _ := os.ReadDir(filepath.Dir(c.File))
	if len(m) != 1 {
		t.Errorf("temp file left: %v", m)
	}
}

func TestCheckErrors(t *testing.T) {
	cases := []struct {
		name, url, out string
		err            error
		want           string
	}{
		{"perm", "https://example.com/x", "", errors.New("git: exit 128: Permission denied (publickey)."), "auth"},
		{"authfail", "https://example.com/x", "", errors.New("Authentication failed for x"), "auth"},
		{"user", "https://example.com/x", "", errors.New("could not read Username for x"), "auth"},
		{"host", "https://example.com/x", "", errors.New("Host key verification failed."), "auth"},
		{"other", "https://example.com/x", "", errors.New("secret raw text"), "unreachable"},
		{"notags", "https://example.com/x", "a\trefs/tags/v1.0.0\n", nil, "no-tags"},
		{"empty", "https://example.com/x", "", nil, "no-tags"},
		{"badurl", "http://example.com/x", "", nil, "bad-url"},
		{"nourl", "", "", nil, "bad-url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, f := newChecker(t, tc.url, func(_ call, w io.Writer) error {
				io.WriteString(w, tc.out)
				return tc.err
			})
			s := c.Check(context.Background())
			if s.Err != tc.want || s.Latest != "" {
				t.Errorf("%+v", s)
			}
			if tc.want == "bad-url" && len(f.calls) != 0 {
				t.Error("git called")
			}
			if strings.Contains(s.Err, "secret") {
				t.Error("raw text leaked")
			}
		})
	}
}

func TestCheckLocalMissing(t *testing.T) {
	c, f := newChecker(t, filepath.Join(t.TempDir(), "nope"), nil)
	s := c.Check(context.Background())
	if s.Err != "unreachable" || len(f.calls) != 0 {
		t.Errorf("%+v calls %d", s, len(f.calls))
	}
	file := filepath.Join(t.TempDir(), "afile")
	os.WriteFile(file, nil, 0o600)
	c, f = newChecker(t, file, nil)
	if s := c.Check(context.Background()); s.Err != "unreachable" || len(f.calls) != 0 {
		t.Errorf("file: %+v", s)
	}
}

func TestLoadMissingOrBad(t *testing.T) {
	c, _ := newChecker(t, "", nil)
	if (c.Load() != State{}) {
		t.Error("expected zero state")
	}
	os.MkdirAll(filepath.Dir(c.File), 0o700)
	os.WriteFile(c.File, []byte("{bad"), 0o600)
	if (c.Load() != State{}) {
		t.Error("expected zero state for bad json")
	}
}

func TestNewer(t *testing.T) {
	cur := Version{1, 2, 3}
	cases := []struct {
		latest string
		ok     bool
	}{{"1.2.4", true}, {"1.3.0", true}, {"2.0.0", true}, {"1.2.3", false}, {"1.2.2", false}, {"", false}, {"junk", false}, {"1.10.0", true}}
	for _, tc := range cases {
		v, ok := State{Latest: tc.latest}.Newer(cur)
		if ok != tc.ok || (ok && v.String() != tc.latest) {
			t.Errorf("%q: %v %v", tc.latest, v, ok)
		}
	}
	if _, ok := (State{Latest: "1.2.4", Err: "auth"}).Newer(cur); !ok {
		t.Error("Newer must ignore Err")
	}
}

func TestRun(t *testing.T) {
	c, _ := newChecker(t, "https://example.com/x", func(_ call, w io.Writer) error {
		io.WriteString(w, "a\trefs/tags/webui-v1.0.0\n")
		return nil
	})
	c.First, c.Every = time.Millisecond, 5*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	deadline := time.After(2 * time.Second)
	for c.Load().Latest == "" {
		select {
		case <-deadline:
			t.Fatal("no check ran")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestCheckKeepsLatestOnError(t *testing.T) {
	fail := false
	c, _ := newChecker(t, "https://example.com/x", func(_ call, w io.Writer) error {
		if fail {
			return errors.New("network down")
		}
		io.WriteString(w, "a\trefs/tags/webui-v1.5.0\n")
		return nil
	})
	c.Check(context.Background())
	fail = true
	s := c.Check(context.Background())
	if s.Err != "unreachable" || s.Latest != "1.5.0" {
		t.Fatalf("%+v", s)
	}
	if _, ok := c.Load().Newer(Version{1, 0, 0}); !ok {
		t.Error("Newer lost after error")
	}
}

func TestBadSettingsPaths(t *testing.T) {
	for _, p := range []string{"/a b/key", "/a;b", "rel/key", "/a/../b", "/a\"b", "/a$(x)"} {
		for _, s := range []Settings{
			{URL: "https://example.com/x", SSHKeyFile: p},
			{URL: "https://example.com/x", KnownHostsFile: p},
			{URL: "https://example.com/x", SignersFile: p},
		} {
			c, f := newChecker(t, s.URL, nil)
			c.Git.S = s
			if st := c.Check(context.Background()); st.Err != "bad-url" || len(f.calls) != 0 {
				t.Errorf("%q: %+v calls %d", p, st, len(f.calls))
			}
			g := Git{R: f, S: s}
			if _, err := g.LsRemote(context.Background()); err == nil {
				t.Errorf("%q: LsRemote ok", p)
			}
			if err := g.Clone(context.Background(), "webui-v1.0.0", filepath.Join(t.TempDir(), "c")); err == nil {
				t.Errorf("%q: Clone ok", p)
			}
			if len(f.calls) != 0 {
				t.Errorf("%q: git called", p)
			}
		}
	}
}
