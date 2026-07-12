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
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"testing"

	_ "embed"

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
			h, err := newIndexHTTPHandler([]string{"/ok", "/abc"}, tc.modern)
			if err != nil {
				t.Fatal(err)
			}
			hs := httptest.NewServer(h)
			defer hs.Close()
			resp, err := hs.Client().Get(hs.URL + "/")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			data, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Error(err)
			}

			if diff := cmp.Diff(string(tc.want), string(data)); diff != "" {
				t.Errorf("index mismatch (-want +got):\n%s", diff)

				t.Errorf("Wanted:\n%s", string(tc.want))
				t.Errorf("Got:\n%s", string(data))
				writeTestFile(t, data, tc.sourceFile)
			}
		})
	}
}

func writeTestFile(tb testing.TB, data []byte, filename string) {
	f, err := os.CreateTemp("", "test-output")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Logf("writing content to %q", f.Name())
	if filename != "" {
		tb.Logf("Overwrite command: mv %s %s", f.Name(), filename)
	}
	if n, err := f.Write(data); err != nil {
		tb.Fatal(err)
	} else if n != len(data) {
		tb.Errorf("cannot write contents of data with len:%d got:%d", len(data), n)
	}
	if err := f.Close(); err != nil {
		tb.Errorf("cannot close temp file, %v", err)
	}
}
