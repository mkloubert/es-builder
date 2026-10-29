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

// Package sysinfo collects device statistics.
package sysinfo

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Info holds device statistics.
type Info struct {
	Hostname     string
	TempC        float64
	HasTemp      bool
	Load1        float64
	MemTotal     uint64
	MemAvail     uint64
	DiskTotal    uint64
	DiskFree     uint64
	IPs          []string
	CertNotAfter time.Time
	CertDaysLeft int
	HasCert      bool
}

// Collector reads stats; Root "" means "/".
type Collector struct {
	Root     string
	DiskPath string
	CertPath string
}

func (c Collector) read(rel string) (string, bool) {
	root := c.Root
	if root == "" {
		root = "/"
	}
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// Collect gathers what is available; missing sources leave zero values.
// It only returns an error if DiskPath is set and Statfs fails.
func (c Collector) Collect(now time.Time) (Info, error) {
	var info Info
	if s, ok := c.read("proc/sys/kernel/hostname"); ok {
		info.Hostname = s
	}
	if s, ok := c.read("sys/class/thermal/thermal_zone0/temp"); ok {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			info.TempC, info.HasTemp = v/1000, true
		}
	}
	if s, ok := c.read("proc/loadavg"); ok {
		if f := strings.Fields(s); len(f) > 0 {
			info.Load1, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	if s, ok := c.read("proc/meminfo"); ok {
		for _, line := range strings.Split(s, "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			v, err := strconv.ParseUint(f[1], 10, 64)
			if err != nil {
				continue
			}
			switch f[0] {
			case "MemTotal:":
				info.MemTotal = v * 1024
			case "MemAvailable:":
				info.MemAvail = v * 1024
			}
		}
	}
	info.IPs = localIPs()
	if c.CertPath != "" {
		if b, err := os.ReadFile(c.CertPath); err == nil {
			for len(b) > 0 {
				var blk *pem.Block
				blk, b = pem.Decode(b)
				if blk == nil {
					break
				}
				if blk.Type != "CERTIFICATE" {
					continue
				}
				if cert, err := x509.ParseCertificate(blk.Bytes); err == nil {
					info.HasCert = true
					info.CertNotAfter = cert.NotAfter
					info.CertDaysLeft = int(cert.NotAfter.Sub(now).Hours() / 24)
					if d := cert.NotAfter.Sub(now); d < 0 && d%(24*time.Hour) != 0 {
						info.CertDaysLeft-- // floor for negatives
					}
				}
				break
			}
		}
	}
	if c.DiskPath != "" {
		var st syscall.Statfs_t
		if err := syscall.Statfs(c.DiskPath, &st); err != nil {
			return info, err
		}
		info.DiskTotal = uint64(st.Blocks) * uint64(st.Bsize)
		info.DiskFree = uint64(st.Bavail) * uint64(st.Bsize)
	}
	return info, nil
}

func localIPs() []string {
	var out []string
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP
			if ip.IsLoopback() {
				continue
			}
			if ip.To4() != nil || ip.IsGlobalUnicast() {
				out = append(out, ip.String())
			}
		}
	}
	return out
}
