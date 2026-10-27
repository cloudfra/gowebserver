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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// measured is h as the server mounts it: inside otelhttp's handler and
// the request metrics, its requests kind's unless it says otherwise.
func measured(t *testing.T, mc *monitoringContext, h http.Handler, kind string) http.Handler {
	t.Helper()
	rm, err := newRequestMetrics(mc)
	if err != nil {
		t.Fatal(err)
	}
	return otelhttp.NewHandler(rm.wrap(h, kind), "/",
		otelhttp.WithTracerProvider(mc.getTraceProvider()), otelhttp.WithMeterProvider(mc.getMeterProvider()), otelhttp.WithPropagators(tracePropagator))
}

func get(t *testing.T, url string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err := resp.Body.Close(); err != nil {
		t.Error(err)
	}
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, err %v", url, resp.StatusCode, err)
	}
	return string(body)
}

// byHandler is points, whose keys are attributes written "k=v,k=v",
// summed by their handler attribute.
func byHandler(points map[string]int64) map[string]int64 {
	got := map[string]int64{}
	for key, n := range points {
		handler := "(none)"
		for _, kv := range strings.Split(key, ",") {
			if v, ok := strings.CutPrefix(kv, "handler="); ok {
				handler = v
			}
		}
		got[handler] += n
	}
	return got
}

// Every request under a served filesystem has the one route. The
// metrics tell a listing, a file, the text viewer and a thumbnail apart,
// and each says where its time went.
func TestRequestMetrics(t *testing.T) {
	dir := t.TempDir()
	const entries = 200
	for i := range entries {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%03d.txt", i)), []byte("text\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	source := []byte("package main\n\nfunc main() {}\n")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "photo.jpg"), encodeJPEG(t, stripes(400, 200)), 0o600); err != nil {
		t.Fatal(err)
	}

	mc, reader, spans := testMonitoring()
	thumbs, err := newThumbnailer(Thumbnails{Enabled: true, CachePath: thumbnailCacheMemory}, nil, mc)
	if err != nil {
		t.Fatal(err)
	}
	h, closeFS, err := newHandlerFromFS(dir, mc, true, thumbs, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := errors.Join(thumbs.close(), closeFS()); err != nil {
			t.Error(err)
		}
	})
	ts := httptest.NewServer(measured(t, mc, h, handlerFile))
	// Closed before the metrics are read, so every request has finished.
	closed := false
	defer func() {
		if !closed {
			ts.Close()
		}
	}()

	if page := get(t, ts.URL+"/"); !strings.Contains(page, "file199.txt") {
		t.Error("the listing doesn't hold the directory's files")
	}
	if got := get(t, ts.URL+"/main.go"); got != string(source) {
		t.Errorf("file = %q, want %q", got, source)
	}
	if page := get(t, ts.URL+"/main.go?view=rich"); !strings.Contains(page, "chroma") {
		t.Error("the text viewer's page isn't highlighted")
	}
	get(t, ts.URL+"/photo.jpg?width=64")
	ts.Close()
	closed = true

	got := collected(t, reader)
	each := map[string]int64{handlerListing: 1, handlerFile: 1, handlerRichView: 1, handlerThumbnail: 1}
	for _, name := range []string{"http_server_time_to_first_byte_seconds", "http.server.request.duration", "http.server.response.body.size"} {
		if diff := cmp.Diff(each, byHandler(got[name])); diff != "" {
			t.Errorf("%s by handler (-want +got):\n%s", name, diff)
		}
	}
	want := map[string]map[string]int64{
		"http_server_active_requests":     {"handler=file": 0},
		"listing_entries":                 {"": 1},
		"listing_stage_duration_seconds":  {"stage=open": 1, "stage=read": 1, "stage=stat": 1, "stage=sort": 1, "stage=render": 1},
		"richview_requests_total":         {"result=highlighted": 1},
		"richview_source_bytes":           {"": 1},
		"richview_stage_duration_seconds": {"stage=read": 1, "stage=highlight": 1, "stage=render": 1},
	}
	for name, points := range want {
		if diff := cmp.Diff(points, got[name]); diff != "" {
			t.Errorf("%s (-want +got):\n%s", name, diff)
		}
	}

	// A span per stage, however many entries the directory holds.
	names := spanNames(spans)
	for _, name := range []string{
		"listing", "listing.open", "listing.read", "listing.stat", "listing.sort", "listing.render",
		"richview", "richview.read", "richview.highlight", "richview.render",
		"thumbnail", "thumbnail.lookup", "thumbnail.decode",
	} {
		if names[name] != 1 {
			t.Errorf("%d %s spans, want 1", names[name], name)
		}
	}
	if total := len(spans.Ended()); total > entries/4 {
		t.Errorf("%d spans for four requests: %v", total, names)
	}
}

func TestRequestMetricsUpload(t *testing.T) {
	dir := t.TempDir()
	mc, reader, _ := testMonitoring()
	h, err := newUploadHandler(mc, "/upload", dir)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(measured(t, mc, h, handlerUpload))
	defer ts.Close()

	upload := func(origin string, want int) {
		t.Helper()
		req, err := newUploadFormRequest(t.Context(), ts.URL+"/upload", "notes.txt", bytes.NewReader([]byte("notes")), nil)
		if err != nil {
			t.Fatal(err)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Error(err)
		}
		if resp.StatusCode != want {
			t.Errorf("upload from %q: status %d, want %d", origin, resp.StatusCode, want)
		}
	}
	upload("", http.StatusOK)
	upload("http://elsewhere.example", http.StatusForbidden)

	got := collected(t, reader)
	want := map[string]map[string]int64{
		"upload_request_duration_seconds":        {"result=ok": 1, "result=rejected": 1},
		"uploaded_files_total":                   {"": 1},
		"uploaded_bytes_total":                   {"": 5},
		"http_server_time_to_first_byte_seconds": {"handler=upload": 2},
	}
	for name, points := range want {
		if diff := cmp.Diff(points, got[name]); diff != "" {
			t.Errorf("%s (-want +got):\n%s", name, diff)
		}
	}
}

// A proxied request is measured as the proxy's, its call to the other
// server is in the client metrics, and that server is handed the trace.
func TestRequestMetricsProxy(t *testing.T) {
	var traceparent string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceparent = r.Header.Get("traceparent")
		if _, err := w.Write([]byte("from the backend")); err != nil {
			t.Error(err)
		}
	}))
	defer backend.Close()

	mc, reader, spans := testMonitoring()
	h, _, err := newHandlerFromFS(backend.URL, mc, true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(measured(t, mc, h, handlerFile))
	defer ts.Close()

	if got := get(t, ts.URL+"/page"); got != "from the backend" {
		t.Errorf("body = %q", got)
	}
	got := collected(t, reader)
	if diff := cmp.Diff(map[string]int64{"handler=proxy": 1}, got["http_server_time_to_first_byte_seconds"]); diff != "" {
		t.Errorf("http_server_time_to_first_byte_seconds (-want +got):\n%s", diff)
	}
	if len(got["http.client.request.duration"]) != 1 {
		t.Errorf("http.client.request.duration = %v, want the call to the backend", got["http.client.request.duration"])
	}
	var client string
	for _, s := range spans.Ended() {
		if s.Name() == "HTTP GET" || s.Name() == "GET" {
			client = s.SpanContext().TraceID().String()
		}
	}
	if client == "" || !strings.Contains(traceparent, client) {
		t.Errorf("the backend was sent traceparent %q, want the trace %q of the proxy's client span (spans: %v)", traceparent, client, spanNames(spans))
	}
}
