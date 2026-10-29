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

package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startRun(t *testing.T, q *Queue) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { q.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return cancel
}

func waitStatus(t *testing.T, q *Queue, id string, want Status) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := q.Get(id); ok && j.Status == want {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	j, _ := q.Get(id)
	t.Fatalf("job %s: status %q, want %q", id, j.Status, want)
	return j
}

func TestJobsRunSequentially(t *testing.T) {
	q, err := NewQueue("")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	started1 := make(chan struct{})
	started2 := make(chan struct{})
	a := q.Submit("k", "a", func(ctx context.Context, r Reporter) error {
		close(started1)
		<-release
		return nil
	})
	b := q.Submit("k", "b", func(ctx context.Context, r Reporter) error {
		close(started2)
		return nil
	})
	startRun(t, q)
	<-started1
	select {
	case <-started2:
		t.Fatal("second job started before first finished")
	case <-time.After(100 * time.Millisecond):
	}
	if j, _ := q.Get(b.ID); j.Status != StatusQueued {
		t.Fatalf("second job status %q, want queued", j.Status)
	}
	close(release)
	waitStatus(t, q, a.ID, StatusDone)
	waitStatus(t, q, b.ID, StatusDone)
}

func TestFailedJobRecordsError(t *testing.T) {
	q, _ := NewQueue("")
	startRun(t, q)
	j := q.Submit("k", "x", func(ctx context.Context, r Reporter) error { return errors.New("boom") })
	got := waitStatus(t, q, j.ID, StatusFailed)
	if got.Err != "boom" {
		t.Fatalf("Err = %q", got.Err)
	}
	p := q.Submit("k", "p", func(ctx context.Context, r Reporter) error { panic("oops") })
	got = waitStatus(t, q, p.ID, StatusFailed)
	if got.Err != "panic: oops" {
		t.Fatalf("Err = %q", got.Err)
	}
}

func TestSubscribeReceivesProgress(t *testing.T) {
	q, _ := NewQueue("")
	gate := make(chan struct{})
	j := q.Submit("k", "x", func(ctx context.Context, r Reporter) error {
		<-gate
		r.Progress(0.5)
		r.Logf("hello %d", 1)
		return nil
	})
	ch, unsub := q.Subscribe(j.ID)
	defer unsub()
	startRun(t, q)
	close(gate)
	var last Event
	sawHalf, sawLog := false, false
	timeout := time.After(5 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break loop
			}
			last = ev
			if ev.Job.Progress == 0.5 {
				sawHalf = true
			}
			if len(ev.Job.Log) == 1 && ev.Job.Log[0] == "hello 1" {
				sawLog = true
			}
		case <-timeout:
			t.Fatal("timeout")
		}
	}
	if !sawHalf || !sawLog {
		t.Fatalf("half=%v log=%v", sawHalf, sawLog)
	}
	if last.Job.Status != StatusDone {
		t.Fatalf("final status %q", last.Job.Status)
	}
	// finished job: one snapshot, then closed
	ch2, _ := q.Subscribe(j.ID)
	ev, ok := <-ch2
	if !ok || ev.Job.Status != StatusDone {
		t.Fatalf("got %v %v", ev, ok)
	}
	if _, ok := <-ch2; ok {
		t.Fatal("expected closed")
	}
	ch3, _ := q.Subscribe("nope")
	if _, ok := <-ch3; ok {
		t.Fatal("expected closed for unknown id")
	}
}

func TestSlowSubscriberSeesFinalState(t *testing.T) {
	q, _ := NewQueue("")
	j := q.Submit("k", "x", func(ctx context.Context, r Reporter) error {
		for i := 0; i < 200; i++ {
			r.Progress(float64(i) / 200)
		}
		return nil
	})
	ch, _ := q.Subscribe(j.ID)
	startRun(t, q)
	time.Sleep(300 * time.Millisecond)
	var last Event
	for ev := range ch {
		last = ev
	}
	if last.Job.Status != StatusDone {
		t.Fatalf("final %q", last.Job.Status)
	}
}

func TestRestartMarksRunningAsAborted(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "jobs.json")
	data, _ := json.Marshal([]Job{
		{ID: "a", Status: StatusRunning},
		{ID: "b", Status: StatusQueued},
		{ID: "c", Status: StatusDone},
	})
	if err := os.WriteFile(f, data, 0o600); err != nil {
		t.Fatal(err)
	}
	q, err := NewQueue(f)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Status{"a": StatusAborted, "b": StatusAborted, "c": StatusDone}
	for id, st := range want {
		if j, ok := q.Get(id); !ok || j.Status != st {
			t.Fatalf("%s: %v %q, want %q", id, ok, j.Status, st)
		}
	}
}

func TestPersistence(t *testing.T) {
	f := filepath.Join(t.TempDir(), "jobs.json")
	q, _ := NewQueue(f)
	startRun(t, q)
	j := q.Submit("k", "x", func(ctx context.Context, r Reporter) error { return nil })
	waitStatus(t, q, j.ID, StatusDone)
	q2, err := NewQueue(f)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := q2.Get(j.ID); !ok || got.Status != StatusDone {
		t.Fatalf("reloaded: %v %v", got, ok)
	}
}

func TestLogTruncated(t *testing.T) {
	q, _ := NewQueue("")
	startRun(t, q)
	j := q.Submit("k", "x", func(ctx context.Context, r Reporter) error {
		for i := 0; i < 700; i++ {
			r.Logf("line %d", i)
		}
		r.Progress(7)
		return nil
	})
	got := waitStatus(t, q, j.ID, StatusDone)
	if len(got.Log) != 500 || got.Log[0] != "line 200" || got.Log[499] != "line 699" {
		t.Fatalf("len=%d first=%q", len(got.Log), got.Log[0])
	}
	if got.Progress != 1 {
		t.Fatalf("progress %v", got.Progress)
	}
}

func TestCancelAbortsRunningJob(t *testing.T) {
	q, _ := NewQueue("")
	cancel := startRun(t, q)
	started := make(chan struct{})
	j := q.Submit("k", "x", func(ctx context.Context, r Reporter) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started
	cancel()
	waitStatus(t, q, j.ID, StatusAborted)
}

func TestListNewestFirstAndCap(t *testing.T) {
	q, _ := NewQueue("")
	startRun(t, q)
	var last Job
	for i := 0; i < 60; i++ {
		last = q.Submit("k", fmt.Sprint(i), func(ctx context.Context, r Reporter) error { return nil })
	}
	waitStatus(t, q, last.ID, StatusDone)
	l := q.List()
	if len(l) != 50 {
		t.Fatalf("len %d", len(l))
	}
	if l[0].ID != last.ID || l[0].Title != "59" {
		t.Fatalf("first %v", l[0])
	}
}

func TestPersistFailureIsLogged(t *testing.T) {
	sf := filepath.Join(t.TempDir(), "jobs.json")
	if err := os.Mkdir(sf+".tmp", 0o755); err != nil { // makes the tmp write fail
		t.Fatal(err)
	}
	q, err := NewQueue(sf)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	q.SetErrorLog(&buf)
	startRun(t, q)
	j := q.Submit("k", "x", func(ctx context.Context, r Reporter) error { return nil })
	waitStatus(t, q, j.ID, StatusDone)
	if q.LastPersistError() == nil {
		t.Fatal("expected persist error")
	}
	q.mu.Lock()
	out := buf.String()
	q.mu.Unlock()
	if !strings.Contains(out, "jobs: persist failed: ") {
		t.Fatalf("log = %q", out)
	}
}

func TestNilErrorAfterCancelIsDone(t *testing.T) {
	q, _ := NewQueue("")
	cancel := startRun(t, q)
	started := make(chan struct{})
	j := q.Submit("k", "x", func(ctx context.Context, r Reporter) error {
		close(started)
		<-ctx.Done()
		return nil
	})
	<-started
	cancel()
	waitStatus(t, q, j.ID, StatusDone)
}

func TestSetLinkShowsInGetAndState(t *testing.T) {
	f := filepath.Join(t.TempDir(), "jobs.json")
	q, _ := NewQueue(f)
	startRun(t, q)
	j := q.Submit("usb", "x", func(ctx context.Context, r Reporter) error {
		l, ok := r.(Linker)
		if !ok {
			return errors.New("reporter is not a Linker")
		}
		l.SetLink("/upload/a,b")
		return nil
	})
	got := waitStatus(t, q, j.ID, StatusDone)
	if got.Link != "/upload/a,b" {
		t.Fatalf("Link = %q", got.Link)
	}
	q2, err := NewQueue(f)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := q2.Get(j.ID); !ok || got.Link != "/upload/a,b" {
		t.Fatalf("reloaded: %+v %v", got, ok)
	}
	data, _ := os.ReadFile(f)
	if !strings.Contains(string(data), `"link": "/upload/a,b"`) {
		t.Fatalf("state:\n%s", data)
	}
}
