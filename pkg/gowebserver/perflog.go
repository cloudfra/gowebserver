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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// The performance log (monitoring.perfLog) is for finding out why
// browsing is slow on a device that can't be inspected directly, such as
// Safari on an iPad. Every request the server answers is a line in it,
// and every HTML page gets perf-log.js, which posts what the page does
// in the browser (frame times, video events, request timings, taps,
// errors) to perfLogEventsPath; those are lines too. A cookie ties a
// page's requests to its events. Each line is one JSON object, with
// "src" "server" or "client".

const (
	perfLogScriptPath = "/__perf.js"
	perfLogEventsPath = "/__perf"
	perfLogCookie     = "perfsid"

	// perfLogMaxBytes is as large as the log grows; past it, nothing more
	// is written. Any page can post to it, so it can't grow without end.
	perfLogMaxBytes = 256 << 20
	// A batch of events is at most this large, and holds at most
	// perfLogMaxEvents of them.
	perfLogMaxBatchBytes = 1 << 20
	perfLogMaxEvents     = 2000
)

type perfLog struct {
	script []byte

	mu   sync.Mutex
	f    *os.File
	size int64
	max  int64 // perfLogMaxBytes
	full bool
}

// openPerfLog appends to the performance log at path, made if need be.
func openPerfLog(path string) (*perfLog, error) {
	// The template is the script alone.
	script, err := loadTemplate(templateFS, "perf-log")
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot open performance log %q, %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return &perfLog{script: script, f: f, size: info.Size(), max: perfLogMaxBytes}, nil
}

func (l *perfLog) write(rec map[string]any) {
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	b = append(b, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.full {
		return
	}
	if l.size+int64(len(b)) > l.max {
		l.full = true
		slog.Warn("performance log is full; nothing more is written to it", "path", l.f.Name(), "bytes", l.size)
		return
	}
	n, err := l.f.Write(b)
	l.size += int64(n)
	if err != nil {
		slog.Error("cannot write performance log", "path", l.f.Name(), "error", err)
	}
}

func (l *perfLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

// serveScript serves perf-log.js, which every HTML page loads.
func (l *perfLog) serveScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(l.script); err != nil {
		slog.Warn("cannot write performance log script", "error", err)
	}
}

// serveEvents takes a batch of a page's events and logs them.
func (l *perfLog) serveEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var batch struct {
		SID    string           `json:"sid"`
		Page   string           `json:"page"`
		Events []map[string]any `json:"events"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, perfLogMaxBatchBytes)).Decode(&batch); err != nil {
		http.Error(w, "bad batch of events", http.StatusBadRequest)
		return
	}
	if len(batch.Events) > perfLogMaxEvents {
		batch.Events = batch.Events[:perfLogMaxEvents]
	}
	at := time.Now().UTC().Format(time.RFC3339Nano)
	for _, ev := range batch.Events {
		ev["src"] = "client"
		ev["at"] = at
		ev["sid"] = batch.SID
		ev["page"] = batch.Page
		l.write(ev)
	}
	w.WriteHeader(http.StatusNoContent)
}

// wrap logs every request next answers, and adds perf-log.js to the
// HTML pages it serves.
func (l *perfLog) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == perfLogScriptPath || r.URL.Path == perfLogEventsPath {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		pw := &perfLogWriter{ResponseWriter: w, status: http.StatusOK, path: r.URL.Path}
		next.ServeHTTP(pw, r)
		pw.finish()
		rec := map[string]any{
			"src":    "server",
			"at":     start.UTC().Format(time.RFC3339Nano),
			"remote": r.RemoteAddr,
			"method": r.Method,
			"path":   r.URL.RequestURI(),
			"proto":  r.Proto,
			"status": pw.status,
			"bytes":  pw.written,
			"ms":     millis(time.Since(start)),
		}
		if c, err := r.Cookie(perfLogCookie); err == nil {
			rec["sid"] = c.Value
		} else {
			// A request from before the page's script ran: the user
			// agent says whose it was.
			rec["ua"] = r.UserAgent()
		}
		if !pw.firstByte.IsZero() {
			rec["ttfb_ms"] = millis(pw.firstByte.Sub(start))
		}
		if v := r.Header.Get("Range"); v != "" {
			rec["range"] = v
		}
		if r.Context().Err() != nil {
			rec["canceled"] = true
		}
		l.write(rec)
	})
}

func millis(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

// perfLogScriptTag loads perf-log.js from the server's root, as a path
// relative to the page at path, so it's still found behind a proxy that
// serves the server under a prefix.
func perfLogScriptTag(path string) []byte {
	root := strings.Repeat("../", max(0, strings.Count(path, "/")-1))
	if root == "" {
		root = "./"
	}
	return []byte(`<script src="` + root + strings.TrimPrefix(perfLogScriptPath, "/") + `"></script>`)
}

// perfLogWriter notes a response's status, size and first byte, and
// holds back an HTML page to add perf-log.js to it. The header goes out
// with the first byte of the body, once its type is known.
type perfLogWriter struct {
	http.ResponseWriter
	path        string
	status      int
	wroteHeader bool
	headerSent  bool
	page        *bytes.Buffer // an HTML page being held back
	written     int64
	firstByte   time.Time
}

func (w *perfLogWriter) WriteHeader(code int) {
	// 1xx responses come before the answer.
	if code < http.StatusOK {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	if !w.wroteHeader {
		w.wroteHeader = true
		w.status = code
	}
}

// begin decides, at the first write, whether the body is a page to hold
// back (sniffing its type as the server would when none is set) or goes
// out as it comes.
func (w *perfLogWriter) begin(p []byte) {
	if w.headerSent || w.page != nil {
		return
	}
	w.wroteHeader = true
	ct := w.Header().Get("Content-Type")
	if ct == "" && len(p) > 0 {
		ct = http.DetectContentType(p)
	}
	if w.status == http.StatusOK && strings.HasPrefix(ct, "text/html") && w.Header().Get("Content-Encoding") == "" {
		w.Header().Set("Content-Type", ct)
		w.Header().Del("Content-Length")
		w.page = &bytes.Buffer{}
		return
	}
	w.sendHeader()
}

func (w *perfLogWriter) sendHeader() {
	w.headerSent = true
	w.ResponseWriter.WriteHeader(w.status)
}

func (w *perfLogWriter) Write(p []byte) (int, error) {
	w.begin(p)
	if w.page != nil {
		return w.page.Write(p)
	}
	if w.firstByte.IsZero() && len(p) > 0 {
		w.firstByte = time.Now()
	}
	n, err := w.ResponseWriter.Write(p)
	w.written += int64(n)
	return n, err
}

// ReadFrom keeps sendfile for a file, which http.ServeContent copies
// with it.
func (w *perfLogWriter) ReadFrom(r io.Reader) (int64, error) {
	w.begin(nil)
	if w.page != nil {
		return w.page.ReadFrom(r)
	}
	if w.firstByte.IsZero() {
		w.firstByte = time.Now()
	}
	var n int64
	var err error
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err = rf.ReadFrom(r)
	} else {
		// Only Write, or io.Copy would come straight back here.
		n, err = io.Copy(struct{ io.Writer }{w.ResponseWriter}, r)
	}
	w.written += n
	return n, err
}

func (w *perfLogWriter) Flush() {
	if w.page != nil {
		return
	}
	if !w.headerSent {
		w.sendHeader()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap is for http.ResponseController.
func (w *perfLogWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// finish sends what the handler left: the header of an empty response,
// or a held-back page with perf-log.js added before its </body>.
func (w *perfLogWriter) finish() {
	if w.page == nil {
		if !w.headerSent {
			w.sendHeader()
		}
		return
	}
	body, tag := w.page.Bytes(), perfLogScriptTag(w.path)
	out := make([]byte, 0, len(body)+len(tag))
	if i := bytes.LastIndex(body, []byte("</body>")); i >= 0 {
		out = append(append(append(out, body[:i]...), tag...), body[i:]...)
	} else {
		out = append(append(out, body...), tag...)
	}
	w.page = nil
	w.sendHeader()
	w.firstByte = time.Now()
	n, err := w.ResponseWriter.Write(out)
	w.written = int64(n)
	if err != nil {
		slog.Warn("cannot write page", "path", w.path, "error", err)
	}
}
