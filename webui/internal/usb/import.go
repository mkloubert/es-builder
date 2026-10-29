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

package usb

import (
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

	"emulationstation/webui/internal/bios"
	"emulationstation/webui/internal/config"
	"emulationstation/webui/internal/jobs"
	"emulationstation/webui/internal/roms"
	"emulationstation/webui/internal/systems"
)

var (
	// ErrStickRemoved means the stick was pulled out during the import.
	ErrStickRemoved = errors.New("The USB stick was removed.")
	// ErrNoSpace means the SD card has too little free space. Errors from
	// the free space check are a *SpaceError, which matches ErrNoSpace.
	ErrNoSpace = errors.New("Not enough space on the SD card.")
	// ErrTooManyReadErrors means that maxReadErrors files of one import
	// could not be read while the stick was still there.
	ErrTooManyReadErrors = errors.New("The USB stick has too many read errors. Check it on your PC.")
)

const (
	umountTimeout  = 30 * time.Second
	defaultMaxFile = 20000
	maxReadErrors  = 20 // per import; the 20th read error stops the import
)

// Messages for a stick that cannot be mounted.
const (
	msgStickGone = "This USB stick is not available. Click Refresh."
	msgNoMount   = "The USB stick could not be mounted. Check that it uses FAT32, exFAT or NTFS."
	msgNTFSDirty = "An NTFS stick that was not ejected safely on Windows cannot be mounted. Eject it safely and try again."
	msgNoList    = "Could not read the USB devices."
)

// MountError is a failed mount of the stick. Error returns only the message
// for the user. Err is the helper's error with its raw output; it belongs in
// the service log, not on the page.
type MountError struct {
	Msg string
	Err error
}

func (e *MountError) Error() string { return e.Msg }
func (e *MountError) Unwrap() error { return e.Err }

// mountError maps a failed usb-mount to a message. The helper exits with 2
// when the device is not a usable stick (any more), with 3 when it cannot
// read the list of USB devices, and with 1 when mount itself fails.
func mountError(err error, fsType string) *MountError {
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) {
		switch ec.ExitCode() {
		case 2:
			return &MountError{Msg: msgStickGone, Err: err}
		case 3:
			return &MountError{Msg: msgNoList, Err: err}
		}
	}
	msg := msgNoMount
	if fsType == "ntfs" {
		msg += " " + msgNTFSDirty
	}
	return &MountError{Msg: msg, Err: err}
}

// SpaceError is the result of a failed free space check.
type SpaceError struct{ Need, Free int64 }

func (e *SpaceError) Error() string {
	return fmt.Sprintf("Not enough space on the SD card: need %s, free %s.", mb(e.Need), mb(e.Free))
}

// Is makes errors.Is(err, ErrNoSpace) true.
func (e *SpaceError) Is(target error) bool { return target == ErrNoSpace }

func mb(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }

// USBPriv is the part of the root helper that the import needs.
type USBPriv interface {
	USBMount(ctx context.Context, path string) (string, error)
	USBUmount(ctx context.Context) error
}

// Importer imports a USB stick.
type Importer struct {
	Priv    USBPriv
	Stager  *roms.Stager
	Config  func() *config.Config
	Systems func() []systems.System
	Now     func() time.Time
}

// Result is the outcome of Run. The counts are report lines: Staged and
// Review count staged files (companions included), Skipped counts
// "skipped: ..." and "bios skipped: ..." lines, BIOSSaved counts saved and
// overwritten BIOS files.
type Result struct {
	Prepared                           bool     // the stick had no layout and was prepared
	UploadIDs                          []string // staged uploads with at least one file that is not discarded
	Staged, Review, Skipped, BIOSSaved int
	// PerUpload has the Staged and Review counts of each upload in
	// UploadIDs (for example to correct the counts when uploads are
	// discarded).
	PerUpload map[string]Counts
	// Unmounted is true when the stick was unmounted at the end (it can be
	// removed). It is set on errors too.
	Unmounted bool
}

// Counts are the staged and the review files of one upload.
type Counts struct{ Staged, Review int }

// Run imports the stick at device (a /dev path) with the file system fsType
// (as usb-list gives it): mount, find or prepare the layout, scan, check
// free space, stage one upload per folder, save BIOS files, write the report
// and unmount. The stick is unmounted on every exit path. On error all
// staged uploads are discarded. A failed mount is a *MountError.
func (im *Importer) Run(ctx context.Context, r jobs.Reporter, device, fsType string) (res Result, err error) {
	j := &job{im: im, ctx: ctx, r: r, device: device, fsType: fsType, cfg: im.Config(), counts: map[string]Counts{}}
	defer func() { res.Unmounted = j.umount() }()
	ok := false
	defer func() {
		if !ok {
			j.discard(j.uploads)
			res.UploadIDs = nil
		}
	}()
	res, err = j.run()
	ok = err == nil
	return res, err
}

type job struct {
	im      *Importer
	ctx     context.Context
	r       jobs.Reporter
	device  string
	fsType  string
	cfg     *config.Config
	mount   string
	uploads []string
	plans   [][]roms.TreeFile // per group, from the free space check
	counts  map[string]Counts // per upload
	readErr int               // files that could not be read
	tooMany bool              // maxReadErrors was reached
}

// skipReadError counts a read error of one file. It reports whether the
// import may skip the file and go on: not when the stick is gone, and not
// at the maxReadErrors-th read error (then j.tooMany is set).
func (j *job) skipReadError(err error) bool {
	if stickRemoved(j.device, err) {
		return false
	}
	j.readErr++
	if j.readErr >= maxReadErrors {
		j.tooMany = true
		return false
	}
	return true
}

func (j *job) logf(format string, a ...any) {
	if j.r != nil {
		j.r.Logf(format, a...)
	}
}

func (j *job) progress(p float64) {
	if j.r != nil {
		j.r.Progress(p)
	}
}

// umount runs with a fresh context, so it works after a cancel too. It
// reports whether the stick is unmounted.
func (j *job) umount() bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(j.ctx), umountTimeout)
	defer cancel()
	if err := j.im.Priv.USBUmount(ctx); err != nil {
		j.logf("Could not unmount the USB stick: %v", err)
		return false
	}
	return true
}

func (j *job) discard(ids []string) {
	for _, id := range ids {
		_ = j.im.Stager.Discard(id)
	}
}

func (j *job) now() time.Time {
	if j.im.Now != nil {
		return j.im.Now()
	}
	return time.Now()
}

// rel returns p relative to the stick root, as a slash path.
func (j *job) rel(p string) string {
	r, err := filepath.Rel(j.mount, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}

// stickRemoved reports a read error caused by a stick that is gone.
func stickRemoved(device string, err error) bool {
	if !errors.Is(err, syscall.EIO) && !errors.Is(err, syscall.ENODEV) && !errors.Is(err, syscall.ENXIO) {
		return false
	}
	_, serr := os.Stat(device)
	return errors.Is(serr, os.ErrNotExist)
}

func isNoSpace(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}

// fail maps an error to the job error.
func (j *job) fail(err error) error {
	switch {
	case stickRemoved(j.device, err):
		return ErrStickRemoved
	case j.tooMany:
		return ErrTooManyReadErrors
	case isNoSpace(err):
		return ErrNoSpace
	}
	return err
}

func (j *job) run() (Result, error) {
	var res Result
	mount, err := j.im.Priv.USBMount(j.ctx, j.device)
	if err != nil {
		return res, mountError(err, j.fsType)
	}
	j.mount = mount
	lay, found, err := Detect(mount)
	if err != nil {
		return res, j.fail(err)
	}
	if !found {
		var names []string
		for _, s := range j.im.Systems() {
			names = append(names, s.Name)
		}
		if err := Prepare(mount, names); err != nil {
			return res, fmt.Errorf("Could not prepare the USB stick: %w", j.fail(err))
		}
		j.logf("Stick prepared. Fill it on your PC, then import again.")
		res.Prepared = true
		return res, nil
	}

	for _, d := range []string{lay.Roms, lay.BIOS} {
		if _, err := realDir(mount, d); err != nil {
			return res, j.fail(err)
		}
	}
	max := j.cfg.USBMaxFiles
	if max < 1 {
		max = defaultMaxFile
	}
	j.logf("Reading the USB stick.")
	groups, skips, err := Scan(lay.Roms, max)
	if errors.Is(err, ErrTooManyFiles) {
		return res, fmt.Errorf("%w The limit is %d files (USB_MAX_FILES).", ErrTooManyFiles, max)
	}
	if err != nil {
		return res, j.fail(err)
	}
	biosFiles, biosLines, err := j.scanBIOS(lay.BIOS)
	if err != nil {
		return res, j.fail(err)
	}
	if err := j.checkSpace(groups, biosFiles); err != nil {
		return res, err
	}

	var lines []Line
	for _, s := range skips {
		lines = append(lines, Line{Rel: j.rel(filepath.Join(lay.Roms, filepath.FromSlash(s.Rel))), Status: skipped(s.Reason)})
	}
	staged, err := j.stage(groups)
	if err != nil {
		return res, err
	}
	lines = append(lines, staged...)
	lines = append(lines, biosLines...)
	saved, err := j.saveBIOS(biosFiles)
	if err != nil {
		return res, err
	}
	lines = append(lines, saved...)

	for _, l := range lines {
		switch {
		case l.Status == StatusStaged:
			res.Staged++
		case l.Status == StatusReview:
			res.Review++
		case l.Status == StatusBIOSSaved, l.Status == StatusBIOSOverwrite:
			res.BIOSSaved++
		case strings.HasPrefix(l.Status, statusSkippedPrefix), l.Status == StatusBIOSImported:
			res.Skipped++
		}
	}
	if lay.IgnoredRetropie {
		lines = append(lines, Line{Rel: "retropie", Status: skipped("the folder emulationstation is used")})
	}
	if err := WriteReport(lay.Base, j.now(), lines); err != nil {
		j.logf("Could not write the report to the USB stick: %v", err)
	}
	res.UploadIDs = append([]string(nil), j.uploads...)
	res.PerUpload = map[string]Counts{}
	for _, id := range res.UploadIDs {
		res.PerUpload[id] = j.counts[id]
	}
	j.progress(1)
	// the caller logs the summary (it may still discard uploads)
	return res, nil
}

func (j *job) checkSpace(groups []Group, bf []biosFile) error {
	j.plans = planGroups(j.im.Stager, groups)
	need := needBytes(j.plans)
	for _, f := range bf {
		need = addSat(need, f.size)
	}
	if err := os.MkdirAll(j.cfg.Paths.Staging, 0o755); err != nil {
		return j.fail(err)
	}
	free, err := freeBytes(j.cfg.Paths.Staging)
	if err != nil {
		return err
	}
	if need > free {
		return &SpaceError{Need: need, Free: free}
	}
	return nil
}

// stage stages every group as one upload and returns the report lines.
func (j *job) stage(groups []Group) ([]Line, error) {
	var total, done int64
	for _, g := range groups {
		total += g.Bytes
	}
	report := func(n int64) {
		if total > 0 {
			j.progress(0.95 * float64(n) / float64(total))
		}
	}
	var lines []Line
	for gi, g := range groups {
		if err := j.ctx.Err(); err != nil {
			return nil, err
		}
		label := "roms/" + g.Hint
		if g.Hint == "" {
			label = "roms"
		}
		files := "files"
		if len(g.Rels) == 1 {
			files = "file"
		}
		j.logf("Copying %s (%d %s).", label, len(g.Rels), files)
		var plans []roms.TreeFile
		if gi < len(j.plans) {
			plans = j.plans[gi]
		}
		var cur int64
		up, err := j.im.Stager.StageTreeWith(j.ctx, g.Dir, g.Hint, g.Rels, roms.TreeOptions{
			Progress: func(n int64) {
				cur += n
				report(done + cur)
			},
			// a read error skips only that file, unless the stick is gone
			SkipReadError: j.skipReadError,
			Plans:         plans,
		})
		if err != nil {
			return nil, j.fail(err)
		}
		for _, f := range up.Files {
			if f.Reason == roms.ReasonReadError {
				j.logf("Read error: %s", j.rel(filepath.Join(g.Dir, filepath.FromSlash(f.Name))))
			}
		}
		done += g.Bytes
		report(done)
		fl := j.fileLines(g, up)
		if !hasOpenFiles(up) {
			j.discard([]string{up.ID})
		} else {
			j.uploads = append(j.uploads, up.ID)
			var c Counts
			for _, l := range fl {
				switch l.Status {
				case StatusStaged:
					c.Staged++
				case StatusReview:
					c.Review++
				}
			}
			j.counts[up.ID] = c
		}
		lines = append(lines, fl...)
	}
	return lines, nil
}

func hasOpenFiles(up *roms.Upload) bool {
	for _, f := range up.Files {
		if !f.Discarded {
			return true
		}
	}
	return false
}

// fileLines returns one report line per staged file. A companion gets the
// status of its main file. A main file is "staged" only when it has one
// system, no note and its target does not exist yet; a target that exists
// (with another size, else the file would be "already imported") needs the
// overwrite decision on the review page.
func (j *job) fileLines(g Group, up *roms.Upload) []Line {
	byName := map[string]*roms.StagedFile{}
	for i := range up.Files {
		if f := &up.Files[i]; !f.Discarded {
			if _, dup := byName[f.Name]; !dup {
				byName[f.Name] = f
			}
		}
	}
	lines := make([]Line, 0, len(up.Files))
	for _, f := range up.Files {
		src := f.Source
		if src == "" {
			src = f.Name
		}
		rel := j.rel(filepath.Join(g.Dir, filepath.FromSlash(src)))
		if src != f.Name {
			rel += " > " + path.Base(f.Name)
		}
		l := Line{Rel: rel}
		switch {
		case f.Discarded:
			l.Status = skipped(f.Reason)
		default:
			p := mainFile(byName, &f)
			l.Status = StatusReview
			if len(p.Candidates) == 1 && p.Note == "" && !j.targetExists(p.Candidates[0], p.Name) {
				l.Status = StatusStaged
			}
			if p.Name == f.Name {
				l.Note = f.Note
			}
		}
		lines = append(lines, l)
	}
	return lines
}

// targetExists reports whether <Roms>/<system>/<name> exists (of any type).
func (j *job) targetExists(system, name string) bool {
	if !safeSegment(system) || !roms.SafeRel(name) {
		return false
	}
	_, err := os.Lstat(filepath.Join(j.cfg.Paths.Roms, system, filepath.FromSlash(name)))
	return err == nil
}

// mainFile follows the CompanionOf chain to the main file.
func mainFile(byName map[string]*roms.StagedFile, f *roms.StagedFile) *roms.StagedFile {
	cur := f
	for i := 0; i <= len(byName) && cur.CompanionOf != ""; i++ {
		next, ok := byName[cur.CompanionOf]
		if !ok {
			break
		}
		cur = next
	}
	return cur
}

// biosFile is a BIOS file on the stick; name is "x" or "dc/x" as bios.Save
// takes it.
type biosFile struct {
	name, src string
	size      int64
}

// scanBIOS lists the flat files of the BIOS folder (and of its dc/ folder,
// which bios.Save supports).
func (j *job) scanBIOS(dir string) ([]biosFile, []Line, error) {
	var files []biosFile
	var lines []Line
	var walk func(dir, prefix string) error
	walk = func(dir, prefix string) error {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			name := e.Name()
			p := filepath.Join(dir, name)
			skip := func(reason string) { lines = append(lines, Line{Rel: j.rel(p), Status: skipped(reason)}) }
			switch {
			case isJunk(name):
			case e.Type()&os.ModeSymlink != 0:
				skip(reasonSymlink)
			case !safeName(name):
				skip(reasonUnsafe)
			case e.IsDir() && prefix == "" && name == "dc":
				if err := walk(p, "dc/"); err != nil {
					return err
				}
			case e.IsDir():
				skip("sub folder")
			case !e.Type().IsRegular():
				skip(reasonNotFile)
			default:
				info, err := e.Info()
				if err != nil {
					skip(reasonReadError)
					continue
				}
				files = append(files, biosFile{name: prefix + name, src: p, size: info.Size()})
			}
		}
		return nil
	}
	if ok, err := isDir(dir); err != nil || !ok {
		return nil, nil, err
	}
	err := walk(dir, "")
	return files, lines, err
}

// readTracker remembers read errors of the source.
type readTracker struct {
	r   io.Reader
	err error
}

func (t *readTracker) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && err != io.EOF {
		t.err = err
	}
	return n, err
}

// saveBIOS saves the BIOS files with bios.Save. A file with the same name
// and size is skipped; a different size is overwritten.
func (j *job) saveBIOS(files []biosFile) ([]Line, error) {
	var lines []Line
	for _, f := range files {
		if err := j.ctx.Err(); err != nil {
			return nil, err
		}
		line := Line{Rel: j.rel(f.src), Status: StatusBIOSSaved}
		dst := filepath.Join(j.cfg.Paths.BIOS, filepath.FromSlash(f.name))
		if st, err := os.Lstat(dst); err == nil {
			switch {
			case !st.Mode().IsRegular():
				line.Status = skipped("target is not a file")
			case st.Size() == f.size:
				line.Status = StatusBIOSImported
			default:
				line.Status = StatusBIOSOverwrite
			}
		}
		if f.size > bios.MaxSize {
			line.Status = skipped("file is too large")
		}
		if line.Status != StatusBIOSSaved && line.Status != StatusBIOSOverwrite {
			lines = append(lines, line)
			continue
		}
		rc, err := j.im.Stager.OpenSourceFile(f.src)
		if err != nil {
			if !j.skipReadError(err) {
				return nil, j.fail(err)
			}
			j.logf("Read error: %s", line.Rel)
			lines = append(lines, Line{Rel: line.Rel, Status: skipped(reasonReadError)})
			continue
		}
		tr := &readTracker{r: rc}
		err = bios.Save(j.cfg.Paths.BIOS, f.name, tr)
		rc.Close()
		switch {
		case err == nil:
		case tr.err != nil && !j.skipReadError(tr.err):
			return nil, j.fail(tr.err)
		case tr.err != nil:
			j.logf("Read error: %s", line.Rel)
			line.Status = skipped(reasonReadError)
		case errors.Is(err, bios.ErrTooLarge):
			line.Status = skipped("file is too large")
		case isNoSpace(err):
			return nil, ErrNoSpace
		default:
			return nil, fmt.Errorf("Saving the BIOS file %s failed: %w", f.name, err)
		}
		lines = append(lines, line)
	}
	return lines, nil
}
