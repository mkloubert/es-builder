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

// Command emulationstation-web is the web UI for the EmulationStation box.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"emulationstation/webui/internal/bluetooth"
	"emulationstation/webui/internal/config"
	"emulationstation/webui/internal/controllers"
	"emulationstation/webui/internal/es"
	"emulationstation/webui/internal/jobs"
	"emulationstation/webui/internal/priv"
	"emulationstation/webui/internal/roms"
	"emulationstation/webui/internal/sysinfo"
	"emulationstation/webui/internal/systems"
	"emulationstation/webui/internal/usb"
	"emulationstation/webui/internal/web"
)

const defaultConfig = "/etc/emulationstation-web/web.conf"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return cmdServe(args, stderr)
	case "credentials":
		return cmdCredentials(args, stdin, stderr)
	case "passwd":
		return cmdPasswd(args, stdin, stdout, stderr)
	case "guid":
		return cmdGUID(args, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\nusage: emulationstation-web [serve|credentials|passwd|guid] [--config P]\n", cmd)
		return 2
	}
}

func newFlags(name string, stderr io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs, fs.String("config", defaultConfig, "path to configuration file")
}

func cmdServe(args []string, stderr io.Writer) int {
	fs, cfgPath := newFlags("serve", stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, *cfgPath, stderr); err != nil {
		fmt.Fprintln(stderr, "serve:", err)
		return 1
	}
	return 0
}

// cfgHolder gives handlers concurrent access to the current configuration.
// The file is re-read whenever its mtime or size changes, so edits made
// outside the process (e.g. the CLI passwd command) are picked up.
type cfgHolder struct {
	wmu  sync.Mutex // serializes Save+swap
	mu   sync.Mutex
	path string
	cfg  *config.Config
	logw io.Writer

	sigMod  time.Time
	sigSize int64
	sigOK   bool
	logged  string // last stat/load problem already logged
}

func newCfgHolder(path string, cfg *config.Config, logw io.Writer) *cfgHolder {
	h := &cfgHolder{path: path, cfg: cfg, logw: logw}
	h.remember()
	return h
}

// remember records the current file signature (mu or exclusive use required).
func (h *cfgHolder) remember() {
	fi, err := os.Stat(h.path)
	if err != nil {
		h.sigOK = false
		return
	}
	h.sigMod, h.sigSize, h.sigOK = fi.ModTime(), fi.Size(), true
}

func (h *cfgHolder) problem(msg string) {
	if msg != h.logged && h.logw != nil {
		fmt.Fprintln(h.logw, msg)
	}
	h.logged = msg
}

// refresh reloads the file if it changed. Caller holds mu.
func (h *cfgHolder) refresh() {
	fi, err := os.Stat(h.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			h.problem("config: " + err.Error())
		}
		return
	}
	if h.sigOK && fi.ModTime().Equal(h.sigMod) && fi.Size() == h.sigSize {
		return
	}
	h.sigMod, h.sigSize, h.sigOK = fi.ModTime(), fi.Size(), true
	c, err := config.Load(h.path)
	if err != nil {
		h.problem("config reload: " + err.Error())
		return
	}
	h.logged = ""
	h.cfg = c
}

func (h *cfgHolder) get() *config.Config {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.refresh()
	return h.cfg
}

// update applies fn to a copy of the (freshly reloaded) config and saves it
// atomically (read-modify-write under one lock).
func (h *cfgHolder) update(fn func(*config.Config) error) error {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	c := *h.get()
	if err := fn(&c); err != nil {
		return err
	}
	if err := c.Save(h.path); err != nil {
		return err
	}
	h.mu.Lock()
	h.cfg = &c
	h.remember()
	h.mu.Unlock()
	return nil
}

func (h *cfgHolder) set(c *config.Config) error {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	if err := c.Save(h.path); err != nil {
		return err
	}
	h.mu.Lock()
	h.cfg = c
	h.remember()
	h.mu.Unlock()
	return nil
}

func serve(ctx context.Context, cfgPath string, logw io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	holder := newCfgHolder(cfgPath, cfg, logw)

	sysList, err := systems.Load(cfg.Paths.ESSystemsCfg)
	if err != nil {
		fmt.Fprintln(logw, "systems:", err)
		sysList = nil
	}
	stager := roms.NewStager(cfg, sysList)
	if err := stager.CleanAll(); err != nil {
		fmt.Fprintln(logw, "staging cleanup:", err)
	}
	q, err := jobs.NewQueue(filepath.Join(cfg.Paths.StateDir, "jobs.json"))
	if err != nil {
		return fmt.Errorf("jobs: %w", err)
	}
	q.SetErrorLog(logw)
	qDone := make(chan struct{})
	go func() { q.Run(ctx); close(qDone) }()

	helper := cfg.Paths.Priv
	if helper == "" {
		helper = priv.DefaultHelper
	}
	runner := priv.ExecRunner{}
	privHelper := priv.Priv{R: runner, Helper: helper}

	var btMgr *bluetooth.Manager
	mgr := &bluetooth.Manager{}
	if bus, err := bluetooth.NewDBus(mgr.Window()); err != nil {
		fmt.Fprintln(logw, "bluetooth disabled:", err)
	} else {
		mgr.Bus = bus
		btMgr = mgr
	}

	h := web.NewHandler(web.Deps{
		ConfigPath:   cfgPath,
		Config:       holder.get,
		SetConfig:    holder.set,
		UpdateConfig: holder.update,
		Systems:      func() []systems.System { return sysList },
		Stager:       stager,
		ES:           &es.Controller{RunDir: cfg.Paths.RunDir, Procs: es.ProcFS{Root: "/proc"}},
		Jobs:         q,
		Priv:         privHelper,
		Sysinfo:      sysinfo.Collector{DiskPath: cfg.Paths.Roms, CertPath: cfg.TLSCert},
		BT:           btMgr,
		USB: &usb.Importer{
			Priv:    privHelper,
			Stager:  stager,
			Config:  holder.get,
			Systems: func() []systems.System { return sysList },
		},
		Runner: runner,
		LogW:   logw,
	})
	err = web.ListenAndServeTLS(ctx, cfg, h)
	cancel()
	<-qDone
	return err
}

func cmdCredentials(args []string, stdin io.Reader, stderr io.Writer) int {
	fs, cfgPath := newFlags("credentials", stderr)
	user := fs.String("user", "", "new web user name")
	pwStdin := fs.Bool("password-stdin", false, "read new password from stdin")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *user == "" && !*pwStdin {
		fmt.Fprintln(stderr, "credentials: need --user and/or --password-stdin")
		return 2
	}
	c, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "credentials:", err)
		return 1
	}
	if *user != "" {
		c.WebUser = *user
	}
	if *pwStdin {
		pw, err := readLine(bufio.NewReader(stdin))
		if err != nil || pw == "" {
			fmt.Fprintln(stderr, "credentials: no password on stdin")
			return 1
		}
		if c.WebPasswordHash, err = config.HashPassword(pw); err != nil {
			fmt.Fprintln(stderr, "credentials:", err)
			return 1
		}
	}
	if err := c.Save(*cfgPath); err != nil {
		fmt.Fprintln(stderr, "credentials:", err)
		return 1
	}
	return 0
}

func cmdPasswd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs, cfgPath := newFlags("passwd", stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	c, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "passwd:", err)
		return 1
	}
	r := bufio.NewReader(stdin)
	prompt := func(p string) (string, error) {
		fmt.Fprint(stdout, p)
		return readLine(r)
	}
	user, err := prompt("User: ")
	if err != nil || user == "" {
		fmt.Fprintln(stderr, "passwd: no user given")
		return 1
	}
	p1, err := prompt("Password: ")
	if err != nil || p1 == "" {
		fmt.Fprintln(stderr, "passwd: no password given")
		return 1
	}
	p2, err := prompt("Password (repeat): ")
	if err != nil || p1 != p2 {
		fmt.Fprintln(stderr, "passwd: passwords do not match")
		return 1
	}
	c.WebUser = user
	if c.WebPasswordHash, err = config.HashPassword(p1); err != nil {
		fmt.Fprintln(stderr, "passwd:", err)
		return 1
	}
	if err := c.Save(*cfgPath); err != nil {
		fmt.Fprintln(stderr, "passwd:", err)
		return 1
	}
	return 0
}

func cmdGUID(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("guid", flag.ContinueOnError)
	fs.SetOutput(stderr)
	vals := map[string]*string{}
	for _, n := range []string{"bus", "vendor", "product", "version"} {
		vals[n] = fs.String(n, "0", n+" id (decimal or 0x hex)")
	}
	name := fs.String("name", "", "device name")
	crc := fs.Bool("crc", false, "store the CRC16 of the name in the GUID")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var v [4]uint16
	for i, n := range []string{"bus", "vendor", "product", "version"} {
		u, err := strconv.ParseUint(*vals[n], 0, 16)
		if err != nil {
			fmt.Fprintf(stderr, "guid: invalid --%s %q\n", n, *vals[n])
			return 2
		}
		v[i] = uint16(u)
	}
	fmt.Fprintln(stdout, controllers.SDLGUID(v[0], v[1], v[2], v[3], *name, *crc))
	return 0
}

// readLine reads one line without its trailing line break.
func readLine(r *bufio.Reader) (string, error) {
	s, err := r.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && s != "") {
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}
