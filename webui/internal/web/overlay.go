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
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
)

// overlayFS reads a file from the custom folder dir first and falls back to base.
// The folder is opened on every call through os.Root, so it may appear or go away
// while the service runs, and no name or symlink can leave it.
type overlayFS struct {
	dir  string
	base fs.FS
}

// newOverlayFS <dir> <base> : the custom web UI files in dir over base; dir "" gives base.
func newOverlayFS(dir string, base fs.FS) fs.FS {
	if dir == "" {
		return base
	}
	return &overlayFS{dir: dir, base: base}
}

// hiddenName reports whether an element of name starts with a dot.
func hiddenName(name string) bool {
	for _, el := range strings.Split(name, "/") {
		if strings.HasPrefix(el, ".") && el != "." {
			return true
		}
	}
	return false
}

// rootFile closes the os.Root together with the file. It keeps *os.File, so http.FileServerFS
// can seek (Range requests, content sniffing).
type rootFile struct {
	*os.File
	root *os.Root
}

func (f *rootFile) Close() error {
	err := f.File.Close()
	_ = f.root.Close()
	return err
}

// openCustom <name> : the regular file name from the custom folder, if there is one.
func (o *overlayFS) openCustom(name string) (fs.File, bool) {
	if hiddenName(name) {
		return nil, false
	}
	if st, err := fs.Stat(o.base, name); err == nil && st.IsDir() {
		return nil, false // a folder in base stays a folder
	}
	root, err := os.OpenRoot(o.dir)
	if err != nil {
		return nil, false
	}
	f, err := root.Open(name)
	if err != nil {
		_ = root.Close()
		return nil, false
	}
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		_ = root.Close()
		return nil, false
	}
	return &rootFile{File: f, root: root}, true
}

// Open implements fs.FS.
func (o *overlayFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if f, ok := o.openCustom(name); ok {
		return f, nil
	}
	return o.base.Open(name)
}

// customEntries <dir> <name> : the regular files and folders in the custom folder name;
// symlinks inside dir count as their target, dot names are skipped.
func customEntries(dir, name string) ([]fs.DirEntry, error) {
	if dir == "" || hiddenName(name) {
		return nil, fs.ErrNotExist
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	ents, err := fs.ReadDir(root.FS(), name)
	if err != nil {
		return nil, err
	}
	out := ents[:0]
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		st, err := root.Stat(path.Join(name, e.Name()))
		if err != nil || !(st.Mode().IsRegular() || st.IsDir()) {
			continue
		}
		out = append(out, fs.FileInfoToDirEntry(st))
	}
	return out, nil
}

// ReadDir implements fs.ReadDirFS: the entries of both sides, sorted by name.
// A name that is a folder on one side and a file on the other keeps the base entry.
func (o *overlayFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	baseEnts, baseErr := fs.ReadDir(o.base, name)
	custom, customErr := customEntries(o.dir, name)
	if baseErr != nil && customErr != nil {
		return nil, baseErr
	}
	byName := map[string]fs.DirEntry{}
	for _, e := range baseEnts {
		byName[e.Name()] = e
	}
	for _, e := range custom {
		if b, ok := byName[e.Name()]; ok && b.IsDir() != e.IsDir() {
			continue
		}
		byName[e.Name()] = e
	}
	out := make([]fs.DirEntry, 0, len(byName))
	for _, e := range byName {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return out, nil
}

// countCustomFiles <dir> : the number of regular files below templates/ and static/ in dir.
func countCustomFiles(dir string) int {
	var walk func(name string) int
	walk = func(name string) int {
		ents, err := customEntries(dir, name)
		if err != nil {
			return 0
		}
		n := 0
		for _, e := range ents {
			if e.IsDir() {
				n += walk(path.Join(name, e.Name()))
			} else {
				n++
			}
		}
		return n
	}
	return walk("templates") + walk("static")
}
