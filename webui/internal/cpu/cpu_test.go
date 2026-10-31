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

package cpu

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fake writes a fake root; empty values leave the file out.
func fake(t *testing.T, model, hwmax, cur, saved string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"proc/device-tree/model":                                  model,
		"sys/devices/system/cpu/cpufreq/policy0/cpuinfo_max_freq": hwmax,
		"sys/devices/system/cpu/cpufreq/policy0/scaling_max_freq": cur,
		"var/lib/emulationstation-cpu/max-mhz":                    saved,
	}
	for name, body := range files {
		if body == "" {
			continue
		}
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const pi4 = "Raspberry Pi 4 Model B Rev 1.1\x00"

func TestReadSupported(t *testing.T) {
	cases := []struct {
		name, model, hwmax, cur string
		want                    bool
	}{
		{"pi4", pi4, "1500000\n", "1200000\n", true},
		{"pi5", "Raspberry Pi 5 Model B Rev 1.0\x00", "2400000\n", "2400000\n", false},
		{"no policy", pi4, "", "", false},
		{"no model", "", "1500000\n", "1500000\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Read(fake(t, tc.model, tc.hwmax, tc.cur, ""))
			if s.Supported != tc.want {
				t.Fatalf("Supported = %v, want %v", s.Supported, tc.want)
			}
			if tc.want && (s.MaxMHz != 1200 || s.HWMaxMHz != 1500) {
				t.Fatalf("MHz = %d / %d", s.MaxMHz, s.HWMaxMHz)
			}
		})
	}
}

func TestParseThrottled(t *testing.T) {
	cases := []struct {
		raw  string
		want Throttle
	}{
		{"", Throttle{}},
		{"junk", Throttle{}},
		{"0x", Throttle{}},
		{"0xffffffffff", Throttle{}},
		{"0x0", Throttle{Known: true}},
		{"0", Throttle{Known: true}},
		{"0x50005", Throttle{Known: true, Now: true, NowPower: true}},
		{"50005\n", Throttle{Known: true, Now: true, NowPower: true}},
		{"0x20002", Throttle{Known: true, Now: true, NowHeat: true}},
		{"0x8", Throttle{Known: true, Now: true, NowHeat: true}},
		{"0x4", Throttle{Known: true, Now: true, NowHeat: true}},
		{"0x80000", Throttle{Known: true}}, // "since boot" bits are not used
	}
	for _, tc := range cases {
		if got := ParseThrottled(tc.raw); got != tc.want {
			t.Errorf("ParseThrottled(%q) = %+v, want %+v", tc.raw, got, tc.want)
		}
	}
}

func TestReadSaved(t *testing.T) {
	for raw, want := range map[string]string{"1000\n": "1000", "max\n": "max", "junk\n": "", "": ""} {
		if got := Read(fake(t, pi4, "1500000", "1000000", raw)).Saved; got != want {
			t.Errorf("Saved(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestChoicesAndValid(t *testing.T) {
	s := State{Supported: true, HWMaxMHz: 1000}
	if got, want := s.Choices(), []string{"max", "1000", "800", "600"}; !slices.Equal(got, want) {
		t.Errorf("Choices = %v, want %v", got, want)
	}
	for v, want := range map[string]bool{"max": true, "1200": true, "1000": true, "800": true, "600": true,
		"1100": false, "": false, "MAX": false, " 800": false, "1200000": false} {
		if ValidValue(v) != want {
			t.Errorf("ValidValue(%q) = %v", v, !want)
		}
	}
}

func TestCurrent(t *testing.T) {
	if got := (State{}).Current(); got != "max" {
		t.Errorf("Current() = %q", got)
	}
	if got := (State{Saved: "800"}).Current(); got != "800" {
		t.Errorf("Current() = %q", got)
	}
}
