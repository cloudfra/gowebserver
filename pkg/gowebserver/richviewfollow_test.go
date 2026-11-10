// Copyright 2026 Cloudfra
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gowebserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// followRecorder is a response writer a test reads while the stream is still
// running, so it records under a mutex: the follow loop writes from its own
// goroutine. Its Flush method is what makes http.NewResponseController find
// the stream, which httptest.ResponseRecorder cannot do.
type followRecorder struct {
	mu      sync.Mutex
	header  http.Header
	code    int
	body    bytes.Buffer
	flushes int
}

func (r *followRecorder) Header() http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.header == nil {
		r.header = http.Header{}
	}
	return r.header
}

func (r *followRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.Write(p)
}

func (r *followRecorder) WriteHeader(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.code = code
}

func (r *followRecorder) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushes++
}

func (r *followRecorder) status() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code == 0 {
		return http.StatusOK
	}
	return r.code
}

func (r *followRecorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

func (r *followRecorder) contentType() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.header == nil {
		return ""
	}
	return r.header.Get("Content-Type")
}

// followEvent is one event of a stream, as a browser's EventSource would see
// it: a name, the position it left the log at, and its JSON payload.
type followEvent struct {
	id    string
	event string
	data  string
}

// parseFollowStream splits what was written so far into its events, ignoring
// the keep-alive comments between them.
func parseFollowStream(t *testing.T, raw string) []followEvent {
	t.Helper()
	var out []followEvent
	for _, block := range strings.Split(raw, "\n\n") {
		var ev followEvent
		var named bool
		for _, line := range strings.Split(block, "\n") {
			name, value, ok := strings.Cut(line, ": ")
			if !ok {
				continue
			}
			switch name {
			case "id":
				ev.id, named = value, true
			case "event":
				ev.event, named = value, true
			case "data":
				ev.data, named = value, true
			}
		}
		if named {
			out = append(out, ev)
		}
	}
	return out
}

// waitForFollow waits for a stream to look the way the test expects, since
// its lines arrive on a timer rather than when the test asks.
func waitForFollow(t *testing.T, what string, rec *followRecorder, want func(rec *followRecorder) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if want(rec) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; stream so far: %q", what, rec.text())
		}
		time.Sleep(time.Millisecond)
	}
}

// followSawEvent reports whether the stream so far has an event of this name,
// which is more than looking for its text: another name would match that too.
func followSawEvent(t *testing.T, raw, name string) bool {
	t.Helper()
	for _, ev := range parseFollowStream(t, raw) {
		if ev.event == name {
			return true
		}
	}
	return false
}

// snippet shows the interesting part of a rendered page instead of all of it,
// which would be a whole stylesheet and script.
func snippet(body, around string) string {
	i := strings.Index(body, around)
	if i < 0 {
		if len(body) > 200 {
			return body[:200] + "..."
		}
		return body
	}
	from := max(i-120, 0)
	to := min(i+len(around)+120, len(body))
	return "..." + body[from:to] + "..."
}

// makeFollowHandler builds a text viewer over a real directory, where the
// test can write to a log while it is being followed.
func makeFollowHandler(t *testing.T, dir string) *richViewHandler {
	t.Helper()
	h := makeRichViewHandler(t, nil)
	h.baseFS = os.DirFS(dir)
	h.pollInterval = 2 * time.Millisecond
	return h
}

// startFollow runs a follow request in its own goroutine and hands back the
// recorder to watch, the stream's cancel, and a function that waits for it to
// end and says how far it got.
func startFollow(t *testing.T, h *richViewHandler, target string) (*followRecorder, context.CancelFunc, func() string) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	rec := &followRecorder{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.serveFollow(rec, req, strings.TrimPrefix(req.URL.Path, "/"))
	}()
	stop := func() string {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the follow stream did not stop when its request was canceled")
		}
		return rec.text()
	}
	return rec, cancel, stop
}

// removeFollowLog deletes a log while a stream is following it. Go opens a
// file on Windows with no FILE_SHARE_DELETE, so removing one while the follow
// loop has it open fails with a sharing violation until that look is over,
// which is why this tries again rather than taking the first answer.
func removeFollowLog(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := os.Remove(name)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed to remove %s: %s", name, err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestIsFollowable(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{name: "app.log", want: true},
		{name: "APP.LOG", want: true},
		{name: "app.log.1", want: true},
		{name: "nginx.error.log.gz", want: false},
		{name: "syslog", want: true},
		{name: "messages", want: true},
		{name: "dmesg", want: true},
		{name: "notes.txt", want: false},
		{name: "main.go", want: false},
		{name: "catalog.log", want: true},
	} {
		if got := isFollowable(tc.name); got != tc.want {
			t.Errorf("isFollowable(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFollowStateFrom(t *testing.T) {
	// A reconnecting browser says where its own stream left off, which beats
	// the offset and line the page started from.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/app.log?view=rich&follow=1&offset=0&line=1", nil)
	req.Header.Set("Last-Event-ID", "1024:57")
	if got := followStateFrom(req); got.offset != 1024 || got.line != 57 {
		t.Errorf("followStateFrom with Last-Event-ID = %+v, want offset 1024 and line 57", got)
	}

	req = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/app.log?view=rich&follow=1&offset=64&line=9", nil)
	if got := followStateFrom(req); got.offset != 64 || got.line != 9 {
		t.Errorf("followStateFrom with the page's handoff = %+v, want offset 64 and line 9", got)
	}

	// Garbage in either place starts the log over rather than trusting it.
	req = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/app.log?view=rich&follow=1&offset=-4&line=0", nil)
	req.Header.Set("Last-Event-ID", "nonsense")
	if got := followStateFrom(req); got.offset != 0 || got.line != 1 {
		t.Errorf("followStateFrom with garbage = %+v, want offset 0 and line 1", got)
	}

	if got := (followState{offset: 12, line: 4}).eventID(); got != "12:4" {
		t.Errorf("eventID = %q, want %q", got, "12:4")
	}
}

func TestFollowHandler_StreamsLinesAsTheyAreWritten(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "app.log")
	if err := os.WriteFile(log, []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := makeFollowHandler(t, dir)

	rec, _, stop := startFollow(t, h, "/app.log?view=rich&follow=1&offset=0&line=1")
	defer stop()

	// The stream's own goroutine sets these, so wait for it to have got there.
	waitForFollow(t, "the stream's headers", rec, func(rec *followRecorder) bool {
		return rec.contentType() != ""
	})
	if got := rec.contentType(); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := rec.status(); got != http.StatusOK {
		t.Errorf("status = %d, want 200", got)
	}

	// Everything the page did not already show, in one event.
	waitForFollow(t, "the log's first two lines", rec, func(rec *followRecorder) bool {
		return strings.Contains(rec.text(), "second")
	})

	if f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o600); err != nil {
		t.Fatal(err)
	} else if _, err := f.WriteString("third\n"); err != nil {
		t.Fatal(err)
	} else if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	waitForFollow(t, "the line appended to the log", rec, func(rec *followRecorder) bool {
		return strings.Contains(rec.text(), "third")
	})

	raw := stop()
	events := parseFollowStream(t, raw)
	if len(events) < 2 {
		t.Fatalf("got %d events, want at least 2: %q", len(events), raw)
	}
	frames := make([]followFrame, 0, len(events))
	for _, ev := range events {
		if ev.event != "lines" {
			t.Fatalf("event %q is not a lines event: %q", ev.event, ev.id+" "+ev.data)
		}
		var frame followFrame
		if err := json.Unmarshal([]byte(ev.data), &frame); err != nil {
			t.Fatalf("event data %q is not a frame: %s", ev.data, err)
		}
		frames = append(frames, frame)
		if ev.id == "" {
			t.Errorf("a lines event has no id for a browser to resume from: %q", ev.data)
		}
	}

	// The two chunks of the log continue each other's line numbers, which is
	// what lets the page number them as one file.
	if frames[0].Start != 1 || frames[0].Count != 2 {
		t.Errorf("the first frame covers lines %d..%d, want 1..2", frames[0].Start, frames[0].Start+frames[0].Count-1)
	}
	if frames[1].Start != 3 || frames[1].Count != 1 {
		t.Errorf("the second frame covers lines %d..%d, want 3..3", frames[1].Start, frames[1].Start+frames[1].Count-1)
	}
	if !strings.Contains(frames[1].Lines, `id="L3"`) {
		t.Errorf("the appended line is not numbered 3: %q", frames[1].Lines)
	}
	if !strings.Contains(frames[1].Lines, "third") {
		t.Errorf("the appended line is missing from the frame: %q", frames[1].Lines)
	}
	if frames[1].Size == "" {
		t.Error("a frame does not say how large the log is now")
	}

	if rec.flushes == 0 {
		t.Error("the stream never flushed, so its lines would sit in a buffer")
	}
}

func TestFollowHandler_HoldsBackAPartialLine(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "app.log")
	if err := os.WriteFile(log, []byte("half a"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := makeFollowHandler(t, dir)

	rec, _, stop := startFollow(t, h, "/app.log?view=rich&follow=1&offset=0&line=1")
	defer stop()

	time.Sleep(50 * time.Millisecond)
	if events := parseFollowStream(t, rec.text()); len(events) > 0 {
		t.Errorf("a line without its newline was streamed: %q", rec.text())
	}

	if f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o600); err != nil {
		t.Fatal(err)
	} else if _, err := f.WriteString(" line\n"); err != nil {
		t.Fatal(err)
	} else if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	waitForFollow(t, "the finished line", rec, func(rec *followRecorder) bool {
		return strings.Contains(rec.text(), "half a line")
	})
}

func TestFollowHandler_StartsOverWhenTheLogIsRotated(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "app.log")
	if err := os.WriteFile(log, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := makeFollowHandler(t, dir)

	rec, _, stop := startFollow(t, h, "/app.log?view=rich&follow=1&offset=0&line=1")
	defer stop()
	waitForFollow(t, "the log's three lines", rec, func(rec *followRecorder) bool {
		return strings.Contains(rec.text(), "three")
	})

	// A log rotated in place is shorter than the reader has got to, so its
	// new lines start at the top again and the page drops what it has.
	if err := os.WriteFile(log, []byte("fresh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForFollow(t, "the rotated log's first line", rec, func(rec *followRecorder) bool {
		return strings.Contains(rec.text(), "fresh")
	})

	var last followFrame
	for _, ev := range parseFollowStream(t, stop()) {
		var frame followFrame
		if err := json.Unmarshal([]byte(ev.data), &frame); err != nil {
			t.Fatalf("event data %q is not a frame: %s", ev.data, err)
		}
		last = frame
	}
	if last.Start != 1 || last.Count != 1 {
		t.Errorf("the rotated log arrived at line %d, want line 1", last.Start)
	}
}

func TestFollowHandler_SaysNothingWhileTheRotatedLogIsEmpty(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "app.log")
	if err := os.WriteFile(log, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := makeFollowHandler(t, dir)

	rec, _, stop := startFollow(t, h, "/app.log?view=rich&follow=1&offset=0&line=1")
	defer stop()
	waitForFollow(t, "the log's two lines", rec, func(rec *followRecorder) bool {
		return strings.Contains(rec.text(), "two")
	})

	// A log rotated in place is empty for a moment between its rename and its
	// replacement's first line. There is nothing to tell the browser in that
	// moment, and a frame with no lines in it would clear the page before
	// anything had arrived to fill it again.
	if err := os.Truncate(log, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := len(parseFollowStream(t, rec.text())); got != 1 {
		t.Errorf("got %d events with the log empty, want only the 1 it had already: %q", got, rec.text())
	}

	if err := os.WriteFile(log, []byte("fresh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForFollow(t, "the new log's first line", rec, func(rec *followRecorder) bool {
		return strings.Contains(rec.text(), "fresh")
	})
	if !strings.Contains(rec.text(), `"start":1`) {
		t.Errorf("the new log did not start over from its top: %s", snippet(rec.text(), `"start"`))
	}
}

func TestFollowHandler_StartsOverWhenTheStartIsPastTheEnd(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "app.log")
	if err := os.WriteFile(log, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The page rendered five lines, but the log was replaced before the
	// browser asked for it, so the start the page sent is past the end.
	// Pulling that start back to the end would number the new log's lines
	// from line six; it is a different log and starts at its own top.
	if err := os.WriteFile(log, []byte("fresh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := makeFollowHandler(t, dir)

	rec, _, stop := startFollow(t, h, "/app.log?view=rich&follow=1&offset=32&line=6")
	defer stop()
	waitForFollow(t, "the log's first line", rec, func(rec *followRecorder) bool {
		return strings.Contains(rec.text(), "fresh")
	})

	var last followFrame
	for _, ev := range parseFollowStream(t, stop()) {
		var frame followFrame
		if err := json.Unmarshal([]byte(ev.data), &frame); err != nil {
			t.Fatalf("event data %q is not a frame: %s", ev.data, err)
		}
		last = frame
	}
	if last.Start != 1 || last.Count != 1 {
		t.Errorf("the log arrived at line %d, want line 1", last.Start)
	}
	if strings.Contains(rec.text(), "\"start\":6") {
		t.Errorf("the log's lines carried the old numbering: %s", snippet(rec.text(), "\"start\""))
	}
}

func TestFollowHandler_SaysGoneWhenTheLogIsDeleted(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "app.log")
	if err := os.WriteFile(log, []byte("only line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := makeFollowHandler(t, dir)

	rec, _, stop := startFollow(t, h, "/app.log?view=rich&follow=1&offset=0&line=1")
	defer stop()
	waitForFollow(t, "the log's line", rec, func(rec *followRecorder) bool {
		return strings.Contains(rec.text(), "only line")
	})

	removeFollowLog(t, log)
	waitForFollow(t, "the gone event", rec, func(rec *followRecorder) bool {
		return followSawEvent(t, rec.text(), "gone")
	})
	stop()
}

// followUnopenableFS is a log file system in which some opens are refused.
// This is what a log whose deletion is still waiting for another handle to
// close looks like on Windows: it is still there to ask about, but opening it
// says it is not allowed rather than that it is gone.
type followUnopenableFS struct {
	fs.FS
	mu    sync.Mutex
	opens int // opens answered normally before the refusals start
	refs  int // opens answered with a refusal
}

func (f *followUnopenableFS) Open(name string) (fs.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.opens > 0:
		f.opens--
	case f.refs > 0:
		f.refs--
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return f.FS.Open(name)
}

// Stat answers from the file underneath, which is what keeps a refused open
// from looking like a log that is missing.
func (f *followUnopenableFS) Stat(name string) (fs.FileInfo, error) {
	return fs.Stat(f.FS, name)
}

// refusalsLeft is how many injected refusals a stream never got to see, which
// is how a test knows the looks it means to check really happened.
func (f *followUnopenableFS) refusalsLeft() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refs
}

func TestFollowHandler_LooksAgainWhenTheLogCannotBeOpened(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "app.log")
	if err := os.WriteFile(log, []byte("only line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := makeFollowHandler(t, dir)
	// The open that checks the log can be followed at all gets through, and
	// the three looks after it are refused.
	unopenable := &followUnopenableFS{FS: h.baseFS, opens: 1, refs: 3}
	h.baseFS = unopenable

	rec, _, stop := startFollow(t, h, "/app.log?view=rich&follow=1&offset=0&line=1")
	defer stop()
	waitForFollow(t, "the log's line", rec, func(rec *followRecorder) bool {
		return strings.Contains(rec.text(), "only line")
	})
	if left := unopenable.refusalsLeft(); left != 0 {
		t.Errorf("%d refused looks went unused, so the stream did not look again as this test means", left)
	}

	removeFollowLog(t, log)
	// A log that could not be opened for a moment is not a log that went away:
	// the looks after it are what find out which, and say gone here.
	waitForFollow(t, "the gone event", rec, func(rec *followRecorder) bool {
		return followSawEvent(t, rec.text(), "gone")
	})
	stop()
}

func TestFollowHandler_GivesUpOnALogItCannotOpenAtAll(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "app.log")
	if err := os.WriteFile(log, []byte("only line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := makeFollowHandler(t, dir)
	refusals := followMaxReadErrors * 2
	unopenable := &followUnopenableFS{FS: h.baseFS, opens: 1, refs: refusals}
	h.baseFS = unopenable

	// This stream has to end by itself, so it is not started with startFollow,
	// whose stop would cancel the look that is meant to give up.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/app.log?view=rich&follow=1&offset=0&line=1", nil)
	rec := &followRecorder{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.serveFollow(rec, req, "app.log")
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the follow stream kept going on a log it cannot open")
	}

	if got := refusals - unopenable.refusalsLeft(); got != followMaxReadErrors {
		t.Errorf("the stream gave up on a log it cannot open after %d looks, want %d", got, followMaxReadErrors)
	}
	if raw := rec.text(); followSawEvent(t, raw, "gone") {
		t.Errorf("a log that could not be opened was streamed as a gone log: %q", raw)
	}
}

func TestFollowHandler_ResumesFromLastEventID(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "app.log")
	if err := os.WriteFile(log, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := makeFollowHandler(t, dir)

	// The page says it showed the first two lines, so only the third is the
	// browser's to see; the offset and line the page sent are wrong on
	// purpose to show which of the two wins.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/app.log?view=rich&follow=1&offset=0&line=1", nil)
	req.Header.Set("Last-Event-ID", (followState{offset: 8, line: 3}).eventID())
	rec := &followRecorder{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.serveFollow(rec, req, "app.log")
	}()
	waitForFollow(t, "the log's last line", rec, func(rec *followRecorder) bool {
		return strings.Contains(rec.text(), "three")
	})
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the follow stream did not stop when its request was canceled")
	}

	raw := rec.text()
	if strings.Contains(raw, "one") || strings.Contains(raw, "two") {
		t.Errorf("the stream replayed lines the browser had already seen: %q", raw)
	}
}

func TestFollowHandler_Refusals(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main\n")
	write("app.log", "\x89PNG\r\n\x1a\n not a text log")
	write("empty.log", "")
	h := makeFollowHandler(t, dir)

	for _, tc := range []struct {
		name   string
		target string
		want   int
	}{
		{name: "not a log file", target: "/main.go?view=rich&follow=1", want: http.StatusNotFound},
		{name: "a log of binary bytes", target: "/app.log?view=rich&follow=1", want: http.StatusUnsupportedMediaType},
		{name: "no such file", target: "/missing.log?view=rich&follow=1", want: http.StatusNotFound},
		{name: "a directory", target: "/?view=rich&follow=1", want: http.StatusNotFound},
		{name: "an empty log", target: "/empty.log?view=rich&follow=1", want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, _, stop := startFollow(t, h, tc.target)
			raw := stop()
			if got := rec.status(); got != tc.want {
				t.Errorf("status = %d, want %d; body %q", got, tc.want, raw)
			}
		})
	}

	t.Run("too many logs followed at once", func(t *testing.T) {
		h.followSlots = make(chan struct{}, 1)
		h.followSlots <- struct{}{}
		rec, _, stop := startFollow(t, h, "/empty.log?view=rich&follow=1")
		raw := stop()
		if got := rec.status(); got != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want %d; body %q", got, http.StatusServiceUnavailable, raw)
		}
		if got := rec.Header().Get("Retry-After"); got == "" {
			t.Error("a refused stream does not say when to try again")
		}
	})
}

// TestRichViewHandler_LogFollowHandoff checks that the page a log renders
// tells the browser where its stream is, and exactly where the page stopped
// reading, so the two never show a line twice or skip one.
func TestRichViewHandler_LogFollowHandoff(t *testing.T) {
	h := makeRichViewHandler(t, map[string][]byte{
		"app.log":     []byte("one\ntwo\n"),
		"partial.log": []byte("one\ntwo"),
		"notes.txt":   []byte("one\ntwo\n"),
	})

	for _, tc := range []struct {
		name       string
		offset     string
		line       string
		wantOffset string
		wantLine   string
	}{
		{name: "app.log", wantOffset: "8", wantLine: "3"},
		// The page cannot show a last line with no newline as finished, so
		// the stream starts that line again and replaces it.
		{name: "partial.log", wantOffset: "4", wantLine: "2"},
		// Only logs are followed.
		{name: "notes.txt", wantOffset: "", wantLine: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/"+tc.name+"?view=rich", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			body := rec.Body.String()

			marker := `data-follow-url="./` + tc.name + `?view=rich&amp;follow=1"`
			if tc.wantOffset == "" {
				// The page's own script mentions the attribute by name, so
				// look for the attribute being set rather than the word.
				if strings.Contains(body, `data-follow-url="`) {
					t.Errorf("a file that is not a log offers a stream: %q", snippet(body, `data-follow-url="`))
				}
				return
			}
			if !strings.Contains(body, marker) {
				t.Errorf("the page has no stream link for %s: %q", tc.name, snippet(body, "data-follow-"))
			}
			for _, want := range []string{
				`data-follow-offset="` + tc.wantOffset + `"`,
				`data-follow-line="` + tc.wantLine + `"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("the page does not hand off with %s: %q", want, snippet(body, "data-follow-"))
				}
			}
		})
	}
}
