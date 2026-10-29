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

// Package bios checks for required BIOS files and stores uploaded ones.
package bios

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"emulationstation/webui/internal/roms"
)

// MaxSize is the largest accepted BIOS upload.
const MaxSize = 64 << 20

// ErrTooLarge is returned by Save for oversized input.
var ErrTooLarge = errors.New("BIOS file is too large")

// Requirement describes one BIOS file. MD5 "" only checks presence.
type Requirement struct {
	System, File, MD5 string
	Optional          bool
}

// Requirements lists the known BIOS files, relative to the BIOS directory.
var Requirements = []Requirement{
	{"psx", "scph5501.bin", "490f666e1afb15b7362b406ed1cea246", false},
	{"psx", "scph1001.bin", "924e392ed05558ffdb115408c263dccf", true},
	{"psx", "scph7001.bin", "1e68c231d0896b7eadcad1d7d8e76129", true},
	{"dreamcast", "dc/dc_boot.bin", "e10c53c2f8b90bab96ead2d368858623", false},
	{"dreamcast", "dc/dc_flash.bin", "0a93f7940c455905bea6e392dfde92a4", true},
	{"segacd", "bios_CD_U.bin", "2efd74e3232ff260e371b99f84024f7f", false},
	{"segacd", "bios_CD_E.bin", "e66fa1dc5820d254611fdcdba0662372", true},
	{"segacd", "bios_CD_J.bin", "278a9397d192149e84e820ac621a8edd", true},
	{"pcengine", "syscard3.pce", "38179df8f4ac870017db21ebcbf53114", false},
	{"gba", "gba_bios.bin", "a860e8c0b6d573d191e4ec7db1b1e4f6", true},
	{"atarilynx", "lynxboot.img", "fcd403db69f54290b51035d82f835e7b", false},
	{"atari5200", "5200.rom", "281f20ea4320404ec820fb7ec0693b38", false},
	{"atari7800", "7800 BIOS (U).rom", "0763f1ffb006ddbe32e52d497ee848ae", true},
	{"amiga", "kick34005.A500", "82a21c1890cae844b3df741f2762d48d", false},
	{"amiga", "kick40068.A1200", "646773759326fbac3b2311fd8c8793ee", true},
	{"intellivision", "exec.bin", "62e761035cb657903761800f4437b8af", false},
	{"intellivision", "grom.bin", "0cd5946c6473e42e8e4c2137785e427f", false},
	{"neogeo", "neogeo.zip", "", false},
}

// Status is a Requirement with its state: "ok", "missing" or "mismatch".
type Status struct {
	Requirement
	State string
}

// Check inspects dir and returns one Status per Requirement, in order.
func Check(dir string) []Status {
	out := make([]Status, 0, len(Requirements))
	for _, r := range Requirements {
		out = append(out, Status{r, state(dir, r)})
	}
	return out
}

func state(dir string, r Requirement) string {
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(r.File)))
	if err != nil {
		return "missing"
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return "missing"
	}
	if r.MD5 == "" {
		return "ok"
	}
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "missing"
	}
	if strings.EqualFold(hex.EncodeToString(h.Sum(nil)), r.MD5) {
		return "ok"
	}
	return "mismatch"
}

// Save stores r as filename ("name" or "dc/name") below dir.
func Save(dir, filename string, r io.Reader) error {
	sub := ""
	name := filename
	if strings.HasPrefix(filename, "dc/") {
		sub = "dc"
		name = filename[3:]
	}
	if strings.ContainsAny(name, "/\\") {
		return roms.ErrUnsafePath
	}
	base, err := roms.SafeName(name)
	if err != nil {
		return err
	}
	target := dir
	if sub != "" {
		target = filepath.Join(dir, sub)
		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}
	} else if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(target, ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(r, MaxSize+1))
	if err == nil && n > MaxSize {
		err = ErrTooLarge
	}
	if err == nil {
		err = tmp.Chmod(0o644)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("save bios: %w", err)
	}
	return os.Rename(tmp.Name(), filepath.Join(target, base))
}
