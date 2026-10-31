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
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"emulationstation/webui/internal/priv"
)

// cpuRoot makes a fake Pi 4 root (see internal/cpu) and points Sysinfo at it.
func cpuRoot(t *testing.T, d *Deps, model, hwmax string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"proc/device-tree/model":                                  model,
		"sys/devices/system/cpu/cpufreq/policy0/cpuinfo_max_freq": hwmax,
		"sys/devices/system/cpu/cpufreq/policy0/scaling_max_freq": hwmax,
	}
	for name, body := range files {
		if body != "" {
			writeCustom(t, root, map[string]string{name: body})
		}
	}
	d.Sysinfo.Root = root
	return root
}

const pi4Model = "Raspberry Pi 4 Model B Rev 1.1\x00"

var throttleTexts = []string{
	"Too hot: the CPU is slowed down now.",
	"Power is too low: the CPU is slowed down now. Use a stronger power supply.",
	"Not throttled.",
	"since the last start", // the "since boot" line is not used (spec 2)
}

func TestDashboardThrottleLine(t *testing.T) {
	cases := []struct{ out, want string }{
		{"0x20002\n", throttleTexts[0]},
		{"0x50005\n", throttleTexts[1]},
		{"0x0\n", throttleTexts[2]},
		{"0x80000\n", throttleTexts[2]},
		{"junk\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.out, func(t *testing.T) {
			d, _ := newDeps(t)
			cpuRoot(t, &d, pi4Model, "1500000")
			fr := &fakeRunner{out: map[string]string{"sudo -n /h cpu-throttled": tc.out}}
			d.Priv = priv.Priv{R: fr, Helper: "/h"}
			body := serve(NewHandler(d), authed(httptest.NewRequest("GET", "/", nil))).Body.String()
			for _, txt := range throttleTexts {
				if has := strings.Contains(body, txt); has != (txt == tc.want) {
					t.Errorf("text %q shown = %v", txt, has)
				}
			}
		})
	}
}

func TestDashboardThrottleNotSupported(t *testing.T) {
	d, _ := newDeps(t)
	cpuRoot(t, &d, "Raspberry Pi 5 Model B\x00", "2400000")
	fr := &fakeRunner{out: map[string]string{"sudo -n /h cpu-throttled": "0x50005\n"}}
	d.Priv = priv.Priv{R: fr, Helper: "/h"}
	body := serve(NewHandler(d), authed(httptest.NewRequest("GET", "/", nil))).Body.String()
	if len(fr.called()) != 0 || strings.Contains(body, "Throttle") {
		t.Fatalf("calls = %v", fr.called())
	}
}

type cpuErrRunner struct{ calls int }

func (e *cpuErrRunner) Run(context.Context, io.Writer, string, ...string) error {
	e.calls++
	return errors.New("exit status 1: raw helper text")
}

func cpuPost(h http.Handler, value string) *httptest.ResponseRecorder {
	r := authed(httptest.NewRequest("POST", "/system/cpu", strings.NewReader(url.Values{"value": {value}}.Encode())))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	return serve(h, r)
}

func TestSystemCPUTab(t *testing.T) {
	d, _ := newDeps(t)
	cpuRoot(t, &d, pi4Model, "1000000")
	writeCustom(t, d.Sysinfo.Root, map[string]string{"var/lib/emulationstation-cpu/max-mhz": "800\n"})
	body := serve(NewHandler(d), authed(httptest.NewRequest("GET", "/system", nil))).Body.String()
	for _, want := range []string{
		">CPU<", "Top CPU speed: 1000 MHz (board top speed: 1000 MHz).", `hx-post="/system/cpu"`,
		`value="max"`, `value="1000"`, `value="600"`, `value="800" checked`,
		"The Pi also slows down by itself when it gets too hot. A heat sink or a fan helps more than a lower speed.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("system page missing %q", want)
		}
	}
	if strings.Contains(body, `value="1200"`) {
		t.Error("a speed above the board top speed is offered")
	}

	d2, _ := newDeps(t)
	cpuRoot(t, &d2, "Raspberry Pi 5 Model B\x00", "2400000")
	if b := serve(NewHandler(d2), authed(httptest.NewRequest("GET", "/system", nil))).Body.String(); strings.Contains(b, "/system/cpu") || strings.Contains(b, ">CPU<") {
		t.Error("CPU tab shown on a board that is not supported")
	}
}

func TestSystemCPUPost(t *testing.T) {
	d, _ := newDeps(t)
	cpuRoot(t, &d, pi4Model, "1500000")
	fr := &fakeRunner{}
	d.Priv = priv.Priv{R: fr, Helper: "/h"}
	h := NewHandler(d)

	w := cpuPost(h, "800")
	if w.Code != 200 || !strings.Contains(w.Header().Get("HX-Trigger"), "CPU speed limit saved.") || !strings.Contains(w.Body.String(), "Top CPU speed:") {
		t.Fatalf("ok: code=%d trigger=%q body=%s", w.Code, w.Header().Get("HX-Trigger"), w.Body.String())
	}
	if c := fr.called(); len(c) != 1 || c[0] != "sudo -n /h cpu-set 800" {
		t.Fatalf("calls = %v", c)
	}
	for _, bad := range []string{"1100", "", "800;id"} {
		if w := cpuPost(h, bad); w.Code != 400 || !strings.Contains(w.Header().Get("HX-Trigger"), "Unknown CPU speed.") {
			t.Errorf("value %q: code=%d trigger=%q", bad, w.Code, w.Header().Get("HX-Trigger"))
		}
	}
	if len(fr.called()) != 1 {
		t.Fatal("helper called for a bad value")
	}

	d2, _ := newDeps(t)
	cpuRoot(t, &d2, pi4Model, "1000000")
	fr2 := &fakeRunner{}
	d2.Priv = priv.Priv{R: fr2, Helper: "/h"}
	if w := cpuPost(NewHandler(d2), "1200"); w.Code != 400 || len(fr2.called()) != 0 {
		t.Errorf("above board top: code=%d calls=%v", w.Code, fr2.called())
	}

	d3, _ := newDeps(t)
	cpuRoot(t, &d3, "Raspberry Pi 5 Model B\x00", "2400000")
	fr3 := &fakeRunner{}
	d3.Priv = priv.Priv{R: fr3, Helper: "/h"}
	if w := cpuPost(NewHandler(d3), "800"); w.Code != http.StatusConflict ||
		!strings.Contains(w.Header().Get("HX-Trigger"), "This device does not support a CPU speed limit.") || len(fr3.called()) != 0 {
		t.Errorf("not supported: code=%d trigger=%q", w.Code, w.Header().Get("HX-Trigger"))
	}

	d4, _ := newDeps(t)
	cpuRoot(t, &d4, pi4Model, "1500000")
	er := &cpuErrRunner{}
	d4.Priv = priv.Priv{R: er, Helper: "/h"}
	w = cpuPost(NewHandler(d4), "800")
	if w.Code != 500 || !strings.Contains(w.Header().Get("HX-Trigger"), "The CPU speed could not be set.") || strings.Contains(w.Header().Get("HX-Trigger"), "raw helper") {
		t.Errorf("helper error: code=%d trigger=%q", w.Code, w.Header().Get("HX-Trigger"))
	}

	r := authed(httptest.NewRequest("POST", "/system/cpu", strings.NewReader("value=800")))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if w := serve(h, r); w.Code != http.StatusForbidden {
		t.Errorf("cross-site: code=%d", w.Code)
	}
}
