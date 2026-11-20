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
	"path"
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
	// an http(s) URL whose server requests are passed on to.
	Path string

	// EnhancedIndex lists folders with the file browser and gallery
	// rather than a plain list.
	EnhancedIndex bool
	// Title names the breadcrumb's first crumb, which links to the root
	// of the handler (where it's mounted), and the page of that folder.
	// Empty uses the page's host name.
	Title string

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
	// is a no-op; the otel globals are never read or set.
	MeterProvider  metric.MeterProvider
	TracerProvider trace.TracerProvider
}

// ThumbnailOptions configures a [Handler]'s thumbnails.
type ThumbnailOptions struct {
	// Disabled turns thumbnails off.
	Disabled bool
	// CachePath is the BoltDB file that keeps thumbnails across
	// restarts. Empty keeps them in memory, for the life of the handler.
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
		ff, err := newFFmpeg(o.FFmpeg, logger)
		if err != nil {
			return nil, fmt.Errorf("gowebserver: %w", err)
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

	pages := pageOptions{enhancedList: o.EnhancedIndex, mounted: true, title: o.Title}
	var content http.Handler
	if o.FS != nil {
		content, err = newContentHandler(o.FS, "fs:", mc, pages, thumbs)
		if err != nil {
			return nil, fmt.Errorf("gowebserver: %w", err)
		}
	} else {
		var closeFS func() error
		content, closeFS, err = newHandlerFromFSContext(ctx, o.Path, mc, pages, thumbs)
		if err != nil {
			return nil, fmt.Errorf("gowebserver: %s: %w", o.Path, err)
		}
		h.closers = append(h.closers, closeFS)
	}

	if o.Upload.Path != "" {
		if o.Upload.Dir == "" {
			return nil, errors.New("gowebserver: Options.Upload.Path is set without Upload.Dir")
		}
		route := "/" + strings.Trim(o.Upload.Path, "/")
		// The page links to itself relative to where it is, so it stays
		// under the mount.
		upload, err := newUploadHandler(mc, "./"+path.Base(route), o.Upload.Dir)
		if err != nil {
			return nil, fmt.Errorf("gowebserver: upload: %w", err)
		}
		files := content
		content = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == route {
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

// pageOptions are what the content handler's pages show.
type pageOptions struct {
	// enhancedList lists folders with the file browser.
	enhancedList bool
	// mounted starts the breadcrumb at the handler's root rather than
	// the server's: an embedded handler's (see NewHandler).
	mounted bool
	// title names the breadcrumb's first crumb; empty is the host name.
	title string
}

// home is the URL of the breadcrumb's first crumb, relative to the page r
// asked for, or "" for the server's root.
func (p pageOptions) home(r *http.Request) string {
	if !p.mounted {
		return ""
	}
	return mountRoot(r)
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
