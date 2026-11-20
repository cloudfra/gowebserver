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
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/cloudfra/gowebserver/internal"
	"github.com/dustin/go-humanize"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const (
	richViewMaxFileSize = 10 * 1024 * 1024 // 10 MB
	// defaultChromaTheme pairs richViewLightTheme and richViewDarkTheme and
	// switches between them with the page's light/dark theme.
	defaultChromaTheme = "auto"
	richViewLightTheme = "github"
	richViewDarkTheme  = "github-dark"
)

var (
	richViewHTML = mustLoadTemplate("rich-view")

	// richViewThemes is the ordered list of themes shown in the picker.
	richViewThemes = func() []string {
		candidates := []string{
			"monokai", "dracula", "github", "github-dark",
			"solarized-dark", "solarized-light", "nord",
		}
		out := []string{defaultChromaTheme}
		for _, name := range candidates {
			if styles.Get(name) != nil {
				out = append(out, name)
			}
		}
		return out
	}()
)

// RichViewReport is the template data for rich-view.html. URLs are relative
// to the file's directory; see relativeURL.
type RichViewReport struct {
	FileName           string
	ParentPath         string
	RawURL             string
	Language           string
	Size               string
	Lines              int
	Theme              string
	AvailableThemes    []string
	ChromaCSS          template.CSS
	HighlightedHTML    template.HTML
	ApplicationVersion string
	Oversized          bool
	// Home and HomeTitle are the breadcrumb's start (see
	// customIndexReport).
	Home      string
	HomeTitle string
}

type richViewHandler struct {
	baseHandler http.Handler
	baseFS      fs.FS
	pages       pageOptions
	log         *slog.Logger
	metrics     *richViewMetrics
	tmpl        *template.Template
}

// The stages of showing a file in the text viewer, the "stage" of
// richview_stage_duration_seconds and the names of the spans under a
// request's "richview" span (as richview.read and so on).
const (
	richViewStageRead      = "read"      // the file, into memory
	richViewStageHighlight = "highlight" // finding its language and coloring it
	richViewStageRender    = "render"    // the page, written to the client
)

// What a text viewer request came to, the "result" of
// richview_requests_total.
const (
	richViewHighlighted = "highlighted"
	richViewOversized   = "oversized" // too large to show
	richViewBinary      = "binary"    // not text; served as the file
	richViewDirectory   = "directory" // redirected to its listing
	richViewFailed      = "failed"
)

// richViewBytes are the histogram buckets for the size of the files
// highlighted, which is what highlighting time follows.
var richViewBytes = []float64{1 << 10, 4 << 10, 16 << 10, 64 << 10, 256 << 10, 1 << 20, 4 << 20, richViewMaxFileSize}

// richViewMetrics says where the text viewer's time goes. Nothing it
// makes is kept, so every view of a file is read and highlighted again.
type richViewMetrics struct {
	*stageMetrics
	requests metric.Int64Counter
	// Size of the files highlighted.
	sourceSize metric.Int64Histogram
}

func newRichViewMetrics(mc *monitoringContext) (*richViewMetrics, error) {
	stages, m, err := newStageMetrics(mc, "richview",
		"How long each stage of showing a file in the text viewer took: read, highlight, render (the page).")
	if err != nil {
		return nil, err
	}
	rm := &richViewMetrics{stageMetrics: stages}
	if rm.requests, err = m.Int64Counter("richview_requests_total",
		metric.WithDescription("Text viewer requests, by result (highlighted, oversized, binary, directory, failed).")); err != nil {
		return nil, err
	}
	if rm.sourceSize, err = m.Int64Histogram("richview_source_bytes",
		metric.WithDescription("Size of the files the text viewer highlighted, in bytes."),
		metric.WithExplicitBucketBoundaries(richViewBytes...)); err != nil {
		return nil, err
	}
	return rm, nil
}

func newRichViewHandler(baseHandler http.Handler, baseFS fs.FS, mc *monitoringContext, pages pageOptions) (*richViewHandler, error) {
	tmpl, err := createTemplate(richViewHTML)
	if err != nil {
		return nil, err
	}
	metrics, err := newRichViewMetrics(mc)
	if err != nil {
		return nil, err
	}
	return &richViewHandler{
		baseHandler: baseHandler,
		baseFS:      baseFS,
		pages:       pages,
		log:         mc.log(),
		metrics:     metrics,
		tmpl:        tmpl,
	}, nil
}

func (h *richViewHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("view") != "rich" {
		h.baseHandler.ServeHTTP(w, r)
		return
	}

	fsPath := cleanPath(strings.TrimPrefix(r.URL.Path, "/"))

	setRequestHandler(r, handlerRichView)
	ctx, span := h.metrics.tracer.Start(r.Context(), "richview", trace.WithAttributes(attribute.String("path", fsPath)))
	defer span.End()
	result := richViewFailed
	defer func() {
		span.SetAttributes(attribute.String("result", result))
		h.metrics.requests.Add(context.WithoutCancel(ctx), 1, metric.WithAttributes(attribute.String("result", result)))
	}()

	_, _, read := h.metrics.stage(ctx, richViewStageRead)
	// The stage ends once, wherever reading stops.
	read = sync.OnceFunc(read)
	defer read()

	f, err := h.baseFS.Open(fsPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			h.log.Error("failed to close file after rich view rendering", "path", fsPath, "error", err)
		}
	}()

	stat, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if stat.IsDir() {
		target := "./"
		if !strings.HasSuffix(r.URL.Path, "/") {
			target = relativeURL(path.Base(r.URL.Path)) + "/"
		}
		result = richViewDirectory
		redirectRelative(w, target, http.StatusFound)
		return
	}

	content, err := io.ReadAll(io.LimitReader(f, int64(richViewMaxFileSize)+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	read()
	span.SetAttributes(attribute.Int64("bytes", stat.Size()))

	fileName := path.Base(r.URL.Path)
	rawURL := relativeURL(fileName)
	parentPath := "./"
	size := humanize.Bytes(uint64(stat.Size()))

	if len(content) > richViewMaxFileSize {
		result = richViewOversized
		report := &RichViewReport{
			FileName:           fileName,
			ParentPath:         parentPath,
			RawURL:             rawURL,
			Size:               size,
			ApplicationVersion: internal.Version(),
			Oversized:          true,
			Home:               h.pages.home(r),
			HomeTitle:          h.pages.title,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.tmpl.Execute(w, report); err != nil {
			h.log.Error("failed to execute rich view template for oversized file", "path", fsPath, "error", err)
		}
		return
	}

	// Fall through to raw serving for binary content.
	sniff := content
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	if ct := http.DetectContentType(sniff); !strings.HasPrefix(ct, "text/") {
		result = richViewBinary
		setRequestHandler(r, handlerFile)
		h.baseHandler.ServeHTTP(w, r)
		return
	}

	// Resolve theme.
	themeName := r.URL.Query().Get("theme")
	if themeName != defaultChromaTheme && styles.Registry[themeName] == nil {
		themeName = defaultChromaTheme
	}

	h.metrics.sourceSize.Record(context.WithoutCancel(ctx), int64(len(content)))
	_, highlightSpan, highlighted := h.metrics.stage(ctx, richViewStageHighlight)
	highlighted = sync.OnceFunc(highlighted)
	defer highlighted()

	// Detect language.
	contentStr := string(content)
	lexer := lexers.Match(fileName)
	if lexer == nil {
		lexer = lexers.Analyse(contentStr)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	formatter := chromahtml.New(
		chromahtml.WithLineNumbers(true),
		chromahtml.WithLinkableLineNumbers(true, "L"),
		chromahtml.WithClasses(true),
	)

	chromaCSS, err := richViewCSS(formatter, themeName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	iterator, err := lexer.Tokenise(nil, contentStr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var htmlBuf bytes.Buffer
	// With classes enabled the style only affects the CSS, not the markup.
	if err := formatter.Format(&htmlBuf, styles.Fallback, iterator); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	language := ""
	if cfg := lexer.Config(); cfg != nil {
		language = cfg.Name
	}
	highlightSpan.SetAttributes(attribute.String("language", language), attribute.Int("html_bytes", htmlBuf.Len()))
	highlighted()

	report := &RichViewReport{
		FileName:           fileName,
		ParentPath:         parentPath,
		RawURL:             rawURL,
		Language:           language,
		Size:               size,
		Lines:              strings.Count(contentStr, "\n") + boolToInt(!strings.HasSuffix(contentStr, "\n") && contentStr != ""),
		Theme:              themeName,
		AvailableThemes:    richViewThemes,
		ChromaCSS:          template.CSS(chromaCSS),
		HighlightedHTML:    template.HTML(htmlBuf.String()),
		ApplicationVersion: internal.Version(),
		Home:               h.pages.home(r),
		HomeTitle:          h.pages.title,
	}

	result = richViewHighlighted
	_, _, rendered := h.metrics.stage(ctx, richViewStageRender)
	defer rendered()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.Execute(w, report); err != nil {
		h.log.Error("failed to execute rich view template", "path", fsPath, "error", err)
	}
}

// richViewCSS returns the chroma stylesheet for themeName. The "auto" theme
// emits a light and a dark stylesheet, each nested under the selectors that
// the page uses for its own light and dark tokens, so the code follows the
// OS setting and the page's theme override.
func richViewCSS(formatter *chromahtml.Formatter, themeName string) (string, error) {
	write := func(name string) (string, error) {
		var b strings.Builder
		if err := formatter.WriteCSS(&b, styles.Get(name)); err != nil {
			return "", err
		}
		return b.String(), nil
	}
	if themeName != defaultChromaTheme {
		return write(themeName)
	}
	light, err := write(richViewLightTheme)
	if err != nil {
		return "", err
	}
	dark, err := write(richViewDarkTheme)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`@media (prefers-color-scheme: light) { :root:not([data-theme="dark"]) { %[1]s } }
:root[data-theme="light"] { %[1]s }
@media (prefers-color-scheme: dark) { :root:not([data-theme="light"]) { %[2]s } }
:root[data-theme="dark"] { %[2]s }
`, light, dark), nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
