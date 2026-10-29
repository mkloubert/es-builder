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

package sysinfo

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCollectFromFakeRoot(t *testing.T) {
	now := time.Now()
	info, err := Collector{Root: "testdata/root", DiskPath: t.TempDir()}.Collect(now)
	if err != nil {
		t.Fatal(err)
	}
	if info.Hostname != "emubox" || !info.HasTemp || math.Abs(info.TempC-52.1) > 1e-9 || info.Load1 != 0.42 {
		t.Fatalf("%+v", info)
	}
	if info.MemTotal != 2048000*1024 || info.MemAvail != 1024000*1024 {
		t.Fatalf("%+v", info)
	}
	if info.DiskTotal == 0 || info.HasCert {
		t.Fatalf("%+v", info)
	}
	for _, ip := range info.IPs {
		if ip == "127.0.0.1" || ip == "::1" {
			t.Fatal("loopback")
		}
	}
	info, err = Collector{Root: "testdata/nothermal"}.Collect(now)
	if err != nil || info.HasTemp || info.Hostname != "emubox" {
		t.Fatalf("%+v %v", info, err)
	}
}

func TestCertDaysLeft(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	na := now.Add(10*24*time.Hour + 5*time.Hour)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "x"},
		NotBefore: now.Add(-time.Hour), NotAfter: na}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "c.crt")
	os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	info, _ := Collector{Root: "testdata/root", CertPath: p}.Collect(now)
	if !info.HasCert || info.CertDaysLeft != 10 || !info.CertNotAfter.Equal(na.Truncate(time.Second)) {
		t.Fatalf("%+v", info)
	}
	os.WriteFile(p, []byte("junk"), 0o644)
	info, _ = Collector{Root: "testdata/root", CertPath: p}.Collect(now)
	if info.HasCert {
		t.Fatal("HasCert")
	}
}
