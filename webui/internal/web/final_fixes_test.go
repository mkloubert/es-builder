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
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"emulationstation/webui/internal/jobs"
	"emulationstation/webui/internal/legal"
)

func TestPanelHeadOptions(t *testing.T) {
	tests := []struct {
		opts []string
		want panelHeadData
		err  bool
	}{
		{nil, panelHeadData{Num: "01", Title: "T"}, false},
		{[]string{"id:x", "info:a:b"}, panelHeadData{Num: "01", Title: "T", ID: "x", Info: "a:b"}, false},
		{[]string{"badge-warn:Decide"}, panelHeadData{Num: "01", Title: "T", Badge: "Decide", BadgeKind: "warn"}, false},
		{[]string{"badge:Plain"}, panelHeadData{Num: "01", Title: "T", Badge: "Plain"}, false},
		{[]string{"nope:x"}, panelHeadData{}, true},
		{[]string{"nocolon"}, panelHeadData{}, true},
	}
	for _, tt := range tests {
		got, err := panelHead("01", "T", tt.opts...)
		if (err != nil) != tt.err {
			t.Errorf("%v: err = %v", tt.opts, err)
			continue
		}
		if !tt.err && got != tt.want {
			t.Errorf("%v: got %+v, want %+v", tt.opts, got, tt.want)
		}
	}
}

// panelHeadRe matches the start of a panel head as the partial writes it.
var panelHeadRe = regexp.MustCompile(`class="panel-head[^"]*">(?:<span class="chip-num">\d\d</span><h2(?: id="[^"]+")? class="label-mono">)?`)

func TestEveryPanelHeadIsNumberedWithH2(t *testing.T) {
	e := newEnv(t)
	pages := allLayoutPages(t, e)
	// A review with files that need a decision, files that belong to another
	// file, ready files and skipped files.
	many := map[string]string{
		"Game.cue": "FILE \"Game.bin\" BINARY", "Game.bin": "b", "Odd.nes": "n", "readme.txt": "junk",
	}
	for i := 0; i < 25; i++ {
		many[fmt.Sprintf("Game %02d.sfc", i)] = "g"
	}
	grouped := "/upload/" + e.stageDir(t, "snes", many)
	pages = append(pages, grouped)
	for _, p := range pages {
		b := e.get(p).Body.String()
		for _, m := range panelHeadRe.FindAllString(b, -1) {
			if !strings.Contains(m, "<h2") {
				t.Errorf("GET %s: panel head without number chip and h2: %q", p, m)
			}
		}
	}
	if b := e.get(grouped).Body.String(); !strings.Contains(b, `<summary class="panel-head cursor-pointer"><span class="chip-num">`) {
		t.Error("grouped review: summary panel heads are not numbered")
	}
	b := e.get("/about").Body.String()
	if n := strings.Count(b, `<span class="chip-num">`); n != 4 {
		t.Errorf("/about: %d number chips, want 4", n)
	}
}

func TestReviewPanelsAreNumbered(t *testing.T) {
	e := newEnv(t)
	id := e.stageDir(t, "snes", map[string]string{"A.sfc": "a", "B.sfc": "b"})
	b := e.get("/upload/" + id).Body.String()
	for _, want := range []string{`<span class="chip-num">01</span>`, `<span class="chip-num">02</span>`} {
		if !strings.Contains(b, want) {
			t.Errorf("review page lacks %q", want)
		}
	}
	if strings.Contains(b, `chip-num">!<`) {
		t.Error(`review page still has the "!" chip`)
	}
}

// detailsRe matches a <details> element (they are not nested on /about).
var detailsRe = regexp.MustCompile(`(?s)<details.*?</details>`)

func TestAboutTableShowsLinksOutsideDetails(t *testing.T) {
	e := newEnv(t)
	b := e.get("/about").Body.String()
	if !strings.Contains(b, "<table") {
		t.Fatal("third-party list is not a table")
	}
	closed := detailsRe.ReplaceAllString(b, "")
	for _, c := range legal.Components {
		for _, want := range []string{c.Name, c.Version, c.SPDX, `href="` + c.URL + `" target="_blank" rel="noopener"`} {
			if !strings.Contains(closed, want) {
				t.Errorf("%s: %q is not visible without opening a row", c.Name, want)
			}
		}
	}
	// Each row's <details> holds only the license text.
	for _, d := range detailsRe.FindAllString(b, -1) {
		if strings.Contains(d, "<a ") {
			t.Errorf("a <details> holds a link: %.120q", d)
		}
	}
}

func TestJobProgressBarColor(t *testing.T) {
	e := newEnv(t)
	ok := e.d.Jobs.Submit("test", "Ok job", func(ctx context.Context, r jobs.Reporter) error { return nil })
	bad := e.d.Jobs.Submit("test", "Bad job", func(ctx context.Context, r jobs.Reporter) error { return errors.New("x") })
	e.waitJob(t, ok.ID)
	e.waitJob(t, bad.ID)
	fill := regexp.MustCompile(`<div class="transition-all duration-300( is-ok)?" :class="\{ 'is-ok': status !== 'failed' && status !== 'aborted' \}"`)
	if m := fill.FindStringSubmatch(e.get("/jobs/" + ok.ID).Body.String()); m == nil || m[1] == "" {
		t.Errorf("done job: bar is not ok: %v", m)
	}
	if m := fill.FindStringSubmatch(e.get("/jobs/" + bad.ID).Body.String()); m == nil || m[1] != "" {
		t.Errorf("failed job: bar is not accent: %v", m)
	}
	up := e.get("/upload").Body.String()
	if !strings.Contains(up, `<div class="is-ok" :class="{ 'is-ok': it.state !== 'error' }"`) {
		t.Error("upload bar is not ok while running")
	}
}

func TestSmallMarkupFixes(t *testing.T) {
	e := newEnv(t)
	if b := e.get("/upload").Body.String(); !regexp.MustCompile(`<input x-ref="picker" type="file"[^>]*tabindex="-1"[^>]*class="sr-only"`).MatchString(b) {
		t.Error("hidden file input is focusable")
	}
	src, err := assets.ReadFile("templates/usb.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `class="min-w-0 max-w-full truncate font-mono text-[11px] text-dim">{{.Path}}`) {
		t.Error("USB device path can overflow")
	}
}

func TestCSSAndJSFixes(t *testing.T) {
	css, err := os.ReadFile("../../assets/input.css")
	if err != nil {
		t.Fatal(err)
	}
	c := string(css)
	tabs := regexp.MustCompile(`(?s)\.tabs > \* \{.*?\}`).FindString(c)
	if tabs == "" || strings.Contains(tabs, "margin-bottom") {
		t.Errorf(".tabs > * uses a negative margin: %q", tabs)
	}
	if !strings.Contains(c, "box-shadow: inset 0 -2px 0 var(--color-accent)") {
		t.Error("active tab has no inset underline")
	}
	busy := regexp.MustCompile(`(?s)button\[aria-busy="true"\],(.*?)\{`).FindStringSubmatch(c)
	if busy == nil || regexp.MustCompile(`(?m)(^|[\s,])\.htmx-request \.btn`).MatchString(busy[1]) || !strings.Contains(busy[1], "form.htmx-request .btn") || !strings.Contains(busy[1], ".btn.htmx-request") {
		t.Errorf("busy rule dims buttons of polled regions: %v", busy)
	}
	check := regexp.MustCompile(`(?s)\.check, \.radio \{.*?\}`).FindString(c)
	if !strings.Contains(check, "border: 1px solid var(--color-dim)") {
		t.Errorf(".check/.radio border is not dim: %q", check)
	}
	input := regexp.MustCompile(`(?s)\.input, \.select \{.*?\}`).FindString(c)
	if !strings.Contains(input, "border: 1px solid var(--color-line-field)") {
		t.Errorf(".input/.select border is not line-field: %q", input)
	}
	js, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), `el.tagName === "BUTTON" || el.tagName === "FORM"`) {
		t.Error("app.js marks any request element busy")
	}
}
