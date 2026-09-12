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
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"facette.io/natsort"
	"github.com/cloudfra/gowebserver/internal"
	"github.com/cloudfra/ufs"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	//go:embed custom-index.html
	customIndexHTML []byte
	nestedDirSuffix = ".d"
)

func isSupportedGit(filePath string) bool {
	return strings.HasSuffix(strings.ToLower(filePath), ".git")
}

func newHandlerFromFS(fsSpec string, tp trace.TracerProvider, enhancedList bool) (http.Handler, func() error, error) {
	ctx := context.Background()
	// fsSpec is probably breaking this.
	if !isSupportedGit(fsSpec) && isSupportedHTTP(fsSpec) {
		handler, err := newHTTPReverseProxy(fsSpec)
		return handler, nilFuncWithError, err
	}

	nFS, err := ufs.New(ctx, fsSpec)
	if err != nil {
		return nil, nilFuncWithError, err
	}

	ci, err := newCustomIndex(http.FileServer(http.FS(nFS)), nFS, tp, enhancedList)
	if err != nil {
		return nil, nilFuncWithError, err
	}
	rv, err := newRichViewHandler(ci, nFS, tp)
	if err != nil {
		return nil, nilFuncWithError, err
	}
	return rv, nFS.Close, nil
}

func cleanPath(path string) string {
	return strings.ReplaceAll(filepath.Clean(path), "\\", "/")
}

type entryList struct {
	Entries    map[string]*DirEntry
	EntryOrder []string
	sortBy     string
}

func (l *entryList) Len() int {
	return len(l.Entries)
}

func (l *entryList) Less(i, j int) bool {
	iElem := l.Entries[l.EntryOrder[i]]
	jElem := l.Entries[l.EntryOrder[j]]
	if iElem.IsDir != jElem.IsDir {
		return iElem.IsDir
	}
	switch l.sortBy {
	case "size":
		return iElem.Size < jElem.Size
	case "size-desc":
		return jElem.Size < iElem.Size
	case "date":
		return iElem.ModTime.Before(jElem.ModTime)
	case "date-desc":
		return jElem.ModTime.Before(iElem.ModTime)
	case "name":
		return natsort.Compare(iElem.NameForSorting(), jElem.NameForSorting())
	case "name-desc":
		return natsort.Compare(jElem.NameForSorting(), iElem.NameForSorting())
	default:
		return natsort.Compare(iElem.NameForSorting(), jElem.NameForSorting())
	}
}

func (l *entryList) Swap(i, j int) {
	l.EntryOrder[i], l.EntryOrder[j] = l.EntryOrder[j], l.EntryOrder[i]
}

func (l *entryList) add(entry *DirEntry) {
	l.Entries[entry.Name] = entry
	l.EntryOrder = append(l.EntryOrder, entry.Name)
}

func newEntryList(sortBy string) *entryList {
	return &entryList{
		sortBy:     sortBy,
		Entries:    map[string]*DirEntry{},
		EntryOrder: []string{},
	}
}

// DirEntry represents a directory item's metadata.
type DirEntry struct {
	Name       string
	Size       uint64
	ModTime    time.Time
	IsDir      bool
	IsArchive  bool
	IsViewable bool
	IconClass  string
}

// NameForSorting returns the value for sorting this item in the template rendering.
func (d *DirEntry) NameForSorting() string {
	return strings.ToLower(d.Name)
}

// String returns the string representation of the [DirEntry].
func (d *DirEntry) String() string {
	if d == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s: dir= %t, archive= %t iconClass= %q", d.Name, d.IsDir, d.IsArchive, d.IconClass)
}

type customIndexReport struct {
	Root                  string
	RootName              string
	DirEntries            []*DirEntry
	SortBy                string
	UseTimestamp          bool
	HasNonMediaEntry      bool
	HasImage              bool
	HasVideo              bool
	ApplicationVersion    string
	ApplicationBuildstamp string
}

type customIndexHandler struct {
	baseHandler  http.Handler
	baseFS       fs.FS
	enhancedList bool
	tp           trace.TracerProvider
	tmpl         *template.Template
}

func canonicalizeSortBy(v string) string {
	parts := strings.Split(strings.ToLower(v), "=")
	key := parts[0]
	switch key {
	case "name", "name-desc", "size", "size-desc", "date", "date-desc":
		return key
	}
	return "name"
}

func tryListDir(fsys fs.FS, path string) {
	f, err := fsys.Open(path)
	if err != nil {
		slog.Warn("failed to open file", "path", path, "error", err)
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Error("failed to close directory listing file", "path", path, "error", err)
		}
	}()
	if dirList, ok := f.(fs.ReadDirFile); ok {
		dirs, err := dirList.ReadDir(-1)
		if err != nil {
			slog.Warn("failed to open file", "path", path, "error", err)
		}
		for _, dir := range dirs {
			slog.Info(fmt.Sprintf("- %s", dir.Name()), "path", path, "stat", statToString(dir.Info()))
		}
	} else {
		slog.Info("regular file", "path", path, "stat", statToString(f.Stat()))
	}
}

func statToString(info fs.FileInfo, err error) string {
	statStr := ""
	if err != nil {
		statStr = fmt.Sprintf("%s", err)
	} else {
		statStr = fmt.Sprintf("size: %d, isDir: %t, time: %s", info.Size(), info.IsDir(), info.ModTime())
	}
	return statStr
}

// redirectDirectoryToTrailingSlash issues a redirect to urlPath+"/" when the
// request targets a directory but the URL is missing its trailing slash. It
// reports whether it wrote a response; the caller must stop handling the
// request when it returns true.
//
// This works around a bug in net/http's own handling of this case:
// http.FileServer's localRedirect builds the Location header by appending
// "/" to the raw request path without re-encoding it. For a directory named
// e.g. "test#dir", that produces "Location: test#dir/". Browsers treat the
// unescaped '#' as a fragment delimiter, so they request "/test" instead of
// "/test#dir/", which 404s. Encoding the path with encodeURLPath before
// redirecting keeps '#' (and other reserved characters) part of the path
// instead of being read as a fragment or query string.
func (c *customIndexHandler) redirectDirectoryToTrailingSlash(w http.ResponseWriter, r *http.Request, path, urlPath string) bool {
	if strings.HasSuffix(urlPath, "/") || path == "." {
		return false
	}

	// fs.Stat uses the filesystem's StatFS implementation when available
	// (ufs.FS implements it), so this avoids opening a file handle just to
	// check IsDir.
	info, err := fs.Stat(c.baseFS, path)
	if err != nil || !info.IsDir() {
		return false
	}

	target := encodeURLPath(urlPath) + "/"
	if q := r.URL.RawQuery; q != "" {
		target += "?" + q
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
	return true
}

func (c *customIndexHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sortBy := canonicalizeSortBy(r.URL.Query().Get("sort"))
	rootTrace := c.tp.Tracer("customIndex")
	ctx, span := rootTrace.Start(r.Context(), r.URL.Path)
	defer span.End()
	span.SetAttributes(attribute.Bool("enhanced_list", c.enhancedList))
	if c.enhancedList {
		path := r.URL.Path
		urlPath := r.URL.Path
		path = cleanPath(strings.TrimPrefix(path, "/"))

		tryListDir(c.baseFS, path)
		slog.Info("customIndexHandler", "url", r.URL, "path", path)

		if c.redirectDirectoryToTrailingSlash(w, r, path, urlPath) {
			return
		}

		if strings.HasSuffix(urlPath, "/") || path == "." {
			_, openSpan := rootTrace.Start(ctx, "Open")
			openSpan.SetAttributes(attribute.String("path", path))
			f, err := c.baseFS.Open(path)
			openSpan.End()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer func() {
				_, closeSpan := rootTrace.Start(ctx, "Close")
				closeSpan.SetAttributes(attribute.String("path", path))
				if err := f.Close(); err != nil {
					slog.Error("failed to close file after serving custom index", "path", path, "error", err)
				}
				closeSpan.End()
			}()

			readDirCtx, readDirSpan := rootTrace.Start(ctx, "readDirectory")
			defer readDirSpan.End()
			rdf, ok := f.(fs.ReadDirFile)
			if ok {
				span.SetAttributes(attribute.Bool("custom_directory_list", true))
				readDirSpan.AddEvent("")
				now := time.Now()
				entries, err := rdf.ReadDir(-1)
				span.SetAttributes(attribute.Int("num_entries", len(entries)))
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				params := &customIndexReport{
					Root:                  path,
					RootName:              strings.TrimSuffix(filepath.Base(path), nestedDirSuffix),
					DirEntries:            []*DirEntry{},
					SortBy:                sortBy,
					UseTimestamp:          strings.Contains(sortBy, "date"),
					ApplicationVersion:    internal.Version(),
					ApplicationBuildstamp: internal.Buildstamp(),
				}

				allFiles := map[string]any{}
				archiveDirs := map[string]fs.DirEntry{}
				for _, entry := range entries {
					if strings.HasSuffix(entry.Name(), nestedDirSuffix) {
						archiveDirs[entry.Name()] = entry
					}
					allFiles[entry.Name()] = entry
				}
				actualArchiveDir := map[string]fs.DirEntry{}
				for name := range allFiles {
					if archiveDir, ok := archiveDirs[name+nestedDirSuffix]; ok {
						actualArchiveDir[name] = archiveDir
					}
				}

				files := newEntryList(sortBy)
				for _, entry := range entries {
					_, statFileSpan := rootTrace.Start(readDirCtx, entry.Name())

					if strings.HasSuffix(entry.Name(), nestedDirSuffix) {
						if _, ok := actualArchiveDir[strings.TrimSuffix(entry.Name(), nestedDirSuffix)]; ok {
							continue
						}
					}

					size := int64(0)
					t := now
					stat, err := entry.Info()
					if err == nil {
						t = stat.ModTime()
						size = stat.Size()
					}

					if strings.HasSuffix(entry.Name(), ".xz") {
						slog.Info(entry.Name())
					}
					_, isArchive := actualArchiveDir[entry.Name()]
					isDir := entry.IsDir() || isArchive
					iconClass := nameToIconClass(isDir, entry.Name())
					newEntry := &DirEntry{
						Name:       entry.Name(),
						Size:       uint64(size),
						ModTime:    t,
						IsDir:      isDir,
						IsArchive:  isArchive,
						IsViewable: !isDir && isRichViewable(iconClass),
						IconClass:  iconClass,
					}
					files.add(newEntry)
					statFileSpan.End()
				}

				_, sortFileSpan := rootTrace.Start(readDirCtx, "sort")
				sortFileSpan.SetAttributes(attribute.Int("num_files", files.Len()))
				sort.Sort(files)
				sortFileSpan.End()

				_, generateSpan := rootTrace.Start(readDirCtx, "applyTemplate")
				generateSpan.SetAttributes(attribute.Int("num_files", files.Len()))
				defer generateSpan.End()
				hasNonMediaEntry := false
				hasImage := false
				hasVideo := false
				for _, name := range files.EntryOrder {
					entry := files.Entries[name]
					params.DirEntries = append(params.DirEntries, entry)
					if !isMedia(entry.Name) {
						hasNonMediaEntry = true
					}
					if isImage(entry.Name) {
						hasImage = true
					}
					if isVideo(entry.Name) {
						hasVideo = true
					}
				}
				params.HasNonMediaEntry = hasNonMediaEntry
				params.HasImage = hasImage
				params.HasVideo = hasVideo

				slog.Info("Params", "params", params)
				if err := c.tmpl.Execute(w, params); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}

				return
			}
		}
	}
	span.SetAttributes(attribute.Bool("custom_directory_list", false))
	c.baseHandler.ServeHTTP(w, r)
}

func newCustomIndex(baseHandler http.Handler, baseFS fs.FS, tp trace.TracerProvider, enhancedList bool) (http.Handler, error) {
	tmpl, err := createTemplate(customIndexHTML)
	if err != nil {
		return nil, err
	}
	return &customIndexHandler{
		baseHandler:  baseHandler,
		baseFS:       baseFS,
		enhancedList: enhancedList,
		tp:           tp,
		tmpl:         tmpl,
	}, nil
}
