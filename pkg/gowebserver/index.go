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
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

var templateIndexHTML = mustLoadTemplate("template-index")

type indexHTTPHandler struct {
	tmpl   *template.Template
	params customIndexReport
	// folders is the answer to "/?folders": the serve paths, in the
	// order they were configured, which is the order the page lists
	// them in.
	folders []subfolder
}

func newIndexHTTPHandler(servePaths []string, modern bool) (*indexHTTPHandler, error) {
	templateHTML := templateIndexHTML
	if modern {
		templateHTML = customIndexHTML
	}
	tmpl, err := createTemplate(templateHTML)
	if err != nil {
		return nil, err
	}

	entries := make([]*DirEntry, len(servePaths))
	folders := make([]subfolder, len(servePaths))
	for idx, servePath := range servePaths {
		folders[idx] = subfolder{Name: strings.Trim(servePath, "/")}
		entries[idx] = &DirEntry{
			Name:      strings.Trim(servePath, "/"),
			IsDir:     true,
			IsArchive: false,
			IconClass: "folder",
			ModTime:   time.Time{},
		}
	}
	params := &customIndexReport{
		Root:             "/",
		RootName:         "/",
		DirEntries:       entries,
		SortBy:           "name",
		UseTimestamp:     false,
		HasNonMediaEntry: true,
	}

	slog.Debug("newIndexHTTPHandler", "params", params)
	// Made once here so that a template that can't be is an error now;
	// each request makes its own (see ServeHTTP).
	h := &indexHTTPHandler{tmpl: tmpl, params: *params}
	if _, err := h.page("/"); err != nil {
		return nil, err
	}

	h.folders = folders
	return h, nil
}

// page is the index as the page at requestURI. The index answers every
// path no served one does, such as a mistyped /typo/sub/, so its links to
// the assets are made for the path asked for, as a folder's are.
func (h *indexHTTPHandler) page(requestURI string) ([]byte, error) {
	params := h.params
	params.Assets = customIndexAssets.pagePath(requestURI)
	var b bytes.Buffer
	if err := h.tmpl.Execute(&b, &params); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (h *indexHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" && r.URL.Query().Has("folders") {
		writeSubfolders(w, h.folders)
		return
	}
	page, err := h.page(r.RequestURI)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Add("Content-Type", "text/html")
	if _, err := w.Write(page); err != nil {
		slog.Warn("cannot write index response", "error", err)
	}
}
