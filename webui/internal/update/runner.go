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
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Runner runs an outside program. dir may be empty. env is added to the
// environment of this process. stdout may be nil. If stdout has a
// MergeStderr() method, stderr goes to it too.
type Runner interface {
	Run(ctx context.Context, dir string, env []string, stdout io.Writer, name string, args ...string) error
}

// ExecRunner runs programs with os/exec.
type ExecRunner struct{}

const maxStderr = 4096

// limitBuf keeps the first maxStderr bytes and drops the rest.
type limitBuf struct{ bytes.Buffer }

func (b *limitBuf) Write(p []byte) (int, error) {
	if room := maxStderr - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// Run starts name with args. The text of stderr is part of the returned error.
// Callers must not show that text to the user.
func (ExecRunner) Run(ctx context.Context, dir string, env []string, stdout io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = stdout
	var stderr limitBuf
	cmd.Stderr = &stderr
	// A writer with MergeStderr() also gets stderr (for build logs). The
	// short stderr copy stays in the error. The writer must be safe for
	// concurrent use.
	if _, ok := stdout.(interface{ MergeStderr() }); ok {
		cmd.Stderr = io.MultiWriter(&stderr, stdout)
	}
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s: %w: %s", name, err, msg)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
