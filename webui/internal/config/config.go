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

// Package config loads and stores the KEY=VALUE configuration file.
package config

import (
	"bufio"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/crypto/bcrypt"
)

const (
	defaultHome    = "/home/emulationstation"
	esSystemsCfg   = "/etc/emulationstation/es_systems.cfg"
	inputHook      = "/opt/retropie/supplementary/emulationstation/scripts/inputconfiguration.sh"
	runDir         = "/run/emulationstation"
	stateDir       = "/var/lib/emulationstation-web"
	privHelper     = "/usr/local/lib/emulationstation/priv"
	defaultTLSCert = "/etc/emulationstation-web/tls/server.crt"
	defaultTLSKey  = "/etc/emulationstation-web/tls/server.key"
	mib            = int64(1) << 20

	// DefaultSourceURL is where the corresponding source code is offered.
	DefaultSourceURL = "https://github.com/mkloubert/es-builder"
)

// Paths holds filesystem locations derived from the home directory.
type Paths struct {
	Home, Roms, BIOS, ESConfigDir, ESSystemsCfg, ESInputCfg string
	InputHook, RunDir, Staging, StateDir, Priv              string
}

// Config is the web service configuration.
type Config struct {
	WebUser, WebPasswordHash             string
	ScreenscraperUser, ScreenscraperPass string
	Listen, TLSCert, TLSKey              string
	MaxUploadBytes, MaxExtractBytes      int64
	MaxZipEntries                        int
	USBMaxFiles                          int    // most files on one USB stick per import
	SourceURL                            string // AGPL section 13 source link; empty = no link
	UIDir                                string // custom web UI files (templates/, static/); empty = off
	Paths                                Paths
}

// Default returns the default configuration.
func Default() *Config {
	c := &Config{
		Listen:          "0.0.0.0:11944",
		TLSCert:         defaultTLSCert,
		TLSKey:          defaultTLSKey,
		MaxUploadBytes:  4096 * mib,
		MaxExtractBytes: 8192 * mib,
		MaxZipEntries:   10000,
		USBMaxFiles:     20000,
		SourceURL:       DefaultSourceURL,
	}
	c.setHome(defaultHome)
	c.UIDir = defaultUIDir(defaultHome)
	return c
}

// defaultUIDir returns the default custom web UI folder for home.
func defaultUIDir(home string) string { return home + "/webui-custom" }

// setHome sets Home and derives all dependent paths.
func (c *Config) setHome(home string) {
	c.Paths = Paths{
		Home:         home,
		Roms:         home + "/RetroPie/roms",
		BIOS:         home + "/RetroPie/BIOS",
		ESConfigDir:  home + "/.emulationstation",
		ESSystemsCfg: esSystemsCfg,
		ESInputCfg:   home + "/.emulationstation/es_input.cfg",
		InputHook:    inputHook,
		RunDir:       runDir,
		Staging:      home + "/.cache/emulationstation-web/staging",
		StateDir:     stateDir,
		Priv:         privHelper,
	}
}

// Load reads path; a missing file yields Default(). Unknown keys are ignored.
func Load(path string) (*Config, error) {
	c := Default()
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	vals := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vals[strings.TrimSpace(k)] = v
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	if v, ok := vals["HOME_DIR"]; ok && v != "" {
		c.setHome(v)
		c.UIDir = defaultUIDir(v)
	}
	if v, ok := vals["ES_SYSTEMS_CFG"]; ok && v != "" {
		c.Paths.ESSystemsCfg = v
	}
	set := func(key string, dst *string) {
		if v, ok := vals[key]; ok {
			*dst = v
		}
	}
	set("WEB_USER", &c.WebUser)
	set("WEB_PASSWORD_HASH", &c.WebPasswordHash)
	set("SCREENSCRAPER_USER", &c.ScreenscraperUser)
	set("SCREENSCRAPER_PASSWORD", &c.ScreenscraperPass)
	set("LISTEN", &c.Listen)
	set("TLS_CERT", &c.TLSCert)
	set("TLS_KEY", &c.TLSKey)
	set("SOURCE_URL", &c.SourceURL)
	set("UI_DIR", &c.UIDir)

	num := func(key string) (int64, bool, error) {
		v, ok := vals[key]
		if !ok || strings.TrimSpace(v) == "" {
			return 0, false, nil
		}
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || n < 0 {
			return 0, false, fmt.Errorf("config: invalid %s: %q", key, v)
		}
		return n, true, nil
	}
	if n, ok, err := num("MAX_UPLOAD_MB"); err != nil {
		return nil, err
	} else if ok {
		c.MaxUploadBytes = n * mib
	}
	if n, ok, err := num("MAX_EXTRACT_MB"); err != nil {
		return nil, err
	} else if ok {
		c.MaxExtractBytes = n * mib
	}
	if n, ok, err := num("MAX_ZIP_ENTRIES"); err != nil {
		return nil, err
	} else if ok {
		c.MaxZipEntries = int(n)
	}
	if n, ok, err := num("USB_MAX_FILES"); err != nil {
		return nil, err
	} else if ok {
		if n < 1 {
			return nil, fmt.Errorf("config: invalid USB_MAX_FILES: %q", vals["USB_MAX_FILES"])
		}
		c.USBMaxFiles = int(n)
	}
	return c, nil
}

// Save writes the configuration atomically with mode 0600.
func (c *Config) Save(path string) error {
	pairs := [][2]string{
		{"WEB_USER", c.WebUser},
		{"WEB_PASSWORD_HASH", c.WebPasswordHash},
		{"SCREENSCRAPER_USER", c.ScreenscraperUser},
		{"SCREENSCRAPER_PASSWORD", c.ScreenscraperPass},
		{"LISTEN", c.Listen},
		{"TLS_CERT", c.TLSCert},
		{"TLS_KEY", c.TLSKey},
		{"MAX_UPLOAD_MB", strconv.FormatInt(c.MaxUploadBytes/mib, 10)},
		{"MAX_EXTRACT_MB", strconv.FormatInt(c.MaxExtractBytes/mib, 10)},
		{"MAX_ZIP_ENTRIES", strconv.Itoa(c.MaxZipEntries)},
		{"USB_MAX_FILES", strconv.Itoa(c.USBMaxFiles)},
		{"HOME_DIR", c.Paths.Home},
		{"ES_SYSTEMS_CFG", c.Paths.ESSystemsCfg},
		{"SOURCE_URL", c.SourceURL},
		{"UI_DIR", c.UIDir},
	}
	var b strings.Builder
	for _, p := range pairs {
		if strings.ContainsAny(p[1], "\r\n") {
			return fmt.Errorf("config: value of %s contains a line break", p[0])
		}
		b.WriteString(p[0] + "=" + p[1] + "\n")
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".web.conf-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if fi, err := os.Stat(path); err == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				if err := os.Chown(name, int(st.Uid), int(st.Gid)); err != nil {
					return err
				}
			}
		}
	}
	return os.Rename(name, path)
}

// HashPassword returns a bcrypt hash (cost 10).
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 10)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// CheckCredentials verifies user and password; the user comparison is constant time.
func (c *Config) CheckCredentials(user, pw string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(c.WebUser)) == 1
	if c.WebPasswordHash == "" {
		return false
	}
	pwOK := bcrypt.CompareHashAndPassword([]byte(c.WebPasswordHash), []byte(pw)) == nil
	return userOK && pwOK
}
