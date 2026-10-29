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

package systems

import (
	"path/filepath"
	"reflect"
	"testing"
)

const fixture = "testdata/es_systems.cfg"

func load(t *testing.T) []System {
	t.Helper()
	l, err := Load(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func names(l []System) []string {
	out := []string{}
	for _, s := range l {
		out = append(out, s.Name)
	}
	return out
}

func TestLoadParsesExtensionsLowercaseDedup(t *testing.T) {
	s, ok := ByName(load(t), "snes")
	if !ok {
		t.Fatal("snes missing")
	}
	want := []string{".sfc", ".smc", ".zip"}
	if !reflect.DeepEqual(s.Extensions, want) {
		t.Errorf("Extensions = %v, want %v", s.Extensions, want)
	}
	if s.FullName != "Super Nintendo" || s.Path != "~/RetroPie/roms/snes" {
		t.Errorf("got %+v", s)
	}
}

func TestKeepArchives(t *testing.T) {
	l := load(t)
	for name, want := range map[string]bool{"arcade": true, "neogeo": true, "nes": false} {
		s, _ := ByName(l, name)
		if s.KeepArchives != want {
			t.Errorf("%s KeepArchives = %v, want %v", name, s.KeepArchives, want)
		}
	}
}

func TestByNameMissing(t *testing.T) {
	if _, ok := ByName(load(t), "nope"); ok {
		t.Error("expected not found")
	}
}

func TestCandidatesUnique(t *testing.T) {
	got := names(Candidates(load(t), "x.sfc"))
	if !reflect.DeepEqual(got, []string{"snes"}) {
		t.Errorf("got %v", got)
	}
}

func TestCandidatesAmbiguous(t *testing.T) {
	l := load(t)
	got := names(Candidates(l, "x.ZIP"))
	want := []string{"arcade", "megadrive", "neogeo", "nes", "snes"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("zip: got %v, want %v", got, want)
	}
	got = names(Candidates(l, "x.bin"))
	want = []string{"megadrive", "psx", "segacd"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bin: got %v, want %v", got, want)
	}
}

func TestCandidatesNone(t *testing.T) {
	l := load(t)
	for _, f := range []string{"x.txt", "noext"} {
		if got := Candidates(l, f); len(got) != 0 {
			t.Errorf("%s: got %v", f, got)
		}
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.cfg")); err == nil {
		t.Error("expected error")
	}
}
