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

// Package auth provides HTTP Basic-Auth middleware with fail2ban-friendly logging.
package auth

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"emulationstation/webui/internal/config"
)

// ClientIP returns the peer address without port. IPv4-mapped IPv6 is returned
// as IPv4; X-Forwarded-For is ignored. Unparsable input is returned unchanged.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	return addr.Unmap().String()
}

const (
	cacheTTL     = 10 * time.Minute
	cacheMaxSize = 16
)

// checkCredentials is the (slow, bcrypt based) verification; a variable so
// tests can count calls.
var checkCredentials = func(c *config.Config, user, pw string) bool {
	return c.CheckCredentials(user, pw)
}

// successCache remembers recent successful credential checks. The key
// includes the password hash, so a credential change invalidates entries.
type successCache struct {
	mu  sync.Mutex
	m   map[[32]byte]time.Time
	now func() time.Time
}

func cacheKey(c *config.Config, user, pw string) [32]byte {
	return sha256.Sum256([]byte(c.WebPasswordHash + "\x00" + user + "\x00" + pw))
}

func (sc *successCache) has(k [32]byte) bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	exp, ok := sc.m[k]
	if !ok {
		return false
	}
	if !sc.now().Before(exp) {
		delete(sc.m, k)
		return false
	}
	return true
}

func (sc *successCache) add(k [32]byte) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	now := sc.now()
	for key, exp := range sc.m {
		if !now.Before(exp) {
			delete(sc.m, key)
		}
	}
	if _, ok := sc.m[k]; !ok && len(sc.m) >= cacheMaxSize {
		var oldest [32]byte
		var oldestExp time.Time
		first := true
		for key, exp := range sc.m {
			if first || exp.Before(oldestExp) {
				oldest, oldestExp, first = key, exp, false
			}
		}
		delete(sc.m, oldest)
	}
	sc.m[k] = now.Add(cacheTTL)
}

// Middleware enforces Basic auth against the current config. Failed attempts
// (credentials supplied but invalid) log "auth failure from <ip>\n" to logw.
func Middleware(current func() *config.Config, logw io.Writer, next http.Handler) http.Handler {
	var mu sync.Mutex
	cache := &successCache{m: map[[32]byte]time.Time{}, now: time.Now}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="EmuStation Builder", charset="UTF-8"`)
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		user, pw, ok := r.BasicAuth()
		if ok {
			cfg := current()
			k := cacheKey(cfg, user, pw)
			if cfg.WebPasswordHash != "" && cache.has(k) {
				ok = true
			} else if ok = checkCredentials(cfg, user, pw); ok {
				cache.add(k)
			}
		}
		if !ok {
			mu.Lock()
			fmt.Fprintf(logw, "auth failure from %s\n", strings.TrimSpace(ClientIP(r)))
			mu.Unlock()
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Del("WWW-Authenticate")
		next.ServeHTTP(w, r)
	})
}
