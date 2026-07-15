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
	_ "embed"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/cloudfra/gowebserver/internal"
	"go.opentelemetry.io/otel/trace"
)

const (
	richViewMaxFileSize = 10 * 1024 * 1024 // 10 MB
	defaultChromaTheme  = "monokai"
)

var (
	//go:embed rich-view.html
	richViewHTML []byte

	// richViewThemes is the ordered list of themes shown in the picker.
	richViewThemes = func() []string {
		candidates := []string{
			"monokai", "dracula", "github", "github-dark",
			"solarized-dark", "solarized-light", "nord",
		}
		var out []string
		for _, name := range candidates {
			if styles.Get(name) != nil {
				out = append(out, name)
			}
		}
		return out
	}()
)

// RichViewReport is the template data for rich-view.html.
type RichViewReport struct {
	FileName           string
	FilePath           string
	ParentPath         string
	RawURL             string
	Language           string
	Theme              string
	AvailableThemes    []string
	ChromaCSS          template.CSS
	HighlightedHTML    template.HTML
	ApplicationVersion string
	Oversized          bool
}

type richViewHandler struct {
	baseHandler http.Handler
	baseFS      fs.FS
	tp          trace.TracerProvider
	tmpl        *template.Template
}

func newRichViewHandler(baseHandler http.Handler, baseFS fs.FS, tp trace.TracerProvider) (*richViewHandler, error) {
	tmpl, err := createTemplate(richViewHTML)
	if err != nil {
		return nil, err
	}
	return &richViewHandler{
		baseHandler: baseHandler,
		baseFS:      baseFS,
		tp:          tp,
		tmpl:        tmpl,
	}, nil
}

func (h *richViewHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("view") != "rich" {
		h.baseHandler.ServeHTTP(w, r)
		return
	}

	fsPath := cleanPath(strings.TrimPrefix(r.URL.Path, "/"))

	f, err := h.baseFS.Open(fsPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if stat.IsDir() {
		http.Redirect(w, r, encodeURLPath(r.URL.Path), http.StatusFound)
		return
	}

	content, err := io.ReadAll(io.LimitReader(f, int64(richViewMaxFileSize)+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	fileName := path.Base(r.URL.Path)
	filePath := encodeURLPath(r.URL.Path)
	rawURL := filePath
	parentPath := path.Dir(r.URL.Path)
	if !strings.HasSuffix(parentPath, "/") {
		parentPath += "/"
	}
	parentPath = encodeURLPath(parentPath)

	if len(content) > richViewMaxFileSize {
		report := &RichViewReport{
			FileName:           fileName,
			FilePath:           filePath,
			ParentPath:         parentPath,
			RawURL:             rawURL,
			ApplicationVersion: internal.Version(),
			Oversized:          true,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		h.tmpl.Execute(w, report)
		return
	}

	// Fall through to raw serving for binary content.
	sniff := content
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	if ct := http.DetectContentType(sniff); !strings.HasPrefix(ct, "text/") {
		h.baseHandler.ServeHTTP(w, r)
		return
	}

	// Resolve theme.
	themeName := r.URL.Query().Get("theme")
	style := styles.Get(themeName)
	if style == nil {
		themeName = defaultChromaTheme
		style = styles.Get(themeName)
	}

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

	formatter := chromahtml.New(chromahtml.WithLineNumbers(true), chromahtml.WithClasses(true))

	var cssBuilder strings.Builder
	if err := formatter.WriteCSS(&cssBuilder, style); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	iterator, err := lexer.Tokenise(nil, contentStr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var htmlBuf bytes.Buffer
	if err := formatter.Format(&htmlBuf, style, iterator); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	language := ""
	if cfg := lexer.Config(); cfg != nil {
		language = cfg.Name
	}

	report := &RichViewReport{
		FileName:           fileName,
		FilePath:           filePath,
		ParentPath:         parentPath,
		RawURL:             rawURL,
		Language:           language,
		Theme:              themeName,
		AvailableThemes:    richViewThemes,
		ChromaCSS:          template.CSS(cssBuilder.String()),
		HighlightedHTML:    template.HTML(htmlBuf.String()),
		ApplicationVersion: internal.Version(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	h.tmpl.Execute(w, report)
}
