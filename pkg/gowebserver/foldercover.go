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

// folderCover is what the file browser shows for a folder in its list: a
// photo or video from it to show in place of the folder's icon, and what
// the folder holds. It's the answer to a folder's URL with ?cover.
type folderCover struct {
	// File is the cover, a slash-separated path from the folder: one of
	// its own photos or videos, or, when it has none, one from a folder
	// below it. Empty when none was found.
	File string `json:"file,omitempty"`
	// Video says File is a video, whose thumbnail needs ffmpeg.
	Video bool `json:"video,omitempty"`
	// What the folder itself holds, not counting its subfolders' files.
	Photos  int `json:"photos"`
	Videos  int `json:"videos"`
	Folders int `json:"folders"`
}

const (
	// folderCoverDepth is how many folders down a cover is looked for
	// when a folder has no photos or videos of its own.
	folderCoverDepth = 3
	// folderCoverBudget is how many entries of folders below a folder
	// are read, at most, looking for its cover: a folder of thousands of
	// empty folders mustn't make a cover cost thousands of reads.
	folderCoverBudget = 2000
	// folderCoverFor is how long a folder's cover is kept before it's
	// looked for again, even though the folder hasn't changed: what's
	// changed below it doesn't change its modification time.
	folderCoverFor = 10 * time.Minute
	// folderCoverMax is how many folders' covers are kept.
	folderCoverMax = 20000
)

// coverEntry is one entry of a folder, for picking a cover.
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

// pickCover is the first photo of entries, or else the first video, or "".
func pickCover(entries []coverEntry) (string, bool) {
	for _, e := range entries {
		if e.photo {
			return e.name, false
		}
	}
	for _, e := range entries {
		if e.video {
			return e.name, true
		}
	}
	return "", false
}

// findFolderCover finds the cover of dir in fsys (see folderCover). A
// photo is preferred to a video, which costs ffmpeg a run to show; one of
// the folder's own to one from below it; and a nearer folder below to a
// deeper one, looked through in name order.
func findFolderCover(fsys fs.FS, dir string) (folderCover, error) {
	entries, err := readCoverDir(fsys, dir)
	if err != nil {
		return folderCover{}, err
	}
	var c folderCover
	var subdirs []string
	for _, e := range entries {
		switch {
		case e.dir:
			c.Folders++
			subdirs = append(subdirs, e.name)
		case e.photo:
			c.Photos++
		case e.video:
			c.Videos++
		}
	}
	if file, video := pickCover(entries); file != "" {
		c.File, c.Video = file, video
		return c, nil
	}
	// None of its own: the nearest one below, a level at a time.
	budget := folderCoverBudget
	level := subdirs
	for depth := 1; depth <= folderCoverDepth && len(level) > 0; depth++ {
		var next []string
		for _, sub := range level {
			if budget <= 0 {
				return c, nil
			}
			entries, err := readCoverDir(fsys, path.Join(dir, sub))
			if err != nil {
				continue // unreadable: look elsewhere
			}
			budget -= len(entries)
			if file, video := pickCover(entries); file != "" {
				c.File, c.Video = path.Join(sub, file), video
				return c, nil
			}
			for _, e := range entries {
				if e.dir {
					next = append(next, path.Join(sub, e.name))
				}
			}
		}
		level = next
	}
	return c, nil
}

// folderCovers keeps folders' covers, by folder and its modification time,
// for folderCoverFor.
type folderCovers struct {
	mu     sync.Mutex
	covers map[string]keptCover
}

type keptCover struct {
	cover folderCover
	until time.Time
}

func (k *folderCovers) get(key string) (folderCover, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	kept, ok := k.covers[key]
	if !ok || time.Now().After(kept.until) {
		return folderCover{}, false
	}
	return kept.cover, true
}

func (k *folderCovers) put(key string, c folderCover) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.covers == nil || len(k.covers) >= folderCoverMax {
		k.covers = map[string]keptCover{}
	}
	k.covers[key] = keptCover{cover: c, until: time.Now().Add(folderCoverFor)}
}

// serveFolderCover answers a folder's URL with ?cover: its cover, as JSON.
func (c *customIndexHandler) serveFolderCover(w http.ResponseWriter, r *http.Request, dir string) {
	_, span := c.metrics.tracer.Start(r.Context(), "folder.cover")
	defer span.End()
	info, err := fs.Stat(c.baseFS, dir)
	if err != nil || !info.IsDir() {
		http.NotFound(w, r)
		return
	}
	key := dir + "@" + info.ModTime().UTC().Format(time.RFC3339Nano)
	cover, kept := c.covers.get(key)
	span.SetAttributes(attribute.Bool("cached", kept))
	if !kept {
		cover, err = findFolderCover(c.baseFS, dir)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		c.covers.put(key, cover)
	}
	span.SetAttributes(attribute.Bool("found", cover.File != ""))
	w.Header().Set("Content-Type", "application/json")
	// Short: what's below the folder can change without it changing.
	w.Header().Set("Cache-Control", "private, max-age=300")
	if err := json.NewEncoder(w).Encode(cover); err != nil {
		slog.Debug("failed to write folder cover", "error", err)
	}
}
