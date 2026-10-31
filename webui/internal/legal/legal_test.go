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

package legal

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestComponentsComplete(t *testing.T) {
	if len(Components) != 11 {
		t.Fatalf("got %d components, want 11", len(Components))
	}
	for _, c := range Components {
		if c.Name == "" || c.Version == "" || c.URL == "" {
			t.Errorf("%q: name, version or URL is empty", c.Name)
		}
		if !AllowedSPDX[c.SPDX] {
			t.Errorf("%q: SPDX id %q is not allowed", c.Name, c.SPDX)
		}
		txt, err := ComponentLicense(c)
		if err != nil || strings.TrimSpace(txt) == "" {
			t.Errorf("%q: license text: %v", c.Name, err)
		}
	}
}

func TestComponentLicenseMissing(t *testing.T) {
	if _, err := ComponentLicense(Component{LicenseFile: "nope.txt"}); err == nil {
		t.Error("want an error for a missing file")
	}
}

func TestLicenseEqualsRepoLicense(t *testing.T) {
	want, err := os.ReadFile("../../../LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	if LicenseText() != string(want) {
		t.Error("embedded LICENSE differs from the repo LICENSE")
	}
}

func TestEveryGoModuleListed(t *testing.T) {
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^\s*(?:require\s+)?(\S+\.\S+/\S+)\s+v\S+`)
	found := 0
	for _, m := range re.FindAllStringSubmatch(string(mod), -1) {
		found++
		ok := false
		for _, c := range Components {
			if c.Name == m[1] {
				ok = true
			}
		}
		if !ok {
			t.Errorf("go.mod module %s is not in Components", m[1])
		}
	}
	if found == 0 {
		t.Error("no require lines found in go.mod")
	}
}

func TestNoticesFileNamesEveryComponent(t *testing.T) {
	for _, c := range Components {
		if !strings.Contains(noticesText(), c.Name) || !strings.Contains(noticesText(), c.SPDX) {
			t.Errorf("THIRD-PARTY-NOTICES misses %q or %q", c.Name, c.SPDX)
		}
	}
}

func TestModuleVersion(t *testing.T) {
	if got := ModuleVersion("example.invalid/none", "v9.9.9"); got != "v9.9.9" {
		t.Errorf("got %q, want fallback", got)
	}
}

// TestModuleVersionFallbacksMatchGoMod checks that every fallback version
// in legal.go is the version that go.mod requires.
func TestModuleVersionFallbacksMatchGoMod(t *testing.T) {
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("legal.go")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	re := regexp.MustCompile(`(?m)^\s*(?:require\s+)?(\S+\.\S+/\S+)\s+(v\S+)`)
	for _, m := range re.FindAllStringSubmatch(string(mod), -1) {
		want[m[1]] = m[2]
	}
	calls := regexp.MustCompile(`ModuleVersion\("([^"]+)", "([^"]+)"\)`).FindAllStringSubmatch(string(src), -1)
	if len(calls) == 0 {
		t.Fatal("no ModuleVersion calls found in legal.go")
	}
	seen := map[string]bool{}
	for _, c := range calls {
		seen[c[1]] = true
		if v, ok := want[c[1]]; !ok {
			t.Errorf("%s is not in go.mod", c[1])
		} else if c[2] != v {
			t.Errorf("%s: fallback %s, go.mod has %s", c[1], c[2], v)
		}
	}
	for m := range want {
		if !seen[m] {
			t.Errorf("go.mod module %s has no ModuleVersion fallback", m)
		}
	}
}
