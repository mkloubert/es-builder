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

// Package gamelist reads and writes EmulationStation gamelist.xml files,
// preserving fields it does not know about.
package gamelist

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"emulationstation/webui/internal/config"
)

// Field is an arbitrary child element; Inner holds raw (escaped) inner XML.
type Field struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Inner   string     `xml:",innerxml"`
}

// Game is a <game> element with all its children.
type Game struct {
	XMLName xml.Name   `xml:"game"`
	Attrs   []xml.Attr `xml:",any,attr"`
	Fields  []Field    `xml:",any"`
}

// List is a parsed gamelist. Other holds <folder> and any other top-level elements.
type List struct {
	Games []Game
	Other []Field
}

// PathFor returns the gamelist.xml location for a system.
func PathFor(p config.Paths, system string) string {
	inRoms := filepath.Join(p.Roms, system, "gamelist.xml")
	if _, err := os.Stat(inRoms); err == nil {
		return inRoms
	}
	return filepath.Join(p.ESConfigDir, "gamelists", system, "gamelist.xml")
}

// BrokenMessage is the German user message for an unreadable gamelist file.
func BrokenMessage(path string) string {
	return "The game list " + path + " is damaged. Please repair or delete it (a backup may be next to it as .bak)."
}

// Read parses path. A missing file yields an empty list.
func Read(path string) (*List, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &List{}, nil
	}
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	l := &List{}
	depth := 0
	seenRoot := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("gamelist %s: %w", path, err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			if _, end := tok.(xml.EndElement); end {
				depth--
			}
			continue
		}
		if depth == 0 {
			seenRoot = true
			depth++
			continue
		}
		// child of root
		if start.Name.Local == "game" {
			var g Game
			if err := dec.DecodeElement(&g, &start); err != nil {
				return nil, fmt.Errorf("gamelist %s: %w", path, err)
			}
			l.Games = append(l.Games, g)
		} else {
			var f Field
			if err := dec.DecodeElement(&f, &start); err != nil {
				return nil, fmt.Errorf("gamelist %s: %w", path, err)
			}
			l.Other = append(l.Other, f)
		}
	}
	if !seenRoot {
		return nil, fmt.Errorf("gamelist %s: no root element", path)
	}
	return l, nil
}

// Write stores l at path, keeping a .bak of an existing file.
func Write(path string, l *List) error {
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0"?>` + "\n")
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	root := xml.StartElement{Name: xml.Name{Local: "gameList"}}
	if err := enc.EncodeToken(root); err != nil {
		return err
	}
	for i := range l.Games {
		if err := enc.Encode(&l.Games[i]); err != nil {
			return err
		}
	}
	for i := range l.Other {
		if err := enc.Encode(&l.Other[i]); err != nil {
			return err
		}
	}
	if err := enc.EncodeToken(root.End()); err != nil {
		return err
	}
	if err := enc.Flush(); err != nil {
		return err
	}
	buf.WriteString("\n")

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err == nil {
		if err := os.WriteFile(path+".bak", old, 0o644); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".gamelist-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// text returns the unescaped text content of f. Plain text needs no XML
// decoding; this keeps lookups fast in gamelists with thousands of games.
func (f Field) text() string {
	if !strings.ContainsAny(f.Inner, "<&\r") {
		return f.Inner
	}
	return f.decodeText()
}

// decodeText returns the text content of f through the XML decoder.
func (f Field) decodeText() string {
	dec := xml.NewDecoder(bytes.NewReader([]byte("<x>" + f.Inner + "</x>")))
	var s []byte
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if cd, ok := tok.(xml.CharData); ok {
			s = append(s, cd...)
		}
	}
	return string(s)
}

func escape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// Path returns the text of the <path> child.
func (g *Game) Path() string {
	for _, f := range g.Fields {
		if f.XMLName.Local == "path" {
			return f.text()
		}
	}
	return ""
}

func (l *List) find(rel string) *Game {
	for i := range l.Games {
		if l.Games[i].Path() == rel {
			return &l.Games[i]
		}
	}
	return nil
}

// Field returns the text of child name of the game with path rel.
func (l *List) Field(rel, name string) (string, bool) {
	g := l.find(rel)
	if g == nil {
		return "", false
	}
	for _, f := range g.Fields {
		if f.XMLName.Local == name {
			return f.text(), true
		}
	}
	return "", false
}

// Set updates or appends the given fields, creating the game if needed.
func (l *List) Set(rel string, fields map[string]string) {
	g := l.find(rel)
	if g == nil {
		l.Games = append(l.Games, Game{
			XMLName: xml.Name{Local: "game"},
			Fields:  []Field{{XMLName: xml.Name{Local: "path"}, Inner: escape(rel)}},
		})
		g = &l.Games[len(l.Games)-1]
	}
	// deterministic order for newly appended fields
	names := make([]string, 0, len(fields))
	for n := range fields {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v := escape(fields[n])
		found := false
		for i := range g.Fields {
			if g.Fields[i].XMLName.Local == n {
				g.Fields[i].Inner = v
				found = true
				break
			}
		}
		if !found {
			g.Fields = append(g.Fields, Field{XMLName: xml.Name{Local: n}, Inner: v})
		}
	}
}

// Remove deletes the game with path rel and reports whether it existed.
func (l *List) Remove(rel string) bool {
	for i := range l.Games {
		if l.Games[i].Path() == rel {
			l.Games = append(l.Games[:i], l.Games[i+1:]...)
			return true
		}
	}
	return false
}
