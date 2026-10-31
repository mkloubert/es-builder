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

package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
)

// writeCustom writes files (path -> content) below dir.
func writeCustom(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func testBase() fstest.MapFS {
	return fstest.MapFS{
		"templates/a.html":          {Data: []byte("base a")},
		"templates/b.html":          {Data: []byte("base b")},
		"templates/partials/p.html": {Data: []byte("base p")},
		"static/app.css":            {Data: []byte("base css")},
		"static/vendor/x.js":        {Data: []byte("base x")},
	}
}

func readOr(fsys fs.FS, name string) string {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return "ERR"
	}
	return string(b)
}

func TestOverlay(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.html")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeCustom(t, dir, map[string]string{
		"templates/a.html":          "custom a",
		"templates/new.html":        "custom new",
		"templates/partials/q.html": "custom q",
		"templates/.hidden.html":    "hidden",
		"static/logo.png":           "png",
		"static/vendor/x.js/y":      "dir instead of file",
		".git/config":               "hidden dir",
	})
	if err := os.Symlink(outside, filepath.Join(dir, "templates", "b.html")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("new.html", filepath.Join(dir, "templates", "link.html")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "near.html"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../near.html", filepath.Join(dir, "templates", "esc.html")); err != nil {
		t.Fatal(err)
	}
	o := newOverlayFS(dir, testBase())

	reads := []struct{ name, want string }{
		{"templates/a.html", "custom a"},          // override
		{"templates/partials/p.html", "base p"},   // fallback
		{"templates/new.html", "custom new"},      // custom only
		{"templates/partials/q.html", "custom q"}, // custom only, nested
		{"static/logo.png", "png"},
		{"templates/b.html", "base b"},        // symlink to outside: never the outside file
		{"templates/link.html", "custom new"}, // relative symlink inside the folder
		{"static/vendor/x.js", "base x"},      // custom folder where base has a file
		{"templates/esc.html", "ERR"},         // relative symlink that leaves the folder
		{"templates/.hidden.html", "ERR"},     // dot names are ignored
		{".git/config", "ERR"},
		{"../secret.html", "ERR"},
		{"templates/../../secret.html", "ERR"},
	}
	for _, r := range reads {
		if got := readOr(o, r.name); got != r.want {
			t.Errorf("read %s = %q, want %q", r.name, got, r.want)
		}
	}

	ents, err := fs.ReadDir(o, "templates")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if want := []string{"a.html", "b.html", "link.html", "new.html", "partials"}; !slices.Equal(names, want) {
		t.Errorf("ReadDir templates = %v, want %v", names, want)
	}
	glob, err := fs.Glob(o, "templates/partials/*.html")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"templates/partials/p.html", "templates/partials/q.html"}; !slices.Equal(glob, want) {
		t.Errorf("Glob partials = %v, want %v", glob, want)
	}
	if got := readOr(mustSub(t, o, "static"), "logo.png"); got != "png" {
		t.Errorf("sub static logo.png = %q", got)
	}
}

func mustSub(t *testing.T, fsys fs.FS, dir string) fs.FS {
	t.Helper()
	s, err := fs.Sub(fsys, dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOverlayOff(t *testing.T) {
	base := testBase()
	for _, dir := range []string{"", filepath.Join(t.TempDir(), "missing")} {
		o := newOverlayFS(dir, base)
		if got := readOr(o, "templates/a.html"); got != "base a" {
			t.Errorf("dir %q: read = %q", dir, got)
		}
		if g, _ := fs.Glob(o, "templates/*.html"); len(g) != 2 {
			t.Errorf("dir %q: glob = %v", dir, g)
		}
	}
}

func TestOverlayLazyFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ui")
	o := newOverlayFS(dir, testBase())
	if got := readOr(o, "templates/a.html"); got != "base a" {
		t.Fatalf("before = %q", got)
	}
	writeCustom(t, dir, map[string]string{"templates/a.html": "late"})
	if got := readOr(o, "templates/a.html"); got != "late" {
		t.Fatalf("after create = %q", got)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if got := readOr(o, "templates/a.html"); got != "base a" {
		t.Fatalf("after remove = %q", got)
	}
}

func TestCountCustomFiles(t *testing.T) {
	dir := t.TempDir()
	writeCustom(t, dir, map[string]string{
		"templates/a.html":          "x",
		"templates/partials/q.html": "x",
		"static/img/logo.png":       "x",
		"static/.DS_Store":          "x",
		"other/z.txt":               "x",
		"README.md":                 "x",
	})
	cases := []struct {
		dir  string
		want int
	}{
		{dir, 3},
		{"", 0},
		{filepath.Join(dir, "missing"), 0},
	}
	for _, c := range cases {
		if got := countCustomFiles(c.dir); got != c.want {
			t.Errorf("countCustomFiles(%q) = %d, want %d", c.dir, got, c.want)
		}
	}
}
