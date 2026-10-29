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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"emulationstation/webui/internal/config"
	"emulationstation/webui/internal/gamelist"
	"emulationstation/webui/internal/roms"
)

// Change is a pending modification of the ROM library.
type Change struct {
	Kind        string // "rename" | "delete"
	System, Rel string // Rel is the file name inside the system folder
	Name        string // new display name (rename)
}

// Changes holds the pending changes until they are applied or discarded.
type Changes struct {
	mu   sync.Mutex
	list []Change
}

// Add records ch. A later change to the same ROM supersedes earlier ones:
// a delete drops pending renames, a rename replaces an earlier rename.
func (c *Changes) Add(ch Change) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Change, 0, len(c.list)+1)
	for _, o := range c.list {
		if o.System == ch.System && o.Rel == ch.Rel {
			if o.Kind == "delete" {
				return // already marked for deletion
			}
			continue // superseded
		}
		out = append(out, o)
	}
	c.list = append(out, ch)
}

// List returns a copy of the pending changes.
func (c *Changes) List() []Change {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Change(nil), c.list...)
}

// Clear drops all pending changes.
func (c *Changes) Clear() {
	c.mu.Lock()
	c.list = nil
	c.mu.Unlock()
}

// Drop removes the given (already applied) changes, keeping any added since.
func (c *Changes) Drop(applied []Change) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.list[:0:0]
	for _, o := range c.list {
		done := false
		for _, a := range applied {
			if a == o {
				done = true
				break
			}
		}
		if !done {
			out = append(out, o)
		}
	}
	c.list = out
}

// forRom returns the pending change for a ROM, if any.
func (c *Changes) forRom(system, rel string) (Change, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, o := range c.list {
		if o.System == system && o.Rel == rel {
			return o, true
		}
	}
	return Change{}, false
}

// userError carries a German message that is safe to show to the user; Err
// holds the technical cause for the log.
type userError struct {
	msg string
	Err error
}

func (e *userError) Error() string { return e.msg + ": " + fmt.Sprint(e.Err) }
func (e *userError) Unwrap() error { return e.Err }

// userMessage maps an error from the library operations to a German message.
func userMessage(err error) string {
	var ue *userError
	var ce *roms.ConflictError
	var ge *roms.GamelistError
	switch {
	case isNoSpace(err):
		return msgNoSpace
	case errors.As(err, &ue):
		return ue.msg
	case errors.As(err, &ce):
		return "File(s) already exist: " + strings.Join(ce.Files, ", ") + ". To replace them, choose “Overwrite existing”."
	case errors.As(err, &ge):
		return gamelist.BrokenMessage(ge.Path)
	case errors.Is(err, roms.ErrUnsafePath):
		return "Unsafe file name."
	}
	return "The operation failed."
}

// ApplyChanges performs the changes, grouped per system. All gamelists are read
// first, so an unreadable gamelist aborts before anything has been touched.
// Deletes run before renames; renames of vanished files are skipped and all
// renames of one system share a single gamelist write. The caller must hold ES
// stopped. Errors are *userError values.
func ApplyChanges(cfg *config.Config, list []Change) error {
	bySystem := map[string][]Change{}
	for _, ch := range list {
		bySystem[ch.System] = append(bySystem[ch.System], ch)
	}
	syss := make([]string, 0, len(bySystem))
	for s := range bySystem {
		syss = append(syss, s)
	}
	sort.Strings(syss)

	for _, s := range syss {
		p := gamelist.PathFor(cfg.Paths, s)
		if _, err := gamelist.Read(p); err != nil {
			return &userError{gamelist.BrokenMessage(p), err}
		}
	}
	changed := false
	fail := func(msg string, err error) error {
		if changed {
			msg += " Changes that were already made stay in place."
		} else {
			msg += " Nothing was changed."
		}
		return &userError{msg, err}
	}
	for _, s := range syss {
		var renames []Change
		for _, ch := range bySystem[s] {
			switch ch.Kind {
			case "delete":
				if err := roms.DeleteROM(cfg, s, ch.Rel); err != nil {
					return fail("Deleting "+ch.Rel+" failed.", err)
				}
				changed = true
			case "rename":
				renames = append(renames, ch)
			default:
				return fail("Unknown change.", fmt.Errorf("kind %q", ch.Kind))
			}
		}
		if len(renames) == 0 {
			continue
		}
		glPath := gamelist.PathFor(cfg.Paths, s)
		l, err := gamelist.Read(glPath)
		if err != nil {
			return fail(gamelist.BrokenMessage(glPath), err)
		}
		n := 0
		for _, ch := range renames {
			if _, err := os.Lstat(filepath.Join(cfg.Paths.Roms, s, ch.Rel)); errors.Is(err, os.ErrNotExist) {
				continue
			}
			l.Set("./"+ch.Rel, map[string]string{"name": ch.Name})
			n++
		}
		if n == 0 {
			continue
		}
		if err := gamelist.Write(glPath, l); err != nil {
			return fail("Gamelist of "+s+" is not writable.", err)
		}
		changed = true
	}
	return nil
}
