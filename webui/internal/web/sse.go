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

package web

import (
	"fmt"
	"net/http"
	"strings"
)

// SSE streams values from ch as Server-Sent Events until ch closes or the
// client disconnects. render turns one value into the event payload; a render
// error ends the stream with an "error" event.
func (s *Server) SSE(w http.ResponseWriter, r *http.Request, ch <-chan any, render func(any) (string, error)) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming is not supported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case v, open := <-ch:
			if !open {
				return
			}
			out, err := render(v)
			if err != nil {
				s.logf("sse render: %v", err)
				fmt.Fprint(w, "event: error\ndata: Internal error\n\n")
				fl.Flush()
				return
			}
			var sb strings.Builder
			for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
				sb.WriteString("data: " + line + "\n")
			}
			sb.WriteString("\n")
			if _, err := fmt.Fprint(w, sb.String()); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
