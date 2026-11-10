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
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"facette.io/natsort"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// folderPreview is what the file browser shows for a folder in its list:
// a strip of its photos and videos, and what it holds. It's the answer to
// a folder's URL with ?preview.
type folderPreview struct {
	// Items are the first of the folder's photos and videos, in the order
	// the list sorts them; when it has none of its own, ones from the
	// nearest folders below it. At most folderPreviewItems.
	Items []previewItem `json:"items"`
	// What the folder itself holds, not counting its subfolders' files.
	Photos  int `json:"photos"`
	Videos  int `json:"videos"`
	Folders int `json:"folders"`
}

// previewItem is a photo or video in a folder's preview.
type previewItem struct {
	// File is a slash-separated path from the folder.
	File string `json:"file"`
	// Video says File is a video, whose thumbnail needs ffmpeg.
	Video bool `json:"video,omitempty"`
}

const (
	// folderPreviewItems is how many photos and videos a folder's preview
	// has, at most.
	folderPreviewItems = 6
	// folderPreviewDepth is how many folders down photos and videos are
	// looked for when a folder has none of its own.
	folderPreviewDepth = 3
	// folderPreviewBudget is how many entries of folders below a folder
	// are read, at most, for its preview: a folder of thousands of empty
	// folders mustn't make a preview cost thousands of reads.
	folderPreviewBudget = 2000
	// folderPreviewFor is how long a folder's preview is kept before it's
	// looked for again, even though the folder hasn't changed: what's
	// changed below it doesn't change its modification time.
	folderPreviewFor = 10 * time.Minute
	// folderPreviewMax is how many folders' previews are kept.
	folderPreviewMax = 20000
)

// coverEntry is one entry of a folder, for a preview.
type coverEntry struct {
	name  string
	dir   bool
	photo bool
	video bool
}

// readCoverDir reads dir in fsys in natural name order (as the file
// browser lists it), leaving out hidden entries and the folders that
// mount archives (see nestedDirSuffix), which are archives, not folders.
func readCoverDir(fsys fs.FS, dir string) ([]coverEntry, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}
	out := make([]coverEntry, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() && strings.HasSuffix(name, nestedDirSuffix) && names[strings.TrimSuffix(name, nestedDirSuffix)] {
			continue
		}
		ce := coverEntry{name: name, dir: e.IsDir()}
		if !ce.dir {
			ce.photo = isImage(name)
			ce.video = !ce.photo && isVideo(name)
		}
		out = append(out, ce)
	}
	slices.SortFunc(out, func(a, b coverEntry) int {
		switch {
		case a.name == b.name:
			return 0
		case natsort.Compare(a.name, b.name):
			return -1
		}
		return 1
	})
	return out, nil
}

// addPreviewItems appends the photos and videos of entries, in dir (a
// path from the previewed folder, "" for itself), to items, up to
// folderPreviewItems.
func addPreviewItems(items []previewItem, dir string, entries []coverEntry) []previewItem {
	for _, e := range entries {
		if len(items) >= folderPreviewItems {
			break
		}
		if e.photo || e.video {
			items = append(items, previewItem{File: path.Join(dir, e.name), Video: e.video})
		}
	}
	return items
}

// findFolderPreview finds the preview of dir in fsys (see folderPreview):
// its own photos and videos, or, with none, those of the folders below
// it, nearer ones first, looked through in name order.
func findFolderPreview(fsys fs.FS, dir string) (folderPreview, error) {
	entries, err := readCoverDir(fsys, dir)
	if err != nil {
		return folderPreview{}, err
	}
	p := folderPreview{Items: []previewItem{}}
	var subdirs []string
	for _, e := range entries {
		switch {
		case e.dir:
			p.Folders++
			subdirs = append(subdirs, e.name)
		case e.photo:
			p.Photos++
		case e.video:
			p.Videos++
		}
	}
	if p.Items = addPreviewItems(p.Items, "", entries); len(p.Items) > 0 {
		return p, nil
	}
	// None of its own: from below, a level at a time.
	budget := folderPreviewBudget
	level := subdirs
	for depth := 1; depth <= folderPreviewDepth && len(level) > 0 && len(p.Items) < folderPreviewItems; depth++ {
		var next []string
		for _, sub := range level {
			if budget <= 0 || len(p.Items) >= folderPreviewItems {
				return p, nil
			}
			entries, err := readCoverDir(fsys, path.Join(dir, sub))
			if err != nil {
				continue // unreadable: look elsewhere
			}
			budget -= len(entries)
			p.Items = addPreviewItems(p.Items, sub, entries)
			for _, e := range entries {
				if e.dir {
					next = append(next, path.Join(sub, e.name))
				}
			}
		}
		level = next
	}
	return p, nil
}

// folderPreviews keeps folders' previews, by folder and its modification
// time, for folderPreviewFor.
type folderPreviews struct {
	mu       sync.Mutex
	previews map[string]keptPreview
}

type keptPreview struct {
	preview folderPreview
	until   time.Time
}

func (k *folderPreviews) get(key string) (folderPreview, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	kept, ok := k.previews[key]
	if !ok || time.Now().After(kept.until) {
		return folderPreview{}, false
	}
	return kept.preview, true
}

func (k *folderPreviews) put(key string, p folderPreview) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.previews == nil || len(k.previews) >= folderPreviewMax {
		k.previews = map[string]keptPreview{}
	}
	k.previews[key] = keptPreview{preview: p, until: time.Now().Add(folderPreviewFor)}
}

// serveFolderPreview answers a folder's URL with ?preview: its preview,
// as JSON.
func (c *customIndexHandler) serveFolderPreview(w http.ResponseWriter, r *http.Request, dir string) {
	_, span := c.metrics.tracer.Start(r.Context(), "folder.preview")
	defer span.End()
	info, err := fs.Stat(c.baseFS, dir)
	if err != nil || !info.IsDir() {
		http.NotFound(w, r)
		return
	}
	key := dir + "@" + info.ModTime().UTC().Format(time.RFC3339Nano)
	preview, kept := c.previews.get(key)
	span.SetAttributes(attribute.Bool("cached", kept))
	if !kept {
		preview, err = findFolderPreview(c.baseFS, dir)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		c.previews.put(key, preview)
	}
	span.SetAttributes(attribute.Int("items", len(preview.Items)))
	w.Header().Set("Content-Type", "application/json")
	// Short: what's below the folder can change without it changing.
	w.Header().Set("Cache-Control", "private, max-age=300")
	if err := json.NewEncoder(w).Encode(preview); err != nil {
		slog.Debug("failed to write folder preview", "error", err)
	}
}
