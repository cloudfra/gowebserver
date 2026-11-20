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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gowsTesting "github.com/cloudfra/gowebserver/internal/gowebserver/testing"
	"github.com/cloudfra/ufs"
	"github.com/google/go-cmp/cmp"
)

func TestDirEntryString(t *testing.T) {
	entry := DirEntry{
		Name:      "abc.txt",
		Size:      500,
		ModTime:   time.Now(),
		IsDir:     false,
		IsArchive: true,
		IconClass: "archive",
	}
	got := entry.String()
	want := `abc.txt: dir= false, archive= true iconClass= "archive"`
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("nestedFS.Mode() mismatch (-want +got):\n%s", diff)
	}
}

func TestDirEntryListJS(t *testing.T) {
	mod := time.Date(2024, 3, 5, 14, 7, 9, 0, time.Local)
	testCases := []struct {
		entry     DirEntry
		timestamp bool
		want      string
	}{
		{
			DirEntry{Name: "notes.txt", Size: 12, ModTime: mod, IsViewable: true, IconClass: "text"},
			false,
			`[1,"notes.txt","notes.txt","text",12,"2024-03-05"]`,
		},
		{
			DirEntry{Name: "tool.exe", Size: 5, ModTime: mod, IconClass: "terminal"},
			true,
			`[0,"tool.exe","tool.exe","terminal",5,"2024-03-05 02:07:09PM"]`,
		},
		{
			DirEntry{Name: "My Docs", ModTime: mod, IsDir: true, IconClass: "folder"},
			false,
			`[2,"My%20Docs","My Docs","folder",0,"2024-03-05"]`,
		},
		{
			DirEntry{Name: "a.zip", Size: 9, ModTime: mod, IsDir: true, IsArchive: true, IconClass: "folder"},
			false,
			`[3,"a.zip","a.zip","folder",9,"2024-03-05"]`,
		},
		// Nothing in a name can end the script it's in. (\x5c is a
		// backslash, \xe2\x80\xa8 the line separator U+2028.)
		{
			DirEntry{Name: "</script><b>&\xe2\x80\xa8\"x\"", ModTime: mod, IconClass: "unknown"},
			false,
			"[0,\"%3C%2Fscript%3E%3Cb%3E\x5cu0026%E2%80%A8%22x%22\"," +
				"\"\x5cu003c/script\x5cu003e\x5cu003cb\x5cu003e\x5cu0026\x5cu2028\x5c\"x\x5c\"\",\"unknown\",0,\"2024-03-05\"]",
		},
	}
	for _, tc := range testCases {
		if diff := cmp.Diff(tc.want, string(tc.entry.ListJS(tc.timestamp))); diff != "" {
			t.Errorf("ListJS(%q) mismatch (-want +got):\n%s", tc.entry.Name, diff)
		}
	}
}

// statEntry is a DirEntry whose Info says its place, or fails.
type statEntry struct {
	fs.DirEntry
	i    int
	fail bool
}

func (e statEntry) Info() (fs.FileInfo, error) {
	if e.fail {
		return nil, errors.New("gone")
	}
	return statInfo(e.i), nil
}

type statInfo int

func (i statInfo) Name() string       { return fmt.Sprint(int(i)) }
func (i statInfo) Size() int64        { return int64(i) }
func (i statInfo) Mode() fs.FileMode  { return 0 }
func (i statInfo) ModTime() time.Time { return time.Time{} }
func (i statInfo) IsDir() bool        { return false }
func (i statInfo) Sys() any           { return nil }

func TestStatEntries(t *testing.T) {
	for _, n := range []int{0, 1, statParallelMin - 1, statParallelMin, 10007} {
		entries := make([]fs.DirEntry, n)
		for i := range entries {
			entries[i] = statEntry{i: i, fail: i%97 == 3}
		}
		infos, err := statEntries(t.Context(), entries)
		if err != nil {
			t.Fatal(err)
		}
		if len(infos) != n {
			t.Fatalf("statEntries of %d gave %d", n, len(infos))
		}
		for i, info := range infos {
			if i%97 == 3 {
				if info != nil {
					t.Errorf("n=%d: entry %d failed but has info %v", n, i, info)
				}
			} else if info == nil || info.Size() != int64(i) {
				t.Errorf("n=%d: entry %d has info %v", n, i, info)
			}
		}
	}
}

// slowEntry is a DirEntry whose Info takes a while, counting how many
// are being statted at once, and calls stat when it starts.
type slowEntry struct {
	fs.DirEntry
	now, most *atomic.Int64
	stat      func()
}

func (e slowEntry) Info() (fs.FileInfo, error) {
	n := e.now.Add(1)
	defer e.now.Add(-1)
	for {
		m := e.most.Load()
		if n <= m || e.most.CompareAndSwap(m, n) {
			break
		}
	}
	if e.stat != nil {
		e.stat()
	}
	time.Sleep(50 * time.Microsecond)
	return statInfo(0), nil
}

func TestStatEntriesSharesItsHelpers(t *testing.T) {
	// Many big folders listed at once: each listing stats its own, and
	// the helpers they share are statParallelism in all.
	const listings = 6
	var now, most atomic.Int64
	entries := make([]fs.DirEntry, 4*statParallelMin)
	for i := range entries {
		entries[i] = slowEntry{now: &now, most: &most}
	}
	var wg sync.WaitGroup
	for range listings {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := statEntries(t.Context(), entries); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got, limit := most.Load(), int64(listings+statParallelism); got > limit {
		t.Errorf("%d entries statted at once, want at most %d", got, limit)
	}
	if len(statHelpers) != 0 {
		t.Errorf("%d helpers still held", len(statHelpers))
	}
}

func TestStatEntriesStopsWhenTheRequestIs(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var now, most, statted atomic.Int64
	entries := make([]fs.DirEntry, 100*statChunk)
	for i := range entries {
		entries[i] = slowEntry{now: &now, most: &most, stat: func() {
			if statted.Add(1) == statChunk {
				cancel()
			}
		}}
	}
	_, err := statEntries(ctx, entries)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("statEntries() error = %v, want context.Canceled", err)
	}
	if got := statted.Load(); got > int64(statChunk*(statParallelism+1)) {
		t.Errorf("%d of %d entries statted after the request was canceled", got, len(entries))
	}
}

func TestCustomIndexFolderWithAMediaName(t *testing.T) {
	// A folder named like a photo or a video is a folder: it's in the
	// list, not the grid.
	dir := t.TempDir()
	for _, d := range []string{"2024.jpg", "Clips.mp4"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	nFS, err := ufs.New(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer gowsTesting.DeferClose(t, nFS)()
	ci, err := newCustomIndex(http.FileServer(http.FS(nFS)), nFS, &monitoringContext{}, pageOptions{enhancedList: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	ci.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	_, page, found := strings.Cut(body, "files: [")
	files, media, ok := strings.Cut(page, "media: [")
	if !found || !ok {
		t.Fatalf("no files and media arrays in the page:\n%s", body)
	}
	media, _, _ = strings.Cut(media, "]\n")
	for _, d := range []string{"2024.jpg", "Clips.mp4"} {
		if want := fmt.Sprintf(`[2,%q,%q,"folder",`, d, d); !strings.Contains(files, want) {
			t.Errorf("the list has no folder %s (%s)", want, files)
		}
		if strings.Contains(media, d) {
			t.Errorf("the folder %s is in the grid: %s", d, media)
		}
	}
	if !strings.Contains(media, `"a.png"`) {
		t.Errorf("the photo isn't in the grid: %s", media)
	}
	if !strings.Contains(body, `id="files"`) {
		t.Error("no list for the folders")
	}
}

func TestDirEntryIsMedia(t *testing.T) {
	for _, tc := range []struct {
		entry               DirEntry
		image, video, media bool
	}{
		{DirEntry{Name: "a.jpg"}, true, false, true},
		{DirEntry{Name: "a.mp4"}, false, true, true},
		{DirEntry{Name: "a.txt"}, false, false, false},
		{DirEntry{Name: "2024.jpg", IsDir: true}, false, false, false},
		{DirEntry{Name: "Clips.mp4", IsDir: true}, false, false, false},
		{DirEntry{Name: "pics.zip", IsDir: true, IsArchive: true}, false, false, false},
	} {
		e := tc.entry
		if e.IsImage() != tc.image || e.IsVideo() != tc.video || e.IsMedia() != tc.media {
			t.Errorf("%s (dir %t): image %t video %t media %t, want %t %t %t", e.Name, e.IsDir,
				e.IsImage(), e.IsVideo(), e.IsMedia(), tc.image, tc.video, tc.media)
		}
	}
}

func TestCustomIndex(t *testing.T) {
	nestedZipPath := gowsTesting.MustNestedZipFilePath(t)

	nFS, err := ufs.New(t.Context(), nestedZipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer gowsTesting.DeferClose(t, nFS)()

	mc := &monitoringContext{}
	ci, err := newCustomIndex(http.FileServer(http.FS(nFS)), nFS, mc, pageOptions{enhancedList: true}, nil)
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewUnstartedServer(ci)
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	// The list's entries, as the page is sent them: [kind, url, name, ...],
	// where kind 2 is a folder and 3 an archive opened as one.
	verifyCustomIndex(t, ts.Client(), ts.URL, []string{`[3,"single-testassets.zip","single-testassets.zip",`, `[2,"testassets","testassets",`, `[3,"testassets.zip","testassets.zip",`})
	verifyCustomIndex(t, ts.Client(), ts.URL+"/testassets", []string{"index.html", "site.js", `[2,"assets","assets",`})
	verifyCustomIndex(t, ts.Client(), ts.URL+"/testassets/", []string{"index.html", "site.js", `[2,"assets","assets",`})
	verifyCustomIndex(t, ts.Client(), ts.URL+"/testassets.zip.d/", []string{"index.html", "site.js", `[2,"assets","assets",`})
	verifyCustomIndex(t, ts.Client(), ts.URL+"/testassets/assets/images", []string{"ocean.jpg", "nature.jpg"})
	verifyCustomIndex(t, ts.Client(), ts.URL+"/testassets/assets/images/", []string{"ocean.jpg", "nature.jpg"})
}

func verifyCustomIndex(tb testing.TB, hc *http.Client, u string, substrs []string) {
	ctx := tb.Context()
	if len(substrs) == 0 {
		tb.Error("must have at least 1 substring to check")
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		tb.Fatalf("cannot create request for %q, %s", u, err)
	}
	res, err := hc.Do(req)
	if err != nil {
		tb.Errorf("cannot GET %s, %s", u, err)
		return
	}
	defer gowsTesting.DeferClose(tb, res.Body)()

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		tb.Errorf("cannot read response from %s, %s", u, err)
		return
	}
	body := gowsTesting.RemoveLineFeed(string(bodyBytes))
	missingSubstr := true
	for idx, substr := range substrs {
		if !strings.Contains(body, substr) {
			tb.Errorf("%s does not contain string[%d] %q", u, idx, substr)
			missingSubstr = true
		}
	}
	if missingSubstr {
		tb.Logf("--- GET: %s\n\n%s", u, body)
	}
}
