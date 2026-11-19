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
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
)

var templateIndexHTML = mustLoadTemplate("template-index")

type indexHTTPHandler struct {
	page []byte
	// folders is the answer to "/?folders": the serve paths, as the
	// page lists them.
	folders []subfolder
}

func newIndexHTTPHandler(servePaths []string, modern bool) (*indexHTTPHandler, error) {
	templateHTML := templateIndexHTML
	if modern {
		templateHTML = customIndexHTML
	}
	w := &bytes.Buffer{}

	entries := make([]*DirEntry, len(servePaths))
	folders := newEntryList("name")
	for idx, servePath := range servePaths {
		folders.add(&DirEntry{Name: strings.Trim(servePath, "/"), IsDir: true})
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
	if err := executeTemplate(templateHTML, params, w); err != nil {
		return nil, err
	}

	sort.Sort(folders)
	list := make([]subfolder, 0, folders.Len())
	for _, name := range folders.EntryOrder {
		list = append(list, subfolder{Name: name})
	}

	return &indexHTTPHandler{
		page:    w.Bytes(),
		folders: list,
	}, nil
}

func (h *indexHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" && r.URL.Query().Has("folders") {
		writeSubfolders(w, h.folders)
		return
	}
	w.Header().Add("Content-Type", "text/html")
	if _, err := w.Write(h.page); err != nil {
		slog.Warn("cannot write index response", "error", err)
	}
}
