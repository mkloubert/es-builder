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
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"emulationstation/webui/internal/gamelist"
	"emulationstation/webui/internal/jobs"
)

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

type uploadResp struct {
	Uploads []struct{ ID, Name string }
}

func (e *env) upload(t *testing.T, filename string, body io.Reader) (*httptest.ResponseRecorder, uploadResp) {
	t.Helper()
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("file", filename)
		if err == nil {
			_, err = io.Copy(part, body)
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()
	req := httptest.NewRequest("POST", "/upload", pr)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth("u", "pw")
	w := serve(e.h, req)
	io.Copy(io.Discard, pr) // let the writer finish on early errors
	var resp uploadResp
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w, resp
}

func TestUploadStreamsLargeFileWithoutParseMultipart(t *testing.T) {
	e := newEnv(t)
	const size = 100 << 20
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	w, resp := e.upload(t, "big.sfc", io.LimitReader(zeroReader{}, size))
	if w.Code != 200 || len(resp.Uploads) != 1 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	const limit = 32 << 20
	if d := int64(after.TotalAlloc) - int64(before.TotalAlloc); d > limit {
		t.Fatalf("allocated %d MiB while uploading 100 MiB (limit 32)", d>>20)
	}
	if d := int64(after.HeapAlloc) - int64(before.HeapAlloc); d > limit {
		t.Fatalf("heap grew by %d MiB (limit 32)", d>>20)
	}
	fi, err := os.Stat(filepath.Join(e.cfg.Paths.Staging, resp.Uploads[0].ID, "big.sfc"))
	if err != nil || fi.Size() != size {
		t.Fatalf("staged file: %v %v", fi, err)
	}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range files {
		f, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(c))
	}
	zw.Close()
	return buf.Bytes()
}

func TestUploadCommitFlow(t *testing.T) {
	e := newEnv(t)
	e.setState(t, "stopped\n")
	const fn = "Super Mario World (USA).sfc"
	w, resp := e.upload(t, "smw.zip", bytes.NewReader(zipOf(t, map[string]string{fn: "ROMDATA"})))
	if w.Code != 200 || len(resp.Uploads) != 1 {
		t.Fatalf("upload status = %d body = %s", w.Code, w.Body)
	}
	id := resp.Uploads[0].ID

	review := e.do("GET", "/upload/"+id, nil)
	body := review.Body.String()
	if review.Code != 200 || !strings.Contains(body, "Super Mario World") || !strings.Contains(body, `value="snes"`) {
		t.Fatalf("review page = %d: %s", review.Code, body)
	}

	w = e.do("POST", "/upload/"+id+"/commit", url.Values{
		"system|" + id + "|" + fn: {"snes"},
		"name|" + id + "|" + fn:   {"Super Mario World"},
	})
	if w.Code != 200 {
		t.Fatalf("commit status = %d: %s", w.Code, w.Body)
	}
	if loc := w.Header().Get("HX-Redirect"); !strings.HasPrefix(loc, "/games/snes") {
		t.Fatalf("HX-Redirect = %q", loc)
	}
	if b, err := os.ReadFile(filepath.Join(e.roms, "snes", fn)); err != nil || string(b) != "ROMDATA" {
		t.Fatalf("rom: %q %v", b, err)
	}
	gl, _ := os.ReadFile(gamelist.PathFor(e.cfg.Paths, "snes"))
	if !strings.Contains(string(gl), "<name>Super Mario World</name>") {
		t.Fatalf("gamelist = %s", gl)
	}
	if e.do("GET", "/upload/"+id, nil).Code != http.StatusNotFound {
		t.Fatal("upload still reviewable after commit")
	}
}

func TestUploadCommitWithScrapeSubmitsJob(t *testing.T) {
	e := newEnv(t)
	e.setState(t, "stopped\n")
	_, resp := e.upload(t, "Game.sfc", strings.NewReader("x"))
	id := resp.Uploads[0].ID
	w := e.do("POST", "/upload/"+id+"/commit", url.Values{
		"system|" + id + "|Game.sfc": {"snes"}, "scrape": {"1"}})
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("HX-Redirect"), "/jobs/") {
		t.Fatalf("status = %d redirect = %q", w.Code, w.Header().Get("HX-Redirect"))
	}
	jp := e.do("GET", w.Header().Get("HX-Redirect"), nil)
	if jp.Code != 200 || !strings.Contains(jp.Body.String(), "Scrape") {
		t.Fatalf("job page = %d: %s", jp.Code, jp.Body)
	}
}

func TestUploadRejectsUnsafeAndSkipsNonFiles(t *testing.T) {
	e := newEnv(t)
	w, _ := e.upload(t, "../../evil.sfc", strings.NewReader("x"))
	if w.Code != 200 { // path components are stripped, never honoured
		t.Fatalf("status = %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(e.cfg.Paths.Staging, "..", "evil.sfc")); err == nil {
		t.Fatal("file escaped staging")
	}
	if e.do("GET", "/upload/nonexistent", nil).Code != http.StatusNotFound {
		t.Fatal("unknown upload id must be 404")
	}
}

func TestJobEventsStream(t *testing.T) {
	e := newEnv(t)
	job := e.d.Jobs.Submit("test", "Testjob", func(ctx context.Context, r jobs.Reporter) error {
		r.Logf("hallo")
		return nil
	})
	req := httptest.NewRequest("GET", "/jobs/"+job.ID+"/events", nil)
	req.SetBasicAuth("u", "pw")
	w := serve(e.h, req)
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), `"status":"done"`) {
		t.Fatalf("stream = %s", w.Body)
	}
	if e.do("GET", "/jobs/zzz", nil).Code != http.StatusNotFound {
		t.Fatal("unknown job must be 404")
	}
}

func TestMultiUploadChecksAllBeforeMoving(t *testing.T) {
	e := newEnv(t)
	e.setState(t, "stopped\n")
	_, a := e.upload(t, "A.sfc", strings.NewReader("a"))
	_, b := e.upload(t, "B.sfc", strings.NewReader("b"))
	ida, idb := a.Uploads[0].ID, b.Uploads[0].ID
	e.write(t, "snes/B.sfc", "existing") // conflict only in the second upload
	form := url.Values{"system|" + ida + "|A.sfc": {"snes"}, "system|" + idb + "|B.sfc": {"snes"}}
	// duplicate id in the path must be ignored
	w := e.do("POST", "/upload/"+ida+","+idb+","+ida+"/commit", form)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(e.roms, "snes", "A.sfc")); err == nil {
		t.Fatal("first upload moved although second conflicts")
	}
	if !strings.Contains(w.Header().Get("HX-Trigger"), "B.sfc") {
		t.Fatalf("toast = %q", w.Header().Get("HX-Trigger"))
	}
	form.Set("overwrite", "1")
	if w := e.do("POST", "/upload/"+ida+","+idb+"/commit", form); w.Code != 200 {
		t.Fatalf("overwrite commit = %d: %s", w.Code, w.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(e.roms, "snes", "B.sfc")); string(b) != "b" {
		t.Fatalf("B = %q", b)
	}
}

type enospcReader struct{ n int }

func (r *enospcReader) Read(p []byte) (int, error) {
	if r.n > 0 {
		r.n--
		return copy(p, "data"), nil
	}
	return 0, fmt.Errorf("write: %w", syscall.ENOSPC)
}

func TestUploadDiskFullGives507(t *testing.T) {
	e := newEnv(t)
	w, _ := e.upload(t, "x.sfc", &enospcReader{n: 2})
	if w.Code != 507 || !strings.Contains(w.Body.String(), "Not enough space on the SD card.") {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}
	ents, _ := os.ReadDir(e.cfg.Paths.Staging)
	if len(ents) != 0 {
		t.Fatalf("staging not empty: %v", ents)
	}
}

func TestUploadZipNames(t *testing.T) {
	e := newEnv(t)
	zipOf := func(name string) *bytes.Reader {
		var buf bytes.Buffer
		w := zip.NewWriter(&buf)
		f, _ := w.Create(name)
		f.Write([]byte("rom"))
		w.Close()
		return bytes.NewReader(buf.Bytes())
	}
	// a long folder in the ZIP is fine: only the base name is kept
	w, resp := e.upload(t, "Pack.zip", zipOf(strings.Repeat("ゲ", 100)+"/Mario.smc"))
	if w.Code != 200 || len(resp.Uploads) != 1 {
		t.Fatalf("long folder: %d %s", w.Code, w.Body)
	}
	if up, ok := e.d.Stager.Get(resp.Uploads[0].ID); !ok || len(up.Files) != 1 || up.Files[0].Name != "Mario.smc" || up.Files[0].Discarded {
		t.Fatalf("%+v", up)
	}
	// a base name over 255 bytes cannot be stored on the SD card
	w, _ = e.upload(t, "Long.zip", zipOf(strings.Repeat("ゲ", 100)+".smc"))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "The archive contains a file name that is too long.") {
		t.Fatalf("long name: %d %s", w.Code, w.Body)
	}
}
