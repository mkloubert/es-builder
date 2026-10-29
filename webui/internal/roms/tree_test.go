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
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"emulationstation/webui/internal/systems"
)

// mkTree writes files (slash paths) below a new temp dir and returns it.
func mkTree(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func fileByName(t *testing.T, up *Upload, name string) StagedFile {
	t.Helper()
	for _, f := range up.Files {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no staged file %q in %+v", name, up.Files)
	return StagedFile{}
}

func TestStageTreeHintMatchGivesOneCandidate(t *testing.T) {
	s, c := newStager(t)
	src := mkTree(t, map[string][]byte{"Mario.zip": mkZip(t, ent{"rom.bin", "x"})})
	up, err := s.StageTree(context.Background(), src, "snes", []string{"Mario.zip"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := fileByName(t, up, "Mario.zip")
	if !reflect.DeepEqual(f.Candidates, []string{"snes"}) || f.Note != "" || f.Source != "Mario.zip" {
		t.Fatalf("%+v", f)
	}
	if _, err := os.Stat(filepath.Join(c.Paths.Staging, up.ID, "Mario.zip")); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Get(up.ID); !ok || len(got.Files) != 1 {
		t.Fatal("upload not registered")
	}
}

func TestStageTreeHintMismatchAddsNote(t *testing.T) {
	s, _ := newStager(t)
	src := mkTree(t, map[string][]byte{"Sonic.md": []byte("x")})
	up, err := s.StageTree(context.Background(), src, "snes", []string{"Sonic.md"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := fileByName(t, up, "Sonic.md")
	if !reflect.DeepEqual(f.Candidates, []string{"megadrive"}) || f.Note != "folder snes does not accept .md" {
		t.Fatalf("%+v", f)
	}
}

func TestStageTreeUnknownHintIsIgnored(t *testing.T) {
	s, _ := newStager(t)
	src := mkTree(t, map[string][]byte{"Mario.zip": mkZip(t, ent{"rom.bin", "x"})})
	up, err := s.StageTree(context.Background(), src, "games", []string{"Mario.zip"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := fileByName(t, up, "Mario.zip")
	if len(f.Candidates) < 2 || f.Note != "" {
		t.Fatalf("%+v", f)
	}
}

func TestStageTreeDeepCueBinCompanion(t *testing.T) {
	s, c := newStager(t)
	src := mkTree(t, map[string][]byte{
		"Game/Game.cue": []byte(`FILE "Game.bin" BINARY` + "\n"),
		"Game/Game.bin": []byte("data"),
	})
	up, err := s.StageTree(context.Background(), src, "psx", []string{"Game/Game.bin", "Game/Game.cue"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cue := fileByName(t, up, "Game/Game.cue")
	bin := fileByName(t, up, "Game/Game.bin")
	if !reflect.DeepEqual(cue.Candidates, []string{"psx"}) || cue.CompanionOf != "" || cue.Discarded || cue.SuggestedName != "Game" {
		t.Fatalf("cue %+v", cue)
	}
	if bin.CompanionOf != "Game/Game.cue" || bin.Discarded {
		t.Fatalf("bin %+v", bin)
	}
	if _, err := os.Stat(filepath.Join(c.Paths.Staging, up.ID, "Game", "Game.bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(up.ID, []Choice{{Name: "Game/Game.cue", System: "psx"}}, false); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"Game.cue", "Game.bin"} {
		if _, err := os.Stat(filepath.Join(c.Paths.Roms, "psx", "Game", n)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStageTreeZipWithRomIsUnpacked(t *testing.T) {
	s, c := newStager(t)
	src := mkTree(t, map[string][]byte{
		"Pack/Mario.zip": mkZip(t, ent{"Mario.smc", "rom"}, ent{"readme.txt", "hi"}),
	})
	up, err := s.StageTree(context.Background(), src, "snes", []string{"Pack/Mario.zip"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rom := fileByName(t, up, "Pack/Mario.smc")
	if !reflect.DeepEqual(rom.Candidates, []string{"snes"}) || rom.Discarded || rom.Source != "Pack/Mario.zip" {
		t.Fatalf("rom %+v", rom)
	}
	txt := fileByName(t, up, "Pack/readme.txt")
	if !txt.Discarded || txt.Reason != "unknown file type" {
		t.Fatalf("txt %+v", txt)
	}
	stage := filepath.Join(c.Paths.Staging, up.ID)
	if _, err := os.Stat(filepath.Join(stage, "Pack", "Mario.smc")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stage, "Pack", "Mario.zip")); !os.IsNotExist(err) {
		t.Fatal("zip should not stay in staging", err)
	}
	if _, err := os.Stat(stage + ".x"); !os.IsNotExist(err) {
		t.Fatal("scratch dir left", err)
	}
}

func TestStageTreeArcadeZipStays(t *testing.T) {
	s, c := newStager(t)
	src := mkTree(t, map[string][]byte{
		"sf2.zip":   mkZip(t, ent{"sf2.smc", "x"}),
		"mslug.zip": mkZip(t, ent{"201-p1.bin", "x"}),
	})
	up, err := s.StageTree(context.Background(), src, "arcade", []string{"mslug.zip", "sf2.zip"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"sf2.zip", "mslug.zip"} {
		f := fileByName(t, up, n)
		if !reflect.DeepEqual(f.Candidates, []string{"arcade"}) || f.Discarded {
			t.Fatalf("%+v", f)
		}
		if _, err := os.Stat(filepath.Join(c.Paths.Staging, up.ID, n)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStageTreeZipNameClashIsDuplicate(t *testing.T) {
	s, _ := newStager(t)
	src := mkTree(t, map[string][]byte{
		"A.zip":     mkZip(t, ent{"Mario.smc", "from zip"}),
		"Mario.smc": []byte("plain"),
	})
	up, err := s.StageTree(context.Background(), src, "snes", []string{"A.zip", "Mario.smc"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var kept, dups int
	for _, f := range up.Files {
		if f.Name != "Mario.smc" {
			t.Fatalf("unexpected %+v", f)
		}
		if f.Discarded && f.Reason == "duplicate file name" {
			dups++
		} else if !f.Discarded {
			kept++
		}
	}
	if kept != 1 || dups != 1 {
		t.Fatalf("%+v", up.Files)
	}
}

func TestStageTreeAlreadyImported(t *testing.T) {
	s, c := newStager(t)
	src := mkTree(t, map[string][]byte{
		"Same.smc":      []byte("1234"),
		"Other.smc":     []byte("1234"),
		"Disc/Game.cue": []byte(`FILE "Game.bin" BINARY` + "\n"),
		"Disc/Game.bin": []byte("data"),
	})
	existing := map[string]string{
		"snes/Same.smc":     "abcd",
		"snes/Other.smc":    "abc",
		"psx/Disc/Game.cue": `FILE "Game.bin" BINARY` + "\n",
		"psx/Disc/Game.bin": "dat2",
	}
	for rel, body := range existing {
		p := filepath.Join(c.Paths.Roms, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	up, err := s.StageTree(context.Background(), src, "", []string{"Disc/Game.bin", "Disc/Game.cue", "Other.smc", "Same.smc"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByName(t, up, "Same.smc"); !f.Discarded || f.Reason != ReasonImported {
		t.Fatalf("same %+v", f)
	}
	if f := fileByName(t, up, "Other.smc"); f.Discarded {
		t.Fatalf("other %+v", f)
	}
	if f := fileByName(t, up, "Disc/Game.cue"); !f.Discarded || f.Reason != ReasonImported {
		t.Fatalf("cue %+v", f)
	}
	if f := fileByName(t, up, "Disc/Game.bin"); !f.Discarded || f.Reason != ReasonImported {
		t.Fatalf("bin %+v", f)
	} // files that are already imported are not copied
	for _, n := range []string{"Same.smc", "Disc/Game.cue", "Disc/Game.bin"} {
		if _, err := os.Stat(filepath.Join(c.Paths.Staging, up.ID, filepath.FromSlash(n))); !os.IsNotExist(err) {
			t.Errorf("%s was copied: %v", n, err)
		}
	}
}

func TestStageTreeImportedCueWithChangedBinCopiesBin(t *testing.T) {
	s, c := newStager(t)
	cue := `FILE "Game.bin" BINARY` + "\n"
	src := mkTree(t, map[string][]byte{"Game.cue": []byte(cue), "Game.bin": []byte("new data")})
	os.MkdirAll(filepath.Join(c.Paths.Roms, "psx"), 0o755)
	os.WriteFile(filepath.Join(c.Paths.Roms, "psx", "Game.cue"), []byte(cue), 0o644)
	os.WriteFile(filepath.Join(c.Paths.Roms, "psx", "Game.bin"), []byte("old"), 0o644)
	up, err := s.StageTree(context.Background(), src, "psx", []string{"Game.bin", "Game.cue"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByName(t, up, "Game.cue"); !f.Discarded || f.Reason != ReasonImported {
		t.Fatalf("cue %+v", f)
	}
	if f := fileByName(t, up, "Game.bin"); f.Discarded {
		t.Fatalf("bin %+v", f)
	}
}

func TestStageTreeNewCueKeepsImportedBin(t *testing.T) {
	c := testCfg(t)
	sys := append(testSystems(), systems.System{Name: "psx2", Extensions: []string{".bin"}})
	s := NewStager(c, sys)
	src := mkTree(t, map[string][]byte{"Game.cue": []byte(`FILE "Game.bin" BINARY` + "\n"), "Game.bin": []byte("data")})
	os.MkdirAll(filepath.Join(c.Paths.Roms, "psx2"), 0o755)
	os.WriteFile(filepath.Join(c.Paths.Roms, "psx2", "Game.bin"), []byte("data"), 0o644)
	up, err := s.StageTree(context.Background(), src, "", []string{"Game.bin", "Game.cue"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByName(t, up, "Game.cue"); f.Discarded {
		t.Fatalf("cue %+v", f)
	}
	if f := fileByName(t, up, "Game.bin"); f.Discarded || f.CompanionOf != "Game.cue" {
		t.Fatalf("bin %+v", f)
	}
}

func TestStageTreeCorruptZipIsSkipped(t *testing.T) {
	s, c := newStager(t)
	z := mkZip(t, ent{"Mario.smc", strings.Repeat("compressible data ", 5000)})
	// flip bytes in the deflate data of the first entry
	for i := 60; i < 200; i++ {
		z[i] ^= 0xff
	}
	src := mkTree(t, map[string][]byte{"Bad.zip": z, "Good.smc": []byte("x")})
	if p := s.PlanTree(src, "snes", []string{"Bad.zip"}); !p[0].Unpack {
		t.Fatalf("zip directory should be readable, so the error comes from the data: %+v", p)
	}
	up, err := s.StageTree(context.Background(), src, "snes", []string{"Bad.zip", "Good.smc"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByName(t, up, "Bad.zip"); !f.Discarded || f.Reason != "damaged archive" {
		t.Fatalf("zip %+v", f)
	}
	if f := fileByName(t, up, "Good.smc"); f.Discarded {
		t.Fatalf("good %+v", f)
	}
	for _, n := range []string{"Mario.smc", "Bad.zip"} {
		if _, err := os.Stat(filepath.Join(c.Paths.Staging, up.ID, n)); !os.IsNotExist(err) {
			t.Errorf("%s left in staging: %v", n, err)
		}
	}
}

func TestStageTreeZipWithBrokenDirectoryIsSkipped(t *testing.T) {
	s, _ := newStager(t)
	src := mkTree(t, map[string][]byte{"Bad.zip": []byte("not a zip at all")})
	up, err := s.StageTree(context.Background(), src, "snes", []string{"Bad.zip"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByName(t, up, "Bad.zip"); !f.Discarded || f.Reason != "damaged archive" {
		t.Fatalf("zip %+v", f)
	}
}

func TestPlanTree(t *testing.T) {
	s, _ := newStager(t)
	src := mkTree(t, map[string][]byte{
		"a.zip": mkZip(t, ent{"a.smc", strings.Repeat("x", 1000)}),
		"b.zip": mkZip(t, ent{"b.bin", "y"}),
	})
	plans := s.PlanTree(src, "snes", []string{"a.zip", "b.zip"})
	if !plans[0].Unpack || plans[0].Unpacked != 1000 || plans[1].Unpack || plans[1].Size == 0 {
		t.Fatalf("%+v", plans)
	}
	plans = s.PlanTree(src, "arcade", []string{"a.zip"})
	if plans[0].Unpack {
		t.Fatalf("arcade zip unpacked: %+v", plans)
	}
}

func TestStageTreeProgressCountsBytes(t *testing.T) {
	s, _ := newStager(t)
	src := mkTree(t, map[string][]byte{"a.smc": make([]byte, 100000), "b/c.smc": make([]byte, 5)})
	var total int64
	_, err := s.StageTree(context.Background(), src, "snes", []string{"a.smc", "b/c.smc"}, func(n int64) { total += n })
	if err != nil {
		t.Fatal(err)
	}
	if total != 100005 {
		t.Fatalf("progress total %d", total)
	}
}

func TestStageTreeCancelRemovesUpload(t *testing.T) {
	s, c := newStager(t)
	src := mkTree(t, map[string][]byte{"a.smc": make([]byte, 1<<20), "b.smc": []byte("x")})
	ctx, cancel := context.WithCancel(context.Background())
	_, err := s.StageTree(ctx, src, "snes", []string{"a.smc", "b.smc"}, func(int64) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if !isEmptyDir(t, c.Paths.Staging) {
		t.Fatal("staging not empty")
	}
}

type eioSource struct{}

func (eioSource) Read([]byte) (int, error) { return 0, syscall.EIO }
func (eioSource) Close() error             { return nil }

func TestStageTreeReadErrorRemovesUpload(t *testing.T) {
	s, c := newStager(t)
	src := mkTree(t, map[string][]byte{"a.smc": []byte("x"), "b.smc": []byte("y")})
	s.SetTestHooks(TestHooks{Open: func(p string) (io.ReadCloser, error) {
		if filepath.Base(p) == "b.smc" {
			return eioSource{}, nil
		}
		return os.Open(p)
	}})
	_, err := s.StageTree(context.Background(), src, "snes", []string{"a.smc", "b.smc"}, nil)
	var re *ReadError
	if !errors.As(err, &re) || re.Rel != "b.smc" || !errors.Is(err, syscall.EIO) {
		t.Fatalf("err = %v", err)
	}
	if !isEmptyDir(t, c.Paths.Staging) {
		t.Fatal("staging not empty")
	}
}

func TestStageTreeRejectsUnsafeRel(t *testing.T) {
	s, c := newStager(t)
	src := mkTree(t, map[string][]byte{"a.smc": []byte("x")})
	for _, rel := range []string{"../a.smc", "/a.smc", "a/../a.smc", "", "a\\b.smc"} {
		if _, err := s.StageTree(context.Background(), src, "snes", []string{rel}, nil); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%q: err = %v", rel, err)
		}
	}
	if !isEmptyDir(t, c.Paths.Staging) {
		t.Fatal("staging not empty")
	}
}

func TestStageTreeSymlinkSourceFails(t *testing.T) {
	s, c := newStager(t)
	src := mkTree(t, map[string][]byte{"real.smc": []byte("x")})
	os.Symlink(filepath.Join(src, "real.smc"), filepath.Join(src, "link.smc"))
	_, err := s.StageTree(context.Background(), src, "snes", []string{"link.smc"}, nil)
	var re *ReadError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v", err)
	}
	if !isEmptyDir(t, c.Paths.Staging) {
		t.Fatal("staging not empty")
	}
}

// putRoms writes files (slash paths below the ROM folder).
func putRoms(t *testing.T, roms string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(roms, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlanTreeUnpackedZipAlreadyImported(t *testing.T) {
	s, c := newStager(t)
	src := mkTree(t, map[string][]byte{
		"Pack/Mario.zip": mkZip(t, ent{"Mario.smc", "rom"}, ent{"readme.txt", "hi"}),
		"Zelda.zip":      mkZip(t, ent{"Zelda.smc", "zelda"}),
		"Metroid.zip":    mkZip(t, ent{"Metroid.smc", "metroid"}),
	})
	putRoms(t, c.Paths.Roms, map[string]string{
		"snes/Pack/Mario.smc": "rom",
		"snes/Zelda.smc":      "zelda",
		"snes/Metroid.smc":    "other size",
	})
	rels := []string{"Metroid.zip", "Pack/Mario.zip", "Zelda.zip"}
	plans := s.PlanTree(src, "snes", rels)
	if plans[0].Imported || plans[0].Reason != "" || !plans[0].Unpack {
		t.Fatalf("metroid %+v", plans[0])
	}
	for _, p := range plans[1:] {
		if !p.Imported || p.Reason != ReasonImported {
			t.Fatalf("%+v", p)
		}
	}
	opened := []string{}
	s.SetTestHooks(TestHooks{Open: func(p string) (io.ReadCloser, error) {
		opened = append(opened, filepath.Base(p))
		return os.Open(p)
	}})
	up, err := s.StageTree(context.Background(), src, "snes", rels, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opened, []string{"Metroid.zip"}) {
		t.Fatalf("copied %v", opened)
	}
	for _, n := range []string{"Pack/Mario.zip", "Zelda.zip"} {
		if f := fileByName(t, up, n); !f.Discarded || f.Reason != ReasonImported {
			t.Fatalf("%s %+v", n, f)
		}
	}
}

func TestPlanTreeZipWithDiscNeedsCompanions(t *testing.T) {
	s, c := newStager(t)
	src := mkTree(t, map[string][]byte{
		"Game.zip": mkZip(t, ent{"Game.cue", `FILE "Game.raw" BINARY`}, ent{"Game.raw", "data"}),
	})
	putRoms(t, c.Paths.Roms, map[string]string{"psx/Game.cue": `FILE "Game.raw" BINARY`})
	if p := s.PlanTree(src, "psx", []string{"Game.zip"})[0]; p.Imported || p.Reason != "" {
		t.Fatalf("companion missing, but %+v", p)
	}
	putRoms(t, c.Paths.Roms, map[string]string{"psx/Game.raw": "data"})
	if p := s.PlanTree(src, "psx", []string{"Game.zip"})[0]; !p.Imported {
		t.Fatalf("%+v", p)
	}
}

func TestPlanTreeZipOverLimitsIsSkipped(t *testing.T) {
	s, c := newStager(t)
	c.MaxExtractBytes = 100
	c.MaxZipEntries = 2
	src := mkTree(t, map[string][]byte{
		"Big.zip":  mkZip(t, ent{"Big.smc", strings.Repeat("x", 1000)}),
		"Many.zip": mkZip(t, ent{"a.smc", "a"}, ent{"b.smc", "b"}, ent{"c.smc", "c"}),
		"Ok.zip":   mkZip(t, ent{"Ok.smc", "ok"}),
	})
	rels := []string{"Big.zip", "Many.zip", "Ok.zip"}
	plans := s.PlanTree(src, "snes", rels)
	if plans[0].Reason != "archive is too large when unpacked" || plans[1].Reason != "too many files in archive" || plans[2].Reason != "" {
		t.Fatalf("%+v", plans)
	}
	opened := []string{}
	s.SetTestHooks(TestHooks{Open: func(p string) (io.ReadCloser, error) {
		opened = append(opened, filepath.Base(p))
		return os.Open(p)
	}})
	up, err := s.StageTree(context.Background(), src, "snes", rels, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opened, []string{"Ok.zip"}) {
		t.Fatalf("copied %v", opened)
	}
	if f := fileByName(t, up, "Big.zip"); !f.Discarded || f.Reason != "archive is too large when unpacked" {
		t.Fatalf("%+v", f)
	}
	if f := fileByName(t, up, "Ok.smc"); f.Discarded {
		t.Fatalf("%+v", f)
	}
}

func TestStageTreeScriptNeedsDecision(t *testing.T) {
	s, _ := newStager(t)
	src := mkTree(t, map[string][]byte{
		"x.sh":    []byte("#!/bin/sh\n"),
		"Run.SH":  []byte("#!/bin/sh\n"),
		"Game.sh": []byte("#!/bin/sh\n"),
	})
	up, err := s.StageTree(context.Background(), src, "ports", []string{"Game.sh", "Run.SH"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"Game.sh", "Run.SH"} {
		f := fileByName(t, up, n)
		if f.Discarded || !reflect.DeepEqual(f.Candidates, []string{"ports"}) || f.Note != NoteScript {
			t.Fatalf("%+v", f)
		}
	}
	up, err = s.StageTree(context.Background(), src, "snes", []string{"x.sh"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByName(t, up, "x.sh"); f.Note != "folder snes does not accept .sh. "+NoteScript {
		t.Fatalf("%+v", f)
	}
}

func TestStageTreeProgressIsThrottled(t *testing.T) {
	s, _ := newStager(t)
	src := mkTree(t, map[string][]byte{"a.smc": make([]byte, 3<<20), "b.smc": make([]byte, 10)})
	var total int64
	calls := 0
	_, err := s.StageTree(context.Background(), src, "snes", []string{"a.smc", "b.smc"}, func(n int64) { total += n; calls++ })
	if err != nil {
		t.Fatal(err)
	}
	// 3 MiB in 32 KiB reads: about one call per MiB, plus the rest at the end
	if total != 3<<20+10 || calls > 5 {
		t.Fatalf("total %d, %d calls", total, calls)
	}
}

func TestStageTreeProgressCountsFilesThatAreNotCopied(t *testing.T) {
	s, c := newStager(t)
	src := mkTree(t, map[string][]byte{"a.smc": make([]byte, 2<<20), "b.smc": make([]byte, 2<<20)})
	putRoms(t, c.Paths.Roms, map[string]string{"snes/a.smc": string(make([]byte, 2<<20)), "snes/b.smc": string(make([]byte, 2<<20))})
	var steps []int64
	up, err := s.StageTree(context.Background(), src, "snes", []string{"a.smc", "b.smc"}, func(n int64) { steps = append(steps, n) })
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByName(t, up, "a.smc"); f.Reason != ReasonImported {
		t.Fatalf("%+v", f)
	}
	// nothing is copied, but each file moves the progress
	if !reflect.DeepEqual(steps, []int64{2 << 20, 2 << 20}) {
		t.Fatalf("steps %v", steps)
	}
}

func TestStageTreeZipThatCannotBeReadIsReadError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can read every file")
	}
	s, c := newStager(t)
	src := mkTree(t, map[string][]byte{
		"Bad.zip":  mkZip(t, ent{"Bad.smc", "x"}),
		"Good.smc": []byte("y"),
	})
	if err := os.Chmod(filepath.Join(src, "Bad.zip"), 0); err != nil {
		t.Fatal(err)
	}
	rels := []string{"Bad.zip", "Good.smc"}
	if p := s.PlanTree(src, "snes", rels)[0]; p.Reason != ReasonReadError || p.Damaged {
		t.Fatalf("%+v", p)
	}
	up, err := s.StageTreeWith(context.Background(), src, "snes", rels, TreeOptions{SkipReadError: func(error) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByName(t, up, "Bad.zip"); !f.Discarded || f.Reason != ReasonReadError {
		t.Fatalf("%+v", f)
	}
	if f := fileByName(t, up, "Good.smc"); f.Discarded {
		t.Fatalf("%+v", f)
	}
	// when the read error is not skipped (the stick is gone), staging fails
	_, err = s.StageTreeWith(context.Background(), src, "snes", rels, TreeOptions{SkipReadError: func(error) bool { return false }})
	var re *ReadError
	if !errors.As(err, &re) || re.Rel != "Bad.zip" || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("err = %v", err)
	}
	s.Discard(up.ID)
	if !isEmptyDir(t, c.Paths.Staging) {
		t.Fatal("staging not empty")
	}
}

func TestStageTreeReadErrorSkipsOnlyThatFile(t *testing.T) {
	s, _ := newStager(t)
	cue := `FILE "Game.bin" BINARY` + "\n"
	src := mkTree(t, map[string][]byte{
		"a.smc":           []byte("x"),
		"b.smc":           []byte("y"),
		"Disc/Game.cue":   []byte(cue),
		"Disc/Game.bin":   []byte("data"),
		"Disc/Readme.m3u": []byte("Game.cue\n"),
	})
	opened := map[string]int{}
	s.SetTestHooks(TestHooks{Open: func(p string) (io.ReadCloser, error) {
		opened[filepath.Base(p)]++
		if n := filepath.Base(p); n == "b.smc" || n == "Game.bin" {
			return eioSource{}, nil
		}
		return os.Open(p)
	}})
	rels := []string{"Disc/Game.bin", "Disc/Game.cue", "Disc/Readme.m3u", "a.smc", "b.smc"}
	up, err := s.StageTreeWith(context.Background(), src, "", rels, TreeOptions{SkipReadError: func(err error) bool { return errors.Is(err, syscall.EIO) }})
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByName(t, up, "a.smc"); f.Discarded {
		t.Fatalf("a %+v", f)
	}
	// the disc needs Game.bin: the m3u, the cue and the bin are skipped together
	for _, n := range []string{"b.smc", "Disc/Game.bin"} {
		if f := fileByName(t, up, n); !f.Discarded || f.Reason != ReasonReadError {
			t.Fatalf("%s %+v", n, f)
		}
	}
	for _, n := range []string{"Disc/Game.cue", "Disc/Readme.m3u"} {
		if f := fileByName(t, up, n); !f.Discarded || f.Reason != "read error in Disc/Game.bin" {
			t.Fatalf("%s %+v", n, f)
		}
	}
	for n, k := range opened {
		if k != 1 {
			t.Errorf("%s opened %d times", n, k)
		}
	}
}

// failingCreate makes every new file below a path with part fail with err.
func failingCreate(part string, err error) func(string) (io.WriteCloser, error) {
	return func(p string) (io.WriteCloser, error) {
		if strings.Contains(p, part) {
			return nil, &os.PathError{Op: "open", Path: p, Err: err}
		}
		return createFile(p)
	}
}

func TestStageTreeWriteErrorWhileUnpackingStops(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{syscall.EROFS, "Could not write to the SD card: "},
		{syscall.EIO, "Could not write to the SD card: "},
		{syscall.EACCES, "Could not write to the SD card: "},
		{syscall.ENOSPC, "no space left on device"},
	} {
		s, c := newStager(t)
		src := mkTree(t, map[string][]byte{"Pack.zip": mkZip(t, ent{"Mario.smc", "rom"}), "Good.smc": []byte("y")})
		s.SetTestHooks(TestHooks{Create: failingCreate(".x"+string(filepath.Separator), tc.err)})
		_, err := s.StageTree(context.Background(), src, "snes", []string{"Good.smc", "Pack.zip"}, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !errors.Is(err, tc.err) {
			t.Fatalf("%v: err = %v", tc.err, err)
		}
		if tc.err == syscall.ENOSPC && strings.Contains(err.Error(), "Could not write") {
			t.Fatalf("a full card keeps its own error: %v", err)
		}
		if !isEmptyDir(t, c.Paths.Staging) {
			t.Fatal("staging not empty")
		}
	}
}

func TestStageTreeWithPlansReadsZipDirectoryOnce(t *testing.T) {
	s, _ := newStager(t)
	src := mkTree(t, map[string][]byte{"Pack.zip": mkZip(t, ent{"Mario.smc", "rom"})})
	reads := 0
	old := openZip
	openZip = func(name string) (*zip.Reader, io.Closer, error) {
		if !strings.HasSuffix(name, ".part") {
			reads++
		}
		return old(name)
	}
	t.Cleanup(func() { openZip = old })
	rels := []string{"Pack.zip"}
	plans := s.PlanTree(src, "snes", rels)
	up, err := s.StageTreeWith(context.Background(), src, "snes", rels, TreeOptions{Plans: plans})
	if err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("ZIP directory read %d times on the stick", reads)
	}
	if f := fileByName(t, up, "Mario.smc"); f.Discarded {
		t.Fatalf("%+v", f)
	}
	// plans for other rels are not used
	if _, err := s.StageTreeWith(context.Background(), src, "snes", rels, TreeOptions{Plans: plans[:0]}); err != nil || reads != 2 {
		t.Fatalf("reads %d, err %v", reads, err)
	}
}

func TestPlanTreeImportedInOneSystemOnly(t *testing.T) {
	c := testCfg(t)
	sys := []systems.System{
		{Name: "psx", Extensions: []string{".cue", ".bin"}},
		{Name: "psx2", Extensions: []string{".cue", ".bin"}},
	}
	s := NewStager(c, sys)
	cue := `FILE "Game.bin" BINARY` + "\n"
	src := mkTree(t, map[string][]byte{"Game.cue": []byte(cue), "Game.bin": []byte("new data")})
	// Game.cue is imported in psx; psx/Game.bin is older; psx2 has another
	// Game.bin with the size of the new one
	putRoms(t, c.Paths.Roms, map[string]string{
		"psx/Game.cue":  cue,
		"psx/Game.bin":  "old",
		"psx2/Game.bin": "abcdefgh",
	})
	rels := []string{"Game.bin", "Game.cue"}
	for _, hint := range []string{"", "psx"} {
		plans := s.PlanTree(src, hint, rels)
		if plans[0].Imported || plans[0].Reason != "" {
			t.Fatalf("hint %q: changed bin dropped: %+v", hint, plans[0])
		}
		up, err := s.StageTree(context.Background(), src, hint, rels, nil)
		if err != nil {
			t.Fatal(err)
		}
		if f := fileByName(t, up, "Game.bin"); f.Discarded {
			t.Fatalf("hint %q: %+v", hint, f)
		}
	}
	// a file that only one system has with the same size is imported
	putRoms(t, c.Paths.Roms, map[string]string{"psx/Game.bin": "new data"})
	os.Remove(filepath.Join(c.Paths.Roms, "psx2", "Game.bin"))
	if p := s.PlanTree(src, "", rels); !p[0].Imported || !p[1].Imported {
		t.Fatalf("%+v", p)
	}
	// with the same file in two systems and no hint, it is not counted
	putRoms(t, c.Paths.Roms, map[string]string{"psx2/Game.bin": "new data", "psx2/Game.cue": cue})
	if p := s.PlanTree(src, "", rels); p[0].Imported || p[1].Imported {
		t.Fatalf("%+v", p)
	}
	// with the hint, only the hint system counts
	if p := s.PlanTree(src, "psx2", rels); !p[0].Imported || !p[1].Imported {
		t.Fatalf("%+v", p)
	}
}

func TestStageTreeZipWithTooLongNameIsSkipped(t *testing.T) {
	s, c := newStager(t)
	long := strings.Repeat("ゲ", 100) + ".smc" // 304 bytes, more than ext4 stores
	src := mkTree(t, map[string][]byte{
		"Long.zip": mkZip(t, ent{long, "rom"}),
		"Good.smc": []byte("y"),
	})
	rels := []string{"Good.smc", "Long.zip"}
	if p := s.PlanTree(src, "snes", rels)[1]; p.Reason != "file name too long in archive" {
		t.Fatalf("%+v", p)
	}
	up, err := s.StageTree(context.Background(), src, "snes", rels, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByName(t, up, "Long.zip"); !f.Discarded || f.Reason != "file name too long in archive" {
		t.Fatalf("%+v", f)
	}
	if f := fileByName(t, up, "Good.smc"); f.Discarded {
		t.Fatalf("%+v", f)
	}
	// a name that the file system refuses while unpacking skips the ZIP too
	for _, errno := range []error{syscall.ENAMETOOLONG, syscall.EINVAL, syscall.EILSEQ} {
		s2, _ := newStager(t)
		src2 := mkTree(t, map[string][]byte{"Pack.zip": mkZip(t, ent{"Mario.smc", "rom"}), "Good.smc": []byte("y")})
		s2.SetTestHooks(TestHooks{Create: failingCreate(".x"+string(filepath.Separator), errno)})
		up, err := s2.StageTree(context.Background(), src2, "snes", []string{"Good.smc", "Pack.zip"}, nil)
		if err != nil {
			t.Fatalf("%v: %v", errno, err)
		}
		if f := fileByName(t, up, "Pack.zip"); !f.Discarded || f.Reason != "file name too long in archive" {
			t.Fatalf("%v: %+v", errno, f)
		}
	}
	_ = c
}

func TestStageTreeZipWithLongFolderNameUnpacks(t *testing.T) {
	s, _ := newStager(t)
	// unpacking keeps only the base name, so a long folder in the ZIP is fine
	src := mkTree(t, map[string][]byte{"Pack.zip": mkZip(t, ent{strings.Repeat("ゲ", 100) + "/Mario.smc", "rom"})})
	if p := s.PlanTree(src, "snes", []string{"Pack.zip"})[0]; p.Reason != "" || !p.Unpack {
		t.Fatalf("%+v", p)
	}
	up, err := s.StageTree(context.Background(), src, "snes", []string{"Pack.zip"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByName(t, up, "Mario.smc"); f.Discarded {
		t.Fatalf("%+v", f)
	}
}

func TestStageTreePlainFileWithTooLongNameIsSkipped(t *testing.T) {
	s, _ := newStager(t)
	// Linux cannot create a name over 255 bytes, so this one is only planned
	long := strings.Repeat("ゲ", 100) + ".smc"
	src := mkTree(t, map[string][]byte{"Good.smc": []byte("y"), "Odd.smc": []byte("z")})
	rels := []string{"Good.smc", "Long/" + long}
	if p := s.PlanTree(src, "snes", rels)[1]; p.Reason != "file name too long" {
		t.Fatalf("%+v", p)
	}
	up, err := s.StageTree(context.Background(), src, "snes", rels, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByName(t, up, "Long/"+long); !f.Discarded || f.Reason != "file name too long" {
		t.Fatalf("%+v", f)
	}
	// the file system refuses a name while copying: only that file is skipped
	for _, errno := range []error{syscall.ENAMETOOLONG, syscall.EILSEQ} {
		s2, c2 := newStager(t)
		s2.SetTestHooks(TestHooks{Create: failingCreate("Odd.smc", errno)})
		up, err := s2.StageTree(context.Background(), src, "snes", []string{"Good.smc", "Odd.smc"}, nil)
		if err != nil {
			t.Fatalf("%v: %v", errno, err)
		}
		if f := fileByName(t, up, "Odd.smc"); !f.Discarded || f.Reason != "file name too long" {
			t.Fatalf("%v: %+v", errno, f)
		}
		if f := fileByName(t, up, "Good.smc"); f.Discarded {
			t.Fatalf("%v: %+v", errno, f)
		}
		_ = c2
	}
}

func TestStageTreeDiscWithTrackNameTooLongIsSkipped(t *testing.T) {
	s, c := newStager(t)
	src := mkTree(t, map[string][]byte{
		"Game/Game.cue":   []byte("FILE \"Track1.bin\" BINARY\nFILE \"Track2.bin\" BINARY\n"),
		"Game/Track1.bin": []byte("1"),
		"Game/Track2.bin": []byte("2"),
		"Other.cue":       []byte("x"),
	})
	s.SetTestHooks(TestHooks{Create: failingCreate("Track2.bin", syscall.ENAMETOOLONG)})
	up, err := s.StageTree(context.Background(), src, "psx", []string{"Game/Game.cue", "Game/Track1.bin", "Game/Track2.bin", "Other.cue"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByName(t, up, "Game/Track2.bin"); !f.Discarded || f.Reason != "file name too long" {
		t.Fatalf("%+v", f)
	}
	for _, n := range []string{"Game/Game.cue", "Game/Track1.bin"} {
		if f := fileByName(t, up, n); !f.Discarded || f.Reason != "file name too long in Game/Track2.bin" {
			t.Fatalf("%s %+v", n, f)
		}
	}
	if f := fileByName(t, up, "Other.cue"); f.Discarded {
		t.Fatalf("%+v", f)
	}
	// a folder that cannot be created is not left behind
	s2, c2 := newStager(t)
	src2 := mkTree(t, map[string][]byte{"Deep/Dir/a.smc": []byte("x"), "b.smc": []byte("y")})
	s2.SetTestHooks(TestHooks{Create: failingCreate("a.smc", syscall.ENAMETOOLONG)})
	up2, err := s2.StageTree(context.Background(), src2, "snes", []string{"Deep/Dir/a.smc", "b.smc"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c2.Paths.Staging, up2.ID, "Deep")); !os.IsNotExist(err) {
		t.Fatalf("empty folder left: %v", err)
	}
	_ = c
}
