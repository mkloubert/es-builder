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

package bios

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"emulationstation/webui/internal/roms"
)

func TestCheckStates(t *testing.T) {
	dir := t.TempDir()
	content := []byte("fake bios")
	sum := md5.Sum(content)
	orig := Requirements
	defer func() { Requirements = orig }()
	Requirements = []Requirement{
		{System: "a", File: "ok.bin", MD5: strings.ToUpper(hex.EncodeToString(sum[:]))},
		{System: "b", File: "bad.bin", MD5: hex.EncodeToString(sum[:])},
		{System: "c", File: "gone.bin", MD5: hex.EncodeToString(sum[:])},
		{System: "d", File: "any.bin"},
	}
	os.WriteFile(filepath.Join(dir, "ok.bin"), content, 0o644)
	os.WriteFile(filepath.Join(dir, "bad.bin"), []byte("other"), 0o644)
	os.WriteFile(filepath.Join(dir, "any.bin"), []byte("x"), 0o644)
	got := Check(dir)
	want := []string{"ok", "mismatch", "missing", "ok"}
	if len(got) != len(want) {
		t.Fatalf("len %d", len(got))
	}
	for i, w := range want {
		if got[i].State != w || got[i].File != Requirements[i].File {
			t.Errorf("%d: %+v want %s", i, got[i], w)
		}
	}
}

func TestRequirementsTable(t *testing.T) {
	if len(Requirements) != 18 {
		t.Fatalf("got %d", len(Requirements))
	}
	for _, r := range Requirements {
		if r.File == "dc/dc_flash.bin" && !r.Optional {
			t.Error("dc_flash.bin must be optional")
		}
		if r.File == "dc/dc_boot.bin" && r.Optional {
			t.Error("dc_boot.bin must be required")
		}
	}
}

func TestSaveDreamcastSubdir(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, "dc/dc_boot.bin", strings.NewReader("abc")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "dc", "dc_boot.bin"))
	if err != nil || string(b) != "abc" {
		t.Fatalf("%v %q", err, b)
	}
	fi, _ := os.Stat(filepath.Join(dir, "dc", "dc_boot.bin"))
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode %v", fi.Mode())
	}
	if err := Save(dir, "plain.bin", strings.NewReader("z")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plain.bin")); err != nil {
		t.Fatal(err)
	}
}

func TestSaveRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"../x", "a/b", "dc/../x", "/etc/passwd", "other/x.bin", "dc/", "..", "", "dc/sub/x"} {
		err := Save(dir, n, strings.NewReader("x"))
		if !errors.Is(err, roms.ErrUnsafePath) {
			t.Errorf("%q: err %v", n, err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("leftovers: %v", entries)
	}
}

type endless struct{}

func (endless) Read(p []byte) (int, error) { return len(p), nil }

func TestSaveTooLarge(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, "big.bin", endless{}); err == nil {
		t.Fatal("expected error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("leftovers: %v", entries)
	}
}
