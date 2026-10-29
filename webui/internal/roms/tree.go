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
	"archive/zip"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"emulationstation/webui/internal/names"
	"emulationstation/webui/internal/systems"
)

// ReasonImported marks a file that is already in the ROM folder with the
// same size.
const ReasonImported = "already imported"

// ReasonReadError marks a source file that could not be read. The other
// files of a disc that needs it get "read error in <file>" (see
// ReasonReadErrorIn).
const ReasonReadError = "read error"

// ReasonReadErrorIn is the reason of a file that was read, but is skipped
// because another file of its disc (rel) could not be read.
func ReasonReadErrorIn(rel string) string { return ReasonReadError + " in " + rel }

// Reasons for archives on a USB stick that cannot be unpacked. An upload
// fails for these; a stick import skips only the archive.
const (
	reasonDamaged  = "damaged archive"
	reasonTooLarge = "archive is too large when unpacked"
	reasonTooMany  = "too many files in archive"
	reasonUnsafe   = "unsafe file names in archive"
	reasonLongName = "file name too long in archive"
	reasonLongFile = "file name too long"
)

// ReadError is a failed read of one source file in StageTree.
type ReadError struct {
	Rel string // slash path below the source folder
	Err error
}

func (e *ReadError) Error() string { return "reading " + e.Rel + " failed: " + e.Err.Error() }
func (e *ReadError) Unwrap() error { return e.Err }

// OpenSource opens a regular file for reading. It does not follow symlinks.
func OpenSource(name string) (io.ReadCloser, error) {
	f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s: not a regular file", name)
	}
	return f, nil
}

// srcReader reads a source file. It stops when ctx is done, reports copied
// bytes and remembers read errors of the source.
type srcReader struct {
	ctx      context.Context
	r        io.Reader
	progress func(n int64)
	n        int64 // bytes read
	err      error
}

func (s *srcReader) Read(p []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := s.r.Read(p)
	s.n += int64(n)
	if n > 0 && s.progress != nil {
		s.progress(int64(n))
	}
	if err != nil && err != io.EOF {
		s.err = err
	}
	return n, err
}

// safeRel reports whether rel is a clean relative slash path whose parts are
// all safe file names.
func safeRel(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "/") {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if b, err := SafeName(part); err != nil || b != part {
			return false
		}
	}
	return true
}

// isZipFormatErr reports an error from a broken ZIP directory.
func isZipFormatErr(err error) bool {
	return errors.Is(err, zip.ErrFormat) || errors.Is(err, zip.ErrAlgorithm) || errors.Is(err, zip.ErrChecksum)
}

func isNoSpace(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}

// isArchiveErr reports an error that comes from the archive data itself: a
// broken directory, an unknown compression method, a bad checksum, corrupt
// deflate data or a cut entry.
func isArchiveErr(err error) bool {
	var ce flate.CorruptInputError
	return isZipFormatErr(err) || errors.As(err, &ce) || errors.Is(err, io.ErrUnexpectedEOF)
}

// archiveReason maps an error from reading or unpacking the local copy of a
// ZIP (<id>.part) to a skip reason. It returns "" for no error and for an
// error of the SD card (a write error, a full card); those stop the staging
// (see sdError). Only errors of the archive itself (see isArchiveErr) and the
// unpack limits skip the archive.
func archiveReason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrTooLarge):
		return reasonTooLarge
	case errors.Is(err, ErrTooManyEntries):
		return reasonTooMany
	case errors.Is(err, ErrUnsafePath):
		return reasonUnsafe
	case errors.Is(err, ErrNameTooLong):
		return reasonLongName
	case isArchiveErr(err):
		return reasonDamaged
	}
	return ""
}

// sdError is an error of a file in the staging folder on the SD card. A full
// card and a cancel keep their error (the caller has a message for them).
func sdError(err error) error {
	if err == nil || isNoSpace(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("Could not write to the SD card: %w", err)
}

// TreeFile is the plan for one source file of StageTree, made before
// anything is copied.
type TreeFile struct {
	Rel      string
	Size     int64 // size on the source
	Unpack   bool  // a ZIP that will be unpacked
	Unpacked int64 // declared unpacked size of such a ZIP
	Damaged  bool  // a ZIP whose directory cannot be read; it is not copied
	Imported bool  // already in the ROM folder with the same size; it is not copied
	// Reason is set when the file is not copied: already imported, a damaged
	// archive, or a ZIP over the unpack limits (too large, too many files,
	// unsafe names). Such a file needs no space.
	Reason string

	checked bool // size is known and, for a ZIP, Unpack is decided
	sys     string
	readErr error // the ZIP directory could not be read (Reason is "read error")
}

// NoteScript is the note of a file that runs as a program (a .sh file).
// Such a file always needs a decision on the review page.
const NoteScript = "This file is a script. It runs as a program when started."

// isScript reports a file that a system runs as a program.
func isScript(name string) bool {
	return strings.EqualFold(path.Ext(name), ".sh")
}

// folderRules are the rules for the files of one stick folder. PlanTree (the
// free space check) and StageTree (the copy) both use them, so they cannot
// decide differently.
type folderRules struct {
	s       *Stager
	hint    systems.System
	hasHint bool
}

func (s *Stager) rules(hint string) folderRules {
	h, ok := systems.ByName(s.systems, hint)
	return folderRules{s: s, hint: h, hasHint: ok}
}

// keepsZips reports a hint system that keeps ZIPs as they are (for example
// arcade).
func (r folderRules) keepsZips() bool {
	return r.hasHint && r.hint.KeepArchives && accepts(r.hint, ".zip")
}

// candidates returns the candidate systems of a file. When the hint system
// accepts the file, it is the only candidate. When it does not, the file
// keeps all candidates and gets a note.
func (r folderRules) candidates(name string) ([]string, string) {
	if isGamelistFile(name) {
		return nil, ""
	}
	cands := r.s.candidateNames(name, false)
	if !r.hasHint {
		return cands, ""
	}
	if contains(cands, r.hint.Name) {
		return []string{r.hint.Name}, ""
	}
	return cands, fmt.Sprintf("folder %s does not accept %s", r.hint.Name, strings.ToLower(path.Ext(name)))
}

// zipPlan is the decision for one ZIP, made from its directory alone.
type zipPlan struct {
	unpack   bool
	unpacked int64      // declared unpacked size
	reason   string     // set when the ZIP is skipped: over the limits or unsafe names
	entries  []zipEntry // the files that unpacking keeps
}

// openZip opens a ZIP and reads its directory. It does not follow symlinks.
// It is a variable, so tests can count how often a directory is read.
var openZip = func(name string) (*zip.Reader, io.Closer, error) {
	rc, err := OpenSource(name)
	if err != nil {
		return nil, nil, err
	}
	f := rc.(*os.File)
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	zr, err := zip.NewReader(f, fi.Size())
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return zr, f, nil
}

// planZip decides how a ZIP is handled: kept as it is, unpacked, or skipped
// because unpacking it would break the limits (MAX_EXTRACT_MB,
// MAX_ZIP_ENTRIES). A ZIP whose directory cannot be read returns the error
// (see isArchiveErr and archiveReason).
func (r folderRules) planZip(zipPath string) (zipPlan, error) {
	if r.keepsZips() {
		return zipPlan{}, nil
	}
	zr, closer, err := openZip(zipPath)
	if err != nil {
		return zipPlan{}, err
	}
	defer closer.Close()
	if !r.s.holdsROMs(entryNames(zr.File)) {
		return zipPlan{}, nil
	}
	zp := zipPlan{unpack: true, unpacked: declaredSize(zr.File)}
	if err := checkZipFiles(zr.File, r.s.cfg.MaxExtractBytes, r.s.cfg.MaxZipEntries); err != nil {
		zp.reason = archiveReason(err)
		return zp, nil
	}
	if zp.entries, err = keptEntries(zr.File); err != nil {
		zp.reason = archiveReason(err)
	}
	return zp, nil
}

// zipImported reports whether every file that unpacking the ZIP at rel would
// keep is already in the ROM folder: <Roms>/<system>/<folder of rel>/<name>
// with the same size, for one of the file's candidate systems. A file that
// no system accepts must exist too when the ZIP holds a descriptor file (it
// may be a disc companion); otherwise staging would drop it anyway.
func (r folderRules) zipImported(rel string, entries []zipEntry) bool {
	folder := path.Dir(rel)
	found := map[string]bool{}
	var unknown []zipEntry
	hasDescriptor := false
	for _, e := range entries {
		name := path.Join(folder, e.name)
		hasDescriptor = hasDescriptor || isDescriptor(e.name)
		if cands, _ := r.candidates(name); len(cands) == 0 {
			unknown = append(unknown, e)
			continue
		}
		sys, ok := r.importedIn(name, e.size)
		if !ok {
			return false
		}
		found[sys] = true
	}
	if len(found) == 0 {
		return false
	}
	for _, e := range unknown {
		ok := false
		for c := range found {
			ok = ok || r.s.sameFile(c, path.Join(folder, e.name), e.size)
		}
		if !ok && hasDescriptor {
			return false
		}
	}
	return true
}

// importedIn returns the system whose ROM folder already has rel as a
// regular file of this size. When the hint system accepts the file, only the
// hint system counts. Otherwise the file counts only when exactly one
// candidate system has a file with this name, and that file has the same
// size. When several systems have one (for example psx and psx2), it is not
// counted: the file then goes to the review page, so a changed file is never
// dropped without a word.
func (r folderRules) importedIn(rel string, size int64) (string, bool) {
	cands, _ := r.candidates(rel)
	if r.hasHint && len(cands) == 1 && cands[0] == r.hint.Name {
		return cands[0], r.s.sameFile(cands[0], rel, size)
	}
	hit, n := "", 0
	for _, c := range cands {
		if r.s.hasTarget(c, rel) {
			hit, n = c, n+1
		}
	}
	if n == 1 && r.s.sameFile(hit, rel, size) {
		return hit, true
	}
	return "", false
}

// PlanTree decides without copying how StageTree handles each file: which
// ZIPs are unpacked or skipped, which ZIPs are damaged or cannot be read and
// which files are already imported (see importedIn). A ZIP that would be
// already imported when every file it would give is (see zipImported).
// Companions of an imported descriptor file count as imported when they
// exist with the same size in the same system folder; a file that a not
// imported descriptor needs is always copied. A ZIP whose directory cannot
// be read gets the reason "read error"; other files that cannot be read get
// a plain plan, and the copy reports their read error.
func (s *Stager) PlanTree(srcDir, hint string, rels []string) []TreeFile {
	r := s.rules(hint)
	plans := make([]TreeFile, len(rels))
	idx := map[string]int{}
	for i, rel := range rels {
		p := &plans[i]
		p.Rel = rel
		idx[rel] = i
		if !safeRel(rel) {
			continue
		}
		if nameTooLong(rel) {
			// the SD card (ext4) stores at most 255 bytes per name; FAT,
			// exFAT and NTFS allow 255 characters, which can be more
			p.Reason = reasonLongFile
			continue
		}
		src := filepath.Join(srcDir, filepath.FromSlash(rel))
		fi, err := os.Lstat(src)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		p.Size = fi.Size()
		if strings.EqualFold(path.Ext(rel), ".zip") {
			zp, err := r.planZip(src)
			switch {
			case err == nil:
			case isArchiveErr(err):
				p.Damaged = true
				continue
			default:
				p.readErr, p.Reason = err, ReasonReadError
				continue
			}
			p.Unpack, p.Unpacked, p.Reason = zp.unpack, zp.unpacked, zp.reason
			if zp.unpack && zp.reason == "" && r.zipImported(rel, zp.entries) {
				p.Imported = true
			}
		}
		p.checked = true
	}

	plain := func(p *TreeFile) bool { return p.checked && !p.Unpack && !p.Damaged }
	for i := range plans {
		p := &plans[i]
		if !plain(p) {
			continue
		}
		if sys, ok := r.importedIn(p.Rel, p.Size); ok {
			p.Imported, p.sys = true, sys
		}
	}
	needed := map[int]bool{}
	for i := range plans {
		p := &plans[i]
		if !p.checked || !isDescriptor(p.Rel) {
			continue
		}
		cs, err := allCompanions(filepath.Join(srcDir, filepath.FromSlash(p.Rel)))
		if err != nil {
			continue
		}
		for _, c := range cs {
			j, ok := idx[path.Join(path.Dir(p.Rel), c)]
			if !ok {
				continue
			}
			cp := &plans[j]
			switch {
			case !p.Imported:
				needed[j] = true
			case plain(cp):
				// a companion of an imported disc counts only in the
				// system of that disc
				cp.Imported = s.sameFile(p.sys, cp.Rel, cp.Size)
				if cp.Imported {
					cp.sys = p.sys
				}
			}
		}
	}
	for j := range needed {
		plans[j].Imported = false
	}
	for i := range plans {
		p := &plans[i]
		switch {
		case p.Damaged:
			p.Reason = reasonDamaged
		case p.Imported:
			p.Reason = ReasonImported
		}
	}
	return plans
}

// hasTarget reports whether <Roms>/<system>/<rel> exists (of any type).
func (s *Stager) hasTarget(system, rel string) bool {
	if !isSafeSegment(system) {
		return false
	}
	_, err := os.Lstat(filepath.Join(s.cfg.Paths.Roms, system, filepath.FromSlash(rel)))
	return err == nil
}

// sameFile reports whether <Roms>/<system>/<rel> is a regular file of size.
func (s *Stager) sameFile(system, rel string, size int64) bool {
	if !isSafeSegment(system) {
		return false
	}
	fi, err := os.Lstat(filepath.Join(s.cfg.Paths.Roms, system, filepath.FromSlash(rel)))
	return err == nil && fi.Mode().IsRegular() && fi.Size() == size
}

// StageTree stages files of a folder (for example a system folder on a USB
// stick) as one upload; see StageTreeWith. progress is TreeOptions.Progress.
// Every failed read returns a *ReadError.
func (s *Stager) StageTree(ctx context.Context, srcDir, hint string, rels []string, progress func(n int64)) (*Upload, error) {
	return s.StageTreeWith(ctx, srcDir, hint, rels, TreeOptions{Progress: progress})
}

// TreeOptions are the options of StageTreeWith.
type TreeOptions struct {
	// Progress (may be nil) gets the number of bytes done since its last
	// call: bytes copied, and the size of each file that is not copied
	// (for example already imported). It is called at most every 1 MiB or
	// 250 ms, and once at the end.
	Progress func(n int64)
	// SkipReadError reports whether a failed read of one source file only
	// skips that file (reason "read error"). When it is nil or returns
	// false, StageTreeWith fails with a *ReadError.
	SkipReadError func(err error) bool
	// Plans is the result of PlanTree for the same srcDir, hint and rels.
	// When it is set, the ZIP directories are not read a second time.
	Plans []TreeFile
}

// Progress is reported at most every progressBytes or progressEvery.
const (
	progressBytes = 1 << 20
	progressEvery = 250 * time.Millisecond
)

// throttle collects progress and passes it on at most every progressBytes
// or progressEvery.
type throttle struct {
	fn      func(int64)
	pending int64
	last    time.Time
}

func (p *throttle) add(n int64) {
	if p.fn == nil || n <= 0 {
		return
	}
	p.pending += n
	if p.pending >= progressBytes || time.Since(p.last) >= progressEvery {
		p.flush()
	}
}

func (p *throttle) flush() {
	if p.fn != nil && p.pending > 0 {
		n := p.pending
		p.pending, p.last = 0, time.Now()
		p.fn(n)
	}
}

// plansFor returns opt.Plans when they fit rels, else a new plan.
func (s *Stager) plansFor(srcDir, hint string, rels []string, opt TreeOptions) []TreeFile {
	if len(opt.Plans) == len(rels) {
		ok := true
		for i, p := range opt.Plans {
			ok = ok && p.Rel == rels[i]
		}
		if ok {
			return opt.Plans
		}
	}
	return s.PlanTree(srcDir, hint, rels)
}

// StageTreeWith stages files of a folder (for example a system folder on a
// USB stick) as one upload. rels are slash paths below srcDir. Every file is
// copied to <staging>/<id>/<rel>; its staged name is rel. A ZIP is handled
// like an uploaded ZIP: when it holds ROMs it is unpacked into its own folder
// inside the upload. hint is a system name (usually the folder name); see
// folderRules. A failed read of a source file skips that file when
// opt.SkipReadError allows it; a disc that needs the file is skipped too.
// Otherwise it returns a *ReadError. A failed write on the SD card stops
// with "Could not write to the SD card: ..." (a full card keeps its error).
// On any error nothing is left in the staging folder.
func (s *Stager) StageTreeWith(ctx context.Context, srcDir, hint string, rels []string, opt TreeOptions) (*Upload, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.cfg.Paths.Staging, id)
	t := &tree{
		s: s, ctx: ctx, srcDir: srcDir, skip: opt.SkipReadError,
		prog: &throttle{fn: opt.Progress, last: time.Now()},
		dir:  dir, part: dir + ".part", scratch: dir + ".x",
		taken: map[string]bool{}, source: map[string]string{}, failed: map[string]string{},
	}
	t.rules = s.rules(hint)
	up, err := t.stage(id, rels, opt)
	os.Remove(t.part)
	os.RemoveAll(t.scratch)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	t.prog.flush()
	s.mu.Lock()
	s.uploads[id] = up
	s.mu.Unlock()
	return up.clone(), nil
}

type tree struct {
	s                  *Stager
	ctx                context.Context
	srcDir             string
	skip               func(error) bool
	prog               *throttle
	dir, part, scratch string
	rules              folderRules
	kept               []string          // staged names, in order
	source             map[string]string // staged name -> source rel
	taken              map[string]bool   // staged names in use
	failed             map[string]string // source rel -> why it is not staged (read error, name too long)
	dups               []StagedFile      // duplicate names, not on disk
	bad                []StagedFile      // not copied (already imported) or bad archives
}

func (t *tree) stage(id string, rels []string, opt TreeOptions) (*Upload, error) {
	if err := os.MkdirAll(t.dir, 0o755); err != nil {
		return nil, sdError(err)
	}
	for _, rel := range rels {
		if !safeRel(rel) {
			return nil, ErrUnsafePath
		}
	}
	for _, p := range t.s.plansFor(t.srcDir, t.rules.hint.Name, rels, opt) {
		if err := t.ctx.Err(); err != nil {
			return nil, err
		}
		if p.readErr != nil {
			if err := t.readFailed(p.Rel, &ReadError{Rel: p.Rel, Err: p.readErr}); err != nil {
				return nil, err
			}
			t.prog.add(p.Size)
			continue
		}
		if p.Reason != "" {
			if p.Reason == reasonLongFile {
				t.failed[p.Rel] = reasonLongFile // a disc that needs it is skipped too
			}
			t.discard(p.Rel, p.Reason)
			t.prog.add(p.Size)
			continue
		}
		if err := t.add(p); err != nil {
			return nil, err
		}
	}
	files := t.s.classify(t.dir, t.kept, nil)
	for i := range files {
		files[i].Source = t.source[files[i].Name]
	}
	t.applyRules(files)
	t.markImported(files)
	t.dropBrokenDiscs(files)
	files = append(files, t.dups...)
	files = append(files, t.bad...)
	return &Upload{ID: id, Files: files}, nil
}

// readFailed handles a failed read of the source file rel: it skips the file
// when t.skip allows it (and returns nil), else it returns err.
func (t *tree) readFailed(rel string, err error) error {
	var re *ReadError
	if !errors.As(err, &re) || t.skip == nil || !t.skip(re.Err) {
		return err
	}
	t.failed[rel] = ReasonReadError
	t.discard(rel, ReasonReadError)
	return nil
}

// add copies one source file into the upload.
func (t *tree) add(p TreeFile) error {
	rel := p.Rel
	if t.isTaken(rel) {
		t.dup(rel, rel)
		t.prog.add(p.Size)
		return nil
	}
	target := filepath.Join(t.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		if errors.Is(createError(err), ErrNameTooLong) {
			t.removeEmptyDirs(filepath.Dir(target))
			t.nameTooLong(rel)
			t.prog.add(p.Size)
			return nil
		}
		return sdError(err)
	}
	isZip := strings.EqualFold(path.Ext(rel), ".zip")
	dst := target
	if isZip {
		dst = t.part
	}
	if n, err := t.copy(rel, dst); err != nil {
		if errors.Is(err, ErrNameTooLong) {
			// the file system refuses the name (ENAMETOOLONG, EILSEQ)
			t.removeEmptyDirs(filepath.Dir(target))
			t.nameTooLong(rel)
			t.prog.add(p.Size - n)
			return nil
		}
		if err := t.readFailed(rel, err); err != nil {
			return err
		}
		t.prog.add(p.Size - n)
		return nil
	}
	if !isZip {
		t.keep(rel, rel)
		return nil
	}
	defer os.Remove(t.part)
	unpack := p.Unpack
	if !p.checked {
		// the plan could not read the ZIP on the stick; decide on the copy
		zp, err := t.rules.planZip(t.part)
		if err != nil {
			if reason := archiveReason(err); reason != "" {
				t.discard(rel, reason)
				return nil
			}
			return sdError(err)
		}
		if zp.reason != "" {
			t.discard(rel, zp.reason)
			return nil
		}
		unpack = zp.unpack
	}
	if !unpack {
		if err := os.Rename(t.part, target); err != nil {
			return sdError(err)
		}
		t.keep(rel, rel)
		return nil
	}
	return t.unpack(rel)
}

// unpack extracts the ZIP at t.part into the folder of rel. Only errors of
// the archive itself skip it; an error of the SD card stops the staging.
func (t *tree) unpack(rel string) error {
	if err := os.RemoveAll(t.scratch); err != nil {
		return sdError(err)
	}
	kept, dups, err := extract(t.s.createFn(), t.part, t.scratch, t.s.cfg.MaxExtractBytes, t.s.cfg.MaxZipEntries)
	if err != nil {
		// extract removed what it wrote; the scratch dir is removed later
		if reason := archiveReason(err); reason != "" {
			t.discard(rel, reason)
			return nil
		}
		return sdError(err)
	}
	folder := path.Dir(rel)
	for _, k := range kept {
		name := path.Join(folder, k)
		if t.isTaken(name) {
			t.dup(name, rel)
			continue
		}
		if err := os.Rename(filepath.Join(t.scratch, k), filepath.Join(t.dir, filepath.FromSlash(name))); err != nil {
			return sdError(err)
		}
		t.keep(name, rel)
	}
	for _, d := range dups {
		t.dup(path.Join(folder, d), rel)
	}
	return nil
}

// copy streams the source file rel to dst and returns the bytes read. A
// failed read of the source is a *ReadError; on any error dst is removed.
func (t *tree) copy(rel, dst string) (int64, error) {
	rc, err := t.s.OpenSourceFile(filepath.Join(t.srcDir, filepath.FromSlash(rel)))
	if err != nil {
		return 0, &ReadError{Rel: rel, Err: err}
	}
	defer rc.Close()
	sr := &srcReader{ctx: t.ctx, r: rc, progress: t.prog.add}
	err = writeNew(func(p string) (io.WriteCloser, error) {
		w, err := t.s.createFn()(p)
		return w, createError(err) // a name the file system refuses
	}, sr, dst, -1)
	if err != nil && sr.err != nil {
		return sr.n, &ReadError{Rel: rel, Err: sr.err}
	}
	if errors.Is(err, ErrNameTooLong) {
		return sr.n, err
	}
	return sr.n, sdError(err)
}

// isTaken reports a staged name in use or a path that already exists.
func (t *tree) isTaken(name string) bool {
	if t.taken[name] {
		return true
	}
	_, err := os.Lstat(filepath.Join(t.dir, filepath.FromSlash(name)))
	return err == nil
}

func (t *tree) keep(name, src string) {
	t.kept = append(t.kept, name)
	t.taken[name] = true
	t.source[name] = src
}

func (t *tree) dup(name, src string) {
	t.dups = append(t.dups, StagedFile{Name: name, SuggestedName: names.Clean(path.Base(name)), Discarded: true, Reason: reasonDup, Source: src})
}

// discard records a source file that is not copied or not kept.
func (t *tree) discard(rel, reason string) {
	t.bad = append(t.bad, StagedFile{Name: rel, SuggestedName: names.Clean(path.Base(rel)), Discarded: true, Reason: reason, Source: rel})
}

// applyRules sets the candidates and the note of every primary file with
// the folder rules (see folderRules.candidates). A script (.sh) always gets
// a note, so it always needs a decision.
func (t *tree) applyRules(files []StagedFile) {
	for i := range files {
		f := &files[i]
		if f.Discarded || f.CompanionOf != "" {
			continue
		}
		f.Candidates, f.Note = t.rules.candidates(f.Name)
		if isScript(f.Name) {
			if f.Note != "" {
				f.Note += ". "
			}
			f.Note += NoteScript
		}
	}
}

// markImported discards primary files that are already imported (see
// importedIn), together with their companions.
func (t *tree) markImported(files []StagedFile) {
	byName := map[string]*StagedFile{}
	for i := range files {
		if f := &files[i]; !f.Discarded {
			byName[f.Name] = f
		}
	}
	imported := map[string]bool{}
	for i := range files {
		f := &files[i]
		if f.Discarded || f.CompanionOf != "" {
			continue
		}
		st, err := os.Stat(filepath.Join(t.dir, filepath.FromSlash(f.Name)))
		if err != nil {
			continue
		}
		if _, ok := t.rules.importedIn(f.Name, st.Size()); ok {
			imported[f.Name] = true
		}
	}
	for i := range files {
		f := &files[i]
		if !f.Discarded && imported[root(byName, f.Name)] {
			f.Discarded, f.Reason = true, ReasonImported
		}
	}
}

// nameTooLong skips rel, whose name the SD card cannot store. A disc that
// needs it is skipped too (see dropBrokenDiscs).
func (t *tree) nameTooLong(rel string) {
	t.failed[rel] = reasonLongFile
	t.discard(rel, reasonLongFile)
}

// removeEmptyDirs removes dir and its parents up to the upload folder while
// they are empty (left over when a file in them could not be created).
func (t *tree) removeEmptyDirs(dir string) {
	for dir != t.dir && strings.HasPrefix(dir, t.dir+string(filepath.Separator)) {
		if os.Remove(dir) != nil { // not empty, or not there
			return
		}
		dir = filepath.Dir(dir)
	}
}

// dropBrokenDiscs skips a disc whose descriptor file (.cue, .gdi, .m3u,
// .ccd) needs a source file that was not staged (a read error, a name that
// is too long): the main file and all its companions get the reason
// "<why> in <file>", for example "read error in Game/Track2.bin". A disc
// without all its files does not work.
func (t *tree) dropBrokenDiscs(files []StagedFile) {
	if len(t.failed) == 0 {
		return
	}
	byName := map[string]*StagedFile{}
	for i := range files {
		if f := &files[i]; !f.Discarded {
			byName[f.Name] = f
		}
	}
	broken := map[string]string{} // main file -> a file it needs that failed
	for i := range files {
		f := &files[i]
		if f.Discarded || f.Source != f.Name || !isDescriptor(f.Name) {
			continue
		}
		cs, err := allCompanions(filepath.Join(t.srcDir, filepath.FromSlash(f.Name)))
		if err != nil {
			continue
		}
		for _, c := range cs {
			if bad := path.Join(path.Dir(f.Name), c); t.failed[bad] != "" {
				if r := root(byName, f.Name); broken[r] == "" {
					broken[r] = bad
				}
			}
		}
	}
	for i := range files {
		f := &files[i]
		if bad := broken[root(byName, f.Name)]; !f.Discarded && bad != "" {
			f.Discarded, f.Reason = true, t.failed[bad]+" in "+bad
		}
	}
}

func accepts(s systems.System, ext string) bool {
	for _, e := range s.Extensions {
		if e == ext {
			return true
		}
	}
	return false
}
