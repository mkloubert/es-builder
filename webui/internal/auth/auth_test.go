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

package auth

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"emulationstation/webui/internal/config"
)

func mkCfg(t *testing.T, user, pw string) *config.Config {
	t.Helper()
	h, err := config.HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	return &config.Config{WebUser: user, WebPasswordHash: h}
}

func do(t *testing.T, cfg func() *config.Config, remote string, setup func(*http.Request)) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var buf bytes.Buffer
	h := Middleware(cfg, &buf, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = remote
	if setup != nil {
		setup(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, buf.String()
}

func TestNoCredentialsGives401WithoutLog(t *testing.T) {
	c := mkCfg(t, "u", "p")
	rec, log := do(t, func() *config.Config { return c }, "1.2.3.4:1", nil)
	if rec.Code != 401 {
		t.Fatal(rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != `Basic realm="EmuStation Builder", charset="UTF-8"` {
		t.Fatal(got)
	}
	if log != "" {
		t.Fatal(log)
	}
}

func wrong(r *http.Request) { r.SetBasicAuth("u", "bad") }

func TestWrongCredentialsLogsExactLine(t *testing.T) {
	c := mkCfg(t, "u", "p")
	rec, log := do(t, func() *config.Config { return c }, "192.168.1.7:5555", wrong)
	if rec.Code != 401 || log != "auth failure from 192.168.1.7\n" {
		t.Fatal(rec.Code, log)
	}
}

func TestMappedIPv6Normalized(t *testing.T) {
	c := mkCfg(t, "u", "p")
	_, log := do(t, func() *config.Config { return c }, "[::ffff:10.0.0.2]:1", wrong)
	if log != "auth failure from 10.0.0.2\n" {
		t.Fatal(log)
	}
}

func TestIPv6(t *testing.T) {
	c := mkCfg(t, "u", "p")
	_, log := do(t, func() *config.Config { return c }, "[2001:db8::1]:1", wrong)
	if log != "auth failure from 2001:db8::1\n" {
		t.Fatal(log)
	}
}

func TestIgnoresXForwardedFor(t *testing.T) {
	c := mkCfg(t, "u", "p")
	_, log := do(t, func() *config.Config { return c }, "1.1.1.1:1", func(r *http.Request) {
		wrong(r)
		r.Header.Set("X-Forwarded-For", "9.9.9.9")
	})
	if log != "auth failure from 1.1.1.1\n" {
		t.Fatal(log)
	}
}

func TestMalformedAuthHeaderLogged(t *testing.T) {
	c := mkCfg(t, "u", "p")
	for _, v := range []string{"Basic !!!notbase64", "Basic dXNlcm5vY29sb24=", "Bearer x"} {
		rec, log := do(t, func() *config.Config { return c }, "1.1.1.1:1", func(r *http.Request) {
			r.Header.Set("Authorization", v)
		})
		if rec.Code != 401 || log != "auth failure from 1.1.1.1\n" {
			t.Fatal(v, rec.Code, log)
		}
	}
}

func TestClientIPUnparsable(t *testing.T) {
	r := &http.Request{RemoteAddr: "garbage"}
	if got := ClientIP(r); got != "garbage" {
		t.Fatal(got)
	}
}

func TestValidCredentialsPassThrough(t *testing.T) {
	c := mkCfg(t, "u", "p")
	rec, log := do(t, func() *config.Config { return c }, "1.2.3.4:1", func(r *http.Request) { r.SetBasicAuth("u", "p") })
	if rec.Code != 200 || log != "" {
		t.Fatal(rec.Code, log)
	}
}

func TestCredentialChangeTakesEffect(t *testing.T) {
	cur := mkCfg(t, "u", "p")
	f := func() *config.Config { return cur }
	var buf bytes.Buffer
	h := Middleware(f, &buf, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	call := func() int {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "1.2.3.4:1"
		req.SetBasicAuth("u", "p")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := call(); c != 200 {
		t.Fatal(c)
	}
	cur = mkCfg(t, "u", "new")
	if c := call(); c != 401 {
		t.Fatal(c)
	}
}

func TestSuccessIsCachedFailureIsNot(t *testing.T) {
	c := mkCfg(t, "u", "p")
	calls := 0
	orig := checkCredentials
	checkCredentials = func(c *config.Config, u, p string) bool { calls++; return orig(c, u, p) }
	defer func() { checkCredentials = orig }()

	var buf bytes.Buffer
	h := Middleware(func() *config.Config { return c }, &buf, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := func(pw string) int {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "1.2.3.4:1"
		r.SetBasicAuth("u", pw)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if req("p") != 200 || req("p") != 200 {
		t.Fatal("expected 200")
	}
	if calls != 1 {
		t.Fatalf("bcrypt calls = %d, want 1", calls)
	}
	if req("bad") != 401 || req("bad") != 401 {
		t.Fatal("expected 401")
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (failures uncached)", calls)
	}
	if buf.String() != "auth failure from 1.2.3.4\nauth failure from 1.2.3.4\n" {
		t.Fatal(buf.String())
	}
	if req("p") != 200 {
		t.Fatal("cached success lost")
	}
}

func TestCacheEvictsOldest(t *testing.T) {
	now := time.Unix(1000, 0)
	sc := &successCache{m: map[[32]byte]time.Time{}, now: func() time.Time { now = now.Add(time.Second); return now }}
	c := &config.Config{WebPasswordHash: "h"}
	for i := 0; i < cacheMaxSize+1; i++ {
		sc.add(cacheKey(c, "u", string(rune('a'+i))))
	}
	if len(sc.m) != cacheMaxSize {
		t.Fatal(len(sc.m))
	}
	if sc.has(cacheKey(c, "u", "a")) {
		t.Fatal("oldest not evicted")
	}
}
