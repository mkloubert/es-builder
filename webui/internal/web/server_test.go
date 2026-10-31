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
	"compress/gzip"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"emulationstation/webui/internal/config"
	"emulationstation/webui/internal/es"
	"emulationstation/webui/internal/sysinfo"
	"emulationstation/webui/internal/systems"
)

func newDeps(t *testing.T) (Deps, *bytes.Buffer) {
	t.Helper()
	hash, err := config.HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{WebUser: "u", WebPasswordHash: hash}
	var logw bytes.Buffer
	d := Deps{
		Config:  func() *config.Config { return cfg },
		Systems: func() []systems.System { return nil },
		ES:      &es.Controller{RunDir: t.TempDir(), StopTimeout: time.Second},
		Sysinfo: sysinfo.Collector{Root: "../sysinfo/testdata/root"},
		LogW:    &logw,
	}
	startSession(t, d.ES.RunDir)
	return d, &logw
}

// startSession makes the controller see a running es-session (session.pid = own PID).
func startSession(t *testing.T, runDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(runDir, "session.pid"), []byte(fmt.Sprint(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
}

// noSession removes session.pid: no es-session runs (ES_AUTOSTART=no).
func noSession(t *testing.T, runDir string) {
	t.Helper()
	if err := os.Remove(filepath.Join(runDir, "session.pid")); err != nil {
		t.Fatal(err)
	}
}

func authed(r *http.Request) *http.Request {
	r.SetBasicAuth("u", "pw")
	return r
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func writeRun(t *testing.T, d Deps, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(d.ES.RunDir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPlainHTTPGets400(t *testing.T) {
	d, _ := newDeps(t)
	srv := httptest.NewUnstartedServer(NewHandler(d))
	srv.EnableHTTP2 = false
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()
	resp, err := http.Get("http://" + srv.Listener.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestTLS11Rejected(t *testing.T) {
	d, _ := newDeps(t)
	srv := httptest.NewUnstartedServer(NewHandler(d))
	srv.TLS = tlsConfig()
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11,
	}}}
	if resp, err := client.Get(srv.URL); err == nil {
		resp.Body.Close()
		t.Fatal("TLS 1.1 handshake succeeded")
	}
}

// spawnES starts a process and registers it as the ES process.
func spawnES(t *testing.T, d Deps) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { <-done })
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	writeRun(t, d, "es.pid", strconv.Itoa(cmd.Process.Pid))
	writeRun(t, d, "state", "es\n")
	return cmd
}

func alive(cmd *exec.Cmd) bool { return syscall.Kill(cmd.Process.Pid, 0) == nil }

func TestCrossOriginPostRejected(t *testing.T) {
	d, _ := newDeps(t)
	cmd := spawnES(t, d)
	req := authed(httptest.NewRequest("POST", "/es/restart", nil))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := serve(NewHandler(d), req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	time.Sleep(100 * time.Millisecond)
	if !alive(cmd) {
		t.Fatal("ES was restarted by a cross-origin request")
	}
}

func TestSameOriginPostAccepted(t *testing.T) {
	d, _ := newDeps(t)
	cmd := spawnES(t, d)
	req := authed(httptest.NewRequest("POST", "/es/restart", nil))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	w := serve(NewHandler(d), req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	for i := 0; i < 100 && alive(cmd); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(cmd) {
		t.Fatal("ES was not signalled")
	}
}

func TestHTMXActionRefreshesStatusWithToast(t *testing.T) {
	d, _ := newDeps(t)
	writeRun(t, d, "state", "stopped\n")
	req := authed(httptest.NewRequest("POST", "/es/start", nil))
	req.Header.Set("HX-Request", "true")
	w := serve(NewHandler(d), req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `id="status"`) {
		t.Fatalf("code=%d body=%q", w.Code, w.Body.String())
	}
	h := w.Header().Get("HX-Trigger")
	if !strings.Contains(h, `"toast"`) {
		t.Fatalf("HX-Trigger = %q", h)
	}
	for _, r := range h {
		if r > 127 {
			t.Fatalf("HX-Trigger not ASCII: %q", h)
		}
	}
}

func TestHTMXActionErrorToast(t *testing.T) {
	d, _ := newDeps(t)
	req := authed(httptest.NewRequest("POST", "/es/restart", nil)) // no ES process
	req.Header.Set("HX-Request", "true")
	w := serve(NewHandler(d), req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Header().Get("HX-Trigger"), "toast") {
		t.Fatalf("code=%d trigger=%q", w.Code, w.Header().Get("HX-Trigger"))
	}
}

func TestDashboardRequiresAuth(t *testing.T) {
	d, _ := newDeps(t)
	w := serve(NewHandler(d), httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestDashboardRendersStatus(t *testing.T) {
	d, _ := newDeps(t)
	writeRun(t, d, "state", "game\tsnes\t/r/x.sfc\n")
	w := serve(NewHandler(d), authed(httptest.NewRequest("GET", "/", nil)))
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "x.sfc") || !strings.Contains(body, "Stop game") {
		t.Fatalf("code=%d body=%s", w.Code, body)
	}
	for _, h := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Content-Security-Policy"} {
		if w.Header().Get(h) == "" {
			t.Errorf("missing header %s", h)
		}
	}
	if strings.Contains(body, "cdn.") || !strings.Contains(body, "Settings") {
		t.Error("layout should not reference CDNs and must list the nav")
	}
}

func TestStatusPartialHasNoLayout(t *testing.T) {
	d, _ := newDeps(t)
	writeRun(t, d, "state", "es\n")
	w := serve(NewHandler(d), authed(httptest.NewRequest("GET", "/partials/status", nil)))
	b := w.Body.String()
	if w.Code != 200 || strings.Contains(b, "<html") || !strings.Contains(b, "Restart ES") {
		t.Fatalf("code=%d body=%s", w.Code, b)
	}
}

func TestCertWarningShown(t *testing.T) {
	d, _ := newDeps(t)
	d.Sysinfo = sysinfo.Collector{Root: "../sysinfo/testdata/root", CertPath: writeCert(t, 10*24*time.Hour)}
	w := serve(NewHandler(d), authed(httptest.NewRequest("GET", "/", nil)))
	if !strings.Contains(w.Body.String(), "certificate") {
		t.Fatal("no certificate warning")
	}
	d.Sysinfo.CertPath = writeCert(t, 200*24*time.Hour)
	w = serve(NewHandler(d), authed(httptest.NewRequest("GET", "/", nil)))
	if strings.Contains(w.Body.String(), "certificate") {
		t.Fatal("unexpected certificate warning")
	}
}

func TestStaticServedFromEmbed(t *testing.T) {
	d, _ := newDeps(t)
	w := serve(NewHandler(d), authed(httptest.NewRequest("GET", "/static/vendor/htmx.min.js", nil)))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "max-age=86400" || w.Body.Len() < 1000 {
		t.Fatalf("code=%d cc=%q len=%d", w.Code, w.Header().Get("Cache-Control"), w.Body.Len())
	}
}

func TestSSE(t *testing.T) {
	s := &Server{}
	ch := make(chan any, 2)
	ch <- "a\nb"
	ch <- "c"
	close(ch)
	w := httptest.NewRecorder()
	s.SSE(w, httptest.NewRequest("GET", "/", nil), ch, func(v any) (string, error) { return v.(string), nil })
	got, _ := io.ReadAll(w.Body)
	if string(got) != "data: a\ndata: b\n\ndata: c\n\n" || w.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("got %q", got)
	}
}

func TestTLSConfigMinVersion(t *testing.T) {
	if tlsConfig().MinVersion != tls.VersionTLS12 {
		t.Fatal("MinVersion must be TLS 1.2")
	}
}

func TestListenAndServeTLS(t *testing.T) {
	// Reserve a free port, then let ListenAndServeTLS bind it.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	dir := t.TempDir()
	certPath, keyPath := writeCertKey(t, dir)
	cfg := &config.Config{Listen: addr, TLSCert: certPath, TLSKey: keyPath}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	go func() { done <- ListenAndServeTLS(ctx, cfg, h) }()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12,
	}}}
	var resp *http.Response
	for i := 0; i < 100; i++ {
		if resp, err = client.Get("https://" + addr + "/"); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 || resp.TLS.Version != tls.VersionTLS12 {
		t.Fatalf("status=%d tls=%x", resp.StatusCode, resp.TLS.Version)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ListenAndServeTLS = %v, want nil", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no shutdown")
	}
}

func TestUnauthenticatedRequests401(t *testing.T) {
	d, _ := newDeps(t)
	h := NewHandler(d)
	for _, c := range []struct{ method, path string }{
		{"GET", "/static/vendor/htmx.min.js"}, {"GET", "/partials/status"}, {"POST", "/es/restart"},
	} {
		w := serve(h, httptest.NewRequest(c.method, c.path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", c.method, c.path, w.Code)
		}
		if w.Header().Get("X-Frame-Options") == "" {
			t.Errorf("%s %s: security headers missing on 401", c.method, c.path)
		}
	}
}

func TestMismatchedOriginRejected(t *testing.T) {
	d, _ := newDeps(t)
	cmd := spawnES(t, d)
	req := authed(httptest.NewRequest("POST", "/es/restart", nil))
	req.Host = "box.local:11944"
	req.Header.Set("Origin", "https://evil.example")
	w := serve(NewHandler(d), req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if w.Header().Get("X-Frame-Options") == "" {
		t.Error("security headers missing on 403")
	}
	time.Sleep(100 * time.Millisecond)
	if !alive(cmd) {
		t.Fatal("ES was restarted")
	}
}

func TestStaticNoDirectoryListing(t *testing.T) {
	d, _ := newDeps(t)
	for _, p := range []string{"/static/", "/static/vendor/"} {
		w := serve(NewHandler(d), authed(httptest.NewRequest("GET", p, nil)))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", p, w.Code)
		}
	}
}

func TestListenAndServeTLSStopsPromptlyWithSSEClient(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	certPath, keyPath := writeCertKey(t, t.TempDir())
	cfg := &config.Config{Listen: addr, TLSCert: certPath, TLSKey: keyPath}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Server{}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.SSE(w, r, make(chan any), func(any) (string, error) { return "", nil })
	})
	done := make(chan error, 1)
	go func() { done <- ListenAndServeTLS(ctx, cfg, h) }()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	var resp *http.Response
	for i := 0; i < 100; i++ {
		if resp, err = client.Get("https://" + addr + "/"); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ListenAndServeTLS = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown blocked by SSE client")
	}
}

func TestGzipPages(t *testing.T) {
	e := newEnv(t)
	get := func(path, enc string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.SetBasicAuth("u", "pw")
		if enc != "" {
			req.Header.Set("Accept-Encoding", enc)
		}
		return serve(e.h, req)
	}
	plain := get("/upload", "")
	if plain.Code != 200 || plain.Header().Get("Content-Encoding") != "" || !strings.Contains(plain.Body.String(), "<html") {
		t.Fatalf("plain: %d %q", plain.Code, plain.Header().Get("Content-Encoding"))
	}
	w := get("/upload", "br, gzip;q=0.8")
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Vary") != "Accept-Encoding" || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("gzip: %d %v", w.Code, w.Header())
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(zr)
	if err != nil || string(body) != plain.Body.String() {
		t.Fatalf("unpacked body differs: %v", err)
	}
	if w := get("/upload", "gzip;q=0"); w.Header().Get("Content-Encoding") != "" {
		t.Fatal("q=0 must turn gzip off")
	}
	// a text file of the static folder is compressed, an image is not
	if w := get("/static/app.css", "gzip"); w.Code != 200 || w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Content-Length") != "" {
		t.Fatalf("css: %d %v", w.Code, w.Header())
	}
	if w := get("/static/app.css", ""); w.Header().Get("Content-Encoding") != "" {
		t.Fatal("css without Accept-Encoding")
	}
	if !compressible("text/html; charset=utf-8") || compressible("text/event-stream") || compressible("image/png") {
		t.Fatal("compressible")
	}
}

func TestGzipKeepsEventStreamPlain(t *testing.T) {
	rec := httptest.NewRecorder()
	h := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		io.WriteString(w, "data: x\n\n")
		w.(http.Flusher).Flush()
	}))
	req := httptest.NewRequest("GET", "/jobs/1/events", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != "data: x\n\n" || !rec.Flushed {
		t.Fatalf("%v %q", rec.Header(), rec.Body.String())
	}
}

func TestDashboardOff(t *testing.T) {
	d, _ := newDeps(t)
	writeRun(t, d, "state", "es\n") // a left state file is ignored without a session
	noSession(t, d.ES.RunDir)
	w := serve(NewHandler(d), authed(httptest.NewRequest("GET", "/partials/status", nil)))
	b := w.Body.String()
	if w.Code != 200 || !strings.Contains(b, "EmulationStation is off") ||
		!strings.Contains(b, "Autostart is off. Start it on the device screen.") {
		t.Fatalf("code=%d body=%s", w.Code, b)
	}
	for _, p := range []string{"/es/start", "/es/stop", "/es/restart"} {
		if strings.Contains(b, p) {
			t.Errorf("button %s shown without a session", p)
		}
	}
}

// procList is a fixed es.Procs for the status tests.
type procList []es.Proc

func (p procList) List() ([]es.Proc, error) { return p, nil }

const (
	testRunCommand = "/opt/retropie/supplementary/runcommand/runcommand.sh"
	testESBinary   = "/opt/retropie/supplementary/emulationstation/emulationstation"
)

func TestDashboardMenuGame(t *testing.T) {
	d, _ := newDeps(t)
	writeRun(t, d, "state", "es\n")
	uid := os.Getuid()
	d.ES.Procs = procList{
		{PID: 100, PPID: 1, UID: uid, Exe: testESBinary},
		{PID: 102, PPID: 100, UID: uid, Args: []string{"/bin/bash", testRunCommand, "0", "_SYS_", "c64", "/r/FOO.d64"}},
	}
	d.ES.InfoFile = filepath.Join(d.ES.RunDir, "runcommand.info")
	if err := os.WriteFile(d.ES.InfoFile, []byte("c64\nvice-x64\n/r/FOO.d64\ncmd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := serve(NewHandler(d), authed(httptest.NewRequest("GET", "/partials/status", nil)))
	b := w.Body.String()
	for _, want := range []string{"Game running", "FOO.d64", "Started from the EmulationStation menu.", "/game/stop", "/es/restart"} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %q in %s", want, b)
		}
	}
}

func TestDashboardMenuGameWithoutNames(t *testing.T) {
	d, _ := newDeps(t)
	writeRun(t, d, "state", "es\n")
	noSession(t, d.ES.RunDir)
	d.ES.Procs = procList{{PID: 102, PPID: 1, UID: os.Getuid(), Args: []string{"/bin/bash", testRunCommand}}}
	d.ES.InfoFile = filepath.Join(d.ES.RunDir, "missing.info")
	w := serve(NewHandler(d), authed(httptest.NewRequest("GET", "/partials/status", nil)))
	b := w.Body.String()
	if !strings.Contains(b, "Game running") || !strings.Contains(b, "/game/stop") {
		t.Fatalf("body=%s", b)
	}
	if strings.Contains(b, `title="."`) {
		t.Error("an empty ROM is shown as \".\"")
	}
	for _, p := range []string{"/es/start", "/es/stop", "/es/restart"} {
		if strings.Contains(b, p) {
			t.Errorf("button %s shown without a session", p)
		}
	}
}

func TestDashboardUnmanaged(t *testing.T) {
	d, _ := newDeps(t)
	noSession(t, d.ES.RunDir)
	d.ES.Procs = procList{{PID: 100, PPID: 1, UID: os.Getuid(), Exe: testESBinary}}
	w := serve(NewHandler(d), authed(httptest.NewRequest("GET", "/partials/status", nil)))
	b := w.Body.String()
	if !strings.Contains(b, "EmulationStation runs outside its session") || strings.Contains(b, "EmulationStation is off") {
		t.Fatalf("body=%s", b)
	}
	for _, p := range []string{"/es/start", "/es/stop", "/es/restart", "/game/stop"} {
		if strings.Contains(b, p) {
			t.Errorf("button %s shown for an ES outside the session", p)
		}
	}
	req := authed(httptest.NewRequest("POST", "/es/stop", nil))
	req.Header.Set("HX-Request", "true")
	w = serve(NewHandler(d), req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Header().Get("HX-Trigger"), "not in its session") {
		t.Fatalf("stop: code=%d trigger=%q", w.Code, w.Header().Get("HX-Trigger"))
	}
}

func TestESStartWithoutSession(t *testing.T) {
	d, _ := newDeps(t)
	noSession(t, d.ES.RunDir)
	for _, p := range []string{"/es/start", "/es/restart"} {
		req := authed(httptest.NewRequest("POST", p, nil))
		req.Header.Set("HX-Request", "true")
		w := serve(NewHandler(d), req)
		if w.Code != http.StatusConflict ||
			!strings.Contains(w.Header().Get("HX-Trigger"), "EmulationStation is not running on the device. Start it on the device screen.") {
			t.Errorf("%s: code=%d trigger=%q", p, w.Code, w.Header().Get("HX-Trigger"))
		}
	}
}
