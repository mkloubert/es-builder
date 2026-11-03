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
	"strings"
	"testing"
)

func TestExecRunnerMergesStderrOnRequest(t *testing.T) {
	var tl tail
	err := ExecRunner{}.Run(context.Background(), "", nil, &tl, "sh", "-c", "echo out; echo err >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "err") {
		t.Errorf("error should keep the stderr text: %v", err)
	}
	got := strings.Join(tl.lines(), "|")
	if !strings.Contains(got, "out") || !strings.Contains(got, "err") {
		t.Errorf("tail = %q", got)
	}
}

func TestExecRunnerPlainStdoutHasNoStderr(t *testing.T) {
	var b capBuf
	b.max = 100
	ExecRunner{}.Run(context.Background(), "", nil, &b, "sh", "-c", "echo out; echo err >&2")
	if strings.Contains(b.buf.String(), "err") {
		t.Errorf("stdout = %q", b.buf.String())
	}
}
