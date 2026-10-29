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

package gamelist

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"emulationstation/webui/internal/config"
)

func TestRoundTripPreservesUnknownFieldsAndFolders(t *testing.T) {
	for _, name := range []string{"basic.xml", "with-folder.xml"} {
		l1, err := Read(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "gamelist.xml")
		if err := Write(out, l1); err != nil {
			t.Fatal(err)
		}
		l2, err := Read(out)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(l1, l2) {
			t.Errorf("%s: round trip differs:\n%+v\n%+v", name, l1, l2)
		}
	}
	l, _ := Read("testdata/basic.xml")
	if v, ok := l.Field("./Super Mario World.sfc", "favorite"); !ok || v != "true" {
		t.Errorf("favorite = %q %v", v, ok)
	}
	if v, _ := l.Field("./Super Mario World.sfc", "customtag"); v != "x & y" {
		t.Errorf("customtag = %q", v)
	}
	f, _ := Read("testdata/with-folder.xml")
	if len(f.Other) != 1 || f.Other[0].XMLName.Local != "folder" {
		t.Errorf("folder lost: %+v", f.Other)
	}
}

func TestSetNameEscapes(t *testing.T) {
	l := &List{}
	l.Set("./x.sfc", map[string]string{"name": "Tom & Jerry <1>"})
	out := filepath.Join(t.TempDir(), "g.xml")
	if err := Write(out, l); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(out)
	if !strings.Contains(string(raw), "Tom &amp; Jerry &lt;1&gt;") {
		t.Errorf("not escaped: %s", raw)
	}
	l2, err := Read(out)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := l2.Field("./x.sfc", "name"); v != "Tom & Jerry <1>" {
		t.Errorf("name = %q", v)
	}
	if !l2.Remove("./x.sfc") || l2.Remove("./x.sfc") {
		t.Error("Remove behaviour wrong")
	}
}

func TestSetUpdatesInPlace(t *testing.T) {
	l, _ := Read("testdata/basic.xml")
	l.Set("./Zelda.sfc", map[string]string{"name": "Z", "desc": "d"})
	g := l.Games[1]
	if g.Fields[1].XMLName.Local != "name" || g.Fields[1].Inner != "Z" || g.Fields[2].XMLName.Local != "desc" {
		t.Errorf("%+v", g.Fields)
	}
}

func TestReadMissingIsEmpty(t *testing.T) {
	l, err := Read(filepath.Join(t.TempDir(), "nope.xml"))
	if err != nil || l == nil || len(l.Games) != 0 {
		t.Fatalf("%v %v", l, err)
	}
}

func TestReadBrokenFails(t *testing.T) {
	if _, err := Read("testdata/broken.xml"); err == nil {
		t.Fatal("expected error")
	}
}

func TestWriteCreatesBackup(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "gamelist.xml")
	l := &List{}
	l.Set("./a", map[string]string{"name": "A"})
	if err := Write(p, l); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p + ".bak"); err == nil {
		t.Error("unexpected bak on first write")
	}
	first, _ := os.ReadFile(p)
	l.Set("./a", map[string]string{"name": "B"})
	if err := Write(p, l); err != nil {
		t.Fatal(err)
	}
	bak, err := os.ReadFile(p + ".bak")
	if err != nil || string(bak) != string(first) {
		t.Errorf("bak wrong: %v", err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o644 {
		t.Errorf("mode %v", st.Mode())
	}
}

func TestPathForPrefersRomDir(t *testing.T) {
	roms, es := t.TempDir(), t.TempDir()
	p := config.Paths{Roms: roms, ESConfigDir: es}
	want := filepath.Join(es, "gamelists", "snes", "gamelist.xml")
	if got := PathFor(p, "snes"); got != want {
		t.Errorf("got %s want %s", got, want)
	}
	os.MkdirAll(filepath.Join(roms, "snes"), 0o755)
	rp := filepath.Join(roms, "snes", "gamelist.xml")
	os.WriteFile(rp, []byte("<gameList/>"), 0o644)
	if got := PathFor(p, "snes"); got != rp {
		t.Errorf("got %s want %s", got, rp)
	}
}

func TestBrokenMessageNamesFile(t *testing.T) {
	got := BrokenMessage("/x/gamelist.xml")
	want := "The game list /x/gamelist.xml is damaged. Please repair or delete it (a backup may be next to it as .bak)."
	if got != want {
		t.Fatal(got)
	}
}

func TestTextFastPathMatchesDecoder(t *testing.T) {
	for _, in := range []string{
		"", "./Mario.sfc", "./Pack/Super Mario World (USA).sfc", "Pokémon ✓", "a > b", `say "hi"`, "it's",
		"Tom &amp; Jerry", "<![CDATA[x < y]]>", "a\r\nb", "  spaces  ", "&#x41;",
	} {
		f := Field{Inner: in}
		if got, want := f.text(), f.decodeText(); got != want {
			t.Errorf("text(%q) = %q, want %q", in, got, want)
		}
	}
}
