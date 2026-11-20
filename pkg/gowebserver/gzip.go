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
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// gzipMinSize is the smallest response, when its length is known, worth
// compressing: below it gzip's header and the work cost more than they save.
const gzipMinSize = 1024

var gzipWriters = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

// gzipHandler compresses next's responses with gzip for a client that
// accepts it. Only text-like content is compressed (pages, scripts,
// styles, JSON, SVG...): images, video and archives are already
// compressed. A response that already has a Content-Encoding, a partial
// one (206), or one to a Range or HEAD request is passed on untouched, so
// seeking in a video and sendfile still work.
func gzipHandler(next http.Handler) http.Handler {
	return gzipHandlerLog(next, slog.Default(), defaultCompression)
}

// Compress compresses h's responses with gzip for a client that accepts
// it, as the standalone server does: only text-like content (pages,
// scripts, styles, JSON, SVG...), and never a response that already has
// a Content-Encoding, a partial one (206), a 304, or one to a Range or
// HEAD request, so seeking in a video and sendfile still work. Each
// compressed response, and each that could have been, has
// "Vary: Accept-Encoding".
//
// A response smaller than 1 KiB stays plain: by its Content-Length, or,
// without one, by holding its first KiB before deciding. A HEAD response
// gets the Vary its GET would have. CompressWith changes the size and the
// types.
//
// Wrap a host's whole mux in it, API responses and all. Nested, as when
// it wraps a mux that has a Compress of its own inside, only the
// outermost compresses. It logs nothing.
func Compress(h http.Handler) http.Handler {
	return CompressWith(h, CompressOptions{})
}

// CompressOptions configures [CompressWith].
type CompressOptions struct {
	// MinSize is the smallest body compressed, in bytes. Zero is 1024.
	MinSize int
	// Types are the media types compressed: "application/json", a whole
	// type ("text/*"), or a structured suffix ("*+json"). Nil is
	// Compress's list: text, scripts, styles, JSON, XML, SVG, WebAssembly
	// and a few fonts.
	Types []string
}

// CompressWith is [Compress] with the minimum size and the types o says.
func CompressWith(h http.Handler, o CompressOptions) http.Handler {
	c := defaultCompression
	if o.MinSize > 0 {
		c.minSize = o.MinSize
	}
	if o.Types != nil {
		c.compressible = typeMatcher(o.Types)
	}
	return gzipHandlerLog(h, slog.New(slog.DiscardHandler), c)
}

// compression is what a gzipHandler compresses.
type compression struct {
	minSize      int
	compressible func(contentType string) bool
}

var defaultCompression = compression{minSize: gzipMinSize, compressible: compressible}

// typeMatcher matches a Content-Type against types (see
// CompressOptions.Types).
func typeMatcher(types []string) func(string) bool {
	return func(contentType string) bool {
		mt, _, err := mime.ParseMediaType(contentType)
		if err != nil {
			return false
		}
		for _, t := range types {
			t = strings.ToLower(strings.TrimSpace(t))
			switch {
			case strings.HasSuffix(t, "/*"):
				if strings.HasPrefix(mt, strings.TrimSuffix(t, "*")) {
					return true
				}
			case strings.HasPrefix(t, "*+"):
				if strings.HasSuffix(mt, t[1:]) {
					return true
				}
			case t == mt:
				return true
			}
		}
		return false
	}
}

// gzipping marks a request whose response a gzipHandler further out
// compresses.
type gzipping struct{}

// gzipHandlerLog is gzipHandler, logging to log, compressing what c says.
func gzipHandlerLog(next http.Handler, log *slog.Logger, c compression) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(gzipping{}) != nil || r.Header.Get("Range") != "" {
			next.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), gzipping{}, true)
		if r.Method == http.MethodHead {
			// Not compressed, as there's no body, but with the Vary its
			// GET would have.
			next.ServeHTTP(&headVaryWriter{ResponseWriter: w, c: c}, r.WithContext(ctx))
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w, accepts: acceptsGzip(r.Header.Get("Accept-Encoding")), log: log, c: c}
		defer gw.close()
		next.ServeHTTP(gw, r.WithContext(ctx))
	})
}

// headVaryWriter adds Vary: Accept-Encoding to a HEAD response whose GET
// could be compressed.
type headVaryWriter struct {
	http.ResponseWriter
	c    compression
	done bool
}

func (w *headVaryWriter) vary() {
	if w.done {
		return
	}
	w.done = true
	h := w.Header()
	if h.Get("Content-Encoding") == "" && w.c.compressible(h.Get("Content-Type")) {
		h.Add("Vary", "Accept-Encoding")
	}
}

func (w *headVaryWriter) WriteHeader(code int) {
	if code >= http.StatusOK {
		w.vary()
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *headVaryWriter) Write(b []byte) (int, error) {
	w.vary()
	return w.ResponseWriter.Write(b)
}

// Unwrap is for http.ResponseController.
func (w *headVaryWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip.
func acceptsGzip(header string) bool {
	star := false
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "gzip" && name != "x-gzip" && name != "*" {
			continue
		}
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "q") {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					q = f
				}
			}
		}
		if name == "*" {
			star = q > 0
			continue
		}
		// An explicit gzip entry overrides "*".
		return q > 0
	}
	return star
}

// compressible reports whether a response of contentType is worth gzipping.
func compressible(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	if mt == "text/event-stream" {
		return false
	}
	if strings.HasPrefix(mt, "text/") {
		return true
	}
	switch mt {
	case "application/javascript", "application/x-javascript", "application/ecmascript",
		"application/json", "application/manifest+json", "application/ld+json",
		"application/xml", "application/xhtml+xml", "application/rss+xml", "application/atom+xml",
		"application/wasm", "application/x-ndjson", "application/x-yaml", "application/yaml",
		"application/toml", "application/x-sh", "application/sql", "application/graphql",
		"image/svg+xml", "image/x-icon", "image/vnd.microsoft.icon", "image/bmp",
		"font/ttf", "font/otf", "application/vnd.ms-fontobject":
		return true
	}
	return strings.HasSuffix(mt, "+json") || strings.HasSuffix(mt, "+xml")
}

// gzipResponseWriter decides at the response's header whether to compress
// it, then writes the body through gzip or straight on.
type gzipResponseWriter struct {
	http.ResponseWriter
	accepts     bool
	decided     bool
	wroteHeader bool
	// A status written before the body with no Content-Type, held until the
	// first write: net/http would sniff the type from it, and so do we.
	// otelhttp's writer writes the status ahead of every first Write.
	pending int
	gz      *gzip.Writer // nil unless compressing
	log     *slog.Logger
	c       compression
	// A response that could be compressed but whose length isn't known
	// is held, its status and first bytes, until it's c.minSize long
	// (then it's compressed) or ends (then it's sent plain).
	holding bool
	status  int
	held    []byte
}

// decide chooses whether to compress, from the header the handler set and
// the status, and fixes up the header to match.
func (w *gzipResponseWriter) decide(status int) {
	if w.decided {
		return
	}
	w.decided = true
	h := w.Header()
	if h.Get("Content-Encoding") != "" || !w.c.compressible(h.Get("Content-Type")) {
		return
	}
	// Whether the body is compressed depends on Accept-Encoding, which
	// caches need to know even for a client that got it plain.
	h.Add("Vary", "Accept-Encoding")
	if !w.accepts || status == http.StatusNoContent ||
		status == http.StatusPartialContent || status == http.StatusNotModified {
		return
	}
	cl := h.Get("Content-Length")
	if cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil && n < int64(w.c.minSize) {
			return
		}
	} else {
		w.holding, w.status = true, status
		return
	}
	w.startGzip()
}

// startGzip sets the header for a compressed body and starts gzip.
func (w *gzipResponseWriter) startGzip() {
	h := w.Header()
	h.Del("Content-Length")
	h.Del("Accept-Ranges")
	h.Set("Content-Encoding", "gzip")
	// The compressed bytes differ from the file's, so a strong validator
	// would be a lie; a weak one still matches If-None-Match.
	if etag := h.Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
		h.Set("ETag", "W/"+etag)
	}
	w.gz = gzipWriters.Get().(*gzip.Writer)
	w.gz.Reset(w.ResponseWriter)
}

// untyped reports whether the handler has left the body's type to sniffing.
func (w *gzipResponseWriter) untyped() bool {
	return w.Header().Get("Content-Type") == "" && w.Header().Get("Content-Encoding") == ""
}

func (w *gzipResponseWriter) sendHeader(code int) {
	w.decide(code)
	w.wroteHeader = true
	w.pending = 0
	if !w.holding {
		w.ResponseWriter.WriteHeader(code)
	}
}

// release ends holding: compressed when gzip is true, else plain, and
// sends what was held.
func (w *gzipResponseWriter) release(gzip bool) error {
	w.holding = false
	if gzip {
		w.startGzip()
	} else {
		w.Header().Set("Content-Length", strconv.Itoa(len(w.held)))
	}
	w.ResponseWriter.WriteHeader(w.status)
	held := w.held
	w.held = nil
	if len(held) == 0 {
		return nil
	}
	var err error
	if w.gz != nil {
		_, err = w.gz.Write(held)
	} else {
		_, err = w.ResponseWriter.Write(held)
	}
	return err
}

// sendPending sends a held status, if there is one.
func (w *gzipResponseWriter) sendPending() {
	if !w.wroteHeader && w.pending != 0 {
		w.sendHeader(w.pending)
	}
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if code < http.StatusOK {
		// An informational response comes before the real one, or (101)
		// hands the connection over.
		w.ResponseWriter.WriteHeader(code)
		return
	}
	if w.wroteHeader || w.pending != 0 {
		return
	}
	if w.untyped() && code != http.StatusNoContent && code != http.StatusNotModified {
		w.pending = code
		return
	}
	w.sendHeader(code)
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		if w.untyped() {
			w.Header().Set("Content-Type", http.DetectContentType(b))
		}
		code := w.pending
		if code == 0 {
			code = http.StatusOK
		}
		w.sendHeader(code)
	}
	if w.holding {
		w.held = append(w.held, b...)
		if len(w.held) >= w.c.minSize {
			if err := w.release(true); err != nil {
				return 0, err
			}
		}
		return len(b), nil
	}
	if w.gz != nil {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// ReadFrom keeps sendfile for what isn't compressed.
func (w *gzipResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	if !w.wroteHeader && !w.untyped() {
		code := w.pending
		if code == 0 {
			code = http.StatusOK
		}
		w.sendHeader(code)
	}
	if w.wroteHeader && w.gz == nil && !w.holding {
		if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
			return rf.ReadFrom(r)
		}
	}
	// Only Write, or io.Copy would come straight back here.
	return io.Copy(struct{ io.Writer }{w}, r)
}

func (w *gzipResponseWriter) Flush() {
	w.sendPending()
	if w.holding {
		// A stream: what follows is worth compressing.
		if err := w.release(true); err != nil {
			w.log.Debug("gzip flush failed", "error", err)
		}
	}
	if w.gz != nil {
		if err := w.gz.Flush(); err != nil {
			// Usually the client has gone; the next write says so too.
			w.log.Debug("gzip flush failed", "error", err)
		}
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap is for http.ResponseController.
func (w *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// close ends the response: a held status with no body, or the gzip stream.
func (w *gzipResponseWriter) close() {
	w.sendPending()
	if w.holding {
		// It ended short: plain.
		if err := w.release(false); err != nil {
			w.log.Debug("cannot write a short response", "error", err)
		}
	}
	if w.gz == nil {
		return
	}
	if err := w.gz.Close(); err != nil {
		w.log.Debug("gzip close failed", "error", err)
	}
	w.gz.Reset(io.Discard)
	gzipWriters.Put(w.gz)
	w.gz = nil
}
