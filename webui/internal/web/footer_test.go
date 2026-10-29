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
	"strings"
	"testing"
)

func footerOf(t *testing.T, e *env) string {
	t.Helper()
	w := e.do("GET", "/system", nil)
	if w.Code != 200 {
		t.Fatalf("GET /system = %d", w.Code)
	}
	body := w.Body.String()
	i := strings.Index(body, "<footer")
	if i < 0 {
		t.Fatalf("no footer in page")
	}
	j := strings.Index(body[i:], "</footer>")
	if j < 0 {
		t.Fatalf("unterminated footer")
	}
	return body[i : i+j]
}

func TestFooterWithDefaultSourceURL(t *testing.T) {
	e := newEnv(t)
	e.cfg.SourceURL = "https://github.com/mkloubert/es-builder"
	f := footerOf(t, e)
	for _, want := range []string{
		"EmuStation Builder",
		`href="https://www.gnu.org/licenses/agpl-3.0.html"`,
		`href="https://github.com/mkloubert/es-builder"`,
		"AGPL-3.0", "Source code", `rel="noopener"`, `target="_blank"`,
	} {
		if !strings.Contains(f, want) {
			t.Errorf("footer lacks %q:\n%s", want, f)
		}
	}
}

func TestFooterWithEmptySourceURL(t *testing.T) {
	e := newEnv(t)
	e.cfg.SourceURL = ""
	f := footerOf(t, e)
	if !strings.Contains(f, "AGPL-3.0") || !strings.Contains(f, "EmuStation Builder") {
		t.Errorf("footer incomplete:\n%s", f)
	}
	if strings.Contains(f, "Source code") {
		t.Errorf("unexpected source link:\n%s", f)
	}
}

func TestFooterRejectsNonHTTPSourceURL(t *testing.T) {
	for _, u := range []string{"javascript:alert(1)", "data:text/html,x", "ftp://example.org/x", "//evil.example/x"} {
		e := newEnv(t)
		e.cfg.SourceURL = u
		f := footerOf(t, e)
		if strings.Contains(f, "Source code") || strings.Contains(f, "javascript") || strings.Contains(f, "ftp:") || strings.Contains(f, "evil") || strings.Contains(f, "data:") {
			t.Errorf("SourceURL %q leaked into footer:\n%s", u, f)
		}
	}
}
