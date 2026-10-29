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

package controllers

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"emulationstation/webui/internal/priv"
)

type node struct {
	raw      []byte
	joystick bool
	name     string
	guid     string
}

// InputCfg holds the raw children of es_input.cfg in file order.
type InputCfg struct {
	nodes []node
}

func classify(raw []byte) node {
	n := node{raw: raw}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if err != nil {
			return n
		}
		if se, ok := tok.(xml.StartElement); ok {
			if se.Name.Local != "inputConfig" {
				return n
			}
			var typ string
			for _, a := range se.Attr {
				switch a.Name.Local {
				case "type":
					typ = a.Value
				case "deviceName":
					n.name = a.Value
				case "deviceGUID":
					n.guid = a.Value
				}
			}
			n.joystick = typ == "joystick"
			return n
		}
	}
}

// ReadInputCfg parses path; a missing file yields an empty list.
func ReadInputCfg(path string) (*InputCfg, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &InputCfg{}, nil
	}
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	c := &InputCfg{}
	inRoot := false
	for {
		start := dec.InputOffset()
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if !inRoot {
			if se.Name.Local != "inputList" {
				return nil, fmt.Errorf("parse %s: root element is %q, want inputList", path, se.Name.Local)
			}
			inRoot = true
			continue
		}
		if err := dec.Skip(); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		c.nodes = append(c.nodes, classify(bytes.TrimSpace(data[start:dec.InputOffset()])))
	}
	if !inRoot {
		return nil, fmt.Errorf("parse %s: no inputList element", path)
	}
	return c, nil
}

func (c *InputCfg) matchIndex(d Device) []int {
	for _, pred := range []func(node) bool{
		func(n node) bool { return d.GUIDCRC != "" && strings.EqualFold(n.guid, d.GUIDCRC) },
		func(n node) bool { return d.GUID != "" && strings.EqualFold(n.guid, d.GUID) },
		func(n node) bool { return d.Name != "" && n.name == d.Name },
	} {
		var idx []int
		for i, n := range c.nodes {
			if n.joystick && pred(n) {
				idx = append(idx, i)
			}
		}
		if len(idx) > 0 {
			return idx
		}
	}
	return nil
}

// Match returns the raw joystick node for d: by GUIDCRC, then GUID, then name.
func (c *InputCfg) Match(d Device) ([]byte, bool) {
	idx := c.matchIndex(d)
	if len(idx) == 0 {
		return nil, false
	}
	return append([]byte(nil), c.nodes[idx[0]].raw...), true
}

// Remove deletes all joystick nodes matching d and reports whether any existed.
func (c *InputCfg) Remove(d Device) bool {
	idx := c.matchIndex(d)
	if len(idx) == 0 {
		return false
	}
	drop := map[int]bool{}
	for _, i := range idx {
		drop[i] = true
	}
	kept := c.nodes[:0:0]
	for i, n := range c.nodes {
		if !drop[i] {
			kept = append(kept, n)
		}
	}
	c.nodes = kept
	return true
}

// Upsert validates a single joystick <inputConfig> element and replaces the
// node with the same deviceGUID, or appends it.
func (c *InputCfg) Upsert(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	dec := xml.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("invalid inputConfig: %w", err)
	}
	se, ok := tok.(xml.StartElement)
	if !ok || se.Name.Local != "inputConfig" {
		return errors.New("invalid inputConfig: root element must be inputConfig")
	}
	if err := dec.Skip(); err != nil {
		return fmt.Errorf("invalid inputConfig: %w", err)
	}
	for {
		t, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid inputConfig: %w", err)
		}
		switch v := t.(type) {
		case xml.CharData:
			if len(bytes.TrimSpace(v)) == 0 {
				continue
			}
		case xml.Comment:
			continue
		}
		return errors.New("invalid inputConfig: trailing content")
	}
	n := classify(raw)
	if !n.joystick || n.name == "" || n.guid == "" {
		return errors.New(`invalid inputConfig: need type="joystick", deviceName and deviceGUID`)
	}
	for i, e := range c.nodes {
		if e.joystick && strings.EqualFold(e.guid, n.guid) {
			c.nodes[i] = n
			return nil
		}
	}
	c.nodes = append(c.nodes, n)
	return nil
}

// Write saves the list to path, keeping a .bak of an existing file.
func (c *InputCfg) Write(path string) error {
	mode := os.FileMode(0o644)
	if old, err := os.ReadFile(path); err == nil {
		if st, err := os.Stat(path); err == nil {
			mode = st.Mode().Perm()
		}
		if err := os.WriteFile(path+".bak", old, mode); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var b bytes.Buffer
	b.WriteString("<?xml version=\"1.0\"?>\n<inputList>\n")
	for _, n := range c.nodes {
		b.Write(n.raw)
		b.WriteByte('\n')
	}
	b.WriteString("</inputList>\n")
	return writeAtomic(path, b.Bytes(), mode)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// MarkConfigured sets Configured on every device that has a matching node.
func MarkConfigured(devs []Device, c *InputCfg) {
	for i := range devs {
		_, devs[i].Configured = c.Match(devs[i])
	}
}

// ApplyHook writes es_temporaryinput.cfg and runs the RetroPie input hook.
func ApplyHook(ctx context.Context, r priv.Runner, esConfigDir, hook string, node []byte) error {
	var b bytes.Buffer
	b.WriteString("<?xml version=\"1.0\"?>\n<inputList>\n")
	b.Write(bytes.TrimSpace(node))
	b.WriteString("\n</inputList>\n")
	if err := os.WriteFile(filepath.Join(esConfigDir, "es_temporaryinput.cfg"), b.Bytes(), 0o644); err != nil {
		return err
	}
	return r.Run(ctx, nil, "bash", hook)
}
