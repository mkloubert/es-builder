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

package roms

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"emulationstation/webui/internal/config"
	"emulationstation/webui/internal/gamelist"
	"emulationstation/webui/internal/systems"
)

type ent struct{ name, body string }

func mkZip(t *testing.T, ents ...ent) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range ents {
		f, err := w.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(e.body))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeZip(t *testing.T, ents ...ent) string {
	p := filepath.Join(t.TempDir(), "a.zip")
	if err := os.WriteFile(p, mkZip(t, ents...), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func testSystems() []systems.System {
	return []systems.System{
		{Name: "snes", Extensions: []string{".sfc", ".smc", ".zip"}},
		{Name: "psx", Extensions: []string{".cue", ".m3u", ".chd"}},
		{Name: "dreamcast", Extensions: []string{".gdi"}},
		{Name: "saturn", Extensions: []string{".ccd"}},
		{Name: "arcade", Extensions: []string{".zip"}, KeepArchives: true},
		{Name: "neogeo", Extensions: []string{".zip"}, KeepArchives: true},
		{Name: "megadrive", Extensions: []string{".md", ".bin", ".zip"}},
		{Name: "ports", Extensions: []string{".sh"}},
	}
}

func testCfg(t *testing.T) *config.Config {
	home := t.TempDir()
	c := config.Default()
	c.Paths.Roms = filepath.Join(home, "roms")
	c.Paths.Staging = filepath.Join(home, "staging")
	c.Paths.ESConfigDir = filepath.Join(home, "es")
	return c
}

func newStager(t *testing.T) (*Stager, *config.Config) {
	c := testCfg(t)
	return NewStager(c, testSystems()), c
}

func isEmptyDir(t *testing.T, dir string) bool {
	es, err := os.ReadDir(dir)
	if err != nil {
		return os.IsNotExist(err)
	}
	return len(es) == 0
}

func TestSafeName(t *testing.T) {
	if n, err := SafeName("../../etc/passwd"); err != nil || n != "passwd" {
		t.Fatalf("got %q %v", n, err)
	}
	if n, err := SafeName("Pokémon Rot.gb"); err != nil || n != "Pokémon Rot.gb" {
		t.Fatalf("got %q %v", n, err)
	}
	for _, bad := range []string{"..", ".", "", "a\x00b", "x/.."} {
		if _, err := SafeName(bad); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestExtractRejectsZipSlip(t *testing.T) {
	base := t.TempDir()
	dest := filepath.Join(base, "dest")
	zp := writeZip(t, ent{"ok.sfc", "x"}, ent{"../evil", "x"})
	if _, err := Extract(zp, dest, 1<<20, 100); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "evil")); err == nil {
		t.Fatal("evil written")
	}
	if !isEmptyDir(t, dest) {
		t.Fatal("dest not empty")
	}
}

func TestExtractRejectsAbsolute(t *testing.T) {
	dest := t.TempDir()
	zp := writeZip(t, ent{"/tmp/x", "x"})
	if _, err := Extract(zp, dest, 1<<20, 100); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("got %v", err)
	}
}

func TestExtractFlattens(t *testing.T) {
	dest := t.TempDir()
	zp := writeZip(t, ent{"sub/dir/a.sfc", "1"}, ent{"sub/", ""})
	got, err := Extract(zp, dest, 1<<20, 100)
	if err != nil || len(got) != 1 || got[0] != "a.sfc" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestExtractTooLarge(t *testing.T) {
	body := strings.Repeat("A", 100)
	// honest header
	dest := t.TempDir()
	zp := writeZip(t, ent{"a.bin", body})
	if _, err := Extract(zp, dest, 50, 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v", err)
	}
	if !isEmptyDir(t, dest) {
		t.Fatal("dest not empty")
	}
	// lying header: patch the central directory's uncompressed size to 1
	data := mkZip(t, ent{"a.bin", body})
	i := bytes.Index(data, []byte("PK\x01\x02"))
	if i < 0 {
		t.Fatal("no central dir")
	}
	data[i+28], data[i+29], data[i+30], data[i+31] = 1, 0, 0, 0
	p := filepath.Join(t.TempDir(), "lie.zip")
	os.WriteFile(p, data, 0o644)
	dest2 := t.TempDir()
	if _, err := Extract(p, dest2, 50, 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("lying header: got %v", err)
	}
	if !isEmptyDir(t, dest2) {
		t.Fatal("dest2 not empty")
	}
}

func TestExtractTooManyEntries(t *testing.T) {
	dest := t.TempDir()
	zp := writeZip(t, ent{"a", "1"}, ent{"b", "1"}, ent{"c", "1"})
	if _, err := Extract(zp, dest, 1<<20, 2); !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("got %v", err)
	}
	if !isEmptyDir(t, dest) {
		t.Fatal("dest not empty")
	}
}

func find(u *Upload, name string) *StagedFile {
	for i := range u.Files {
		if u.Files[i].Name == name {
			return &u.Files[i]
		}
	}
	return nil
}

func TestReceiveSnesZipExtracts(t *testing.T) {
	s, _ := newStager(t)
	u, err := s.Receive(bytes.NewReader(mkZip(t, ent{"Mario (USA).sfc", "rom"}, ent{"readme.txt", "hi"})), "mario.zip")
	if err != nil {
		t.Fatal(err)
	}
	m := find(u, "Mario (USA).sfc")
	if m == nil || m.Discarded || m.CompanionOf != "" || len(m.Candidates) != 1 || m.Candidates[0] != "snes" || m.SuggestedName != "Mario" {
		t.Fatalf("%+v", m)
	}
	r := find(u, "readme.txt")
	if r == nil || !r.Discarded || r.Reason != "unknown file type" {
		t.Fatalf("%+v", r)
	}
	if got, ok := s.Get(u.ID); !ok || len(got.Files) != 2 {
		t.Fatal("Get failed")
	}
}

func TestReceiveArcadeZipKept(t *testing.T) {
	s, cfg := newStager(t)
	u, err := s.Receive(bytes.NewReader(mkZip(t, ent{"p1.bin", "1"}, ent{"c1.rom", "2"}, ent{"mslug.u1", "3"})), "mslug.zip")
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Files) != 1 || u.Files[0].Name != "mslug.zip" || u.Files[0].Discarded {
		t.Fatalf("%+v", u.Files)
	}
	if got := strings.Join(u.Files[0].Candidates, ","); got != "arcade,megadrive,neogeo,snes" {
		t.Fatalf("candidates %s", got)
	}
	if _, err := os.Stat(filepath.Join(cfg.Paths.Staging, u.ID, "mslug.zip")); err != nil {
		t.Fatal(err)
	}
}

func TestReceiveCueBinCompanions(t *testing.T) {
	s, _ := newStager(t)
	cue := "FILE \"FF7 (Disc 1) (Track 1).bin\" BINARY\n  TRACK 01 MODE2/2352\n"
	u, err := s.Receive(bytes.NewReader(mkZip(t,
		ent{"FF7 (Disc 1).cue", cue}, ent{"disc/FF7 (Disc 1) (Track 1).bin", "bin"}, ent{"notes.txt", "n"})), "ff7.zip")
	if err != nil {
		t.Fatal(err)
	}
	b := find(u, "FF7 (Disc 1) (Track 1).bin")
	if b == nil || b.CompanionOf != "FF7 (Disc 1).cue" || b.Discarded {
		t.Fatalf("%+v", b)
	}
	if c := find(u, "FF7 (Disc 1).cue"); c == nil || c.CompanionOf != "" {
		t.Fatalf("%+v", c)
	}
	if n := find(u, "notes.txt"); !n.Discarded {
		t.Fatal("txt kept")
	}
}

func TestReceiveGdiTracks(t *testing.T) {
	s, _ := newStager(t)
	gdi := "3\n1 0 4 2352 track01.bin 0\n2 600 0 2352 \"track02.raw\" 0\n3 45000 4 2352 track03.bin 0\n"
	u, err := s.Receive(bytes.NewReader(mkZip(t, ent{"game.gdi", gdi}, ent{"track01.bin", "1"}, ent{"track02.raw", "2"})), "g.zip")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"track01.bin", "track02.raw"} {
		if f := find(u, n); f == nil || f.CompanionOf != "game.gdi" {
			t.Fatalf("%s: %+v", n, f)
		}
	}
}

func TestCompanionsFormats(t *testing.T) {
	d := t.TempDir()
	for _, n := range []string{"a.img", "a.sub", "a.ccd", "My Game.bin", "x.m3u", "d1.cue", "d1.bin"} {
		os.WriteFile(filepath.Join(d, n), []byte("FILE \"My Game.bin\" BINARY\n"), 0o644)
	}
	os.WriteFile(filepath.Join(d, "x.m3u"), []byte("#c\nd1.cue\n\nmissing.cue\n"), 0o644)
	if got, _ := Companions(filepath.Join(d, "a.ccd")); len(got) != 2 {
		t.Fatalf("ccd %v", got)
	}
	if got, _ := Companions(filepath.Join(d, "x.m3u")); len(got) != 1 || got[0] != "d1.cue" {
		t.Fatalf("m3u %v", got)
	}
	if got, _ := Companions(filepath.Join(d, "d1.cue")); len(got) != 1 || got[0] != "My Game.bin" {
		t.Fatalf("cue %v", got)
	}
}

func TestReceiveDuplicateBasename(t *testing.T) {
	s, _ := newStager(t)
	u, err := s.Receive(bytes.NewReader(mkZip(t, ent{"a/x.sfc", "1"}, ent{"b/x.sfc", "2"})), "d.zip")
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Files) != 2 || u.Files[0].Discarded || !u.Files[1].Discarded || u.Files[1].Reason != "duplicate file name" {
		t.Fatalf("%+v", u.Files)
	}
}

func TestReceiveUnknownPlainFile(t *testing.T) {
	s, _ := newStager(t)
	u, err := s.Receive(strings.NewReader("x"), "foo.xyz")
	if err != nil || !u.Files[0].Discarded || u.Files[0].Reason != "unknown file type" {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestReceiveUploadTooLargeCleansUp(t *testing.T) {
	s, cfg := newStager(t)
	cfg.MaxUploadBytes = 10
	if _, err := s.Receive(strings.NewReader("12345678901"), "a.sfc"); !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("got %v", err)
	}
	if !isEmptyDir(t, cfg.Paths.Staging) {
		t.Fatal("staging not empty")
	}
	cfg.MaxUploadBytes = 10
	if _, err := s.Receive(strings.NewReader("1234567890"), "a.sfc"); err != nil {
		t.Fatalf("exact limit: %v", err)
	}
}

type failReader struct{ n int }

func (f *failReader) Read(p []byte) (int, error) {
	if f.n == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, "12345"[:f.n])
	f.n -= n
	return n, nil
}

func TestReceiveReaderErrorCleansUp(t *testing.T) {
	s, cfg := newStager(t)
	if _, err := s.Receive(&failReader{n: 5}, "a.sfc"); err == nil {
		t.Fatal("want error")
	}
	if !isEmptyDir(t, cfg.Paths.Staging) {
		t.Fatal("staging not empty")
	}
	if _, err := s.Receive(&failReader{n: 5}, "a.zip"); err == nil {
		t.Fatal("want error")
	}
	if !isEmptyDir(t, cfg.Paths.Staging) {
		t.Fatal("staging not empty (zip)")
	}
}

func TestReceiveBadZipCleansUp(t *testing.T) {
	s, cfg := newStager(t)
	if _, err := s.Receive(strings.NewReader("not a zip"), "a.zip"); err == nil {
		t.Fatal("want error")
	}
	if !isEmptyDir(t, cfg.Paths.Staging) {
		t.Fatal("staging not empty")
	}
}

func TestCleanAllAndDiscard(t *testing.T) {
	s, cfg := newStager(t)
	u, _ := s.Receive(strings.NewReader("x"), "a.sfc")
	if err := s.Discard(u.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(u.ID); ok || !isEmptyDir(t, cfg.Paths.Staging) {
		t.Fatal("not discarded")
	}
	s.Receive(strings.NewReader("x"), "a.sfc")
	if err := s.CleanAll(); err != nil || !isEmptyDir(t, cfg.Paths.Staging) {
		t.Fatalf("%v", err)
	}
}

func TestCommitMovesAndWritesGamelist(t *testing.T) {
	s, cfg := newStager(t)
	u, err := s.Receive(bytes.NewReader(mkZip(t,
		ent{"Disc.cue", "FILE \"t1.bin\" BINARY\n"}, ent{"t1.bin", "bin"},
		ent{"Mario (USA).sfc", "rom"}, ent{"junk.txt", "j"})), "x.zip")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Commit(u.ID, []Choice{
		{Name: "Mario (USA).sfc", System: "snes", DisplayName: "Super Mario"},
		{Name: "Disc.cue", System: "psx", DisplayName: "Disc"},
	}, false)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	for _, p := range []string{"snes/Mario (USA).sfc", "psx/Disc.cue", "psx/t1.bin"} {
		if _, err := os.Stat(filepath.Join(cfg.Paths.Roms, p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.Paths.Roms, "snes", "junk.txt")); err == nil {
		t.Error("discarded file moved")
	}
	l, _ := gamelist.Read(gamelist.PathFor(cfg.Paths, "snes"))
	if v, ok := l.Field("./Mario (USA).sfc", "name"); !ok || v != "Super Mario" {
		t.Fatalf("name %q %v", v, ok)
	}
	pl, _ := gamelist.Read(gamelist.PathFor(cfg.Paths, "psx"))
	if _, ok := pl.Field("./t1.bin", "name"); ok {
		t.Error("companion has entry")
	}
	if _, ok := pl.Field("./Disc.cue", "name"); !ok {
		t.Error("primary has no entry")
	}
	if _, ok := s.Get(u.ID); ok || !isEmptyDir(t, cfg.Paths.Staging) {
		t.Error("staging not cleaned")
	}
}

func TestCommitSkipMovesNothing(t *testing.T) {
	s, cfg := newStager(t)
	u, _ := s.Receive(strings.NewReader("x"), "a.sfc")
	got, err := s.Commit(u.ID, []Choice{{Name: "a.sfc", Skip: true}}, false)
	if err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Paths.Roms, "snes", "a.sfc")); err == nil {
		t.Fatal("moved")
	}
}

func TestCommitExistingFileWithoutOverwriteFails(t *testing.T) {
	s, cfg := newStager(t)
	dir := filepath.Join(cfg.Paths.Roms, "snes")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "a.sfc"), []byte("old"), 0o644)
	u, _ := s.Receive(strings.NewReader("new"), "a.sfc")
	_, err := s.Commit(u.ID, []Choice{{Name: "a.sfc", System: "snes"}}, false)
	if err == nil || !strings.Contains(err.Error(), "a.sfc") {
		t.Fatalf("got %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.sfc")); string(b) != "old" {
		t.Fatal("overwritten")
	}
	if _, err := s.Commit(u.ID, []Choice{{Name: "a.sfc", System: "snes"}}, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.sfc")); string(b) != "new" {
		t.Fatal("not overwritten")
	}
}

func TestCommitBrokenGamelistMovesNothing(t *testing.T) {
	s, cfg := newStager(t)
	glp := gamelist.PathFor(cfg.Paths, "snes")
	os.MkdirAll(filepath.Dir(glp), 0o755)
	os.WriteFile(glp, []byte("<gameList><game>"), 0o644)
	u, _ := s.Receive(strings.NewReader("x"), "a.sfc")
	if _, err := s.Commit(u.ID, []Choice{{Name: "a.sfc", System: "snes"}}, false); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(filepath.Join(cfg.Paths.Roms, "snes", "a.sfc")); err == nil {
		t.Fatal("moved")
	}
	if b, _ := os.ReadFile(glp); string(b) != "<gameList><game>" {
		t.Fatal("gamelist modified")
	}
}

func TestCommitRejectsWrongSystem(t *testing.T) {
	s, _ := newStager(t)
	u, _ := s.Receive(strings.NewReader("x"), "a.sfc")
	if _, err := s.Commit(u.ID, []Choice{{Name: "a.sfc", System: "../etc"}}, false); err == nil {
		t.Fatal("want error")
	}
	if _, err := s.Commit(u.ID, []Choice{{Name: "a.sfc", System: "psx"}}, false); err == nil {
		t.Fatal("want error")
	}
}

func TestDeleteROMRemovesCompanionsAndEntry(t *testing.T) {
	_, cfg := newStager(t)
	dir := filepath.Join(cfg.Paths.Roms, "psx")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "D.cue"), []byte("FILE \"t1.bin\" BINARY\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "t1.bin"), []byte("b"), 0o644)
	os.WriteFile(filepath.Join(dir, "other.bin"), []byte("b"), 0o644)
	glp := gamelist.PathFor(cfg.Paths, "psx")
	l := &gamelist.List{}
	l.Set("./D.cue", map[string]string{"name": "D"})
	l.Set("./other.bin", map[string]string{"name": "O"})
	if err := gamelist.Write(glp, l); err != nil {
		t.Fatal(err)
	}
	if err := DeleteROM(cfg, "psx", "./D.cue"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"D.cue", "t1.bin"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			t.Errorf("%s remains", n)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "other.bin")); err != nil {
		t.Error("other removed")
	}
	l2, _ := gamelist.Read(glp)
	if _, ok := l2.Field("./D.cue", "name"); ok {
		t.Error("entry remains")
	}
	if _, ok := l2.Field("./other.bin", "name"); !ok {
		t.Error("other entry gone")
	}
}

func TestDeleteROMRejectsTraversal(t *testing.T) {
	_, cfg := newStager(t)
	for _, c := range [][2]string{{"psx", "../x"}, {"../psx", "a"}, {"psx", "/etc/passwd"}, {"", "a"}} {
		if err := DeleteROM(cfg, c[0], c[1]); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%v: %v", c, err)
		}
	}
}

func TestDeleteROMBrokenGamelistDeletesNothing(t *testing.T) {
	_, cfg := newStager(t)
	dir := filepath.Join(cfg.Paths.Roms, "snes")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "a.sfc"), []byte("x"), 0o644)
	glp := gamelist.PathFor(cfg.Paths, "snes")
	os.MkdirAll(filepath.Dir(glp), 0o755)
	os.WriteFile(glp, []byte("<gameList><game>"), 0o644)
	if err := DeleteROM(cfg, "snes", "./a.sfc"); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(filepath.Join(dir, "a.sfc")); err != nil {
		t.Fatal("deleted")
	}
}

func TestReceiveZipWithMdExtracts(t *testing.T) {
	s, _ := newStager(t)
	u, err := s.Receive(bytes.NewReader(mkZip(t, ent{"Sonic.md", "r"})), "sonic.zip")
	if err != nil {
		t.Fatal(err)
	}
	f := find(u, "Sonic.md")
	if f == nil || len(u.Files) != 1 || f.Discarded || len(f.Candidates) != 1 || f.Candidates[0] != "megadrive" {
		t.Fatalf("%+v", u.Files)
	}
}

func TestReceiveZipCueBinExtracts(t *testing.T) {
	s, _ := newStager(t)
	u, err := s.Receive(bytes.NewReader(mkZip(t, ent{"Game.cue", "FILE \"Game.bin\" BINARY\n"}, ent{"Game.bin", "b"})), "g.zip")
	if err != nil {
		t.Fatal(err)
	}
	if b := find(u, "Game.bin"); b == nil || b.CompanionOf != "Game.cue" || b.Discarded {
		t.Fatalf("%+v", u.Files)
	}
}

func TestGamelistXMLNeverRomOrCompanion(t *testing.T) {
	s, _ := newStager(t)
	cue := "FILE \"GameList.XML\" BINARY\n  TRACK 01 MODE2/2352\n"
	u, err := s.Receive(bytes.NewReader(mkZip(t, ent{"g.cue", cue}, ent{"GameList.XML", "<gameList/>"})), "g.zip")
	if err != nil {
		t.Fatal(err)
	}
	if f := find(u, "GameList.XML"); f == nil || !f.Discarded || f.CompanionOf != "" || len(f.Candidates) != 0 {
		t.Fatalf("%+v", f)
	}
	u, err = s.Receive(strings.NewReader("<gameList/>"), "gamelist.xml")
	if err != nil {
		t.Fatal(err)
	}
	if f := u.Files[0]; !f.Discarded || len(f.Candidates) != 0 {
		t.Fatalf("%+v", f)
	}
}
