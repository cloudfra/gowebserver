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
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
)

// withDiagnostics turns diagnostics on for the test, into a file whose
// contents it returns.
func withDiagnostics(t *testing.T) (http.Handler, func() string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "diag.log")
	h, closeDiag, err := startDiagnostics(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := closeDiag(); err != nil {
			t.Error(err)
		}
	})
	return h, func() string {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
}

// secret are words of names that must never reach the log.
var secret = regexp.MustCompile(`(?i)secret|vacation|alice|beach|bob|birthday|home|users`)

func TestDiagnosticsNeverWriteNames(t *testing.T) {
	h, read := withDiagnostics(t)

	// The server's own events.
	diagEvent("server", "thumbnail", map[string]any{
		"file": anon("Alice beach 1.jpg"), "dir": anonPath("Secret Vacation/2024"),
		"err": errCategory(fmt.Errorf("ffmpeg: exit status 1: /home/users/Secret Vacation/Bob birthday.mp4: Invalid data found")),
	})
	diagEvent("server", "ffmpeg-frame", map[string]any{"file": diagFile("/home/users/Secret Vacation/Bob birthday.mp4", "")})
	diagEvent("server", "ffmpeg-frame", map[string]any{"file": diagFile("pipe:0", "Secret Vacation/Bob birthday.mp4")})
	diagEvent("server", "ffmpeg-frame", map[string]any{"file": diagFile("/tmp/gowebserver-video-123.mp4", "")})

	// The page's, with names wherever it might put them.
	body := strings.Join([]string{
		`{"ev":"thumb","url":"http://host:8080/Secret%20Vacation/Alice%20beach%201.jpg?width=256&height=144&fit=outside&x=Alice","ok":true,"ms":12}`,
		`{"ev":"page","page":"https://host/e/Secret%20Vacation/","n":7}`,
		`{"ev":"strip","why":"Alice beach","display":"none/block","timing":"Bob birthday.mp4","rect":[1,"Alice/x",3]}`,
		`{"ev":"thumb","Alice":"beach","name":"Secret/Vacation","kind":{"nested":"Bob"}}`,
		`{"ev":"Alice beach","ok":true}`,
	}, "\n")
	req := httptest.NewRequest(http.MethodPost, "/__diag", strings.NewReader(body))
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPad; CPU OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1 Alice")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d", rec.Code)
	}

	log := read()
	t.Log("\n" + log)
	if m := secret.FindString(log); m != "" {
		t.Errorf("the log has %q in it", m)
	}
	for _, want := range []string{
		"err=ffmpeg-invalid-data",
		"browser=ipad,safari-18",
		"file=temporary-copy.mp4",
		"?fit=outside&height=144&width=256",
		"ok=true ms=12",
		"client strip\n", // every field of it was unsafe
	} {
		if !strings.Contains(log, want) {
			t.Errorf("the log is missing %q", want)
		}
	}
	// The same name is the same token throughout, from the server or the
	// page.
	tok := anon("Alice beach 1.jpg")
	if !regexp.MustCompile(`^[0-9a-f]{10}\.jpg$`).MatchString(tok) {
		t.Errorf("token %q", tok)
	}
	if strings.Count(log, tok) != 2 {
		t.Errorf("token %s appears %d times, want 2 (server and page)", tok, strings.Count(log, tok))
	}
	if strings.Count(log, anon("Bob birthday.mp4")) != 2 {
		t.Error("a video piped and read from disk has different tokens")
	}
}

func TestDiagnosticsLines(t *testing.T) {
	_, read := withDiagnostics(t)
	diagEvent("server", "thumbnail", map[string]any{"ms": int64(812), "result": "made", "file": "abc.mp4", "zeta": 1.5, "err": "", "rect": []any{1.0, 2.0}, "why": "two words"})
	lines := strings.Split(strings.TrimSpace(read()), "\n")
	last := lines[len(lines)-1]
	want := regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z server thumbnail file=abc\.mp4 result=made why="two words" ms=812 rect=1,2 zeta=1\.50$`)
	if !want.MatchString(last) {
		t.Errorf("line %q", last)
	}
	if !strings.HasPrefix(lines[0], "# ") {
		t.Errorf("no header: %q", lines[0])
	}
}

func TestErrCategory(t *testing.T) {
	for err, want := range map[error]string{
		nil:                                 "",
		errThumbnailUnsupported:             "unsupported",
		fmt.Errorf("x: %w", os.ErrNotExist): "not-exist",
		errors.New("ffmpeg: moov atom not found in /a/b.mp4"):    "ffmpeg-moov-not-found",
		errors.New("ffmpeg: something nobody has seen /a/b.mp4"): "other",
	} {
		if got := errCategory(err); got != want {
			t.Errorf("errCategory(%v) = %q, want %q", err, got, want)
		}
	}
}

func TestDiagPageEndpoint(t *testing.T) {
	if got := diagPageEndpoint("/a/"); got != "" {
		t.Errorf("off: %q", got)
	}
	withDiagnostics(t)
	for uri, want := range map[string]string{
		"/":              "./__diag",
		"/a/":            "./../__diag",
		"/e/a%20b/c/?x=": "./../../../__diag",
	} {
		if got := diagPageEndpoint(uri); got != want {
			t.Errorf("diagPageEndpoint(%q) = %q, want %q", uri, got, want)
		}
	}
}

// A folder named like a file keeps no "extension": it's part of its name.
func TestDiagnosticsFolderNames(t *testing.T) {
	h, read := withDiagnostics(t)
	diagEvent("server", "thumbnail", map[string]any{"file": anon("clip.mp4"), "dir": anonDir("Vacation.com/Beach.2024")})
	body := `{"ev":"page","page":"https://host/e/Vacation.com/Beach.2024/"}` + "\n" +
		`{"ev":"thumb","url":"https://host/e/Vacation.com/clip.mp4?width=256"}` + "\n" +
		`{"ev":"js-error","name":"TypeError","line":12,"col":19,"stack":"loadTile<render<tick"}` + "\n" +
		`{"ev":"js-error","name":"TypeError","stack":"Alice beach<x"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__diag", strings.NewReader(body)))
	log := read()
	t.Log("\n" + log)
	if strings.Contains(log, ".com") || strings.Contains(log, ".2024") {
		t.Error("a folder's extension is in the log")
	}
	if !strings.Contains(log, anon("clip.mp4")) || !strings.HasSuffix(anon("clip.mp4"), ".mp4") {
		t.Error("a file lost its extension")
	}
	if !strings.Contains(log, "stack=loadTile<render<tick") {
		t.Error("a stack of function names was dropped")
	}
	if strings.Contains(log, "Alice") {
		t.Error("a stack with a space in it got through")
	}
}

func TestErrCategoryNotStarted(t *testing.T) {
	notStarted := func(err error) error { return fmt.Errorf("%w: %w", ffmpeg.ErrNotStarted, err) }
	for err, want := range map[error]string{
		notStarted(&fs.PathError{Op: "fork/exec", Path: "/Secret/ffmpeg", Err: syscall.EACCES}):     "ffmpeg-not-started:permission-denied",
		notStarted(&exec.Error{Name: "ffmpeg", Err: exec.ErrDot}):                                   "ffmpeg-not-started:found-in-working-folder",
		notStarted(&fs.PathError{Op: "fork/exec", Path: "/Secret/ffmpeg", Err: syscall.Errno(193)}): "ffmpeg-not-started:errno-193",
		errFFmpegUnavailable: "ffmpeg-unavailable",
	} {
		if got := errCategory(err); got != want {
			t.Errorf("errCategory(%v) = %q, want %q", err, got, want)
		}
	}
}

// Off, nothing is gathered or written, and names stay as they are (the
// callers only ask for tokens when diagnostics are on).
func TestDiagnosticsOff(t *testing.T) {
	if diagEnabled() {
		t.Fatal("diagnostics on before any test turned them on")
	}
	diagEvent("server", "thumbnail", map[string]any{"file": "a.jpg"}) // no file to write to, no panic
	if got := diagPageEndpoint("/a/"); got != "" {
		t.Errorf("page endpoint %q with diagnostics off", got)
	}
}

// The file stops growing at diagMaxBytes, and says so.
func TestDiagnosticsFull(t *testing.T) {
	_, read := withDiagnostics(t)
	d := diagActive.Load()
	d.mu.Lock()
	d.written = diagMaxBytes - 100
	d.mu.Unlock()
	diagEvent("server", "thumbnail", map[string]any{"file": anon("a.jpg"), "spec": strings.Repeat("x", 200)})
	diagEvent("server", "thumbnail", map[string]any{"file": anon("b.jpg")})
	log := read()
	if !strings.Contains(log, "# diagnostics stopped") {
		t.Errorf("no note that the file is full:\n%s", log)
	}
	if strings.Contains(log, anon("b.jpg")) || strings.Contains(log, strings.Repeat("x", 200)) {
		t.Error("events written past the limit")
	}
}

// A thumbnail request is an event with tokens for its file and folder.
func TestDiagnosticsThumbnailEvent(t *testing.T) {
	_, read := withDiagnostics(t)
	photos := fstest.MapFS{"Secret Vacation/Alice beach.png": {Data: encodePNG(t, stripes(64, 36))}}
	h, _ := newTestThumbnailHandler(t, photos, "")
	decodeThumb(t, thumbGet(t, h, "/Secret%20Vacation/Alice%20beach.png?width=32", nil))
	log := read()
	want := "server thumbnail file=" + anon("Alice beach.png") + " dir=" + anonDir("Secret Vacation") + " kind=photo result=made"
	if !strings.Contains(log, want) {
		t.Errorf("missing %q in\n%s", want, log)
	}
	if m := secret.FindString(log); m != "" {
		t.Errorf("the log has %q in it", m)
	}
}
