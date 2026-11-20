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

package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

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
