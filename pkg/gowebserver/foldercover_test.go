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
	"fmt"
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

func TestFindFolderCover(t *testing.T) {
	file := &fstest.MapFile{Data: []byte("x")}
	dir := &fstest.MapFile{Mode: fs.ModeDir}
	for name, tc := range map[string]struct {
		fsys fstest.MapFS
		want folderCover
	}{
		"the first photo, in natural order": {
			fsys: fstest.MapFS{"f/img10.jpg": file, "f/img2.jpg": file, "f/clip.mp4": file, "f/notes.txt": file, "f/sub/a.jpg": file},
			want: folderCover{File: "img2.jpg", Photos: 2, Videos: 1, Folders: 1},
		},
		"a video when there are no photos": {
			fsys: fstest.MapFS{"f/b.mp4": file, "f/a.mov": file},
			want: folderCover{File: "a.mov", Video: true, Videos: 2},
		},
		"from below when it has none of its own, nearest first": {
			fsys: fstest.MapFS{"f/a/deep/x.jpg": file, "f/b/y.jpg": file, "f/readme.txt": file},
			want: folderCover{File: "b/y.jpg", Folders: 2},
		},
		"hidden files and archive mounts aren't covers": {
			fsys: fstest.MapFS{"f/.thumb.jpg": file, "f/pics.zip": file, "f/pics.zip.d/a.jpg": file, "f/z/c.jpg": file},
			want: folderCover{File: "z/c.jpg", Folders: 1},
		},
		"no deeper than folderCoverDepth": {
			fsys: fstest.MapFS{"f/1/2/3/4/x.jpg": file},
			want: folderCover{Folders: 1},
		},
		"empty": {
			fsys: fstest.MapFS{"f": dir},
			want: folderCover{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := findFolderCover(tc.fsys, "f")
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("cover (-want +got):\n%s", diff)
			}
		})
	}
}

// Thousands of empty folders cost at most folderCoverBudget entries read.
func TestFindFolderCoverBudget(t *testing.T) {
	fsys := fstest.MapFS{}
	for i := 0; i < 3*folderCoverBudget; i++ {
		fsys[fmt.Sprintf("f/%05d/empty.txt", i)] = &fstest.MapFile{Data: []byte("x")}
	}
	fsys["f/99999/last.jpg"] = &fstest.MapFile{Data: []byte("x")}
	got, err := findFolderCover(fsys, "f")
	if err != nil {
		t.Fatal(err)
	}
	if got.File != "" {
		t.Errorf("found %q past the budget", got.File)
	}
}

func coverGet(t *testing.T, h http.Handler, target string) folderCover {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%s: status %d, type %q, body %q", target, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	var c folderCover
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFolderCoverEndpoint(t *testing.T) {
	root := t.TempDir()
	write := func(name string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("Trip/b.jpg")
	write("Trip/clip.mp4")
	h, closeFS, err := newHandlerFromFS(root, &monitoringContext{}, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := closeFS(); err != nil {
			t.Error(err)
		}
	})

	if got := coverGet(t, h, "/Trip/?cover"); got.File != "b.jpg" || got.Photos != 1 || got.Videos != 1 {
		t.Errorf("cover %+v", got)
	}
	// A new first photo changes the folder, so its cover is looked for
	// again rather than kept.
	write("Trip/a.jpg")
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(root, "Trip"), later, later); err != nil {
		t.Fatal(err)
	}
	if got := coverGet(t, h, "/Trip/?cover"); got.File != "a.jpg" || got.Photos != 2 {
		t.Errorf("after a.jpg was added: %+v", got)
	}
	// Without ?cover it's the folder's page, as before.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/Trip/", nil))
	if ct := rec.Header().Get("Content-Type"); rec.Code != http.StatusOK || ct == "application/json" {
		t.Errorf("folder page: status %d, type %q", rec.Code, ct)
	}
}
