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
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"emulationstation/webui/internal/gamelist"
)

//go:embed static templates
var assets embed.FS

// navItem is one entry of the sidebar / tab bar.
type navItem struct {
	Key, Label, Href, Icon string
	Active                 bool
}

var navItems = []navItem{
	{Key: "dashboard", Label: "Dashboard", Href: "/", Icon: "M3 11l9-8 9 8M5 10v10h5v-6h4v6h5V10"},
	{Key: "games", Label: "Games", Href: "/games", Icon: "M6 12h4m-2-2v4m6-1h.01m2-2h.01M7 7h10a4 4 0 014 4v2a4 4 0 01-4 4l-2-2H9l-2 2a4 4 0 01-4-4v-2a4 4 0 014-4z"},
	{Key: "upload", Label: "Upload", Href: "/upload", Icon: "M12 16V4m0 0l-4 4m4-4l4 4M4 16v3a1 1 0 001 1h14a1 1 0 001-1v-3"},
	{Key: "usb", Label: "USB", Href: "/usb", Icon: "M12 3v13m0 0a2.5 2.5 0 100 5 2.5 2.5 0 000-5zM12 7l-3 3v2M12 9l3 2v2M8 12h2v2H8zM14 13h2v2h-2z"},
	{Key: "bios", Label: "BIOS", Href: "/bios", Icon: "M9 3v2m6-2v2M9 19v2m6-2v2M3 9h2m-2 6h2m14-6h2m-2 6h2M7 5h10a2 2 0 012 2v10a2 2 0 01-2 2H7a2 2 0 01-2-2V7a2 2 0 012-2zm2 4h6v6H9z"},
	{Key: "controllers", Label: "Controller", Href: "/controllers", Icon: "M12 3a2 2 0 100 4 2 2 0 000-4zm0 4v6M5 15h14a1 1 0 011 1v3a1 1 0 01-1 1H5a1 1 0 01-1-1v-3a1 1 0 011-1z"},
	{Key: "system", Label: "System", Href: "/system", Icon: "M4 5h16v5H4zM4 14h16v5H4zM8 7.5h.01M8 16.5h.01"},
	{Key: "settings", Label: "Settings", Href: "/settings", Icon: "M4 6h10m4 0h2M4 12h4m4 0h8M4 18h12m4 0h0M16 4v4M10 10v4M18 16v4"},
}

// certWarnDays is the remaining validity below which the layout warns.
const certWarnDays = 30

// Page is the data passed to layout.html; page templates read .Data.
type Page struct {
	Title        string
	Nav          []navItem
	CertWarning  bool
	CertDaysLeft int
	SourceURL    string // validated http(s) source link for the footer; "" = none
	Data         any
}

// safeSourceURL returns u if it is an absolute http(s) URL, else "".
func safeSourceURL(u string) string {
	pu, err := url.Parse(strings.TrimSpace(u))
	if err != nil || pu.Host == "" || (pu.Scheme != "http" && pu.Scheme != "https") {
		return ""
	}
	return pu.String()
}

type confirmBtn struct{ Label, Action, Message, Target string }

var funcs = template.FuncMap{
	"sub": func(a, b uint64) uint64 {
		if b > a {
			return 0
		}
		return a - b
	},
	"bytes":          humanBytes,
	"gamelistBroken": gamelist.BrokenMessage,
	"confirmBtn": func(label, action, msg, target string) confirmBtn {
		return confirmBtn{label, action, msg, target}
	},
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// templates holds one parsed set per page plus a set of bare partials.
type templates struct {
	pages    map[string]*template.Template
	partials *template.Template
	builtin  *templates // the built-in set when this set has custom files, else nil
}

var errTemplateMissing = errors.New("template missing")

// render runs template name of the page set page ("" = the partials) into a buffer. If a set
// with custom files fails, it logs the error and uses the built-in set, so a custom template
// that breaks only at run time never locks the owner out (for example of the reload button).
func (s *Server) render(page, name string, data any) ([]byte, error) {
	t := s.tpl.Load()
	run := func(t *templates) ([]byte, error) {
		set := t.partials
		if page != "" {
			set = t.pages[page]
		}
		if set == nil {
			return nil, errTemplateMissing
		}
		var buf bytes.Buffer
		err := set.ExecuteTemplate(&buf, name, data)
		return buf.Bytes(), err
	}
	b, err := run(t)
	if err != nil && t.builtin != nil {
		s.logf("custom UI: render %s/%s: %v; using built-in templates", page, name, err)
		b, err = run(t.builtin)
	}
	return b, err
}

// parseTemplates parses everything once; page sets contain layout + partials + the page.
func parseTemplates(fsys fs.FS) (*templates, error) {
	partials, err := fs.Glob(fsys, "templates/partials/*.html")
	if err != nil {
		return nil, err
	}
	base := func() (*template.Template, error) {
		return template.New("").Funcs(funcs).ParseFS(fsys, append([]string{"templates/layout.html"}, partials...)...)
	}
	t := &templates{pages: map[string]*template.Template{}}
	if t.partials, err = template.New("").Funcs(funcs).ParseFS(fsys, partials...); err != nil {
		return nil, err
	}
	files, err := fs.Glob(fsys, "templates/*.html")
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if path.Base(f) == "layout.html" {
			continue
		}
		b, err := base()
		if err != nil {
			return nil, err
		}
		if _, err := b.ParseFS(fsys, f); err != nil {
			return nil, err
		}
		t.pages[strings.TrimSuffix(path.Base(f), ".html")] = b
	}
	return t, nil
}

// uiState describes the custom web UI files of the loaded templates.
type uiState struct {
	Dir   string // custom folder in use; "" = off (no UI_DIR or no folder)
	Files int    // regular files below templates/ and static/
	Err   string // first line of the parse error; the built-in templates are in use
}

// uiDir returns the configured custom folder, or "" when it is off.
func (s *Server) uiDir() string {
	if cfg := s.d.Config(); cfg != nil {
		return cfg.UIDir
	}
	return ""
}

// loadTemplates parses the templates with the custom folder over the built-in files and
// swaps them in. If the custom files do not parse, it uses the built-in files only.
// It panics if the built-in templates are broken (a build-time bug).
func (s *Server) loadTemplates() uiState {
	var st uiState
	dir := s.uiDir()
	if fi, err := os.Stat(dir); dir != "" && err == nil && fi.IsDir() {
		st = uiState{Dir: dir, Files: countCustomFiles(dir)}
	}
	builtin, err := parseTemplates(assets)
	if err != nil {
		panic("web: parse templates: " + err.Error())
	}
	tpl := builtin
	if st.Dir != "" {
		if custom, err := parseTemplates(newOverlayFS(st.Dir, assets)); err != nil {
			st.Err, _, _ = strings.Cut(err.Error(), "\n")
			s.logf("custom UI: %s; using built-in templates", st.Err)
		} else {
			custom.builtin = builtin
			tpl = custom
		}
	}
	s.tpl.Store(tpl)
	s.ui.Store(&st)
	return st
}

// page renders a full page inside the layout.
func (s *Server) page(w http.ResponseWriter, r *http.Request, name, title string, data any) {
	if _, ok := s.tpl.Load().pages[name]; !ok {
		http.Error(w, "Template missing", http.StatusInternalServerError)
		return
	}
	p := Page{Title: title, Data: data, Nav: make([]navItem, len(navItems))}
	copy(p.Nav, navItems)
	for i := range p.Nav {
		p.Nav[i].Active = p.Nav[i].Key == name
	}
	if cfg := s.d.Config(); cfg != nil {
		p.SourceURL = safeSourceURL(cfg.SourceURL)
	}
	if info, _ := s.d.Sysinfo.Collect(s.now()); info.HasCert && info.CertDaysLeft < certWarnDays {
		p.CertWarning, p.CertDaysLeft = true, info.CertDaysLeft
	}
	b, err := s.render(name, "layout", p)
	if err != nil {
		s.logf("render %s: %v", name, err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

// partial renders a named partial template.
func (s *Server) partial(w http.ResponseWriter, status int, name string, data any) {
	b, err := s.render("", name, data)
	if err != nil {
		s.logf("render partial %s: %v", name, err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// toast sets an HX-Trigger header showing msg as a toast. Non-ASCII runes are
// escaped because header bytes are decoded as Latin-1 by browsers.
func toast(w http.ResponseWriter, msg string) { toastWith(w, msg) }

// toastWith is toast plus additional (payload-less) events to trigger.
func toastWith(w http.ResponseWriter, msg string, events ...string) {
	m := map[string]string{"toast": msg}
	for _, e := range events {
		m[e] = ""
	}
	b, _ := json.Marshal(m)
	var sb strings.Builder
	for _, r := range string(b) {
		if r < utf8.RuneSelf {
			sb.WriteRune(r)
		} else if r > 0xFFFF {
			r -= 0x10000
			fmt.Fprintf(&sb, `\u%04x\u%04x`, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
		} else {
			fmt.Fprintf(&sb, `\u%04x`, r)
		}
	}
	w.Header().Set("HX-Trigger", sb.String())
}
