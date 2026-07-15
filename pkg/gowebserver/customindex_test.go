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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gowsTesting "github.com/cloudfra/gowebserver/internal/gowebserver/testing"
	"github.com/cloudfra/ufs"
	"github.com/google/go-cmp/cmp"
)

func TestDirEntryString(t *testing.T) {
	entry := DirEntry{
		Name:      "abc.txt",
		Size:      500,
		ModTime:   time.Now(),
		IsDir:     false,
		IsArchive: true,
		IconClass: "archive",
	}
	got := entry.String()
	want := `abc.txt: dir= false, archive= true iconClass= "archive"`
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("nestedFS.Mode() mismatch (-want +got):\n%s", diff)
	}
}

func TestCustomIndex(t *testing.T) {
	nestedZipPath := gowsTesting.MustNestedZipFilePath(t)

	nFS, err := ufs.New(t.Context(), nestedZipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer gowsTesting.DeferClose(t, nFS)

	mc := &monitoringContext{}
	ci, err := newCustomIndex(http.FileServer(http.FS(nFS)), nFS, mc.getTraceProvider(), true)
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewUnstartedServer(ci)
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	verifyCustomIndex(t, ts.Client(), ts.URL, []string{"single-testassets.zip", "single-testassets.zip.d/", "testassets/", "testassets.zip.d/"})
	verifyCustomIndex(t, ts.Client(), ts.URL+"/testassets", []string{"index.html", "site.js", "assets/"})
	verifyCustomIndex(t, ts.Client(), ts.URL+"/testassets/", []string{"index.html", "site.js", "assets/"})
	verifyCustomIndex(t, ts.Client(), ts.URL+"/testassets.zip.d/", []string{"index.html", "site.js", "assets/"})
	verifyCustomIndex(t, ts.Client(), ts.URL+"/testassets/assets/images", []string{"ocean.jpg", "nature.jpg"})
	verifyCustomIndex(t, ts.Client(), ts.URL+"/testassets/assets/images/", []string{"ocean.jpg", "nature.jpg"})
}

func verifyCustomIndex(tb testing.TB, hc *http.Client, u string, substrs []string) {
	ctx := tb.Context()
	if len(substrs) == 0 {
		tb.Error("must have at least 1 substring to check")
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		tb.Fatalf("cannot create request for %q, %s", u, err)
	}
	res, err := hc.Do(req)
	if err != nil {
		tb.Errorf("cannot GET %s, %s", u, err)
		return
	}
	defer gowsTesting.DeferClose(tb, res.Body)

	bodyBytes, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		tb.Errorf("cannot read response from %s, %s", u, err)
		return
	}
	body := gowsTesting.RemoveLineFeed(string(bodyBytes))
	missingSubstr := true
	for _, substr := range substrs {
		if !strings.Contains(body, substr) {
			tb.Errorf("%s does not contain string '%s'", u, substr)
			missingSubstr = true
		}
	}
	if missingSubstr {
		tb.Logf("--- GET: %s\n\n%s", u, body)
	}
}
