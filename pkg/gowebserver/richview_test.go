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
	"net/http"
	"net/http/httptest"
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
		w.Write([]byte("raw"))
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
		w.Write([]byte("raw content"))
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
		w.Write([]byte("binary"))
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
	h := makeRichViewHandler(t, map[string][]byte{
		"weird#1.txt": []byte("hello world\n"),
	})

	req := httptest.NewRequest(http.MethodGet, "/placeholder?view=rich", nil)
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
	h := makeRichViewHandler(t, map[string][]byte{
		"hello.go": []byte("package main\n"),
	})

	req := httptest.NewRequest(http.MethodGet, "/hello.go?view=rich&theme=notavalidthemexyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, defaultChromaTheme) {
		t.Errorf("expected fallback to %q theme in body, got: %q", defaultChromaTheme, body[:min(300, len(body))])
	}
}
