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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"emulationstation/webui/internal/jobs"
	"emulationstation/webui/internal/update"
)

const testRepo = "https://example.org/r.git"

// lsRunner answers git ls-remote with a fixed tag list.
type lsRunner struct{ out string }

func (l lsRunner) Run(_ context.Context, _ string, _ []string, stdout io.Writer, _ string, _ ...string) error {
	_, _ = io.WriteString(stdout, l.out)
	return nil
}

// updEnv gives an env with self-update wired in. latest "" = no saved check.
func updEnv(t *testing.T, url, latest string) (*env, *update.Updater, *update.Checker) {
	t.Helper()
	e := newEnv(t)
	base := t.TempDir()
	e.cfg.UpdateURL = url
	g := update.Git{R: lsRunner{out: "aaaa\trefs/tags/webui-v1.3.0\n"}, S: update.Settings{URL: url}}
	ck := &update.Checker{Git: g, File: filepath.Join(base, "check.json"), Current: update.Version{Major: 1, Minor: 2, Patch: 3}}
	if latest != "" {
		if err := os.WriteFile(ck.File, []byte(`{"checked_at":"2026-11-02T10:00:00Z","latest":"`+latest+`","error":""}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	up := &update.Updater{Git: g, Base: base, GOARCH: "amd64",
		Statfs:  func(string) (uint64, error) { return 0, nil },
		Install: func(context.Context, string) error { return nil }}
	e.d.Version = "1.2.3"
	if url != "" {
		e.d.Update, e.d.Checker = up, ck
	}
	e.h = NewHandler(e.d)
	return e, up, ck
}

func TestHealthzLoopbackOnlyNoAuth(t *testing.T) {
	e, _, _ := updEnv(t, testRepo, "")
	for _, tc := range []struct {
		addr string
		code int
	}{
		{"127.0.0.1:1234", 200}, {"[::1]:1234", 200}, {"192.0.2.1:1234", 404}, {"[2001:db8::1]:1", 404}, {"junk", 404},
	} {
		req := httptest.NewRequest("GET", "/healthz", nil)
		req.RemoteAddr = tc.addr
		w := serve(e.h, req)
		if w.Code != tc.code {
			t.Errorf("%s: code = %d, want %d", tc.addr, w.Code, tc.code)
		}
		if tc.code == 200 {
			if got := strings.TrimSpace(w.Body.String()); got != `{"version":"1.2.3"}` {
				t.Errorf("body = %q", got)
			}
			if w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Error("security headers missing")
			}
		}
	}
}

func TestBannerOnlyWhenNewerAndLoggedIn(t *testing.T) {
	const msg = "Web UI version 1.3.0 is ready. Open System to update."
	e, _, _ := updEnv(t, testRepo, "1.3.0")
	if b := e.get("/").Body.String(); !strings.Contains(b, msg) {
		t.Error("banner missing")
	}
	if b := serve(e.h, httptest.NewRequest("GET", "/", nil)).Body.String(); strings.Contains(b, "Open System to update") {
		t.Error("banner shown without login")
	}
	e2, _, _ := updEnv(t, testRepo, "1.2.3")
	if b := e2.get("/").Body.String(); strings.Contains(b, "Open System to update") {
		t.Error("banner shown for the same version")
	}
	e3, _, _ := updEnv(t, "", "")
	if b := e3.get("/").Body.String(); strings.Contains(b, "Open System to update") {
		t.Error("banner shown while off")
	}
}

func TestSystemPageUpdateSection(t *testing.T) {
	e, _, _ := updEnv(t, testRepo, "1.3.0")
	b := e.get("/system").Body.String()
	for _, want := range []string{"Version: 1.2.3", testRepo, "Last check:", "Check now", "Update now",
		"This takes 5 to 15 minutes. The web UI restarts at the end.", "Tags are not checked for a signature.",
		"/system/webui-update/check", "/system/webui-update/run"} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %q", want)
		}
	}
	// signers file set: no hint; same version: no update button
	e2, _, _ := updEnv(t, testRepo, "1.2.3")
	e2.cfg.UpdateSignersFile = "/etc/s"
	b = e2.get("/system").Body.String()
	if strings.Contains(b, "Tags are not checked") || strings.Contains(b, "Update now") {
		t.Error("hint or update button shown wrongly")
	}
	// off
	e3, _, _ := updEnv(t, "", "")
	b = e3.get("/system").Body.String()
	if !strings.Contains(b, "Self-update is off.") || strings.Contains(b, "Check now") || strings.Contains(b, "/system/webui-update/") {
		t.Error("off state wrong")
	}
}

func TestSystemPageShowsCheckError(t *testing.T) {
	e, _, ck := updEnv(t, testRepo, "")
	if err := os.WriteFile(ck.File, []byte(`{"checked_at":"2026-11-02T10:00:00Z","latest":"","error":"auth"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	b := e.get("/system").Body.String()
	if !strings.Contains(b, "The login for the update source failed.") || strings.Contains(b, "auth</") {
		t.Errorf("error text wrong")
	}
}

func TestUpdateRunNeedsNewerVersion(t *testing.T) {
	e, _, _ := updEnv(t, testRepo, "1.2.3")
	if w := e.do("POST", "/system/webui-update/run", nil); w.Code != 409 {
		t.Errorf("same version: %d", w.Code)
	}
	e2, _, _ := updEnv(t, testRepo, "")
	if w := e2.do("POST", "/system/webui-update/run", nil); w.Code != 409 {
		t.Errorf("no check yet: %d", w.Code)
	}
	e3, _, _ := updEnv(t, "", "")
	if w := e3.do("POST", "/system/webui-update/run", nil); w.Code != 409 {
		t.Errorf("off: %d", w.Code)
	}
	if w := e3.do("POST", "/system/webui-update/check", nil); w.Code != 409 {
		t.Errorf("off check: %d", w.Code)
	}
}

func TestUpdateRunSubmitsJobAndHidesRawError(t *testing.T) {
	e, up, _ := updEnv(t, testRepo, "1.3.0")
	var mu sync.Mutex
	up.Statfs = func(string) (uint64, error) {
		mu.Lock()
		defer mu.Unlock()
		return 0, io.ErrUnexpectedEOF // raw text must not reach the UI
	}
	w := e.do("POST", "/system/webui-update/run", nil)
	loc := w.Header().Get("HX-Redirect")
	if w.Code != 200 || !strings.HasPrefix(loc, "/jobs/") {
		t.Fatalf("code=%d loc=%q", w.Code, loc)
	}
	id := strings.TrimPrefix(loc, "/jobs/")
	var j jobs.Job
	waitFor(t, "job end", func() bool {
		var ok bool
		j, ok = e.d.Jobs.Get(id)
		return ok && j.Status == jobs.StatusFailed
	})
	if j.Kind != "webui-update" || j.Title != "Update web UI to 1.3.0" {
		t.Errorf("kind=%q title=%q", j.Kind, j.Title)
	}
	if j.Err != "The update failed. The old version runs again." || strings.Contains(j.Err, "EOF") {
		t.Errorf("err = %q", j.Err)
	}
}

func TestUpdateBusyGives409(t *testing.T) {
	e, up, _ := updEnv(t, testRepo, "1.3.0")
	started, release := make(chan struct{}), make(chan struct{})
	up.Statfs = func(string) (uint64, error) { close(started); <-release; return 0, nil }
	if w := e.do("POST", "/system/webui-update/run", nil); w.Code != 200 {
		t.Fatalf("first run: %d", w.Code)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not start")
	}
	defer close(release)
	if !up.Busy() {
		t.Fatal("updater not busy")
	}
	if w := e.do("POST", "/system/webui-update/run", nil); w.Code != 409 {
		t.Errorf("run while busy: %d", w.Code)
	}
	if w := e.do("POST", "/system/webui-update/check", nil); w.Code != 409 {
		t.Errorf("check while busy: %d", w.Code)
	}
}

func TestUpdateCheckRunsInRequest(t *testing.T) {
	e, _, ck := updEnv(t, testRepo, "")
	w := e.do("POST", "/system/webui-update/check", nil)
	if w.Code != 200 || w.Header().Get("HX-Refresh") != "true" {
		t.Fatalf("code=%d refresh=%q", w.Code, w.Header().Get("HX-Refresh"))
	}
	if st := ck.Load(); st.Latest != "1.3.0" || st.Err != "" {
		t.Errorf("state = %+v", st)
	}
}

func TestUpdatePostsRejectCrossOrigin(t *testing.T) {
	e, _, _ := updEnv(t, testRepo, "1.3.0")
	for _, p := range []string{"/system/webui-update/run", "/system/webui-update/check"} {
		req := httptest.NewRequest("POST", p, nil)
		req.SetBasicAuth("u", "pw")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		if w := serve(e.h, req); w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", p, w.Code)
		}
		req = httptest.NewRequest("POST", p, nil)
		if w := serve(e.h, req); w.Code != http.StatusUnauthorized {
			t.Errorf("%s without login: %d", p, w.Code)
		}
	}
}

func TestReadInstallStatus(t *testing.T) {
	for _, tc := range []struct {
		in    string
		ok    bool
		v     string
		found bool
	}{
		{"ok 1.2.3\n", true, "1.2.3", true},
		{"rolled-back 1.2.3\n", false, "1.2.3", true},
		{"failed 1.2.3", false, "1.2.3", true},
		{"ok 1.2\n", false, "", false},
		{"ok 1.2.3 extra\n", false, "", false},
		{"junk\n", false, "", false},
		{"", false, "", false},
	} {
		dir := t.TempDir()
		f := filepath.Join(dir, "webui-update.status")
		if err := os.WriteFile(f, []byte(tc.in), 0o644); err != nil {
			t.Fatal(err)
		}
		ok, v, found := ReadInstallStatus(dir)
		if ok != tc.ok || v != tc.v || found != tc.found {
			t.Errorf("%q: got %v %q %v", tc.in, ok, v, found)
		}
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%q: status file not removed", tc.in)
		}
	}
	if _, _, found := ReadInstallStatus(t.TempDir()); found {
		t.Error("no file: found")
	}
}

func TestInstallStatusFlashShownOnce(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"ok 1.2.3\n", "Web UI updated to 1.2.3."},
		{"rolled-back 1.2.3\n", "The update failed. The old version runs again."},
		{"failed 1.2.3\n", "The update failed. The old version runs again."},
	} {
		e := newEnv(t)
		e.h = NewHandler(e.d)
		if b := e.get("/system").Body.String(); strings.Contains(b, tc.want) {
			t.Errorf("%q: flash before the file exists", tc.in)
		}
		if err := os.WriteFile(filepath.Join(e.d.ES.RunDir, "webui-update.status"), []byte(tc.in), 0o644); err != nil {
			t.Fatal(err)
		}
		if b := e.get("/system").Body.String(); !strings.Contains(b, tc.want) {
			t.Errorf("%q: flash missing", tc.in)
		}
		if b := e.get("/system").Body.String(); strings.Contains(b, tc.want) {
			t.Errorf("%q: flash shown twice", tc.in)
		}
	}
	e := newEnv(t)
	e.h = NewHandler(e.d)
	if err := os.WriteFile(filepath.Join(e.d.ES.RunDir, "webui-update.status"), []byte("junk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b := e.get("/system").Body.String(); strings.Contains(b, "Web UI updated") || strings.Contains(b, "update failed") {
		t.Error("junk produced a flash")
	}
}

func TestJobPageRestartLink(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"#restart", "waitRestart"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	tp, err := os.ReadFile("templates/job.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tp), "Restarting…") {
		t.Error("job.html lacks the restart text")
	}
}

func TestUpdateQueuedJobGives409(t *testing.T) {
	e, _, _ := updEnv(t, testRepo, "1.3.0")
	started, release := make(chan struct{}), make(chan struct{})
	e.d.Jobs.Submit("apt", "blocker", func(ctx context.Context, _ jobs.Reporter) error {
		close(started)
		<-release
		return nil
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("blocker did not start")
	}
	defer close(release)
	if w := e.do("POST", "/system/webui-update/run", nil); w.Code != 200 {
		t.Fatalf("first run: %d", w.Code)
	}
	if w := e.do("POST", "/system/webui-update/run", nil); w.Code != 409 {
		t.Errorf("second run: %d", w.Code)
	}
	if w := e.do("POST", "/system/webui-update/check", nil); w.Code != 409 {
		t.Errorf("check with queued update: %d", w.Code)
	}
	n := 0
	for _, j := range e.d.Jobs.List() {
		if j.Kind == "webui-update" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("webui-update jobs = %d, want 1", n)
	}
}
