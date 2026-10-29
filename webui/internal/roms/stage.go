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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"emulationstation/webui/internal/config"
	"emulationstation/webui/internal/names"
	"emulationstation/webui/internal/systems"
)

const (
	reasonUnknown = "unknown file type"
	reasonDup     = "duplicate file name"
)

// genericExts are too ambiguous to prove a ZIP holds an unpackable ROM.
var genericExts = map[string]bool{".bin": true, ".rom": true, ".img": true, ".dat": true, ".raw": true, ".iso": true}

// StagedFile is one file of an upload awaiting a decision.
type StagedFile struct {
	Name          string
	Candidates    []string
	SuggestedName string
	CompanionOf   string // empty = primary file
	Discarded     bool
	Reason        string
	Note          string // extra info for the report and the review page
	Source        string // StageTree only: path of the source file below the source folder
}

// Upload is a staged upload.
type Upload struct {
	ID    string
	Files []StagedFile
}

// Stager manages staged uploads under cfg.Paths.Staging.
type Stager struct {
	cfg     *config.Config
	systems []systems.System
	mu      sync.Mutex
	uploads map[string]*Upload

	// Test hooks, see SetTestHooks. Nil means the normal functions.
	open   func(path string) (io.ReadCloser, error)
	create func(path string) (io.WriteCloser, error)
}

// TestHooks replace how StageTree opens source files and creates files in
// the staging folder. They exist only for tests (to inject read and write
// errors).
type TestHooks struct {
	Open   func(path string) (io.ReadCloser, error)
	Create func(path string) (io.WriteCloser, error)
}

// SetTestHooks sets the test hooks. Call it before the first use. Only tests
// call it.
func (s *Stager) SetTestHooks(h TestHooks) {
	s.open, s.create = h.Open, h.Create
}

// OpenSourceFile opens a source file of an import (see OpenSource).
func (s *Stager) OpenSourceFile(path string) (io.ReadCloser, error) {
	if s.open != nil {
		return s.open(path)
	}
	return OpenSource(path)
}

// createFile creates a new file in the staging folder (it must not exist).
func createFile(path string) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
}

func (s *Stager) createFn() func(string) (io.WriteCloser, error) {
	if s.create != nil {
		return s.create
	}
	return createFile
}

// NewStager creates a Stager.
func NewStager(cfg *config.Config, sys []systems.System) *Stager {
	return &Stager{cfg: cfg, systems: sys, uploads: map[string]*Upload{}}
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (u *Upload) clone() *Upload {
	c := &Upload{ID: u.ID, Files: make([]StagedFile, len(u.Files))}
	for i, f := range u.Files {
		f.Candidates = append([]string(nil), f.Candidates...)
		c.Files[i] = f
	}
	return c
}

func (s *Stager) candidateNames(name string, keepOnly bool) []string {
	out := []string{}
	for _, c := range systems.Candidates(s.systems, name) {
		if keepOnly && !c.KeepArchives {
			continue
		}
		out = append(out, c.Name)
	}
	return out
}

// Receive stages the upload read from r under the given file name.
func (s *Stager) Receive(r io.Reader, filename string) (*Upload, error) {
	safe, err := SafeName(filename)
	if err != nil {
		return nil, err
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.cfg.Paths.Staging, id)
	part := dir + ".part"
	cleanup := func() {
		os.RemoveAll(dir)
		os.Remove(part)
	}
	up, err := s.receive(r, safe, id, dir, part)
	if err != nil {
		cleanup()
		return nil, err
	}
	os.Remove(part)
	s.mu.Lock()
	s.uploads[id] = up
	s.mu.Unlock()
	return up.clone(), nil
}

func (s *Stager) receive(r io.Reader, safe, id, dir, part string) (*Upload, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	isZip := strings.EqualFold(filepath.Ext(safe), ".zip")
	target := filepath.Join(dir, safe)
	if isZip {
		target = part
	}
	if err := s.stream(r, target); err != nil {
		return nil, err
	}
	up := &Upload{ID: id}
	if !isZip {
		up.Files = []StagedFile{s.plain(safe)}
		return up, nil
	}

	unpack, err := s.shouldUnpack(part)
	if err != nil {
		return nil, err
	}
	if !unpack {
		if err := os.Rename(part, filepath.Join(dir, safe)); err != nil {
			return nil, err
		}
		up.Files = []StagedFile{s.plain(safe)}
		return up, nil
	}
	kept, dups, err := extract(createFile, part, dir, s.cfg.MaxExtractBytes, s.cfg.MaxZipEntries)
	if err != nil {
		return nil, err
	}
	up.Files = s.classify(dir, kept, dups)
	return up, nil
}

func (s *Stager) stream(r io.Reader, target string) error {
	return writeNew(createFile, r, target, s.cfg.MaxUploadBytes)
}

// writeNew creates target with create (it must not exist yet) and copies r
// into it. A limit below 0 means no limit.
func writeNew(create func(string) (io.WriteCloser, error), r io.Reader, target string, limit int64) error {
	out, err := create(target)
	if err != nil {
		return err
	}
	if limit >= 0 {
		r = io.LimitReader(r, limit+1)
	}
	n, err := io.Copy(out, r)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && limit >= 0 && n > limit {
		err = ErrUploadTooLarge
	}
	if err != nil {
		// the file is ours (O_EXCL); do not leave a part of it
		os.Remove(target)
	}
	return err
}

// isGamelistFile reports ES's gamelist.xml (any case, any folder); an upload
// must never treat it as ROM or companion.
func isGamelistFile(name string) bool {
	return strings.EqualFold(filepath.Base(filepath.FromSlash(name)), "gamelist.xml")
}

func (s *Stager) plain(name string) StagedFile {
	f := StagedFile{Name: name, SuggestedName: names.Clean(path.Base(name))}
	if !isGamelistFile(name) {
		f.Candidates = s.candidateNames(name, false)
	}
	if len(f.Candidates) == 0 {
		f.Discarded, f.Reason = true, reasonUnknown
	}
	return f
}

func (s *Stager) shouldUnpack(zipPath string) (bool, error) {
	names, err := zipEntryNames(zipPath)
	if err != nil {
		return false, err
	}
	return s.holdsROMs(names), nil
}

// holdsROMs reports whether a ZIP with these entry names holds a ROM of a
// system that does not keep archives; such a ZIP is unpacked.
func (s *Stager) holdsROMs(names []string) bool {
	for _, n := range names {
		if strings.EqualFold(filepath.Ext(n), ".zip") {
			continue
		}
		if genericExts[strings.ToLower(filepath.Ext(n))] {
			continue
		}
		for _, c := range systems.Candidates(s.systems, n) {
			if !c.KeepArchives {
				return true
			}
		}
	}
	return false
}

// classify marks primaries, companions and discarded files of an extraction.
// Names are slash paths below dir; a companion must be in the same folder as
// its descriptor file.
func (s *Stager) classify(dir string, kept, dups []string) []StagedFile {
	files := make([]StagedFile, 0, len(kept)+len(dups))
	idx := map[string]int{}
	for _, n := range kept {
		idx[n] = len(files)
		files = append(files, s.plain(n))
	}
	// companions of every descriptor file that is itself a ROM candidate
	for _, n := range kept {
		if len(files[idx[n]].Candidates) == 0 || !isDescriptor(n) {
			continue
		}
		cs, err := Companions(filepath.Join(dir, filepath.FromSlash(n)))
		if err != nil {
			continue
		}
		for _, c := range cs {
			c = path.Join(path.Dir(n), c)
			i, ok := idx[c]
			if !ok || isGamelistFile(c) || files[i].CompanionOf != "" || c == n || s.reaches(files, idx, n, c) {
				continue
			}
			files[i].CompanionOf = n
			files[i].Discarded, files[i].Reason = false, ""
		}
	}
	for _, d := range dups {
		files = append(files, StagedFile{Name: d, SuggestedName: names.Clean(d), Discarded: true, Reason: reasonDup})
	}
	return files
}

// reaches reports whether walking up the CompanionOf chain from start hits target.
func (s *Stager) reaches(files []StagedFile, idx map[string]int, start, target string) bool {
	cur := start
	for i := 0; i < len(files)+1; i++ {
		if cur == target {
			return true
		}
		j, ok := idx[cur]
		if !ok || files[j].CompanionOf == "" {
			return false
		}
		cur = files[j].CompanionOf
	}
	return true
}

// Get returns a copy of the staged upload.
func (s *Stager) Get(id string) (*Upload, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.uploads[id]
	if !ok {
		return nil, false
	}
	return u.clone(), true
}

// Discard removes a staged upload.
func (s *Stager) Discard(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.uploads[id]; !ok {
		return errors.New("Upload not found")
	}
	delete(s.uploads, id)
	return os.RemoveAll(filepath.Join(s.cfg.Paths.Staging, id))
}

// CleanAll empties the staging directory (service start).
func (s *Stager) CleanAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploads = map[string]*Upload{}
	entries, err := os.ReadDir(s.cfg.Paths.Staging)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(s.cfg.Paths.Staging, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
