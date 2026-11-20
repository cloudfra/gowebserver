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
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAcceptsGzip(t *testing.T) {
	for header, want := range map[string]bool{
		"":                       false,
		"gzip":                   true,
		"GZIP":                   true,
		"gzip, deflate, br":      true,
		"br;q=1.0, gzip;q=0.8":   true,
		"gzip;q=0":               false,
		"identity":               false,
		"*":                      true,
		"*;q=0":                  false,
		"gzip;q=0, *":            false,
		"*;q=0, gzip":            true,
		"deflate, x-gzip":        true,
		"gzip ; q=0.000":         false,
		"gzip;q=0.5;level=1":     true,
		"compress, deflate, br ": false,
	} {
		if got := acceptsGzip(header); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestCompressible(t *testing.T) {
	for ct, want := range map[string]bool{
		"text/html; charset=utf-8": true,
		"text/css":                 true,
		"application/javascript":   true,
		"application/json":         true,
		"application/vnd.api+json": true,
		"image/svg+xml":            true,
		"image/jpeg":               false,
		"video/mp4":                false,
		"application/zip":          false,
		"application/octet-stream": false,
		"text/event-stream":        false,
		"":                         false,
	} {
		if got := compressible(ct); got != want {
			t.Errorf("compressible(%q) = %v, want %v", ct, got, want)
		}
	}
}

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func gzipGet(h http.Handler, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// gzipWrappers are the standalone server's compression and the one a
// host wraps its own mux in, which behave the same.
var gzipWrappers = map[string]func(http.Handler) http.Handler{"gzipHandler": gzipHandler, "Compress": Compress}

func TestGzipHandler(t *testing.T) {
	for name, wrap := range gzipWrappers {
		t.Run(name, func(t *testing.T) { testGzipHandler(t, wrap) })
	}
}

func testGzipHandler(t *testing.T, gzipHandler func(http.Handler) http.Handler) {
	page := strings.Repeat("<p>hello, world</p>\n", 200)
	html := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Content-Type left to sniffing.
		if _, err := io.WriteString(w, page); err != nil {
			t.Error(err)
		}
	}))
	gz := http.Header{"Accept-Encoding": {"gzip, deflate"}}

	t.Run("compresses a page", func(t *testing.T) {
		rec := gzipGet(html, gz)
		if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("Content-Encoding %q, want gzip", got)
		}
		if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
			t.Errorf("Vary %q", got)
		}
		if rec.Body.Len() >= len(page) {
			t.Errorf("compressed %d bytes to %d", len(page), rec.Body.Len())
		}
		if got := gunzip(t, rec.Body.Bytes()); got != page {
			t.Error("body changed by compression")
		}
	})

	t.Run("plain for a client without gzip", func(t *testing.T) {
		rec := gzipGet(html, nil)
		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Content-Encoding %q", got)
		}
		if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
			t.Errorf("Vary %q, caches need it", got)
		}
		if rec.Body.String() != page {
			t.Error("body changed")
		}
	})

	t.Run("leaves images alone", func(t *testing.T) {
		img := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			if _, err := w.Write(bytes.Repeat([]byte{0xff}, 4096)); err != nil {
				t.Error(err)
			}
		}))
		rec := gzipGet(img, gz)
		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Content-Encoding %q", got)
		}
		if rec.Body.Len() != 4096 {
			t.Errorf("body %d bytes", rec.Body.Len())
		}
	})

	t.Run("leaves already encoded responses alone", func(t *testing.T) {
		enc := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Encoding", "br")
			if _, err := io.WriteString(w, page); err != nil {
				t.Error(err)
			}
		}))
		rec := gzipGet(enc, gz)
		if got := rec.Header().Get("Content-Encoding"); got != "br" || rec.Body.String() != page {
			t.Errorf("Content-Encoding %q, body changed %v", got, rec.Body.String() != page)
		}
	})

	t.Run("small responses stay plain", func(t *testing.T) {
		small := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Length", "2")
			if _, err := io.WriteString(w, "hi"); err != nil {
				t.Error(err)
			}
		}))
		rec := gzipGet(small, gz)
		if got := rec.Header().Get("Content-Encoding"); got != "" || rec.Body.String() != "hi" {
			t.Errorf("Content-Encoding %q, body %q", got, rec.Body.String())
		}
	})
}

// A file as http.ServeContent sends it: compressed whole, plain in ranges.
func TestGzipHandlerServeContent(t *testing.T) {
	for name, wrap := range gzipWrappers {
		t.Run(name, func(t *testing.T) { testGzipHandlerServeContent(t, wrap) })
	}
}

func testGzipHandlerServeContent(t *testing.T, gzipHandler func(http.Handler) http.Handler) {
	text := strings.Repeat("line of a text file\n", 500)
	h := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "file.txt", time.Unix(0, 0), strings.NewReader(text))
	}))

	rec := gzipGet(h, http.Header{"Accept-Encoding": {"gzip"}})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("status %d, Content-Encoding %q", rec.Code, rec.Header().Get("Content-Encoding"))
	}
	if cl := rec.Header().Get("Content-Length"); cl != "" {
		t.Errorf("Content-Length %s is the uncompressed length", cl)
	}
	if got := rec.Header().Get("ETag"); got != `W/"v1"` {
		t.Errorf("ETag %q, want a weak one", got)
	}
	if gunzip(t, rec.Body.Bytes()) != text {
		t.Error("body changed by compression")
	}

	rec = gzipGet(h, http.Header{"Accept-Encoding": {"gzip"}, "Range": {"bytes=0-9"}})
	if rec.Code != http.StatusPartialContent || rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != text[:10] {
		t.Errorf("range: status %d, Content-Encoding %q, body %q", rec.Code, rec.Header().Get("Content-Encoding"), rec.Body.String())
	}

	rec = gzipGet(h, http.Header{"Accept-Encoding": {"gzip"}, "If-None-Match": {`W/"v1"`}})
	if rec.Code != http.StatusNotModified {
		t.Errorf("revalidation with the weak ETag: status %d", rec.Code)
	}
}

// The file browser's page, the largest the server makes, is compressed.
func TestGzipHandlerListing(t *testing.T) {
	dir := t.TempDir()
	for i := range 50 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%d.txt", i)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mc, _, _ := testMonitoring()
	h, closeFS, err := newHandlerFromFS(dir, mc, pageOptions{enhancedList: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := closeFS(); err != nil {
			t.Error(err)
		}
	})
	rec := gzipGet(gzipHandler(measured(t, mc, h, handlerFile)), http.Header{"Accept-Encoding": {"gzip"}})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("status %d, header %v", rec.Code, rec.Header())
	}
	if page := gunzip(t, rec.Body.Bytes()); !strings.Contains(page, "file-49.txt") {
		t.Error("listing is missing its files")
	}
}

// Nested, as when a host wraps its whole mux and a handler in it has its
// own, a response is compressed once, by the outermost.
func TestCompressNested(t *testing.T) {
	page := strings.Repeat("<p>hello, world</p>\n", 200)
	var inner *httptest.ResponseRecorder
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := io.WriteString(w, page); err != nil {
			t.Error(err)
		}
	})
	for name, nested := range map[string]http.Handler{
		"Compress in Compress":    Compress(Compress(h)),
		"gzipHandler in Compress": Compress(gzipHandler(h)),
		"Compress in gzipHandler": gzipHandler(Compress(h)),
		"three deep":              Compress(http.StripPrefix("/x", Compress(Compress(h)))),
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/x/page", nil)
			req.Header.Set("Accept-Encoding", "gzip")
			inner = httptest.NewRecorder()
			nested.ServeHTTP(inner, req)
			if got := inner.Header().Values("Content-Encoding"); len(got) != 1 || got[0] != "gzip" {
				t.Fatalf("Content-Encoding %q, want one gzip", got)
			}
			if got := inner.Header().Values("Vary"); len(got) != 1 {
				t.Errorf("Vary %q, want one", got)
			}
			if got := gunzip(t, inner.Body.Bytes()); got != page {
				t.Errorf("body after one gunzip is not the page: compressed twice?")
			}
		})
	}
}

// Without a Content-Length, a short body is held and sent plain: a
// redirect's few bytes aren't made longer by gzip's header.
func TestCompressShortBodies(t *testing.T) {
	gz := http.Header{"Accept-Encoding": {"gzip"}}
	redirect := Compress(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere/", http.StatusFound)
	}))
	rec := gzipGet(redirect, gz)
	if rec.Code != http.StatusFound || rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("Location") != "/elsewhere/" {
		t.Errorf("redirect: %d, Content-Encoding %q", rec.Code, rec.Header().Get("Content-Encoding"))
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(rec.Body.Len()) {
		t.Errorf("Content-Length %q for %d bytes", got, rec.Body.Len())
	}

	// In pieces, past the minimum: compressed.
	long := strings.Repeat("<p>a line</p>\n", 200)
	pieces := Compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		for i := 0; i < len(long); i += 100 {
			if _, err := io.WriteString(w, long[i:min(i+100, len(long))]); err != nil {
				t.Error(err)
			}
		}
	}))
	rec = gzipGet(pieces, gz)
	if rec.Header().Get("Content-Encoding") != "gzip" || gunzip(t, rec.Body.Bytes()) != long {
		t.Errorf("pieces: Content-Encoding %q", rec.Header().Get("Content-Encoding"))
	}

	// A stream that flushes is compressed from its first flush.
	stream := Compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		if _, err := io.WriteString(w, "tick\n"); err != nil {
			t.Error(err)
		}
		w.(http.Flusher).Flush()
		if _, err := io.WriteString(w, "tock\n"); err != nil {
			t.Error(err)
		}
	}))
	rec = gzipGet(stream, gz)
	if rec.Header().Get("Content-Encoding") != "gzip" || gunzip(t, rec.Body.Bytes()) != "tick\ntock\n" {
		t.Errorf("stream: Content-Encoding %q", rec.Header().Get("Content-Encoding"))
	}
}

func TestCompressWith(t *testing.T) {
	gz := http.Header{"Accept-Encoding": {"gzip"}}
	body := func(contentType, s string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", contentType)
			if _, err := io.WriteString(w, s); err != nil {
				t.Error(err)
			}
		})
	}
	small := strings.Repeat("ab", 30)
	if rec := gzipGet(CompressWith(body("text/plain", small), CompressOptions{MinSize: 32}), gz); rec.Header().Get("Content-Encoding") != "gzip" {
		t.Error("MinSize 32: 60 bytes stayed plain")
	}
	if rec := gzipGet(Compress(body("text/plain", small)), gz); rec.Header().Get("Content-Encoding") != "" {
		t.Error("default: 60 bytes were compressed")
	}
	big := strings.Repeat("x", 4096)
	types := CompressOptions{Types: []string{"application/x-custom", "image/*", "*+cbor"}}
	for contentType, want := range map[string]string{
		"application/x-custom":        "gzip",
		"image/png":                   "gzip",
		"application/vnd.thing+cbor":  "gzip",
		"text/html; charset=utf-8":    "",
		"application/json":            "",
		"application/x-custom; a=b":   "gzip",
		"not a media type at all ;;;": "",
	} {
		if got := gzipGet(CompressWith(body(contentType, big), types), gz).Header().Get("Content-Encoding"); got != want {
			t.Errorf("Types, %s: Content-Encoding %q, want %q", contentType, got, want)
		}
	}
}

// HEAD has the Vary its GET would have, and no body to compress.
func TestCompressHEADVary(t *testing.T) {
	text := strings.Repeat("line\n", 1000)
	h := Compress(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "a.txt", time.Unix(0, 0), strings.NewReader(text))
	}))
	req := httptest.NewRequest(http.MethodHead, "/a.txt", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Vary") != "Accept-Encoding" || rec.Header().Get("Content-Encoding") != "" || rec.Body.Len() != 0 {
		t.Errorf("HEAD: Vary %q, Content-Encoding %q, %d bytes", rec.Header().Get("Vary"), rec.Header().Get("Content-Encoding"), rec.Body.Len())
	}
	img := Compress(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "a.png", time.Unix(0, 0), strings.NewReader(text))
	}))
	rec = httptest.NewRecorder()
	img.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/a.png", nil))
	if rec.Header().Get("Vary") != "" {
		t.Errorf("HEAD of an image: Vary %q", rec.Header().Get("Vary"))
	}
}
