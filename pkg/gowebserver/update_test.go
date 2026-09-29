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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpdateTrackValidated(t *testing.T) {
	if _, err := New(&Config{Update: Update{Track: "nightly"}}); err == nil || !strings.Contains(err.Error(), "update.track") {
		t.Errorf("New with an unknown track: %v, want an update.track error", err)
	}
}

func TestUpgradeEndpointWhenOff(t *testing.T) {
	ws := &webServerImpl{debugEndpoint: "/debug"}
	mux := http.NewServeMux()
	stop, err := ws.serveUpdates(mux)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Error(err)
		}
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/debug/upgrade", nil))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "update.track") {
		t.Errorf("POST with updates off: %d %q", rec.Code, rec.Body)
	}

	// Without the debug endpoint there's no route at all.
	mux = http.NewServeMux()
	if _, err := (&webServerImpl{}).serveUpdates(mux); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/debug/upgrade", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("without the debug endpoint: %d, want 404", rec.Code)
	}
}
