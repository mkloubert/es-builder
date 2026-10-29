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
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"emulationstation/webui/internal/jobs"
	"emulationstation/webui/internal/priv"
	"emulationstation/webui/internal/usb"
)

const (
	usbJobKind      = "usb-import"
	usbListTimeout  = 10 * time.Second
	usbEjectTimeout = 30 * time.Second

	msgUSBListFailed = "Could not read the USB devices."
	msgUSBBusy       = "A USB import is already running."

	// usbMountDir is where the root helper mounts a stick.
	usbMountDir = "/run/emulationstation-usb"
	// mountInfo lists the mounts of this process; it is readable without root.
	mountInfo = "/proc/self/mountinfo"
)

var errNoHelper = errors.New("the root helper is not set up")

func (s *Server) usbRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /usb", s.handleUSBPage)
	mux.HandleFunc("POST /usb/import", s.handleUSBImport)
	mux.HandleFunc("POST /usb/eject", s.handleUSBEject)
}

type usbDeviceView struct {
	Path, FSType, Label, Size string
}

type usbPageView struct {
	Devices []usbDeviceView
	Error   string
	Busy    bool
	Mounted bool // a stick is mounted on usbMountDir; only then Eject is shown
}

// usbMounted reports whether something is mounted on usbMountDir. When the
// mount list cannot be read, it returns true, so Eject is still offered.
func (s *Server) usbMounted() bool {
	p := s.d.MountInfo
	if p == "" {
		p = mountInfo
	}
	f, err := os.Open(p)
	if err != nil {
		s.logf("usb: %v", err)
		return true
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		// field 5 is the mount point (spaces are written as \040; the
		// mount dir has none)
		if fields := strings.Fields(sc.Text()); len(fields) > 4 && fields[4] == usbMountDir {
			return true
		}
	}
	if err := sc.Err(); err != nil {
		s.logf("usb: read %s: %v", p, err)
		return true
	}
	return false
}

// usbDevices asks the helper for the USB sticks (10 s at most).
func (s *Server) usbDevices(ctx context.Context) ([]priv.USBDevice, error) {
	if s.d.Priv.R == nil {
		return nil, errNoHelper
	}
	ctx, cancel := context.WithTimeout(ctx, usbListTimeout)
	defer cancel()
	return s.d.Priv.USBList(ctx)
}

// usbBusy reports a usb-import job that is queued or running.
func (s *Server) usbBusy() bool {
	if s.d.Jobs == nil {
		return false
	}
	for _, j := range s.d.Jobs.List() {
		if j.Kind == usbJobKind && (j.Status == jobs.StatusQueued || j.Status == jobs.StatusRunning) {
			return true
		}
	}
	return false
}

// gb formats a size in bytes as GB (10^9 bytes) with one decimal.
func gb(n int64) string { return fmt.Sprintf("%.1f GB", float64(n)/1e9) }

func (s *Server) handleUSBPage(w http.ResponseWriter, r *http.Request) {
	v := usbPageView{Busy: s.usbBusy(), Mounted: s.usbMounted()}
	devs, err := s.usbDevices(r.Context())
	if err != nil {
		s.logf("usb-list: %v", err)
		v.Error = msgUSBListFailed
	}
	for _, d := range devs {
		v.Devices = append(v.Devices, usbDeviceView{Path: d.Path, FSType: d.FSType, Label: d.Label, Size: gb(d.Size)})
	}
	s.page(w, r, "usb", "USB", v)
}

func (s *Server) handleUSBImport(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	device := r.PostFormValue("device")
	if s.d.Jobs == nil || s.d.USB == nil {
		s.fail(w, r, http.StatusInternalServerError, "USB import is not available.")
		return
	}
	devs, err := s.usbDevices(r.Context())
	if err != nil {
		s.logf("usb-list: %v", err)
		s.fail(w, r, http.StatusInternalServerError, msgUSBListFailed)
		return
	}
	found, fsType := false, ""
	for _, d := range devs {
		if d.Path == device {
			found, fsType = true, d.FSType
		}
	}
	if !found {
		s.fail(w, r, http.StatusBadRequest, "This USB stick is not available.")
		return
	}
	// The lock makes the check and the submit one step.
	s.usbMu.Lock()
	defer s.usbMu.Unlock()
	if s.usbBusy() {
		s.fail(w, r, http.StatusConflict, msgUSBBusy)
		return
	}
	job := s.d.Jobs.Submit(usbJobKind, "Import from USB stick", s.usbImportJob(device, fsType))
	redirect(w, r, "/jobs/"+job.ID)
}

// usbImportJob runs the import and links the job to the review page. The
// job log gets one summary. The import unmounts the stick at the end; the
// log then says that it can be removed.
func (s *Server) usbImportJob(device, fsType string) func(ctx context.Context, rep jobs.Reporter) error {
	im := s.d.USB
	return func(ctx context.Context, rep jobs.Reporter) error {
		res, err := im.Run(ctx, rep, device, fsType)
		var me *usb.MountError
		if errors.As(err, &me) {
			// the raw helper output goes to the service log only
			s.logf("usb-mount %s: %v", device, me.Err)
		}
		if err == nil && !res.Prepared {
			s.usbSummary(rep, &res)
		}
		if res.Unmounted && me == nil && !errors.Is(err, usb.ErrStickRemoved) {
			rep.Logf("You can remove the USB stick now.")
		}
		// Run logs "Stick prepared. Fill it on your PC, then import again."
		return err
	}
}

// usbSummary keeps at most maxReviewIDs uploads, logs the one summary of the
// import and links the job to the review page.
func (s *Server) usbSummary(rep jobs.Reporter, res *usb.Result) {
	ids := res.UploadIDs
	dropped := 0
	if len(ids) > maxReviewIDs {
		// The rest is discarded; it is staged again by the next import.
		for _, id := range ids[maxReviewIDs:] {
			c := res.PerUpload[id]
			res.Staged -= c.Staged
			res.Review -= c.Review
			dropped += c.Staged + c.Review
			_ = s.d.USB.Stager.Discard(id)
		}
		ids = ids[:maxReviewIDs]
	}
	rep.Logf("Result: %s ready, %s %s a decision, %s skipped, %s saved.",
		fileCount(res.Staged), fileCount(res.Review), needVerb(res.Review), fileCount(res.Skipped), countOf(res.BIOSSaved, "BIOS file", "BIOS files"))
	if dropped > 0 {
		rep.Logf("There are more than %d folders. Only the first %d are ready for review. Not staged: %s. Apply the files, then import the stick again for the rest.",
			maxReviewIDs, maxReviewIDs, fileCount(dropped))
	}
	if len(ids) == 0 {
		if res.BIOSSaved == 0 {
			rep.Logf("Nothing new on the stick.")
		}
		return
	}
	if l, ok := rep.(jobs.Linker); ok {
		l.SetLink("/upload/" + strings.Join(ids, ","))
	}
	rep.Logf("Click \"Review files\" to check the files and apply them.")
}

func (s *Server) handleUSBEject(w http.ResponseWriter, r *http.Request) {
	// The same lock as the import submit: no import can start between the
	// busy check and the unmount.
	s.usbMu.Lock()
	defer s.usbMu.Unlock()
	if s.usbBusy() {
		s.fail(w, r, http.StatusConflict, msgUSBBusy)
		return
	}
	if s.d.Priv.R == nil {
		s.fail(w, r, http.StatusInternalServerError, "Could not eject the USB stick.")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), usbEjectTimeout)
	defer cancel()
	if err := s.d.Priv.USBUmount(ctx); err != nil {
		s.logf("usb-umount: %v", err)
		s.fail(w, r, http.StatusInternalServerError, "Could not eject the USB stick.")
		return
	}
	s.ok(w, r, "You can remove the USB stick now.", "/usb")
}
