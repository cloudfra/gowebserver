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
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"facette.io/natsort"
	"github.com/cloudfra/gowebserver/internal"
	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
	"github.com/cloudfra/ufs"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var (
	customIndexHTML = mustLoadTemplate("custom-index")
	nestedDirSuffix = ".d"
)

func isSupportedGit(filePath string) bool {
	return strings.HasSuffix(strings.ToLower(filePath), ".git")
}

// newHandlerFromFS serves the content at fsSpec: the file browser, the
// text viewer, thumbnails (when thumbs is set), videos' metadata and every
// photo's, video's and song's details (with ff, which may be nil, for
// what needs ffmpeg).
func newHandlerFromFS(fsSpec string, mc *monitoringContext, enhancedList bool, thumbs *thumbnailer, ff *ffmpeg.FFmpeg) (http.Handler, func() error, error) {
	ctx := context.Background()
	// fsSpec is probably breaking this.
	if !isSupportedGit(fsSpec) && isSupportedHTTP(fsSpec) {
		handler, err := newHTTPReverseProxy(fsSpec, mc)
		return handler, nilFuncWithError, err
	}

	nFS, err := ufs.New(ctx, fsSpec)
	if err != nil {
		return nil, nilFuncWithError, err
	}

	ci, err := newCustomIndex(http.FileServer(http.FS(nFS)), nFS, mc, enhancedList, thumbs)
	if err != nil {
		return nil, nilFuncWithError, err
	}
	rv, err := newRichViewHandler(ci, nFS, mc)
	if err != nil {
		return nil, nilFuncWithError, err
	}
	var h http.Handler = rv
	if thumbs != nil {
		h = thumbs.handler(rv, nFS, fsSpec)
	}
	vm := newVideoMetaHandler(h, nFS, fsSpec, thumbs, ff)
	return newMediaInfoHandler(vm, nFS, vm), nFS.Close, nil
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
	// Sidecar is a file of a video's metadata (see videometa.go) whose
	// video is in the same folder: it's listed under the video, as one
	// of its Badges, not as a file of its own.
	Sidecar bool
	// Badges are what a video has beside it (badgeSubtitles and so on).
	Badges int
}

// The kinds of entry in the file browser's list, as the page is sent them
// (files in custom-index.html, read by data.js and list.js).
const (
	listKindFile     = 0 // a file, opened as itself
	listKindViewable = 1 // a file opened in the text viewer (?view=rich)
	listKindFolder   = 2
	listKindArchive  = 3 // an archive, opened as the folder of its content
)

// ListKind returns what the file browser's list does with the entry: one
// of the listKind constants.
func (d *DirEntry) ListKind() int {
	switch {
	case d.IsArchive:
		return listKindArchive
	case d.IsDir:
		return listKindFolder
	case d.IsViewable:
		return listKindViewable
	}
	return listKindFile
}

// ListJS returns the entry as the file browser's list is sent it, a
// JavaScript array: [kind, url, name, icon, size, date], with the date as
// the list shows it (with the time when sorted by date). json.Marshal
// escapes <, > and &, and U+2028 and U+2029, so it is safe in a <script>.
func (d *DirEntry) ListJS(useTimestamp bool) template.JS {
	date := humanizeDate(d.ModTime)
	if useTimestamp {
		date = humanizeTimestamp(d.ModTime)
	}
	b, err := json.Marshal([]any{d.ListKind(), urlEncode(d.Name), d.Name, d.IconClass, d.Size, date})
	if err != nil {
		// Strings and numbers always marshal.
		return "null"
	}
	return template.JS(b)
}

// IsImage reports whether the entry is a photo for the grid. A folder
// (or an archive listed as one) never is, whatever its name: 2024.jpg/
// is a folder.
func (d *DirEntry) IsImage() bool {
	return !d.IsDir && isImage(d.Name)
}

// IsVideo reports whether the entry is a video for the grid; a folder
// never is.
func (d *DirEntry) IsVideo() bool {
	return !d.IsDir && isVideo(d.Name)
}

// IsMedia reports whether the entry goes in the grid rather than the
// list.
func (d *DirEntry) IsMedia() bool {
	return d.IsImage() || d.IsVideo()
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
	Root             string
	RootName         string
	DirEntries       []*DirEntry
	SortBy           string
	UseTimestamp     bool
	HasNonMediaEntry bool
	HasImage         bool
	HasVideo         bool
	// Thumbnails is set when the grid can request resized images, and
	// VideoThumbnails when it can request video frames too.
	Thumbnails      bool
	VideoThumbnails bool
	// Diagnostics is where the page sends its diagnostics events, or ""
	// when they're off (see diagnostics.go).
	Diagnostics           string
	ApplicationVersion    string
	ApplicationBuildstamp string
}

type customIndexHandler struct {
	baseHandler  http.Handler
	baseFS       fs.FS
	enhancedList bool
	thumbs       *thumbnailer
	metrics      *listingMetrics
	tmpl         *template.Template
	previews     folderPreviews
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
// "/test#dir/", which 404s. Escaping the name before redirecting keeps '#'
// (and other reserved characters) part of the path instead of being read as
// a fragment or query string.
//
// The redirect is relative because this handler runs behind
// http.StripPrefix: urlPath lacks the serve path prefix (e.g. "/e"), so an
// absolute Location built from it would leave the mounted filesystem.
func (c *customIndexHandler) redirectDirectoryToTrailingSlash(w http.ResponseWriter, r *http.Request, fsPath, urlPath string) bool {
	if strings.HasSuffix(urlPath, "/") || fsPath == "." {
		return false
	}

	// fs.Stat uses the filesystem's StatFS implementation when available
	// (ufs.FS implements it), so this avoids opening a file handle just to
	// check IsDir.
	info, err := fs.Stat(c.baseFS, fsPath)
	if err != nil || !info.IsDir() {
		return false
	}

	target := relativeURL(path.Base(urlPath)) + "/"
	if q := r.URL.RawQuery; q != "" {
		target += "?" + q
	}
	redirectRelative(w, target, http.StatusMovedPermanently)
	return true
}

// The stages of a directory listing, the "stage" of
// listing_stage_duration_seconds and the names of the spans under a
// request's "listing" span (as listing.open and so on).
const (
	listingStageOpen   = "open"
	listingStageRead   = "read" // the directory's entries
	listingStageStat   = "stat" // each entry's size and time
	listingStageSort   = "sort"
	listingStageRender = "render" // the page, written to the client
)

// listingEntries are the histogram buckets for how many entries the
// listed directories hold, which is what every stage's time follows.
var listingEntries = []float64{10, 50, 100, 500, 1000, 5000, 10000, 50000, 100000}

// listingMetrics says where the time listing directories goes.
type listingMetrics struct {
	*stageMetrics
	// Entries in each directory listed.
	entries metric.Int64Histogram
}

func newListingMetrics(mc *monitoringContext) (*listingMetrics, error) {
	stages, m, err := newStageMetrics(mc, "listing",
		"How long each stage of listing a directory took: open, read (its entries), stat (each entry), sort, render (the page).")
	if err != nil {
		return nil, err
	}
	entries, err := m.Int64Histogram("listing_entries",
		metric.WithDescription("Entries in the directories listed."),
		metric.WithExplicitBucketBoundaries(listingEntries...))
	if err != nil {
		return nil, err
	}
	return &listingMetrics{stageMetrics: stages, entries: entries}, nil
}

func (c *customIndexHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if c.enhancedList && c.serveListing(w, r) {
		return
	}
	c.baseHandler.ServeHTTP(w, r)
}

// serveListing answers a request for a directory with the file browser's
// page, or with the redirect to the directory's own URL. It reports
// whether it answered; other requests are the base handler's.
func (c *customIndexHandler) serveListing(w http.ResponseWriter, r *http.Request) bool {
	urlPath := r.URL.Path
	path := cleanPath(strings.TrimPrefix(urlPath, "/"))

	slog.Info("customIndexHandler", "url", r.URL, "path", path)

	if c.redirectDirectoryToTrailingSlash(w, r, path, urlPath) {
		return true
	}
	if !strings.HasSuffix(urlPath, "/") && path != "." {
		return false
	}
	if r.URL.Query().Has("preview") {
		setRequestHandler(r, handlerListing)
		c.serveFolderPreview(w, r, path)
		return true
	}
	if r.URL.Query().Has("folders") {
		setRequestHandler(r, handlerListing)
		c.serveSubfolders(w, r, path)
		return true
	}

	// One span per stage, never per entry: a folder can hold tens of
	// thousands.
	ctx, span := c.metrics.tracer.Start(r.Context(), "listing", trace.WithAttributes(attribute.String("path", path)))
	defer span.End()
	fail := func(err error) bool {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return true
	}

	_, _, opened := c.metrics.stage(ctx, listingStageOpen)
	f, err := c.baseFS.Open(path)
	opened()
	if err != nil {
		return fail(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Error("failed to close file after serving custom index", "path", path, "error", err)
		}
	}()
	rdf, ok := f.(fs.ReadDirFile)
	span.SetAttributes(attribute.Bool("custom_directory_list", ok))
	if !ok {
		return false
	}
	setRequestHandler(r, handlerListing)

	now := time.Now()
	_, _, read := c.metrics.stage(ctx, listingStageRead)
	entries, err := rdf.ReadDir(-1)
	read()
	span.SetAttributes(attribute.Int("num_entries", len(entries)))
	if err != nil {
		return fail(err)
	}
	c.metrics.entries.Record(context.WithoutCancel(ctx), int64(len(entries)))

	sortBy := canonicalizeSortBy(r.URL.Query().Get("sort"))
	params := &customIndexReport{
		Root:                  path,
		RootName:              strings.TrimSuffix(filepath.Base(path), nestedDirSuffix),
		DirEntries:            []*DirEntry{},
		SortBy:                sortBy,
		UseTimestamp:          strings.Contains(sortBy, "date"),
		Thumbnails:            c.thumbs != nil,
		VideoThumbnails:       c.thumbs.videos(),
		Diagnostics:           diagPageEndpoint(r.RequestURI),
		ApplicationVersion:    internal.Version(),
		ApplicationBuildstamp: internal.Buildstamp(),
	}

	allFiles := map[string]any{}
	archiveDirs := map[string]fs.DirEntry{}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		// Only files are videos or their sidecars, as findSidecars has it.
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
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

	sidecars, badges := listingSidecars(names)

	_, statSpan, statted := c.metrics.stage(ctx, listingStageStat)
	listed := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), nestedDirSuffix) {
			if _, ok := actualArchiveDir[strings.TrimSuffix(entry.Name(), nestedDirSuffix)]; ok {
				continue
			}
		}
		listed = append(listed, entry)
	}
	infos, err := statEntries(ctx, listed)
	if err != nil {
		// The client has gone.
		return fail(err)
	}
	statErrors := 0
	files := newEntryList(sortBy)
	for i, entry := range listed {
		size := int64(0)
		t := now
		if stat := infos[i]; stat != nil {
			t = stat.ModTime()
			size = stat.Size()
		} else {
			statErrors++
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
			Sidecar:    !isDir && sidecars[entry.Name()],
		}
		if !isDir && isMetaVideo(entry.Name()) {
			newEntry.Badges = badges[strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))]
		}
		files.add(newEntry)
	}
	statSpan.SetAttributes(attribute.Int("num_files", files.Len()), attribute.Int("stat_errors", statErrors))
	statted()

	_, sortSpan, sorted := c.metrics.stage(ctx, listingStageSort)
	sortSpan.SetAttributes(attribute.Int("num_files", files.Len()), attribute.String("sort", sortBy))
	sort.Sort(files)
	sorted()

	_, renderSpan, rendered := c.metrics.stage(ctx, listingStageRender)
	renderSpan.SetAttributes(attribute.Int("num_files", files.Len()))
	defer rendered()
	hasNonMediaEntry := false
	hasImage := false
	hasVideo := false
	for _, name := range files.EntryOrder {
		entry := files.Entries[name]
		params.DirEntries = append(params.DirEntries, entry)
		if !entry.IsMedia() && !entry.Sidecar {
			hasNonMediaEntry = true
		}
		if entry.IsImage() {
			hasImage = true
		}
		if entry.IsVideo() {
			hasVideo = true
		}
	}
	params.HasNonMediaEntry = hasNonMediaEntry
	params.HasImage = hasImage
	params.HasVideo = hasVideo

	if err := c.tmpl.Execute(w, params); err != nil {
		return fail(err)
	}
	return true
}

// statParallelMin is the fewest entries worth statting on more than one
// goroutine, statParallelism the most goroutines that stat a folder, and
// statChunk how many entries a goroutine takes at a time. On a local disk
// each Info is a system call (lstat), one at a time, and a folder of tens
// of thousands spends much of its listing there.
const (
	statParallelMin = 256
	statParallelism = 8
	statChunk       = 64
)

// statHelpers are the goroutines that help stat big folders, shared by
// every listing at once: however many folders are listed together, no
// more than statParallelism of them are at work beside the listings' own.
var statHelpers = make(chan struct{}, statParallelism)

// statEntries returns each entry's Info, or nil where it failed, in
// entries' order. A big folder's entries are statted by its listing and
// whichever helpers are free (statHelpers). It stops when ctx is done,
// and returns ctx's error then.
func statEntries(ctx context.Context, entries []fs.DirEntry) ([]fs.FileInfo, error) {
	infos := make([]fs.FileInfo, len(entries))
	var next atomic.Int64
	work := func() {
		for ctx.Err() == nil {
			from := int(next.Add(statChunk)) - statChunk
			if from >= len(entries) {
				return
			}
			for i := from; i < min(from+statChunk, len(entries)); i++ {
				if info, err := entries[i].Info(); err == nil {
					infos[i] = info
				}
			}
		}
	}
	var wg sync.WaitGroup
	if len(entries) >= statParallelMin {
	helpers:
		for range min(statParallelism, runtime.GOMAXPROCS(0)) - 1 {
			select {
			case statHelpers <- struct{}{}:
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer func() { <-statHelpers }()
					work()
				}()
			default:
				// Every helper is busy with another folder.
				break helpers
			}
		}
	}
	work()
	wg.Wait()
	return infos, ctx.Err()
}

func newCustomIndex(baseHandler http.Handler, baseFS fs.FS, mc *monitoringContext, enhancedList bool, thumbs *thumbnailer) (http.Handler, error) {
	tmpl, err := createTemplate(customIndexHTML)
	if err != nil {
		return nil, err
	}
	metrics, err := newListingMetrics(mc)
	if err != nil {
		return nil, err
	}
	return &customIndexHandler{
		baseHandler:  baseHandler,
		baseFS:       baseFS,
		enhancedList: enhancedList,
		thumbs:       thumbs,
		metrics:      metrics,
		tmpl:         tmpl,
	}, nil
}
