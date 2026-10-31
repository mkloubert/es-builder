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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"emulationstation/webui/internal/controllers"
	"emulationstation/webui/internal/es"
)

// maxControllerXML bounds an uploaded inputConfig element.
const maxControllerXML = 64 << 10

func (s *Server) controllerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /controllers", s.handleControllers)
	mux.HandleFunc("POST /controllers/reset", s.handleControllerReset)
	mux.HandleFunc("GET /controllers/export", s.handleControllerExport)
	mux.HandleFunc("POST /controllers/import", s.handleControllerImport)
	s.bluetoothRoutes(mux)
}

func (s *Server) sysRoot() string {
	if s.d.SysRoot != "" {
		return s.d.SysRoot
	}
	return "/sys"
}

type controllersView struct {
	Devices []controllers.Device
	Error   bool
	BT      btView
}

func (s *Server) handleControllers(w http.ResponseWriter, r *http.Request) {
	v := controllersView{BT: s.btView()}
	devs, err := controllers.Enumerate(s.sysRoot())
	if err != nil {
		s.logf("enumerate controllers: %v", err)
		v.Error = true
	}
	if cfg, err := controllers.ReadInputCfg(s.d.Config().Paths.ESInputCfg); err != nil {
		s.logf("read input cfg: %v", err)
		v.Error = true
	} else {
		controllers.MarkConfigured(devs, cfg)
	}
	v.Devices = devs
	s.page(w, r, "controllers", "Controller", v)
}

// findDevice resolves a GUID (with or without name CRC) to a connected device.
func (s *Server) findDevice(w http.ResponseWriter, r *http.Request, guid string) (controllers.Device, bool) {
	devs, err := controllers.Enumerate(s.sysRoot())
	if err == nil && guid != "" {
		for _, d := range devs {
			if strings.EqualFold(d.GUID, guid) || strings.EqualFold(d.GUIDCRC, guid) {
				return d, true
			}
		}
	}
	s.fail(w, r, http.StatusNotFound, "Controller not found.")
	return controllers.Device{}, false
}

// esFail answers an error of a WithESStopped operation.
func (s *Server) esFail(w http.ResponseWriter, r *http.Request, what string, err error) {
	var ue *userError
	s.logf("%s: %v", what, err)
	switch {
	case errors.As(err, &ue):
		s.fail(w, r, http.StatusUnprocessableEntity, ue.msg)
	case errors.Is(err, es.ErrBusy), errors.Is(err, es.ErrGameRunning), errors.Is(err, es.ErrStopTimeout), errors.Is(err, es.ErrNotRunning),
		errors.Is(err, es.ErrNoSession):
		code, msg := actionError(err)
		s.fail(w, r, code, msg)
	default:
		s.fail(w, r, http.StatusInternalServerError, "The controller configuration could not be changed.")
	}
}

func (s *Server) handleControllerReset(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	dev, ok := s.findDevice(w, r, r.PostFormValue("guid"))
	if !ok {
		return
	}
	path := s.d.Config().Paths.ESInputCfg
	err := s.d.ES.WithESStopped(context.WithoutCancel(r.Context()), func() error {
		cfg, err := controllers.ReadInputCfg(path)
		if err != nil {
			return &userError{"es_input.cfg is not readable – nothing was changed.", err}
		}
		if !cfg.Remove(dev) {
			return nil
		}
		return cfg.Write(path)
	})
	if err != nil {
		s.esFail(w, r, "controller reset", err)
		return
	}
	s.ok(w, r, "Controller configuration reset.", "/controllers")
}

// attrEncode percent-encodes s for an RFC 5987 ext-value.
func attrEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// downloadName builds a Content-Disposition value for "<name>.xml".
func downloadName(name string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`/\"`, r) {
			return '_'
		}
		return r
	}, name)
	clean = strings.Trim(clean, " .")
	if clean == "" {
		clean = "controller"
	}
	ascii := strings.Map(func(r rune) rune {
		if r > 0x7e || r == '%' {
			return '_'
		}
		return r
	}, clean)
	return fmt.Sprintf(`attachment; filename="%s.xml"; filename*=UTF-8''%s`, ascii, attrEncode(clean+".xml"))
}

func (s *Server) handleControllerExport(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.findDevice(w, r, r.URL.Query().Get("guid"))
	if !ok {
		return
	}
	cfg, err := controllers.ReadInputCfg(s.d.Config().Paths.ESInputCfg)
	if err != nil {
		s.logf("export: %v", err)
		s.fail(w, r, http.StatusInternalServerError, "es_input.cfg is not readable.")
		return
	}
	node, found := cfg.Match(dev)
	if !found {
		s.fail(w, r, http.StatusNotFound, "This controller has no configuration.")
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", downloadName(dev.Name))
	_, _ = w.Write(append([]byte("<?xml version=\"1.0\"?>\n"), append(bytes.TrimSpace(node), '\n')...))
}

// readImportFile streams the first file part of the multipart body.
func readImportFile(w http.ResponseWriter, r *http.Request) ([]byte, int, string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxControllerXML+(64<<10))
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, http.StatusBadRequest, "Invalid upload request."
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, http.StatusBadRequest, "No file was sent."
		}
		if err != nil {
			return nil, http.StatusRequestEntityTooLarge, "The file is too large."
		}
		if part.FileName() == "" {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(part, maxControllerXML+1))
		if err != nil {
			return nil, http.StatusBadRequest, "Invalid upload request."
		}
		if len(b) > maxControllerXML {
			return nil, http.StatusRequestEntityTooLarge, "The file is too large (64 KiB at most)."
		}
		return b, 0, ""
	}
}

func (s *Server) handleControllerImport(w http.ResponseWriter, r *http.Request) {
	raw, code, msg := readImportFile(w, r)
	if code != 0 {
		s.fail(w, r, code, msg)
		return
	}
	raw = bytes.TrimSpace(raw)
	if bytes.HasPrefix(raw, []byte("<?xml")) { // optional XML declaration
		if i := bytes.Index(raw, []byte("?>")); i >= 0 {
			raw = bytes.TrimSpace(raw[i+2:])
		}
	}
	cfgv := s.d.Config()
	ctx := context.WithoutCancel(r.Context())
	err := s.d.ES.WithESStopped(ctx, func() error {
		cfg, err := controllers.ReadInputCfg(cfgv.Paths.ESInputCfg)
		if err != nil {
			return &userError{"es_input.cfg is not readable – nothing was changed.", err}
		}
		if err := cfg.Upsert(raw); err != nil {
			return &userError{"The file is not a valid controller configuration (inputConfig with type=\"joystick\").", err}
		}
		if err := cfg.Write(cfgv.Paths.ESInputCfg); err != nil {
			return err
		}
		hctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		return controllers.ApplyHook(hctx, s.runner(), cfgv.Paths.ESConfigDir, cfgv.Paths.InputHook, raw)
	})
	if err != nil {
		s.esFail(w, r, "controller import", err)
		return
	}
	redirect(w, r, "/controllers")
}
