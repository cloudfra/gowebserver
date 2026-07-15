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
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

//go:embed template-index.html
var templateIndexHTML []byte

type indexHTTPHandler struct {
	page []byte
}

func newIndexHTTPHandler(servePaths []string, modern bool) (*indexHTTPHandler, error) {
	templateHTML := templateIndexHTML
	if modern {
		templateHTML = customIndexHTML
	}
	w := &bytes.Buffer{}

	entries := make([]*DirEntry, len(servePaths))
	for idx, servePath := range servePaths {
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

	zap.S().With("params", params).Debug("newIndexHTTPHandler")
	if err := executeTemplate(templateHTML, params, w); err != nil {
		return nil, err
	}

	return &indexHTTPHandler{
		page: w.Bytes(),
	}, nil
}

func (h *indexHTTPHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Add("Content-Type", "text/html")
	if _, err := w.Write(h.page); err != nil {
		zap.S().With("error", err).Warn("cannot write index response")
	}
}
