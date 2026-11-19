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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

// pageAssetsDir is where, under the server root, a page's assets are
// served: /_gows/HASH/NAME, HASH being of their content.
const pageAssetsDir = "_gows"

// customIndexAssets are the file browser's styles, scripts and icons:
// the same for every folder, so each folder's page links to them rather
// than holding them, and the browser fetches them once.
var customIndexAssets = mustPageAssets(customIndexHTML, "custom-index.css", "icons.js", "custom-index.js")

// pageAsset is one file of a page's assets.
type pageAsset struct {
	contentType string
	body        []byte
}

// pageAssets are the files a page links to rather than holds: the parts
// that are the same on every page, kept by the browser. They are served
// under a hash of their content, so a URL always names the same bytes and
// can be cached for good (immutable), and a new build that changes them
// has new URLs.
type pageAssets struct {
	hash  string
	files map[string]pageAsset
}

// mustPageAssets is newPageAssets for the embedded templates, which are
// known to make them (TestPageAssets).
func mustPageAssets(page []byte, names ...string) *pageAssets {
	a, err := newPageAssets(page, pageThemeHTML, names...)
	if err != nil {
		panic(err)
	}
	return a
}

// assetElements are the elements an asset of each type is made in: the
// asset is the text the page would have held there, made the same way, so
// html/template leaves out its comments as it did when the page held it.
var assetElements = map[string][2]string{
	".css": {"<style>", "</style>"},
	".js":  {"<script>", "</script>"},
}

// newPageAssets makes the assets named names from the page template's
// definitions of the same names: {{define "NAME"}}...{{end}}, which can
// use the shared ones (page-style, page-script-js). They are made once,
// with no data: nothing in them comes from a request.
func newPageAssets(page, theme []byte, names ...string) (*pageAssets, error) {
	tmpl, err := template.New("").Funcs(templateFuncs).Parse(string(page))
	if err == nil {
		// The pieces the pages share (page-style, page-script-js).
		_, err = tmpl.Parse(string(theme))
	}
	if err != nil {
		return nil, err
	}
	a := &pageAssets{files: map[string]pageAsset{}}
	h := sha256.New()
	for _, name := range names {
		ext := path.Ext(name)
		element, ok := assetElements[ext]
		if !ok {
			return nil, fmt.Errorf("asset %q: not CSS or JavaScript", name)
		}
		// A template can't be added to once it has run, so each asset
		// is made in a copy.
		t, err := tmpl.Clone()
		if err == nil {
			_, err = t.New("asset").Parse(element[0] + `{{template "` + name + `"}}` + element[1])
		}
		var b bytes.Buffer
		if err == nil {
			err = t.ExecuteTemplate(&b, "asset", nil)
		}
		if err != nil {
			return nil, fmt.Errorf("asset %q: %w", name, err)
		}
		body := bytes.TrimPrefix(b.Bytes(), []byte(element[0]))
		body = bytes.TrimLeft(bytes.TrimSuffix(body, []byte(element[1])), "\r\n")
		// A checkout on Windows can have the templates' lines end in
		// CRLF; the assets, and so their hash and URLs, are the same on
		// every platform.
		body = bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
		a.files[name] = pageAsset{contentType: mime.TypeByExtension(ext), body: body}
		// A hash's Write never fails.
		h.Write([]byte(name + " " + strconv.Itoa(len(body)) + "\n"))
		h.Write(body)
	}
	a.hash = hex.EncodeToString(h.Sum(nil))[:16]
	return a, nil
}

// prefix is the path the assets are served under, from the server root.
func (a *pageAssets) prefix() string {
	return "/" + pageAssetsDir + "/" + a.hash + "/"
}

// pagePath is the link to the assets' directory from a page at
// requestURI. It is relative, like every link a page has (see
// relativeURL): the handlers run behind http.StripPrefix, and the server
// may be behind a proxy that serves it under a path of its own. It climbs
// from the page's directory to the server root, which is what makes it
// the same URL, and so the same cached file, from every folder.
func (a *pageAssets) pagePath(requestURI string) string {
	p, _, _ := strings.Cut(requestURI, "?")
	return "./" + strings.Repeat("../", max(strings.Count(p, "/")-1, 0)) + pageAssetsDir + "/" + a.hash + "/"
}

// ServeHTTP answers a request for one of the assets, under prefix().
// Its URL names its content, so the browser can keep it as long as it
// likes and never ask again; when it does ask (a reload), the ETag lets
// it be answered with 304 Not Modified.
func (a *pageAssets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, a.prefix())
	f, ok := a.files[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", f.contentType)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("ETag", `"`+a.hash+`"`)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(f.body))
}
