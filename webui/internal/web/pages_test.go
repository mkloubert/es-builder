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
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"emulationstation/webui/internal/bluetooth"
	"emulationstation/webui/internal/config"
	"emulationstation/webui/internal/priv"
)

// fakeRunner records commands; hook is called on every Run.
type fakeRunner struct {
	mu    sync.Mutex
	calls []string
	out   map[string]string
	hook  func(args []string)
}

func (f *fakeRunner) Run(ctx context.Context, stdout io.Writer, name string, args ...string) error {
	line := strings.Join(append([]string{name}, args...), " ")
	f.mu.Lock()
	f.calls = append(f.calls, line)
	out := f.out[line]
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		hook(args)
	}
	if stdout != nil {
		_, _ = io.WriteString(stdout, out)
	}
	return nil
}

func (f *fakeRunner) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (e *env) withRunner(f *fakeRunner) {
	e.d.Runner = f
	e.d.Priv = priv.Priv{R: f, Helper: "/h"}
	e.h = NewHandler(e.d)
}

// withHolder makes SetConfig persist to a web.conf and update Config().
func (e *env) withHolder(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "web.conf")
	var mu sync.RWMutex
	cur := e.cfg
	e.d.Config = func() *config.Config { mu.RLock(); defer mu.RUnlock(); return cur }
	e.d.SetConfig = func(c *config.Config) error {
		if err := c.Save(path); err != nil {
			return err
		}
		mu.Lock()
		cur = c
		mu.Unlock()
		return nil
	}
	e.d.UpdateConfig = func(fn func(*config.Config) error) error {
		mu.Lock()
		defer mu.Unlock()
		c := *cur
		if err := fn(&c); err != nil {
			return err
		}
		if err := c.Save(path); err != nil {
			return err
		}
		cur = &c
		return nil
	}
	e.d.LogW = io.Discard
	e.h = NewHandler(e.d)
	return path
}

func (e *env) get(path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	req.SetBasicAuth("u", "pw")
	return serve(e.h, req)
}

func (e *env) multipart(path string, fields map[string]string, fname, content string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	for _, name := range strings.Split(fname, ",") {
		fw, _ := mw.CreateFormFile("file", name)
		_, _ = io.WriteString(fw, content)
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	req.SetBasicAuth("u", "pw")
	return serve(e.h, req)
}

func TestBiosPageShowsStates(t *testing.T) {
	e := newEnv(t)
	e.cfg.Paths.BIOS = t.TempDir()
	_ = os.WriteFile(filepath.Join(e.cfg.Paths.BIOS, "scph5501.bin"), []byte("wrong"), 0o644)
	_ = os.WriteFile(filepath.Join(e.cfg.Paths.BIOS, "neogeo.zip"), []byte("x"), 0o644)
	w := e.get("/bios")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"Sony PlayStation", "wrong checksum", ">OK<", "missing", "optional", "scph5501.bin", "dc/dc_boot.bin"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestBiosUploadStoresFilesAndDreamcast(t *testing.T) {
	e := newEnv(t)
	e.cfg.Paths.BIOS = t.TempDir()
	w := e.multipart("/bios", nil, "neogeo.zip,dc_boot.bin", "data")
	if w.Code != 200 || w.Header().Get("HX-Redirect") != "/bios?saved=2" {
		t.Fatalf("code=%d redirect=%q", w.Code, w.Header().Get("HX-Redirect"))
	}
	for _, f := range []string{"neogeo.zip", "dc/dc_boot.bin"} {
		if _, err := os.Stat(filepath.Join(e.cfg.Paths.BIOS, f)); err != nil {
			t.Error(err)
		}
	}
	w = e.multipart("/bios", map[string]string{"target": "dc"}, "other.bin", "d")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if _, err := os.Stat(filepath.Join(e.cfg.Paths.BIOS, "dc", "other.bin")); err != nil {
		t.Error(err)
	}
	if w := e.multipart("/bios", nil, "../evil", "d"); w.Code != 200 {
		t.Fatalf("../evil: %d", w.Code)
	}
	// Go's multipart reader reduces the name to its base: stored inside the BIOS dir.
	if _, err := os.Stat(filepath.Join(e.cfg.Paths.BIOS, "evil")); err != nil {
		t.Fatal("evil not stored as plain file name:", err)
	}
	if _, err := os.Stat(filepath.Join(e.cfg.Paths.BIOS, "..", "evil")); err == nil {
		t.Fatal("path traversal")
	}
}

const inputFixture = "../controllers/testdata/es_input.cfg"

func (e *env) controllerSetup(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(inputFixture)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	e.cfg.Paths.ESConfigDir = dir
	e.cfg.Paths.ESInputCfg = filepath.Join(dir, "es_input.cfg")
	e.cfg.Paths.InputHook = "/hook.sh"
	if err := os.WriteFile(e.cfg.Paths.ESInputCfg, b, 0o644); err != nil {
		t.Fatal(err)
	}
	e.d.SysRoot = "../controllers/testdata/sys"
	e.h = NewHandler(e.d)
	return e.cfg.Paths.ESInputCfg
}

func TestControllersPageMarksUnconfigured(t *testing.T) {
	e := newEnv(t)
	e.controllerSetup(t)
	w := e.get("/controllers")
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "Microsoft X-Box 360 pad") {
		t.Fatalf("code=%d", w.Code)
	}
	if strings.Count(body, "not configured") != 1 || !strings.Contains(body, ">configured<") {
		t.Fatalf("expected one unconfigured and one configured device")
	}
	if !strings.Contains(body, "030000005e0400008e02000014010000") || !strings.Contains(body, "bluetooth") {
		t.Fatal("GUID/bus missing")
	}
}

func TestControllerExportAndReset(t *testing.T) {
	e := newEnv(t)
	cfgPath := e.controllerSetup(t)
	wr := e.fakeES(t)
	w := e.get("/controllers/export?guid=030000005e0400008e02000014010000")
	cd := w.Header().Get("Content-Disposition")
	if w.Code != 200 || !strings.Contains(cd, `filename="Microsoft X-Box 360 pad.xml"`) || !strings.Contains(cd, "filename*=UTF-8''Microsoft%20X-Box%20360%20pad.xml") {
		t.Fatalf("code=%d cd=%q", w.Code, cd)
	}
	if !strings.Contains(w.Body.String(), `deviceName="Microsoft X-Box 360 pad"`) {
		t.Fatal(w.Body.String())
	}
	if w := e.get("/controllers/export?guid=nope"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	w = e.do("POST", "/controllers/reset", url.Values{"guid": {"030000005e0400008e02000014010000"}})
	if w.Code != 200 {
		t.Fatalf("reset: %d %s", w.Code, w.Header().Get("HX-Trigger"))
	}
	b, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(b), "X-Box 360") || !strings.Contains(string(b), "Keyboard") {
		t.Fatalf("reset result: %s", b)
	}
	waitFor(t, "ES restarted", wr.started.Load) // let the fake wrapper settle before cleanup
}

func TestControllerImportRunsHookInsideESStop(t *testing.T) {
	e := newEnv(t)
	cfgPath := e.controllerSetup(t)
	wr := e.fakeES(t)
	var holdDuringHook, stoppedDuringHook bool
	fr := &fakeRunner{}
	fr.hook = func(args []string) {
		_, err := os.Stat(filepath.Join(e.cfg.Paths.RunDir, "hold"))
		holdDuringHook = err == nil
		b, _ := os.ReadFile(filepath.Join(e.cfg.Paths.RunDir, "state"))
		stoppedDuringHook = string(b) == "stopped\n"
	}
	e.withRunner(fr)
	xml := `<?xml version="1.0"?>` + "\n" + `<inputConfig type="joystick" deviceName="New Pad" deviceGUID="0300aaaa"><input name="a" type="button" id="1" value="1"/></inputConfig>`
	w := e.multipart("/controllers/import", nil, "pad.xml", xml)
	if w.Code != 200 || w.Header().Get("HX-Redirect") != "/controllers" {
		t.Fatalf("code=%d trigger=%q", w.Code, w.Header().Get("HX-Trigger"))
	}
	if !holdDuringHook || !stoppedDuringHook {
		t.Fatalf("hook ran outside ES stop: hold=%v stopped=%v", holdDuringHook, stoppedDuringHook)
	}
	if calls := fr.called(); len(calls) != 1 || calls[0] != "bash /hook.sh" {
		t.Fatalf("calls=%v", calls)
	}
	b, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(b), "New Pad") {
		t.Fatal("not imported")
	}
	if _, err := os.Stat(filepath.Join(e.cfg.Paths.RunDir, "hold")); err == nil {
		t.Fatal("hold left behind")
	}
	waitFor(t, "ES restarted", wr.started.Load)
	if w := e.multipart("/controllers/import", nil, "bad.xml", "<foo/>"); w.Code != 422 {
		t.Fatalf("invalid xml: %d", w.Code)
	}
	if w := e.multipart("/controllers/import", nil, "big.xml", strings.Repeat("x", 70<<10)); w.Code != 413 {
		t.Fatalf("big xml: %d", w.Code)
	}
}

func TestBluetoothPageWithoutAdapter(t *testing.T) {
	e := newEnv(t)
	e.controllerSetup(t)
	w := e.get("/controllers")
	if !strings.Contains(w.Body.String(), "no Bluetooth adapter found") {
		t.Fatal("hint missing")
	}
	for _, p := range []string{"/bluetooth/scan", "/bluetooth/AA:BB:CC:DD:EE:FF/pair", "/bluetooth/AA:BB:CC:DD:EE:FF/disconnect", "/bluetooth/AA:BB:CC:DD:EE:FF/remove"} {
		w := e.do("POST", p, nil)
		if w.Code != 409 || !strings.Contains(w.Header().Get("HX-Trigger"), "toast") {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

type fakeBus struct {
	mu     sync.Mutex
	paired []string
}

func (b *fakeBus) HasAdapter() (bool, error) { return true, nil }
func (b *fakeBus) PowerOn() error            { return nil }
func (b *fakeBus) StartDiscovery() error     { return nil }
func (b *fakeBus) StopDiscovery() error      { return nil }
func (b *fakeBus) Devices() ([]bluetooth.Device, error) {
	return []bluetooth.Device{{Address: "11:22:33:44:55:66", Name: "Zeta Pad"}, {Address: "AA:BB:CC:DD:EE:FF", Name: "Alpha Pad"}}, nil
}
func (b *fakeBus) Pair(a string) error {
	b.mu.Lock()
	b.paired = append(b.paired, a)
	b.mu.Unlock()
	return nil
}
func (b *fakeBus) Trust(string) error      { return nil }
func (b *fakeBus) Connect(string) error    { return nil }
func (b *fakeBus) Disconnect(string) error { return nil }
func (b *fakeBus) Remove(string) error     { return nil }

func TestBluetoothScanSingleAndEvents(t *testing.T) {
	old, oldI := scanDuration, bluetooth.ScanInterval
	scanDuration, bluetooth.ScanInterval = time.Second, 50*time.Millisecond
	t.Cleanup(func() { scanDuration, bluetooth.ScanInterval = old, oldI })
	e := newEnv(t)
	bus := &fakeBus{}
	e.d.BT = &bluetooth.Manager{Bus: bus}
	e.h = NewHandler(e.d)
	if w := e.do("POST", "/bluetooth/scan", nil); w.Code != 200 || !strings.Contains(w.Header().Get("HX-Trigger"), "bt-scan") {
		t.Fatalf("scan: %d %q", w.Code, w.Header().Get("HX-Trigger"))
	}
	w := e.do("POST", "/bluetooth/scan", nil)
	if w.Code != 409 || !strings.Contains(w.Header().Get("HX-Trigger"), "A scan is already running") {
		t.Fatalf("second scan: %d %q", w.Code, w.Header().Get("HX-Trigger"))
	}
	ev := e.get("/bluetooth/events")
	body := ev.Body.String()
	if ev.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(body, "Alpha Pad") {
		t.Fatalf("events: %q", body)
	}
	if strings.Index(body, "Alpha Pad") > strings.Index(body, "Zeta Pad") {
		t.Fatal("devices not sorted")
	}
	// pair validates the address
	if w := e.do("POST", "/bluetooth/nonsense/pair", nil); w.Code != 400 {
		t.Fatalf("bad addr: %d", w.Code)
	}
	if w := e.do("POST", "/bluetooth/aa:bb:cc:dd:ee:ff/pair", nil); w.Code != 200 {
		t.Fatalf("pair: %d", w.Code)
	}
	if len(bus.paired) != 1 || bus.paired[0] != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("paired=%v", bus.paired)
	}
}

func TestUnbanCallsPriv(t *testing.T) {
	e := newEnv(t)
	fr := &fakeRunner{}
	e.withRunner(fr)
	w := e.do("POST", "/system/unban", url.Values{"ip": {"203.0.113.9"}})
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if c := fr.called(); len(c) != 1 || c[0] != "sudo -n /h f2b-unban 203.0.113.9" {
		t.Fatalf("calls=%v", c)
	}
}

func TestUnbanInvalidIP400(t *testing.T) {
	e := newEnv(t)
	fr := &fakeRunner{}
	e.withRunner(fr)
	for _, ip := range []string{"", "1.2.3", "1.2.3.4; reboot", "--all"} {
		if w := e.do("POST", "/system/unban", url.Values{"ip": {ip}}); w.Code != 400 {
			t.Errorf("%q: %d", ip, w.Code)
		}
	}
	if len(fr.called()) != 0 {
		t.Fatal("priv called")
	}
}

func TestSystemPageAndLogs(t *testing.T) {
	e := newEnv(t)
	e.cfg.Paths.Home = t.TempDir()
	_ = os.MkdirAll(filepath.Join(e.cfg.Paths.Home, ".emulationstation"), 0o755)
	_ = os.WriteFile(filepath.Join(e.cfg.Paths.Home, ".emulationstation", "es_log.txt"), []byte("ES <b>line</b>\n"), 0o644)
	fr := &fakeRunner{out: map[string]string{
		"sudo -n /h f2b-list":                     "sshd 203.0.113.9\n",
		"sudo -n /h journal ssh":                  "ssh journal line\n",
		"sudo -n /h journal emulationstation-web": "web journal line\n",
	}}
	e.withRunner(fr)
	body := e.get("/system").Body.String()
	for _, want := range []string{"203.0.113.9", "Unban", "Restart", "Shut down", "Update package lists", "EmulationStation"} {
		if !strings.Contains(body, want) {
			t.Errorf("system page missing %q", want)
		}
	}
	if b := e.get("/system/log?unit=ssh").Body.String(); !strings.Contains(b, "ssh journal line") {
		t.Fatal(b)
	}
	if b := e.get("/system/log?unit=es").Body.String(); !strings.Contains(b, "ES &lt;b&gt;line") {
		t.Fatal(b)
	}
	if w := e.get("/system/log?unit=passwd"); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestAptUpdateRunsAsJob(t *testing.T) {
	e := newEnv(t)
	fr := &fakeRunner{out: map[string]string{"sudo -n /h apt-update": "Hit:1 foo\nDone\n"}}
	e.withRunner(fr)
	w := e.do("POST", "/system/update", nil)
	loc := w.Header().Get("HX-Redirect")
	if w.Code != 200 || !strings.HasPrefix(loc, "/jobs/") {
		t.Fatalf("code=%d loc=%q", w.Code, loc)
	}
	waitFor(t, "apt-update", func() bool { return len(fr.called()) == 1 })
}

func TestPasswordChangeMismatch(t *testing.T) {
	e := newEnv(t)
	path := e.withHolder(t)
	for _, f := range []url.Values{
		{"user": {"u"}, "password": {"abcd"}, "password2": {"abce"}},
		{"user": {"u"}, "password": {""}, "password2": {""}},
		{"user": {""}, "password": {"abcd"}, "password2": {"abcd"}},
		{"user": {"u"}, "password": {"abc"}, "password2": {"abc"}},
		{"user": {"a:b"}, "password": {"abcd"}, "password2": {"abcd"}},
		{"user": {"a\nb"}, "password": {"abcd"}, "password2": {"abcd"}},
	} {
		if w := e.do("POST", "/settings/password", f); w.Code != 400 || !strings.Contains(w.Header().Get("HX-Trigger"), "toast") {
			t.Errorf("%v: %d", f, w.Code)
		}
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("config written despite errors")
	}
	if w := e.get("/bios"); w.Code != 200 {
		t.Fatal("old credentials stopped working")
	}
}

func TestPasswordChangeAppliesImmediately(t *testing.T) {
	e := newEnv(t)
	path := e.withHolder(t)
	w := e.do("POST", "/settings/password", url.Values{"user": {"neu"}, "password": {"geheim123"}, "password2": {"geheim123"}})
	if w.Code != 200 || w.Header().Get("HX-Redirect") != "/" {
		t.Fatalf("code=%d redirect=%q", w.Code, w.Header().Get("HX-Redirect"))
	}
	if w := e.get("/settings"); w.Code != 401 {
		t.Fatalf("old credentials: %d", w.Code)
	}
	req := httptest.NewRequest("GET", "/settings", nil)
	req.SetBasicAuth("neu", "geheim123")
	if w := serve(e.h, req); w.Code != 200 {
		t.Fatalf("new credentials: %d", w.Code)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "geheim123") || !strings.Contains(string(b), "WEB_USER=neu") || !strings.Contains(string(b), "WEB_PASSWORD_HASH=$2") {
		t.Fatalf("web.conf: %s", b)
	}
}

func TestScreenscraperKeepsPasswordAndNeverEchoesIt(t *testing.T) {
	e := newEnv(t)
	e.cfg.ScreenscraperUser, e.cfg.ScreenscraperPass = "old", "topsecret"
	e.withHolder(t)
	if w := e.do("POST", "/settings/screenscraper", url.Values{"user": {"new"}, "pass": {""}}); w.Code != 200 {
		t.Fatal(w.Code)
	}
	c := e.d.Config()
	if c.ScreenscraperUser != "new" || c.ScreenscraperPass != "topsecret" {
		t.Fatalf("%+v", c)
	}
	if body := e.get("/settings").Body.String(); strings.Contains(body, "topsecret") || !strings.Contains(body, `value="new"`) {
		t.Fatal("password echoed or user missing")
	}
	e.do("POST", "/settings/screenscraper", url.Values{"user": {"new"}, "pass": {"fresh"}})
	if e.d.Config().ScreenscraperPass != "fresh" {
		t.Fatal("password not replaced")
	}
}

func TestPoweroffRespondsBeforeAction(t *testing.T) {
	old := powerDelay
	powerDelay = 150 * time.Millisecond
	t.Cleanup(func() { powerDelay = old })
	e := newEnv(t)
	fr := &fakeRunner{}
	e.withRunner(fr)
	w := e.do("POST", "/system/poweroff", nil)
	if w.Code != 200 || !strings.Contains(w.Header().Get("HX-Trigger"), "toast") {
		t.Fatalf("code=%d", w.Code)
	}
	if len(fr.called()) != 0 {
		t.Fatal("action ran before the response")
	}
	waitFor(t, "poweroff", func() bool { return len(fr.called()) == 1 })
	if c := fr.called()[0]; c != "sudo -n /h poweroff" {
		t.Fatal(c)
	}
}

func TestESStopWhileOperationRunningIsBusy(t *testing.T) {
	e := newEnv(t)
	wr := e.fakeES(t)
	in, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- e.d.ES.WithESStopped(context.Background(), func() error { close(in); <-release; return nil })
	}()
	<-in
	for _, p := range []string{"/es/stop", "/es/start", "/es/restart"} {
		w := e.do("POST", p, nil)
		if w.Code != http.StatusConflict || !strings.Contains(w.Header().Get("HX-Trigger"), "Please wait") {
			t.Errorf("%s: code=%d trigger=%q", p, w.Code, w.Header().Get("HX-Trigger"))
		}
	}
	if _, err := os.Stat(filepath.Join(e.cfg.Paths.RunDir, "hold")); err != nil {
		t.Fatal("hold removed mid-operation")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ES restarted", wr.started.Load)
}

func TestBluetoothPartialIsRowsOnly(t *testing.T) {
	e := newEnv(t)
	e.d.BT = &bluetooth.Manager{Bus: &fakeBus{}}
	e.h = NewHandler(e.d)
	body := e.get("/partials/bluetooth").Body.String()
	if !strings.Contains(body, "Alpha Pad") || strings.Contains(body, "btLive") || strings.Contains(body, `id="bt-list"`) {
		t.Fatalf("partial must be device rows only: %s", body)
	}
}

func TestPowerSingleFlight(t *testing.T) {
	old := powerDelay
	powerDelay = 100 * time.Millisecond
	t.Cleanup(func() { powerDelay = old })
	e := newEnv(t)
	fr := &fakeRunner{}
	e.withRunner(fr)
	if w := e.do("POST", "/system/reboot", nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := e.do("POST", "/system/poweroff", nil); w.Code != 409 || !strings.Contains(w.Header().Get("HX-Trigger"), "Already running") {
		t.Fatalf("second: %d", w.Code)
	}
	waitFor(t, "reboot", func() bool { return len(fr.called()) == 1 })
}
