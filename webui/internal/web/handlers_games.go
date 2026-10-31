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
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"emulationstation/webui/internal/config"
	"emulationstation/webui/internal/es"
	"emulationstation/webui/internal/gamelist"
	"emulationstation/webui/internal/jobs"
	"emulationstation/webui/internal/names"
	"emulationstation/webui/internal/priv"
	"emulationstation/webui/internal/roms"
	"emulationstation/webui/internal/scrape"
	"emulationstation/webui/internal/systems"
)

// maxRows caps the rows rendered on a system page (use the search beyond).
const maxRows = 500

// maxNameLen is the longest accepted display name.
const maxNameLen = 200

type sysCard struct {
	Name, FullName string
	Count          int
}

type changeItem struct {
	Kind, System, Rel, Name string
}

type changesView struct {
	Items   []changeItem
	Count   int
	Deletes int
}

type gamesView struct {
	Cards   []sysCard
	All     bool
	Notice  string
	Changes changesView
	Scrape  bool // the system can be scraped
}

type romRow struct {
	Rel, Display     string
	Renamed, Deleted bool
}

type systemView struct {
	System  systems.System
	Rows    []romRow
	Total   int
	Q       string
	Capped  bool
	GLError bool
	GLPath  string
	Notice  string
	Changes changesView
}

type kv struct{ Name, Value string }

type forceDialog struct {
	Message, Label, Action string
	Fields                 []kv
}

func (s *Server) gamesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /games", s.handleGames)
	mux.HandleFunc("GET /games/{system}", s.handleSystem)
	mux.HandleFunc("POST /games/{system}/rename", s.handleRename)
	mux.HandleFunc("POST /games/{system}/delete", s.handleDelete)
	mux.HandleFunc("POST /games/{system}/launch", s.handleLaunch)
	mux.HandleFunc("POST /games/{system}/scrape", s.handleScrape)
	mux.HandleFunc("GET /partials/changes", s.handleChangesPartial)
	mux.HandleFunc("POST /changes/apply", s.handleApply)
	mux.HandleFunc("POST /changes/discard", s.handleDiscard)
	mux.HandleFunc("GET /jobs/{id}", s.handleJob)
	mux.HandleFunc("GET /jobs/{id}/events", s.handleJobEvents)
}

// --- listing -------------------------------------------------------------

// listable reports whether a directory entry name can be a ROM entry.
func listable(name string) bool {
	return !strings.HasPrefix(name, ".") && name != "gamelist.xml" && name != "gamelist.xml.bak"
}

// maxListDepth is how deep listROMs looks into sub folders of a system
// folder (a stick import keeps folders like "Game/Game.cue").
const maxListDepth = 4

// listROMs returns the ROM files of a system folder as slash paths below it
// (for example "Mario.sfc" or "Final Fantasy VII/FF7 (Disc 1).cue"): regular,
// visible files that are not gamelists and not companions of a descriptor
// file. Sub folders are listed too; symlinks are never followed. In a sub
// folder only files with an extension of the system count (so a folder of
// pictures does not show up as games). Like in EmulationStation, a
// descriptor file (.cue, .gdi, .m3u, .ccd) hides the files it lists only when
// the system accepts the descriptor's extension; an .m3u file also hides
// files that it lists in sub folders. It is a variable, so tests can count
// the calls.
var listROMs = func(cfg *config.Config, sys systems.System) ([]string, error) {
	l := romLister{root: filepath.Join(cfg.Paths.Roms, sys.Name), exts: sys.Extensions, hidden: map[string]bool{}}
	if err := l.dir("", 0); err != nil {
		return nil, err
	}
	var out []string
	for _, f := range l.files {
		if !l.hidden[strings.ToLower(f)] {
			out = append(out, f)
		}
	}
	return out, nil
}

// romLister collects the files of a system folder and the files that
// descriptor files list (hidden, as lower case slash paths).
type romLister struct {
	root   string
	exts   []string
	files  []string
	hidden map[string]bool
}

// dir adds the files of the folder root/rel.
func (l *romLister) dir(rel string, depth int) error {
	dir := filepath.Join(l.root, filepath.FromSlash(rel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var subs []string
	for _, e := range entries {
		if !listable(e.Name()) {
			continue
		}
		if e.IsDir() { // a symlink is not a folder here
			subs = append(subs, e.Name())
			continue
		}
		if !e.Type().IsRegular() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		accepted := len(l.exts) == 0 || slices.Contains(l.exts, ext)
		switch ext {
		case ".cue", ".gdi", ".m3u", ".ccd":
			if accepted {
				l.hide(dir, rel, e.Name(), ext == ".m3u")
			}
		}
		if rel != "" && !accepted {
			continue
		}
		l.files = append(l.files, path.Join(rel, e.Name()))
	}
	if depth >= maxListDepth {
		return nil
	}
	for _, d := range subs {
		_ = l.dir(path.Join(rel, d), depth+1) // an unreadable sub folder is left out
	}
	return nil
}

// hide marks the files that the descriptor rel/name lists. Files in the same
// folder are matched like roms.Companions (also with other upper and lower
// case). An .m3u file may list files in sub folders ("Disc/Game.cue").
func (l *romLister) hide(dir, rel, name string, subFolders bool) {
	p := filepath.Join(dir, name)
	if cs, err := roms.Companions(p); err == nil {
		for _, c := range cs {
			l.hidden[strings.ToLower(path.Join(rel, c))] = true
		}
	}
	if !subFolders {
		return
	}
	refs, err := roms.References(p)
	if err != nil {
		return
	}
	for _, ref := range refs {
		ref = strings.ReplaceAll(ref, "\\", "/")
		if !strings.Contains(ref, "/") || strings.HasPrefix(ref, "/") {
			continue
		}
		if full := path.Join(rel, ref); roms.SafeRel(full) {
			l.hidden[strings.ToLower(full)] = true
		}
	}
}

func (s *Server) changesView() changesView {
	v := changesView{}
	sys := s.systemList()
	for _, c := range s.changes.List() {
		it := changeItem{Kind: c.Kind, System: c.System, Rel: c.Rel, Name: c.Name}
		if x, ok := systems.ByName(sys, c.System); ok && x.FullName != "" {
			it.System = x.FullName
		}
		if c.Kind == "delete" {
			v.Deletes++
		}
		v.Items = append(v.Items, it)
	}
	v.Count = len(v.Items)
	return v
}

func (s *Server) systemList() []systems.System {
	if s.d.Systems == nil {
		return nil
	}
	return s.d.Systems()
}

func notice(r *http.Request) string {
	if n, err := strconv.Atoi(r.URL.Query().Get("added")); err == nil && n > 0 {
		if n == 1 {
			return "1 game was added."
		}
		return fmt.Sprintf("%d games were added.", n)
	}
	return ""
}

func (s *Server) handleGames(w http.ResponseWriter, r *http.Request) {
	cfg := s.d.Config()
	v := gamesView{All: r.URL.Query().Get("all") == "1", Notice: notice(r), Changes: s.changesView()}
	for _, sys := range s.systemList() {
		files, err := listROMs(cfg, sys)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			s.logf("list %s: %v", sys.Name, err)
		}
		if len(files) == 0 && !v.All {
			continue
		}
		name := sys.FullName
		if name == "" {
			name = sys.Name
		}
		v.Cards = append(v.Cards, sysCard{Name: sys.Name, FullName: name, Count: len(files)})
	}
	sort.Slice(v.Cards, func(i, j int) bool {
		return strings.ToLower(v.Cards[i].FullName) < strings.ToLower(v.Cards[j].FullName)
	})
	s.page(w, r, "games", "Games", v)
}

func (s *Server) handleChangesPartial(w http.ResponseWriter, r *http.Request) {
	s.partial(w, http.StatusOK, "changes_banner", s.changesView())
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	cfg := s.d.Config()
	sys, ok := systems.ByName(s.systemList(), r.PathValue("system"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if sys.FullName == "" {
		sys.FullName = sys.Name
	}
	files, err := listROMs(cfg, sys)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logf("list %s: %v", sys.Name, err)
	}
	v := systemView{System: sys, Q: strings.TrimSpace(r.URL.Query().Get("q")), Notice: notice(r), Changes: s.changesView(), Scrape: scrape.Supported(sys.Name)}
	glPath := gamelist.PathFor(cfg.Paths, sys.Name)
	gl, err := gamelist.Read(glPath)
	if err != nil {
		v.GLError, v.GLPath = true, glPath
		gl = &gamelist.List{}
	}
	q := strings.ToLower(v.Q)
	for _, f := range files {
		row := romRow{Rel: f}
		row.Display, _ = gl.Field("./"+f, "name")
		if row.Display == "" {
			row.Display = names.Clean(path.Base(f))
		}
		if ch, ok := s.changes.forRom(sys.Name, f); ok {
			if ch.Kind == "delete" {
				row.Deleted = true
			} else {
				row.Display, row.Renamed = ch.Name, true
			}
		}
		if q != "" && !strings.Contains(strings.ToLower(row.Display), q) && !strings.Contains(strings.ToLower(f), q) {
			continue
		}
		v.Rows = append(v.Rows, row)
	}
	sort.Slice(v.Rows, func(i, j int) bool {
		a, b := strings.ToLower(v.Rows[i].Display), strings.ToLower(v.Rows[j].Display)
		if a != b {
			return a < b
		}
		return v.Rows[i].Rel < v.Rows[j].Rel
	})
	v.Total = len(v.Rows)
	if len(v.Rows) > maxRows {
		v.Rows, v.Capped = v.Rows[:maxRows], true
	}
	if r.Header.Get("HX-Target") == "games-list" {
		s.block(w, "games_system", "games_list", v)
		return
	}
	s.page(w, r, "games_system", sys.FullName, v)
}

// block renders a named template of a page set (without layout).
func (s *Server) block(w http.ResponseWriter, page, name string, data any) {
	b, err := s.render(page, name, data)
	if errors.Is(err, errTemplateMissing) {
		http.Error(w, "Template missing", http.StatusInternalServerError)
		return
	}
	if err != nil {
		s.logf("render %s/%s: %v", page, name, err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

// --- request helpers -----------------------------------------------------

// fail answers an error: toast for htmx, plain text otherwise.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, code int, msg string) {
	if isHTMX(r) {
		toast(w, msg)
		w.Header().Set("HX-Reswap", "none")
		w.WriteHeader(code)
		return
	}
	http.Error(w, msg, code)
}

// ok answers success: toast (+events) for htmx, a redirect otherwise.
func (s *Server) ok(w http.ResponseWriter, r *http.Request, msg, back string, events ...string) {
	if isHTMX(r) {
		toastWith(w, msg, events...)
		w.Header().Set("HX-Reswap", "none")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// redirect sends the browser to loc (htmx or plain).
func redirect(w http.ResponseWriter, r *http.Request, loc string) {
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", loc)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, loc, http.StatusSeeOther)
}

// systemParam resolves the {system} path value.
func (s *Server) systemParam(w http.ResponseWriter, r *http.Request) (systems.System, bool) {
	sys, ok := systems.ByName(s.systemList(), r.PathValue("system"))
	if !ok {
		s.fail(w, r, http.StatusNotFound, "System not found.")
	}
	return sys, ok
}

// romParam validates a "rel" value: a file in the system folder, given as a
// slash path below it (it may be in a sub folder). Paths with "..", absolute
// paths and paths through symlinks are rejected.
func (s *Server) romParam(w http.ResponseWriter, r *http.Request, sys systems.System, rel string) bool {
	return s.romChecker(sys).check(w, r, rel)
}

// romCheck validates many "rel" values of one system. It lists the system
// folder once, at the first check that needs it.
type romCheck struct {
	s      *Server
	sys    systems.System
	dir    string
	listed bool
	files  map[string]bool
	err    error
}

func (s *Server) romChecker(sys systems.System) *romCheck {
	return &romCheck{s: s, sys: sys, dir: filepath.Join(s.d.Config().Paths.Roms, sys.Name)}
}

// check is romParam for one value; it answers the error itself.
func (c *romCheck) check(w http.ResponseWriter, r *http.Request, rel string) bool {
	if !roms.SafeRel(rel) || slices.ContainsFunc(strings.Split(rel, "/"), func(p string) bool { return !listable(p) }) {
		c.s.fail(w, r, http.StatusBadRequest, "Invalid file name.")
		return false
	}
	if roms.CheckDirs(c.dir, rel) != nil {
		c.s.fail(w, r, http.StatusBadRequest, "Invalid file name.")
		return false
	}
	fi, err := os.Lstat(filepath.Join(c.dir, filepath.FromSlash(rel)))
	if err != nil || !fi.Mode().IsRegular() {
		c.s.fail(w, r, http.StatusNotFound, "File not found.")
		return false
	}
	if !c.listed {
		c.listed = true
		var files []string
		files, c.err = listROMs(c.s.d.Config(), c.sys) // companions are not ROM entries
		c.files = make(map[string]bool, len(files))
		for _, f := range files {
			c.files[f] = true
		}
	}
	if c.err != nil || !c.files[rel] {
		c.s.fail(w, r, http.StatusBadRequest, "This file cannot be edited.")
		return false
	}
	return true
}

// askForce answers 409 with the "Stop running game" dialog, which
// re-sends the same form values plus force=1.
func (s *Server) askForce(w http.ResponseWriter, r *http.Request, message, label string) {
	d := forceDialog{Message: message, Label: label, Action: r.URL.Path}
	keys := make([]string, 0, len(r.PostForm))
	for k := range r.PostForm {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "force" {
			continue
		}
		for _, v := range r.PostForm[k] {
			d.Fields = append(d.Fields, kv{k, v})
		}
	}
	w.Header().Set("HX-Force-Dialog", "1")
	w.Header().Set("HX-Retarget", "#modal-slot")
	w.Header().Set("HX-Reswap", "innerHTML")
	s.partial(w, http.StatusConflict, "force_dialog", d)
}

// stopGameAndWait stops the running game and waits until the wrapper left the
// "game" state.
func (s *Server) stopGameAndWait(ctx context.Context) error {
	if err := s.d.ES.StopGame(); err != nil && !errors.Is(err, es.ErrNoGame) {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for s.d.ES.State().Kind == "game" {
		if time.Now().After(deadline) {
			return es.ErrGameRunning
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil
}

// --- pending changes -----------------------------------------------------

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	sys, ok := s.systemParam(w, r)
	if !ok {
		return
	}
	rel := r.PostFormValue("rel")
	if !s.romParam(w, r, sys, rel) {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" || len(name) > maxNameLen || strings.ContainsFunc(name, func(c rune) bool { return c < 0x20 || c == 0x7f }) {
		s.fail(w, r, http.StatusBadRequest, "Please enter a valid name.")
		return
	}
	s.changes.Add(Change{Kind: "rename", System: sys.Name, Rel: rel, Name: name})
	s.ok(w, r, "Rename queued.", "/games/"+url.PathEscape(sys.Name), "changes-updated")
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	sys, ok := s.systemParam(w, r)
	if !ok {
		return
	}
	rel := r.PostFormValue("rel")
	if !s.romParam(w, r, sys, rel) {
		return
	}
	s.changes.Add(Change{Kind: "delete", System: sys.Name, Rel: rel})
	s.ok(w, r, "Delete queued.", "/games/"+url.PathEscape(sys.Name), "changes-updated")
}

func (s *Server) handleDiscard(w http.ResponseWriter, r *http.Request) {
	s.changes.Clear()
	s.ok(w, r, "Changes discarded.", "/games", "changes-updated")
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	cfg := s.d.Config()
	list := s.changes.List()
	if len(list) == 0 {
		s.ok(w, r, "There are no changes.", "/games", "changes-updated")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if r.PostFormValue("force") == "1" {
		if err := s.stopGameAndWait(ctx); err != nil {
			code, msg := actionError(err)
			s.fail(w, r, code, msg)
			return
		}
	}
	err := s.d.ES.WithESStopped(ctx, func() error { return ApplyChanges(cfg, list) })
	var ue *userError
	switch {
	case err == nil:
		s.changes.Drop(list)
		s.ok(w, r, "Changes applied.", "/games", "changes-updated")
	case errors.Is(err, es.ErrGameRunning):
		s.askForce(w, r, "Stop the running game and continue?", "Stop and continue")
	case errors.As(err, &ue):
		s.logf("apply changes: %v", err)
		s.fail(w, r, http.StatusUnprocessableEntity, ue.msg)
	default:
		s.logf("apply changes: %v", err)
		code, msg := actionError(err)
		s.fail(w, r, code, msg)
	}
}

// --- launch & scrape -------------------------------------------------------

func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	sys, ok := s.systemParam(w, r)
	if !ok {
		return
	}
	rel := r.PostFormValue("rel")
	if !s.romParam(w, r, sys, rel) {
		return
	}
	abs := filepath.Join(s.d.Config().Paths.Roms, sys.Name, rel)
	err := s.d.ES.Launch(sys.Name, abs)
	if errors.Is(err, es.ErrGameRunning) {
		if r.PostFormValue("force") != "1" {
			s.askForce(w, r, "Stop the running game and start?", "Stop and start")
			return
		}
		ctx := context.WithoutCancel(r.Context())
		if err = s.stopGameAndWait(ctx); err == nil {
			err = s.d.ES.Launch(sys.Name, abs)
		}
	}
	if err != nil {
		s.logf("launch %s/%s: %v", sys.Name, rel, err)
		code, msg := actionError(err)
		s.fail(w, r, code, msg)
		return
	}
	s.ok(w, r, "Game is starting.", "/")
}

func (s *Server) runner() priv.Runner {
	if s.d.Runner != nil {
		return s.d.Runner
	}
	return priv.ExecRunner{}
}

// submitScrape queues a scrape job for the given ROMs (grouped per system).
func (s *Server) submitScrape(cfg *config.Config, title string, bySystem map[string][]string) jobs.Job {
	syss := make([]string, 0, len(bySystem))
	for k := range bySystem {
		syss = append(syss, k)
	}
	sort.Strings(syss)
	run := s.runner()
	return s.d.Jobs.Submit("scrape", title, func(ctx context.Context, rep jobs.Reporter) error {
		for i, sys := range syss {
			sub := scrapeStep{rep: rep, i: i, n: len(syss)}
			if err := scrape.Run(ctx, sub, run, s.d.ES, cfg, sys, bySystem[sys]); err != nil {
				return err
			}
		}
		return nil
	})
}

// scrapeStep scales the progress of one system into the whole job.
type scrapeStep struct {
	rep  jobs.Reporter
	i, n int
}

func (p scrapeStep) Progress(v float64) { p.rep.Progress((float64(p.i) + v) / float64(p.n)) }
func (p scrapeStep) Logf(f string, a ...any) {
	p.rep.Logf(f, a...)
}

func (s *Server) handleScrape(w http.ResponseWriter, r *http.Request) {
	sys, ok := s.systemParam(w, r)
	if !ok {
		return
	}
	if !scrape.Supported(sys.Name) {
		s.fail(w, r, http.StatusBadRequest, "Scraping is not available for this system.")
		return
	}
	_ = r.ParseForm()
	rels := []string{}
	check := s.romChecker(sys) // lists the folder once for all files
	for _, rel := range r.PostForm["rel"] {
		if rel == "" {
			continue
		}
		if !check.check(w, r, rel) {
			return
		}
		rels = append(rels, rel)
	}
	if s.d.Jobs == nil {
		s.fail(w, r, http.StatusInternalServerError, "Job queue is not available.")
		return
	}
	title := "Scrape: " + sys.FullName
	if sys.FullName == "" {
		title = "Scrape: " + sys.Name
	}
	job := s.submitScrape(s.d.Config(), title, map[string][]string{sys.Name: rels})
	redirect(w, r, "/jobs/"+job.ID)
}
