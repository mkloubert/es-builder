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
	"emulationstation/webui/internal/jobs"
	"io/fs"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDashboardPanels(t *testing.T) {
	e := newEnv(t)
	e.d.Sysinfo.DiskPath = t.TempDir()
	e.h = NewHandler(e.d)
	body := e.do("GET", "/", nil).Body.String()
	for _, want := range []string{
		`panel-head`, "Status", "Device", `class="bar`,
		`id="status"`, `hx-trigger="every 5s`, `hx-get="/partials/status"`, "confirming",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard lacks %q", want)
		}
	}
	if n := strings.Count(body, `class="bar`); n < 2 {
		t.Errorf("want bars for RAM and storage, got %d", n)
	}
}

func TestGameRowsUseMenu(t *testing.T) {
	e := newEnv(t)
	long := strings.Repeat("a", 196) + ".sfc"
	e.write(t, "snes/"+long, "x")
	body := e.do("GET", "/games/snes", nil).Body.String()
	for _, want := range []string{
		`truncate`, `title="` + long + `"`, ">Start<", `aria-haspopup="menu"`, `aria-expanded`,
		`role="menu"`, `role="menuitem"`, "Rename", "Scrape", "Delete",
		`hx-post="/games/snes/delete"`, `hx-confirm="Queue`, `>001<`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("games page lacks %q", want)
		}
	}
	if strings.Contains(body, "rounded-xl") || strings.Contains(body, "zinc-") {
		t.Error("old classes are still used")
	}
}

func TestGamesGridAndSegmented(t *testing.T) {
	e := newEnv(t)
	e.write(t, "snes/a.sfc", "x")
	for _, p := range []string{"/games", "/games?all=1"} {
		body := e.do("GET", p, nil).Body.String()
		for _, want := range []string{`href="/games"`, `href="/games?all=1"`, `aria-current`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", p, want)
			}
		}
	}
	all := e.do("GET", "/games?all=1", nil).Body.String()
	if !strings.Contains(all, `href="/games?all=1" aria-current="true"`) {
		t.Error("all segment not active")
	}
	some := e.do("GET", "/games", nil).Body.String()
	if !strings.Contains(some, `href="/games" aria-current="true"`) {
		t.Error("default segment not active")
	}
}

func TestGamesEmptyStates(t *testing.T) {
	e := newEnv(t)
	if b := e.do("GET", "/games", nil).Body.String(); !strings.Contains(b, `class="empty`) || !strings.Contains(b, "Upload ROMs") {
		t.Error("no empty state on /games")
	}
	if b := e.do("GET", "/games/snes", nil).Body.String(); !strings.Contains(b, `class="empty`) {
		t.Error("no empty state on system page")
	}
}

func TestDashboardLabelAndRoundDots(t *testing.T) {
	e := newEnv(t)
	e.setState(t, "es")
	b := e.do("GET", "/", nil).Body.String()
	if strings.Contains(b, "Now playing") || !strings.Contains(b, "rounded-full") {
		t.Error("es state: label must be neutral and dot round")
	}
	e.setState(t, "stopped")
	if strings.Contains(e.do("GET", "/", nil).Body.String(), "Now playing") {
		t.Error("stopped state shows Now playing")
	}
}

func TestGamesCountInsideSwappedList(t *testing.T) {
	e := newEnv(t)
	e.write(t, "snes/a.sfc", "x")
	e.write(t, "snes/b.sfc", "x")
	b := e.do("GET", "/games/snes", nil).Body.String()
	i := strings.Index(b, `id="games-list"`)
	if i < 0 || !strings.Contains(b[i:], "2 entries") {
		t.Error("count is not inside #games-list")
	}
	if strings.Contains(b[:i], "2 entries") {
		t.Error("count outside the swapped list")
	}
	if !strings.Contains(b, `x-data="rowMenu()"`) || strings.Contains(b, "go(d)") {
		t.Error("row menu must use the shared factory")
	}
}

func TestGamesGridNoFullWidthBorder(t *testing.T) {
	e := newEnv(t)
	e.write(t, "snes/a.sfc", "x")
	b := e.do("GET", "/games", nil).Body.String()
	if strings.Contains(b, "border-t border-line sm:grid-cols-2") {
		t.Error("grid draws a border across empty columns")
	}
}

func TestDeleteFocusHooks(t *testing.T) {
	e := newEnv(t)
	e.write(t, "snes/a.sfc", "x")
	b := e.do("GET", "/games/snes", nil).Body.String()
	if !strings.Contains(b, "focusAfterDelete()") {
		t.Error("delete form lacks the focus hook")
	}
	e.do("POST", "/games/snes/delete", url.Values{"rel": {"a.sfc"}})
	b = e.do("GET", "/partials/changes", nil).Body.String()
	if !strings.Contains(b, `id="apply-changes"`) {
		t.Error("Apply button lacks its id")
	}
}

func TestUploadDropzone(t *testing.T) {
	e := newEnv(t)
	b := e.do("GET", "/upload", nil).Body.String()
	for _, want := range []string{`dropzone`, "Drop ROMs or ZIP archives here", "Choose files", `x-data="uploader()"`, `btn-primary`} {
		if !strings.Contains(b, want) {
			t.Errorf("upload page lacks %q", want)
		}
	}
	if strings.Contains(b, "rounded-") || strings.Contains(b, "zinc-") {
		t.Error("old classes are still used")
	}
}

func TestJobPageLook(t *testing.T) {
	e := newEnv(t)
	job := e.d.Jobs.Submit("test", "Testjob", func(ctx context.Context, r jobs.Reporter) error { return nil })
	b := e.do("GET", "/jobs/"+job.ID, nil).Body.String()
	for _, want := range []string{`font-display`, `class="bar`, `role="progressbar"`, `<pre`, `x-ref="log"`} {
		if !strings.Contains(b, want) {
			t.Errorf("job page lacks %q", want)
		}
	}
	if strings.Contains(b, "rounded-") || strings.Contains(b, "zinc-") {
		t.Error("old classes are still used")
	}
}

func TestUSBRows(t *testing.T) {
	e := newEnv(t)
	e.withUSB(t, usbList2)
	b := e.get("/usb").Body.String()
	if n := strings.Count(b, `badge`); n < 4 {
		t.Errorf("want badges for size and file system of 2 sticks, got %d", n)
	}
	if n := strings.Count(b, `<button class="btn btn-primary"><svg`); n != 2 || strings.Count(b, "Import</button>") != 2 {
		t.Errorf("want 2 primary Import buttons, got %d", n)
	}
	if strings.Contains(b, "rounded-") || strings.Contains(b, "zinc-") {
		t.Error("old classes are still used")
	}
}

func TestUSBEmptyState(t *testing.T) {
	e := newEnv(t)
	e.withUSB(t, "")
	if b := e.get("/usb").Body.String(); !strings.Contains(b, `class="empty`) {
		t.Error("no empty state without sticks")
	}
}

func TestBiosBadges(t *testing.T) {
	e := newEnv(t)
	e.cfg.Paths.BIOS = t.TempDir()
	_ = os.WriteFile(filepath.Join(e.cfg.Paths.BIOS, "scph5501.bin"), []byte("wrong"), 0o644)
	_ = os.WriteFile(filepath.Join(e.cfg.Paths.BIOS, "neogeo.zip"), []byte("x"), 0o644)
	b := e.get("/bios").Body.String()
	for _, want := range []string{"badge-ok", "badge-warn", "panel-head"} {
		if !strings.Contains(b, want) {
			t.Errorf("bios page lacks %q", want)
		}
	}
	// The text is lower case in the HTML and shown in capitals by the badge style.
	if !strings.Contains(b, `badge-warn">missing<`) {
		t.Error("no warn badge with the text missing")
	}
}

func TestSystemTabs(t *testing.T) {
	d, _ := newDeps(t)
	cpuRoot(t, &d, pi4Model, "1000000")
	b := serve(NewHandler(d), authed(httptest.NewRequest("GET", "/system", nil))).Body.String()
	if !strings.Contains(b, `class="tabs`) || !strings.Contains(b, `role="tablist"`) {
		t.Error("no tab bar")
	}
	if n := strings.Count(b, `role="tab"`); n != 6 {
		t.Errorf("want 6 tabs, got %d", n)
	}
	if n := strings.Count(b, `role="tabpanel"`); n != 6 {
		t.Errorf("want 6 tab panels, got %d", n)
	}
	if !strings.Contains(b, `:aria-selected=`) || !strings.Contains(b, `x-show="tab === 'power'"`) {
		t.Error("tab logic is gone")
	}
}

func TestSettingsPanels(t *testing.T) {
	e := newEnv(t)
	b := e.get("/settings").Body.String()
	if n := strings.Count(b, `class="panel-head`); n != 2 {
		t.Errorf("want 2 panel heads, got %d", n)
	}
	if !strings.Contains(b, "label-mono") {
		t.Error("no mono labels")
	}
}

func TestControllersPanels(t *testing.T) {
	e := newEnv(t)
	b := e.get("/controllers").Body.String()
	if n := strings.Count(b, `class="panel-head`); n < 3 {
		t.Errorf("want numbered panels, got %d heads", n)
	}
	if !strings.Contains(b, `class="empty`) {
		t.Error("no empty state")
	}
}

func TestNoOldLookLeft(t *testing.T) {
	old := []string{"rounded-xl", "rounded-lg", "rounded-2xl", "bg-zinc-900", "zinc-", "emerald-", "amber-", "red-500"}
	err := fs.WalkDir(assets, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := assets.ReadFile(p)
		if err != nil {
			return err
		}
		for _, o := range old {
			if strings.Contains(string(b), o) {
				t.Errorf("%s still contains %q", p, o)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
