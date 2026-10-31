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

// Package cpu reads the CPU speed limit and the firmware throttle state of a Raspberry Pi 4
// (spec 2026-10-31-cpu-limit-design.md). Setting the limit is done by the root helper.
package cpu

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Values are the speeds the owner can choose, in MHz ("max" = the board top speed).
var Values = []string{"max", "1200", "1000", "800", "600"}

// ValidValue reports whether v is one of Values.
func ValidValue(v string) bool { return slices.Contains(Values, v) }

// Throttle is the firmware throttle state ("vcgencmd get_throttled" through the root helper).
// The "since boot" bits are not used: the kernel hwmon driver clears them every few seconds.
type Throttle struct {
	Known    bool // the state was read
	Now      bool // slowed down now (heat or power)
	NowHeat  bool // slowed down now because of heat
	NowPower bool // too little power now
}

// State is what the web UI shows.
type State struct {
	Supported bool   // a Pi 4 with cpufreq
	MaxMHz    int    // current top speed (scaling_max_freq)
	HWMaxMHz  int    // board top speed (cpuinfo_max_freq)
	Saved     string // saved choice, "" = none or not valid
}

const (
	modelFile = "proc/device-tree/model"
	policyDir = "sys/devices/system/cpu/cpufreq/policy0"
	savedFile = "var/lib/emulationstation-cpu/max-mhz"
)

func read(root, rel string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", "")), true
}

func readMHz(root, rel string) int {
	s, _ := read(root, rel)
	khz, err := strconv.Atoi(s)
	if err != nil || khz <= 0 {
		return 0
	}
	return khz / 1000
}

// Read collects the state below root ("" = "/"). Missing or broken files leave zero values.
func Read(root string) State {
	if root == "" {
		root = "/"
	}
	var s State
	model, _ := read(root, modelFile)
	s.MaxMHz = readMHz(root, policyDir+"/scaling_max_freq")
	s.HWMaxMHz = readMHz(root, policyDir+"/cpuinfo_max_freq")
	s.Supported = strings.HasPrefix(model, "Raspberry Pi 4") && s.MaxMHz > 0 && s.HWMaxMHz > 0
	if v, ok := read(root, savedFile); ok && ValidValue(v) {
		s.Saved = v
	}
	return s
}

// ParseThrottled reads the hex bit mask of the firmware (with or without "0x"). Bits: 0 under-
// voltage, 1 frequency capped, 2 throttled, 3 soft temperature limit (all "now"). Junk gives
// Known=false.
func ParseThrottled(raw string) Throttle {
	bits, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(raw), "0x"), 16, 32)
	if err != nil {
		return Throttle{}
	}
	return Throttle{
		Known:    true,
		Now:      bits&0xf != 0,
		NowPower: bits&0x1 != 0,
		NowHeat:  bits&0x1 == 0 && bits&0xe != 0,
	}
}

// Choices returns the values that are not above the board top speed ("max" always).
func (s State) Choices() []string {
	var out []string
	for _, v := range Values {
		if mhz, err := strconv.Atoi(v); err == nil && mhz > s.HWMaxMHz {
			continue
		}
		out = append(out, v)
	}
	return out
}

// Current returns the saved choice, or "max" when none is saved.
func (s State) Current() string {
	if s.Saved == "" {
		return "max"
	}
	return s.Saved
}
