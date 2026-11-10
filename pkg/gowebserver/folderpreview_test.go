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

// items is a preview's items: names ending in .mp4 are videos.
func items(files ...string) []previewItem {
	out := make([]previewItem, 0, len(files))
	for _, f := range files {
		out = append(out, previewItem{File: f, Video: filepath.Ext(f) == ".mp4"})
	}
	return out
}

func TestFindFolderPreview(t *testing.T) {
	file := &fstest.MapFile{Data: []byte("x")}
	dir := &fstest.MapFile{Mode: fs.ModeDir}
	for name, tc := range map[string]struct {
		fsys fstest.MapFS
		want folderPreview
	}{
		"its photos and videos, in natural order": {
			fsys: fstest.MapFS{"f/img10.jpg": file, "f/img2.jpg": file, "f/clip.mp4": file, "f/notes.txt": file, "f/sub/a.jpg": file},
			want: folderPreview{Items: items("clip.mp4", "img2.jpg", "img10.jpg")},
		},

		"from below when it has none of its own, nearest first": {
			fsys: fstest.MapFS{"f/a/deep/x.jpg": file, "f/b/y.jpg": file, "f/b/z.mp4": file, "f/readme.txt": file},
			want: folderPreview{Items: items("b/y.jpg", "b/z.mp4", "a/deep/x.jpg")},
		},
		"hidden files and archive mounts aren't shown": {
			fsys: fstest.MapFS{"f/.thumb.jpg": file, "f/pics.zip": file, "f/pics.zip.d/a.jpg": file, "f/z/c.jpg": file},
			want: folderPreview{Items: items("z/c.jpg")},
		},
		"no deeper than folderPreviewDepth": {
			fsys: fstest.MapFS{"f/1/2/3/4/x.jpg": file},
			want: folderPreview{Items: items()},
		},
		"empty": {
			fsys: fstest.MapFS{"f": dir},
			want: folderPreview{Items: items()},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := findFolderPreview(tc.fsys, "f")
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("preview (-want +got):\n%s", diff)
			}
		})
	}
}

// A preview has at most folderPreviewItems, the first in name order.
func TestFindFolderPreviewItems(t *testing.T) {
	fsys := fstest.MapFS{}
	var want []string
	for i := 1; i <= folderPreviewItems+5; i++ {
		name := fmt.Sprintf("%d.jpg", i)
		fsys["f/"+name] = &fstest.MapFile{Data: []byte("x")}
		if i <= folderPreviewItems {
			want = append(want, name)
		}
	}
	got, err := findFolderPreview(fsys, "f")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(items(want...), got.Items); diff != "" {
		t.Errorf("items (-want +got):\n%s", diff)
	}
}

// Thousands of empty folders cost at most folderPreviewBudget entries read.
func TestFindFolderPreviewBudget(t *testing.T) {
	fsys := fstest.MapFS{}
	for i := 0; i < 3*folderPreviewBudget; i++ {
		fsys[fmt.Sprintf("f/%05d/empty.txt", i)] = &fstest.MapFile{Data: []byte("x")}
	}
	fsys["f/99999/last.jpg"] = &fstest.MapFile{Data: []byte("x")}
	got, err := findFolderPreview(fsys, "f")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 0 {
		t.Errorf("found %v past the budget", got.Items)
	}
}

func previewGet(t *testing.T, h http.Handler, target string) folderPreview {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%s: status %d, type %q, body %q", target, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	var c folderPreview
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFolderPreviewEndpoint(t *testing.T) {
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

	if got := previewGet(t, h, "/Trip/?preview"); !cmp.Equal(got.Items, items("b.jpg", "clip.mp4")) {
		t.Errorf("preview %+v", got)
	}
	// A new first photo changes the folder, so its preview is looked for
	// again rather than kept.
	write("Trip/a.jpg")
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(root, "Trip"), later, later); err != nil {
		t.Fatal(err)
	}
	if got := previewGet(t, h, "/Trip/?preview"); !cmp.Equal(got.Items, items("a.jpg", "b.jpg", "clip.mp4")) {
		t.Errorf("after a.jpg was added: %+v", got)
	}
	// Without ?preview it's the folder's page, as before.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/Trip/", nil))
	if ct := rec.Header().Get("Content-Type"); rec.Code != http.StatusOK || ct == "application/json" {
		t.Errorf("folder page: status %d, type %q", rec.Code, ct)
	}
}
