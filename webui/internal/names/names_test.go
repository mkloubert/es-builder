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

package names

import "testing"

func TestClean(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Super Mario World (USA) (Rev 1).sfc", "Super Mario World"},
		{"Legend of Zelda, The - A Link to the Past (Europe).sfc", "The Legend of Zelda - A Link to the Past"},
		{"Final_Fantasy_VII (Europe) (Disc 1).cue", "Final Fantasy VII – Disc 1"},
		{"Final Fantasy VII (Disk 2).cue", "Final Fantasy VII – Disc 2"},
		{"Tetris [!].gb", "Tetris"},
		{"  Double  Dragon  (J).nes", "Double Dragon"},
		{"sonic.the.hedgehog.md", "sonic the hedgehog"},
		{"Dr. Mario (USA).nes", "Dr. Mario"},
		{"Pokémon - Gelbe Edition (Germany).gbc", "Pokémon - Gelbe Edition"},
		{"(Prototype).nes", "(Prototype)"},
		{"game", "game"},
		{"Legend of Zelda, The (USA).nes", "The Legend of Zelda"},
	}
	for _, tt := range tests {
		if got := Clean(tt.in); got != tt.want {
			t.Errorf("Clean(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
