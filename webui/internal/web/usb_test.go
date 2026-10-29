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

package web

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"emulationstation/webui/internal/gamelist"
	"emulationstation/webui/internal/jobs"
	"emulationstation/webui/internal/priv"
	"emulationstation/webui/internal/roms"
	"emulationstation/webui/internal/systems"
	"emulationstation/webui/internal/usb"
)

const (
	usbDev   = "/dev/sdz9" // does not exist here, like a stick that was pulled out
	usbList2 = "/dev/sdz9\tvfat\t16000000000\tMYSTICK\n/dev/sdy1\texfat\t64000000000\t-\n"
)

// withUSB sets up a fake helper and a temp stick. It returns the stick dir
// and the fake runner.
func (e *env) withUSB(t *testing.T, list string) (string, *fakeRunner) {
	t.Helper()
	stick := t.TempDir()
	fr := &fakeRunner{out: map[string]string{
		"sudo -n /h usb-list":            list,
		"sudo -n /h usb-mount " + usbDev: stick + "\n",
	}}
	e.cfg.Paths.BIOS = filepath.Join(t.TempDir(), "bios")
	e.d.Runner = fr
	e.d.Priv = priv.Priv{R: fr, Helper: "/h"}
	e.d.USB = &usb.Importer{Priv: e.d.Priv, Stager: e.d.Stager, Config: e.d.Config, Systems: e.d.Systems}
	e.d.MountInfo = filepath.Join(t.TempDir(), "mountinfo")
	e.setMounted(t, false)
	return stick, fr
}

// setMounted writes the fake mount list: with or without a stick on
// /run/emulationstation-usb.
func (e *env) setMounted(t *testing.T, mounted bool) {
	t.Helper()
	list := "22 1 179:2 / / rw,noatime shared:1 - ext4 /dev/mmcblk0p2 rw\n"
	if mounted {
		list += "95 22 8:1 / /run/emulationstation-usb rw,nosuid,nodev,noexec,noatime - vfat /dev/sda1 rw,uid=1000\n"
	}
	if err := os.WriteFile(e.d.MountInfo, []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	e.h = NewHandler(e.d)
}

func TestUSBPageShowsEjectOnlyWhenMounted(t *testing.T) {
	e := newEnv(t)
	e.withUSB(t, usbList2)
	if b := e.get("/usb").Body.String(); strings.Contains(b, `action="/usb/eject"`) || strings.Contains(b, "still mounted") {
		t.Fatal("Eject shown without a mounted stick")
	}
	e.setMounted(t, true)
	if b := e.get("/usb").Body.String(); !strings.Contains(b, `action="/usb/eject"`) || !strings.Contains(b, "A USB stick is still mounted.") {
		t.Fatal("Eject missing for a mounted stick")
	}
	// a mount list that cannot be read still offers Eject
	e.d.MountInfo = filepath.Join(t.TempDir(), "missing")
	e.h = NewHandler(e.d)
	if b := e.get("/usb").Body.String(); !strings.Contains(b, `action="/usb/eject"`) {
		t.Fatal("Eject missing when the mount list is unknown")
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// waitJob waits until the job is finished and returns it.
func (e *env) waitJob(t *testing.T, id string) jobs.Job {
	t.Helper()
	var j jobs.Job
	waitFor(t, "job "+id, func() bool {
		var ok bool
		j, ok = e.d.Jobs.Get(id)
		return ok && (j.Status == jobs.StatusDone || j.Status == jobs.StatusFailed || j.Status == jobs.StatusAborted)
	})
	return j
}

// startImport posts an import and returns the job id.
func (e *env) startImport(t *testing.T) string {
	t.Helper()
	w := e.do("POST", "/usb/import", url.Values{"device": {usbDev}})
	loc := w.Header().Get("HX-Redirect")
	if w.Code != 200 || !strings.HasPrefix(loc, "/jobs/") {
		t.Fatalf("import: code=%d loc=%q trigger=%q", w.Code, loc, w.Header().Get("HX-Trigger"))
	}
	return strings.TrimPrefix(loc, "/jobs/")
}

func hasCall(fr *fakeRunner, call string) bool {
	for _, c := range fr.called() {
		if c == call {
			return true
		}
	}
	return false
}

// blockUSBJob submits a usb-import job that runs until the test ends.
func (e *env) blockUSBJob(t *testing.T) {
	t.Helper()
	stop := make(chan struct{})
	started := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	e.d.Jobs.Submit("usb-import", "Import from USB stick", func(ctx context.Context, r jobs.Reporter) error {
		close(started)
		select {
		case <-stop:
		case <-ctx.Done():
		}
		return nil
	})
	<-started
}

func TestUSBPageListsDevices(t *testing.T) {
	e := newEnv(t)
	e.withUSB(t, usbList2)
	w := e.get("/usb")
	b := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
	for _, want := range []string{"MYSTICK", "16.0 GB", "vfat", "64.0 GB", "exfat",
		`action="/usb/import"`, `value="/dev/sdz9"`, `value="/dev/sdy1"`, `href="/usb"`, "Refresh", `href="/usb" aria-current="page"`} {
		if !strings.Contains(b, want) {
			t.Errorf("page misses %q", want)
		}
	}
	if strings.Contains(b, "No USB stick found.") {
		t.Error("empty state shown with devices")
	}
}

func TestUSBNavEntryAfterUpload(t *testing.T) {
	e := newEnv(t)
	b := e.get("/upload").Body.String()
	up, us := strings.Index(b, `href="/upload"`), strings.Index(b, `href="/usb"`)
	if up < 0 || us < 0 || us < up {
		t.Fatalf("nav: upload at %d, usb at %d", up, us)
	}
}

func TestUSBPageEmpty(t *testing.T) {
	e := newEnv(t)
	e.withUSB(t, "")
	b := e.get("/usb").Body.String()
	if !strings.Contains(b, "No USB stick found. Plug in a stick with FAT32, exFAT or NTFS and click Refresh.") {
		t.Fatal(b)
	}
}

type errRunner struct{}

func (errRunner) Run(ctx context.Context, stdout io.Writer, name string, args ...string) error {
	return errors.New("helper broke")
}

func TestUSBPageHelperError(t *testing.T) {
	e := newEnv(t)
	var logw bytes.Buffer
	e.d.LogW = &logw
	e.d.Priv = priv.Priv{R: errRunner{}, Helper: "/h"}
	e.h = NewHandler(e.d)
	b := e.get("/usb").Body.String()
	if !strings.Contains(b, "Could not read the USB devices.") {
		t.Fatal(b)
	}
	if strings.Contains(b, "helper broke") || !strings.Contains(logw.String(), "helper broke") {
		t.Fatalf("error must be in the log only; log = %q", logw.String())
	}
}

func TestUSBImportUnknownDevice(t *testing.T) {
	e := newEnv(t)
	_, fr := e.withUSB(t, usbList2)
	w := e.do("POST", "/usb/import", url.Values{"device": {"/dev/sdx1"}})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Header().Get("HX-Trigger"), "This USB stick is not available.") {
		t.Fatalf("code=%d trigger=%q", w.Code, w.Header().Get("HX-Trigger"))
	}
	if len(e.d.Jobs.List()) != 0 || hasCall(fr, "sudo -n /h usb-mount /dev/sdx1") {
		t.Fatal("job started for an unknown device")
	}
}

func TestUSBImportAndEjectWhileRunning(t *testing.T) {
	e := newEnv(t)
	_, fr := e.withUSB(t, usbList2)
	e.blockUSBJob(t)
	w := e.do("POST", "/usb/import", url.Values{"device": {usbDev}})
	if w.Code != http.StatusConflict || !strings.Contains(w.Header().Get("HX-Trigger"), "A USB import is already running.") {
		t.Fatalf("import: code=%d trigger=%q", w.Code, w.Header().Get("HX-Trigger"))
	}
	if n := len(e.d.Jobs.List()); n != 1 {
		t.Fatalf("jobs = %d", n)
	}
	w = e.do("POST", "/usb/eject", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("eject: code=%d", w.Code)
	}
	if hasCall(fr, "sudo -n /h usb-umount") {
		t.Fatal("eject called the helper while an import runs")
	}
}

func TestUSBEjectCallsHelper(t *testing.T) {
	e := newEnv(t)
	_, fr := e.withUSB(t, usbList2)
	w := e.do("POST", "/usb/eject", nil)
	if w.Code != 200 || !strings.Contains(w.Header().Get("HX-Trigger"), "You can remove the USB stick now.") {
		t.Fatalf("code=%d trigger=%q", w.Code, w.Header().Get("HX-Trigger"))
	}
	if !hasCall(fr, "sudo -n /h usb-umount") {
		t.Fatalf("calls = %v", fr.called())
	}
}

func TestUSBImportRunsJobAndLinksReview(t *testing.T) {
	e := newEnv(t)
	stick, fr := e.withUSB(t, usbList2)
	writeTree(t, stick, map[string]string{
		"emulationstation/roms/snes/Mario.sfc": "mario",
		"emulationstation/roms/nes/Zelda.nes":  "zelda",
		"emulationstation/bios/.keep":          "",
	})
	id := e.startImport(t)
	j := e.waitJob(t, id)
	if j.Status != jobs.StatusDone || j.Kind != "usb-import" {
		t.Fatalf("job = %+v", j)
	}
	if ok, _ := regexp.MatchString(`^/upload/[^,]+,[^,]+$`, j.Link); !ok {
		t.Fatalf("link = %q", j.Link)
	}
	// one summary line; the last line says that the stick can be removed
	log := strings.Join(j.Log, "\n")
	if !strings.Contains(log, "Result: 2 files ready, 0 files need a decision, 0 files skipped, 1 BIOS file saved.") ||
		strings.Count(log, "ready") != 1 || j.Log[len(j.Log)-1] != "You can remove the USB stick now." ||
		!strings.Contains(log, "Copying roms/nes (1 file).") {
		t.Fatalf("log = %q", j.Log)
	}
	if !hasCall(fr, "sudo -n /h usb-umount") {
		t.Fatal("stick not unmounted")
	}
	page := e.get("/jobs/" + id).Body.String()
	if !strings.Contains(page, "Review files") || !strings.Contains(page, `href="`+j.Link+`"`) {
		t.Fatalf("job page has no review link: %s", page)
	}
	rev := e.get(j.Link)
	if rev.Code != 200 || !strings.Contains(rev.Body.String(), "Mario.sfc") || !strings.Contains(rev.Body.String(), "Zelda.nes") {
		t.Fatalf("review = %d", rev.Code)
	}

	// Apply everything, then import the same stick again: nothing is new.
	e.setState(t, "stopped\n")
	if w := e.do("POST", j.Link+"/commit", url.Values{}); w.Code != 200 {
		t.Fatalf("commit = %d: %s", w.Code, w.Header().Get("HX-Trigger"))
	}
	j2 := e.waitJob(t, e.startImport(t))
	if j2.Status != jobs.StatusDone || j2.Link != "" || !strings.Contains(strings.Join(j2.Log, "\n"), "Nothing new on the stick.") {
		t.Fatalf("second job = %+v", j2)
	}
	if page := e.get("/jobs/" + j2.ID).Body.String(); strings.Contains(page, `href="/upload/`) {
		t.Fatal("review link shown without uploads")
	}
}

func TestUSBImportPreparesEmptyStick(t *testing.T) {
	e := newEnv(t)
	stick, _ := e.withUSB(t, usbList2)
	j := e.waitJob(t, e.startImport(t))
	if j.Status != jobs.StatusDone || j.Link != "" || !strings.Contains(strings.Join(j.Log, "\n"), "Stick prepared. Fill it on your PC, then import again.") {
		t.Fatalf("job = %+v", j)
	}
	if _, err := os.Stat(filepath.Join(stick, "emulationstation", "roms", "snes")); err != nil {
		t.Fatal(err)
	}
}

type eioReader struct{}

func (eioReader) Read([]byte) (int, error) {
	return 0, &os.PathError{Op: "read", Path: "x", Err: syscall.EIO}
}
func (eioReader) Close() error { return nil }

// Review Focus 4: a stick pulled out during the import.
func TestUSBImportStickRemoved(t *testing.T) {
	if _, err := os.Stat(usbDev); !errors.Is(err, os.ErrNotExist) {
		t.Skip(usbDev + " exists on this machine")
	}
	e := newEnv(t)
	stick, fr := e.withUSB(t, usbList2)
	writeTree(t, stick, map[string]string{
		"emulationstation/roms/nes/a.nes":  "a",
		"emulationstation/roms/snes/b.sfc": "b",
	})
	e.d.Stager.SetTestHooks(roms.TestHooks{Open: func(p string) (io.ReadCloser, error) {
		if filepath.Base(p) == "b.sfc" {
			return eioReader{}, nil
		}
		return os.Open(p)
	}})
	id := e.startImport(t)
	j := e.waitJob(t, id)
	if j.Status != jobs.StatusFailed || j.Err != "The USB stick was removed." || j.Link != "" {
		t.Fatalf("job = %+v", j)
	}
	if !strings.Contains(e.get("/jobs/"+id).Body.String(), "The USB stick was removed.") {
		t.Fatal("job page does not show the error")
	}
	if !hasCall(fr, "sudo -n /h usb-umount") {
		t.Fatal("stick not unmounted")
	}
	ents, _ := os.ReadDir(e.cfg.Paths.Staging)
	if len(ents) != 0 {
		t.Fatalf("staging not empty: %v", ents)
	}
}

// --- review page ----------------------------------------------------------

// stageDir stages the files below a temp folder as one upload with a hint.
func (e *env) stageDir(t *testing.T, hint string, files map[string]string) string {
	t.Helper()
	src := t.TempDir()
	writeTree(t, src, files)
	var rels []string
	for rel := range files {
		rels = append(rels, rel)
	}
	up, err := e.d.Stager.StageTree(context.Background(), src, hint, rels, nil)
	if err != nil {
		t.Fatal(err)
	}
	return up.ID
}

func zipBytes(t *testing.T, name, body string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create(name)
	f.Write([]byte(body))
	zw.Close()
	return buf.String()
}

func TestReviewGroupsManyFiles(t *testing.T) {
	e := newEnv(t)
	e.write(t, "snes/Old.sfc", "old")       // already imported
	e.write(t, "snes/Mario.smc", "mario-1") // same name as a file in the ZIP below
	files := map[string]string{
		"Old.sfc":    "old",
		"Mario.smc":  "mario-1",
		"Mario.zip":  zipBytes(t, "Mario.smc", "mario-2-longer"),
		"Odd.nes":    "nes",
		"readme.txt": "junk",
	}
	for i := 0; i < 25; i++ {
		files[fmt.Sprintf("Game %02d.sfc", i)] = "g"
	}
	id := e.stageDir(t, "snes", files)
	up, _ := e.d.Stager.Get(id)
	w := e.get("/upload/" + id)
	b := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
	ready := 0
	for _, f := range up.Files {
		if !f.Discarded && f.CompanionOf == "" && len(f.Candidates) == 1 && f.Note == "" {
			ready++
		}
	}
	if !strings.Contains(b, "files ready,") || !strings.Contains(b, "need a decision") {
		t.Fatalf("no summary: %s", b)
	}
	skipped := 0
	for _, f := range up.Files {
		if f.Discarded {
			skipped++
		}
	}
	// the file with a note and the ZIP's Mario.smc (exists with another size) need a decision
	want := fmt.Sprintf("%d files ready, 2 files need a decision, %d files skipped", ready-1, skipped)
	if !strings.Contains(b, want) {
		t.Fatalf("summary %q not found; up = %+v", want, up.Files)
	}

	// The decision group is open and comes first; the note is shown there.
	decide := strings.Index(b, `id="review-decide"`)
	readyAt := strings.Index(b, `id="review-ready-snes"`)
	skipAt := strings.Index(b, `id="review-skipped"`)
	odd := strings.Index(b, `name="name|`+id+`|Odd.nes"`)
	if decide < 0 || readyAt < 0 || skipAt < 0 || !(decide < odd && odd < readyAt && readyAt < skipAt) {
		t.Fatalf("order: decide=%d odd=%d ready=%d skipped=%d", decide, odd, readyAt, skipAt)
	}
	if !strings.Contains(b, "folder snes does not accept .nes") {
		t.Fatal("note not shown")
	}
	// Ready inputs are disabled, decision inputs are not.
	readyIn := regexp.MustCompile(`<input[^>]*name="name\|` + id + `\|Game 07\.sfc"[^>]*>`).FindString(b)
	if !strings.Contains(readyIn, " disabled") {
		t.Fatalf("ready input not disabled: %q", readyIn)
	}
	oddIn := regexp.MustCompile(`<input[^>]*name="name\|` + id + `\|Odd\.nes"[^>]*>`).FindString(b)
	if oddIn == "" || strings.Contains(oddIn, "disabled") {
		t.Fatalf("decision input: %q", oddIn)
	}
	if !strings.Contains(b, `data-edit`) {
		t.Fatal("no Edit button")
	}
	// Both Mario.smc entries are shown: one with inputs, one as skipped.
	if n := strings.Count(b, `name="name|`+id+`|Mario.smc"`); n != 1 {
		t.Fatalf("Mario.smc inputs = %d", n)
	}
	if !strings.Contains(b[skipAt:], "Mario.smc") || !strings.Contains(b[skipAt:], "already imported") {
		t.Fatal("imported Mario.smc missing in the skipped group")
	}
	if !strings.Contains(b[skipAt:], "readme.txt") {
		t.Fatal("junk missing in the skipped group")
	}
	// The ready group and the skipped group are collapsed.
	for _, gid := range []string{"review-ready-snes", "review-skipped"} {
		tag := regexp.MustCompile(`<details[^>]*id="` + gid + `"[^>]*>`).FindString(b)
		if tag == "" || strings.Contains(tag, " open") {
			t.Fatalf("group %s: %q", gid, tag)
		}
	}
	if tag := regexp.MustCompile(`<[a-z]+[^>]*id="review-decide"[^>]*>`).FindString(b); strings.Contains(tag, "<details") && !strings.Contains(tag, " open") {
		t.Fatalf("decision group closed: %q", tag)
	}
}

func TestReviewSmallUploadIsFlat(t *testing.T) {
	e := newEnv(t)
	_, resp := e.upload(t, "Game.sfc", strings.NewReader("x"))
	id := resp.Uploads[0].ID
	b := e.get("/upload/" + id).Body.String()
	if !strings.Contains(b, "1 file ready, 0 files need a decision, 0 files skipped") {
		t.Fatalf("summary missing: %s", b)
	}
	in := regexp.MustCompile(`<input[^>]*name="name\|` + id + `\|Game\.sfc"[^>]*>`).FindString(b)
	if in == "" || strings.Contains(in, "disabled") || strings.Contains(b, "<details") {
		t.Fatalf("small upload must look as before: %q", in)
	}
	for q, want := range map[string]string{"1": "1 game was already applied;", "3": "3 games were already applied;"} {
		if b := e.get("/upload/" + id + "?done=" + q).Body.String(); !strings.Contains(b, want) {
			t.Fatalf("notice %q missing", want)
		}
	}
}

func TestReviewAcceptsManyIDs(t *testing.T) {
	e := newEnv(t)
	var ids []string
	for i := 0; i < 60; i++ {
		ids = append(ids, e.stageDir(t, "snes", map[string]string{fmt.Sprintf("G%d.sfc", i): "x"}))
	}
	if w := e.get("/upload/" + strings.Join(ids, ",")); w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
}

// Review Focus 5: thousands of files still commit with a small form.
func TestReviewThousandsOfFilesCommit(t *testing.T) {
	e := newEnv(t)
	e.setState(t, "stopped\n")
	const n = 5000
	files := make(map[string]string, n)
	for i := 0; i < n; i++ {
		files[fmt.Sprintf("Game %04d.sfc", i)] = "x"
	}
	id := e.stageDir(t, "snes", files)
	b := e.get("/upload/" + id).Body.String()
	t.Logf("review page for %d files: %d KiB", n, len(b)>>10)
	in := regexp.MustCompile(`<input[^>]*name="(?:name|skip)\|[^"]*"[^>]*>`).FindAllString(b, -1)
	if len(in) != 2*n {
		t.Fatalf("inputs = %d", len(in))
	}
	for _, x := range in {
		if !strings.Contains(x, " disabled") {
			t.Fatalf("enabled input on a ready file: %q", x)
		}
	}
	// ready rows have no system select at all (the one system is used)
	for _, x := range regexp.MustCompile(`<select[^>]*name="system\|[^"]*"[^>]*>`).FindAllString(b, -1) {
		if !strings.Contains(x, " disabled") {
			t.Fatalf("enabled system select on a ready file: %q", x)
		}
	}

	// The browser sends only the two files the owner opened to edit.
	form := url.Values{
		"name|" + id + "|Game 0001.sfc":   {"First Game"},
		"system|" + id + "|Game 0001.sfc": {"snes"},
		"name|" + id + "|Game 4999.sfc":   {"Last Game"},
	}
	body := form.Encode()
	if len(body) >= 1<<20 {
		t.Fatalf("body = %d bytes", len(body))
	}
	w := e.do("POST", "/upload/"+id+"/commit", form)
	if w.Code != 200 || w.Header().Get("HX-Redirect") != fmt.Sprintf("/games/snes?added=%d", n) {
		t.Fatalf("commit = %d redirect=%q trigger=%q", w.Code, w.Header().Get("HX-Redirect"), w.Header().Get("HX-Trigger"))
	}
	ents, err := os.ReadDir(filepath.Join(e.roms, "snes"))
	if err != nil {
		t.Fatal(err)
	}
	roms := 0
	for _, x := range ents {
		if strings.HasSuffix(x.Name(), ".sfc") {
			roms++
		}
	}
	if roms != n {
		t.Fatalf("committed %d files, want %d", roms, n)
	}
	// the two edited display names reached the gamelist, the others keep
	// their suggested names
	gl, err := os.ReadFile(gamelist.PathFor(e.cfg.Paths, "snes"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<name>First Game</name>", "<name>Last Game</name>", "<name>Game 0002</name>"} {
		if !strings.Contains(string(gl), want) {
			t.Fatalf("gamelist has no %s", want)
		}
	}
	if strings.Contains(string(gl), "<name>Game 0001</name>") || strings.Contains(string(gl), "<name>Game 4999</name>") {
		t.Fatal("edited names were not used")
	}
}

func TestCommitNeedsSystemForFileWithManySystems(t *testing.T) {
	e := newEnv(t)
	e.setState(t, "stopped\n")
	// .bin fits psx and (here) megadrive: the page sends a select for it
	e.d.Systems = func() []systems.System {
		return []systems.System{
			{Name: "snes", Extensions: []string{".sfc"}},
			{Name: "psx", Extensions: []string{".bin"}},
			{Name: "megadrive", Extensions: []string{".bin"}},
		}
	}
	e.d.Stager = roms.NewStager(e.cfg, e.d.Systems())
	e.h = NewHandler(e.d)
	id := e.stageDir(t, "", map[string]string{"Game.bin": "x", "Mario.sfc": "y"})
	if up, _ := e.d.Stager.Get(id); len(up.Files) != 2 {
		t.Fatalf("%+v", up.Files)
	}
	w := e.do("POST", "/upload/"+id+"/commit", url.Values{})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Header().Get("HX-Trigger"), "Choose a system for every file.") {
		t.Fatalf("code=%d trigger=%q", w.Code, w.Header().Get("HX-Trigger"))
	}
	if _, err := os.Stat(filepath.Join(e.roms, "snes", "Mario.sfc")); !os.IsNotExist(err) {
		t.Fatal("a file was moved")
	}
	// skipping the file needs no system; the one-system file uses its system
	w = e.do("POST", "/upload/"+id+"/commit", url.Values{"skip|" + id + "|Game.bin": {"1"}})
	if w.Code != 200 {
		t.Fatalf("code=%d trigger=%q", w.Code, w.Header().Get("HX-Trigger"))
	}
	if _, err := os.Stat(filepath.Join(e.roms, "snes", "Mario.sfc")); err != nil {
		t.Fatal(err)
	}
}

// A small USB-style upload uses the flat view. It must still show the
// note and put the file that needs a decision first.
func TestReviewFlatShowsNoteFirst(t *testing.T) {
	e := newEnv(t)
	files := map[string]string{"Odd.nes": "nes"}
	for i := 0; i < 5; i++ {
		files[fmt.Sprintf("Game %d.sfc", i)] = "g"
	}
	id := e.stageDir(t, "snes", files)
	b := e.get("/upload/" + id).Body.String()
	if strings.Contains(b, "<details") {
		t.Fatal("6 files must use the flat view")
	}
	if !strings.Contains(b, "5 files ready, 1 file needs a decision, 0 files skipped") {
		t.Fatalf("summary missing: %s", b)
	}
	odd := strings.Index(b, `name="name|`+id+`|Odd.nes"`)
	note := strings.Index(b, "Note: folder snes does not accept .nes")
	if odd < 0 || note < 0 {
		t.Fatalf("odd=%d note=%d", odd, note)
	}
	// The note is on the Odd.nes row: after the row before it, before its input.
	if prev := strings.LastIndex(b[:odd], "<li"); note < prev || note > odd {
		t.Fatalf("note not on the Odd.nes row: li=%d note=%d input=%d", prev, note, odd)
	}
	for i := 0; i < 5; i++ {
		at := strings.Index(b, fmt.Sprintf(`name="name|%s|Game %d.sfc"`, id, i))
		if at < 0 || at < odd {
			t.Fatalf("Game %d.sfc at %d comes before Odd.nes at %d", i, at, odd)
		}
	}
	for _, x := range regexp.MustCompile(`<input[^>]*name="(?:name|skip)\|[^"]*"[^>]*>`).FindAllString(b, -1) {
		if strings.Contains(x, "disabled") {
			t.Fatalf("flat view must not disable inputs: %q", x)
		}
	}
}

func TestReviewScriptNeedsDecision(t *testing.T) {
	e := newEnv(t)
	files := map[string]string{"Doom.sh": "#!/bin/sh\n"}
	for i := 0; i < 25; i++ {
		files[fmt.Sprintf("Game %02d.sh", i)] = "#!/bin/sh\n"
	}
	id := e.stageDir(t, "ports", files)
	b := e.get("/upload/" + id).Body.String()
	if !strings.Contains(b, "0 files ready, 26 files need a decision, 0 files skipped") {
		t.Fatalf("summary missing: %s", b)
	}
	decide := strings.Index(b, `id="review-decide"`)
	doom := strings.Index(b, `name="name|`+id+`|Doom.sh"`)
	if decide < 0 || doom < decide || strings.Contains(b, `id="review-ready-ports"`) {
		t.Fatalf("decide=%d doom=%d", decide, doom)
	}
	if !strings.Contains(b, "This file is a script. It runs as a program when started.") {
		t.Fatal("note not shown")
	}
	in := regexp.MustCompile(`<input[^>]*name="name\|` + id + `\|Doom\.sh"[^>]*>`).FindString(b)
	if in == "" || strings.Contains(in, "disabled") {
		t.Fatalf("decision input: %q", in)
	}
}

// mountFailRunner answers usb-list like fakeRunner and lets usb-mount fail
// like the real helper: usage text on stderr and the given exit code.
type mountFailRunner struct {
	*fakeRunner
	code int
}

func (m mountFailRunner) Run(ctx context.Context, stdout io.Writer, name string, args ...string) error {
	if len(args) >= 3 && args[2] == "usb-mount" {
		script := fmt.Sprintf("echo 'priv: device is not a usable USB stick' >&2; echo 'Usage: priv reboot|poweroff' >&2; exit %d", m.code)
		return priv.ExecRunner{}.Run(ctx, stdout, "sh", "-c", script)
	}
	return m.fakeRunner.Run(ctx, stdout, name, args...)
}

func TestUSBMountFailureMessages(t *testing.T) {
	cases := []struct {
		code int
		list string
		want string
	}{
		{2, usbList2, "This USB stick is not available. Click Refresh."},
		{1, usbList2, "The USB stick could not be mounted. Check that it uses FAT32, exFAT or NTFS."},
		{1, "/dev/sdz9\tntfs\t16000000000\tWIN\n", "The USB stick could not be mounted. Check that it uses FAT32, exFAT or NTFS. An NTFS stick that was not ejected safely on Windows cannot be mounted. Eject it safely and try again."},
	}
	for _, c := range cases {
		e := newEnv(t)
		var logw bytes.Buffer
		e.d.LogW = &logw
		_, fr := e.withUSB(t, c.list)
		e.d.Priv = priv.Priv{R: mountFailRunner{fr, c.code}, Helper: "/h"}
		e.d.USB.Priv = e.d.Priv
		e.h = NewHandler(e.d)
		j := e.waitJob(t, e.startImport(t))
		if j.Status != jobs.StatusFailed || j.Err != c.want {
			t.Errorf("exit %d: status %s, err %q", c.code, j.Status, j.Err)
		}
		page := e.get("/jobs/" + j.ID).Body.String()
		if strings.Contains(page, "Usage:") || strings.Contains(strings.Join(j.Log, "\n"), "Usage:") {
			t.Errorf("exit %d: the helper usage text is shown to the user", c.code)
		}
		if !strings.Contains(logw.String(), "Usage: priv") || !strings.Contains(logw.String(), "usb-mount /dev/sdz9") {
			t.Errorf("exit %d: raw error not in the service log: %q", c.code, logw.String())
		}
	}
}

func TestUSBEjectAndImportDoNotOverlap(t *testing.T) {
	e := newEnv(t)
	_, fr := e.withUSB(t, usbList2)
	inUmount := make(chan struct{})
	release := make(chan struct{})
	var once, releaseOnce sync.Once
	free := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(free) // runs before the job queue stops
	fr.hook = func(args []string) {
		if len(args) >= 3 && args[2] == "usb-umount" {
			once.Do(func() { close(inUmount); <-release })
		}
	}
	e.h = NewHandler(e.d)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); e.do("POST", "/usb/eject", nil) }()
	<-inUmount
	go func() { defer wg.Done(); e.do("POST", "/usb/import", url.Values{"device": {usbDev}}) }()
	time.Sleep(100 * time.Millisecond)
	if n := len(e.d.Jobs.List()); n != 0 {
		t.Fatalf("an import started while the eject was unmounting (%d jobs)", n)
	}
	free()
	wg.Wait()
	if n := len(e.d.Jobs.List()); n != 1 {
		t.Fatalf("jobs = %d", n)
	}
	e.waitJob(t, e.d.Jobs.List()[0].ID)
}

func TestUSBImportMoreThanMaxFolders(t *testing.T) {
	e := newEnv(t)
	stick, _ := e.withUSB(t, usbList2)
	files := map[string]string{}
	for i := 0; i <= maxReviewIDs; i++ {
		files[fmt.Sprintf("emulationstation/roms/f%03d/Game.sfc", i)] = "x"
	}
	files["emulationstation/roms/f200/Other.sfc"] = "y"
	writeTree(t, stick, files)
	j := e.waitJob(t, e.startImport(t))
	if j.Status != jobs.StatusDone || strings.Count(j.Link, ",") != maxReviewIDs-1 {
		t.Fatalf("job = %v %q", j.Status, j.Log)
	}
	log := strings.Join(j.Log, "\n")
	// the counts are those of the uploads that are left
	if !strings.Contains(log, fmt.Sprintf("Result: %d files ready, 0 files need a decision, 0 files skipped, 0 BIOS files saved.", maxReviewIDs)) ||
		!strings.Contains(log, "Not staged: 2 files.") || strings.Index(log, "Result:") > strings.Index(log, "Not staged") {
		t.Fatalf("log = %s", log)
	}
	ents, _ := os.ReadDir(e.cfg.Paths.Staging)
	if len(ents) != maxReviewIDs {
		t.Fatalf("staging has %d uploads", len(ents))
	}
}

// renderReview renders the review page for hand-made uploads.
func (e *env) renderReview(t *testing.T, ups ...*roms.Upload) string {
	t.Helper()
	tpl, err := parseTemplates(assets)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{d: e.d, tpl: tpl, now: time.Now, changes: &Changes{}}
	v := s.buildReview(ups)
	v.IDs = "x"
	w := httptest.NewRecorder()
	s.page(w, httptest.NewRequest("GET", "/upload/x", nil), "upload_review", "Review upload", v)
	return w.Body.String()
}

func TestReviewOrphanCompanionsAreNotADecision(t *testing.T) {
	e := newEnv(t)
	up := &roms.Upload{ID: "u1"}
	for i := 0; i < reviewFlatMax+1; i++ {
		up.Files = append(up.Files, roms.StagedFile{Name: fmt.Sprintf("G%02d.sfc", i), Candidates: []string{"snes"}, SuggestedName: "G"})
	}
	// a companion whose main file is discarded
	up.Files = append(up.Files,
		roms.StagedFile{Name: "Disc.cue", Discarded: true, Reason: "read error"},
		roms.StagedFile{Name: "Disc.bin", CompanionOf: "Disc.cue"})
	b := e.renderReview(t, up)
	if strings.Contains(b, "Need a decision") || strings.Contains(b, `id="review-decide"`) {
		t.Fatal(`"Need a decision" shown without a file that needs one`)
	}
	at := strings.Index(b, `id="review-orphans"`)
	if at < 0 || !strings.Contains(b[at:], "Files that belong to another file (1)") || !strings.Contains(b[at:], "Belongs to Disc.cue") {
		t.Fatalf("orphan section missing")
	}
	if !strings.Contains(b, "21 files ready, 0 files need a decision, 1 file skipped") {
		t.Fatal("summary")
	}
}

func TestReviewFlatKeepsCompanionsWithTheirMainFile(t *testing.T) {
	e := newEnv(t)
	e.write(t, "snes/Exists.sfc", "old")
	up := &roms.Upload{ID: "u1", Files: []roms.StagedFile{
		{Name: "A.sfc", Candidates: []string{"snes"}},
		{Name: "Disc/Game.bin", CompanionOf: "Disc/Game.cue"},
		{Name: "Disc/Game.cue", Candidates: []string{"psx", "snes"}},
		{Name: "Disc/Track2.bin", CompanionOf: "Disc/Game.cue"},
		{Name: "Exists.sfc", Candidates: []string{"snes"}},
		{Name: "Odd.xyz"},
		{Name: "Z.sfc", Candidates: []string{"snes"}},
	}}
	b := e.renderReview(t, up)
	pos := func(s string) int {
		i := strings.Index(b, s)
		if i < 0 {
			t.Fatalf("%q missing", s)
		}
		return i
	}
	cue := pos(`title="Disc/Game.cue"`)
	bin := pos(`title="Disc/Game.bin"`)
	track := pos(`title="Disc/Track2.bin"`)
	exists := pos(`title="Exists.sfc"`)
	odd := pos(`title="Odd.xyz"`)
	a := pos(`title="A.sfc"`)
	// decision rows first (the disc with its companions right after it),
	// then the ready rows in their order
	if !(cue < bin && bin < track && track < exists && exists < odd && odd < a && a < pos(`title="Z.sfc"`)) {
		t.Fatalf("order: cue=%d bin=%d track=%d exists=%d odd=%d a=%d", cue, bin, track, exists, odd, a)
	}
	if !strings.Contains(b[exists:odd], "A file with this name is already there.") {
		t.Fatal("conflict text missing on Exists.sfc")
	}
	if !strings.Contains(b[odd:a], "No system accepts this file.") {
		t.Fatal(`"No system accepts this file." missing on Odd.xyz`)
	}
	if strings.Count(b, "A file with this name is already there.") != 1 || strings.Count(b, "No system accepts this file.") != 1 {
		t.Fatal("texts on the wrong rows")
	}
}
