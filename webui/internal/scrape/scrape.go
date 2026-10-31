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

// Package scrape fetches game metadata with Skyscraper and merges it into
// EmulationStation gamelists.
package scrape

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"emulationstation/webui/internal/config"
	"emulationstation/webui/internal/es"
	"emulationstation/webui/internal/gamelist"
	"emulationstation/webui/internal/jobs"
	"emulationstation/webui/internal/names"
	"emulationstation/webui/internal/priv"
)

// Binary is the Skyscraper executable (a variable so tests can replace it).
var Binary = "/opt/retropie/supplementary/skyscraper/Skyscraper"

// mergeFields are copied from the scraped gamelist; name is handled separately.
var mergeFields = []string{"desc", "image", "thumbnail", "marquee", "video", "releasedate", "developer", "publisher", "genre", "players", "rating"}

// unsupported are systems that ScreenScraper has no data for (spec
// 2026-10-31-c64-music-vm-systems-design.md 3.3).
var unsupported = map[string]bool{"c64music": true}

// Supported reports whether the web UI may scrape the system.
func Supported(system string) bool { return !unsupported[system] }

// GatherArgs are the arguments of the scraping (cache filling) pass.
func GatherArgs(cfg *config.Config, system string, roms []string) []string {
	args := []string{"-p", system, "-s", "screenscraper"}
	if cfg.ScreenscraperUser != "" {
		args = append(args, "-u", cfg.ScreenscraperUser+":"+cfg.ScreenscraperPass)
	}
	args = append(args, "--flags", "unattend")
	return append(args, roms...)
}

// GenerateArgs are the arguments of the gamelist generation pass.
func GenerateArgs(system, outDir, mediaDir string) []string {
	return []string{"-p", system, "-g", outDir, "-o", mediaDir, "--flags", "unattend"}
}

// normRel maps a scraped path to the "./<path>" form used in our gamelists.
// The path stays relative to the system folder romDir, so a game in a sub
// folder keeps its folder ("./Game/Game.cue"). Skyscraper writes absolute
// paths below romDir or relative "./" paths. A path that leaves romDir
// (".." or another absolute folder) falls back to its base name.
func normRel(p, romDir string) string {
	p = filepath.ToSlash(p)
	dir := strings.TrimSuffix(filepath.ToSlash(romDir), "/")
	rel := p
	switch {
	case dir != "" && strings.HasPrefix(p, dir+"/"):
		rel = p[len(dir)+1:]
	case strings.HasPrefix(p, "/"):
		return "./" + path.Base(p)
	}
	rel = path.Clean(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, "/") {
		return "./" + path.Base(p)
	}
	return "./" + rel
}

// Merge copies metadata of scraped into dst. romDir is the system folder
// (see normRel). The name is only replaced when isAutoName(rel, currentName)
// reports that it was never edited by hand.
func Merge(dst, scraped *gamelist.List, romDir string, isAutoName func(rel, name string) bool) {
	for i := range scraped.Games {
		raw := scraped.Games[i].Path()
		if raw == "" {
			continue
		}
		rel := normRel(raw, romDir)
		set := map[string]string{}
		for _, k := range mergeFields {
			if v, ok := scraped.Field(raw, k); ok && v != "" {
				set[k] = v
			}
		}
		if v, ok := scraped.Field(raw, "name"); ok && v != "" {
			cur, _ := dst.Field(rel, "name")
			if isAutoName(rel, cur) {
				set["name"] = v
			}
		}
		if len(set) > 0 {
			dst.Set(rel, set)
		}
	}
}

// lineWriter forwards complete output lines to the reporter.
type lineWriter struct {
	r   jobs.Reporter
	buf bytes.Buffer
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		i := bytes.IndexByte(w.buf.Bytes(), '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(w.buf.Next(i + 1)))
		if line != "" {
			w.r.Logf("%s", line)
		}
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	if s := strings.TrimSpace(w.buf.String()); s != "" {
		w.r.Logf("%s", s)
	}
	w.buf.Reset()
}

func runLogged(ctx context.Context, r jobs.Reporter, run priv.Runner, args []string) error {
	lw := &lineWriter{r: r}
	err := run.Run(ctx, lw, Binary, args...)
	lw.flush()
	return err
}

// Run scrapes roms (slash paths below the system's ROM folder, for example
// "Mario.sfc" or "Game/Game.cue"; empty = the whole system). The gather pass runs while ES keeps running; generating and
// writing the gamelist happens while ES is stopped.
func Run(ctx context.Context, r jobs.Reporter, run priv.Runner, esc *es.Controller, cfg *config.Config, system string, roms []string) error {
	dir := filepath.Join(cfg.Paths.Roms, system)
	abs := make([]string, len(roms))
	for i, n := range roms {
		abs[i] = filepath.Join(dir, n)
	}
	glPath := gamelist.PathFor(cfg.Paths, system)
	if _, err := gamelist.Read(glPath); err != nil { // fail before ES is stopped
		return fmt.Errorf("%s: %w", gamelist.BrokenMessage(glPath), err)
	}
	r.Logf("Loading metadata for %s …", system)
	r.Progress(0.05)
	if err := runLogged(ctx, r, run, GatherArgs(cfg, system, abs)); err != nil {
		return fmt.Errorf("Skyscraper (gather) failed: %w", err)
	}
	r.Progress(0.6)

	wanted := map[string]bool{}
	for _, n := range roms {
		wanted["./"+n] = true
	}
	isAuto := func(rel, name string) bool {
		return name == "" || name == names.Clean(filepath.Base(rel))
	}
	mediaDir := filepath.Join(cfg.Paths.ESConfigDir, "downloaded_media", system)
	err := esc.WithESStopped(ctx, func() error {
		out, err := os.MkdirTemp("", "scrape-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(out)
		if err := os.MkdirAll(mediaDir, 0o755); err != nil {
			return err
		}
		r.Logf("Creating gamelist …")
		if err := runLogged(ctx, r, run, GenerateArgs(system, out, mediaDir)); err != nil {
			return fmt.Errorf("Skyscraper (gamelist) failed: %w", err)
		}
		r.Progress(0.85)
		scraped, err := gamelist.Read(filepath.Join(out, "gamelist.xml"))
		if err != nil {
			return fmt.Errorf("created gamelist is not readable: %w", err)
		}
		if len(wanted) > 0 {
			for i := len(scraped.Games) - 1; i >= 0; i-- {
				raw := scraped.Games[i].Path()
				if !wanted[normRel(raw, dir)] {
					scraped.Games = append(scraped.Games[:i], scraped.Games[i+1:]...)
				}
			}
		}
		dst, err := gamelist.Read(glPath)
		if err != nil {
			return fmt.Errorf("%s: %w", gamelist.BrokenMessage(glPath), err)
		}
		Merge(dst, scraped, dir, isAuto)
		if err := gamelist.Write(glPath, dst); err != nil {
			return fmt.Errorf("Gamelist of %s is not writable: %w", system, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	r.Progress(1)
	r.Logf("Done: %s", system)
	return nil
}
