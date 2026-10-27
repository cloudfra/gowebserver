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
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gowsTesting "github.com/cloudfra/gowebserver/internal/gowebserver/testing"
	"github.com/google/go-cmp/cmp"
)

func mustOpenPerfLog(t *testing.T) (*perfLog, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "perf.jsonl")
	l, err := openPerfLog(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})
	return l, path
}

// readPerfLog is the log's lines, each a JSON object.
func readPerfLog(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer gowsTesting.DeferClose(t, f)()
	var recs []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("not a JSON object: %q, %s", sc.Text(), err)
		}
		recs = append(recs, rec)
	}
	return recs
}

func TestPerfLogAddsScriptToPages(t *testing.T) {
	l, _ := mustOpenPerfLog(t)
	page := "<!doctype html><html><body><p>hi</p></body></html>"
	testCases := []struct {
		name   string
		path   string
		header map[string]string
		status int
		body   string
		want   string
	}{
		{
			name: "root", path: "/", body: page,
			want: `<!doctype html><html><body><p>hi</p><script src="./__perf.js"></script></body></html>`,
		},
		{
			name: "folder, relative to it", path: "/photos/2025/", body: page,
			want: `<!doctype html><html><body><p>hi</p><script src="../../__perf.js"></script></body></html>`,
		},
		{
			name: "a page's type is sniffed when unset", path: "/a.html", header: map[string]string{"Content-Type": ""}, body: page,
			want: `<!doctype html><html><body><p>hi</p><script src="./__perf.js"></script></body></html>`,
		},
		{
			name: "a page with no </body> gets it at the end", path: "/x/", body: "<p>hi",
			want: `<p>hi<script src="../__perf.js"></script>`,
		},
		{
			name: "not a page", path: "/a.txt", header: map[string]string{"Content-Type": "text/plain"}, body: "</body>",
			want: "</body>",
		},
		{
			name: "a compressed page is left alone", path: "/", header: map[string]string{"Content-Encoding": "gzip"}, body: page,
			want: page,
		},
		{
			name: "an error page is left alone", path: "/missing", status: http.StatusNotFound, body: page,
			want: page,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			h := l.wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				for k, v := range tc.header {
					if v == "" {
						w.Header().Del(k)
					} else {
						w.Header().Set(k, v)
					}
				}
				w.Header().Set("Content-Length", fmt.Sprint(len(tc.body)))
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				// In two pieces, as a template might write it.
				for _, piece := range []string{tc.body[:len(tc.body)/2], tc.body[len(tc.body)/2:]} {
					if _, err := io.WriteString(w, piece); err != nil {
						t.Error(err)
					}
				}
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if diff := cmp.Diff(tc.want, rec.Body.String()); diff != "" {
				t.Errorf("body mismatch (-want +got):\n%s", diff)
			}
			if cl := rec.Header().Get("Content-Length"); cl != "" && cl != fmt.Sprint(len(tc.want)) {
				t.Errorf("Content-Length = %s for a body of %d", cl, len(tc.want))
			}
			wantStatus := tc.status
			if wantStatus == 0 {
				wantStatus = http.StatusOK
			}
			if rec.Code != wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, wantStatus)
			}
		})
	}
}

func TestPerfLogRecordsRequests(t *testing.T) {
	l, path := mustOpenPerfLog(t)
	content := bytes.Repeat([]byte("0123456789"), 1000)
	srv := httptest.NewServer(l.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "clip.mp4", time.Time{}, bytes.NewReader(content))
	})))
	defer srv.Close()

	get := func(rangeHeader string, cookie *http.Cookie) {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/videos/clip.mp4", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("User-Agent", "tester")
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer gowsTesting.DeferClose(t, resp.Body)()
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Fatal(err)
		}
	}
	get("", nil)
	get("bytes=100-199", &http.Cookie{Name: perfLogCookie, Value: "abc123"})

	recs := readPerfLog(t, path)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2: %v", len(recs), recs)
	}
	ignore := map[string]bool{"at": true, "ms": true, "ttfb_ms": true, "remote": true}
	strip := func(rec map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range rec {
			if !ignore[k] {
				out[k] = v
			}
		}
		return out
	}
	want := []map[string]any{
		{"src": "server", "method": "GET", "ext": ".mp4", "depth": 1.0, "proto": "HTTP/1.1", "status": 200.0, "bytes": 10000.0, "ua": "tester"},
		{"src": "server", "method": "GET", "ext": ".mp4", "depth": 1.0, "proto": "HTTP/1.1", "status": 206.0, "bytes": 100.0, "sid": "abc123", "range": "bytes=100-199"},
	}
	for i := range want {
		if diff := cmp.Diff(want[i], strip(recs[i])); diff != "" {
			t.Errorf("record %d mismatch (-want +got):\n%s", i, diff)
		}
		for _, k := range []string{"at", "ms", "ttfb_ms", "remote"} {
			if _, ok := recs[i][k]; !ok {
				t.Errorf("record %d has no %q: %v", i, k, recs[i])
			}
		}
	}
}

func TestPerfLogEvents(t *testing.T) {
	l, path := mustOpenPerfLog(t)
	post := func(method, body string) int {
		rec := httptest.NewRecorder()
		l.serveEvents(rec, httptest.NewRequest(method, perfLogEventsPath, strings.NewReader(body)))
		return rec.Code
	}
	if got := post(http.MethodPost, `{"sid":"s1","page":"/photos/","events":[{"type":"start","ts":12},{"type":"mark","n":1}]}`); got != http.StatusNoContent {
		t.Errorf("POST = %d, want %d", got, http.StatusNoContent)
	}
	if got := post(http.MethodGet, ""); got != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, want %d", got, http.StatusMethodNotAllowed)
	}
	if got := post(http.MethodPost, "not json"); got != http.StatusBadRequest {
		t.Errorf("bad batch = %d, want %d", got, http.StatusBadRequest)
	}
	// Too many events in one batch: the rest are dropped.
	var many strings.Builder
	many.WriteString(`{"sid":"s2","events":[`)
	for i := range perfLogMaxEvents + 10 {
		if i > 0 {
			many.WriteString(",")
		}
		many.WriteString(`{"type":"x"}`)
	}
	many.WriteString("]}")
	if got := post(http.MethodPost, many.String()); got != http.StatusNoContent {
		t.Errorf("large batch = %d, want %d", got, http.StatusNoContent)
	}

	recs := readPerfLog(t, path)
	if len(recs) != 2+perfLogMaxEvents {
		t.Fatalf("got %d records, want %d", len(recs), 2+perfLogMaxEvents)
	}
	delete(recs[0], "at")
	if diff := cmp.Diff(map[string]any{"src": "client", "sid": "s1", "page": map[string]any{"ext": "/", "depth": 1.0}, "type": "start", "ts": 12.0}, recs[0]); diff != "" {
		t.Errorf("event mismatch (-want +got):\n%s", diff)
	}
}

func TestPerfLogStopsWhenFull(t *testing.T) {
	l, path := mustOpenPerfLog(t)
	l.max = 100
	for i := range 10 {
		l.write(map[string]any{"n": i, "pad": "0123456789"})
	}
	recs := readPerfLog(t, path)
	if len(recs) == 0 || len(recs) >= 10 {
		t.Errorf("got %d records, want some but not all 10", len(recs))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > l.max {
		t.Errorf("log is %d bytes, over its %d", info.Size(), l.max)
	}
}

func TestPerfLogScriptTag(t *testing.T) {
	for path, want := range map[string]string{
		"":          `<script src="./__perf.js"></script>`,
		"/":         `<script src="./__perf.js"></script>`,
		"/a.html":   `<script src="./__perf.js"></script>`,
		"/a/":       `<script src="../__perf.js"></script>`,
		"/a/b/c.md": `<script src="../../__perf.js"></script>`,
	} {
		if got := string(perfLogScriptTag(path)); got != want {
			t.Errorf("perfLogScriptTag(%q) = %s, want %s", path, got, want)
		}
	}
}

func TestServeWithPerfLog(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "perf.jsonl")
	baseURL, closer := serveAsync(t, &Config{
		Serve:        []Serve{{Source: dir, Endpoint: "/"}},
		EnhancedList: true,
		Monitoring:   Monitoring{PerfLog: logPath},
	})
	defer closer()

	get := func(path string) (string, http.Header) {
		resp, err := http.Get(baseURL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer gowsTesting.DeferClose(t, resp.Body)()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", path, resp.StatusCode)
		}
		return string(b), resp.Header
	}
	if page, _ := get("/sub/"); !strings.Contains(page, `<script src="../__perf.js"></script></body>`) {
		t.Errorf("the folder's page doesn't load the script:\n%s", page)
	}
	script, header := get("/__perf.js")
	if !strings.Contains(script, "__perfLog") || !strings.HasPrefix(header.Get("Content-Type"), "text/javascript") {
		t.Errorf("GET /__perf.js = %s, %.80q", header.Get("Content-Type"), script)
	}
	resp, err := http.Post(baseURL+"/__perf", "application/json", strings.NewReader(`{"sid":"s","page":"/sub/","events":[{"type":"mark"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	gowsTesting.DeferClose(t, resp.Body)()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("POST /__perf = %d", resp.StatusCode)
	}

	var server, client []string
	for _, rec := range readPerfLog(t, logPath) {
		if rec["src"] == "server" {
			server = append(server, fmt.Sprintf("%v %v", rec["ext"], rec["depth"]))
		} else {
			client = append(client, rec["type"].(string))
		}
	}
	// serveAsync's check that the server is up, then the folder's page;
	// the log's own requests aren't logged.
	if diff := cmp.Diff([]string{"/ 0", "/ 1"}, server); diff != "" {
		t.Errorf("server records mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"mark"}, client); diff != "" {
		t.Errorf("client events mismatch (-want +got):\n%s", diff)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("sub")) {
		t.Errorf("the log holds a path:\n%s", raw)
	}
}

func TestPathShape(t *testing.T) {
	for p, want := range map[string]string{
		"":                                 "/ 0",
		"/":                                "/ 0",
		"/photos/":                         "/ 1",
		"/photos/2025/IMG_0001.JPG":        ".jpg 2",
		"/a.txt?view=rich":                 ".txt 0",
		"/videos/clip.mp4#t=0.5":           ".mp4 1",
		"https://host:8080/proxy/x/y.webm": ".webm 2",
		"https://host":                     "/ 0",
		"/notes/README":                    " 1",
		"/a/my.secret-name":                "? 1",
		"/a/archive.tar.gz":                ".gz 1",
	} {
		shape := pathShape(p)
		if got := fmt.Sprintf("%v %v", shape["ext"], shape["depth"]); got != want {
			t.Errorf("pathShape(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestPerfLogScrubsEvents(t *testing.T) {
	ev := map[string]any{
		"type":  "err",
		"url":   "https://h/proxy/8182/photos/IMG_1.jpg",
		"n":     map[string]any{"ext": ".mp4", "depth": 2.0},
		"file":  map[string]any{"ext": "/etc/passwd", "depth": 1.0},
		"page":  map[string]any{"ext": ".jpg", "depth": -1.0},
		"msg":   "Failed to load https://h/photos/a.jpg and /videos/b.mp4 (from photos/2025/c.png)",
		"stack": "at load (https://h/__perf.js:12:3)",
		"line":  "12:3",
		"ua":    "Mozilla/5.0 (iPad) AppleWebKit/605.1.15 Safari/605.1.15",
		"where": "dialog",
		"list":  []any{"/photos/a.jpg"},
		"obj":   map[string]any{"path": "/photos/a.jpg"},
		"ok":    true,
		"ts":    12.0,
	}
	scrubEvent(ev)
	want := map[string]any{
		"type":  "err",
		"url":   map[string]any{"ext": ".jpg", "depth": 3},
		"n":     map[string]any{"ext": ".mp4", "depth": 2},
		"msg":   "Failed to load <path> and <path> (from <path>)",
		"stack": "at load (<path>)",
		"line":  "12:3",
		"ua":    "Mozilla/5.0 (iPad) AppleWebKit/605.1.15 Safari/605.1.15",
		"where": "dialog",
		"ok":    true,
		"ts":    12.0,
	}
	if diff := cmp.Diff(want, ev); diff != "" {
		t.Errorf("scrubbed event mismatch (-want +got):\n%s", diff)
	}
}
