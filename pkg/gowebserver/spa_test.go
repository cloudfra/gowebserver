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
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// A built app, as a bundler such as Vite leaves it.
var spaBuild = fstest.MapFS{
	"index.html":                {Data: []byte("<!doctype html><title>app</title><script src=/assets/app-1a2b3c.js></script>"), ModTime: time.Unix(1700000000, 0)},
	"favicon.ico":               {Data: []byte("icon"), ModTime: time.Unix(1700000000, 0)},
	"assets/app-1a2b3c.js":      {Data: []byte(strings.Repeat("console.log('app');\n", 100)), ModTime: time.Unix(1700000000, 0)},
	"assets/logo-9f8e7d.png":    {Data: []byte("\x89PNG\r\n\x1a\nnot decoded by anything")},
	"docs/guide/index.html":     {Data: []byte("not the app")},
	"robots.txt":                {Data: []byte("User-agent: *\n")},
	"assets/fonts/inter-1.woff": {Data: []byte("font")},
}

func TestSPA(t *testing.T) {
	h := SPA(spaBuild, SPAOptions{ImmutablePrefix: "assets/"})
	index := string(spaBuild["index.html"].Data)

	for _, target := range []string{"/", "/settings", "/users/42/edit", "/users/42/", "/docs/guide/", "/index.html", "/a/../b", "/settings?tab=2&view=rich&width=64&preview&sort=size&folders", "/users/jane.doe", "/releases/v1.2"} {
		rec := mountGet(t, h, http.MethodGet, target, nil)
		if rec.Code != http.StatusOK || rec.Body.String() != index {
			t.Errorf("GET %s: %d %q, want the app's index", target, rec.Code, rec.Body.String())
			continue
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s: Cache-Control %q, want no-cache", target, got)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Errorf("GET %s: Content-Type %q", target, got)
		}
	}

	// Files are themselves; the build's hashed ones cached for good.
	for target, want := range map[string]string{
		"/assets/app-1a2b3c.js":                      spaImmutableCache,
		"/assets/logo-9f8e7d.png":                    spaImmutableCache,
		"/assets/fonts/inter-1.woff":                 spaImmutableCache,
		"/favicon.ico":                               "",
		"/robots.txt":                                "",
		"/assets/logo-9f8e7d.png?width=32&height=32": spaImmutableCache,
	} {
		rec := mountGet(t, h, http.MethodGet, target, nil)
		name := strings.TrimPrefix(strings.SplitN(target, "?", 2)[0], "/")
		if rec.Code != http.StatusOK || rec.Body.String() != string(spaBuild[name].Data) {
			t.Errorf("GET %s: %d, not the file", target, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != want {
			t.Errorf("GET %s: Cache-Control %q, want %q", target, got, want)
		}
	}

	// A file that isn't there is a 404, never the index: a script that
	// came back as HTML would fail strangely, and be cached for a year.
	for _, target := range []string{"/assets/app-000000.js", "/assets/", "/assets/missing", "/missing.js", "/settings/photo.png"} {
		rec := mountGet(t, h, http.MethodGet, target, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", target, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "" {
			t.Errorf("GET %s: Cache-Control %q on a 404", target, got)
		}
	}

	if rec := mountGet(t, h, http.MethodPost, "/settings", nil); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST: %d, Allow %q", rec.Code, rec.Header().Get("Allow"))
	}
}

func TestSPAFileRequests(t *testing.T) {
	h := SPA(spaBuild, SPAOptions{ImmutablePrefix: "/assets"})
	js := string(spaBuild["assets/app-1a2b3c.js"].Data)

	rec := mountGet(t, h, http.MethodGet, "/assets/app-1a2b3c.js", http.Header{"Range": {"bytes=0-6"}})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != js[:7] {
		t.Errorf("range: %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != spaImmutableCache {
		t.Errorf("range: Cache-Control %q", got)
	}

	rec = mountGet(t, h, http.MethodHead, "/assets/app-1a2b3c.js", nil)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "2000" {
		t.Errorf("HEAD: %d, %d bytes, Content-Length %q", rec.Code, rec.Body.Len(), rec.Header().Get("Content-Length"))
	}
	if rec := mountGet(t, h, http.MethodHead, "/some/route", nil); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("HEAD of a route: %d, %d bytes", rec.Code, rec.Body.Len())
	}

	modified := time.Unix(1700000000, 0).UTC().Format(http.TimeFormat)
	for _, target := range []string{"/assets/app-1a2b3c.js", "/", "/some/route"} {
		rec := mountGet(t, h, http.MethodGet, target, http.Header{"If-Modified-Since": {modified}})
		if rec.Code != http.StatusNotModified {
			t.Errorf("GET %s If-Modified-Since: %d, want 304", target, rec.Code)
		}
	}

	// Compressed by Compress, as a host wraps its mux, but not in ranges.
	gz := Compress(h)
	rec = mountGet(t, gz, http.MethodGet, "/assets/app-1a2b3c.js", http.Header{"Accept-Encoding": {"gzip"}})
	if rec.Header().Get("Content-Encoding") != "gzip" || gunzip(t, rec.Body.Bytes()) != js {
		t.Errorf("Compress: Content-Encoding %q", rec.Header().Get("Content-Encoding"))
	}
	rec = mountGet(t, gz, http.MethodGet, "/assets/app-1a2b3c.js", http.Header{"Accept-Encoding": {"gzip"}, "Range": {"bytes=0-6"}})
	if rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != js[:7] {
		t.Errorf("Compress, range: Content-Encoding %q, body %q", rec.Header().Get("Content-Encoding"), rec.Body.String())
	}
}

// Without its index the app can't start, and says so, rather than
// serving a listing or a 404 that looks like a missing page.
func TestSPAMissingIndex(t *testing.T) {
	build := fstest.MapFS{"assets/app.js": {Data: []byte("x")}}
	h := SPA(build, SPAOptions{})
	for _, target := range []string{"/", "/settings"} {
		rec := mountGet(t, h, http.MethodGet, target, nil)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "index.html is missing") {
			t.Errorf("GET %s: %d %q, want 503", target, rec.Code, rec.Body.String())
		}
	}
	if rec := mountGet(t, h, http.MethodGet, "/assets/app.js", nil); rec.Code != http.StatusOK {
		t.Errorf("a file without the index: %d", rec.Code)
	}
	// Another name for the page.
	h = SPA(fstest.MapFS{"app.html": {Data: []byte("app")}}, SPAOptions{Index: "app.html"})
	if rec := mountGet(t, h, http.MethodGet, "/x", nil); rec.Code != http.StatusOK || rec.Body.String() != "app" {
		t.Errorf("Index: %d %q", rec.Code, rec.Body.String())
	}
}

// The host's routes come first; the app has the rest, the mount
// included in neither.
func TestSPABesideHandler(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"q":"`+r.URL.RawQuery+`"}`); err != nil {
			t.Error(err)
		}
	})
	mux.Handle("/media/", http.StripPrefix("/media", newTestHandler(t, Options{FS: fstest.MapFS{"a.txt": {Data: []byte("media")}}})))
	mux.Handle("/", SPA(spaBuild, SPAOptions{ImmutablePrefix: "assets/"}))
	for target, want := range map[string]string{
		"/api/items?view=rich": `{"q":"view=rich"}`,
		"/media/a.txt":         "media",
		"/mediaplayer":         string(spaBuild["index.html"].Data),
		"/":                    string(spaBuild["index.html"].Data),
	} {
		if rec := mountGet(t, mux, http.MethodGet, target, nil); rec.Body.String() != want {
			t.Errorf("GET %s: %q, want %q", target, rec.Body.String(), want)
		}
	}
}
