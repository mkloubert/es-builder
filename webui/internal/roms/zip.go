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

// Package roms handles upload staging, safe ZIP extraction, disc companion
// files, committing into the ROM folders and deleting ROMs.
package roms

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

var (
	ErrUnsafePath     = errors.New("unsafe file name")
	ErrTooLarge       = errors.New("unpacked data exceeds the limit")
	ErrTooManyEntries = errors.New("too many entries in the archive")
	ErrUploadTooLarge = errors.New("upload exceeds the limit")
	// ErrNameTooLong is a name that the SD card cannot store: the name of
	// a file that is created is longer than maxNameBytes, or the file
	// system refuses the name (ENAMETOOLONG, EINVAL, EILSEQ).
	ErrNameTooLong = errors.New("file name too long")
)

// maxNameBytes is the longest file name (one path part) that ext4 stores.
const maxNameBytes = 255

// SafeName returns the base name of name; empty, "." and ".." names and
// names with control characters are rejected. Umlauts and spaces are kept.
func SafeName(name string) (string, error) {
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", ErrUnsafePath
		}
	}
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	if base == "" || base == "." || base == ".." || base == "/" {
		return "", ErrUnsafePath
	}
	return base, nil
}

// checkEntryName rejects absolute paths, drive letters and ".." components,
// and a base name over maxNameBytes. Folder parts may be longer: unpacking
// keeps only the base name.
func checkEntryName(name string) error {
	n := strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(n, "/") || (len(n) > 1 && n[1] == ':') {
		return ErrUnsafePath
	}
	for _, r := range n {
		if r < 0x20 || r == 0x7f {
			return ErrUnsafePath
		}
	}
	parts := strings.Split(n, "/")
	for _, part := range parts {
		if part == ".." {
			return ErrUnsafePath
		}
	}
	if len(parts[len(parts)-1]) > maxNameBytes {
		return ErrNameTooLong
	}
	return nil
}

// nameTooLong reports a slash path with a part over maxNameBytes (every
// part is created on the SD card).
func nameTooLong(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if len(part) > maxNameBytes {
			return true
		}
	}
	return false
}

// createError marks an error of creating an unpacked file whose name the
// file system refuses (see ErrNameTooLong). It comes from the entry name,
// not from the SD card.
func createError(err error) error {
	if errors.Is(err, syscall.ENAMETOOLONG) || errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.EILSEQ) {
		return fmt.Errorf("%w: %v", ErrNameTooLong, err)
	}
	return err
}

func isJunk(name string) bool {
	n := strings.ReplaceAll(name, "\\", "/")
	return n == "__MACOSX" || strings.HasPrefix(n, "__MACOSX/")
}

// checkZipFiles checks a ZIP directory against the limits before anything
// is unpacked: the number of entries, the declared unpacked size, unsafe
// names and symlinks.
func checkZipFiles(files []*zip.File, maxBytes int64, maxEntries int) error {
	if len(files) > maxEntries {
		return ErrTooManyEntries
	}
	var declared uint64
	for _, f := range files {
		if err := checkEntryName(f.Name); err != nil {
			return err
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return ErrUnsafePath
		}
		declared += f.UncompressedSize64
		if declared > uint64(maxBytes) {
			return ErrTooLarge
		}
	}
	return nil
}

// zipEntry is a file that unpacking a ZIP keeps: its base name and its
// declared unpacked size.
type zipEntry struct {
	name string
	size int64
}

// keptEntries lists the files that extract keeps, in the same way: folders
// and __MACOSX are left out, names become base names, and of two entries
// with the same base name only the first is kept.
func keptEntries(files []*zip.File) ([]zipEntry, error) {
	seen := map[string]bool{}
	var out []zipEntry
	for _, f := range files {
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") || isJunk(f.Name) {
			continue
		}
		base, err := SafeName(f.Name)
		if err != nil {
			return nil, err
		}
		if seen[base] {
			continue
		}
		seen[base] = true
		out = append(out, zipEntry{name: base, size: int64(min(f.UncompressedSize64, 1<<62))})
	}
	return out, nil
}

// extract unpacks flat into dest. It returns the kept names and the names of
// later entries whose base name was already taken. On error everything it
// wrote is removed.
func extract(create func(string) (io.WriteCloser, error), zipPath, dest string, maxBytes int64, maxEntries int) (kept, dups []string, err error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, nil, err
	}
	defer zr.Close()
	if err := checkZipFiles(zr.File, maxBytes, maxEntries); err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, nil, err
	}
	defer func() {
		if err != nil {
			for _, k := range kept {
				os.Remove(filepath.Join(dest, k))
			}
			kept, dups = nil, nil
		}
	}()

	seen := map[string]bool{}
	remaining := maxBytes
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") || isJunk(f.Name) {
			continue
		}
		base, err := SafeName(f.Name)
		if err != nil {
			return kept, dups, err
		}
		if seen[base] {
			dups = append(dups, base)
			continue
		}
		seen[base] = true
		n, err := writeEntry(create, f, filepath.Join(dest, base), remaining, &kept, base)
		if err != nil {
			return kept, dups, err
		}
		remaining -= n
	}
	return kept, dups, nil
}

func writeEntry(create func(string) (io.WriteCloser, error), f *zip.File, target string, remaining int64, kept *[]string, base string) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	out, err := create(target)
	if err != nil {
		return 0, createError(err)
	}
	*kept = append(*kept, base)
	n, err := io.Copy(out, io.LimitReader(rc, remaining+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	if n > remaining {
		return n, ErrTooLarge
	}
	return n, nil
}

// Extract unpacks zipPath flat into dest (sub-folders become base names) and
// returns the extracted file names. Limits count the bytes actually read.
func Extract(zipPath, dest string, maxBytes int64, maxEntries int) ([]string, error) {
	kept, _, err := extract(createFile, zipPath, dest, maxBytes, maxEntries)
	return kept, err
}

// zipEntryNames lists the file entry names without unpacking.
func zipEntryNames(zipPath string) ([]string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return entryNames(zr.File), nil
}

// entryNames returns the base names of the file entries.
func entryNames(files []*zip.File) []string {
	var out []string
	for _, f := range files {
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") || isJunk(f.Name) {
			continue
		}
		out = append(out, path.Base(strings.ReplaceAll(f.Name, "\\", "/")))
	}
	return out
}

// declaredSize returns the sum of the declared unpacked entry sizes.
func declaredSize(files []*zip.File) int64 {
	var n uint64
	for _, f := range files {
		n += f.UncompressedSize64
		if n > 1<<62 {
			return 1 << 62
		}
	}
	return int64(n)
}
