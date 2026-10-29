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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"emulationstation/webui/internal/config"
	"emulationstation/webui/internal/gamelist"
	"emulationstation/webui/internal/systems"
)

// Choice is the user's decision for one primary file.
type Choice struct {
	Name, System, DisplayName string
	Skip                      bool
}

// Committed describes one ROM placed into a system folder.
type Committed struct{ System, RelPath, DisplayName string }

type move struct{ src, dst string }

// root follows the CompanionOf chain to the primary file.
func root(byName map[string]*StagedFile, name string) string {
	cur := name
	for i := 0; i <= len(byName); i++ {
		f, ok := byName[cur]
		if !ok || f.CompanionOf == "" {
			return cur
		}
		cur = f.CompanionOf
	}
	return cur
}

// Commit moves the chosen files and their companions into the ROM folders and
// writes the gamelist entries. It must run while EmulationStation is stopped.
func (s *Stager) Commit(id string, choices []Choice, overwrite bool) ([]Committed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.plan(id, choices, overwrite)
	if err != nil {
		return nil, err
	}
	return s.execute(id, p)
}

// Check validates choices, gamelists and file conflicts like Commit would, but
// changes nothing.
func (s *Stager) Check(id string, choices []Choice, overwrite bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.plan(id, choices, overwrite)
	return err
}

// ConflictError reports target files that already exist.
type ConflictError struct{ Files []string }

func (e *ConflictError) Error() string {
	return "File(s) already exist: " + strings.Join(e.Files, ", ")
}

// GamelistError reports an unreadable gamelist.
type GamelistError struct {
	System string
	Path   string
	Err    error
}

func (e *GamelistError) Error() string {
	return fmt.Sprintf("Gamelist of %s is not readable: %v", e.System, e.Err)
}
func (e *GamelistError) Unwrap() error { return e.Err }

type commitPlan struct {
	stageDir  string
	moves     []move
	committed []Committed
	lists     map[string]*gamelist.List
	listPath  map[string]string
}

// plan prepares a commit without touching the file system. s.mu must be held.
func (s *Stager) plan(id string, choices []Choice, overwrite bool) (*commitPlan, error) {
	up, ok := s.uploads[id]
	if !ok {
		return nil, errors.New("Upload not found")
	}
	stageDir := filepath.Join(s.cfg.Paths.Staging, id)

	byName := map[string]*StagedFile{}
	for i := range up.Files {
		f := &up.Files[i]
		if _, dup := byName[f.Name]; !dup && !f.Discarded {
			byName[f.Name] = f
		}
	}

	type item struct {
		file    *StagedFile
		system  string
		display string
	}
	chosen := map[string]string{} // primary -> system
	var items []item
	for _, c := range choices {
		f, ok := byName[c.Name]
		if !ok || f.CompanionOf != "" {
			return nil, fmt.Errorf("File %q is not a selectable main file", c.Name)
		}
		if c.Skip {
			continue
		}
		if _, dup := chosen[c.Name]; dup {
			return nil, fmt.Errorf("File %q selected more than once", c.Name)
		}
		if _, ok := systems.ByName(s.systems, c.System); !ok || !contains(f.Candidates, c.System) {
			return nil, fmt.Errorf("System %q is not possible for %q", c.System, c.Name)
		}
		if !isSafeSegment(c.System) {
			return nil, ErrUnsafePath
		}
		disp := strings.TrimSpace(c.DisplayName)
		if disp == "" {
			disp = f.SuggestedName
		}
		chosen[c.Name] = c.System
		items = append(items, item{f, c.System, disp})
	}

	// plan moves: primaries and their companions
	var moves []move
	var committed []Committed
	lists := map[string]*gamelist.List{}
	listPath := map[string]string{}
	for _, it := range items {
		if _, ok := lists[it.system]; !ok {
			p := gamelist.PathFor(s.cfg.Paths, it.system)
			l, err := gamelist.Read(p)
			if err != nil {
				return nil, &GamelistError{it.system, p, err}
			}
			lists[it.system], listPath[it.system] = l, p
		}
	}
	for _, it := range items {
		dir := filepath.Join(s.cfg.Paths.Roms, it.system)
		moves = append(moves, move{filepath.Join(stageDir, it.file.Name), filepath.Join(dir, it.file.Name)})
		for i := range up.Files {
			c := &up.Files[i]
			if c.Discarded || c.CompanionOf == "" || byName[c.Name] != c {
				continue
			}
			if root(byName, c.Name) == it.file.Name {
				moves = append(moves, move{filepath.Join(stageDir, c.Name), filepath.Join(dir, c.Name)})
			}
		}
		committed = append(committed, Committed{it.system, "./" + it.file.Name, it.display})
	}

	var conflicts []string
	for _, m := range moves {
		if _, err := os.Lstat(m.dst); err == nil {
			conflicts = append(conflicts, filepath.Base(m.dst))
		}
	}
	if len(conflicts) > 0 && !overwrite {
		return nil, &ConflictError{conflicts}
	}
	return &commitPlan{stageDir, moves, committed, lists, listPath}, nil
}

// execute performs a plan. s.mu must be held.
func (s *Stager) execute(id string, p *commitPlan) ([]Committed, error) {
	moves, committed, lists, listPath, stageDir := p.moves, p.committed, p.lists, p.listPath, p.stageDir

	for _, m := range moves {
		if err := os.MkdirAll(filepath.Dir(m.dst), 0o755); err != nil {
			return nil, err
		}
		if err := moveFile(m.src, m.dst); err != nil {
			return nil, fmt.Errorf("Moving %s failed: %w", filepath.Base(m.src), err)
		}
	}

	for _, c := range committed {
		lists[c.System].Set(c.RelPath, map[string]string{"name": c.DisplayName})
	}
	for sys, l := range lists {
		if err := gamelist.Write(listPath[sys], l); err != nil {
			return committed, fmt.Errorf("Gamelist of %s is not writable: %w", sys, err)
		}
	}
	delete(s.uploads, id)
	os.RemoveAll(stageDir)
	return committed, nil
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func isSafeSegment(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`) && !strings.ContainsRune(s, 0)
}

// moveFile renames src to dst, copying across devices.
func moveFile(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	var le *os.LinkError
	if !errors.As(err, &le) || !errors.Is(le.Err, syscall.EXDEV) {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".upload-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return err
	}
	return os.Remove(src)
}

// SafeRel reports whether rel is a clean relative slash path (for example
// "Game/Game.cue") whose parts are all safe file names. It rejects "..",
// "." and absolute paths.
func SafeRel(rel string) bool {
	return safeRel(rel)
}

// CheckDirs returns ErrUnsafePath when a folder on the way from base to the
// slash path rel is not a real folder (for example a symlink that points out
// of base). A missing folder is not an error.
func CheckDirs(base, rel string) error {
	parts := strings.Split(rel, "/")
	cur := base
	for _, part := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			return ErrUnsafePath
		}
	}
	return nil
}

// DeleteROM removes a ROM, its companion files and its gamelist entry. A
// sub folder that is empty afterwards is removed too.
func DeleteROM(cfg *config.Config, system, rel string) error {
	if !isSafeSegment(system) {
		return ErrUnsafePath
	}
	clean := strings.TrimPrefix(filepath.ToSlash(rel), "./")
	if clean == "" || !filepath.IsLocal(filepath.FromSlash(clean)) {
		return ErrUnsafePath
	}
	for _, r := range clean {
		if r < 0x20 || r == 0x7f {
			return ErrUnsafePath
		}
	}
	clean = filepath.ToSlash(filepath.Clean(filepath.FromSlash(clean)))
	dir := filepath.Join(cfg.Paths.Roms, system)
	target := filepath.Join(dir, filepath.FromSlash(clean))
	if err := CheckDirs(dir, clean); err != nil {
		return err
	}

	glPath := gamelist.PathFor(cfg.Paths, system)
	list, err := gamelist.Read(glPath)
	if err != nil {
		return &GamelistError{system, glPath, err}
	}
	if fi, err := os.Lstat(target); err == nil && fi.IsDir() {
		return ErrUnsafePath
	}

	var victims []string
	if isDescriptor(target) {
		cs, err := allCompanions(target)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for _, c := range cs {
			victims = append(victims, filepath.Join(filepath.Dir(target), c))
		}
	}
	victims = append(victims, target)
	for _, v := range victims {
		if err := os.Remove(v); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// A game in a sub folder: remove the folders that are empty now.
	for d := filepath.Dir(target); d != dir && strings.HasPrefix(d, dir+string(filepath.Separator)); d = filepath.Dir(d) {
		if os.Remove(d) != nil {
			break // not empty (or not removable): keep it
		}
	}
	if list.Remove("./" + clean) {
		return gamelist.Write(glPath, list)
	}
	return nil
}
