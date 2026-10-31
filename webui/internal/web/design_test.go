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
	"bytes"
	"html/template"
	"io/fs"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestPct(t *testing.T) {
	tests := []struct {
		used, total uint64
		want        int
	}{
		{0, 0, 0}, {1, 0, 0}, {50, 200, 25}, {300, 200, 100}, {200, 200, 100},
	}
	for _, tc := range tests {
		if got := pct(tc.used, tc.total); got != tc.want {
			t.Errorf("pct(%d,%d) = %d, want %d", tc.used, tc.total, got, tc.want)
		}
	}
}

func runIcon(t *testing.T, src string) (string, error) {
	t.Helper()
	tpl, err := template.New("x").Funcs(funcs).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err = tpl.Execute(&buf, nil)
	return buf.String(), err
}

func TestIconUnknownFails(t *testing.T) {
	if _, err := runIcon(t, `{{icon "no-such-icon"}}`); err == nil {
		t.Fatal("unknown icon must fail")
	}
	out, err := runIcon(t, `{{icon "play"}}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`href="/static/icons.svg#play"`, `aria-hidden="true"`, `class="icon"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q lacks %q", out, want)
		}
	}
}

func TestEveryTemplateIconExists(t *testing.T) {
	re := regexp.MustCompile(`icon "([a-z0-9-]+)"`)
	var names []string
	err := fs.WalkDir(assets, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := assets.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			names = append(names, m[1])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range navItems {
		names = append(names, n.Icon)
	}
	for _, n := range names {
		if !iconNames()[n] {
			t.Errorf("icon %q is not in static/icons.svg", n)
		}
	}
}

func lum(hex string) float64 {
	v, _ := strconv.ParseUint(hex, 16, 32)
	ch := func(c uint64) float64 {
		x := float64(c) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(v>>16&255) + 0.7152*ch(v>>8&255) + 0.0722*ch(v&255)
}

func contrast(a, b string) float64 {
	la, lb := lum(a), lum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestTokenContrast(t *testing.T) {
	css, err := os.ReadFile("../../assets/input.css")
	if err != nil {
		t.Fatal(err)
	}
	tok := map[string]string{}
	for _, m := range regexp.MustCompile(`--color-([a-z-]+):\s*#([0-9a-fA-F]{6});`).FindAllStringSubmatch(string(css), -1) {
		tok[m[1]] = m[2]
	}
	pairs := [][2]string{
		{"fg", "bg"}, {"muted", "bg"}, {"dim", "bg"}, {"accent-fg", "bg"}, {"ok", "bg"}, {"warn", "bg"},
		{"bg", "fg"}, {"fg", "raised"}, {"muted", "raised"}, {"dim", "raised"},
	}
	for _, p := range pairs {
		a, b := tok[p[0]], tok[p[1]]
		if a == "" || b == "" {
			t.Errorf("token missing for %s/%s", p[0], p[1])
			continue
		}
		if c := contrast(a, b); c < 4.5 {
			t.Errorf("%s on %s: contrast %.2f < 4.5", p[0], p[1], c)
		}
	}
	// Non-text pairs (WCAG 1.4.11): borders of inputs, selects, checkboxes
	// and radios reach at least 3:1 against the colors next to them.
	for _, p := range [][2]string{
		{"line-field", "bg"}, {"line-field", "surface"}, {"line-field", "raised"},
		{"dim", "bg"}, {"dim", "surface"}, {"dim", "raised"},
	} {
		a, b := tok[p[0]], tok[p[1]]
		if a == "" || b == "" {
			t.Errorf("token missing for %s/%s", p[0], p[1])
			continue
		}
		if c := contrast(a, b); c < 3.0 {
			t.Errorf("%s on %s: contrast %.2f < 3.0", p[0], p[1], c)
		}
	}
}
