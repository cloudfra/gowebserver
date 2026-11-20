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
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/goleak"
	"go.uber.org/zap"
)

// mediaDir is a folder to mount: photos, a text file and a subfolder.
func mediaDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string][]byte{
		"beach.png":             encodePNG(t, stripes(64, 36)),
		"notes.txt":             []byte(strings.Repeat("a line of notes\n", 100)),
		"trips/2024/rome.png":   encodePNG(t, stripes(48, 32)),
		"trips/2024/readme.md":  []byte("# Rome\n"),
		"trips/2025/paris.png":  encodePNG(t, stripes(32, 48)),
		"odd name #1/photo.png": encodePNG(t, stripes(16, 16)),
	}
	for name, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// newTestHandler is a Handler of o, closed when the test ends.
func newTestHandler(t *testing.T, o Options) *Handler {
	t.Helper()
	h, err := NewHandler(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Error(err)
		}
	})
	return h
}

// hostMux is a host program's mux: its API at /api/, h at /media/ and
// its app (a stand-in) at /.
func hostMux(t *testing.T, h http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := fmt.Fprintf(w, `{"path":%q,"query":%q}`, r.URL.Path, r.URL.RawQuery); err != nil {
			t.Error(err)
		}
	})
	if h != nil {
		mux.Handle("/media/", http.StripPrefix("/media", h))
	}
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fmt.Fprintf(w, "app at %s?%s", r.URL.Path, r.URL.RawQuery); err != nil {
			t.Error(err)
		}
	}))
	return mux
}

func mountGet(t *testing.T, h http.Handler, method, target string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestNewHandlerNeedsOneSource(t *testing.T) {
	for name, o := range map[string]Options{
		"neither": {},
		"both":    {FS: os.DirFS(t.TempDir()), Path: t.TempDir()},
	} {
		if h, err := NewHandler(t.Context(), o); err == nil {
			t.Errorf("%s: no error", name)
			if err := h.Close(); err != nil {
				t.Error(err)
			}
		}
	}
}

// The globals a host's own serving depends on, as they are now.
type hostGlobals struct {
	meter, tracer, propagator, errorHandler any
	zapL                                    *zap.Logger
	zapS                                    *zap.SugaredLogger
	slogDefault                             *slog.Logger
	logWriter                               io.Writer
	logFlags                                int
	flags                                   []string
	defaultMux                              []string
	wd                                      string
}

func snapshotGlobals(t *testing.T) hostGlobals {
	t.Helper()
	g := hostGlobals{
		meter:        otel.GetMeterProvider(),
		tracer:       otel.GetTracerProvider(),
		propagator:   otel.GetTextMapPropagator(),
		errorHandler: otel.GetErrorHandler(),
		zapL:         zap.L(),
		zapS:         zap.S(),
		slogDefault:  slog.Default(),
		logWriter:    log.Writer(),
		logFlags:     log.Flags(),
	}
	flag.CommandLine.VisitAll(func(f *flag.Flag) { g.flags = append(g.flags, f.Name) })
	// What http.DefaultServeMux would route the paths gowebserver uses to.
	for _, p := range []string{"/", "/metrics", "/debug/pprof/", "/debug/tracez", "/__diag", "/diediedie", "/upload.asp", "/upload", "/_gows/x/y"} {
		_, pattern := http.DefaultServeMux.Handler(httptest.NewRequest(http.MethodGet, p, nil))
		g.defaultMux = append(g.defaultMux, p+"="+pattern)
	}
	var err error
	if g.wd, err = os.Getwd(); err != nil {
		t.Fatal(err)
	}
	return g
}

// Importing the package registers no flags: this test binary's are all
// the testing package's.
func TestImportRegistersNoFlags(t *testing.T) {
	flag.CommandLine.VisitAll(func(f *flag.Flag) {
		if !strings.HasPrefix(f.Name, "test.") {
			t.Errorf("flag -%s is on flag.CommandLine", f.Name)
		}
	})
}

// A host program with flags named as the standalone server's (-path,
// -verbose, -http.port) parses them, and its -help is its own.
func TestHostFlags(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool:", err)
	}
	bin := filepath.Join(t.TempDir(), "embedhost")
	build := exec.CommandContext(t.Context(), goTool, "build", "-o", bin, "github.com/cloudfra/gowebserver/internal/gowebserver/testing/embedhost")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	// Parsed, then -help: flag stops there, with status 0.
	help := exec.CommandContext(t.Context(), bin, "-path", t.TempDir(), "-verbose", "-http.port", "1234", "-help")
	out, err := help.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	found := regexp.MustCompile(`(?m)^  -(\S+)`).FindAllStringSubmatch(string(out), -1)
	names := make([]string, 0, len(found))
	for _, m := range found {
		names = append(names, m[1])
	}
	if want := []string{"diagnostics", "http.port", "path", "title", "verbose"}; !slices.Equal(names, want) {
		t.Errorf("-help lists %v, want only the host's own %v:\n%s", names, want, out)
	}
}

// NewHandler and Close leave every global as they found it, start nothing
// that outlives Close, and report to the providers they're given.
func TestNewHandlerLeavesGlobalsAlone(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent(),
		// The text viewer's highlighter (chroma) times its regular
		// expressions with regexp2's clock, one goroutine for the whole
		// process that stops by itself a second after the last match.
		goleak.IgnoreAnyFunction("github.com/dlclark/regexp2/v2.runClock"))
	before := snapshotGlobals(t)

	reader := sdkmetric.NewManualReader()
	meters := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	spans := tracetest.NewSpanRecorder()
	tracers := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	var logs bytes.Buffer
	tmp := t.TempDir()
	h, err := NewHandler(context.Background(), Options{
		FS:             os.DirFS(mediaDir(t)),
		EnhancedIndex:  true,
		Title:          "Media",
		Thumbnails:     ThumbnailOptions{CachePath: filepath.Join(tmp, "thumbs.db")},
		Upload:         UploadOptions{Path: "upload", Dir: filepath.Join(tmp, "uploads")},
		Diagnostics:    filepath.Join(tmp, "diag.log"),
		Logger:         slog.New(slog.NewTextHandler(&logs, nil)),
		MeterProvider:  meters,
		TracerProvider: tracers,
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := hostMux(t, h)
	for _, target := range []string{
		"/media/", "/media/trips/", "/media/trips/?folders", "/media/trips/?preview",
		"/media/beach.png?width=32", "/media/notes.txt?view=rich", "/media/upload",
	} {
		if rec := mountGet(t, mux, http.MethodGet, target, nil); rec.Code != http.StatusOK {
			t.Errorf("GET %s: %d", target, rec.Code)
		}
	}
	diag := httptest.NewRecorder()
	mux.ServeHTTP(diag, httptest.NewRequest(http.MethodPost, "/media/__diag", strings.NewReader(`{"ev":"page","n":1}`)))
	if diag.Code != http.StatusNoContent {
		t.Errorf("POST /media/__diag: %d", diag.Code)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"thumbnail_requests_total", "listing_stage_duration_seconds", "http_server_active_requests", "thumbnail_render_slots"} {
		if !hasMetric(rm, name) {
			t.Errorf("metric %s isn't in the host's meter provider", name)
		}
	}
	if len(spans.Ended()) == 0 {
		t.Error("no spans in the host's tracer provider")
	}
	if !strings.Contains(logs.String(), "customIndexHandler") {
		t.Errorf("nothing logged to the host's logger:\n%s", logs.String())
	}

	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// The thumbnail store's gauges are taken back from the host's provider.
	rm = metricdata.ResourceMetrics{}
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}
	if hasMetric(rm, "thumbnail_render_slots") {
		t.Error("thumbnail_render_slots is still observed after Close")
	}

	after := snapshotGlobals(t)
	if before.meter != after.meter || before.tracer != after.tracer || before.propagator != after.propagator || before.errorHandler != after.errorHandler {
		t.Error("an otel global provider was replaced")
	}
	if before.zapL != after.zapL || before.zapS != after.zapS {
		t.Error("zap's global logger was replaced")
	}
	if before.slogDefault != after.slogDefault {
		t.Error("slog's default logger was replaced")
	}
	if before.logWriter != after.logWriter || before.logFlags != after.logFlags {
		t.Error("the log package's output was changed")
	}
	if !slices.Equal(before.flags, after.flags) {
		t.Errorf("flags %v, were %v", after.flags, before.flags)
	}
	if !slices.Equal(before.defaultMux, after.defaultMux) {
		t.Errorf("http.DefaultServeMux routes %v, were %v", after.defaultMux, before.defaultMux)
	}
	if before.wd != after.wd {
		t.Errorf("working directory %s, was %s", after.wd, before.wd)
	}
	if err := tracers.Shutdown(t.Context()); err != nil {
		t.Error(err)
	}
}

// Off by default: no endpoint but the content's own.
func TestNewHandlerOptionalEndpointsOff(t *testing.T) {
	h := newTestHandler(t, Options{FS: os.DirFS(mediaDir(t)), EnhancedIndex: true})
	mux := hostMux(t, h)
	for _, target := range []string{"/media/__diag", "/media/upload", "/media/upload.asp", "/media/metrics", "/media/diediedie", "/media/debug/pprof/", "/metrics", "/debug/pprof/"} {
		// None is there: what answers is the content (a 404, or a 500
		// for a folder that isn't there) or the host's app.
		rec := mountGet(t, mux, http.MethodGet, target, nil)
		if strings.HasPrefix(target, "/media/") && rec.Code == http.StatusOK {
			t.Errorf("GET %s: %d, want an error", target, rec.Code)
		}
		if !strings.HasPrefix(target, "/media/") && rec.Body.String() != "app at "+target+"?" {
			t.Errorf("GET %s: %q, want the host's app", target, rec.Body.String())
		}
	}
	page := mountGet(t, mux, http.MethodGet, "/media/", nil).Body.String()
	if !strings.Contains(page, `diag: "",`) {
		t.Error("the page sends diagnostics when they're off")
	}
}

// links are the URLs a page refers to: its attributes, the breadcrumb's
// home and where its diagnostics go.
var linkPattern = regexp.MustCompile(`(?:href|src|action|data-home)="([^"]*)"|diag: "([^"]*)"`)

// Every link on every page, and every redirect, resolves under /media/.
func TestHandlerLinksStayUnderMount(t *testing.T) {
	tmp := t.TempDir()
	h := newTestHandler(t, Options{
		FS:            os.DirFS(mediaDir(t)),
		EnhancedIndex: true,
		Title:         "Media",
		Upload:        UploadOptions{Path: "upload", Dir: filepath.Join(tmp, "uploads")},
		Diagnostics:   filepath.Join(tmp, "diag.log"),
	})
	mux := hostMux(t, h)
	pages := []string{
		"/media/", "/media/?sort=date", "/media/trips/", "/media/trips/2024/", "/media/odd%20name%20%231/",
		"/media/notes.txt?view=rich", "/media/trips/2024/readme.md?view=rich", "/media/upload",
	}
	homes := 0
	for _, target := range pages {
		rec := mountGet(t, mux, http.MethodGet, target, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d", target, rec.Code)
		}
		base, err := url.Parse("http://host.test" + target)
		if err != nil {
			t.Fatal(err)
		}
		links := linkPattern.FindAllStringSubmatch(rec.Body.String(), -1)
		if len(links) == 0 {
			t.Errorf("%s: no links found", target)
		}
		for _, m := range links {
			ref := m[1] + m[2]
			if ref == "" || strings.HasPrefix(ref, "#") || strings.HasPrefix(ref, "data:") || strings.ContainsAny(ref, "' \n") {
				continue // empty, in the page, or a script building one (the browser test follows those)
			}
			u, err := base.Parse(strings.ReplaceAll(ref, "&amp;", "&"))
			if err != nil {
				t.Errorf("%s: link %q: %v", target, ref, err)
				continue
			}
			if u.Host != "host.test" || !strings.HasPrefix(u.Path, "/media/") {
				t.Errorf("%s: link %q goes to %s, outside /media/", target, ref, u)
			}
			if strings.HasPrefix(m[0], "data-home") {
				homes++
				if u.Path != "/media/" {
					t.Errorf("%s: the breadcrumb's home is %s, want /media/", target, u.Path)
				}
			}
			if m[2] != "" && u.Path != "/media/__diag" {
				t.Errorf("%s: diagnostics go to %s, want /media/__diag", target, u.Path)
			}
		}
		if !strings.Contains(target, "upload") && !strings.Contains(rec.Body.String(), `data-title="Media"`) {
			t.Errorf("%s: the breadcrumb's home isn't named by Title", target)
		}
	}
	if homes != len(pages)-1 { // the upload page has no breadcrumb
		t.Errorf("%d pages say where the breadcrumb starts, want %d", homes, len(pages)-1)
	}

	// Redirects, to a folder's own URL or from the text viewer to its
	// listing, stay under /media/ too.
	for target, want := range map[string]string{
		"/media/trips":                "/media/trips/",
		"/media/trips/2024?sort=size": "/media/trips/2024/",
		"/media/trips?view=rich":      "/media/trips/",
		"/media/index.html":           "/media/",
	} {
		rec := mountGet(t, mux, http.MethodGet, target, nil)
		if rec.Code < 300 || rec.Code > 399 {
			if target == "/media/index.html" && rec.Code == http.StatusNotFound {
				continue // no index.html here, nothing to redirect
			}
			t.Errorf("GET %s: %d, want a redirect", target, rec.Code)
			continue
		}
		base, err := url.Parse("http://host.test" + target)
		if err != nil {
			t.Fatal(err)
		}
		u, err := base.Parse(rec.Header().Get("Location"))
		if err != nil || u.Path != want {
			t.Errorf("GET %s redirects to %q (%v), want %s", target, rec.Header().Get("Location"), u, want)
		}
	}

	// The host's own routes are untouched by the mount.
	plain := hostMux(t, nil)
	for _, target := range []string{"/api/items?x=1", "/", "/settings?tab=2", "/mediax", "/medi"} {
		with, without := mountGet(t, mux, http.MethodGet, target, nil), mountGet(t, plain, http.MethodGet, target, nil)
		if with.Code != without.Code || with.Body.String() != without.Body.String() {
			t.Errorf("GET %s: %d %q with the mount, %d %q without", target, with.Code, with.Body.String(), without.Code, without.Body.String())
		}
	}
}

// A file through the mount: ranges, HEAD and conditional requests, as
// http.FileServer answers them.
func TestHandlerFilesThroughMount(t *testing.T) {
	dir := mediaDir(t)
	mux := hostMux(t, newTestHandler(t, Options{FS: os.DirFS(dir)}))
	notes, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}

	rec := mountGet(t, mux, http.MethodGet, "/media/notes.txt", http.Header{"Range": {"bytes=2-9"}})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != string(notes[2:10]) {
		t.Errorf("range: %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Range"); got != fmt.Sprintf("bytes 2-9/%d", len(notes)) {
		t.Errorf("Content-Range %q", got)
	}

	rec = mountGet(t, mux, http.MethodHead, "/media/notes.txt", nil)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != fmt.Sprint(len(notes)) {
		t.Errorf("HEAD: %d, %d bytes, Content-Length %q", rec.Code, rec.Body.Len(), rec.Header().Get("Content-Length"))
	}

	modified := mountGet(t, mux, http.MethodGet, "/media/notes.txt", nil).Header().Get("Last-Modified")
	if modified == "" {
		t.Fatal("no Last-Modified")
	}
	if rec := mountGet(t, mux, http.MethodGet, "/media/notes.txt", http.Header{"If-Modified-Since": {modified}}); rec.Code != http.StatusNotModified {
		t.Errorf("If-Modified-Since: %d, want 304", rec.Code)
	}
	old := time.Unix(0, 0).UTC().Format(http.TimeFormat)
	if rec := mountGet(t, mux, http.MethodGet, "/media/notes.txt", http.Header{"If-Modified-Since": {old}}); rec.Code != http.StatusOK {
		t.Errorf("If-Modified-Since long ago: %d, want 200", rec.Code)
	}
}

// Path takes what the standalone server's -path does, archives included.
func TestNewHandlerPath(t *testing.T) {
	dir := mediaDir(t)
	mux := hostMux(t, newTestHandler(t, Options{Path: dir, EnhancedIndex: true}))
	if rec := mountGet(t, mux, http.MethodGet, "/media/trips/2024/readme.md", nil); rec.Code != http.StatusOK || rec.Body.String() != "# Rome\n" {
		t.Errorf("GET a file: %d %q", rec.Code, rec.Body.String())
	}
	if _, err := NewHandler(t.Context(), Options{Path: filepath.Join(dir, "missing.zip")}); err == nil {
		t.Error("no error for a path that isn't there")
	}
}

// The upload page, when asked for, takes files at its path under the
// mount, and its form posts back there.
func TestNewHandlerUpload(t *testing.T) {
	uploads := t.TempDir()
	mux := hostMux(t, newTestHandler(t, Options{FS: os.DirFS(t.TempDir()), Upload: UploadOptions{Path: "/inbox/upload/", Dir: uploads}}))
	page := mountGet(t, mux, http.MethodGet, "/media/inbox/upload", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `action="./upload"`) {
		t.Fatalf("upload page: %d, no form posting to ./upload", page.Code)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile(uploadFileFormName, "hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(fw, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/media/inbox/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	if got, err := os.ReadFile(filepath.Join(uploads, "hello.txt")); err != nil || string(got) != "hello" {
		t.Errorf("uploaded file: %q, %v", got, err)
	}
	if _, err := NewHandler(t.Context(), Options{FS: os.DirFS(t.TempDir()), Upload: UploadOptions{Path: "upload"}}); err == nil {
		t.Error("no error for an upload path without a folder")
	}
}

// hasMetric reports whether rm has a metric named name.
func hasMetric(rm metricdata.ResourceMetrics, name string) bool {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return true
			}
		}
	}
	return false
}
