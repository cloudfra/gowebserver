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
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Options configures a [Handler]. The zero value of each field is the
// quiet choice: nothing is logged, measured or written outside the
// content, and no endpoint is served but the content's own.
type Options struct {
	// FS is the content to serve, such as os.DirFS of a folder or an
	// embed.FS. Exactly one of FS and Path is set.
	FS fs.FS
	// Path is the content to serve as the standalone server's -path takes
	// it: a folder, an archive (zip, tar, 7z, rar), a git repository, or
	// an http(s) URL whose server requests are passed on to. A folder
	// that isn't there yet (a path with no extension) is served once it
	// is; until then every request is a 404.
	Path string
	// Source names the content in the thumbnail store, which keeps each
	// source's thumbnails apart and knows a file's by its name, size and
	// modification time. Empty is Path, or "fs:" for FS. Give each FS
	// that shares a Thumbnails.CachePath a name of its own, and change
	// it when files can change without their time changing, as in an
	// embed.FS (whose times are all zero): its build's version will do.
	Source string

	// EnhancedIndex lists folders with the file browser and gallery
	// rather than a plain list.
	EnhancedIndex bool
	// Title names the breadcrumb's first crumb, which links to the root
	// of the handler (where it's mounted), and the page of that folder.
	// Empty uses the page's host name.
	Title string
	// Home, when set, is a link back to the host's own app, shown before
	// the breadcrumb's first crumb: the one link on the pages that leaves
	// the mount, and only because the host asks for it.
	Home HomeLink
	// Footer replaces the pages' footer, "Go Web Server" and its version;
	// HideFooter leaves the footer out.
	Footer     string
	HideFooter bool

	// Thumbnails are resized images and video frames, served for a file
	// asked for with width or height query parameters. On unless
	// disabled.
	Thumbnails ThumbnailOptions
	// FFmpeg chooses the ffmpeg used for video thumbnails and faster
	// photo ones. The zero value uses an installed ffmpeg, if there is
	// one, and never downloads one.
	FFmpeg FFmpeg
	// Upload serves the upload page, off unless Upload.Path is set.
	Upload UploadOptions
	// Diagnostics is a file to write anonymized diagnostics of browsing
	// to (see -debug.diagnostics). Empty turns them off. On, the file
	// browser sends its events to __diag at the handler's root.
	Diagnostics string

	// Logger is where the handler logs. Nil discards its logs.
	Logger *slog.Logger
	// MeterProvider and TracerProvider receive the handler's metrics and
	// spans (the same names and labels as the standalone server's). Nil
	// is a no-op; the otel globals are never read or set. Two handlers
	// on one MeterProvider report the same series, so the thumbnail
	// store's gauges are one handler's; give each its own provider, or
	// a view that tells them apart, to see both.
	MeterProvider  metric.MeterProvider
	TracerProvider trace.TracerProvider
}

// HomeLink is a link to the host's own app (see Options.Home).
type HomeLink struct {
	// Label is the link's text. Empty is "Home".
	Label string
	// URL is where it goes: a path such as "/" or "/app/", relative or
	// absolute, or an http(s) URL.
	URL string
}

// ThumbnailOptions configures a [Handler]'s thumbnails.
type ThumbnailOptions struct {
	// Disabled turns thumbnails off.
	Disabled bool
	// CachePath is the BoltDB file that keeps thumbnails across
	// restarts (see Options.Source). Empty keeps them in memory, for the
	// life of the handler. One process can't open it twice: a second
	// handler given the same file keeps its thumbnails in memory.
	CachePath string
}

// UploadOptions configures a [Handler]'s upload page.
type UploadOptions struct {
	// Path is where the upload page is served, relative to the handler's
	// root, such as "upload". Empty turns uploads off.
	Path string
	// Dir is the folder uploaded files are written to.
	Dir string
}

// Handler serves one content source: its files, folder listings (the
// file browser and gallery with Options.EnhancedIndex), thumbnails and
// the text viewer. It's an [http.Handler] to mount wherever the host
// wants it, behind [http.StripPrefix]:
//
//	mux.Handle("/media/", http.StripPrefix("/media", h))
//
// Everything it serves and every link on its pages stays under that
// path. It registers no flags, routes or globals, starts no listener,
// and never exits the process. [Handler.Close] releases what it holds.
type Handler struct {
	h       http.Handler
	closers []func() error
	once    sync.Once
	err     error
}

// NewHandler makes a [Handler] that serves what o says. ctx is for
// opening the content (cloning a git repository, say), not for the
// handler's life, which ends with Close.
func NewHandler(ctx context.Context, o Options) (_ *Handler, err error) {
	if (o.FS == nil) == (o.Path == "") {
		return nil, errors.New("gowebserver: set exactly one of Options.FS and Options.Path")
	}
	logger := o.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	mc := &monitoringContext{
		meterProvider:  o.MeterProvider,
		tracerProvider: o.TracerProvider,
		logger:         logger,
	}
	if mc.meterProvider == nil {
		mc.meterProvider = metricnoop.NewMeterProvider()
	}
	if mc.tracerProvider == nil {
		mc.tracerProvider = tracenoop.NewTracerProvider()
	}

	h := &Handler{}
	defer func() {
		if err != nil {
			err = errors.Join(err, h.Close())
		}
	}()

	if o.Diagnostics != "" {
		d, err := startDiagnostics(o.Diagnostics, logger)
		if err != nil {
			return nil, fmt.Errorf("gowebserver: diagnostics: %w", err)
		}
		mc.diag = d
		h.closers = append(h.closers, d.close)
	}

	var thumbs *thumbnailer
	if !o.Thumbnails.Disabled {
		ff, err := newFFmpeg(o.FFmpeg, logger, ffmpegOptionNames)
		if err != nil {
			if o.FFmpeg.Required {
				return nil, fmt.Errorf("gowebserver: %w", err)
			}
			logger.Warn("ffmpeg isn't usable; video thumbnails are off, and photos are made without it", "error", err,
				"hint", "point Options.FFmpeg.Path at ffmpeg, or set Options.FFmpeg.Required to fail instead")
			ff = nil
		}
		cachePath := o.Thumbnails.CachePath
		if cachePath == "" {
			cachePath = thumbnailCacheMemory
		}
		thumbs, err = newThumbnailer(Thumbnails{Enabled: true, CachePath: cachePath}, ff, mc)
		if err != nil {
			return nil, fmt.Errorf("gowebserver: thumbnails: %w", err)
		}
		h.closers = append(h.closers, thumbs.close)
	}

	if o.Home.URL != "" {
		u, err := url.Parse(o.Home.URL)
		if err != nil || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("gowebserver: Options.Home.URL %q is not a path or an http(s) URL", o.Home.URL)
		}
	}
	pages := pageOptions{enhancedList: o.EnhancedIndex, mounted: true, title: o.Title, chrome: pageChrome{
		HostLabel: o.Home.Label, HostURL: o.Home.URL, Footer: o.Footer, HideFooter: o.HideFooter,
	}}
	if pages.chrome.HostURL != "" && pages.chrome.HostLabel == "" {
		pages.chrome.HostLabel = "Home"
	}
	var content http.Handler
	switch {
	case o.FS != nil:
		source := o.Source
		if source == "" {
			source = "fs:"
		}
		content, err = newContentHandler(o.FS, source, mc, pages, thumbs)
		if err != nil {
			return nil, fmt.Errorf("gowebserver: %w", err)
		}
	case missingFolder(o.Path):
		// A folder made on first use: served once it's there.
		lazy := &lazyContent{path: o.Path, log: logger, mount: func() (http.Handler, func() error, error) {
			return newHandlerFromFSContext(context.WithoutCancel(ctx), o.Path, o.Source, mc, pages, thumbs)
		}}
		content = lazy
		h.closers = append(h.closers, lazy.close)
	default:
		var closeFS func() error
		content, closeFS, err = newHandlerFromFSContext(ctx, o.Path, o.Source, mc, pages, thumbs)
		if err != nil {
			return nil, fmt.Errorf("gowebserver: %s: %w", o.Path, err)
		}
		h.closers = append(h.closers, closeFS)
	}

	if o.Upload.Path != "" {
		if o.Upload.Dir == "" {
			return nil, errors.New("gowebserver: Options.Upload.Path is set without Upload.Dir")
		}
		if strings.Trim(o.Upload.Path, "/") == "" {
			return nil, errors.New("gowebserver: Options.Upload.Path can't be the handler's root, which is the content's")
		}
		route := "/" + strings.Trim(o.Upload.Path, "/")
		// The page links to itself relative to where it is, so it stays
		// under the mount.
		upload, err := newUploadHandler(mc, "./"+path.Base(route), o.Upload.Dir, pages.chrome)
		if err != nil {
			return nil, fmt.Errorf("gowebserver: upload: %w", err)
		}
		files := content
		content = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rootedPath(r) == route {
				setRequestHandler(r, handlerUpload)
				upload.ServeHTTP(w, r)
				return
			}
			files.ServeHTTP(w, r)
		})
	}

	if o.MeterProvider != nil || o.TracerProvider != nil {
		rm, err := newRequestMetrics(mc)
		if err != nil {
			return nil, fmt.Errorf("gowebserver: request metrics: %w", err)
		}
		content = otelhttp.NewHandler(rm.wrap(content, handlerFile), "gowebserver",
			otelhttp.WithTracerProvider(mc.getTraceProvider()),
			otelhttp.WithMeterProvider(mc.getMeterProvider()),
			otelhttp.WithPropagators(tracePropagator))
	}
	h.h = content
	return h, nil
}

// ServeHTTP serves r, whose URL path is relative to where the handler is
// mounted (as [http.StripPrefix] leaves it).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.h.ServeHTTP(w, r)
}

// Close releases the content, the thumbnail store and the diagnostics
// file, and waits for the handler's background work to stop. Requests
// still being served when it's called may fail. It's safe to call more
// than once.
func (h *Handler) Close() error {
	h.once.Do(func() {
		var errs []error
		for i := len(h.closers) - 1; i >= 0; i-- {
			errs = append(errs, h.closers[i]())
		}
		h.err = errors.Join(errs...)
	})
	return h.err
}

// missingFolder reports whether p names a local folder that isn't there
// yet: not a URL, nothing at p, and no extension, which an archive has.
func missingFolder(p string) bool {
	if strings.Contains(p, "://") || filepath.Ext(p) != "" {
		return false
	}
	_, err := os.Stat(p)
	return errors.Is(err, fs.ErrNotExist)
}

// lazyContent serves a folder that may not exist yet: each request
// before it does is a 404, and the first after it does mounts it.
type lazyContent struct {
	path  string
	log   *slog.Logger
	mount func() (http.Handler, func() error, error)

	mu      sync.Mutex
	h       http.Handler
	closeFS func() error
	closed  bool
}

func (l *lazyContent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h, err := l.handler()
	if err != nil {
		fsError(w, err, l.log)
		return
	}
	h.ServeHTTP(w, r)
}

// handler is the folder's handler, mounting it if it's there now.
func (l *lazyContent) handler() (http.Handler, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.h != nil {
		return l.h, nil
	}
	if l.closed {
		return nil, fs.ErrNotExist
	}
	if _, err := os.Stat(l.path); err != nil {
		return nil, err
	}
	h, closeFS, err := l.mount()
	if err != nil {
		return nil, err
	}
	l.h, l.closeFS = h, closeFS
	return h, nil
}

func (l *lazyContent) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if l.closeFS == nil {
		return nil
	}
	return l.closeFS()
}

// pageOptions are what the content handler's pages show.
type pageOptions struct {
	// enhancedList lists folders with the file browser.
	enhancedList bool
	// mounted starts the breadcrumb at the handler's root rather than
	// the server's: an embedded handler's (see NewHandler).
	mounted bool
	// title names the breadcrumb's first crumb; empty is the host name.
	title string
	// chrome is what the pages show around their content.
	chrome pageChrome
}

// pageChrome is what the pages show around their content, from Options:
// a link to the host's app before the breadcrumb, and the footer.
type pageChrome struct {
	HostLabel, HostURL string
	Footer             string
	HideFooter         bool
}

// home is the URL of the breadcrumb's first crumb, relative to the page r
// asked for, or "" for the server's root.
func (p pageOptions) home(r *http.Request) string {
	if !p.mounted {
		return ""
	}
	return mountRoot(r)
}

// rootedPath is r's path with a leading slash, which http.StripPrefix
// with a trailing slash ("/media/") leaves off.
func rootedPath(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/") {
		return r.URL.Path
	}
	return "/" + r.URL.Path
}

// mountRoot is the URL of the root of the handler serving r, relative to
// the page r asked for: "./" and a "../" for each folder r's path is
// below it. r's path is the handler's own, without the path it's mounted
// at (see http.StripPrefix), so the URL stays under that path wherever it
// is, and behind a proxy that adds a prefix of its own.
func mountRoot(r *http.Request) string {
	p := r.URL.EscapedPath()
	if !strings.HasPrefix(p, "/") {
		// http.StripPrefix("/media/") leaves "a/b/" for /media/a/b/.
		p = "/" + p
	}
	return "./" + strings.Repeat("../", max(strings.Count(p, "/")-1, 0))
}

// Mount serves h at prefix (such as "/media") on mux: everything under
// prefix+"/" goes to h behind http.StripPrefix, and prefix itself is
// redirected to prefix+"/" with a relative Location ("./media/"), not
// ServeMux's absolute one, so the redirect stays right behind a proxy
// that adds a path prefix of its own. A prefix of "/" or "" mounts h at
// the root.
func Mount(mux *http.ServeMux, prefix string, h http.Handler) {
	p := strings.Trim(prefix, "/")
	if p == "" {
		mux.Handle("/", h)
		return
	}
	p = "/" + p
	mux.Handle(p+"/", http.StripPrefix(p, h))
	mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
		target := relativeURL(path.Base(p)) + "/"
		if q := r.URL.RawQuery; q != "" {
			target += "?" + q
		}
		redirectRelative(w, target, http.StatusTemporaryRedirect)
	})
}
