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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const customDash = `{{define "content"}}CUSTOM DASH{{end}}`

func TestCustomTemplateRendered(t *testing.T) {
	d, _ := newDeps(t)
	dir := t.TempDir()
	d.Config().UIDir = dir
	writeCustom(t, dir, map[string]string{"templates/dashboard.html": customDash})
	w := serve(NewHandler(d), authed(httptest.NewRequest("GET", "/", nil)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "CUSTOM DASH") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestBrokenCustomTemplatesFallBack(t *testing.T) {
	cases := map[string]string{
		"templates/dashboard.html":        `{{define "content"}}{{.Nope`,
		"templates/partials/flash.html":   `{{end}}`,
		"templates/partials/new_one.html": `{{if}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			d, logw := newDeps(t)
			dir := t.TempDir()
			d.Config().UIDir = dir
			writeCustom(t, dir, map[string]string{name: body})
			s, h := newServer(d)
			if st := s.ui.Load(); st.Err == "" || strings.Contains(st.Err, "\n") || st.Dir != dir {
				t.Fatalf("ui state = %+v", st)
			}
			if !strings.Contains(logw.String(), "using built-in templates") {
				t.Errorf("log = %q", logw.String())
			}
			w := serve(h, authed(httptest.NewRequest("GET", "/", nil)))
			if w.Code != 200 || !strings.Contains(w.Body.String(), "Settings") {
				t.Fatalf("built-in page not served: code=%d", w.Code)
			}
		})
	}
}

func TestUIStateCountsAll(t *testing.T) {
	d, _ := newDeps(t)
	dir := t.TempDir()
	d.Config().UIDir = dir
	writeCustom(t, dir, map[string]string{"static/x.css": "body{}"})
	s, h := newServer(d)
	if st := s.ui.Load(); st.Files != 1 || st.Err != "" || st.Dir != dir {
		t.Fatalf("ui state = %+v", st)
	}
	w := serve(h, authed(httptest.NewRequest("GET", "/static/x.css", nil)))
	if w.Code != 200 || w.Body.String() != "body{}" {
		t.Fatalf("static: code=%d body=%q", w.Code, w.Body.String())
	}
	if w := serve(h, authed(httptest.NewRequest("GET", "/", nil))); !strings.Contains(w.Body.String(), "Settings") {
		t.Fatal("built-in templates expected")
	}
}

func TestUIStateOff(t *testing.T) {
	for _, dir := range []string{"", "/nonexistent/webui-custom"} {
		d, _ := newDeps(t)
		d.Config().UIDir = dir
		s, _ := newServer(d)
		if st := s.ui.Load(); st.Dir != "" || st.Files != 0 || st.Err != "" {
			t.Errorf("dir %q: ui state = %+v", dir, st)
		}
	}
}

func TestReloadConcurrent(t *testing.T) {
	d, _ := newDeps(t)
	dir := t.TempDir()
	d.Config().UIDir = dir
	s, h := newServer(d)
	writeCustom(t, dir, map[string]string{"templates/dashboard.html": customDash})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if w := serve(h, authed(httptest.NewRequest("GET", "/", nil))); w.Code != 200 {
					t.Errorf("code=%d", w.Code)
				}
			}
		}()
	}
	for range 20 {
		s.loadTemplates()
	}
	wg.Wait()
	if w := serve(h, authed(httptest.NewRequest("GET", "/", nil))); !strings.Contains(w.Body.String(), "CUSTOM DASH") {
		t.Fatal("reload did not pick up the custom template")
	}
}

func TestSystemPageCustomUICard(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		dir   bool
		want  string
	}{
		{"off", nil, false, "Custom web UI is off."},
		{"ok", map[string]string{"static/a.css": "x", "templates/partials/my.html": `{{define "my"}}x{{end}}`}, true, "2 custom files in use from "},
		{"error", map[string]string{"templates/partials/my.html": `{{if}}`}, true, "The custom templates have an error. The built-in web UI is in use."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := newDeps(t)
			d.Config().UIDir = ""
			if tc.dir {
				d.Config().UIDir = t.TempDir()
				writeCustom(t, d.Config().UIDir, tc.files)
			}
			body := serve(NewHandler(d), authed(httptest.NewRequest("GET", "/system", nil))).Body.String()
			for _, want := range []string{tc.want, "Reload web UI", `hx-post="/system/ui-reload"`} {
				if !strings.Contains(body, want) {
					t.Errorf("system page missing %q", want)
				}
			}
		})
	}
}

func reloadReq() *http.Request {
	r := authed(httptest.NewRequest("POST", "/system/ui-reload", nil))
	r.Header.Set("HX-Request", "true")
	return r
}

func TestUIReload(t *testing.T) {
	d, _ := newDeps(t)
	dir := t.TempDir()
	d.Config().UIDir = dir
	h := NewHandler(d)
	writeCustom(t, dir, map[string]string{"templates/dashboard.html": customDash})
	if b := serve(h, authed(httptest.NewRequest("GET", "/", nil))).Body.String(); strings.Contains(b, "CUSTOM DASH") {
		t.Fatal("template changed before the reload")
	}
	w := serve(h, reloadReq())
	if w.Code != 200 || !strings.Contains(w.Header().Get("HX-Trigger"), "Web UI reloaded.") || !strings.Contains(w.Body.String(), "1 custom files in use") {
		t.Fatalf("code=%d trigger=%q body=%s", w.Code, w.Header().Get("HX-Trigger"), w.Body.String())
	}
	if strings.Contains(w.Body.String(), "<html") {
		t.Error("reload must answer with the card only")
	}
	if b := serve(h, authed(httptest.NewRequest("GET", "/", nil))).Body.String(); !strings.Contains(b, "CUSTOM DASH") {
		t.Fatal("reload did not load the new template")
	}

	writeCustom(t, dir, map[string]string{"templates/dashboard.html": `{{define "content"}}{{.Nope`})
	w = serve(h, reloadReq())
	const msg = "The custom templates have an error. The built-in web UI is in use."
	if w.Code != 200 || !strings.Contains(w.Header().Get("HX-Trigger"), "Custom templates have an error. The built-in web UI is in use.") || !strings.Contains(w.Body.String(), msg) {
		t.Fatalf("code=%d trigger=%q body=%s", w.Code, w.Header().Get("HX-Trigger"), w.Body.String())
	}
	if b := serve(h, authed(httptest.NewRequest("GET", "/", nil))).Body.String(); strings.Contains(b, "CUSTOM DASH") || !strings.Contains(b, "Settings") {
		t.Fatal("broken reload must give the built-in templates")
	}
}

func TestUIReloadProtected(t *testing.T) {
	d, _ := newDeps(t)
	h := NewHandler(d)
	if w := serve(h, httptest.NewRequest("POST", "/system/ui-reload", nil)); w.Code != http.StatusUnauthorized {
		t.Errorf("no auth: status = %d, want 401", w.Code)
	}
	r := reloadReq()
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if w := serve(h, r); w.Code != http.StatusForbidden {
		t.Errorf("cross-site: status = %d, want 403", w.Code)
	}
}

func TestCustomStaticSeeks(t *testing.T) {
	d, _ := newDeps(t)
	dir := t.TempDir()
	d.Config().UIDir = dir
	writeCustom(t, dir, map[string]string{"static/a.css": "hello", "static/blob": "hello"})
	h := NewHandler(d)
	r := authed(httptest.NewRequest("GET", "/static/a.css", nil))
	r.Header.Set("Range", "bytes=1-2")
	if w := serve(h, r); w.Code != http.StatusPartialContent || w.Body.String() != "el" {
		t.Errorf("range: code=%d body=%q", w.Code, w.Body.String())
	}
	if w := serve(h, authed(httptest.NewRequest("GET", "/static/blob", nil))); w.Code != 200 || w.Body.String() != "hello" {
		t.Errorf("no extension: code=%d body=%q", w.Code, w.Body.String())
	}
}

func TestCustomRuntimeErrorUsesBuiltIn(t *testing.T) {
	cases := []struct{ file, body, path, want string }{
		{"templates/dashboard.html", `{{define "content"}}{{.Data.Nope.X}}{{end}}`, "/", "Settings"},
		{"templates/partials/custom_ui.html", `{{define "custom_ui"}}{{.Nope.X}}{{end}}`, "/system", "Reload web UI"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			d, logw := newDeps(t)
			dir := t.TempDir()
			d.Config().UIDir = dir
			writeCustom(t, dir, map[string]string{tc.file: tc.body})
			h := NewHandler(d)
			w := serve(h, authed(httptest.NewRequest("GET", tc.path, nil)))
			if w.Code != 200 || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
			}
			if !strings.Contains(logw.String(), "using built-in templates") {
				t.Errorf("log = %q", logw.String())
			}
			if w := serve(h, reloadReq()); w.Code != 200 || !strings.Contains(w.Body.String(), "Reload web UI") {
				t.Errorf("reload card: code=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
