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
	"context"
	"io"
	"regexp"
	"strings"
	"testing"

	"emulationstation/webui/internal/jobs"
)

var layoutPages = []string{"/", "/games", "/upload", "/usb", "/bios", "/controllers", "/system", "/settings", "/about"}

// allLayoutPages returns layoutPages plus the pages that need test data:
// a system games list, an upload review and a job page.
func allLayoutPages(t *testing.T, e *env) []string {
	t.Helper()
	e.write(t, "snes/Mario.sfc", "rom")
	id := e.stageDir(t, "snes", map[string]string{"Zelda.sfc": "z"})
	job := e.d.Jobs.Submit("test", "Test job", func(ctx context.Context, r jobs.Reporter) error { return nil })
	e.waitJob(t, job.ID)
	return append(append([]string{}, layoutPages...), "/games/snes", "/upload/"+id, "/jobs/"+job.ID)
}

func TestEveryPageHasLegalLinksAndSkipLink(t *testing.T) {
	e := newEnv(t)
	e.cfg.SourceURL = "https://github.com/mkloubert/es-builder"
	e.d.LogW = io.Discard
	e.h = NewHandler(e.d)
	for _, p := range allLayoutPages(t, e) {
		w := e.get(p)
		if w.Code != 200 {
			t.Errorf("GET %s = %d", p, w.Code)
			continue
		}
		body := w.Body.String()
		for _, want := range []string{
			`href="/about"`, `href="#content"`, "Skip to content", `id="content"`,
			"https://www.gnu.org/licenses/agpl-3.0.html", `id="legal"`,
			`href="https://github.com/mkloubert/es-builder"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s lacks %q", p, want)
			}
		}
		// The skip link is the first focusable element of the body.
		b := body[strings.Index(body, "<body"):]
		if j := strings.Index(b, "<a "); j < 0 || !strings.HasPrefix(b[j:], `<a href="#content"`) {
			t.Errorf("GET %s: skip link is not the first link", p)
		}
		if strings.Contains(body, "bottom tab bar") || strings.Contains(body, `class="fixed inset-x-0 bottom-0 z-30`) {
			t.Errorf("GET %s still has the bottom tab bar", p)
		}
		if n := strings.Count(body, `id="legal"`); n != 1 {
			t.Errorf("GET %s: id=\"legal\" %d times, want 1", p, n)
		}
	}
}

func TestEveryPageHasAboutNavEntry(t *testing.T) {
	last := navItems[len(navItems)-1]
	if len(navItems) != 9 || last.Key != "about" || last.Href != "/about" || last.Label != "About & licenses" || last.Icon != "info" {
		t.Fatalf("nav entry 09 = %+v (len %d)", last, len(navItems))
	}
}

func TestPhoneMenuMarkup(t *testing.T) {
	e := newEnv(t)
	body := e.get("/").Body.String()
	btn := regexp.MustCompile(`<button[^>]*aria-controls="mobile-menu"[^>]*>`).FindString(body)
	if btn == "" || !strings.Contains(btn, ":aria-expanded") || !strings.Contains(btn, `aria-label="Open menu"`) {
		t.Fatalf("menu button missing or incomplete: %q", btn)
	}
	menu := regexp.MustCompile(`<div[^>]*id="mobile-menu"[^>]*>`).FindString(body)
	for _, want := range []string{"@keydown.escape", "@keydown.tab", `role="dialog"`, `aria-modal="true"`, `x-show="menu"`} {
		if !strings.Contains(menu, want) {
			t.Errorf("mobile menu lacks %q: %q", want, menu)
		}
	}
	if !strings.Contains(body, `aria-label="Close menu"`) {
		t.Error("mobile menu has no close button")
	}
	// Both navs list all nine entries with their numbers.
	if n := strings.Count(body, `href="/about"`); n < 3 {
		t.Errorf("about link %d times, want sidebar + menu + legal line", n)
	}
	for _, num := range []string{">01<", ">09<"} {
		if strings.Count(body, num) < 2 {
			t.Errorf("number %s not in both navs", num)
		}
	}
}

func TestNavKeyFor(t *testing.T) {
	for _, c := range []struct{ page, want string }{
		{"games_system", "games"},
		{"upload_review", "upload"},
		{"job", ""},
		{"dashboard", "dashboard"},
		{"settings", "settings"},
	} {
		if got := navKeyFor(c.page); got != c.want {
			t.Errorf("navKeyFor(%q) = %q, want %q", c.page, got, c.want)
		}
	}
}

// crumbOf returns the breadcrumb nav of a page.
func crumbOf(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)<nav aria-label="Breadcrumb".*?</nav>`).FindString(body)
	if m == "" {
		t.Fatal("no breadcrumb")
	}
	return m
}

func TestBreadcrumbAndActiveNav(t *testing.T) {
	e := newEnv(t)
	e.write(t, "snes/Mario.sfc", "rom")
	w := e.get("/games/snes")
	if w.Code != 200 {
		t.Fatalf("GET /games/snes = %d", w.Code)
	}
	body := w.Body.String()
	c := crumbOf(t, body)
	for _, want := range []string{">02<", "GAMES", `href="/games"`, "SUPER NINTENDO", `aria-current="page"`} {
		if !strings.Contains(c, want) {
			t.Errorf("crumb lacks %q:\n%s", want, c)
		}
	}
	if !strings.Contains(body, `href="/games" aria-current="page"`) {
		t.Error("games nav entry is not active")
	}
	if strings.Contains(body, `href="/" aria-current="page"`) {
		t.Error("dashboard is active on a games page")
	}

	// A top-level page has no link in the crumb.
	c = crumbOf(t, e.get("/settings").Body.String())
	if !strings.Contains(c, ">08<") || !strings.Contains(c, "SETTINGS") || strings.Contains(c, "<a ") {
		t.Errorf("settings crumb:\n%s", c)
	}
}

func TestCustomPageWithoutNewBlocksRenders(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	e.cfg.UIDir = dir
	writeCustom(t, dir, map[string]string{"templates/dashboard.html": `{{define "content"}}CUSTOMX{{end}}`})
	e.d.LogW = io.Discard
	e.h = NewHandler(e.d)
	w := e.get("/")
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "CUSTOMX") || !strings.Contains(body, `id="sidebar"`) {
		t.Fatalf("code=%d body=%s", w.Code, body)
	}
}

func TestLayoutHeadAndDialog(t *testing.T) {
	e := newEnv(t)
	body := e.get("/").Body.String()
	for _, want := range []string{
		`<link rel="preload" href="/static/fonts/InterVariable.woff2" as="font" type="font/woff2" crossorigin>`,
		`<link rel="preload" href="/static/fonts/GeistMono-wght.woff2" as="font" type="font/woff2" crossorigin>`,
		`id="modal-slot"`, "@toast.window", "@ask-confirm.window", "'confirming'",
		"dialog.danger", "Are you sure?", `aria-labelledby="confirm-title"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("layout lacks %q", want)
		}
	}
}
