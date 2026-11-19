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
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// subfolderList is the answer to a folder's URL with ?folders: the
// folders in it, as its listing shows them and in the same order. The
// breadcrumb's panel (page-theme/crumb-panel.js) asks a crumb's parent
// for it, to offer the crumb's siblings.
type subfolderList struct {
	Folders []subfolder `json:"folders"`
}

// subfolder is a folder in a subfolderList.
type subfolder struct {
	// Name is the folder's name, as the listing shows it.
	Name string `json:"name"`
	// Archive says Name is an archive the listing opens as a folder, at
	// Name with nestedDirSuffix.
	Archive bool `json:"archive,omitempty"`
}

// readSubfolders returns the folders in dir in fsys the way the file
// browser lists them (see serveListing): plain folders, and archives
// that have a folder mounting them, in place of that folder, sorted by
// sortBy. Only a sort by size or date reads each folder's details.
func readSubfolders(fsys fs.FS, dir, sortBy string) ([]subfolder, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}
	stat := sortBy != "name" && sortBy != "name-desc"
	list := newEntryList(sortBy)
	for _, e := range entries {
		name := e.Name()
		archive := names[name+nestedDirSuffix]
		switch {
		case strings.HasSuffix(name, nestedDirSuffix) && names[strings.TrimSuffix(name, nestedDirSuffix)]:
			continue // listed as its archive
		case !e.IsDir() && !archive:
			continue
		}
		entry := &DirEntry{Name: name, IsDir: true, IsArchive: archive}
		if stat {
			if info, err := e.Info(); err == nil {
				entry.Size = uint64(info.Size())
				entry.ModTime = info.ModTime()
			}
		}
		list.add(entry)
	}
	sort.Sort(list)
	out := make([]subfolder, 0, list.Len())
	for _, name := range list.EntryOrder {
		out = append(out, subfolder{Name: name, Archive: list.Entries[name].IsArchive})
	}
	return out, nil
}

// serveSubfolders answers a folder's URL with ?folders: its
// subfolderList, as JSON, in the order of ?sort.
func (c *customIndexHandler) serveSubfolders(w http.ResponseWriter, r *http.Request, dir string) {
	_, span := c.metrics.tracer.Start(r.Context(), "folder.subfolders")
	defer span.End()
	info, err := fs.Stat(c.baseFS, dir)
	if err != nil || !info.IsDir() {
		http.NotFound(w, r)
		return
	}
	sortBy := canonicalizeSortBy(r.URL.Query().Get("sort"))
	folders, err := readSubfolders(c.baseFS, dir, sortBy)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	span.SetAttributes(attribute.Int("folders", len(folders)), attribute.String("sort", sortBy))
	writeSubfolders(w, folders)
}

// writeSubfolders writes folders as a subfolderList. It's one directory
// read, made when the panel opens, so it's never kept: a folder made a
// moment ago is in it.
func writeSubfolders(w http.ResponseWriter, folders []subfolder) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	if err := json.NewEncoder(w).Encode(subfolderList{Folders: folders}); err != nil {
		slog.Debug("failed to write subfolders", "error", err)
	}
}
