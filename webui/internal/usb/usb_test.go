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

package usb

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"emulationstation/webui/internal/config"
	"emulationstation/webui/internal/roms"
	"emulationstation/webui/internal/systems"
)

const esSystemsXML = `<systemList>
<system><name>snes</name><extension>.smc .sfc .zip</extension></system>
<system><name>nes</name><extension>.nes .zip</extension></system>
<system><name>psx</name><extension>.cue .bin .chd .m3u</extension></system>
<system><name>arcade</name><extension>.zip</extension></system>
</systemList>`

func loadSystems(t *testing.T) []systems.System {
	t.Helper()
	p := filepath.Join(t.TempDir(), "es_systems.cfg")
	if err := os.WriteFile(p, []byte(esSystemsXML), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := systems.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func writeFiles(t *testing.T, root string, files map[string][]byte) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func mkZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f, err := w.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(f, files[n])
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type fakePriv struct {
	mu       sync.Mutex
	mount    string
	mountErr error
	calls    []string
}

func (f *fakePriv) USBMount(ctx context.Context, path string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "mount "+path)
	if f.mountErr != nil {
		return "", f.mountErr
	}
	return f.mount, nil
}

func (f *fakePriv) USBUmount(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ctx.Err() != nil {
		f.calls = append(f.calls, "umount with dead context")
		return ctx.Err()
	}
	f.calls = append(f.calls, "umount")
	return nil
}

func (f *fakePriv) umounts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == "umount" {
			n++
		}
	}
	return n
}

type logReporter struct {
	mu    sync.Mutex
	lines []string
	last  float64
	hist  []float64
}

func (r *logReporter) Progress(p float64) {
	r.mu.Lock()
	r.last = p
	r.hist = append(r.hist, p)
	r.mu.Unlock()
}
func (r *logReporter) Logf(format string, a ...any) {
	r.mu.Lock()
	r.lines = append(r.lines, fmt.Sprintf(format, a...))
	r.mu.Unlock()
}

type env struct {
	stick  string
	device string
	cfg    *config.Config
	stager *roms.Stager
	priv   *fakePriv
	im     *Importer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	home := t.TempDir()
	cfg := config.Default()
	cfg.Paths.Home = home
	cfg.Paths.Roms = filepath.Join(home, "roms")
	cfg.Paths.BIOS = filepath.Join(home, "bios")
	cfg.Paths.Staging = filepath.Join(home, "staging")
	cfg.Paths.ESConfigDir = filepath.Join(home, "es")
	sys := loadSystems(t)
	e := &env{stick: t.TempDir(), cfg: cfg, priv: &fakePriv{}}
	e.device = filepath.Join(t.TempDir(), "sda1")
	os.WriteFile(e.device, nil, 0o644)
	e.priv.mount = e.stick
	e.stager = roms.NewStager(cfg, sys)
	e.im = &Importer{
		Priv:    e.priv,
		Stager:  e.stager,
		Config:  func() *config.Config { return cfg },
		Systems: func() []systems.System { return sys },
		Now:     func() time.Time { return time.Date(2026, 10, 28, 14, 3, 0, 0, time.UTC) },
	}
	return e
}

func (e *env) run(t *testing.T) (Result, error) {
	t.Helper()
	return e.im.Run(context.Background(), &logReporter{}, e.device, "vfat")
}

func (e *env) report(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.stick, "emulationstation", "import-report.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func stagingEmpty(t *testing.T, dir string) bool {
	es, err := os.ReadDir(dir)
	return os.IsNotExist(err) || (err == nil && len(es) == 0)
}

// --- layout ---

func TestDetect(t *testing.T) {
	cases := []struct {
		name    string
		dirs    []string
		found   bool
		base    string
		bios    string
		ignored bool
	}{
		{"emulationstation only", []string{"emulationstation"}, true, "emulationstation", "emulationstation/bios", false},
		{"retropie only", []string{"retropie"}, true, "retropie", "retropie/BIOS", false},
		{"both", []string{"emulationstation", "retropie"}, true, "emulationstation", "emulationstation/bios", true},
		{"none", nil, false, "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := t.TempDir()
			for _, d := range c.dirs {
				os.MkdirAll(filepath.Join(m, d), 0o755)
			}
			l, found, err := Detect(m)
			if err != nil || found != c.found {
				t.Fatal(found, err)
			}
			if !found {
				return
			}
			want := Layout{
				Base:            filepath.Join(m, c.base),
				Roms:            filepath.Join(m, c.base, "roms"),
				BIOS:            filepath.Join(m, filepath.FromSlash(c.bios)),
				IgnoredRetropie: c.ignored,
			}
			if l != want {
				t.Fatalf("got %+v want %+v", l, want)
			}
		})
	}
}

func TestPrepareThenDetect(t *testing.T) {
	m := t.TempDir()
	if err := Prepare(m, []string{"snes", "psx", "../bad", ""}); err != nil {
		t.Fatal(err)
	}
	l, found, err := Detect(m)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	for _, d := range []string{"roms/snes", "roms/psx", "bios"} {
		if fi, err := os.Stat(filepath.Join(l.Base, filepath.FromSlash(d))); err != nil || !fi.IsDir() {
			t.Fatalf("%s: %v", d, err)
		}
	}
	if _, err := os.Stat(filepath.Join(m, "bad")); !os.IsNotExist(err) {
		t.Fatal("unsafe system name created a folder")
	}
	b, err := os.ReadFile(filepath.Join(l.Base, "README.txt"))
	if err != nil || !strings.Contains(string(b), "roms") || !strings.Contains(string(b), "Nothing is deleted") {
		t.Fatalf("README: %q %v", b, err)
	}
}

// --- scan ---

func TestScan(t *testing.T) {
	r := t.TempDir()
	writeFiles(t, r, map[string][]byte{
		"snes/Mario.smc":                  []byte("12"),
		"psx/Game/Disc 1/Game.cue":        []byte("1"),
		"psx/._Game.cue":                  []byte("x"),
		"psx/.DS_Store":                   []byte("x"),
		"Thumbs.db":                       []byte("x"),
		"snes/desktop.ini":                []byte("x"),
		"System Volume Information/a.bin": []byte("x"),
		"$RECYCLE.BIN/b.smc":              []byte("x"),
		".Trashes/c":                      []byte("x"),
		"loose.smc":                       []byte("123"),
	})
	os.Symlink("/etc/passwd", filepath.Join(r, "snes", "link.smc"))
	groups, skips, err := Scan(r, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []Group{
		{Hint: "", Dir: r, Rels: []string{"loose.smc"}, Bytes: 3},
		{Hint: "psx", Dir: filepath.Join(r, "psx"), Rels: []string{"Game/Disc 1/Game.cue"}, Bytes: 1},
		{Hint: "snes", Dir: filepath.Join(r, "snes"), Rels: []string{"Mario.smc"}, Bytes: 2},
	}
	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("groups %+v", groups)
	}
	if !reflect.DeepEqual(skips, []Skip{{Rel: "snes/link.smc", Reason: "symlink"}}) {
		t.Fatalf("skips %+v", skips)
	}
}

func TestScanTooManyFiles(t *testing.T) {
	r := t.TempDir()
	writeFiles(t, r, map[string][]byte{"a/1.smc": nil, "a/2.smc": nil, "3.smc": nil})
	if _, _, err := Scan(r, 3); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Scan(r, 2); !errors.Is(err, ErrTooManyFiles) {
		t.Fatalf("err = %v", err)
	}
}

func TestScanMissingDirIsEmpty(t *testing.T) {
	g, s, err := Scan(filepath.Join(t.TempDir(), "nope"), 10)
	if err != nil || len(g) != 0 || len(s) != 0 {
		t.Fatal(g, s, err)
	}
}

func TestNeedBytes(t *testing.T) {
	e := newEnv(t)
	r := t.TempDir()
	big := strings.Repeat("a", 200000)
	arcade := mkZip(t, map[string]string{"sf2.smc": big})
	snes1 := mkZip(t, map[string]string{"a.smc": big})
	snes2 := mkZip(t, map[string]string{"b.smc": strings.Repeat("b", 1000)})
	writeFiles(t, r, map[string][]byte{
		"arcade/sf2.zip": arcade,
		"snes/a.zip":     snes1,
		"snes/b.zip":     snes2,
		"snes/c.smc":     make([]byte, 1000),
		"nes/bad.zip":    []byte("not a zip"),
		"nes/old.nes":    []byte("1234"),
	})
	writeFiles(t, e.cfg.Paths.Roms, map[string][]byte{"nes/old.nes": []byte("abcd")})
	groups, _, err := Scan(r, 100)
	if err != nil {
		t.Fatal(err)
	}
	need := NeedBytes(e.stager, groups)
	// arcade: kept ZIP, own size only; snes: unpacked sizes + largest snes ZIP
	// as peak + plain file; nes: damaged ZIP and imported file count 0
	base := int64(len(arcade)) + 200000 + 1000 + 1000 + int64(max(len(snes1), len(snes2)))
	if need != base+base/20 {
		t.Fatalf("need %d, want %d", need, base+base/20)
	}
	if need > 2*200000 {
		t.Fatalf("need %d counts the arcade ZIP as unpacked", need)
	}
}

// --- report ---

func TestWriteReport(t *testing.T) {
	base := t.TempDir()
	os.WriteFile(filepath.Join(base, "import-report.txt"), []byte("old content that is long\n"), 0o644)
	lines := []Line{
		{Rel: "emulationstation/roms/snes/a.smc", Status: "staged"},
		{Rel: "emulationstation/roms/snes/b.smc", Status: "staged"},
		{Rel: "emulationstation/roms/psx/c.iso", Status: "needs review", Note: "folder psx does not accept .iso"},
		{Rel: "emulationstation/roms/x.txt", Status: "skipped: unknown file type"},
		{Rel: "emulationstation/bios/a.bin", Status: "bios saved"},
	}
	now := time.Date(2026, 10, 28, 14, 3, 0, 0, time.UTC)
	if err := WriteReport(base, now, lines); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(base, "import-report.txt"))
	s := string(b)
	for _, want := range []string{
		"2026-10-28 14:03",
		"staged: 2\n",
		"needs review: 1\n",
		"skipped: unknown file type: 1\n",
		"bios saved: 1\n",
		"emulationstation/roms/snes/a.smc - staged\n",
		"emulationstation/roms/psx/c.iso - needs review (folder psx does not accept .iso)\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("report misses %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "old content") {
		t.Error("report not overwritten")
	}
}

// --- importer ---

func TestRunEmptyStickPrepares(t *testing.T) {
	e := newEnv(t)
	res, err := e.run(t)
	if err != nil || !res.Prepared || len(res.UploadIDs) != 0 {
		t.Fatal(res, err)
	}
	for _, d := range []string{"roms/snes", "roms/arcade", "bios", "README.txt"} {
		if _, err := os.Stat(filepath.Join(e.stick, "emulationstation", filepath.FromSlash(d))); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(e.priv.calls, []string{"mount " + e.device, "umount"}) {
		t.Fatalf("calls %v", e.priv.calls)
	}
	if !stagingEmpty(t, e.cfg.Paths.Staging) {
		t.Fatal("staging not empty")
	}
}

func TestRunMountFailureStillUnmounts(t *testing.T) {
	e := newEnv(t)
	e.priv.mountErr = errors.New("boom")
	if _, err := e.run(t); err == nil {
		t.Fatal("want error")
	}
	if e.priv.umounts() != 1 {
		t.Fatalf("calls %v", e.priv.calls)
	}
}

type exitErr int

func (e exitErr) Error() string {
	return fmt.Sprintf("sudo: exit status %d: priv: x Usage: priv ...", int(e))
}
func (e exitErr) ExitCode() int { return int(e) }

func TestRunMountErrorMessages(t *testing.T) {
	cases := []struct {
		err    error
		fsType string
		want   string
	}{
		{exitErr(2), "vfat", msgStickGone},
		{fmt.Errorf("sudo: %w", exitErr(2)), "ntfs", msgStickGone},
		{exitErr(1), "exfat", msgNoMount},
		{exitErr(1), "ntfs", msgNoMount + " " + msgNTFSDirty},
		// the helper could not read the device list: no NTFS hint
		{exitErr(3), "ntfs", "Could not read the USB devices."},
		{errors.New("no sudo"), "vfat", msgNoMount},
	}
	for _, c := range cases {
		e := newEnv(t)
		e.priv.mountErr = c.err
		_, err := e.im.Run(context.Background(), &logReporter{}, e.device, c.fsType)
		var me *MountError
		if !errors.As(err, &me) || err.Error() != c.want || me.Err != c.err {
			t.Errorf("%v/%s: err = %v", c.err, c.fsType, err)
		}
		if strings.Contains(err.Error(), "Usage") {
			t.Errorf("usage text in the message: %q", err.Error())
		}
	}
}

func TestRunUmountUsesFreshContext(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	e.priv.mountErr = errors.New("boom")
	cancel()
	e.im.Run(ctx, &logReporter{}, e.device, "vfat")
	if e.priv.umounts() != 1 {
		t.Fatalf("calls %v", e.priv.calls)
	}
}

func commitAll(t *testing.T, st *roms.Stager, ids []string) {
	t.Helper()
	for _, id := range ids {
		up, ok := st.Get(id)
		if !ok {
			t.Fatalf("upload %s missing", id)
		}
		var ch []roms.Choice
		for _, f := range up.Files {
			if f.Discarded || f.CompanionOf != "" {
				continue
			}
			ch = append(ch, roms.Choice{Name: f.Name, System: f.Candidates[0]})
		}
		if _, err := st.Commit(id, ch, false); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunSameNameInTwoSystemsAndReimport(t *testing.T) {
	e := newEnv(t)
	rom := mkZip(t, map[string]string{"rom.bin": "x"})
	writeFiles(t, e.stick, map[string][]byte{
		"emulationstation/roms/snes/Mario.zip":    rom,
		"emulationstation/roms/nes/Mario.zip":     rom,
		"emulationstation/roms/psx/Game/Game.cue": []byte(`FILE "Game.bin" BINARY` + "\n"),
		"emulationstation/roms/psx/Game/Game.bin": []byte("data"),
		"emulationstation/roms/snes/Thumbs.db":    []byte("x"),
		"emulationstation/roms/snes/readme.txt":   []byte("x"),
		"emulationstation/bios/scph1001.bin":      []byte("bios"),
	})
	res, err := e.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.UploadIDs) != 3 || res.Staged != 4 || res.Review != 0 || res.Skipped != 1 || res.BIOSSaved != 1 {
		t.Fatalf("%+v", res)
	}
	rep := e.report(t)
	for _, want := range []string{
		"emulationstation/roms/snes/Mario.zip - staged\n",
		"emulationstation/roms/nes/Mario.zip - staged\n",
		"emulationstation/roms/psx/Game/Game.bin - staged\n",
		"emulationstation/roms/snes/readme.txt - skipped: unknown file type\n",
		"emulationstation/bios/scph1001.bin - bios saved\n",
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("report misses %q:\n%s", want, rep)
		}
	}
	if strings.Contains(rep, "Thumbs.db") {
		t.Error("junk in report")
	}
	if e.priv.umounts() != 1 {
		t.Fatalf("calls %v", e.priv.calls)
	}

	commitAll(t, e.stager, res.UploadIDs)
	for _, p := range []string{"snes/Mario.zip", "nes/Mario.zip", "psx/Game/Game.cue", "psx/Game/Game.bin"} {
		if _, err := os.Stat(filepath.Join(e.cfg.Paths.Roms, filepath.FromSlash(p))); err != nil {
			t.Fatal(err)
		}
	}

	res, err = e.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.UploadIDs) != 0 || res.Staged != 0 || res.Review != 0 || res.BIOSSaved != 0 {
		t.Fatalf("second run %+v", res)
	}
	if !stagingEmpty(t, e.cfg.Paths.Staging) {
		t.Fatal("staging not empty after re-import")
	}
	for _, l := range strings.Split(e.report(t), "\n") {
		if !strings.HasPrefix(l, "emulationstation/") {
			continue
		}
		if strings.Contains(l, "readme.txt") {
			continue
		}
		if !strings.HasSuffix(l, " - skipped: already imported") && !strings.HasSuffix(l, " - bios skipped: already imported") {
			t.Errorf("line %q", l)
		}
	}
}

func TestRunHintMismatchNeedsReview(t *testing.T) {
	e := newEnv(t)
	writeFiles(t, e.stick, map[string][]byte{
		"emulationstation/roms/snes/Game.cue": []byte("x"),
		"emulationstation/roms/Loose.zip":     mkZip(t, map[string]string{"rom.bin": "x"}),
	})
	res, err := e.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if res.Review != 2 || res.Staged != 0 || len(res.UploadIDs) != 2 {
		t.Fatalf("%+v", res)
	}
	rep := e.report(t)
	if !strings.Contains(rep, "emulationstation/roms/snes/Game.cue - needs review (folder snes does not accept .cue)") {
		t.Fatalf("report:\n%s", rep)
	}
	if !strings.Contains(rep, "emulationstation/roms/Loose.zip - needs review\n") {
		t.Fatalf("report:\n%s", rep)
	}
}

func TestRunUnpackedZipReportLine(t *testing.T) {
	e := newEnv(t)
	writeFiles(t, e.stick, map[string][]byte{
		"emulationstation/roms/snes/Pack.zip": mkZip(t, map[string]string{"Mario.smc": "x"}),
	})
	if _, err := e.run(t); err != nil {
		t.Fatal(err)
	}
	if rep := e.report(t); !strings.Contains(rep, "emulationstation/roms/snes/Pack.zip > Mario.smc - staged\n") {
		t.Fatalf("report:\n%s", rep)
	}
}

func TestRunBIOS(t *testing.T) {
	e := newEnv(t)
	writeFiles(t, e.cfg.Paths.BIOS, map[string][]byte{"same.bin": []byte("1234"), "other.bin": []byte("12")})
	writeFiles(t, e.stick, map[string][]byte{
		"emulationstation/bios/same.bin":       []byte("abcd"),
		"emulationstation/bios/other.bin":      []byte("new data"),
		"emulationstation/bios/new.bin":        []byte("n"),
		"emulationstation/bios/dc/dc_boot.bin": []byte("dc"),
		"emulationstation/bios/sub/x.bin":      []byte("x"),
		"emulationstation/bios/.DS_Store":      []byte("x"),
	})
	res, err := e.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if res.BIOSSaved != 3 || len(res.UploadIDs) != 0 {
		t.Fatalf("%+v", res)
	}
	check := func(name, body string) {
		b, err := os.ReadFile(filepath.Join(e.cfg.Paths.BIOS, filepath.FromSlash(name)))
		if err != nil || string(b) != body {
			t.Errorf("%s: %q %v", name, b, err)
		}
	}
	check("same.bin", "1234")
	check("other.bin", "new data")
	check("new.bin", "n")
	check("dc/dc_boot.bin", "dc")
	rep := e.report(t)
	for _, want := range []string{
		"emulationstation/bios/same.bin - bios skipped: already imported\n",
		"emulationstation/bios/other.bin - bios overwritten\n",
		"emulationstation/bios/new.bin - bios saved\n",
		"emulationstation/bios/dc/dc_boot.bin - bios saved\n",
		"emulationstation/bios/sub - skipped: sub folder\n",
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("report misses %q:\n%s", want, rep)
		}
	}
	if strings.Contains(rep, "DS_Store") {
		t.Error("junk in report")
	}
}

func TestRunRetropieIgnored(t *testing.T) {
	e := newEnv(t)
	writeFiles(t, e.stick, map[string][]byte{
		"emulationstation/roms/snes/a.smc": []byte("x"),
		"retropie/roms/snes/b.smc":         []byte("x"),
	})
	res, err := e.run(t)
	if err != nil || res.Staged != 1 {
		t.Fatal(res, err)
	}
	if rep := e.report(t); !strings.Contains(rep, "retropie - skipped: the folder emulationstation is used") {
		t.Fatalf("report:\n%s", rep)
	}
}

func TestRunRetropieLayout(t *testing.T) {
	e := newEnv(t)
	writeFiles(t, e.stick, map[string][]byte{
		"retropie/roms/snes/b.smc": []byte("x"),
		"retropie/BIOS/scph.bin":   []byte("x"),
	})
	res, err := e.run(t)
	if err != nil || res.Staged != 1 || res.BIOSSaved != 1 {
		t.Fatal(res, err)
	}
	if _, err := os.Stat(filepath.Join(e.stick, "retropie", "import-report.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.stick, "emulationstation")); !os.IsNotExist(err) {
		t.Fatal("emulationstation folder was created")
	}
}

func TestRunTooManyFiles(t *testing.T) {
	e := newEnv(t)
	e.cfg.USBMaxFiles = 1
	writeFiles(t, e.stick, map[string][]byte{
		"emulationstation/roms/snes/a.smc": []byte("x"),
		"emulationstation/roms/snes/b.smc": []byte("x"),
	})
	_, err := e.run(t)
	if !errors.Is(err, ErrTooManyFiles) {
		t.Fatalf("err = %v", err)
	}
	if e.priv.umounts() != 1 || !stagingEmpty(t, e.cfg.Paths.Staging) {
		t.Fatalf("calls %v", e.priv.calls)
	}
}

func TestRunNotEnoughSpace(t *testing.T) {
	e := newEnv(t)
	old := freeBytes
	freeBytes = func(string) (int64, error) { return 1 << 20, nil }
	t.Cleanup(func() { freeBytes = old })
	writeFiles(t, e.stick, map[string][]byte{
		"emulationstation/roms/snes/a.smc": make([]byte, 2<<20),
	})
	_, err := e.run(t)
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("err = %v", err)
	}
	if err.Error() != "Not enough space on the SD card: need 2.1 MB, free 1.0 MB." {
		t.Fatalf("message %q", err.Error())
	}
	if e.priv.umounts() != 1 || !stagingEmpty(t, e.cfg.Paths.Staging) {
		t.Fatal("not cleaned up", e.priv.calls)
	}
}

type eioReader struct{}

func (eioReader) Read([]byte) (int, error) { return 0, syscall.EIO }
func (eioReader) Close() error             { return nil }

func TestRunStickRemoved(t *testing.T) {
	e := newEnv(t)
	writeFiles(t, e.stick, map[string][]byte{
		"emulationstation/roms/nes/a.nes":  []byte("x"),
		"emulationstation/roms/snes/b.smc": []byte("x"),
	})
	e.stager.SetTestHooks(roms.TestHooks{Open: func(p string) (io.ReadCloser, error) {
		if filepath.Base(p) == "b.smc" {
			os.Remove(e.device)
			return eioReader{}, nil
		}
		return os.Open(p)
	}})
	_, err := e.run(t)
	if !errors.Is(err, ErrStickRemoved) || err.Error() != "The USB stick was removed." {
		t.Fatalf("err = %v", err)
	}
	if !stagingEmpty(t, e.cfg.Paths.Staging) {
		t.Fatal("upload not discarded")
	}
	if e.priv.umounts() != 1 {
		t.Fatalf("calls %v", e.priv.calls)
	}
}

func TestRunReadErrorSkipsFile(t *testing.T) {
	e := newEnv(t)
	writeFiles(t, e.stick, map[string][]byte{
		"emulationstation/roms/snes/a.smc": []byte("x"),
		"emulationstation/roms/snes/b.smc": []byte("x"),
	})
	e.stager.SetTestHooks(roms.TestHooks{Open: func(p string) (io.ReadCloser, error) {
		if filepath.Base(p) == "b.smc" {
			return eioReader{}, nil
		}
		return os.Open(p)
	}})
	res, err := e.run(t)
	if err != nil || res.Staged != 1 || res.Skipped != 1 || len(res.UploadIDs) != 1 {
		t.Fatal(res, err)
	}
	if rep := e.report(t); !strings.Contains(rep, "emulationstation/roms/snes/b.smc - skipped: read error\n") {
		t.Fatalf("report:\n%s", rep)
	}
}

func TestRunCorruptZipIsSkipped(t *testing.T) {
	e := newEnv(t)
	z := mkZip(t, map[string]string{"Mario.smc": strings.Repeat("compressible data ", 5000)})
	for i := 60; i < 200; i++ {
		z[i] ^= 0xff
	}
	writeFiles(t, e.stick, map[string][]byte{
		"emulationstation/roms/snes/Bad.zip":  z,
		"emulationstation/roms/snes/Good.smc": []byte("x"),
	})
	res, err := e.run(t)
	if err != nil || res.Staged != 1 || res.Skipped != 1 || len(res.UploadIDs) != 1 {
		t.Fatal(res, err)
	}
	if rep := e.report(t); !strings.Contains(rep, "emulationstation/roms/snes/Bad.zip - skipped: damaged archive\n") {
		t.Fatalf("report:\n%s", rep)
	}
}

func TestRunReimportWithLittleSpace(t *testing.T) {
	e := newEnv(t)
	writeFiles(t, e.stick, map[string][]byte{
		"emulationstation/roms/snes/Big.smc":      make([]byte, 3<<20),
		"emulationstation/roms/snes/Pack.zip":     mkZip(t, map[string]string{"Mario.smc": strings.Repeat("m", 1<<20)}),
		"emulationstation/roms/psx/Game/Game.cue": []byte(`FILE "Game.bin" BINARY` + "\n"),
		"emulationstation/roms/psx/Game/Game.bin": make([]byte, 2<<20),
	})
	res, err := e.run(t)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, e.stager, res.UploadIDs)

	old := freeBytes
	t.Cleanup(func() { freeBytes = old })
	// the SD card is nearly full: nothing is new, so nothing is copied, also
	// not the ZIP that was unpacked the first time
	freeBytes = func(string) (int64, error) { return 1, nil }
	var opened []string
	var mu sync.Mutex
	e.stager.SetTestHooks(roms.TestHooks{Open: func(p string) (io.ReadCloser, error) {
		mu.Lock()
		opened = append(opened, filepath.Base(p))
		mu.Unlock()
		return os.Open(p)
	}})
	res, err = e.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.UploadIDs) != 0 || res.Staged != 0 || res.Review != 0 || len(opened) != 0 {
		t.Fatalf("%+v, copied %v", res, opened)
	}
	rep := e.report(t)
	if !strings.Contains(rep, "emulationstation/roms/snes/Pack.zip - skipped: already imported\n") {
		t.Fatalf("report:\n%s", rep)
	}
	for _, l := range strings.Split(rep, "\n") {
		if strings.HasPrefix(l, "emulationstation/") && !strings.HasSuffix(l, " - skipped: already imported") {
			t.Errorf("line %q", l)
		}
	}
}

func TestRunOversizeZipIsSkipped(t *testing.T) {
	e := newEnv(t)
	e.cfg.MaxExtractBytes = 1 << 20
	old := freeBytes
	t.Cleanup(func() { freeBytes = old })
	freeBytes = func(string) (int64, error) { return 1 << 20, nil }
	writeFiles(t, e.stick, map[string][]byte{
		// 4 MB unpacked, but only a few KB as a ZIP
		"emulationstation/roms/snes/Huge.zip":  mkZip(t, map[string]string{"Huge.smc": strings.Repeat("h", 4<<20)}),
		"emulationstation/roms/snes/Small.smc": []byte("x"),
	})
	res, err := e.run(t)
	if err != nil || res.Staged != 1 || res.Skipped != 1 {
		t.Fatal(res, err)
	}
	if rep := e.report(t); !strings.Contains(rep, "emulationstation/roms/snes/Huge.zip - skipped: archive is too large when unpacked\n") {
		t.Fatalf("report:\n%s", rep)
	}
}

// --- symlinks on the stick ---

func TestDetectRefusesLinkedBase(t *testing.T) {
	for _, name := range []string{"emulationstation", "retropie"} {
		m := t.TempDir()
		target := t.TempDir()
		os.Symlink(target, filepath.Join(m, name))
		if _, _, err := Detect(m); err == nil || !strings.Contains(err.Error(), "link or file named "+name) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	m := t.TempDir()
	os.WriteFile(filepath.Join(m, "emulationstation"), nil, 0o644)
	if _, _, err := Detect(m); err == nil {
		t.Fatal("a file named emulationstation must be an error")
	}
}

func TestRunLinkedBaseIsNotPrepared(t *testing.T) {
	e := newEnv(t)
	target := t.TempDir()
	os.Symlink(target, filepath.Join(e.stick, "emulationstation"))
	res, err := e.run(t)
	if err == nil || res.Prepared {
		t.Fatal(res, err)
	}
	if es, _ := os.ReadDir(target); len(es) != 0 {
		t.Fatalf("wrote through the link: %v", es)
	}
	if e.priv.umounts() != 1 {
		t.Fatal(e.priv.calls)
	}
}

func TestPrepareDoesNotFollowLinks(t *testing.T) {
	// a linked roms folder
	m := t.TempDir()
	target := t.TempDir()
	os.MkdirAll(filepath.Join(m, "emulationstation"), 0o755)
	os.Symlink(target, filepath.Join(m, "emulationstation", "roms"))
	if err := Prepare(m, []string{"snes"}); err == nil {
		t.Fatal("want error for linked roms folder")
	}
	if es, _ := os.ReadDir(target); len(es) != 0 {
		t.Fatalf("wrote through the link: %v", es)
	}
	// a linked README
	m = t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim.txt")
	os.WriteFile(victim, []byte("keep"), 0o644)
	os.MkdirAll(filepath.Join(m, "emulationstation"), 0o755)
	os.Symlink(victim, filepath.Join(m, "emulationstation", "README.txt"))
	if err := Prepare(m, []string{"snes"}); err == nil {
		t.Fatal("want error for linked README")
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatalf("victim changed: %q", b)
	}
}

func TestWriteReportDoesNotFollowLinks(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim.txt")
	os.WriteFile(victim, []byte("keep"), 0o644)
	os.Symlink(victim, filepath.Join(base, "import-report.txt"))
	if err := WriteReport(base, time.Now(), nil); err == nil {
		t.Fatal("want error")
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatalf("victim changed: %q", b)
	}
	if fi, err := os.Lstat(filepath.Join(base, "import-report.txt")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("link was replaced", err)
	}
	// a linked base folder
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "emulationstation")
	os.Symlink(real, link)
	if err := WriteReport(link, time.Now(), nil); err == nil {
		t.Fatal("want error for linked base")
	}
	if es, _ := os.ReadDir(real); len(es) != 0 {
		t.Fatalf("wrote through the link: %v", es)
	}
	// no temp files are left after a normal write
	if err := WriteReport(real, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if es, _ := os.ReadDir(real); len(es) != 1 {
		t.Fatalf("files: %v", es)
	}
}

func TestRunLinkedRomsFolderFails(t *testing.T) {
	e := newEnv(t)
	target := t.TempDir()
	writeFiles(t, target, map[string][]byte{"snes/a.smc": []byte("x")})
	os.MkdirAll(filepath.Join(e.stick, "emulationstation"), 0o755)
	os.Symlink(target, filepath.Join(e.stick, "emulationstation", "roms"))
	if _, err := e.run(t); err == nil || !strings.Contains(err.Error(), "emulationstation/roms") {
		t.Fatalf("err = %v", err)
	}
}

// rawZip returns a ZIP with one stored entry whose header claims size bytes
// when unpacked.
func rawZip(t *testing.T, name string, size uint64) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.CreateRaw(&zip.FileHeader{Name: name, Method: zip.Store, CompressedSize64: 1, UncompressedSize64: size})
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("x"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestNeedBytesDoesNotOverflow(t *testing.T) {
	e := newEnv(t)
	e.cfg.MaxExtractBytes = math.MaxInt64
	r := t.TempDir()
	writeFiles(t, r, map[string][]byte{
		"snes/a.zip": rawZip(t, "a.smc", 1<<62),
		"snes/b.zip": rawZip(t, "b.smc", 1<<62),
		"snes/c.zip": rawZip(t, "c.smc", 1<<62),
	})
	groups, _, err := Scan(r, 100)
	if err != nil {
		t.Fatal(err)
	}
	if need := NeedBytes(e.stager, groups); need != math.MaxInt64 {
		t.Fatalf("need %d", need)
	}
}

func TestRunNoSpaceInDeclaredSizes(t *testing.T) {
	e := newEnv(t)
	e.cfg.MaxExtractBytes = math.MaxInt64
	writeFiles(t, e.stick, map[string][]byte{
		"emulationstation/roms/snes/a.zip": rawZip(t, "a.smc", 1<<62),
		"emulationstation/roms/snes/b.zip": rawZip(t, "b.smc", 1<<62),
	})
	if _, err := e.run(t); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunExistingTargetWithOtherSizeNeedsReview(t *testing.T) {
	e := newEnv(t)
	writeFiles(t, e.stick, map[string][]byte{
		"emulationstation/roms/snes/a.smc": []byte("new"),
		"emulationstation/roms/snes/b.smc": []byte("x"),
	})
	writeFiles(t, e.cfg.Paths.Roms, map[string][]byte{"snes/a.smc": []byte("older file")})
	res, err := e.run(t)
	if err != nil || res.Staged != 1 || res.Review != 1 {
		t.Fatal(res, err)
	}
	rep := e.report(t)
	if !strings.Contains(rep, "emulationstation/roms/snes/a.smc - needs review\n") ||
		!strings.Contains(rep, "emulationstation/roms/snes/b.smc - staged\n") {
		t.Fatalf("report:\n%s", rep)
	}
}

// fullWriter fails every write with ENOSPC, like a full SD card.
type fullWriter struct{ f *os.File }

func (w fullWriter) Write([]byte) (int, error) {
	return 0, &os.PathError{Op: "write", Path: w.f.Name(), Err: syscall.ENOSPC}
}
func (w fullWriter) Close() error { return w.f.Close() }

func TestRunDiskFullWhileCopying(t *testing.T) {
	e := newEnv(t)
	writeFiles(t, e.stick, map[string][]byte{
		"emulationstation/roms/nes/a.nes":  []byte("x"),
		"emulationstation/roms/snes/b.smc": []byte("y"),
	})
	e.stager.SetTestHooks(roms.TestHooks{Create: func(p string) (io.WriteCloser, error) {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil || filepath.Base(p) != "b.smc" {
			return f, err
		}
		return fullWriter{f}, nil
	}})
	_, err := e.run(t)
	if !errors.Is(err, ErrNoSpace) || err.Error() != "Not enough space on the SD card." {
		t.Fatalf("err = %v", err)
	}
	if !stagingEmpty(t, e.cfg.Paths.Staging) {
		t.Fatal("upload not discarded")
	}
	if e.priv.umounts() != 1 {
		t.Fatalf("calls %v", e.priv.calls)
	}
}

func TestRunReimportMovesProgress(t *testing.T) {
	e := newEnv(t)
	files := map[string][]byte{}
	for _, n := range []string{"a", "b", "c", "d"} {
		files["emulationstation/roms/snes/"+n+".smc"] = make([]byte, 1<<20)
	}
	writeFiles(t, e.stick, files)
	res, err := e.run(t)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, e.stager, res.UploadIDs)
	r := &logReporter{}
	res, err = e.im.Run(context.Background(), r, e.device, "vfat")
	if err != nil || len(res.UploadIDs) != 0 {
		t.Fatal(res, err)
	}
	steps := map[float64]bool{}
	for _, p := range r.hist {
		if p > 0 && p < 0.95 {
			steps[p] = true
		}
	}
	if len(steps) < 3 {
		t.Fatalf("progress jumps: %v", r.hist)
	}
}

func TestStickFolderInsteadOfFile(t *testing.T) {
	m := t.TempDir()
	os.MkdirAll(filepath.Join(m, "emulationstation", "README.txt"), 0o755)
	err := Prepare(m, []string{"snes"})
	if err == nil || err.Error() != "The stick has a folder named emulationstation/README.txt. Remove it on your PC." {
		t.Fatalf("err = %v", err)
	}
	base := t.TempDir()
	os.Mkdir(filepath.Join(base, "import-report.txt"), 0o755)
	err = WriteReport(base, time.Now(), nil)
	if err == nil || !strings.HasPrefix(err.Error(), "The stick has a folder named ") || !strings.HasSuffix(err.Error(), "import-report.txt. Remove it on your PC.") {
		t.Fatalf("err = %v", err)
	}
}

func TestWriteReportRemovesStaleTempFiles(t *testing.T) {
	base := t.TempDir()
	os.WriteFile(filepath.Join(base, ".emulationstation-tmp-import-report.txt.0011.tmp"), []byte("old"), 0o644)
	os.Mkdir(filepath.Join(base, ".emulationstation-tmp-keep"), 0o755)
	os.WriteFile(filepath.Join(base, "mine.txt"), []byte("keep"), 0o644)
	if err := WriteReport(base, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	es, _ := os.ReadDir(base)
	var got []string
	for _, e := range es {
		got = append(got, e.Name())
	}
	if strings.Join(got, ",") != ".emulationstation-tmp-keep,import-report.txt,mine.txt" {
		t.Fatalf("files: %v", got)
	}
}

func TestRunStopsAfterTooManyReadErrors(t *testing.T) {
	for _, tc := range []struct {
		bad  int
		fail bool
	}{{maxReadErrors - 1, false}, {maxReadErrors, true}, {maxReadErrors + 5, true}} {
		e := newEnv(t)
		files := map[string][]byte{"emulationstation/roms/snes/good.smc": []byte("ok")}
		for i := 0; i < tc.bad; i++ {
			files[fmt.Sprintf("emulationstation/roms/snes/bad%02d.smc", i)] = []byte("x")
		}
		writeFiles(t, e.stick, files)
		e.stager.SetTestHooks(roms.TestHooks{Open: func(p string) (io.ReadCloser, error) {
			if strings.HasPrefix(filepath.Base(p), "bad") {
				return eioReader{}, nil // the device is still there
			}
			return os.Open(p)
		}})
		res, err := e.run(t)
		if !tc.fail {
			if err != nil || res.Skipped != tc.bad || res.Staged != 1 {
				t.Fatalf("%d errors: %+v %v", tc.bad, res, err)
			}
			continue
		}
		if !errors.Is(err, ErrTooManyReadErrors) || err.Error() != "The USB stick has too many read errors. Check it on your PC." {
			t.Fatalf("%d errors: err = %v", tc.bad, err)
		}
		if !stagingEmpty(t, e.cfg.Paths.Staging) || e.priv.umounts() != 1 {
			t.Fatalf("not cleaned up: %v", e.priv.calls)
		}
	}
}

func TestRunReadErrorInDiscLogsOnlyTheBadFile(t *testing.T) {
	e := newEnv(t)
	writeFiles(t, e.stick, map[string][]byte{
		"emulationstation/roms/psx/Game/Game.cue":   []byte("FILE \"Track1.bin\" BINARY\nFILE \"Track2.bin\" BINARY\n"),
		"emulationstation/roms/psx/Game/Track1.bin": []byte("1"),
		"emulationstation/roms/psx/Game/Track2.bin": []byte("2"),
	})
	e.stager.SetTestHooks(roms.TestHooks{Open: func(p string) (io.ReadCloser, error) {
		if filepath.Base(p) == "Track2.bin" {
			return eioReader{}, nil
		}
		return os.Open(p)
	}})
	r := &logReporter{}
	res, err := e.im.Run(context.Background(), r, e.device, "vfat")
	if err != nil || res.Skipped != 3 || len(res.UploadIDs) != 0 {
		t.Fatal(res, err)
	}
	var reads []string
	for _, l := range r.lines {
		if strings.HasPrefix(l, "Read error:") {
			reads = append(reads, l)
		}
	}
	if len(reads) != 1 || reads[0] != "Read error: emulationstation/roms/psx/Game/Track2.bin" {
		t.Fatalf("log %q", r.lines)
	}
	rep := e.report(t)
	for _, want := range []string{
		"emulationstation/roms/psx/Game/Track2.bin - skipped: read error\n",
		"emulationstation/roms/psx/Game/Game.cue - skipped: read error in Game/Track2.bin\n",
		"emulationstation/roms/psx/Game/Track1.bin - skipped: read error in Game/Track2.bin\n",
	} {
		if !strings.Contains(rep, want) {
			t.Fatalf("report misses %q:\n%s", want, rep)
		}
	}
}

func TestRunFileNameTooLongForTheCardIsSkipped(t *testing.T) {
	e := newEnv(t)
	writeFiles(t, e.stick, map[string][]byte{
		"emulationstation/roms/snes/a.smc":   []byte("x"),
		"emulationstation/roms/snes/odd.smc": []byte("y"),
	})
	e.stager.SetTestHooks(roms.TestHooks{Create: func(p string) (io.WriteCloser, error) {
		if filepath.Base(p) == "odd.smc" {
			return nil, &os.PathError{Op: "open", Path: p, Err: syscall.ENAMETOOLONG}
		}
		return os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	}})
	res, err := e.run(t)
	if err != nil || res.Staged != 1 || res.Skipped != 1 {
		t.Fatal(res, err)
	}
	if rep := e.report(t); !strings.Contains(rep, "emulationstation/roms/snes/odd.smc - skipped: file name too long\n") {
		t.Fatalf("report:\n%s", rep)
	}
}
