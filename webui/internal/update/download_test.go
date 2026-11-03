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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPFetcher(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 1000)
	sum := sha256.Sum256(body)
	good := hex.EncodeToString(sum[:])
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/404" {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	f := HTTPFetcher{Client: srv.Client(), Max: 2000}
	dir := t.TempDir()

	dst := filepath.Join(dir, "ok")
	if err := f.Fetch(context.Background(), srv.URL+"/f", dst, good); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); !bytes.Equal(b, body) {
		t.Error("content differs")
	}

	bad := filepath.Join(dir, "bad")
	if err := f.Fetch(context.Background(), srv.URL+"/f", bad, strings.Repeat("0", 64)); err == nil {
		t.Error("bad sha: want error")
	}
	big := filepath.Join(dir, "big")
	small := HTTPFetcher{Client: srv.Client(), Max: 500}
	if err := small.Fetch(context.Background(), srv.URL+"/f", big, good); err == nil {
		t.Error("too big: want error")
	}
	nf := filepath.Join(dir, "nf")
	if err := f.Fetch(context.Background(), srv.URL+"/404", nf, good); err == nil {
		t.Error("404: want error")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("leftover files: %v", ents)
	}
}
