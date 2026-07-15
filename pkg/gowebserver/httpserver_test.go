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
	"net/http"
	"strings"
	"testing"
	"time"

	_ "embed"

	gowsTesting "github.com/cloudfra/gowebserver/internal/gowebserver/testing"
	"github.com/google/go-cmp/cmp"
	gomainTesting "github.com/jeremyje/gomain/testing"
)

var (
	//go:embed testdata/multiindex.html
	wantMultiIndex []byte

	wantIndex      = []byte("index.html")
	wantSiteJs     = []byte("site.js")
	wantAssets1Txt = []byte("assets/1.txt")
)

func TestServeAndDie(t *testing.T) {
	baseURL, closer := serveAsync(t, &Config{Debug: true})
	defer closer()

	if resp, err := http.Get(baseURL + "/diediedie"); err == nil || resp != nil {
		t.Errorf("got response when server should be dead., Response: %v, Err: %s", resp, err)
		if resp != nil {
			if err := resp.Body.Close(); err != nil {
				t.Errorf("failed to close response body, %s", err)
			}
		}
	}

	if resp, err := http.Get(baseURL); err == nil || resp != nil {
		t.Errorf("got response when server should be dead., Response: %v, Err: %s", resp, err)
		if resp != nil {
			if err := resp.Body.Close(); err != nil {
				t.Errorf("failed to close response body, %s", err)
			}
		}
	}
}

func TestDieDieDieDisabled(t *testing.T) {
	baseURL, closer := serveAsync(t, &Config{})
	defer closer()

	resp, err := http.Get(baseURL + "/diediedie")
	if resp.StatusCode != http.StatusOK || err != nil {
		t.Errorf("got error response, Response: %v, Err: %s", resp, err)
	}
	defer gowsTesting.DeferClose(t, resp.Body)()

	resp, err = http.Get(baseURL)
	if resp.StatusCode != http.StatusOK || err != nil {
		t.Errorf("server should still be alive, Response: %v, Err: %s", resp, err)
	}
	defer gowsTesting.DeferClose(t, resp.Body)()
}

func TestServe(t *testing.T) {
	ch := make(chan error)

	httpServer, err := New(&Config{})
	if err != nil {
		t.Fatal(err)
	}

	closer := gomainTesting.Main(httpServer.Serve)
	go func() {
		time.Sleep(time.Second)
		ch <- closer()
	}()

	if err := <-ch; err != nil {
		if !strings.Contains(err.Error(), "closed network connection") {
			t.Error(err)
		}
	}
}

func TestWebServer_Serve_Multi(t *testing.T) {
	zipPath := gowsTesting.MustZipFilePath(t)
	rarPath := gowsTesting.MustRarFilePath(t)
	tarXzPath := gowsTesting.MustTarXzFilePath(t)

	cfg := &Config{
		Serve: []Serve{
			{
				Source:   zipPath,
				Endpoint: "/zip",
			},
			{
				Source:   rarPath,
				Endpoint: "/rar",
			},
			{
				Source:   tarXzPath,
				Endpoint: "/tar.gz",
			},
		},
	}

	baseURL, closer := serveAsync(t, cfg)
	defer closer()

	testCases := []struct {
		url  string
		want []byte
	}{
		{url: baseURL, want: wantMultiIndex},
		{url: baseURL + "/zip", want: wantIndex},
		{url: baseURL + "/zip/", want: wantIndex},
		{url: baseURL + "/zip/site.js", want: wantSiteJs},
		{url: baseURL + "/zip/assets/1.txt", want: wantAssets1Txt},
		{url: baseURL + "/rar", want: wantIndex},
		{url: baseURL + "/rar/", want: wantIndex},
		{url: baseURL + "/rar/site.js", want: wantSiteJs},
		{url: baseURL + "/rar/assets/1.txt", want: wantAssets1Txt},
		{url: baseURL + "/tar.gz", want: wantIndex},
		{url: baseURL + "/tar.gz/", want: wantIndex},
		{url: baseURL + "/tar.gz/site.js", want: wantSiteJs},
		{url: baseURL + "/tar.gz/assets/1.txt", want: wantAssets1Txt},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.url, func(t *testing.T) {
			resp, err := http.Get(tc.url)
			if err != nil {
				t.Error(err)
			} else {
				defer gowsTesting.DeferClose(t, resp.Body)()
				got, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Error(err)
				}
				if diff := cmp.Diff(tc.want, got); diff != "" {
					t.Errorf("body mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestWebServer_Serve(t *testing.T) {
	archivePaths := []string{"/.", "/", "/index.html", "/site.js", "/assets/", "/assets/fivesix", "/assets/fivesix/", "/assets/fivesix/5.txt", "/assets/more/3.txt", "/assets/1.txt"}
	testCases := []struct {
		source string
		paths  []string
	}{
		{
			source: gowsTesting.MustZipFilePath(t),
			paths:  archivePaths,
		},
		{
			source: gowsTesting.MustRarFilePath(t),
			paths:  archivePaths,
		},
		{
			source: gowsTesting.MustSevenZipFilePath(t),
			paths:  archivePaths,
		},
		{
			source: gowsTesting.MustTarFilePath(t),
			paths:  archivePaths,
		},
		{
			source: gowsTesting.MustTarGzFilePath(t),
			paths:  archivePaths,
		},
		{
			source: gowsTesting.MustTarBzip2FilePath(t),
			paths:  archivePaths,
		},
		{
			source: gowsTesting.MustTarXzFilePath(t),
			paths:  archivePaths,
		},
		{
			source: gowsTesting.MustTarLz4FilePath(t),
			paths:  archivePaths,
		},
		{
			source: "http://example.com/",
			paths:  []string{"/"},
		},
		{
			source: "https://github.com/cloudfra/gowebserver.git",
			paths:  []string{"/", "/README.md"},
		},
		/*
			TODO: This breaks because of https://github.com/go-git/go-git/issues/143.
			{
				source: "git@github.com:cloudfra/gowebserver.git",
				paths:  []string{"/", "/README.md"},
			},
		*/
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.source, func(t *testing.T) {
			t.Parallel()

			cfg := &Config{
				Serve: []Serve{
					{
						Source:   tc.source,
						Endpoint: "/",
					},
				},
			}

			baseURL, closer := serveAsync(t, cfg)
			defer closer()

			for _, path := range tc.paths {
				resp, err := http.Get(baseURL + path)
				if err != nil {
					t.Error(err)
				} else if resp.StatusCode != http.StatusOK {
					t.Errorf("status for '%s' got: %d, want 200", path, resp.StatusCode)
				}
			}
		})
	}
}

func serveAsync(tb testing.TB, cfg *Config) (string, func()) {
	ws, err := New(cfg)
	if err != nil {
		tb.Fatal(err)
	}

	wsi, ok := ws.(*webServerImpl)
	if !ok {
		tb.Fatalf("WebServer is not of type *webServerImpl, %+v", ws)
	}

	closer := gomainTesting.Main(wsi.Serve)

	var httpPort int
	for i := range 600 {
		httpPort, _ = wsi.getPorts()
		if httpPort != 0 {
			break
		}
		if i%10 == 0 && i != 0 {
			tb.Logf("waited %d seconds", i*100)
		}
		time.Sleep(time.Millisecond * 100)
	}

	baseURL := fmt.Sprintf("http://localhost:%d", httpPort)
	if err := waitAvailable(baseURL); err != nil {
		tb.Error(err)
	}

	return baseURL, func() {
		closer()
	}
}

func waitAvailable(url string) error {
	for range 10 {
		if _, err := http.Get(url); err == nil {
			return nil
		}
		time.Sleep(time.Millisecond * 100)
	}
	return fmt.Errorf("exhausted retries while waiting for '%s'", url)
}

func TestNew(t *testing.T) {
	testCases := []struct {
		config *Config
		want   string
	}{
		{config: nil, want: "/"},
		{config: &Config{}, want: "/"},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(fmt.Sprintf("%+v", tc.config), func(t *testing.T) {
			// t.Parallel()
			got, err := New(tc.config)
			if err != nil {
				t.Fatal(err)
			}

			if got == nil {
				t.Error("WebServer is nil")
			}
		})
	}
}

func TestNormalizeHTTPPath(t *testing.T) {
	testCases := []struct {
		input string
		want  string
	}{
		{input: "", want: "/"},
		{input: "/", want: "/"},
		{input: "//", want: "/"},
		{input: "///", want: "/"},
		{input: "gowebserver/", want: "/gowebserver/"},
		{input: "/gowebserver/", want: "/gowebserver/"},
		{input: "/gowebserver", want: "/gowebserver/"},
		{input: "/goweb/server", want: "/goweb/server/"},
		{input: "goweb/server", want: "/goweb/server/"},
		{input: "goweb/server/", want: "/goweb/server/"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := normalizeHTTPPath(tc.input)
			if tc.want != got {
				t.Errorf("expected: %v, got: %v", tc.want, got)
			}
		})
	}
}
