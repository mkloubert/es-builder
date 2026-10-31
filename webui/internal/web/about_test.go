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
	"net/http/httptest"
	"strings"
	"testing"

	"emulationstation/webui/internal/legal"
)

func TestAboutShowsLegalNotices(t *testing.T) {
	e := newEnv(t)
	e.cfg.SourceURL = "https://github.com/mkloubert/es-builder"
	w := e.get("/about")
	if w.Code != 200 {
		t.Fatalf("GET /about = %d", w.Code)
	}
	body := w.Body.String()
	want := []string{
		"Copyright (C) 2026  Marcel Joachim Kloubert",
		"ABSOLUTELY NO WARRANTY",
		"GNU AFFERO GENERAL PUBLIC LICENSE",
		`href="https://github.com/mkloubert/es-builder"`,
		"<details", "<pre",
	}
	for _, c := range legal.Components {
		want = append(want, c.Name, c.SPDX)
	}
	for _, s := range want {
		if !strings.Contains(body, s) {
			t.Errorf("page lacks %q", s)
		}
	}
	if n := strings.Count(body, "<pre"); n != len(legal.Components)+1 {
		t.Errorf("<pre> count = %d, want %d", n, len(legal.Components)+1)
	}
}

func TestAboutWithoutSourceURL(t *testing.T) {
	for _, u := range []string{"", "javascript:alert(1)", "ftp://x/y"} {
		e := newEnv(t)
		e.cfg.SourceURL = u
		w := e.get("/about")
		if w.Code != 200 {
			t.Fatalf("%q: GET /about = %d", u, w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "No source code link is set. Set SOURCE_URL in build.conf.") {
			t.Errorf("%q: no hint", u)
		}
		for _, bad := range []string{"javascript", "ftp:"} {
			if strings.Contains(body, bad) {
				t.Errorf("%q: page contains %q", u, bad)
			}
		}
	}
}

func TestAboutNeedsAuth(t *testing.T) {
	e := newEnv(t)
	w := serve(e.h, httptest.NewRequest("GET", "/about", nil))
	if w.Code != 401 {
		t.Errorf("GET /about without auth = %d, want 401", w.Code)
	}
}

func TestAboutIsGetOnly(t *testing.T) {
	e := newEnv(t)
	if w := e.do("POST", "/about", nil); w.Code != 405 {
		t.Errorf("POST /about = %d, want 405", w.Code)
	}
}
