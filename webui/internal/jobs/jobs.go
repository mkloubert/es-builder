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

// Package jobs implements a background job queue that runs one job at a time,
// publishes live progress to subscribers and persists the job list.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Status is the lifecycle state of a job.
type Status string

const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
	StatusAborted Status = "aborted"
)

const (
	maxJobs     = 50
	maxLogLines = 500
	subBuffer   = 16
)

func (s Status) terminal() bool {
	return s == StatusDone || s == StatusFailed || s == StatusAborted
}

// Job is a snapshot of a queued or finished job.
type Job struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Title    string    `json:"title"`
	Status   Status    `json:"status"`
	Progress float64   `json:"progress"`
	Log      []string  `json:"log"`
	Err      string    `json:"err,omitempty"`
	Link     string    `json:"link,omitempty"` // page with the job's result, e.g. a review page
	Created  time.Time `json:"created"`
}

// Reporter lets a running job report progress and log lines.
type Reporter interface {
	Progress(p float64)
	Logf(format string, a ...any)
}

// Linker is an optional interface of a Reporter: it sets the link to the
// page with the job's result. The queue's reporter implements it.
type Linker interface {
	SetLink(url string)
}

// Event carries a snapshot of a job to subscribers.
type Event struct{ Job Job }

type entry struct {
	job  Job
	fn   func(ctx context.Context, r Reporter) error
	subs map[int]chan Event
}

// Queue runs submitted jobs one at a time in FIFO order.
type Queue struct {
	stateFile string
	wake      chan struct{}

	mu      sync.Mutex
	entries []*entry // oldest first
	nextSub int
	errLog  io.Writer
	lastErr error
	running atomic.Bool
}

// NewQueue creates a queue and loads stateFile if it exists. Jobs that were
// queued or running when the state was written are marked aborted.
// An empty stateFile disables persistence.
func NewQueue(stateFile string) (*Queue, error) {
	q := &Queue{stateFile: stateFile, wake: make(chan struct{}, 1), errLog: os.Stderr}
	if stateFile == "" {
		return q, nil
	}
	data, err := os.ReadFile(stateFile)
	if errors.Is(err, os.ErrNotExist) {
		return q, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Job // newest first
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("jobs: parse %s: %w", stateFile, err)
	}
	changed := false
	for i := len(list) - 1; i >= 0; i-- {
		j := list[i]
		if !j.Status.terminal() {
			j.Status = StatusAborted
			if j.Err == "" {
				j.Err = "interrupted by restart"
			}
			changed = true
		}
		q.entries = append(q.entries, &entry{job: j})
	}
	q.trimLocked()
	if changed {
		q.persistLocked()
	}
	return q, nil
}

func newID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Submit enqueues a job and returns its initial snapshot.
func (q *Queue) Submit(kind, title string, fn func(ctx context.Context, r Reporter) error) Job {
	q.mu.Lock()
	e := &entry{
		job: Job{ID: newID(), Kind: kind, Title: title, Status: StatusQueued, Log: []string{}, Created: time.Now()},
		fn:  fn,
	}
	q.entries = append(q.entries, e)
	q.trimLocked()
	q.persistLocked()
	snap := snapshot(e)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return snap
}

func snapshot(e *entry) Job {
	j := e.job
	j.Log = append([]string{}, e.job.Log...)
	return j
}

// trimLocked keeps at most maxJobs entries, dropping the oldest terminal ones.
func (q *Queue) trimLocked() {
	for len(q.entries) > maxJobs {
		dropped := false
		for i, e := range q.entries {
			if e.job.Status.terminal() {
				q.entries = append(q.entries[:i], q.entries[i+1:]...)
				dropped = true
				break
			}
		}
		if !dropped {
			return
		}
	}
}

// SetErrorLog sets where persistence failures are reported (default stderr).
func (q *Queue) SetErrorLog(w io.Writer) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.errLog = w
}

// LastPersistError returns the error of the most recent state-file write
// (nil if it succeeded or persistence is disabled).
func (q *Queue) LastPersistError() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.lastErr
}

// persistLocked writes the state file. Failures are logged and remembered but
// never fail a job.
func (q *Queue) persistLocked() {
	if q.stateFile == "" {
		return
	}
	err := q.writeState()
	q.lastErr = err
	if err != nil && q.errLog != nil {
		fmt.Fprintf(q.errLog, "jobs: persist failed: %v\n", err)
	}
}

func (q *Queue) writeState() error {
	list := make([]Job, 0, len(q.entries))
	for i := len(q.entries) - 1; i >= 0; i-- {
		list = append(list, snapshot(q.entries[i]))
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(q.stateFile), 0o755); err != nil {
		return err
	}
	tmp := q.stateFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, q.stateFile); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// notifyLocked sends a snapshot to all subscribers of e; on terminal status it
// then closes and removes them. Sends never block: a full buffer drops its
// oldest event first.
func (q *Queue) notifyLocked(e *entry) {
	ev := Event{Job: snapshot(e)}
	for id, ch := range e.subs {
		send(ch, ev)
		if ev.Job.Status.terminal() {
			close(ch)
			delete(e.subs, id)
		}
	}
}

func send(ch chan Event, ev Event) {
	for {
		select {
		case ch <- ev:
			return
		default:
		}
		select {
		case <-ch:
		default:
		}
	}
}

// Get returns a snapshot of the job with the given id.
func (q *Queue) Get(id string) (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if e := q.findLocked(id); e != nil {
		return snapshot(e), true
	}
	return Job{}, false
}

func (q *Queue) findLocked(id string) *entry {
	for _, e := range q.entries {
		if e.job.ID == id {
			return e
		}
	}
	return nil
}

// List returns all kept jobs, newest first.
func (q *Queue) List() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Job, 0, len(q.entries))
	for i := len(q.entries) - 1; i >= 0; i-- {
		out = append(out, snapshot(q.entries[i]))
	}
	return out
}

// Subscribe returns a channel of snapshots for the job. The channel is closed
// after the final snapshot of a finished job. The returned func unsubscribes.
func (q *Queue) Subscribe(id string) (<-chan Event, func()) {
	ch := make(chan Event, subBuffer)
	q.mu.Lock()
	defer q.mu.Unlock()
	e := q.findLocked(id)
	if e == nil {
		close(ch)
		return ch, func() {}
	}
	ch <- Event{Job: snapshot(e)}
	if e.job.Status.terminal() {
		close(ch)
		return ch, func() {}
	}
	if e.subs == nil {
		e.subs = map[int]chan Event{}
	}
	q.nextSub++
	sid := q.nextSub
	e.subs[sid] = ch
	return ch, func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		if c, ok := e.subs[sid]; ok {
			delete(e.subs, sid)
			close(c)
		}
	}
}

type reporter struct {
	q *Queue
	e *entry
}

func (r reporter) Progress(p float64) {
	if p < 0 || p != p {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	r.q.mu.Lock()
	defer r.q.mu.Unlock()
	if r.e.job.Status != StatusRunning {
		return
	}
	r.e.job.Progress = p
	r.q.notifyLocked(r.e)
}

func (r reporter) Logf(format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	r.q.mu.Lock()
	defer r.q.mu.Unlock()
	if r.e.job.Status != StatusRunning {
		return
	}
	l := append(r.e.job.Log, line)
	if len(l) > maxLogLines {
		l = append([]string(nil), l[len(l)-maxLogLines:]...)
	}
	r.e.job.Log = l
	r.q.notifyLocked(r.e)
}

// SetLink sets the job's link while the job runs.
func (r reporter) SetLink(url string) {
	r.q.mu.Lock()
	defer r.q.mu.Unlock()
	if r.e.job.Status != StatusRunning {
		return
	}
	r.e.job.Link = url
	r.q.persistLocked()
	r.q.notifyLocked(r.e)
}

// Run processes queued jobs one at a time until ctx is cancelled. Cancelling
// ctx cancels the running job; remaining queued jobs stay queued.
func (q *Queue) Run(ctx context.Context) {
	if !q.running.CompareAndSwap(false, true) {
		return // another Run is active
	}
	defer q.running.Store(false)
	for {
		if ctx.Err() != nil {
			return
		}
		e := q.nextQueued()
		if e == nil {
			select {
			case <-ctx.Done():
				return
			case <-q.wake:
			}
			continue
		}
		q.execute(ctx, e)
	}
}

func (q *Queue) nextQueued() *entry {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, e := range q.entries {
		if e.job.Status == StatusQueued && e.fn != nil {
			return e
		}
	}
	return nil
}

func (q *Queue) execute(ctx context.Context, e *entry) {
	q.mu.Lock()
	e.job.Status = StatusRunning
	q.persistLocked()
	q.notifyLocked(e)
	q.mu.Unlock()

	err := runSafely(ctx, e.fn, reporter{q, e})

	q.mu.Lock()
	defer q.mu.Unlock()
	switch {
	case err != nil && ctx.Err() != nil:
		e.job.Status = StatusAborted
		if !errors.Is(err, context.Canceled) {
			e.job.Err = err.Error()
		}
	case err != nil:
		e.job.Status = StatusFailed
		e.job.Err = err.Error()
	default:
		e.job.Status = StatusDone
		e.job.Progress = 1
	}
	e.fn = nil
	q.trimLocked()
	q.persistLocked()
	q.notifyLocked(e)
}

func runSafely(ctx context.Context, fn func(context.Context, Reporter) error, r Reporter) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return fn(ctx, r)
}
