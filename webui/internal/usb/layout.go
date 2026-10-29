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

// Package usb imports ROMs and BIOS files from a USB stick: it finds the
// stick layout, prepares an empty stick, scans the files, stages them for
// review and writes a report back to the stick.
package usb

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Layout is the folder layout found on a stick.
type Layout struct {
	Base, Roms, BIOS string
	IgnoredRetropie  bool // both folders exist; retropie/ is not used
}

const readme = `EmuStation Builder - USB stick

How to use this stick:

1. Copy your games into emulationstation/roms/<system>/.
   Example: emulationstation/roms/snes/Zelda.zip
   Sub folders are fine, for example emulationstation/roms/psx/Game/Game.cue
2. Copy BIOS files into emulationstation/bios/. The only sub folder that
   works is emulationstation/bios/dc/ (Dreamcast).
3. Plug the stick into the device. Open the web page, go to "USB" and
   click "Import".
4. When the job is done, click "Review files". Check the files and click
   "Apply".

The file emulationstation/import-report.txt shows what happened to each
file after an import.

Supported file systems: FAT32, exFAT and NTFS.

Nothing is deleted from this stick. The device only copies files from it.
It writes only this README.txt, import-report.txt and empty folders.
`

// isDir reports a real directory (not a symlink).
func isDir(p string) (bool, error) {
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return fi.IsDir(), nil
}

// linkError is the error for a link or a file where the device needs a
// folder (or a file it may overwrite). rel is the path on the stick.
func linkError(rel string) error {
	return fmt.Errorf("The stick has a link or file named %s. Remove it on your PC.", rel)
}

// realDir reports whether p is a real directory. It is an error when p
// exists but is a symlink or no directory.
func realDir(mount, p string) (bool, error) {
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !fi.IsDir() {
		return false, linkError(relTo(mount, p))
	}
	return true, nil
}

func relTo(mount, p string) string {
	r, err := filepath.Rel(mount, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}

// Detect finds the layout below mount: emulationstation/ first (BIOS folder
// bios/), else retropie/ (BIOS folder BIOS/). found is false when neither
// folder exists. A symlink or a file with one of these names is an error,
// so the device never prepares or writes through a link.
func Detect(mount string) (l Layout, found bool, err error) {
	es := filepath.Join(mount, "emulationstation")
	rp := filepath.Join(mount, "retropie")
	hasES, err := realDir(mount, es)
	if err != nil {
		return Layout{}, false, err
	}
	hasRP, rpErr := realDir(mount, rp)
	switch {
	case hasES:
		// a retropie link is never used, so it is only ignored here
		return Layout{Base: es, Roms: filepath.Join(es, "roms"), BIOS: filepath.Join(es, "bios"), IgnoredRetropie: hasRP || rpErr != nil}, true, nil
	case rpErr != nil:
		return Layout{}, false, rpErr
	case hasRP:
		return Layout{Base: rp, Roms: filepath.Join(rp, "roms"), BIOS: filepath.Join(rp, "BIOS")}, true, nil
	}
	return Layout{}, false, nil
}

// safeSegment reports a name that is safe as one folder name.
func safeSegment(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\\") && !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// mkdirNoFollow creates mount/<parts...> one folder at a time. A part that
// exists must be a real directory, not a symlink.
func mkdirNoFollow(mount string, parts ...string) error {
	p := mount
	for _, part := range parts {
		p = filepath.Join(p, part)
		ok, err := realDir(mount, p)
		if err != nil {
			return err
		}
		if ok {
			continue
		}
		if err := os.Mkdir(p, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if ok, err := realDir(mount, p); err != nil || !ok {
			return linkError(relTo(mount, p))
		}
	}
	return nil
}

// folderError is the error for a folder where the device writes a file.
// rel is the path on the stick.
func folderError(rel string) error {
	return fmt.Errorf("The stick has a folder named %s. Remove it on your PC.", rel)
}

// tmpPrefix starts the name of every temp file that writeFileNoFollow
// writes on the stick.
const tmpPrefix = ".emulationstation-tmp-"

// removeStaleTemps removes temp files (and links) of an earlier write that
// was cut off, for example when the stick was pulled out. Folders are kept.
func removeStaleTemps(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tmpPrefix) && !e.IsDir() {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// writeFileNoFollow writes (or overwrites) dir/name without following a
// symlink: an existing name must be a regular file; the data goes to a new
// temp file (O_EXCL|O_NOFOLLOW) that is renamed over the name. Renaming
// replaces a name, never a link target. Old temp files are removed first.
func writeFileNoFollow(mount, dir, name string, data []byte) error {
	target := filepath.Join(dir, name)
	if fi, err := os.Lstat(target); err == nil && fi.IsDir() {
		return folderError(relTo(mount, target))
	} else if err == nil && !fi.Mode().IsRegular() {
		return linkError(relTo(mount, target))
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	removeStaleTemps(dir)
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	tmp := filepath.Join(dir, tmpPrefix+name+"."+hex.EncodeToString(b)+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, target)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// Prepare creates emulationstation/roms/<system>/ for every system,
// emulationstation/bios/ and emulationstation/README.txt below mount.
// Unsafe system names are left out. It never follows a symlink on the
// stick: an existing link where it needs a folder or the README is an error.
func Prepare(mount string, systems []string) error {
	dirs := [][]string{{"emulationstation", "roms"}, {"emulationstation", "bios"}}
	for _, s := range systems {
		if safeSegment(s) {
			dirs = append(dirs, []string{"emulationstation", "roms", s})
		}
	}
	for _, d := range dirs {
		if err := mkdirNoFollow(mount, d...); err != nil {
			return err
		}
	}
	return writeFileNoFollow(mount, filepath.Join(mount, "emulationstation"), "README.txt", []byte(readme))
}
