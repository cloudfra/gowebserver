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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"
)

// folders is a subfolderList's folders: names ending in .zip are archives.
func folders(names ...string) []subfolder {
	out := make([]subfolder, 0, len(names))
	for _, n := range names {
		out = append(out, subfolder{Name: n, Archive: filepath.Ext(n) == ".zip"})
	}
	return out
}

func TestReadSubfolders(t *testing.T) {
	file := &fstest.MapFile{Data: []byte("x")}
	dir := &fstest.MapFile{Mode: fs.ModeDir}
	old := &fstest.MapFile{Mode: fs.ModeDir, ModTime: time.Unix(100, 0)}
	recent := &fstest.MapFile{Mode: fs.ModeDir, ModTime: time.Unix(200, 0)}
	for name, tc := range map[string]struct {
		fsys fstest.MapFS
		sort string
		want []subfolder
	}{
		"folders only, in natural order": {
			fsys: fstest.MapFS{"f/img10": dir, "f/img2": dir, "f/notes.txt": file, "f/Beta": dir, "f/alpha/x.jpg": file},
			sort: "name",
			want: folders("alpha", "Beta", "img2", "img10"),
		},
		"reversed": {
			fsys: fstest.MapFS{"f/a": dir, "f/b": dir},
			sort: "name-desc",
			want: folders("b", "a"),
		},
		"by date": {
			fsys: fstest.MapFS{"f/a": recent, "f/b": old},
			sort: "date",
			want: folders("b", "a"),
		},
		"an archive the listing opens takes its folder's place": {
			fsys: fstest.MapFS{"f/pics.zip": file, "f/pics.zip.d/a.jpg": file, "f/other.zip": file, "f/z": dir},
			sort: "name",
			want: folders("pics.zip", "z"),
		},
		"a mount folder without its archive is a folder": {
			fsys: fstest.MapFS{"f/lone.d": dir},
			sort: "name",
			want: []subfolder{{Name: "lone.d"}},
		},
		"hidden folders are listed, as the listing lists them": {
			fsys: fstest.MapFS{"f/.git": dir, "f/src": dir},
			sort: "name",
			want: folders(".git", "src"),
		},
		"none": {
			fsys: fstest.MapFS{"f/a.txt": file},
			sort: "name",
			want: folders(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := readSubfolders(tc.fsys, "f", tc.sort)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("folders (-want +got):\n%s", diff)
			}
		})
	}
}

func subfoldersGet(t *testing.T, h http.Handler, target string) subfolderList {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%s: status %d, type %q, body %q", target, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("%s: Cache-Control %q", target, cc)
	}
	var l subfolderList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestSubfoldersEndpoint(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"Trips/2023", "Trips/2024", "Trips/2024/Rome"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "Trips", "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, closeFS, err := newHandlerFromFS(root, &monitoringContext{}, pageOptions{enhancedList: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := closeFS(); err != nil {
			t.Error(err)
		}
	})

	if got := subfoldersGet(t, h, "/Trips/?folders"); !cmp.Equal(got.Folders, folders("2023", "2024")) {
		t.Errorf("Trips: %+v", got)
	}
	if got := subfoldersGet(t, h, "/Trips/?folders&sort=name-desc"); !cmp.Equal(got.Folders, folders("2024", "2023")) {
		t.Errorf("Trips, reversed: %+v", got)
	}
	if got := subfoldersGet(t, h, "/?folders"); !cmp.Equal(got.Folders, folders("Trips")) {
		t.Errorf("top: %+v", got)
	}
	// Asked again, a folder made since is in it.
	if err := os.Mkdir(filepath.Join(root, "Trips", "2025"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := subfoldersGet(t, h, "/Trips/?folders"); !cmp.Equal(got.Folders, folders("2023", "2024", "2025")) {
		t.Errorf("after 2025 was made: %+v", got)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/Missing/?folders", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("a missing folder: status %d", rec.Code)
	}
}

// Without the enhanced listing there's no breadcrumb to ask, and the
// folder's own page answers as before.
func TestSubfoldersEndpointNeedsEnhancedListing(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	h, closeFS, err := newHandlerFromFS(root, &monitoringContext{}, pageOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := closeFS(); err != nil {
			t.Error(err)
		}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?folders", nil))
	if ct := rec.Header().Get("Content-Type"); ct == "application/json" {
		t.Errorf("type %q", ct)
	}
}

// The top of a server with several serve paths lists them, in the order
// its page does: as they were configured.
func TestIndexSubfolders(t *testing.T) {
	h, err := newIndexHTTPHandler([]string{"/photos", "/Archive/", "/a/b", "/docs"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := subfoldersGet(t, h, "/?folders"); !cmp.Equal(got.Folders, folders("photos", "Archive", "a/b", "docs")) {
		t.Errorf("top: %+v", got)
	}
	// Without ?folders it's the page, as before.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if ct := rec.Header().Get("Content-Type"); ct != "text/html" {
		t.Errorf("page: type %q", ct)
	}
}

// statlessFS is a filesystem whose entries named in fail can't be statted.
type statlessFS struct {
	fstest.MapFS
	fail map[string]bool
}

type statlessEntry struct{ fs.DirEntry }

func (statlessEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrPermission }

func (f statlessFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := f.MapFS.ReadDir(name)
	for i, e := range entries {
		if f.fail[e.Name()] {
			entries[i] = statlessEntry{e}
		}
	}
	return entries, err
}

// A folder that can't be statted sorts by date as the listing sorts it,
// as new, not as the oldest of all.
func TestReadSubfoldersUnstatted(t *testing.T) {
	recent := &fstest.MapFile{Mode: fs.ModeDir, ModTime: time.Now().Add(-time.Hour)}
	fsys := statlessFS{fstest.MapFS{"f/recent": recent, "f/unknown": recent}, map[string]bool{"unknown": true}}
	for sortBy, want := range map[string][]subfolder{
		"date":      folders("recent", "unknown"),
		"date-desc": folders("unknown", "recent"),
	} {
		got, err := readSubfolders(fsys, "f", sortBy)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("%s (-want +got):\n%s", sortBy, diff)
		}
	}
}
