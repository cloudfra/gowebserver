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
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	gowsTesting "github.com/cloudfra/gowebserver/internal/gowebserver/testing"
)

// TestPageAssets checks that the file browser's assets hold what the page
// used to: its styles, every script in order and the icons.
func TestPageAssets(t *testing.T) {
	read := func(file string) string {
		b, err := fs.ReadFile(templateFS, path.Join("template", file))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// holds checks that asset holds files, in order: each one's first and
	// last lines of code (html/template leaves comments out), found once.
	holds := func(asset string, files ...string) {
		text, last := string(customIndexAssets.files[asset].body), -1
		for _, file := range files {
			var code []string
			for _, line := range strings.Split(read(file), "\n") {
				line = strings.TrimSpace(line)
				if len(line) >= 20 && !strings.HasPrefix(line, "//") && !strings.HasPrefix(line, "/*") && !strings.HasPrefix(line, "*") && strings.Count(text, line) == 1 {
					code = append(code, line)
				}
			}
			if len(code) == 0 {
				t.Fatalf("%s has no line of code to look for", file)
			}
			first, end := strings.Index(text, code[0]), strings.Index(text, code[len(code)-1])
			switch {
			case first < 0 || end < 0:
				t.Errorf("%s doesn't hold %s", asset, file)
			case first < last:
				t.Errorf("%s: %s is out of order", asset, file)
			}
			last = end
		}
	}

	a := customIndexAssets
	if len(a.hash) != 16 {
		t.Errorf("hash = %q, want 16 hex digits", a.hash)
	}
	for name, f := range a.files {
		if strings.Contains(string(f.body), "{{") {
			t.Errorf("%s holds a template action", name)
		}
	}
	for name, want := range map[string]string{
		"custom-index.css": "text/css; charset=utf-8",
		"custom-index.js":  "text/javascript; charset=utf-8",
		"page-theme.js":    "text/javascript; charset=utf-8",
	} {
		if got := a.files[name].contentType; got != want {
			t.Errorf("%s content type = %q, want %q", name, got, want)
		}
	}

	holds("custom-index.css", "page-theme/page-theme.css", "custom-index/custom-index.css")
	holds("page-theme.js", "page-theme/hover-panel.js", "page-theme/page-theme.js", "page-theme/crumb-panel.js")
	holds("custom-index.js", "custom-index/util.js", "custom-index/diag.js", "custom-index/thumbs.js", "custom-index/data.js", "custom-index/folders.js", "custom-index/accent.js", "custom-index/list.js", "custom-index/grid.js", "custom-index/scrubber.js", "custom-index/slideshow.js", "custom-index/main.js")
	// As when the page held them, comments are left out.
	for _, name := range []string{"page-theme.js", "custom-index.js"} {
		if js := string(a.files[name].body); strings.Contains(js, "\n// ") || strings.Contains(js, " // ") {
			t.Errorf("%s holds comments", name)
		}
	}
	// The shared scripts are a file of their own, so a throw in them
	// doesn't stop the file browser's.
	if strings.Contains(string(a.files["custom-index.js"].body), "gowsHoverPanel =") {
		t.Errorf("custom-index.js holds the shared scripts too")
	}

	// The page links to them instead of holding them, but holds the
	// icons, which then draw without JavaScript.
	page, _, _ := strings.Cut(string(customIndexHTML), "</html>")
	for _, gone := range []string{"<style>", "'use strict'"} {
		if strings.Contains(page, gone) {
			t.Errorf("the page still holds %q", gone)
		}
	}
	for _, want := range []string{`<symbol id="icon-folder"`, `<symbol id="ui-up"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page doesn't hold the icons: no %s", want)
		}
	}
	if strings.Index(page, `page-theme.js"></script>`) > strings.Index(page, `custom-index.js"></script>`) {
		t.Errorf("the page must load page-theme.js before custom-index.js")
	}
}

// TestPageAssetsContentType checks that an asset's content type doesn't
// depend on the host's, which can map .js to text/plain (the Windows
// registry).
func TestPageAssetsContentType(t *testing.T) {
	if err := mime.AddExtensionType(".js", "text/plain; charset=utf-8"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := mime.AddExtensionType(".js", "text/javascript; charset=utf-8"); err != nil {
			t.Fatal(err)
		}
	}()
	a, err := newPageAssets([]byte(`{{define "a.js"}}x(){{end}}`), nil, "a.js")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := a.files["a.js"].contentType, "text/javascript; charset=utf-8"; got != want {
		t.Errorf("a.js content type = %q, want %q", got, want)
	}
}

func TestPageAssetsHash(t *testing.T) {
	page := []byte(`{{define "a.css"}}b{color:red}{{end}}{{define "a.js"}}x(){{end}}`)
	a, err := newPageAssets(page, nil, "a.css", "a.js")
	if err != nil {
		t.Fatal(err)
	}
	b, err := newPageAssets([]byte(strings.Replace(string(page), "red", "blue", 1)), nil, "a.css", "a.js")
	if err != nil {
		t.Fatal(err)
	}
	if a.hash == b.hash {
		t.Errorf("assets that differ have the same hash %q", a.hash)
	}
	again, err := newPageAssets(page, nil, "a.css", "a.js")
	if err != nil {
		t.Fatal(err)
	}
	if a.hash != again.hash {
		t.Errorf("the same assets hash to %q and %q", a.hash, again.hash)
	}

	if _, err := newPageAssets(page, nil, "missing.css"); err == nil {
		t.Errorf("newPageAssets() of an asset the page doesn't define: no error")
	}
	if _, err := newPageAssets([]byte(`{{define "a.txt"}}x{{end}}`), nil, "a.txt"); err == nil {
		t.Errorf("newPageAssets() of an asset that is not CSS or JavaScript: no error")
	}
}

// TestPageAssetsLineEndings checks that templates checked out with CRLF
// line endings (git on Windows) make the same assets, and so the same
// hash and URLs, as with LF.
func TestPageAssetsLineEndings(t *testing.T) {
	lf := "{{define \"a.css\"}}\n/* styles */\nb {\n  color: red;\n}\n{{end}}\n" +
		"{{define \"a.js\"}}\n// a script\nvar x = 1;\nfunction f() {\n  return x;\n}\n{{end}}\n"
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")
	a, err := newPageAssets([]byte(lf), nil, "a.css", "a.js")
	if err != nil {
		t.Fatal(err)
	}
	b, err := newPageAssets([]byte(crlf), nil, "a.css", "a.js")
	if err != nil {
		t.Fatal(err)
	}
	if a.hash != b.hash {
		t.Errorf("CRLF templates hash to %q, LF ones to %q", b.hash, a.hash)
	}
	for name, f := range a.files {
		if got := string(b.files[name].body); got != string(f.body) {
			t.Errorf("%s from CRLF = %q, from LF = %q", name, got, f.body)
		}
		if strings.Contains(string(b.files[name].body), "\r") {
			t.Errorf("%s from CRLF holds a CR", name)
		}
	}

	// The same for the file browser's own templates, as Windows would
	// check them out.
	toCRLF := func(b []byte) []byte {
		return []byte(strings.ReplaceAll(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n", "\r\n"))
	}
	c, err := newPageAssets(toCRLF(customIndexHTML), toCRLF(pageThemeHTML), "custom-index.css", "page-theme.js", "custom-index.js")
	if err != nil {
		t.Fatal(err)
	}
	if c.hash != customIndexAssets.hash {
		t.Errorf("the file browser's assets from CRLF templates hash to %q, want %q", c.hash, customIndexAssets.hash)
	}
}

func TestPageAssetsPagePath(t *testing.T) {
	a := &pageAssets{hash: "0123456789abcdef"}
	for _, tc := range []struct {
		requestURI string
		want       string
	}{
		{"/", "./_gows/0123456789abcdef/"},
		{"/?sort=date", "./_gows/0123456789abcdef/"},
		{"/a/", "./../_gows/0123456789abcdef/"},
		{"/e/a/b/", "./../../../_gows/0123456789abcdef/"},
		{"/e/a/b/?sort=size&x=/y/", "./../../../_gows/0123456789abcdef/"},
		{"/e/a%2Fb/", "./../../_gows/0123456789abcdef/"},
	} {
		if got := a.pagePath(tc.requestURI); got != tc.want {
			t.Errorf("pagePath(%q) = %q, want %q", tc.requestURI, got, tc.want)
		}
		// Resolved against the page, it is the same URL from everywhere.
		base, err := url.Parse("http://host" + tc.requestURI)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := url.Parse(a.pagePath(tc.requestURI) + "x.css")
		if err != nil {
			t.Fatal(err)
		}
		if got, want := base.ResolveReference(ref).Path, a.prefix()+"x.css"; got != want {
			t.Errorf("pagePath(%q) resolves to %q, want %q", tc.requestURI, got, want)
		}
	}
}

func TestPageAssetsServeHTTP(t *testing.T) {
	a := customIndexAssets
	if strings.Contains(pageAssetsPattern, a.hash) {
		t.Errorf("the route %q holds the hash, so it changes with every build", pageAssetsPattern)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := io.WriteString(w, "next"); err != nil {
			t.Error(err)
		}
	})
	h := a.handler(next, handlerFile)
	get := func(target string, header http.Header) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		for k, v := range header {
			r.Header[k] = v
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	for name, f := range a.files {
		w := get(a.prefix()+name, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", name, w.Code)
		}
		if got := w.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") || !strings.Contains(got, "max-age=31536000") {
			t.Errorf("GET %s: Cache-Control = %q, want it immutable for a year", name, got)
		}
		if got, want := w.Header().Get("Content-Type"), f.contentType; got != want {
			t.Errorf("GET %s: Content-Type = %q, want %q", name, got, want)
		}
		if w.Body.String() != string(f.body) {
			t.Errorf("GET %s: wrong body", name)
		}
		etag := w.Header().Get("ETag")
		if etag != `"`+a.hash+`"` {
			t.Errorf("GET %s: ETag = %q, want the hash", name, etag)
		}

		// A reload asks again, and is told nothing changed.
		w = get(a.prefix()+name, http.Header{"If-None-Match": {etag}})
		if w.Code != http.StatusNotModified {
			t.Errorf("GET %s If-None-Match: %d, want 304", name, w.Code)
		}
		if w.Body.Len() != 0 {
			t.Errorf("GET %s If-None-Match: a body of %d bytes, want none", name, w.Body.Len())
		}
	}

	// An asset under another build's hash, as from a page another
	// replica made in a rolling update: this build's file, not cached.
	other := "/" + pageAssetsDir + "/0123456789abcdef/custom-index.css"
	w := get(other, nil)
	if w.Code != http.StatusOK || w.Body.String() != string(a.files["custom-index.css"].body) {
		t.Errorf("GET %s = %d, want 200 and this build's file", other, w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("GET %s: Cache-Control = %q, want no-cache", other, got)
	}

	// Anything else is the next handler's: a served folder named the
	// same.
	for _, target := range []string{
		a.prefix() + "missing.js",
		a.prefix(),
		"/" + pageAssetsDir + "/sub/custom-index.css",
		"/" + pageAssetsDir + "/0123456789ABCDEF/custom-index.css",
		"/" + pageAssetsDir + "/mine.txt",
	} {
		if w := get(target, nil); w.Body.String() != "next" {
			t.Errorf("GET %s = %d %q, want it passed on", target, w.Code, w.Body.String())
		}
	}
	w = httptest.NewRecorder()
	a.handler(nil, "").ServeHTTP(w, httptest.NewRequest(http.MethodGet, a.prefix()+"missing.js", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("GET missing.js with nothing to pass it on to = %d, want 404", w.Code)
	}
}

// TestIndexAssetLinks checks the list of served paths, which also
// answers every path nothing else does: its links to the assets lead to
// them from whatever path it was asked for.
func TestIndexAssetLinks(t *testing.T) {
	h, err := newIndexHTTPHandler([]string{"/e", "/f"}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/", "/typo", "/typo/sub/", "/typo/sub/deeper/?sort=date"} {
		r := httptest.NewRequest(http.MethodGet, p, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		links := assetLinks.FindAllStringSubmatch(w.Body.String(), -1)
		if len(links) != len(customIndexAssets.files) {
			t.Fatalf("GET %s: %d asset links, want %d", p, len(links), len(customIndexAssets.files))
		}
		base, err := url.Parse("http://host" + p)
		if err != nil {
			t.Fatal(err)
		}
		for _, link := range links {
			ref, err := url.Parse(link[1])
			if err != nil {
				t.Fatal(err)
			}
			if got := base.ResolveReference(ref).Path; !strings.HasPrefix(got, customIndexAssets.prefix()) {
				t.Errorf("GET %s: %s resolves to %s, outside %s", p, link[1], got, customIndexAssets.prefix())
			}
		}
	}
}

// assetLinks are the links to the assets in a page.
var assetLinks = regexp.MustCompile(`(?:href|src)="([^"]*` + pageAssetsDir + `/[0-9a-f]{16}/[^"]*)"`)

// TestPageAssetsServed loads folder pages from a server, at the root and
// under a serve path, and fetches the assets they link to the way a
// browser would: resolved against the page.
func TestPageAssetsServed(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, pageAssetsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	gowsTesting.MustFile(t, filepath.Join(dir, pageAssetsDir, "mine.txt"), []byte("mine"))
	if err := os.MkdirAll(filepath.Join(dir, pageAssetsDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	gowsTesting.MustFile(t, filepath.Join(dir, pageAssetsDir, "sub", "mine.txt"), []byte("mine too"))

	for _, tc := range []struct {
		name     string
		endpoint string
		pages    []string
	}{
		{name: "root", endpoint: "/", pages: []string{"/", "/a/", "/a/b/?sort=date"}},
		// With no root, "/" is the list of served paths, which also
		// answers a path nothing is served at.
		{name: "serve path", endpoint: "/e", pages: []string{"/", "/e/", "/e/a/", "/e/a/b/", "/typo/sub/"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{EnhancedList: true, Serve: []Serve{{Source: dir, Endpoint: tc.endpoint}}}
			baseURL, closer := serveAsync(t, cfg)
			defer closer()

			fetched := map[string]bool{}
			for _, p := range tc.pages {
				pageURL, err := url.Parse(baseURL + p)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := http.Get(pageURL.String())
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				gowsTesting.DeferClose(t, resp.Body)()
				if err != nil {
					t.Fatal(err)
				}
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("GET %s = %d", p, resp.StatusCode)
				}
				links := assetLinks.FindAllStringSubmatch(string(body), -1)
				if len(links) != len(customIndexAssets.files) {
					t.Errorf("GET %s: %d asset links, want %d", p, len(links), len(customIndexAssets.files))
				}
				for _, link := range links {
					ref, err := url.Parse(link[1])
					if err != nil {
						t.Fatal(err)
					}
					u := pageURL.ResolveReference(ref)
					if !strings.HasPrefix(u.Path, customIndexAssets.prefix()) {
						t.Errorf("GET %s: %s resolves to %s, outside %s", p, link[1], u.Path, customIndexAssets.prefix())
					}
					fetched[u.Path] = true
					resp, err := http.Get(u.String())
					if err != nil {
						t.Fatal(err)
					}
					gowsTesting.DeferClose(t, resp.Body)()
					if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
						t.Errorf("GET %s (from %s) = %d, Cache-Control %q; want 200, immutable", u.Path, p, resp.StatusCode, resp.Header.Get("Cache-Control"))
					}
				}
			}
			// Every folder links to the same URLs, so they're cached once.
			if len(fetched) != len(customIndexAssets.files) {
				t.Errorf("the pages link to %d different asset URLs, want %d: %v", len(fetched), len(customIndexAssets.files), fetched)
			}

			// A folder named like the assets' directory is still the
			// served folder's, at either depth.
			if tc.endpoint == "/" {
				for file, want := range map[string]string{"mine.txt": "mine", "sub/mine.txt": "mine too"} {
					resp, err := http.Get(baseURL + "/" + pageAssetsDir + "/" + file)
					if err != nil {
						t.Fatal(err)
					}
					got, err := io.ReadAll(resp.Body)
					gowsTesting.DeferClose(t, resp.Body)()
					if err != nil {
						t.Fatal(err)
					}
					if string(got) != want {
						t.Errorf("GET /%s/%s = %q, want the served file", pageAssetsDir, file, got)
					}
				}
			}
		})
	}
}
