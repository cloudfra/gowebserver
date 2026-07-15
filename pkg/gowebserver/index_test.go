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
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	gowsTesting "github.com/cloudfra/gowebserver/internal/gowebserver/testing"
	"github.com/google/go-cmp/cmp"
)

var (
	//go:embed testdata/test-index.html
	testIndexHTML []byte
	//go:embed testdata/test-modernindex.html
	testModernIndexHTML []byte
)

func TestTemplateIndexHTML(t *testing.T) {
	if len(templateIndexHTML) < 50 {
		t.Errorf("data/index.html was not stored")
	}
}

func TestIndexHTTPHandlerServeHTTP(t *testing.T) {
	testCases := []struct {
		modern     bool
		want       []byte
		sourceFile string
	}{
		{modern: false, want: testIndexHTML, sourceFile: "pkg/gowebserver/testdata/test-index.html"},
		{modern: true, want: testModernIndexHTML, sourceFile: "pkg/gowebserver/testdata/test-modernindex.html"},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(fmt.Sprintf("modern= %t", tc.modern), func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			h, err := newIndexHTTPHandler([]string{"/ok", "/abc"}, tc.modern)
			if err != nil {
				t.Fatal(err)
			}
			hs := httptest.NewServer(h)
			defer hs.Close()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, hs.URL+"/", nil)
			if err != nil {
				t.Fatalf("cannot create request for %s, %s", hs.URL+"/", err)
			}
			resp, err := hs.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer gowsTesting.DeferClose(t, resp.Body)

			data, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Error(err)
			}

			if diff := cmp.Diff(string(tc.want), string(data)); diff != "" {
				t.Errorf("index mismatch (-want +got):\n%s", diff)

				t.Errorf("Wanted:\n%s", string(tc.want))
				t.Errorf("Got:\n%s", string(data))
				gowsTesting.MustFile(t, tc.sourceFile, data)
			}
		})
	}
}
