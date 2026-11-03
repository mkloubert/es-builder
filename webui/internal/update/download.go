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

package update

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Fetcher downloads url to dst and checks the sha256 (lower case hex).
type Fetcher interface {
	Fetch(ctx context.Context, url, dst, sha string) error
}

// DefaultMax is the download limit: 400 MB.
const DefaultMax = 400 << 20

// HTTPFetcher downloads with net/http. A nil Client means a client with TLS
// checks that follows only https redirects. Max 0 means DefaultMax.
type HTTPFetcher struct {
	Client *http.Client
	Max    int64
}

func (f HTTPFetcher) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return &http.Client{
		Timeout:   15 * time.Minute,
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || len(via) > 5 {
				return errors.New("redirect not allowed")
			}
			return nil
		},
	}
}

// Fetch writes to a temp file next to dst, checks size and sha256, then
// renames it. On any error no file is left.
func (f HTTPFetcher) Fetch(ctx context.Context, url, dst, sha string) error {
	max := f.Max
	if max <= 0 {
		max = DefaultMax
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: status %d", resp.StatusCode)
	}
	if resp.ContentLength > max {
		return errors.New("download is too big")
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, max+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > max {
		return errors.New("download is too big")
	}
	if hex.EncodeToString(h.Sum(nil)) != sha {
		return errors.New("download checksum differs")
	}
	return os.Rename(tmp.Name(), dst)
}
