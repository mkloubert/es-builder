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
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"emulationstation/webui/internal/config"
	"emulationstation/webui/internal/es"
	"emulationstation/webui/internal/jobs"
	"emulationstation/webui/internal/priv"
	"emulationstation/webui/internal/roms"
	"emulationstation/webui/internal/sysinfo"
	"emulationstation/webui/internal/systems"
)

type env struct {
	d    Deps
	cfg  *config.Config
	h    http.Handler
	roms string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	hash, err := config.HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cfg := &config.Config{WebUser: "u", WebPasswordHash: hash,
		MaxUploadBytes: 4 << 30, MaxExtractBytes: 8 << 30, MaxZipEntries: 10000}
	cfg.Paths.Roms = filepath.Join(home, "roms")
	cfg.Paths.ESConfigDir = filepath.Join(home, "es")
	cfg.Paths.Staging = filepath.Join(home, "staging")
	cfg.Paths.RunDir = t.TempDir()
	syss := []systems.System{
		{Name: "snes", FullName: "Super Nintendo", Extensions: []string{".sfc", ".smc", ".zip"}},
		{Name: "psx", FullName: "Sony PlayStation", Extensions: []string{".cue", ".bin", ".m3u"}},
		{Name: "nes", FullName: "Nintendo NES", Extensions: []string{".nes"}},
		{Name: "ports", FullName: "Ports", Extensions: []string{".sh"}},
	}
	for _, s := range syss[:2] {
		if err := os.MkdirAll(filepath.Join(cfg.Paths.Roms, s.Name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	q, err := jobs.NewQueue("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { q.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	d := Deps{
		Config:  func() *config.Config { return cfg },
		Systems: func() []systems.System { return syss },
		Stager:  roms.NewStager(cfg, syss),
		ES:      &es.Controller{RunDir: cfg.Paths.RunDir, StopTimeout: 2 * time.Second},
		Jobs:    q,
		Runner:  priv.ExecRunner{},
		Sysinfo: sysinfo.Collector{Root: "../sysinfo/testdata/root"},
	}
	return &env{d: d, cfg: cfg, h: NewHandler(d), roms: cfg.Paths.Roms}
}

func (e *env) setState(t *testing.T, s string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.cfg.Paths.RunDir, "state"), []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e *env) write(t *testing.T, rel, content string) string {
	t.Helper()
	p := filepath.Join(e.roms, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *env) do(method, path string, form url.Values) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("HX-Request", "true")
	req.SetBasicAuth("u", "pw")
	return serve(e.h, req)
}

// fakeWrapper imitates the ES wrapper: hold -> stopped, no hold -> es.
type fakeESWrapper struct{ stopped, started atomic.Bool }

func (e *env) fakeES(t *testing.T) *fakeESWrapper {
	t.Helper()
	e.setState(t, "es\n")
	return e.runWrapper(t)
}

// runWrapper starts the wrapper loop on the current state.
func (e *env) runWrapper(t *testing.T) *fakeESWrapper {
	t.Helper()
	f := &fakeESWrapper{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	dir := e.cfg.Paths.RunDir
	set := func(s string) {
		tmp := filepath.Join(dir, "state.tmp")
		_ = os.WriteFile(tmp, []byte(s), 0o644)
		_ = os.Rename(tmp, filepath.Join(dir, "state"))
	}
	go func() {
		for ctx.Err() == nil {
			time.Sleep(5 * time.Millisecond)
			_, herr := os.Stat(filepath.Join(dir, "hold"))
			b, _ := os.ReadFile(filepath.Join(dir, "state"))
			switch {
			case herr == nil && string(b) == "es\n":
				set("stopped\n")
				f.stopped.Store(true)
			case herr != nil && string(b) == "stopped\n" && f.stopped.Load():
				set("es\n")
				f.started.Store(true)
			}
		}
	}()
	return f
}

// spawnGame starts a fake game process and registers it as running.
func (e *env) spawnGame(t *testing.T, system, rom string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); <-exited })
	dir := e.cfg.Paths.RunDir
	_ = os.WriteFile(filepath.Join(dir, "game.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	e.setState(t, "game\t"+system+"\t"+rom+"\n")
	e.runWrapper(t)
	go func() { // wrapper: game ended -> ES again, or stopped while hold exists
		<-exited
		st := "es\n"
		if _, err := os.Stat(filepath.Join(dir, "hold")); err == nil {
			st = "stopped\n"
		}
		_ = os.WriteFile(filepath.Join(dir, "state"), []byte(st), 0o644)
	}()
	return cmd
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

const glMario = `<?xml version="1.0"?>
<gameList>
  <game>
    <path>./Mario.sfc</path>
    <name>Mario</name>
    <desc>Hallo</desc>
  </game>
</gameList>
`

func TestRenameIsPendingUntilApply(t *testing.T) {
	e := newEnv(t)
	e.write(t, "snes/Mario.sfc", "rom")
	gl := e.write(t, "snes/gamelist.xml", glMario)
	before, _ := os.ReadFile(gl)
	fake := e.fakeES(t)

	w := e.do("POST", "/games/snes/rename", url.Values{"rel": {"Mario.sfc"}, "name": {"Super Mario"}})
	if w.Code != 200 {
		t.Fatalf("rename status = %d: %s", w.Code, w.Body)
	}
	if after, _ := os.ReadFile(gl); !bytes.Equal(before, after) {
		t.Fatal("gamelist changed before apply")
	}
	page := e.do("GET", "/games/snes", nil)
	if !strings.Contains(page.Body.String(), "Super Mario") || !strings.Contains(page.Body.String(), "Apply") {
		t.Fatalf("pending change not shown: %s", page.Body)
	}

	w = e.do("POST", "/changes/apply", url.Values{})
	if w.Code != 200 {
		t.Fatalf("apply status = %d: %s", w.Code, w.Body)
	}
	after, _ := os.ReadFile(gl)
	if !strings.Contains(string(after), "<name>Super Mario</name>") || !strings.Contains(string(after), "<desc>Hallo</desc>") {
		t.Fatalf("gamelist = %s", after)
	}
	waitFor(t, "ES restart", fake.started.Load)
	if !fake.stopped.Load() {
		t.Fatal("ES was never stopped")
	}
	if strings.Contains(e.do("GET", "/partials/changes", nil).Body.String(), "Super Mario") {
		t.Fatal("changes not cleared")
	}
}

func TestApplyDuringGameAsksForConfirmation(t *testing.T) {
	e := newEnv(t)
	rom := e.write(t, "snes/Mario.sfc", "rom")
	e.write(t, "snes/gamelist.xml", glMario)
	cmd := e.spawnGame(t, "snes", rom)

	if w := e.do("POST", "/games/snes/delete", url.Values{"rel": {"Mario.sfc"}}); w.Code != 200 {
		t.Fatalf("delete status = %d", w.Code)
	}
	w := e.do("POST", "/changes/apply", url.Values{})
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Stop the running game and continue?") || !strings.Contains(body, `name="force"`) {
		t.Fatalf("dialog markup missing: %s", body)
	}
	if syscall.Kill(cmd.Process.Pid, 0) != nil {
		t.Fatal("game stopped without confirmation")
	}
	if _, err := os.Stat(rom); err != nil {
		t.Fatal("ROM deleted without confirmation")
	}

	w = e.do("POST", "/changes/apply", url.Values{"force": {"1"}})
	if w.Code != 200 {
		t.Fatalf("forced apply status = %d: %s", w.Code, w.Body)
	}
	waitFor(t, "game stopped", func() bool { return syscall.Kill(cmd.Process.Pid, 0) != nil })
	if _, err := os.Stat(rom); !os.IsNotExist(err) {
		t.Fatal("ROM still exists after apply")
	}
}

func TestDeleteRejectsTraversal(t *testing.T) {
	e := newEnv(t)
	cases := []struct{ op, rel string }{
		{"delete", "../../x"}, {"delete", "a/../b.sfc"}, {"delete", ".."}, {"delete", ""},
		{"delete", "/etc/passwd"}, {"delete", "./a.sfc"}, {"delete", "a//b.sfc"}, {"delete", ".hidden/a.sfc"},
		{"rename", "../../x"}, {"launch", "../../x"}, {"scrape", "../../x"},
	}
	for _, c := range cases {
		w := e.do("POST", "/games/snes/"+c.op, url.Values{"rel": {c.rel}, "name": {"x"}})
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s rel=%q: status = %d, want 400", c.op, c.rel, w.Code)
		}
	}
	if w := e.do("POST", "/games/snes/delete", url.Values{"rel": {"gibtsnicht.sfc"}}); w.Code != http.StatusNotFound {
		t.Errorf("missing file: status = %d, want 404", w.Code)
	}
	if w := e.do("POST", "/games/..%2Fetc/delete", url.Values{"rel": {"x"}}); w.Code != http.StatusNotFound {
		t.Errorf("unknown system: status = %d, want 404", w.Code)
	}
}

func TestBrokenGamelistNotOverwritten(t *testing.T) {
	e := newEnv(t)
	e.write(t, "snes/Mario.sfc", "rom")
	gl := e.write(t, "snes/gamelist.xml", "<gameList><game><path>./Mario.sfc</path><name>Mar")
	before, _ := os.ReadFile(gl)
	e.setState(t, "stopped\n")

	if w := e.do("POST", "/games/snes/rename", url.Values{"rel": {"Mario.sfc"}, "name": {"Neu"}}); w.Code != 200 {
		t.Fatalf("rename status = %d", w.Code)
	}
	w := e.do("POST", "/changes/apply", url.Values{})
	if w.Code < 400 {
		t.Fatalf("apply status = %d, want an error", w.Code)
	}
	if hdr := w.Header().Get("HX-Trigger"); !strings.Contains(hdr, "Gamelist") {
		t.Fatalf("no German error toast: %q", hdr)
	}
	after, _ := os.ReadFile(gl)
	if !bytes.Equal(before, after) {
		t.Fatalf("broken gamelist modified: %q", after)
	}
	if _, err := os.Stat(gl + ".bak"); err == nil {
		t.Fatal("backup written for broken gamelist")
	}
}

func TestDeleteIsPendingThenApplied(t *testing.T) {
	e := newEnv(t)
	rom := e.write(t, "snes/Mario.sfc", "rom")
	gl := e.write(t, "snes/gamelist.xml", glMario)
	e.setState(t, "stopped\n")
	e.do("POST", "/games/snes/delete", url.Values{"rel": {"Mario.sfc"}})
	if _, err := os.Stat(rom); err != nil {
		t.Fatal("deleted before apply")
	}
	if w := e.do("POST", "/changes/apply", url.Values{}); w.Code != 200 {
		t.Fatalf("apply = %d", w.Code)
	}
	if _, err := os.Stat(rom); !os.IsNotExist(err) {
		t.Fatal("ROM not deleted")
	}
	if b, _ := os.ReadFile(gl); strings.Contains(string(b), "Mario.sfc") {
		t.Fatalf("gamelist entry remains: %s", b)
	}
}

func TestDiscardClearsChanges(t *testing.T) {
	e := newEnv(t)
	e.write(t, "snes/Mario.sfc", "rom")
	e.do("POST", "/games/snes/delete", url.Values{"rel": {"Mario.sfc"}})
	if w := e.do("POST", "/changes/discard", url.Values{}); w.Code != 200 {
		t.Fatalf("discard = %d", w.Code)
	}
	if strings.Contains(e.do("GET", "/partials/changes", nil).Body.String(), "Apply") {
		t.Fatal("changes remain")
	}
}

func TestGamesOverviewCountsSkipCompanions(t *testing.T) {
	e := newEnv(t)
	e.write(t, "snes/a.sfc", "x")
	e.write(t, "snes/b.sfc", "x")
	e.write(t, "snes/gamelist.xml", glMario)
	e.write(t, "snes/.hidden", "x")
	e.write(t, "psx/Game.cue", "FILE \"Game.bin\" BINARY\n")
	e.write(t, "psx/Game.bin", "x")
	body := e.do("GET", "/games", nil).Body.String()
	if !strings.Contains(body, "Super Nintendo") || !strings.Contains(body, "Sony PlayStation") {
		t.Fatalf("systems missing: %s", body)
	}
	if strings.Contains(body, "Nintendo NES") {
		t.Fatal("empty system shown without toggle")
	}
	if !strings.Contains(e.do("GET", "/games?all=1", nil).Body.String(), "Nintendo NES") {
		t.Fatal("toggle does not show all systems")
	}
	psx := e.do("GET", "/games/psx", nil).Body.String()
	if !strings.Contains(psx, "Game.cue") || strings.Contains(psx, "Game.bin") {
		t.Fatalf("companion listing wrong: %s", psx)
	}
	if !strings.Contains(e.do("GET", "/games/snes?q=a.sf", nil).Body.String(), "a.sfc") {
		t.Fatal("search broken")
	}
}

func TestLaunch(t *testing.T) {
	e := newEnv(t)
	rom := e.write(t, "snes/Mario.sfc", "rom")
	e.setState(t, "es\n")
	if w := e.do("POST", "/games/snes/launch", url.Values{"rel": {"Mario.sfc"}}); w.Code != 200 {
		t.Fatalf("launch = %d: %s", w.Code, w.Body)
	}
	b, _ := os.ReadFile(filepath.Join(e.cfg.Paths.RunDir, "launch"))
	if string(b) != "snes\t"+rom+"\n" {
		t.Fatalf("launch file = %q", b)
	}
}

func TestLaunchDuringGameAsksThenForces(t *testing.T) {
	e := newEnv(t)
	rom := e.write(t, "snes/Mario.sfc", "rom")
	cmd := e.spawnGame(t, "snes", rom)
	w := e.do("POST", "/games/snes/launch", url.Values{"rel": {"Mario.sfc"}})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "Stop the running game and start?") {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}
	w = e.do("POST", "/games/snes/launch", url.Values{"rel": {"Mario.sfc"}, "force": {"1"}})
	if w.Code != 200 {
		t.Fatalf("forced launch = %d: %s", w.Code, w.Body)
	}
	waitFor(t, "game stopped", func() bool { return syscall.Kill(cmd.Process.Pid, 0) != nil })
}

func TestFailingApplyRestartsRunningES(t *testing.T) {
	e := newEnv(t)
	e.write(t, "snes/Mario.sfc", "rom")
	e.write(t, "snes/Zelda.sfc", "rom")
	e.write(t, "snes/gamelist.xml", glMario)
	fake := e.fakeES(t)
	e.do("POST", "/games/snes/delete", url.Values{"rel": {"Mario.sfc"}})
	// make the delete fail while ES is stopped: the ROM becomes a directory
	if err := os.Remove(filepath.Join(e.roms, "snes", "Mario.sfc")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(e.roms, "snes", "Mario.sfc"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := e.do("POST", "/changes/apply", url.Values{})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if hdr := w.Header().Get("HX-Trigger"); strings.Contains(hdr, "unsafe") || !strings.Contains(hdr, "Nothing was chan") {
		t.Fatalf("toast = %q", hdr)
	}
	waitFor(t, "ES restart", fake.started.Load)
	if _, err := os.Stat(filepath.Join(e.cfg.Paths.RunDir, "hold")); err == nil {
		t.Fatal("hold left behind after failure")
	}
}

func TestCompanionNotEditable(t *testing.T) {
	e := newEnv(t)
	e.write(t, "psx/Game.cue", "FILE \"Game.bin\" BINARY\n")
	e.write(t, "psx/Game.bin", "x")
	e.write(t, "psx/gamelist.xml", "<gameList/>")
	if w := e.do("POST", "/games/psx/delete", url.Values{"rel": {"Game.bin"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("companion delete = %d", w.Code)
	}
	if w := e.do("POST", "/games/psx/delete", url.Values{"rel": {"gamelist.xml"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("gamelist delete = %d", w.Code)
	}
}

// --- games in sub folders (a stick import keeps them) ---

func TestSubFolderGameIsListedAndEditable(t *testing.T) {
	e := newEnv(t)
	cue := e.write(t, "psx/Final Fantasy VII/FF7 (Disc 1).cue", "FILE \"FF7 (Disc 1).bin\" BINARY\n")
	bin := e.write(t, "psx/Final Fantasy VII/FF7 (Disc 1).bin", "x")
	e.write(t, "psx/Final Fantasy VII/cover.png", "png") // not a psx file
	e.write(t, "psx/Top.cue", "")
	gl := e.write(t, "psx/gamelist.xml", "<gameList/>")
	rel := "Final Fantasy VII/FF7 (Disc 1).cue"

	body := e.do("GET", "/games", nil).Body.String()
	if !strings.Contains(body, "Sony PlayStation") {
		t.Fatalf("psx missing: %s", body)
	}
	page := e.do("GET", "/games/psx", nil).Body.String()
	if !strings.Contains(page, `value="`+html.EscapeString(rel)+`"`) || strings.Contains(page, "Disc 1).bin") || strings.Contains(page, "cover.png") {
		t.Fatalf("listing wrong: %s", page)
	}
	if files, _ := listROMs(e.cfg, systems.System{Name: "psx", Extensions: []string{".cue", ".bin", ".m3u"}}); !reflect.DeepEqual(files, []string{"Top.cue", rel}) {
		t.Fatalf("files = %q", files)
	}

	// launch uses the full path
	e.setState(t, "es\n")
	if w := e.do("POST", "/games/psx/launch", url.Values{"rel": {rel}}); w.Code != 200 {
		t.Fatalf("launch = %d: %s", w.Code, w.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(e.cfg.Paths.RunDir, "launch")); string(b) != "psx\t"+cue+"\n" {
		t.Fatalf("launch file = %q", b)
	}
	os.Remove(filepath.Join(e.cfg.Paths.RunDir, "launch"))

	// rename writes the gamelist entry with the folder
	e.setState(t, "stopped\n")
	if w := e.do("POST", "/games/psx/rename", url.Values{"rel": {rel}, "name": {"Final Fantasy VII"}}); w.Code != 200 {
		t.Fatalf("rename = %d: %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/changes/apply", url.Values{}); w.Code != 200 {
		t.Fatalf("apply = %d: %s", w.Code, w.Body)
	}
	if b, _ := os.ReadFile(gl); !strings.Contains(string(b), "<path>./Final Fantasy VII/FF7 (Disc 1).cue</path>") || !strings.Contains(string(b), "<name>Final Fantasy VII</name>") {
		t.Fatalf("gamelist = %s", b)
	}
	if !strings.Contains(e.do("GET", "/games/psx", nil).Body.String(), `title="Final Fantasy VII"`) {
		t.Fatal("new name not shown")
	}

	// the companion in the folder is not editable
	if w := e.do("POST", "/games/psx/delete", url.Values{"rel": {"Final Fantasy VII/FF7 (Disc 1).bin"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("companion delete = %d", w.Code)
	}

	// delete removes the companions; the folder stays while it holds cover.png
	if w := e.do("POST", "/games/psx/delete", url.Values{"rel": {rel}}); w.Code != 200 {
		t.Fatalf("delete = %d: %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/changes/apply", url.Values{}); w.Code != 200 {
		t.Fatalf("apply = %d: %s", w.Code, w.Body)
	}
	for _, p := range []string{cue, bin} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s still exists", p)
		}
	}
	if _, err := os.Stat(filepath.Join(e.roms, "psx", "Final Fantasy VII", "cover.png")); err != nil {
		t.Fatal("a folder that is not empty was removed")
	}
	if b, _ := os.ReadFile(gl); strings.Contains(string(b), "FF7") {
		t.Fatalf("gamelist entry remains: %s", b)
	}
}

func TestDeleteSubFolderGameRemovesEmptyFolder(t *testing.T) {
	e := newEnv(t)
	e.write(t, "psx/A/B/Game.cue", "FILE \"Game.bin\" BINARY\n")
	e.write(t, "psx/A/B/Game.bin", "x")
	e.write(t, "psx/gamelist.xml", "<gameList/>")
	e.setState(t, "stopped\n")
	if w := e.do("POST", "/games/psx/delete", url.Values{"rel": {"A/B/Game.cue"}}); w.Code != 200 {
		t.Fatalf("delete = %d: %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/changes/apply", url.Values{}); w.Code != 200 {
		t.Fatalf("apply = %d: %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(e.roms, "psx", "A")); !os.IsNotExist(err) {
		t.Fatal("empty folders were not removed")
	}
	if _, err := os.Stat(filepath.Join(e.roms, "psx")); err != nil {
		t.Fatal("the system folder must stay")
	}
}

func TestSubFolderThroughSymlinkIsRejected(t *testing.T) {
	e := newEnv(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "Game.sfc"), []byte("x"), 0o644)
	if err := os.Symlink(outside, filepath.Join(e.roms, "snes", "Link")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.do("GET", "/games/snes", nil).Body.String(), "Link/Game.sfc") {
		t.Fatal("a game behind a symlink is listed")
	}
	for _, op := range []string{"delete", "rename", "launch", "scrape"} {
		w := e.do("POST", "/games/snes/"+op, url.Values{"rel": {"Link/Game.sfc"}, "name": {"x"}})
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", op, w.Code)
		}
	}
	// also a delete that was queued before the folder became a link
	cfg := *e.cfg
	if err := roms.DeleteROM(&cfg, "snes", "Link/Game.sfc"); !errors.Is(err, roms.ErrUnsafePath) {
		t.Fatalf("DeleteROM err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "Game.sfc")); err != nil {
		t.Fatal("a file outside the ROM folder was deleted")
	}
}

func TestScrapeListsTheFolderOnce(t *testing.T) {
	e := newEnv(t)
	form := url.Values{}
	for i := 0; i < 30; i++ {
		n := fmt.Sprintf("Game %02d.sfc", i)
		e.write(t, "snes/"+n, "x")
		form.Add("rel", n)
	}
	calls := 0
	old := listROMs
	listROMs = func(cfg *config.Config, sys systems.System) ([]string, error) {
		calls++
		return old(cfg, sys)
	}
	t.Cleanup(func() { listROMs = old })
	w := e.do("POST", "/games/snes/scrape", form)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("HX-Redirect"), "/jobs/") {
		t.Fatalf("scrape = %d %q", w.Code, w.Header().Get("HX-Trigger"))
	}
	if calls != 1 {
		t.Fatalf("folder listed %d times", calls)
	}
}

func TestListHidesFilesOfDescriptors(t *testing.T) {
	e := newEnv(t)
	// a top-level .m3u lists discs in a sub folder
	e.write(t, "psx/FF7.m3u", "FF7/FF7 (Disc 1).cue\r\nFF7\\FF7 (Disc 2).cue\n../outside.cue\n")
	e.write(t, "psx/FF7/FF7 (Disc 1).cue", "FILE \"FF7 (Disc 1).bin\" BINARY\n")
	e.write(t, "psx/FF7/FF7 (Disc 1).bin", "x")
	e.write(t, "psx/FF7/FF7 (Disc 2).cue", "FILE \"FF7 (Disc 2).bin\" BINARY\n")
	e.write(t, "psx/FF7/FF7 (Disc 2).bin", "x")
	// like in ES, a .gdi that psx does not accept does not hide its tracks
	e.write(t, "psx/Other/Game.gdi", "2\n1 0 4 2352 track01.bin 0\n2 600 0 2352 track02.bin 0\n")
	e.write(t, "psx/Other/track01.bin", "x")
	e.write(t, "psx/Other/track02.bin", "x")
	files, err := listROMs(e.cfg, systems.System{Name: "psx", Extensions: []string{".cue", ".bin", ".m3u"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(files, []string{"FF7.m3u", "Other/track01.bin", "Other/track02.bin"}) {
		t.Fatalf("files = %q", files)
	}
	// in a system without .m3u, the .m3u hides nothing: the discs are listed
	// (top-level files are listed without an extension filter, as before)
	files, err = listROMs(e.cfg, systems.System{Name: "psx", Extensions: []string{".cue"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(files, []string{"FF7.m3u", "FF7/FF7 (Disc 1).cue", "FF7/FF7 (Disc 2).cue"}) {
		t.Fatalf("files = %q", files)
	}
}
