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
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

// pageAssetsDir is where, under the server root, a page's assets are
// served: /_gows/HASH/NAME, HASH being of their content.
const pageAssetsDir = "_gows"

// pageAssetsPattern is what the server routes to the assets: the same in
// every build, so the route and span names don't change with the hash.
const pageAssetsPattern = "/" + pageAssetsDir + "/{hash}/{name}"

// customIndexAssets are the file browser's styles and scripts: the same
// for every folder, so each folder's page links to them rather than
// holding them, and the browser fetches them once. The shared scripts
// (page-theme.js) are a file apart from the file browser's own, so that a
// throw in one doesn't stop the other.
var customIndexAssets = mustPageAssets(customIndexHTML, "custom-index.css", "page-theme.js", "custom-index.js")

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

// assetTypes are, for each kind of asset, the element it is made in and
// its content type. The asset is the text the page would have held in
// that element, made the same way, so html/template leaves out its
// comments as it did when the page held it. The content types are fixed
// rather than looked up with mime.TypeByExtension, which the host's
// settings can change: the Windows registry can make .js text/plain.
var assetTypes = map[string]struct{ open, close, contentType string }{
	".css": {"<style>", "</style>", "text/css; charset=utf-8"},
	".js":  {"<script>", "</script>", "text/javascript; charset=utf-8"},
}

// newPageAssets makes the assets named names from the page template's
// definitions of the same names: {{define "NAME"}}...{{end}}, which can
// use the shared ones in theme (page-style, page-script-js). They are
// made once, with no data: nothing in them comes from a request.
func newPageAssets(page, theme []byte, names ...string) (*pageAssets, error) {
	tmpl, err := template.New("").Funcs(templateFuncs).Parse(string(page))
	if err == nil {
		_, err = tmpl.Parse(string(theme))
	}
	if err != nil {
		return nil, err
	}
	a := &pageAssets{files: map[string]pageAsset{}}
	h := sha256.New()
	for _, name := range names {
		kind, ok := assetTypes[path.Ext(name)]
		if !ok {
			return nil, fmt.Errorf("asset %q: not CSS or JavaScript", name)
		}
		// A template can't be added to once it has run, so each asset
		// is made in a copy.
		t, err := tmpl.Clone()
		if err == nil {
			_, err = t.New("asset").Parse(kind.open + `{{template "` + name + `"}}` + kind.close)
		}
		var b bytes.Buffer
		if err == nil {
			err = t.ExecuteTemplate(&b, "asset", nil)
		}
		if err != nil {
			return nil, fmt.Errorf("asset %q: %w", name, err)
		}
		body := bytes.TrimPrefix(b.Bytes(), []byte(kind.open))
		body = bytes.TrimLeft(bytes.TrimSuffix(body, []byte(kind.close)), "\r\n")
		// A checkout on Windows can have the templates' lines end in
		// CRLF; the assets, and so their hash and URLs, are the same on
		// every platform.
		body = bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
		a.files[name] = pageAsset{contentType: kind.contentType, body: body}
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
// requestURI: up to the server root (see rootRelative), which makes it
// the same URL, and so the same cached file, from every folder.
func (a *pageAssets) pagePath(requestURI string) string {
	return rootRelative(requestURI) + pageAssetsDir + "/" + a.hash + "/"
}

// isAssetsHash reports whether s is shaped like the assets' hash: 16
// lowercase hex digits.
func isAssetsHash(s string) bool {
	if len(s) != 16 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// handler answers the requests routed to it as pageAssetsPattern. One
// that isn't for an asset is passed on to next, which answers as nextKind
// (see setRequestHandler), so a served folder named like the assets'
// directory stays reachable.
//
// An asset under this build's hash is cached for good (immutable): its
// URL names its content. One under another hash comes from a page another
// build made, such as another replica's during a rolling update. It gets
// this build's file, which is likely close, but not to be cached, so the
// browser asks again until its page is this build's too.
func (a *pageAssets) handler(next http.Handler, nextKind string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest, ok := strings.CutPrefix(r.URL.Path, "/"+pageAssetsDir+"/")
		hash, name, _ := strings.Cut(rest, "/")
		f, known := a.files[name]
		if !ok || !known || !isAssetsHash(hash) {
			if next == nil {
				http.NotFound(w, r)
				return
			}
			setRequestHandler(r, nextKind)
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", f.contentType)
		if hash == a.hash {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		// This build's ETag either way: a page from another build that
		// asks again gets this build's file once it differs.
		h.Set("ETag", `"`+a.hash+`"`)
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(f.body))
	})
}
