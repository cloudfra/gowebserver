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

// What a trial embedding found missing (#494): 404s, quiet logs, no
// pprof on DefaultServeMux, a folder made later, Mount, the footer and a
// link home, and ffmpeg that's optional.

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// A missing file or folder is a 404, an unreadable one a 403, and the
// body never echoes the error, which names paths.
func TestHandlerNotFound(t *testing.T) {
	dir := mediaDir(t)
	mux := hostMux(t, newTestHandler(t, Options{FS: os.DirFS(dir), EnhancedIndex: true}))
	for _, target := range []string{
		"/media/nope/", "/media/nope/deeper/", "/media/nope.txt", "/media/nope.txt?view=rich",
		"/media/nope/?preview", "/media/nope/?folders", "/media/trips/nope/?sort=date",
	} {
		rec := mountGet(t, mux, http.MethodGet, target, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", target, rec.Code)
		}
		if b := rec.Body.String(); strings.Contains(b, "nope") || strings.Contains(b, "no such file") || strings.Contains(b, "open") {
			t.Errorf("GET %s: the body %q names the error", target, b)
		}
	}

	// The whole folder missing: os.DirFS of a folder that isn't there.
	gone := hostMux(t, newTestHandler(t, Options{FS: os.DirFS(filepath.Join(t.TempDir(), "gone")), EnhancedIndex: true}))
	if rec := mountGet(t, gone, http.MethodGet, "/media/", nil); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "open .") {
		t.Errorf("GET /media/ of a missing folder: %d %q", rec.Code, rec.Body.String())
	}

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return // no unreadable folders there
	}
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(locked, 0o700); err != nil {
			t.Error(err)
		}
	})
	if rec := mountGet(t, mux, http.MethodGet, "/media/locked/", nil); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "permission") {
		t.Errorf("GET an unreadable folder: %d %q, want a plain 403", rec.Code, rec.Body.String())
	}
}

// records is a slog handler that keeps what's logged.
type records struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (r *records) Enabled(context.Context, slog.Level) bool { return true }
func (r *records) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, rec)
	return nil
}
func (r *records) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *records) WithGroup(string) slog.Handler      { return r }

func (r *records) take() []slog.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.recs
	r.recs = nil
	return out
}

func recordText(rec slog.Record) string {
	var b strings.Builder
	b.WriteString(rec.Message)
	rec.Attrs(func(a slog.Attr) bool {
		b.WriteString(" " + a.Key + "=" + a.Value.String())
		return true
	})
	return b.String()
}

// Browsing logs nothing above Debug, and nothing with a name in it.
func TestHandlerRequestsAreQuiet(t *testing.T) {
	logs := &records{}
	tmp := t.TempDir()
	h := newTestHandler(t, Options{
		FS: os.DirFS(mediaDir(t)), EnhancedIndex: true, Logger: slog.New(logs),
		Upload: UploadOptions{Path: "upload", Dir: filepath.Join(tmp, "up")},
	})
	logs.take() // what NewHandler says about ffmpeg and the store
	mux := hostMux(t, h)
	for _, target := range []string{
		"/media/", "/media/trips/", "/media/trips/2024/", "/media/trips/?preview", "/media/?folders",
		"/media/beach.png?width=32", "/media/notes.txt?view=rich", "/media/notes.txt",
		"/media/nope/", "/media/nope.png?width=32", "/media/upload",
	} {
		mountGet(t, mux, http.MethodGet, target, nil)
	}
	names := []string{"beach", "notes", "trips", "rome", "paris", "2024", "nope", "odd name"}
	for _, rec := range logs.take() {
		text := recordText(rec)
		if rec.Level > slog.LevelDebug {
			t.Errorf("a request logged at %s: %s", rec.Level, text)
		}
		for _, n := range names {
			if strings.Contains(text, n) {
				t.Errorf("a log line names %q: %s", n, text)
			}
		}
	}
}

// Importing the package puts no pprof on http.DefaultServeMux, for a
// host that serves it. (/debug/vars is expvar's, which the Prometheus
// client and an archive driver import; the embedding API that leaves
// those out is #494's.)
func TestDefaultServeMuxEmpty(t *testing.T) {
	for _, p := range []string{"/", "/debug/pprof/", "/debug/pprof/heap", "/debug/pprof/cmdline", "/debug/pprof/profile", "/debug/pprof/symbol", "/debug/pprof/trace", "/metrics"} {
		if _, pattern := http.DefaultServeMux.Handler(httptest.NewRequest(http.MethodGet, p, nil)); pattern != "" {
			t.Errorf("http.DefaultServeMux routes %s to %q", p, pattern)
		}
	}
}

// The standalone server's pprof endpoints still answer, on its own mux.
func TestPprofHandlers(t *testing.T) {
	mux := http.NewServeMux()
	for p, h := range pprofHandlers("/debug") {
		mux.Handle(p, h)
	}
	for target, want := range map[string]string{
		"/debug/pprof/":                    "goroutine",
		"/debug/pprof/heap?debug=1":        "heap profile",
		"/debug/pprof/goroutine?debug=2":   "goroutine",
		"/debug/pprof/symbol":              "num_symbols: 1",
		"/debug/pprof/profile?seconds=0.1": "",
		"/debug/pprof/trace?seconds=0.1":   "",
	} {
		rec := mountGet(t, mux, http.MethodGet, target, nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) || rec.Body.Len() == 0 {
			t.Errorf("GET %s: %d, %d bytes, want %q in it", target, rec.Code, rec.Body.Len(), want)
		}
	}
	if rec := mountGet(t, mux, http.MethodGet, "/debug/pprof/cmdline", nil); !strings.Contains(rec.Body.String(), os.Args[0]) {
		t.Errorf("cmdline %q", rec.Body.String())
	}
	if rec := mountGet(t, mux, http.MethodGet, "/debug/pprof/nothing", nil); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown profile: %d", rec.Code)
	}
	pc := uintptr(0)
	if pcs := make([]uintptr, 1); runtime.Callers(1, pcs) == 1 {
		pc = pcs[0]
	}
	req := httptest.NewRequest(http.MethodPost, "/debug/pprof/symbol", strings.NewReader("0x"+strconv.FormatUint(uint64(pc), 16)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "TestPprofHandlers") {
		t.Errorf("symbol: %q", rec.Body.String())
	}
}

// A Path to a folder that isn't there yet doesn't fail NewHandler; it's
// a 404 until the folder is made, then served.
func TestNewHandlerFolderMadeLater(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "media")
	mux := hostMux(t, newTestHandler(t, Options{Path: dir, EnhancedIndex: true}))
	for _, target := range []string{"/media/", "/media/a.txt"} {
		if rec := mountGet(t, mux, http.MethodGet, target, nil); rec.Code != http.StatusNotFound {
			t.Errorf("before: GET %s: %d, want 404", target, rec.Code)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rec := mountGet(t, mux, http.MethodGet, "/media/a.txt", nil); rec.Code != http.StatusOK || rec.Body.String() != "a" {
		t.Errorf("after: GET /media/a.txt: %d %q", rec.Code, rec.Body.String())
	}
	if rec := mountGet(t, mux, http.MethodGet, "/media/", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "a.txt") {
		t.Errorf("after: GET /media/: %d", rec.Code)
	}
}

// Mount answers the bare prefix with a relative redirect, which stays
// right behind a proxy that adds a prefix of its own.
func TestMount(t *testing.T) {
	h := newTestHandler(t, Options{FS: os.DirFS(mediaDir(t))})
	for _, prefix := range []string{"/media", "/media/", "media"} {
		mux := http.NewServeMux()
		Mount(mux, prefix, h)
		rec := mountGet(t, mux, http.MethodGet, "/media?x=1", nil)
		if rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != "./media/?x=1" {
			t.Errorf("Mount(%q): GET /media: %d %q", prefix, rec.Code, rec.Header().Get("Location"))
		}
		// As the browser resolves it behind a proxy at /proxy/.
		base, err := url.Parse("https://host.test/proxy/media?x=1")
		if err != nil {
			t.Fatal(err)
		}
		if u, err := base.Parse(rec.Header().Get("Location")); err != nil || u.Path != "/proxy/media/" {
			t.Errorf("Mount(%q): behind a proxy the redirect goes to %v", prefix, u)
		}
		if rec := mountGet(t, mux, http.MethodGet, "/media/notes.txt", nil); rec.Code != http.StatusOK {
			t.Errorf("Mount(%q): GET /media/notes.txt: %d", prefix, rec.Code)
		}
	}
	root := http.NewServeMux()
	Mount(root, "/", h)
	if rec := mountGet(t, root, http.MethodGet, "/notes.txt", nil); rec.Code != http.StatusOK {
		t.Errorf("Mount at /: %d", rec.Code)
	}
}

// The footer names the server by default, and can be replaced or left
// out; Home links back to the host's app.
func TestHandlerFooterAndHome(t *testing.T) {
	dir := mediaDir(t)
	tmp := t.TempDir()
	pages := []string{"/media/", "/media/notes.txt?view=rich", "/media/upload"}
	for name, tc := range map[string]struct {
		o          Options
		want, nope []string
	}{
		"default": {Options{}, []string{"Go Web Server", "gowebserver "}, []string{`data-host-href="`}},
		"replaced": {
			Options{Footer: "Media <b>library</b>", Home: HomeLink{Label: "My app", URL: "/app/"}},
			[]string{"Media &lt;b&gt;library&lt;/b&gt;", `data-host-href="/app/"`, `data-host-label="My app"`},
			[]string{"Go Web Server", "gowebserver "},
		},
		"hidden": {Options{HideFooter: true, Home: HomeLink{URL: "/"}}, []string{`data-host-label="Home"`}, []string{`class="footer">`, `<footer class="site-footer"`, "Go Web Server"}},
	} {
		o := tc.o
		o.FS, o.EnhancedIndex = os.DirFS(dir), true
		o.Upload = UploadOptions{Path: "upload", Dir: tmp}
		mux := hostMux(t, newTestHandler(t, o))
		for _, target := range pages {
			body := mountGet(t, mux, http.MethodGet, target, nil).Body.String()
			for _, w := range tc.want {
				if strings.HasPrefix(w, "data-host") && strings.HasSuffix(target, "upload") {
					continue // no breadcrumb there
				}
				if w == "gowebserver " && !strings.Contains(target, "view=rich") || w == "Go Web Server" && strings.Contains(target, "view=rich") {
					continue // each page's own wording
				}
				if !strings.Contains(body, w) {
					t.Errorf("%s: GET %s: no %q", name, target, w)
				}
			}
			for _, n := range tc.nope {
				if strings.Contains(body, n) {
					t.Errorf("%s: GET %s: %q is there", name, target, n)
				}
			}
		}
	}
	if _, err := NewHandler(t.Context(), Options{FS: os.DirFS(dir), Home: HomeLink{URL: "javascript:alert(1)"}}); err == nil {
		t.Error("a javascript: URL for Home was accepted")
	}
}

// A configured ffmpeg that isn't there is no ffmpeg, said once, unless
// it's required; the hint names the option, not the server's flags.
func TestNewHandlerFFmpegOptional(t *testing.T) {
	var logs bytes.Buffer
	empty := t.TempDir()
	h, err := NewHandler(t.Context(), Options{FS: os.DirFS(empty), FFmpeg: FFmpeg{Path: empty}, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	if err != nil {
		t.Fatalf("a folder without ffmpeg failed NewHandler: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Error(err)
	}
	if n := strings.Count(logs.String(), "ffmpeg isn't usable"); n != 1 {
		t.Errorf("said %d times:\n%s", n, logs.String())
	}
	if !strings.Contains(logs.String(), "Options.FFmpeg") || strings.Contains(logs.String(), "ffmpeg.installOnDemand") {
		t.Errorf("the hint names the wrong settings:\n%s", logs.String())
	}
	if _, err := NewHandler(t.Context(), Options{FS: os.DirFS(empty), FFmpeg: FFmpeg{Path: empty, Required: true}}); err == nil {
		t.Error("a required ffmpeg that isn't there didn't fail")
	}
}
