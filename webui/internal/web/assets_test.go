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
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"

	"emulationstation/webui/internal/legal"
)

// isThirdPartyAsset tells if a path in static/ is a third-party file.
func isThirdPartyAsset(p string) bool {
	return strings.HasPrefix(p, "vendor/") || strings.HasPrefix(p, "fonts/") || p == "icons.svg"
}

func TestEveryEmbeddedThirdPartyFileIsListed(t *testing.T) {
	static, err := fs.Sub(assets, "static")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, c := range legal.Components {
		for _, f := range c.Files {
			listed[f] = true
			if f == "app.css" {
				continue // generated at build time
			}
			if _, err := fs.Stat(static, f); err != nil {
				t.Errorf("%s lists %q, which is not embedded", c.Name, f)
			}
		}
	}
	err = fs.WalkDir(static, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if isThirdPartyAsset(p) && !listed[p] {
			t.Errorf("%s is embedded but not listed in legal.Components", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

var (
	extTagRe = regexp.MustCompile(`(?is)<(?:script|link)\b[^>]*\b(?:src|href)\s*=\s*["']https?://`)
	extURLRe = regexp.MustCompile(`(?i)url\(\s*["']?https?://`)
)

func TestNoExternalResources(t *testing.T) {
	n := 0
	err := fs.WalkDir(assets, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasPrefix(p, "static/vendor/") || strings.HasPrefix(p, "static/fonts/") {
			return nil
		}
		b, err := fs.ReadFile(assets, p)
		if err != nil {
			return err
		}
		n++
		if extTagRe.Match(b) {
			t.Errorf("%s loads a script or stylesheet from the internet", p)
		}
		if extURLRe.Match(b) {
			t.Errorf("%s uses a CSS url() to the internet", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no files checked")
	}
}

// bundledPackages maps the package names that vendor files name in their
// license blocks to the component that lists them.
var bundledPackages = map[string]string{
	"@vue/shared":     "Vue reactivity (bundled in Alpine.js)",
	"@vue/reactivity": "Vue reactivity (bundled in Alpine.js)",
}

var (
	licenseBlockRe = regexp.MustCompile(`(?s)/\*[!*].*?\*/`)
	licenseMarkRe  = regexp.MustCompile(`Bundled license information|@license|@preserve`)
	bundledPkgRe   = regexp.MustCompile(`(?m)^[\s*(/!]*(@?[A-Za-z0-9][\w.-]*(?:/[\w.-]+)?) v\d+\.\d+`)
)

// bundledNames returns the package names in the license blocks of a file.
// ok is false if a license block names no package that can be read.
func bundledNames(b []byte) (names []string, ok bool) {
	ok = true
	for _, blk := range licenseBlockRe.FindAll(b, -1) {
		if !licenseMarkRe.Match(blk) {
			continue
		}
		m := bundledPkgRe.FindAllSubmatch(blk, -1)
		if len(m) == 0 {
			ok = false
		}
		for _, x := range m {
			names = append(names, string(x[1]))
		}
	}
	return names, ok
}

func TestBundledNames(t *testing.T) {
	tests := []struct {
		in   string
		want []string
		ok   bool
	}{
		{"var a=1;", nil, true},
		{"/*! Bundled license information:\n\nx.js:\n  (**\n  * @vue/shared v3.5.41\n  * @license MIT\n  **)\n*/", []string{"@vue/shared"}, true},
		{"/*! @license MIT */", nil, false},
		{"/** foo v1.2.3\n * @preserve */", []string{"foo"}, true},
	}
	for _, tt := range tests {
		got, ok := bundledNames([]byte(tt.in))
		if ok != tt.ok || strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("%q: got %v %v, want %v %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

// TestBundledPackagesAreListed fails if a vendor file names a bundled
// package in a license block that is not a component for that file.
func TestBundledPackagesAreListed(t *testing.T) {
	static, err := fs.Sub(assets, "static")
	if err != nil {
		t.Fatal(err)
	}
	covers := map[string]bool{} // "name|file"
	for _, c := range legal.Components {
		for _, f := range c.Files {
			covers[c.Name+"|"+f] = true
		}
	}
	n := 0
	err = fs.WalkDir(static, "vendor", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(static, p)
		if err != nil {
			return err
		}
		n++
		names, ok := bundledNames(b)
		if !ok {
			t.Errorf("%s has a license block without a package name", p)
		}
		for _, name := range names {
			comp, known := bundledPackages[name]
			if !known {
				t.Errorf("%s bundles %s, which is not in bundledPackages", p, name)
				continue
			}
			if !covers[comp+"|"+p] {
				t.Errorf("%s bundles %s, but component %q does not cover the file", p, name, comp)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no vendor files checked")
	}
}

var noticeShaRe = regexp.MustCompile(`(?m)^Files:\s+(\S+)\s+\(sha256 ([0-9a-f]{64})\)`)

// TestNoticesSHA256MatchEmbeddedFiles checks the sha256 values in
// THIRD-PARTY-NOTICES against the embedded files.
func TestNoticesSHA256MatchEmbeddedFiles(t *testing.T) {
	txt, err := os.ReadFile("../legal/THIRD-PARTY-NOTICES")
	if err != nil {
		t.Fatal(err)
	}
	static, err := fs.Sub(assets, "static")
	if err != nil {
		t.Fatal(err)
	}
	hashed := map[string]bool{}
	for _, m := range noticeShaRe.FindAllStringSubmatch(string(txt), -1) {
		hashed[m[1]] = true
		b, err := fs.ReadFile(static, m[1])
		if err != nil {
			t.Errorf("%s: %v", m[1], err)
			continue
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != m[2] {
			t.Errorf("%s: sha256 is %s, THIRD-PARTY-NOTICES says %s", m[1], got, m[2])
		}
	}
	err = fs.WalkDir(static, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if isThirdPartyAsset(p) && !hashed[p] {
			t.Errorf("%s has no sha256 line in THIRD-PARTY-NOTICES", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
