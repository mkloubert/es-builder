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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"emulationstation/webui/internal/es"
	"emulationstation/webui/internal/jobs"
	"emulationstation/webui/internal/roms"
	"emulationstation/webui/internal/systems"
)

// maxReviewIDs bounds how many staged uploads one review page may combine.
const maxReviewIDs = 200

func (s *Server) uploadRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /upload", s.handleUploadPage)
	mux.HandleFunc("POST /upload", s.handleUpload)
	mux.HandleFunc("GET /upload/{ids}", s.handleReview)
	mux.HandleFunc("POST /upload/{ids}/commit", s.handleCommit)
	mux.HandleFunc("POST /upload/{ids}/discard", s.handleUploadDiscard)
}

func (s *Server) handleUploadPage(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, "upload", "Upload", nil)
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func (s *Server) uploadError(w http.ResponseWriter, r *http.Request, code int, msg string) {
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
		return
	}
	http.Error(w, msg, code)
}

const msgNoSpace = "Not enough space on the SD card."

// isNoSpace reports a full disk or an exceeded quota.
func isNoSpace(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}

// uploadErrStatus maps a staging error to an HTTP status and German message.
func uploadErrStatus(err error) (int, string) {
	var maxErr *http.MaxBytesError
	switch {
	case isNoSpace(err):
		return http.StatusInsufficientStorage, msgNoSpace
	case errors.Is(err, roms.ErrUploadTooLarge), errors.As(err, &maxErr):
		return http.StatusRequestEntityTooLarge, "The file is too large."
	case errors.Is(err, roms.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, "The archive is too large when unpacked."
	case errors.Is(err, roms.ErrTooManyEntries):
		return http.StatusBadRequest, "The archive contains too many files."
	case errors.Is(err, roms.ErrUnsafePath):
		return http.StatusBadRequest, "The archive contains unsafe file names."
	case errors.Is(err, roms.ErrNameTooLong):
		return http.StatusBadRequest, "The archive contains a file name that is too long."
	case errors.Is(err, zip.ErrFormat), errors.Is(err, zip.ErrChecksum), errors.Is(err, zip.ErrAlgorithm):
		return http.StatusBadRequest, "The archive is damaged."
	}
	return http.StatusInternalServerError, "Upload failed."
}

// handleUpload streams every file part straight into the staging area. It
// deliberately never calls ParseMultipartForm / FormFile.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	cfg := s.d.Config()
	r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxUploadBytes+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		s.uploadError(w, r, http.StatusBadRequest, "Invalid upload request.")
		return
	}
	type item struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var done []item
	rollback := func() {
		for _, it := range done {
			_ = s.d.Stager.Discard(it.ID)
		}
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			rollback()
			code, msg := uploadErrStatus(err)
			if code == http.StatusInternalServerError {
				code, msg = http.StatusBadRequest, "Invalid upload request."
			}
			s.uploadError(w, r, code, msg)
			return
		}
		if part.FileName() == "" {
			continue
		}
		up, err := s.d.Stager.Receive(part, part.FileName())
		if err != nil {
			rollback()
			s.logf("upload %q: %v", part.FileName(), err)
			code, msg := uploadErrStatus(err)
			s.uploadError(w, r, code, msg)
			return
		}
		done = append(done, item{up.ID, part.FileName()})
	}
	if len(done) == 0 {
		s.uploadError(w, r, http.StatusBadRequest, "No file was sent.")
		return
	}
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"uploads": done})
		return
	}
	ids := make([]string, len(done))
	for i, it := range done {
		ids[i] = it.ID
	}
	http.Redirect(w, r, "/upload/"+strings.Join(ids, ","), http.StatusSeeOther)
}

// --- review & commit -------------------------------------------------------

// countOf returns "1 <one>" or "<n> <many>".
func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// fileCount returns "1 file" or "<n> files".
func fileCount(n int) string { return countOf(n, "file", "files") }

// needVerb returns "needs" for one file, else "need".
func needVerb(n int) string {
	if n == 1 {
		return "needs"
	}
	return "need"
}

// reviewSummary is the count line of the review page, for example
// "1 file ready, 2 files need a decision, 0 files skipped".
func reviewSummary(ready, decide, skipped int) string {
	return fmt.Sprintf("%s ready, %s %s a decision, %s skipped", fileCount(ready), fileCount(decide), needVerb(decide), fileCount(skipped))
}

type sysOpt struct{ Name, FullName string }

type reviewFile struct {
	roms.StagedFile
	UploadID   string
	Primary    bool
	Opts       []sysOpt
	Companions []string // files that are moved with this one (grouped view)
	Conflict   bool     // the target file already exists
}

// reviewGroup holds the ready files of one system.
type reviewGroup struct {
	System, FullName string
	Files            []reviewFile
}

type reviewView struct {
	Notice  string
	IDs     string
	Files   []reviewFile // every file in upload order (flat view)
	Primary int

	// Summary counts: primary files with one clear system, primary files
	// that need a decision, and discarded files. Summary is the count line.
	ReadyN, DecideN, SkippedN int
	Summary                   string

	// Grouped view for many files.
	Grouped bool
	Decide  []reviewFile  // main files that need a decision; shown first and open
	Orphans []reviewFile  // companions whose main file is not on the page
	Ready   []reviewGroup // one collapsed group per system
	Skipped []reviewFile  // discarded files with their reasons
}

// reviewFlatMax is the number of main files up to which the review page
// shows one flat list, as before. Above it, the page groups the files.
const reviewFlatMax = 20

// parseIDs splits and validates the {ids} path value.
func (s *Server) parseIDs(raw string) ([]string, []*roms.Upload, bool) {
	var ids []string
	seen := map[string]bool{}
	for _, id := range strings.Split(raw, ",") {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > maxReviewIDs {
		return nil, nil, false
	}
	ups := make([]*roms.Upload, 0, len(ids))
	for _, id := range ids {
		up, ok := s.d.Stager.Get(id)
		if !ok {
			return nil, nil, false
		}
		ups = append(ups, up)
	}
	return ids, ups, true
}

// needsDecision reports a main file that the owner should look at.
func needsDecision(f reviewFile) bool {
	return len(f.Candidates) != 1 || f.Note != "" || f.Conflict
}

// decides reports a main file that needs a decision.
func decides(f reviewFile) bool { return f.Primary && needsDecision(f) }

// targetExists reports a file that is already in the ROM folder of system.
func (s *Server) targetExists(system, name string) bool {
	if system == "" || strings.ContainsAny(system, `/\`) || !filepath.IsLocal(system) || !filepath.IsLocal(filepath.FromSlash(name)) {
		return false
	}
	_, err := os.Lstat(filepath.Join(s.d.Config().Paths.Roms, system, filepath.FromSlash(name)))
	return err == nil
}

// buildReview turns the staged uploads into the review page data. Files are
// never keyed by name alone: two entries of one upload can have the same
// name (for example a discarded "already imported" file and a file from a
// ZIP), and both are shown.
func (s *Server) buildReview(ups []*roms.Upload) reviewView {
	sys := s.systemList()
	var v reviewView
	groups := map[string]int{}
	// main[i] is the index in v.Files of the main file of companion i
	main := map[int]int{}
	for _, up := range ups {
		start := len(v.Files)
		for _, f := range up.Files {
			rf := reviewFile{StagedFile: f, UploadID: up.ID, Primary: !f.Discarded && f.CompanionOf == ""}
			for _, c := range f.Candidates {
				o := sysOpt{Name: c, FullName: c}
				if x, ok := systems.ByName(sys, c); ok && x.FullName != "" {
					o.FullName = x.FullName
				}
				rf.Opts = append(rf.Opts, o)
			}
			if rf.Primary {
				v.Primary++
				if len(f.Candidates) == 1 {
					rf.Conflict = s.targetExists(f.Candidates[0], f.Name)
				}
				if needsDecision(rf) {
					v.DecideN++
				} else {
					v.ReadyN++
				}
			}
			if f.Discarded {
				v.SkippedN++
			}
			v.Files = append(v.Files, rf)
		}
		// Attach companions to their main file. Only files that are not
		// discarded can be a main file; the first one with a name wins, as
		// in the commit.
		files := v.Files[start:]
		idx := map[string]int{}
		for i, f := range files {
			if _, dup := idx[f.Name]; !dup && !f.Discarded {
				idx[f.Name] = i
			}
		}
		for ci, f := range files {
			if f.Discarded || f.CompanionOf == "" {
				continue
			}
			at, ok := -1, true
			for cur, n := f.CompanionOf, 0; ok && n < 16; n++ {
				at, ok = idx[cur]
				if ok && files[at].Primary {
					break
				}
				if ok {
					cur = files[at].CompanionOf
				}
			}
			if ok && at >= 0 && files[at].Primary {
				files[at].Companions = append(files[at].Companions, f.Name)
				main[start+ci] = start + at
			} else {
				v.Orphans = append(v.Orphans, f)
			}
		}
	}
	v.Grouped = v.Primary > reviewFlatMax
	if !v.Grouped {
		v.Orphans = nil
		v.Files = flatOrder(v.Files, main)
		v.Summary = reviewSummary(v.ReadyN, v.DecideN, v.SkippedN)
		return v
	}
	for _, f := range v.Files {
		switch {
		case f.Discarded:
			v.Skipped = append(v.Skipped, f)
		case !f.Primary:
			// shown with its main file
		case needsDecision(f):
			v.Decide = append(v.Decide, f)
		default:
			sysName := f.Candidates[0]
			i, ok := groups[sysName]
			if !ok {
				g := reviewGroup{System: sysName, FullName: sysName}
				if x, ok := systems.ByName(sys, sysName); ok && x.FullName != "" {
					g.FullName = x.FullName
				}
				i = len(v.Ready)
				groups[sysName] = i
				v.Ready = append(v.Ready, g)
			}
			v.Ready[i].Files = append(v.Ready[i].Files, f)
		}
	}
	sort.SliceStable(v.Ready, func(a, b int) bool { return v.Ready[a].FullName < v.Ready[b].FullName })
	v.Summary = reviewSummary(v.ReadyN, v.DecideN, v.SkippedN)
	return v
}

// flatOrder orders the rows of the flat view: every main file is followed
// by its companions (main maps a companion to its main file), and the main
// files that need a decision come first, with their companions. All other
// rows keep their order, so a normal upload looks as before. Companions keep
// their own rows here.
func flatOrder(files []reviewFile, main map[int]int) []reviewFile {
	after := map[int][]int{}
	for c, m := range main {
		after[m] = append(after[m], c)
	}
	var first, rest []reviewFile
	for i, f := range files {
		if _, ok := main[i]; ok {
			continue // listed after its main file
		}
		dst := &rest
		if decides(f) {
			dst = &first
		}
		f.Companions = nil
		*dst = append(*dst, f)
		cs := after[i]
		sort.Ints(cs)
		for _, c := range cs {
			cf := files[c]
			cf.Companions = nil
			*dst = append(*dst, cf)
		}
	}
	return append(first, rest...)
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	ids, ups, ok := s.parseIDs(r.PathValue("ids"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	v := s.buildReview(ups)
	v.IDs = strings.Join(ids, ",")
	if n, err := strconv.Atoi(r.URL.Query().Get("done")); err == nil && n > 0 {
		was := "games were"
		if n == 1 {
			was = "game was"
		}
		v.Notice = fmt.Sprintf("%d %s already applied; an error occurred with the next upload. The remaining files wait here.", n, was)
	}
	s.page(w, r, "upload_review", "Review upload", v)
}

func (s *Server) handleCommit(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	cfg := s.d.Config()
	ids, ups, ok := s.parseIDs(r.PathValue("ids"))
	if !ok {
		s.fail(w, r, http.StatusNotFound, "Upload not found.")
		return
	}
	choices := make([][]roms.Choice, len(ids))
	for i, up := range ups {
		for _, f := range up.Files {
			if f.Discarded || f.CompanionOf != "" {
				continue
			}
			key := func(field string) string { return field + "|" + up.ID + "|" + f.Name }
			c := roms.Choice{
				Name:        f.Name,
				System:      r.PostFormValue(key("system")),
				DisplayName: strings.TrimSpace(r.PostFormValue(key("name"))),
				Skip:        r.PostFormValue(key("skip")) != "",
			}
			if len(c.DisplayName) > maxNameLen {
				s.fail(w, r, http.StatusBadRequest, "The display name is too long.")
				return
			}
			// A file with one system needs no field (the grouped view
			// sends none for ready files). The page always sends a choice
			// for a file with more systems.
			if c.System == "" && !c.Skip {
				switch len(f.Candidates) {
				case 0:
				case 1:
					c.System = f.Candidates[0]
				default:
					s.fail(w, r, http.StatusBadRequest, "Choose a system for every file.")
					return
				}
			}
			choices[i] = append(choices[i], c)
		}
	}
	overwrite := r.PostFormValue("overwrite") != ""
	scrapeAfter := r.PostFormValue("scrape") != ""

	ctx := context.WithoutCancel(r.Context())
	if r.PostFormValue("force") == "1" {
		if err := s.stopGameAndWait(ctx); err != nil {
			code, msg := actionError(err)
			s.fail(w, r, code, msg)
			return
		}
	}
	// Validate every upload before anything is moved.
	for i, id := range ids {
		if err := s.d.Stager.Check(id, choices[i], overwrite); err != nil {
			s.logf("check upload %s: %v", id, err)
			s.fail(w, r, http.StatusUnprocessableEntity, userMessage(err))
			return
		}
	}
	var committed []roms.Committed
	var failed error
	failedAt := -1
	err := s.d.ES.WithESStopped(ctx, func() error {
		for i, id := range ids {
			c, err := s.d.Stager.Commit(id, choices[i], overwrite)
			if err != nil {
				failed, failedAt = err, i
				return err
			}
			committed = append(committed, c...)
		}
		return nil
	})
	switch {
	case err == nil:
	case errors.Is(err, es.ErrGameRunning):
		s.askForce(w, r, "Stop the running game and continue?", "Stop and continue")
		return
	case failed != nil && err == failed:
		s.logf("commit upload: %v", err)
		if failedAt > 0 && isHTMX(r) { // earlier uploads are in place: continue with the rest
			redirect(w, r, fmt.Sprintf("/upload/%s?done=%d", strings.Join(ids[failedAt:], ","), len(committed)))
			return
		}
		s.fail(w, r, http.StatusUnprocessableEntity, userMessage(err))
		return
	default:
		s.logf("commit upload: %v", err)
		code, msg := actionError(err)
		s.fail(w, r, code, msg)
		return
	}

	if len(committed) == 0 {
		redirect(w, r, "/upload")
		return
	}
	if scrapeAfter && s.d.Jobs != nil {
		by := map[string][]string{}
		for _, c := range committed {
			by[c.System] = append(by[c.System], strings.TrimPrefix(c.RelPath, "./"))
		}
		job := s.submitScrape(cfg, "Scrape: new games", by)
		redirect(w, r, "/jobs/"+job.ID)
		return
	}
	loc := fmt.Sprintf("/games?added=%d", len(committed))
	first := committed[0].System
	same := true
	for _, c := range committed {
		same = same && c.System == first
	}
	if same {
		loc = fmt.Sprintf("/games/%s?added=%d", url.PathEscape(first), len(committed))
	}
	redirect(w, r, loc)
}

func (s *Server) handleUploadDiscard(w http.ResponseWriter, r *http.Request) {
	for _, id := range strings.Split(r.PathValue("ids"), ",") {
		_ = s.d.Stager.Discard(id)
	}
	redirect(w, r, "/upload")
}

// --- jobs ----------------------------------------------------------------

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	if s.d.Jobs == nil {
		http.NotFound(w, r)
		return
	}
	j, ok := s.d.Jobs.Get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.page(w, r, "job", j.Title, struct {
		Job     jobs.Job
		LogText string
	}{j, strings.Join(j.Log, "\n")})
}

func (s *Server) handleJobEvents(w http.ResponseWriter, r *http.Request) {
	if s.d.Jobs == nil {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	if _, ok := s.d.Jobs.Get(id); !ok {
		http.NotFound(w, r)
		return
	}
	evs, unsub := s.d.Jobs.Subscribe(id)
	defer unsub()
	ch := make(chan any)
	go func() {
		defer close(ch)
		for ev := range evs {
			select {
			case ch <- ev:
			case <-r.Context().Done():
				return
			}
		}
	}()
	s.SSE(w, r, ch, func(v any) (string, error) {
		b, err := json.Marshal(v.(jobs.Event).Job)
		return string(b), err
	})
}
