package gowebserver

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gowsTesting "github.com/cloudfra/gowebserver/internal/gowebserver/testing"
)

// bodyPreviewLimit bounds how much of a response body test logs include.
// Some paths below (e.g. a nested ".../index.html" request) legitimately
// receive the full enhanced-directory-listing page: net/http's ServeFile
// redirects any "index.html"-suffixed request to its containing directory,
// and that page embeds the full custom-index.html template (tens of KB) on
// every response. Logging the whole thing produces a single test-output
// line too long for downstream tooling (the CI benchmark step's report
// generator scans go test's JSON output with a bounded line buffer).
const bodyPreviewLimit = 200

func previewBody(body []byte) string {
	if len(body) <= bodyPreviewLimit {
		return string(body)
	}
	return fmt.Sprintf("%s... (%d bytes total)", body[:bodyPreviewLimit], len(body))
}

func TestWebServer_Serve_SpecialCharFiles(t *testing.T) {
	specialPaths := []struct {
		urlPath string
		desc    string
	}{
		{urlPath: "/weird%23.txt", desc: "hash-encoded (#)"},
		{urlPath: "/weird$.txt", desc: "dollar ($)"},
		{urlPath: "/weird%20%231.txt", desc: "space-hash encoded (weird #1.txt)"},
	}

	testCases := []struct {
		name   string
		source string
	}{
		{name: "zip", source: gowsTesting.MustZipFilePath(t)},
		{name: "rar", source: gowsTesting.MustRarFilePath(t)},
		{name: "7z", source: gowsTesting.MustSevenZipFilePath(t)},
		{name: "tar", source: gowsTesting.MustTarFilePath(t)},
		{name: "tar.gz", source: gowsTesting.MustTarGzFilePath(t)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := &Config{
				Serve: []Serve{
					{Source: tc.source, Endpoint: "/"},
				},
			}

			baseURL, closer := serveAsync(t, cfg)
			defer closer()

			for _, sp := range specialPaths {
				url := baseURL + sp.urlPath
				t.Run(sp.desc, func(t *testing.T) {
					resp, err := http.Get(url)
					if err != nil {
						t.Fatalf("GET %s error: %v", url, err)
					}
					defer func() { _ = resp.Body.Close() }()
					body, _ := io.ReadAll(resp.Body)
					if resp.StatusCode != http.StatusOK {
						t.Errorf("GET %s => status %d (want 200), body: %s", url, resp.StatusCode, previewBody(body))
					} else {
						t.Logf("GET %s => 200, body: %q", url, previewBody(body))
					}
				})
			}
		})
	}
}

func TestWebServer_DirectoryWithHash_Redirect(t *testing.T) {
	tmpDir := mustTempDir(t)

	dirPath := filepath.Join(tmpDir, "test#dir")
	if err := os.MkdirAll(dirPath, 0o766); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(dirPath, "file.txt")
	if err := copyFile(strings.NewReader("hello"), time.Now(), time.Now(), filePath); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		EnhancedList: true,
		Serve: []Serve{
			{Source: tmpDir, Endpoint: "/"},
		},
	}

	baseURL, closer := serveAsync(t, cfg)
	defer closer()

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Access directory without trailing / to trigger redirect
	resp, err := client.Get(baseURL + "/test%23dir")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	location := resp.Header.Get("Location")
	t.Logf("GET /test%%23dir => status %d, Location: %q", resp.StatusCode, location)

	if resp.StatusCode == 301 || resp.StatusCode == 302 {
		if strings.Contains(location, "#") && !strings.Contains(location, "%23") {
			t.Errorf("BUG: Location header contains unencoded '#': %q - browser would treat as fragment", location)
		} else if strings.Contains(location, "%23") {
			t.Log("Location header properly encodes # as %%23")
		}
	}

	// Access with trailing / should work
	resp2, err := http.Get(baseURL + "/test%23dir/")
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()
	t.Logf("GET /test%%23dir/ => status %d, body length: %d", resp2.StatusCode, len(body2))

	// Access file inside the directory
	resp3, err := http.Get(baseURL + "/test%23dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	body3, _ := io.ReadAll(resp3.Body)
	_ = resp3.Body.Close()
	t.Logf("GET /test%%23dir/file.txt => status %d, body: %q", resp3.StatusCode, previewBody([]byte(strings.TrimSpace(string(body3)))))
}

func TestWebServer_NestedArchive_SpecialCharFiles(t *testing.T) {
	nestedPath := gowsTesting.MustNestedZipFilePath(t)

	cfg := &Config{
		EnhancedList: true,
		Serve: []Serve{
			{Source: nestedPath, Endpoint: "/"},
		},
	}

	baseURL, closer := serveAsync(t, cfg)
	defer closer()

	testPaths := []struct {
		path string
		desc string
	}{
		{path: "/testassets.zip.d/weird%23.txt", desc: "nested zip: weird#.txt"},
		{path: "/testassets.zip.d/weird$.txt", desc: "nested zip: weird$.txt"},
		{path: "/testassets.zip.d/weird%20%231.txt", desc: "nested zip: weird #1.txt"},
		{path: "/testassets.zip.d/index.html", desc: "nested zip: index.html"},
		{path: "/testassets/weird%23.txt", desc: "dir/weird#.txt"},
		{path: "/testassets/weird$.txt", desc: "dir/weird$.txt"},
	}

	for _, tp := range testPaths {
		url := baseURL + tp.path
		t.Run(tp.desc, func(t *testing.T) {
			resp, err := http.Get(url)
			if err != nil {
				t.Errorf("[%s] GET %s error: %v", tp.desc, url, err)
				return
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("[%s] GET %s => status %d (want 200), body: %s", tp.desc, url, resp.StatusCode, previewBody(body))
			} else {
				t.Logf("[%s] GET %s => 200, body: %q", tp.desc, url, previewBody([]byte(strings.TrimSpace(string(body)))))
			}
		})
	}
}

func TestWebServer_Serve_SpecialCharDirListing(t *testing.T) {
	zipPath := gowsTesting.MustZipFilePath(t)

	cfg := &Config{
		EnhancedList: true,
		Serve: []Serve{
			{Source: zipPath, Endpoint: "/"},
		},
	}

	baseURL, closer := serveAsync(t, cfg)
	defer closer()

	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET / error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	expectedHrefs := []string{
		"weird%23.txt",
		"weird$.txt",
		"weird%20%231.txt",
	}

	t.Logf("Directory listing HTML length: %d", len(html))
	for _, href := range expectedHrefs {
		if !strings.Contains(html, fmt.Sprintf(`href="%s"`, href)) &&
			!strings.Contains(html, fmt.Sprintf(`href="%s?view=rich"`, href)) {
			t.Errorf("expected href containing %q not found in directory listing", href)
		} else {
			t.Logf("Found href with %q", href)
		}
	}
}
