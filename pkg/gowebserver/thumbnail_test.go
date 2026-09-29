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
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
)

var (
	thumbGreen = color.NRGBA{G: 255, A: 255}
	thumbRed   = color.NRGBA{R: 255, A: 255}
	thumbBlue  = color.NRGBA{B: 255, A: 255}
)

// stripes is a w x h image: the left quarter green, the right quarter
// blue, red in between. Cropping, padding and stretching each leave a
// different color at the edges.
func stripes(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := thumbRed
			if x < w/4 {
				c = thumbGreen
			} else if x >= w*3/4 {
				c = thumbBlue
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withOrientation inserts an EXIF APP1 segment carrying orientation o
// right after the JPEG's start-of-image marker.
func withOrientation(jpg []byte, o byte) []byte {
	tiff := []byte{
		'M', 'M', 0, 42, 0, 0, 0, 8, // big-endian header, IFD at 8
		0, 1, // one entry
		0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, o, 0, 0, // Orientation, SHORT, 1, value
		0, 0, 0, 0, // no next IFD
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := make([]byte, 0, 4+len(payload))
	seg = append(seg, 0xFF, 0xE1, byte((len(payload)+2)>>8), byte(len(payload)+2))
	seg = append(seg, payload...)
	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

// countingFS counts Opens, to tell cached thumbnails from rendered ones.
type countingFS struct {
	fstest.MapFS
	opens atomic.Int32
}

func (c *countingFS) Open(name string) (fs.File, error) {
	c.opens.Add(1)
	return c.MapFS.Open(name)
}

func (c *countingFS) Stat(name string) (fs.FileInfo, error) {
	return c.MapFS.Stat(name)
}

func newTestThumbnailHandler(t *testing.T, fsys fs.FS, ffmpegPath string) (http.Handler, *atomic.Int32) {
	t.Helper()
	var baseCalls atomic.Int32
	base := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		baseCalls.Add(1)
		if _, err := w.Write([]byte("original")); err != nil {
			t.Errorf("write: %v", err)
		}
	})
	mc := &monitoringContext{}
	var ff *ffmpeg.FFmpeg // none, whatever the test machine has installed
	if ffmpegPath != "" {
		var err error
		if ff, err = ffmpeg.Find(ffmpegPath); err != nil {
			t.Fatal(err)
		}
	}
	th, err := newThumbnailer(Thumbnails{Enabled: true, CachePath: filepath.Join(t.TempDir(), "thumbnails.db")}, ff, mc.getTraceProvider())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := th.close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return th.handler(base, fsys, "test:"), &baseCalls
}

func thumbGet(t *testing.T, h http.Handler, target string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeThumb(t *testing.T, rec *httptest.ResponseRecorder) image.Image {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	img, _, err := image.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	return img
}

// near reports whether c is within tolerance of want, allowing for JPEG.
func near(c color.Color, want color.NRGBA) bool {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	d := func(a, b uint8) int {
		if a > b {
			return int(a - b)
		}
		return int(b - a)
	}
	return d(n.R, want.R) < 60 && d(n.G, want.G) < 60 && d(n.B, want.B) < 60 && d(n.A, want.A) < 60
}

func TestThumbnailSizes(t *testing.T) {
	fsys := fstest.MapFS{"photo.jpg": {Data: encodeJPEG(t, stripes(400, 200))}}
	h, _ := newTestThumbnailHandler(t, fsys, "")

	tests := []struct {
		query        string
		wantW, wantH int
		// left is the expected color near the left edge, mid-height.
		left color.NRGBA
	}{
		{query: "width=100", wantW: 100, wantH: 50, left: thumbGreen},
		{query: "height=50", wantW: 100, wantH: 50, left: thumbGreen},
		{query: "width=800", wantW: 400, wantH: 200, left: thumbGreen}, // never enlarged
		// cover: scaled to 200x100 and cropped to the middle, which is red.
		{query: "width=100&height=100", wantW: 100, wantH: 100, left: thumbRed},
		{query: "width=100&height=100&fit=cover", wantW: 100, wantH: 100, left: thumbRed},
		// fill: stretched, so the green quarter is still at the left.
		{query: "width=100&height=100&fit=fill", wantW: 100, wantH: 100, left: thumbGreen},
		// inside: the whole image at its own ratio.
		{query: "width=100&height=100&fit=inside", wantW: 100, wantH: 50, left: thumbGreen},
		{query: "width=1000&height=1000&fit=inside", wantW: 400, wantH: 200, left: thumbGreen},
		// contain: padded top and bottom to the box.
		{query: "width=100&height=100&fit=contain", wantW: 100, wantH: 100, left: thumbGreen},
		// cover and contain fill an exact box, even if larger than the file.
		{query: "width=800&height=800&fit=cover", wantW: 800, wantH: 800, left: thumbRed},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			rec := thumbGet(t, h, "/photo.jpg?"+tc.query, nil)
			if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
				t.Errorf("Content-Type = %q, want image/jpeg", ct)
			}
			img := decodeThumb(t, rec)
			if b := img.Bounds(); b.Dx() != tc.wantW || b.Dy() != tc.wantH {
				t.Fatalf("size = %dx%d, want %dx%d", b.Dx(), b.Dy(), tc.wantW, tc.wantH)
			}
			if c := img.At(2, tc.wantH/2); !near(c, tc.left) {
				t.Errorf("left edge = %v, want about %v", c, tc.left)
			}
		})
	}
}

func TestThumbnailContainBackground(t *testing.T) {
	fsys := fstest.MapFS{"photo.jpg": {Data: encodeJPEG(t, stripes(400, 200))}}
	h, _ := newTestThumbnailHandler(t, fsys, "")

	tests := []struct {
		query string
		want  color.NRGBA
	}{
		{query: "fit=contain", want: color.NRGBA{A: 255}}, // jpeg: black
		{query: "fit=contain&format=png", want: color.NRGBA{}},
		{query: "fit=contain&format=png&background=ffffff", want: color.NRGBA{R: 255, G: 255, B: 255, A: 255}},
		{query: "fit=contain&background=%230000ff80&format=png", want: color.NRGBA{B: 255, A: 128}},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			img := decodeThumb(t, thumbGet(t, h, "/photo.jpg?width=100&height=100&"+tc.query, nil))
			// The image is 100x50 in the middle; row 5 is padding.
			if c := img.At(50, 5); !near(c, tc.want) {
				t.Errorf("padding = %v, want about %v", color.NRGBAModel.Convert(c), tc.want)
			}
		})
	}
}

func TestThumbnailFormats(t *testing.T) {
	transparent := image.NewNRGBA(image.Rect(0, 0, 40, 40))
	fsys := fstest.MapFS{
		"photo.jpg":   {Data: encodeJPEG(t, stripes(40, 20))},
		"sticker.png": {Data: encodePNG(t, transparent)},
		"opaque.png":  {Data: encodePNG(t, stripes(40, 20))},
	}
	h, _ := newTestThumbnailHandler(t, fsys, "")

	tests := []struct {
		target, want string
	}{
		{target: "/photo.jpg?width=20", want: "image/jpeg"},
		{target: "/photo.jpg?width=20&format=png", want: "image/png"},
		{target: "/photo.jpg?width=20&format=gif", want: "image/gif"},
		{target: "/photo.jpg?width=20&format=JPG", want: "image/jpeg"},
		{target: "/opaque.png?width=20", want: "image/jpeg"},
		{target: "/sticker.png?width=20", want: "image/png"}, // keeps its transparency
	}
	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			rec := thumbGet(t, h, tc.target, nil)
			decodeThumb(t, rec)
			if ct := rec.Header().Get("Content-Type"); ct != tc.want {
				t.Errorf("Content-Type = %q, want %q", ct, tc.want)
			}
		})
	}
}

func TestThumbnailOrientation(t *testing.T) {
	// A 40x20 photo stored sideways, tagged to be turned 90 degrees
	// clockwise for display: the thumbnail is 20 wide and 40 tall, and the
	// green (left) quarter ends up on top.
	jpg := withOrientation(encodeJPEG(t, stripes(40, 20)), 6)
	if got := jpegOrientation(jpg); got != 6 {
		t.Fatalf("jpegOrientation = %d, want 6", got)
	}
	h, _ := newTestThumbnailHandler(t, fstest.MapFS{"phone.jpg": {Data: jpg}}, "")
	img := decodeThumb(t, thumbGet(t, h, "/phone.jpg?width=20", nil))
	if b := img.Bounds(); b.Dx() != 20 || b.Dy() != 40 {
		t.Fatalf("size = %dx%d, want 20x40", b.Dx(), b.Dy())
	}
	if c := img.At(10, 2); !near(c, thumbGreen) {
		t.Errorf("top = %v, want green", c)
	}
	if c := img.At(10, 37); !near(c, thumbBlue) {
		t.Errorf("bottom = %v, want blue", c)
	}
}

func TestThumbnailOrientAllEight(t *testing.T) {
	// A 2x1 image [A B] must display as the EXIF spec's reference layouts.
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	a, b := color.NRGBA{R: 1, A: 255}, color.NRGBA{R: 2, A: 255}
	src.SetNRGBA(0, 0, a)
	src.SetNRGBA(1, 0, b)
	tests := []struct {
		o    int
		want [][]color.NRGBA // rows
	}{
		{1, [][]color.NRGBA{{a, b}}},
		{2, [][]color.NRGBA{{b, a}}},
		{3, [][]color.NRGBA{{b, a}}},
		{4, [][]color.NRGBA{{a, b}}},
		{5, [][]color.NRGBA{{a}, {b}}},
		{6, [][]color.NRGBA{{a}, {b}}},
		{7, [][]color.NRGBA{{b}, {a}}},
		{8, [][]color.NRGBA{{b}, {a}}},
	}
	for _, tc := range tests {
		got := orient(src, tc.o)
		for y, row := range tc.want {
			for x, want := range row {
				if c := got.NRGBAAt(x, y); c != want {
					t.Errorf("orientation %d: (%d,%d) = %v, want %v", tc.o, x, y, c, want)
				}
			}
		}
	}
}

func TestThumbnailBadRequests(t *testing.T) {
	fsys := fstest.MapFS{"photo.jpg": {Data: encodeJPEG(t, stripes(40, 20))}}
	h, _ := newTestThumbnailHandler(t, fsys, "")
	for _, q := range []string{
		"width=0", "width=abc", "width=5000", "height=-1", "width=", "width=10&fit=zoom",
		"width=10&format=webp", "width=10&background=red", "width=10&quality=0",
	} {
		t.Run(q, func(t *testing.T) {
			if rec := thumbGet(t, h, "/photo.jpg?"+q, nil); rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestThumbnailPassThrough(t *testing.T) {
	fsys := fstest.MapFS{
		"photo.jpg":   {Data: encodeJPEG(t, stripes(40, 20))},
		"notes.txt":   {Data: []byte("hello")},
		"broken.jpg":  {Data: []byte("not a jpeg")},
		"dir.d/a.jpg": {Data: encodeJPEG(t, stripes(40, 20))},
	}
	h, baseCalls := newTestThumbnailHandler(t, fsys, "")
	for _, target := range []string{
		"/photo.jpg",            // no thumbnail parameters
		"/photo.jpg?view=rich",  // other parameters
		"/notes.txt?width=10",   // not an image
		"/missing.jpg?width=10", // not found: the file server answers
		"/broken.jpg?width=10",  // can't decode: served as-is
	} {
		before := baseCalls.Load()
		rec := thumbGet(t, h, target, nil)
		if baseCalls.Load() != before+1 || rec.Body.String() != "original" {
			t.Errorf("%s: want the original file, got status %d body %q", target, rec.Code, rec.Body.String())
		}
	}
}

func TestThumbnailCaching(t *testing.T) {
	fsys := &countingFS{MapFS: fstest.MapFS{"photo.jpg": {Data: encodeJPEG(t, stripes(40, 20)), ModTime: time.Unix(1_700_000_000, 0)}}}
	h, _ := newTestThumbnailHandler(t, fsys, "")

	first := thumbGet(t, h, "/photo.jpg?width=20", nil)
	etag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || etag == "" {
		t.Fatalf("first: status %d, ETag %q", first.Code, etag)
	}
	opens := fsys.opens.Load()
	second := thumbGet(t, h, "/photo.jpg?width=20", nil)
	if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Error("second response differs from the first")
	}
	if fsys.opens.Load() != opens {
		t.Error("second request re-read the file instead of using the cache")
	}
	if rec := thumbGet(t, h, "/photo.jpg?width=20", http.Header{"If-None-Match": {etag}}); rec.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: status %d, want 304", rec.Code)
	}
	if other := thumbGet(t, h, "/photo.jpg?width=10", nil); other.Header().Get("ETag") == etag {
		t.Error("different sizes share an ETag")
	}

	// Editing the file makes a new thumbnail.
	fsys.MapFS["photo.jpg"] = &fstest.MapFile{Data: encodeJPEG(t, stripes(80, 40)), ModTime: time.Unix(1_700_000_100, 0)}
	if rec := thumbGet(t, h, "/photo.jpg?width=20", nil); rec.Header().Get("ETag") == etag {
		t.Error("edited file kept its old ETag")
	}
}

func TestThumbnailStore(t *testing.T) {
	db := filepath.Join(t.TempDir(), "cache", "thumbnails.db")
	fsys := &countingFS{MapFS: fstest.MapFS{"photos/a.jpg": {Data: encodeJPEG(t, stripes(40, 20)), ModTime: time.Unix(1_700_000_000, 0)}}}
	base := http.NotFoundHandler()
	var th *thumbnailer
	open := func() {
		t.Helper()
		var err error
		if th, err = newThumbnailer(Thumbnails{Enabled: true, CachePath: db}, nil, (&monitoringContext{}).getTraceProvider()); err != nil {
			t.Fatal(err)
		}
	}
	closeStore := func() {
		t.Helper()
		if err := th.close(); err != nil {
			t.Fatal(err)
		}
	}
	open()
	stored := func(th *thumbnailer, pattern string) []string {
		t.Helper()
		matches, err := fs.Glob(th.store.fsys, pattern)
		if err != nil {
			t.Fatal(err)
		}
		return matches
	}

	h := th.handler(base, fsys, "/srv/photos")
	rec := thumbGet(t, h, "/photos/a.jpg?width=20&height=20&fit=cover", nil)
	decodeThumb(t, rec)

	// The thumbnail is a file in the bolt database, beside the path of the
	// photo it was made from, under a directory for the served filesystem.
	matches := stored(th, "*/photos/a.jpg/20x20-cover@*")
	if len(matches) != 1 {
		t.Fatalf("stored thumbnails = %v; want one", matches)
	}
	if data, err := th.store.fsys.ReadFile(matches[0]); err != nil || !bytes.Equal(data, rec.Body.Bytes()) {
		t.Errorf("stored file differs from the response (err %v)", err)
	}

	// Another served filesystem with a file at the same path has its own.
	thumbGet(t, th.handler(base, fsys, "/srv/other"), "/photos/a.jpg?width=20&height=20&fit=cover", nil)
	if n := len(stored(th, "*/photos/a.jpg/20x20-cover@*")); n != 2 {
		t.Errorf("%d thumbnails for two served filesystems, want 2", n)
	}
	closeStore()

	// After a restart, thumbnails come from the database without reading
	// the photo.
	open()
	defer closeStore()
	h = th.handler(base, fsys, "/srv/photos")
	opens := fsys.opens.Load()
	if again := thumbGet(t, h, "/photos/a.jpg?width=20&height=20&fit=cover", nil); !bytes.Equal(again.Body.Bytes(), rec.Body.Bytes()) {
		t.Error("thumbnail changed across a restart")
	}
	if fsys.opens.Load() != opens {
		t.Error("read the photo instead of the stored thumbnail after a restart")
	}

	// Editing the photo replaces its old thumbnail of that size and leaves
	// other sizes.
	thumbGet(t, h, "/photos/a.jpg?width=10", nil)
	fsys.MapFS["photos/a.jpg"] = &fstest.MapFile{Data: encodeJPEG(t, stripes(80, 40)), ModTime: time.Unix(1_700_000_100, 0)}
	thumbGet(t, h, "/photos/a.jpg?width=20&height=20&fit=cover", nil)
	if now := stored(th, "*/photos/a.jpg/20x20-cover@*"); len(now) != 2 || slices.Contains(now, matches[0]) {
		t.Errorf("after an edit stored %v; want the old version replaced (was %s)", now, matches[0])
	}
	if n := len(stored(th, "*/photos/a.jpg/10x0-*")); n != 1 {
		t.Errorf("%d thumbnails of another size, want 1 left alone", n)
	}
}

func TestThumbnailStoreMemory(t *testing.T) {
	// "memory:" keeps thumbnails in memory: nothing is written to the
	// user cache directory.
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	fsys := fstest.MapFS{"a.jpg": {Data: encodeJPEG(t, stripes(40, 20))}}
	th, err := newThumbnailer(Thumbnails{Enabled: true, CachePath: thumbnailCacheMemory}, nil, (&monitoringContext{}).getTraceProvider())
	if err != nil {
		t.Fatal(err)
	}
	decodeThumb(t, thumbGet(t, th.handler(http.NotFoundHandler(), fsys, "test:"), "/a.jpg?width=10", nil))
	if err := th.close(); err != nil {
		t.Fatal(err)
	}
	if dbs, err := filepath.Glob(filepath.Join(cache, "*", "*", "thumbnails.db")); err != nil || len(dbs) != 0 {
		t.Errorf("databases %v (err %v); want none", dbs, err)
	}
	if dbs, err := filepath.Glob(filepath.Join(cache, "*", "thumbnails.db")); err != nil || len(dbs) != 0 {
		t.Errorf("databases %v (err %v); want none", dbs, err)
	}
}

func TestThumbnailShared(t *testing.T) {
	th, err := newThumbnailer(Thumbnails{Enabled: true, CachePath: thumbnailCacheMemory}, nil, (&monitoringContext{}).getTraceProvider())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := th.close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	result := &thumbnail{data: []byte("thumb")}
	// blockingRender counts its runs and waits for release (or its
	// context) before returning; cancelled reports its context ending.
	blockingRender := func(runs *atomic.Int32, release, cancelled chan struct{}) func(context.Context) (*thumbnail, error) {
		return func(ctx context.Context) (*thumbnail, error) {
			runs.Add(1)
			select {
			case <-release:
				return result, nil
			case <-ctx.Done():
				close(cancelled)
				return nil, ctx.Err()
			}
		}
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}
	waiters := func(key string) int {
		th.mu.Lock()
		defer th.mu.Unlock()
		if r := th.renders[key]; r != nil {
			return r.waiters
		}
		return 0
	}

	t.Run("one render for concurrent requests", func(t *testing.T) {
		var runs atomic.Int32
		release, cancelled := make(chan struct{}), make(chan struct{})
		mk := blockingRender(&runs, release, cancelled)
		var wg sync.WaitGroup
		for range 5 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if got, err := th.shared(t.Context(), "a", mk); err != nil || got != result {
					t.Errorf("shared = %v, %v", got, err)
				}
			}()
		}
		waitFor("5 waiters", func() bool { return waiters("a") == 5 })
		close(release)
		wg.Wait()
		if n := runs.Load(); n != 1 {
			t.Errorf("rendered %d times, want once", n)
		}
	})

	t.Run("cancelled when every request leaves", func(t *testing.T) {
		var runs atomic.Int32
		release, cancelled := make(chan struct{}), make(chan struct{})
		mk := blockingRender(&runs, release, cancelled)
		ctx1, cancel1 := context.WithCancel(t.Context())
		ctx2, cancel2 := context.WithCancel(t.Context())
		errs := make(chan error, 2)
		for _, ctx := range []context.Context{ctx1, ctx2} {
			go func() {
				_, err := th.shared(ctx, "b", mk)
				errs <- err
			}()
		}
		waitFor("2 waiters", func() bool { return waiters("b") == 2 })
		cancel1()
		if err := <-errs; !errors.Is(err, context.Canceled) {
			t.Errorf("first request: %v, want context.Canceled", err)
		}
		select {
		case <-cancelled:
			t.Fatal("render cancelled while a request still waited for it")
		case <-time.After(20 * time.Millisecond):
		}
		cancel2()
		<-errs
		select {
		case <-cancelled:
		case <-time.After(5 * time.Second):
			t.Fatal("render kept going after every request left")
		}

		// A new request after that starts a fresh render.
		var again atomic.Int32
		fresh := make(chan struct{})
		close(fresh)
		if got, err := th.shared(t.Context(), "b", blockingRender(&again, fresh, make(chan struct{}))); err != nil || got != result {
			t.Errorf("after abandoning: %v, %v", got, err)
		}
		if again.Load() != 1 {
			t.Error("a request after the render was abandoned didn't start a new one")
		}
	})
}

func TestThumbnailStoreUnsafePath(t *testing.T) {
	// ufs would cut the path short at a # and put the database somewhere
	// else, so such paths fall back to memory and create nothing.
	dir := t.TempDir()
	s, err := newThumbnailStore(filepath.Join(dir, "a#b", "thumbnails.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.put("x/1x1-cover@v", &thumbnail{data: []byte("thumb")}); err != nil {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	if left, err := os.ReadDir(dir); err != nil || len(left) != 0 {
		t.Errorf("created %v (err %v); want nothing", left, err)
	}
}

func TestShrinkYCbCr(t *testing.T) {
	// Left half one color, right half another, in 4:2:0 like most JPEGs.
	left, right := color.RGBA{200, 40, 40, 255}, color.RGBA{30, 90, 220, 255}
	src := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := range 480 {
		for x := range 640 {
			c := left
			if x >= 320 {
				c = right
			}
			src.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := decoded.(*image.YCbCr)
	if !ok {
		t.Fatalf("decoded %T, want *image.YCbCr", decoded)
	}

	// 640/7 doesn't divide evenly: the last column averages a partial block.
	out := shrinkYCbCr(m, 7)
	if b := out.Bounds(); b.Dx() != 92 || b.Dy() != 69 {
		t.Errorf("size = %dx%d, want 92x69", b.Dx(), b.Dy())
	}
	near := func(got, want color.RGBA) bool {
		d := func(a, b uint8) bool { return a-b < 6 || b-a < 6 }
		return d(got.R, want.R) && d(got.G, want.G) && d(got.B, want.B) && got.A == 0xff
	}
	for _, p := range []struct {
		x, y int
		want color.RGBA
	}{{5, 30, left}, {40, 5, left}, {50, 60, right}, {91, 68, right}} {
		if got := out.RGBAAt(p.x, p.y); !near(got, p.want) {
			t.Errorf("pixel (%d,%d) = %v, want about %v", p.x, p.y, got, p.want)
		}
	}

	// The whole path: a big reduction goes through the shrink and ends at
	// the requested size, still split down the middle.
	th := scaleImage(m, 64, 48)
	if b := th.Bounds(); b.Dx() != 64 || b.Dy() != 48 {
		t.Errorf("scaled to %dx%d, want 64x48", b.Dx(), b.Dy())
	}
	l, r := th.NRGBAAt(10, 24), th.NRGBAAt(54, 24)
	if !near(color.RGBA(l), left) || !near(color.RGBA(r), right) {
		t.Errorf("scaled halves %v and %v, want about %v and %v", l, r, left, right)
	}
}

func TestThumbnailStoreFallback(t *testing.T) {
	// A path that can't hold a database falls back to memory.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := newThumbnailStore(filepath.Join(blocker, "thumbnails.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.put("x/1x1-cover@v", &thumbnail{data: []byte("thumb")}); err != nil {
		t.Fatal(err)
	}
	if th, ok := s.get("x/1x1-cover@v"); !ok || string(th.data) != "thumb" {
		t.Errorf("get = %v, %v", th, ok)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
}

// fakeFFmpeg writes a script standing in for ffmpeg. It appends each
// run's arguments to argsFile, describes the input as lasting duration
// (an ffmpeg duration such as "00:00:20.00", or "N/A") when probed, and
// prints frame as a PNG when asked for one.
func fakeFFmpeg(t *testing.T, frame image.Image, duration string) (bin, argsFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as ffmpeg")
	}
	dir := t.TempDir()
	framePath := filepath.Join(dir, "frame.png")
	if err := os.WriteFile(framePath, encodePNG(t, frame), 0o600); err != nil {
		t.Fatal(err)
	}
	argsFile = filepath.Join(dir, "args.txt")
	bin = filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> '" + argsFile + "'\n" +
		"cat > /dev/null\n" +
		// Without -frames:v it's a probe: describe the input, then fail
		// for want of an output, as ffmpeg does.
		"case \"$*\" in *-frames:v*) ;; *) echo '  Duration: " + duration + ", start: 0.000000' >&2; exit 1;; esac\n" +
		"cat '" + framePath + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile
}

// frameArgs returns the arguments of the runs that asked for a frame.
func frameArgs(t *testing.T, argsFile string) []string {
	t.Helper()
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	var runs []string
	for _, run := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.Contains(run, "-frames:v") {
			runs = append(runs, run)
		}
	}
	return runs
}

func TestThumbnailVideo(t *testing.T) {
	video := fstest.MapFS{"clip.mp4": {Data: []byte("not really a video")}}

	t.Run("no ffmpeg", func(t *testing.T) {
		h, _ := newTestThumbnailHandler(t, video, "")
		if rec := thumbGet(t, h, "/clip.mp4?width=32", nil); rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("status = %d, want 415", rec.Code)
		}
	})

	t.Run("piped from an archive", func(t *testing.T) {
		bin, argsFile := fakeFFmpeg(t, stripes(64, 36), "00:00:20.00")
		h, _ := newTestThumbnailHandler(t, video, bin)
		img := decodeThumb(t, thumbGet(t, h, "/clip.mp4?width=32", nil))
		if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 18 {
			t.Errorf("size = %dx%d, want 32x18", b.Dx(), b.Dy())
		}
		args, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(args), "-i pipe:0") {
			t.Errorf("ffmpeg args %q: want the file piped to stdin", args)
		}
		// 10% of the 20s the probe reported.
		if runs := frameArgs(t, argsFile); len(runs) != 1 || !strings.Contains(runs[0], "-ss 2.000 -i pipe:0") {
			t.Errorf("frame runs %q: want one at 2s", runs)
		}
	})

	t.Run("copied when it can't be piped", func(t *testing.T) {
		// Stands in for an MP4 with its index at the end: ffmpeg fails on a
		// pipe but reads a file by path.
		bin, argsFile := fakeFFmpeg(t, stripes(64, 36), "00:00:20.00")
		script, err := os.ReadFile(bin)
		if err != nil {
			t.Fatal(err)
		}
		refusePipe := strings.Replace(string(script), "#!/bin/sh\n", "#!/bin/sh\ncase \"$*\" in *pipe:0*) cat > /dev/null; exit 1;; esac\n", 1)
		if err := os.WriteFile(bin, []byte(refusePipe), 0o700); err != nil {
			t.Fatal(err)
		}
		h, _ := newTestThumbnailHandler(t, video, bin)
		decodeThumb(t, thumbGet(t, h, "/clip.mp4?width=32", nil))
		args, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(args), "gowebserver-video-") {
			t.Errorf("ffmpeg args %q: want a temporary copy's path", args)
		}
		left, err := filepath.Glob(filepath.Join(os.TempDir(), "gowebserver-video-*"))
		if err != nil || len(left) != 0 {
			t.Errorf("temporary copies left behind: %v (err %v)", left, err)
		}
	})

	t.Run("local file by path", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "clip.mp4"), []byte("not really a video"), 0o600); err != nil {
			t.Fatal(err)
		}
		bin, argsFile := fakeFFmpeg(t, stripes(64, 36), "00:00:20.00")
		h, _ := newTestThumbnailHandler(t, os.DirFS(dir), bin)
		decodeThumb(t, thumbGet(t, h, "/clip.mp4?width=32&height=32", nil))
		args, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(args), "-i "+filepath.Join(dir, "clip.mp4")) {
			t.Errorf("ffmpeg args %q: want the file's path so ffmpeg can seek", args)
		}
		if runs := frameArgs(t, argsFile); len(runs) != 1 || !strings.Contains(runs[0], "-ss 2.000 -i ") {
			t.Errorf("frame runs %q: want one at 10%% of 20s", runs)
		}
	})

	t.Run("unknown duration", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "clip.mp4"), []byte("not really a video"), 0o600); err != nil {
			t.Fatal(err)
		}
		bin, argsFile := fakeFFmpeg(t, stripes(64, 36), "N/A")
		h, _ := newTestThumbnailHandler(t, os.DirFS(dir), bin)
		decodeThumb(t, thumbGet(t, h, "/clip.mp4?width=32", nil))
		if runs := frameArgs(t, argsFile); len(runs) != 1 || !strings.Contains(runs[0], "-ss 1.000 ") {
			t.Errorf("frame runs %q: want one a second in", runs)
		}
	})

	t.Run("first frame of a short clip", func(t *testing.T) {
		// ffmpeg gives no frame when asked to seek past the end.
		bin, argsFile := fakeFFmpeg(t, stripes(64, 36), "00:00:00.08")
		script, err := os.ReadFile(bin)
		if err != nil {
			t.Fatal(err)
		}
		noSeek := strings.Replace(string(script), "cat > /dev/null\n", "cat > /dev/null\ncase \"$*\" in *-frames:v*) case \"$*\" in *'-ss 0.000 '*) ;; *) exit 0;; esac;; esac\n", 1)
		if err := os.WriteFile(bin, []byte(noSeek), 0o700); err != nil {
			t.Fatal(err)
		}
		h, _ := newTestThumbnailHandler(t, video, bin)
		decodeThumb(t, thumbGet(t, h, "/clip.mp4?width=32", nil))
		runs := frameArgs(t, argsFile)
		if len(runs) != 2 || !strings.Contains(runs[0], "-ss 0.008 ") || !strings.Contains(runs[1], "-ss 0.000 ") {
			t.Errorf("frame runs %q: want 10%% in, then the first frame", runs)
		}
	})
}

func TestParseThumbnailSpec(t *testing.T) {
	q := func(s string) url.Values {
		v, err := url.ParseQuery(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if _, ok, err := parseThumbnailSpec(q("view=rich")); ok || err != nil {
		t.Errorf("a request without width or height is not a thumbnail request (ok %v, err %v)", ok, err)
	}
	spec, ok, err := parseThumbnailSpec(q("width=10"))
	if !ok || err != nil || spec.fit != fitCover || spec.quality != thumbnailJPEGQuality || spec.format != "" {
		t.Errorf("defaults: %+v, %v, %v", spec, ok, err)
	}
	a, _, err := parseThumbnailSpec(q("width=10&height=20&fit=cover"))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := parseThumbnailSpec(q("width=10&height=20&fit=contain"))
	if err != nil {
		t.Fatal(err)
	}
	if a.key() == b.key() {
		t.Error("different fits share a cache key")
	}
}
