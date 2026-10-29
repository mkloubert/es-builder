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

package controllers

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Device is a joystick found in sysfs.
type Device struct {
	Name, Bus                string // Bus: "usb" | "bluetooth" | "other"
	Vendor, Product, Version uint16
	GUID, GUIDCRC            string
	Configured               bool
}

// Enumerate lists joysticks below <sysRoot>/class/input/js*, ordered by index.
// Entries with unreadable files are skipped.
func Enumerate(sysRoot string) ([]Device, error) {
	dirs, err := filepath.Glob(filepath.Join(sysRoot, "class", "input", "js*"))
	if err != nil {
		return nil, err
	}
	type entry struct {
		idx int
		dev Device
	}
	var found []entry
	for _, dir := range dirs {
		idx, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(dir), "js"))
		if err != nil {
			continue
		}
		d, ok := readDevice(filepath.Join(dir, "device"))
		if ok {
			found = append(found, entry{idx, d})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].idx < found[j].idx })
	devs := make([]Device, len(found))
	for i, f := range found {
		devs[i] = f.dev
	}
	return devs, nil
}

func readDevice(dir string) (Device, bool) {
	nameB, err := os.ReadFile(filepath.Join(dir, "name"))
	if err != nil {
		return Device{}, false
	}
	var v [4]uint16
	for i, f := range []string{"bustype", "vendor", "product", "version"} {
		b, err := os.ReadFile(filepath.Join(dir, "id", f))
		if err != nil {
			return Device{}, false
		}
		n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 16, 16)
		if err != nil {
			return Device{}, false
		}
		v[i] = uint16(n)
	}
	d := Device{
		Name:    strings.TrimRight(string(nameB), "\r\n"),
		Vendor:  v[1],
		Product: v[2],
		Version: v[3],
	}
	switch v[0] {
	case 0x03:
		d.Bus = "usb"
	case 0x05:
		d.Bus = "bluetooth"
	default:
		d.Bus = "other"
	}
	d.GUID = SDLGUID(v[0], d.Vendor, d.Product, d.Version, d.Name, false)
	d.GUIDCRC = SDLGUID(v[0], d.Vendor, d.Product, d.Version, d.Name, true)
	return d, true
}
