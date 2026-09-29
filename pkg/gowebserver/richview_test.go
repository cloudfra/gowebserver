// Copyright 2022 Cloudfra
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
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func makeRichViewHandler(t *testing.T, files map[string][]byte) *richViewHandler {
	t.Helper()
	testFS := fstest.MapFS{}
	for name, content := range files {
		testFS[name] = &fstest.MapFile{Data: content}
	}
	base := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("raw")); err != nil {
			t.Fatalf("w.Write() failed: %s", err)
		}
	})
	mc := &monitoringContext{}
	h, err := newRichViewHandler(base, testFS, mc.getTraceProvider())
	if err != nil {
		t.Fatalf("newRichViewHandler: %v", err)
	}
	return h
}

func TestRichViewHandler_PassThrough(t *testing.T) {
	ctx := t.Context()
	h := makeRichViewHandler(t, map[string][]byte{
		"hello.go": []byte("package main\n"),
	})

	var called bool
	base := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		if _, err := w.Write([]byte("raw content")); err != nil {
			t.Fatalf("w.Write() failed: %s", err)
		}
	})
	h.baseHandler = base

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/hello.go", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Error("expected base handler to be called when ?view=rich is absent")
	}
	if got := rec.Body.String(); got != "raw content" {
		t.Errorf("expected raw content passthrough, got: %s", got)
	}
}

func TestRichViewHandler_TextFile(t *testing.T) {
	ctx := t.Context()
	h := makeRichViewHandler(t, map[string][]byte{
		"hello.go": []byte("package main\n\nfunc main() {}\n"),
	})

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/hello.go?view=rich", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html Content-Type, got: %s", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "chroma") {
		t.Errorf("expected chroma CSS class in body, body[:300]=%q", body[:min(300, len(body))])
	}
	if !strings.Contains(body, "hello.go") {
		t.Errorf("expected filename in body")
	}
}

func TestRichViewHandler_BinaryFile(t *testing.T) {
	ctx := t.Context()
	// PNG magic bytes — detected as image/png by http.DetectContentType
	pngBytes := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRfakedata")
	h := makeRichViewHandler(t, map[string][]byte{
		"image.png": pngBytes,
	})

	var calledBase bool
	h.baseHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calledBase = true
		if _, err := w.Write([]byte("binary")); err != nil {
			t.Fatalf("w.Write() failed: %s", err)
		}
	})

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/image.png?view=rich", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !calledBase {
		t.Error("expected binary file to pass through to base handler")
	}
}

func TestRichViewHandler_Directory(t *testing.T) {
	ctx := t.Context()
	h := makeRichViewHandler(t, map[string][]byte{
		"subdir/file.txt": []byte("hello"),
	})

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/subdir?view=rich", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Errorf("expected 302 redirect for directory, got %d", rec.Code)
	}
}

func TestRichViewHandler_OversizedFile(t *testing.T) {
	ctx := t.Context()
	largeContent := bytes.Repeat([]byte("x"), richViewMaxFileSize+1)
	h := makeRichViewHandler(t, map[string][]byte{
		"large.txt": largeContent,
	})

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/large.txt?view=rich", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "too large") {
		t.Errorf("expected 'too large' message in body, got: %q", body[:min(300, len(body))])
	}
	if !strings.Contains(body, "large.txt") {
		t.Errorf("expected filename in oversized body")
	}
}

func TestRichViewHandler_ThemeOverride(t *testing.T) {
	ctx := t.Context()
	h := makeRichViewHandler(t, map[string][]byte{
		"hello.go": []byte("package main\n"),
	})

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/hello.go?view=rich&theme=dracula", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "dracula") {
		t.Errorf("expected 'dracula' theme name in body, got: %q", body[:min(300, len(body))])
	}
}

func TestRichViewHandler_HashInFileName(t *testing.T) {
	ctx := t.Context()
	h := makeRichViewHandler(t, map[string][]byte{
		"weird#1.txt": []byte("hello world\n"),
	})

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/placeholder?view=rich", nil)
	req.URL.Path = "/weird#1.txt"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `"/weird#1.txt"`) {
		t.Errorf("links must not contain a literal '#', browsers treat it as a fragment separator: %q", body)
	}
	if !strings.Contains(body, "/weird%231.txt") {
		t.Errorf("expected escaped path '/weird%%231.txt' in body, got: %q", body[:min(600, len(body))])
	}
}

func TestRichViewHandler_InvalidTheme(t *testing.T) {
	ctx := t.Context()
	h := makeRichViewHandler(t, map[string][]byte{
		"hello.go": []byte("package main\n"),
	})

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/hello.go?view=rich&theme=notavalidthemexyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, defaultChromaTheme) {
		t.Errorf("expected fallback to %q theme in body, got: %q", defaultChromaTheme, body[:min(300, len(body))])
	}
}

// TestWebServer_ServePathPrefix_RelativeLinks checks that redirects and rich
// view links stay inside a filesystem mounted under a serve path. Handlers
// run behind http.StripPrefix, so links built from r.URL.Path used to drop
// the prefix: /e/apps/README.md linked back to /apps/.
func TestWebServer_ServePathPrefix_RelativeLinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "apps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "apps", "README.md"), []byte("# hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	baseURL, closer := serveAsync(t, &Config{
		EnhancedList: true,
		Serve:        []Serve{{Source: dir, Endpoint: "/e/"}},
	})
	defer closer()

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resolve := func(t *testing.T, reqURL, ref string) string {
		t.Helper()
		base, err := url.Parse(reqURL)
		if err != nil {
			t.Fatal(err)
		}
		r, err := url.Parse(ref)
		if err != nil {
			t.Fatal(err)
		}
		return base.ResolveReference(r).Path
	}

	for _, tc := range []struct {
		reqPath string
		want    string
	}{
		{reqPath: "/e/apps", want: "/e/apps/"},
		{reqPath: "/e/apps?view=rich", want: "/e/apps/"},
	} {
		t.Run(tc.reqPath, func(t *testing.T) {
			reqURL := baseURL + tc.reqPath
			resp, err := client.Get(reqURL)
			if err != nil {
				t.Fatal(err)
			}
			if err := resp.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode < 300 || resp.StatusCode >= 400 {
				t.Fatalf("status = %d, want a redirect", resp.StatusCode)
			}
			if got := resolve(t, reqURL, resp.Header.Get("Location")); got != tc.want {
				t.Errorf("redirect resolves to %q, want %q", got, tc.want)
			}
		})
	}

	reqURL := baseURL + "/e/apps/README.md?view=rich"
	resp, err := http.Get(reqURL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		class string
		want  string
	}{
		{class: "back", want: "/e/apps/"},
		{class: "icon-btn", want: "/e/apps/README.md"},
	} {
		m := regexp.MustCompile(`class="` + tc.class + `" href="([^"]*)"`).FindSubmatch(body)
		if m == nil {
			t.Fatalf("no %q link in rich view", tc.class)
		}
		if got := resolve(t, reqURL, html.UnescapeString(string(m[1]))); got != tc.want {
			t.Errorf("%q link resolves to %q, want %q", tc.class, got, tc.want)
		}
	}
}
