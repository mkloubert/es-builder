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
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"emulationstation/webui/internal/gamelist"
)

//go:embed static templates
var assets embed.FS

// navItem is one entry of the sidebar and the phone menu.
type navItem struct {
	Key, Label, Href, Icon string
	N                      int // number shown next to the entry (1 = "01")
	Active                 bool
}

var navItems = []navItem{
	{Key: "dashboard", Label: "Dashboard", Href: "/", Icon: "layout-dashboard"},
	{Key: "games", Label: "Games", Href: "/games", Icon: "gamepad-2"},
	{Key: "upload", Label: "Upload", Href: "/upload", Icon: "upload"},
	{Key: "usb", Label: "USB", Href: "/usb", Icon: "usb"},
	{Key: "bios", Label: "BIOS", Href: "/bios", Icon: "cpu"},
	{Key: "controllers", Label: "Controllers", Href: "/controllers", Icon: "joystick"},
	{Key: "system", Label: "System", Href: "/system", Icon: "server"},
	{Key: "settings", Label: "Settings", Href: "/settings", Icon: "settings"},
	{Key: "about", Label: "About & licenses", Href: "/about", Icon: "info"},
}

// navKeyFor <page> : returns the key of the nav entry that is active on a page ("" = none).
func navKeyFor(page string) string {
	switch page {
	case "games_system":
		return "games"
	case "upload_review":
		return "upload"
	case "job":
		return ""
	}
	return page
}

// certWarnDays is the remaining validity below which the layout warns.
const certWarnDays = 30

// Page is the data passed to layout.html; page templates read .Data.
type Page struct {
	Title        string
	Nav          []navItem
	Section      string   // key of the active nav entry; "" = none
	SectionHref  string   // link of the section in the crumb on a sub page; "" = no link
	Crumb        []string // breadcrumb parts, for example ["02", "GAMES", "SUPER NINTENDO"]
	CertWarning  bool
	CertDaysLeft int
	SourceURL    string // validated http(s) source link for the footer; "" = none
	Data         any
}

// IsLastCrumb <i> : reports whether i is the index of the last crumb part (the current page).
func (p Page) IsLastCrumb(i int) bool { return i == len(p.Crumb)-1 }

// safeSourceURL returns u if it is an absolute http(s) URL, else "".
func safeSourceURL(u string) string {
	pu, err := url.Parse(strings.TrimSpace(u))
	if err != nil || pu.Host == "" || (pu.Scheme != "http" && pu.Scheme != "https") {
		return ""
	}
	return pu.String()
}

type confirmBtn struct{ Label, Action, Message, Target string }

// panelHeadData is the data of the "panel-head" partial.
type panelHeadData struct {
	Num, Title, ID, Info string
	Badge, BadgeKind     string // BadgeKind is "", "ok", "warn" or "danger"
}

// panelHead <num> <title> [key:value ...] : builds the data for the "panel-head" partial.
// Keys: "id" (id of the h2), "info" (mono text on the right), "badge", "badge-ok",
// "badge-warn", "badge-danger" (a badge on the right). An unknown key is an error.
func panelHead(num, title string, opts ...string) (panelHeadData, error) {
	d := panelHeadData{Num: num, Title: title}
	for _, o := range opts {
		k, v, ok := strings.Cut(o, ":")
		if !ok {
			return d, fmt.Errorf("panelHead: bad option %q", o)
		}
		switch k {
		case "id":
			d.ID = v
		case "info":
			d.Info = v
		case "badge", "badge-ok", "badge-warn", "badge-danger":
			d.Badge, d.BadgeKind = v, strings.TrimPrefix(strings.TrimPrefix(k, "badge"), "-")
		default:
			return d, fmt.Errorf("panelHead: unknown option %q", k)
		}
	}
	return d, nil
}

var funcs = template.FuncMap{
	"sub": func(a, b uint64) uint64 {
		if b > a {
			return 0
		}
		return a - b
	},
	"inc":            func(i int) int { return i + 1 },
	"bytes":          humanBytes,
	"icon":           icon,
	"pct":            pct,
	"gamelistBroken": gamelist.BrokenMessage,
	"panelHead":      panelHead,
	"confirmBtn": func(label, action, msg, target string) confirmBtn {
		return confirmBtn{label, action, msg, target}
	},
}

// iconNames is the set of symbol ids in the built-in static/icons.svg, read once.
var iconNames = sync.OnceValue(func() map[string]bool {
	set := map[string]bool{}
	b, err := assets.ReadFile("static/icons.svg")
	if err != nil {
		return set
	}
	for _, m := range symbolID.FindAllSubmatch(b, -1) {
		set[string(m[1])] = true
	}
	return set
})

var symbolID = regexp.MustCompile(`<symbol[^>]*\sid="([a-z0-9-]+)"`)

// icon <name> : returns the SVG tag for a sprite symbol. An unknown name is an error.
func icon(name string) (template.HTML, error) {
	if !iconNames()[name] {
		return "", fmt.Errorf("unknown icon %q", name)
	}
	return template.HTML(`<svg class="icon" aria-hidden="true"><use href="/static/icons.svg#` + name + `"></use></svg>`), nil
}

// pct <used> <total> : percent of used in total, 0 when total is 0, at most 100.
func pct(used, total uint64) int {
	if total == 0 {
		return 0
	}
	if used >= total {
		return 100
	}
	if used > math.MaxUint64/100 {
		return int(used / (total / 100))
	}
	return int(used * 100 / total)
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
	p := Page{Title: title, Data: data, Section: navKeyFor(name), Nav: make([]navItem, len(navItems))}
	copy(p.Nav, navItems)
	for i := range p.Nav {
		n := &p.Nav[i]
		n.N, n.Active = i+1, n.Key == p.Section
		if n.Active {
			p.Crumb = []string{fmt.Sprintf("%02d", n.N), strings.ToUpper(n.Label)}
			if name != n.Key {
				p.SectionHref = n.Href
			}
		}
	}
	if p.Crumb == nil || p.SectionHref != "" {
		p.Crumb = append(p.Crumb, strings.ToUpper(title))
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
