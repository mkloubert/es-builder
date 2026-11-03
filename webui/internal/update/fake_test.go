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
	"io"
)

// call is one recorded runner call.
type call struct {
	Dir  string
	Env  []string
	Name string
	Args []string
}

// fakeRunner records calls and answers from a function.
type fakeRunner struct {
	calls []call
	fn    func(c call, stdout io.Writer) error
}

func (f *fakeRunner) Run(_ context.Context, dir string, env []string, stdout io.Writer, name string, args ...string) error {
	c := call{dir, env, name, args}
	f.calls = append(f.calls, c)
	if f.fn == nil {
		return nil
	}
	return f.fn(c, stdout)
}
