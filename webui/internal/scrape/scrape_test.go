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

package scrape

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"emulationstation/webui/internal/config"
	"emulationstation/webui/internal/es"
	"emulationstation/webui/internal/gamelist"
)

func autoName(rel, name string) bool { return name == "" || name == "Mario" }

func TestSupported(t *testing.T) {
	for _, tc := range []struct {
		sys  string
		want bool
	}{{"snes", true}, {"c64", true}, {"dreamcast", true}, {"c64music", false}} {
		if got := Supported(tc.sys); got != tc.want {
			t.Errorf("Supported(%q) = %v, want %v", tc.sys, got, tc.want)
		}
	}
}

func TestGatherArgsWithoutAccount(t *testing.T) {
	got := GatherArgs(&config.Config{}, "snes", []string{"a.sfc", "b.sfc"})
	want := []string{"-p", "snes", "-s", "screenscraper", "--flags", "unattend", "a.sfc", "b.sfc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
	for _, a := range got {
		if a == "-u" {
			t.Fatal("-u must not be set without an account")
		}
	}
}

func TestGatherArgsWithAccount(t *testing.T) {
	cfg := &config.Config{ScreenscraperUser: "me", ScreenscraperPass: "pw"}
	got := GatherArgs(cfg, "snes", nil)
	want := []string{"-p", "snes", "-s", "screenscraper", "-u", "me:pw", "--flags", "unattend"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestGenerateArgs(t *testing.T) {
	got := GenerateArgs("snes", "/out", "/media")
	want := []string{"-p", "snes", "-g", "/out", "-o", "/media", "--flags", "unattend"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func scraped() *gamelist.List {
	l := &gamelist.List{}
	l.Set("./Mario.sfc", map[string]string{"name": "Super Mario World", "desc": "A classic & more", "image": "/m/mario.png", "rating": "0.9"})
	return l
}

func TestMergeKeepsManualName(t *testing.T) {
	dst := &gamelist.List{}
	dst.Set("./Mario.sfc", map[string]string{"name": "Mein Mario", "players": "1"})
	Merge(dst, scraped(), "/r/snes", autoName)
	if v, _ := dst.Field("./Mario.sfc", "name"); v != "Mein Mario" {
		t.Fatalf("name = %q, manual name must be kept", v)
	}
	if v, _ := dst.Field("./Mario.sfc", "desc"); v != "A classic & more" {
		t.Fatalf("desc = %q", v)
	}
	if v, _ := dst.Field("./Mario.sfc", "image"); v != "/m/mario.png" {
		t.Fatalf("image = %q", v)
	}
	if v, _ := dst.Field("./Mario.sfc", "players"); v != "1" {
		t.Fatalf("players = %q, unrelated field must stay", v)
	}
}

func TestMergeReplacesAutoName(t *testing.T) {
	dst := &gamelist.List{}
	dst.Set("./Mario.sfc", map[string]string{"name": "Mario"})
	Merge(dst, scraped(), "/r/snes", autoName)
	if v, _ := dst.Field("./Mario.sfc", "name"); v != "Super Mario World" {
		t.Fatalf("name = %q, auto name must be replaced", v)
	}
	// unknown ROM in dst gets created
	dst2 := &gamelist.List{}
	Merge(dst2, scraped(), "/r/snes", autoName)
	if v, ok := dst2.Field("./Mario.sfc", "name"); !ok || v != "Super Mario World" {
		t.Fatalf("created name = %q, %v", v, ok)
	}
}

type fakeRunner struct {
	mu    sync.Mutex
	calls [][]string
	gen   func(args []string) error
}

func (f *fakeRunner) Run(ctx context.Context, w io.Writer, name string, args ...string) error {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	f.mu.Unlock()
	io.WriteString(w, "line one\nline two\n")
	for i, a := range args {
		if a == "-g" && f.gen != nil {
			return f.gen(args[i:])
		}
	}
	return nil
}

type rep struct {
	mu   sync.Mutex
	logs []string
	p    float64
}

func (r *rep) Progress(p float64) { r.mu.Lock(); r.p = p; r.mu.Unlock() }
func (r *rep) Logf(f string, a ...any) {
	r.mu.Lock()
	r.logs = append(r.logs, strings.TrimSpace(f))
	r.mu.Unlock()
}

func TestRunMergesIntoGamelist(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Roms = filepath.Join(home, "roms")
	cfg.Paths.ESConfigDir = filepath.Join(home, "es")
	cfg.Paths.RunDir = t.TempDir()
	os.MkdirAll(filepath.Join(cfg.Paths.Roms, "snes"), 0o755)
	os.WriteFile(filepath.Join(cfg.Paths.RunDir, "state"), []byte("stopped\n"), 0o644)

	run := &fakeRunner{gen: func(args []string) error {
		out := args[1]
		return gamelist.Write(filepath.Join(out, "gamelist.xml"), scraped())
	}}
	esc := &es.Controller{RunDir: cfg.Paths.RunDir, StopTimeout: time.Second}
	old := Binary
	Binary = "/fake/Skyscraper"
	defer func() { Binary = old }()

	if err := Run(context.Background(), &rep{}, run, esc, cfg, "snes", []string{"Mario.sfc"}); err != nil {
		t.Fatal(err)
	}
	if len(run.calls) != 2 || run.calls[0][0] != "/fake/Skyscraper" {
		t.Fatalf("calls = %v", run.calls)
	}
	l, err := gamelist.Read(gamelist.PathFor(cfg.Paths, "snes"))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := l.Field("./Mario.sfc", "name"); v != "Super Mario World" {
		t.Fatalf("name = %q", v)
	}
}

func TestNormRelKeepsSubFolders(t *testing.T) {
	dir := "/home/u/RetroPie/roms/psx"
	for in, want := range map[string]string{
		"./Mario.sfc":                     "./Mario.sfc",
		dir + "/Mario.sfc":                "./Mario.sfc",
		dir + "/FF7/FF7 (Disc 1).cue":     "./FF7/FF7 (Disc 1).cue",
		"./FF7/FF7 (Disc 1).cue":          "./FF7/FF7 (Disc 1).cue",
		"FF7/./FF7.cue":                   "./FF7/FF7.cue",
		"/elsewhere/psx/Game/Game.cue":    "./Game.cue",
		"./../../etc/passwd":              "./passwd",
		dir + "/../snes/x.sfc":            "./x.sfc",
		dir + "/Game/../../../etc/shadow": "./shadow",
	} {
		if got := normRel(in, dir); got != want {
			t.Errorf("normRel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunMergesSubFolderGame(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Roms = filepath.Join(home, "roms")
	cfg.Paths.ESConfigDir = filepath.Join(home, "es")
	cfg.Paths.RunDir = t.TempDir()
	romDir := filepath.Join(cfg.Paths.Roms, "psx")
	os.MkdirAll(filepath.Join(romDir, "FF7"), 0o755)
	os.WriteFile(filepath.Join(cfg.Paths.RunDir, "state"), []byte("stopped\n"), 0o644)
	glPath := gamelist.PathFor(cfg.Paths, "psx")
	start := &gamelist.List{}
	start.Set("./FF7/FF7.cue", map[string]string{"name": "FF7"})
	if err := gamelist.Write(glPath, start); err != nil {
		t.Fatal(err)
	}

	run := &fakeRunner{gen: func(args []string) error {
		l := &gamelist.List{}
		// Skyscraper writes absolute paths
		l.Set(romDir+"/FF7/FF7.cue", map[string]string{"name": "Final Fantasy VII", "desc": "RPG"})
		l.Set(romDir+"/Other.cue", map[string]string{"name": "Other", "desc": "x"})
		return gamelist.Write(filepath.Join(args[1], "gamelist.xml"), l)
	}}
	esc := &es.Controller{RunDir: cfg.Paths.RunDir, StopTimeout: time.Second}
	old := Binary
	Binary = "/fake/Skyscraper"
	defer func() { Binary = old }()

	if err := Run(context.Background(), &rep{}, run, esc, cfg, "psx", []string{"FF7/FF7.cue"}); err != nil {
		t.Fatal(err)
	}
	if got := run.calls[0][len(run.calls[0])-1]; got != filepath.Join(romDir, "FF7", "FF7.cue") {
		t.Fatalf("gather arg %q", got)
	}
	l, err := gamelist.Read(glPath)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := l.Field("./FF7/FF7.cue", "name"); v != "Final Fantasy VII" {
		t.Fatalf("name = %q", v)
	}
	if v, _ := l.Field("./FF7/FF7.cue", "desc"); v != "RPG" {
		t.Fatalf("desc = %q", v)
	}
	if _, ok := l.Field("./FF7.cue", "name"); ok {
		t.Fatal("a wrong top-level entry was added")
	}
	if _, ok := l.Field("./Other.cue", "name"); ok {
		t.Fatal("a game that was not asked for was merged")
	}

	// whole system: every scraped game lands on its own path
	if err := Run(context.Background(), &rep{}, run, esc, cfg, "psx", nil); err != nil {
		t.Fatal(err)
	}
	l, _ = gamelist.Read(glPath)
	if _, ok := l.Field("./FF7.cue", "name"); ok {
		t.Fatal("whole-system scrape added a top-level entry for a sub folder game")
	}
	if v, _ := l.Field("./Other.cue", "desc"); v != "x" {
		t.Fatalf("other desc = %q", v)
	}
}
