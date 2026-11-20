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
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
)

// SPAOptions configures [SPA].
type SPAOptions struct {
	// Index is the app's page, served for every path that isn't a file.
	// Empty is "index.html".
	Index string
	// ImmutablePrefix is the folder of the build's content-hashed files,
	// such as "assets/" for Vite: they're cached for a year, as a
	// changed file has a new name. Empty marks nothing immutable.
	ImmutablePrefix string
	// Logger is where errors reading fsys are logged. Nil discards them.
	Logger *slog.Logger
}

// Cache-Control of the app's page, which is checked with the server on
// every load so a new build is picked up, and of its content-hashed
// files, which never change.
const (
	spaIndexCache     = "no-cache"
	spaImmutableCache = "public, max-age=31536000, immutable"
)

// SPA serves a built single-page app from fsys, which can be an embed.FS
// or os.DirFS of the build's folder:
//
//   - a file is served as it is, with Range, HEAD and conditional
//     requests, and files under SPAOptions.ImmutablePrefix are cached for
//     a year;
//   - any other path is the app's own route and gets its index page,
//     with "Cache-Control: no-cache", unless it names a file (its last
//     segment ends in an extension with a known media type, such as
//     .js, .css or .png; /users/jane.doe is a route) or is under
//     ImmutablePrefix, which is a 404: a missing script must not come
//     back as HTML;
//   - with no index page in fsys, those get a 503 that says so.
//
// It lists no folders, and the query string is the app's: nothing in it
// changes what's served. Only GET and HEAD are answered. Routes a host
// mounts before it (an API, say) are the host's.
func SPA(fsys fs.FS, o SPAOptions) http.Handler {
	if o.Index == "" {
		o.Index = "index.html"
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	o.ImmutablePrefix = strings.TrimPrefix(o.ImmutablePrefix, "/")
	if o.ImmutablePrefix != "" && !strings.HasSuffix(o.ImmutablePrefix, "/") {
		o.ImmutablePrefix += "/"
	}
	return &spaHandler{fsys: fsys, o: o}
}

type spaHandler struct {
	fsys fs.FS
	o    SPAOptions
}

func (s *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	immutable := s.o.ImmutablePrefix != "" && strings.HasPrefix(name+"/", s.o.ImmutablePrefix)
	if name != "" && s.serveFile(w, r, name, immutable) {
		return
	}
	if immutable || isFileName(name) {
		http.NotFound(w, r)
		return
	}
	if !s.serveFile(w, r, s.o.Index, false) {
		http.Error(w, "the app's "+s.o.Index+" is missing: it hasn't been built, or not into this folder", http.StatusServiceUnavailable)
	}
}

// isFileName reports whether name's last segment is a file's name: it
// ends in an extension with a known media type.
func isFileName(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext != "" && (spaTypes[ext] != "" || mime.TypeByExtension(ext) != "")
}

// spaTypes are the media types of the files a web app's build has, by
// extension, set on each response: Go's table (mime.TypeByExtension,
// which reads the system's) lacks some, such as .webmanifest, or differs
// by platform. It's this package's own; the global table isn't touched.
var spaTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	".wasm":        "application/wasm",
	".txt":         "text/plain; charset=utf-8",
	".xml":         "application/xml",
	".svg":         "image/svg+xml",
	".ico":         "image/x-icon",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".webp":        "image/webp",
	".avif":        "image/avif",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ttf":         "font/ttf",
	".otf":         "font/otf",
	".mp4":         "video/mp4",
	".webm":        "video/webm",
	".mp3":         "audio/mpeg",
	".pdf":         "application/pdf",
}

// serveFile serves the file at name in fsys, reporting false when there
// is no such file.
func (s *spaHandler) serveFile(w http.ResponseWriter, r *http.Request, name string, immutable bool) bool {
	if !fs.ValidPath(name) {
		return false
	}
	f, err := s.fsys.Open(name)
	if err != nil {
		return false
	}
	defer func() {
		if err := f.Close(); err != nil {
			s.o.Logger.Warn("cannot close a file of the app", "path", name, "error", err)
		}
	}()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	content, ok := f.(io.ReadSeeker)
	if !ok {
		b, err := io.ReadAll(f)
		if err != nil {
			s.o.Logger.Error("cannot read a file of the app", "path", name, "error", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return true
		}
		content = bytes.NewReader(b)
	}
	if t := spaTypes[strings.ToLower(path.Ext(name))]; t != "" {
		w.Header().Set("Content-Type", t)
	}
	switch {
	case immutable:
		w.Header().Set("Cache-Control", spaImmutableCache)
	case name == s.o.Index:
		w.Header().Set("Cache-Control", spaIndexCache)
	}
	http.ServeContent(w, r, name, info.ModTime(), content)
	return true
}
