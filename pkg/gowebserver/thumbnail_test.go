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
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gowsTesting "github.com/cloudfra/gowebserver/internal/gowebserver/testing"
	"github.com/cloudfra/ufs"
	"github.com/google/go-cmp/cmp"
)

func mustNoError(tb testing.TB, err error) {
	tb.Helper()
	if err != nil {
		tb.Fatal(err)
	}
}

func wantEqual[T any](tb testing.TB, what string, want T, got T) {
	tb.Helper()
	if diff := cmp.Diff(want, got); diff != "" {
		tb.Errorf("%s mismatch (-want +got):\n%s", what, diff)
	}
}

func wantBytes(tb testing.TB, what string, want []byte, got []byte) {
	tb.Helper()
	if !bytes.Equal(want, got) {
		tb.Errorf("%s mismatch: want %d bytes, got %d bytes", what, len(want), len(got))
	}
}

func wantStatus(tb testing.TB, what string, want int, res *http.Response) {
	tb.Helper()
	if res.StatusCode != want {
		tb.Fatalf("%s: status = %d, want %d", what, res.StatusCode, want)
	}
}

// countingFS counts Open calls so tests can tell whether a source image was
// read. Stat is delegated without counting.
type countingFS struct {
	fs.FS
	opens atomic.Int64
}

func (c *countingFS) Open(name string) (fs.File, error) {
	c.opens.Add(1)
	return c.FS.Open(name)
}

func (c *countingFS) Stat(name string) (fs.FileInfo, error) {
	return fs.Stat(c.FS, name)
}

func gradientImage(w, h int, alpha uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 255 / w), G: uint8(y * 255 / h), B: 128, A: alpha})
		}
	}
	return img
}

func jpegBytes(tb testing.TB, img image.Image) []byte {
	tb.Helper()
	var buf bytes.Buffer
	mustNoError(tb, jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}))
	return buf.Bytes()
}

func pngBytes(tb testing.TB, img image.Image) []byte {
	tb.Helper()
	var buf bytes.Buffer
	mustNoError(tb, png.Encode(&buf, img))
	return buf.Bytes()
}

// withEXIFOrientation inserts an APP1 Exif segment carrying orientation right
// after the JPEG SOI marker.
func withEXIFOrientation(tb testing.TB, jpg []byte, orientation uint16, bo binary.AppendByteOrder) []byte {
	tb.Helper()
	var tiff []byte
	if bo == binary.BigEndian {
		tiff = append(tiff, "MM"...)
	} else {
		tiff = append(tiff, "II"...)
	}
	tiff = bo.AppendUint16(tiff, 42)
	tiff = bo.AppendUint32(tiff, 8)
	tiff = bo.AppendUint16(tiff, 1) // one IFD entry
	tiff = bo.AppendUint16(tiff, 0x0112)
	tiff = bo.AppendUint16(tiff, 3) // SHORT
	tiff = bo.AppendUint32(tiff, 1)
	tiff = bo.AppendUint16(tiff, orientation)
	tiff = bo.AppendUint16(tiff, 0)
	tiff = bo.AppendUint32(tiff, 0) // no next IFD

	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1}
	seg = binary.BigEndian.AppendUint16(seg, uint16(len(payload)+2))
	seg = append(seg, payload...)

	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

type thumbTestServer struct {
	*httptest.Server
	dir    string
	src    *countingFS
	thumbs *thumbnailer
}

func newThumbTestServer(tb testing.TB, budget int64, files map[string][]byte) *thumbTestServer {
	tb.Helper()
	dir := tb.TempDir()
	for name, content := range files {
		mustNoError(tb, os.WriteFile(filepath.Join(dir, name), content, 0o644))
	}
	nFS, err := ufs.New(tb.Context(), dir)
	mustNoError(tb, err)
	tb.Cleanup(gowsTesting.DeferClose(tb, nFS))
	src := &countingFS{FS: nFS}

	thumbs, err := newThumbnailer(tb.Context(), thumbnailConfig{BudgetBytes: budget})
	mustNoError(tb, err)
	tb.Cleanup(gowsTesting.DeferClose(tb, thumbs))

	mc := &monitoringContext{}
	ci, err := newCustomIndex(http.FileServer(http.FS(nFS)), src, mc.getTraceProvider(), false, thumbs)
	mustNoError(tb, err)
	ts := httptest.NewServer(ci)
	tb.Cleanup(ts.Close)
	return &thumbTestServer{Server: ts, dir: dir, src: src, thumbs: thumbs}
}

func (s *thumbTestServer) get(tb testing.TB, urlPath string, header http.Header) (*http.Response, []byte) {
	tb.Helper()
	return httpGet(tb, s.Client(), s.URL+urlPath, header)
}

func httpGet(tb testing.TB, hc *http.Client, u string, header http.Header) (*http.Response, []byte) {
	tb.Helper()
	req, err := http.NewRequestWithContext(tb.Context(), http.MethodGet, u, nil)
	mustNoError(tb, err)
	for k, v := range header {
		req.Header[k] = v
	}
	res, err := hc.Do(req)
	mustNoError(tb, err)
	defer gowsTesting.DeferClose(tb, res.Body)()
	body, err := io.ReadAll(res.Body)
	mustNoError(tb, err)
	return res, body
}

func decodeBounds(tb testing.TB, data []byte) image.Point {
	tb.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	mustNoError(tb, err)
	return image.Pt(cfg.Width, cfg.Height)
}

func TestThumbnailResizesToSizeLadder(t *testing.T) {
	t.Parallel()
	src := jpegBytes(t, gradientImage(1200, 800, 255))
	s := newThumbTestServer(t, 64<<20, map[string][]byte{"photo.jpg": src})

	for _, tc := range []struct {
		size int
		want image.Point
	}{
		{256, image.Pt(256, 170)},
		{512, image.Pt(512, 341)},
		{1024, image.Pt(1024, 682)},
	} {
		res, body := s.get(t, "/photo.jpg?thumb="+strconv.Itoa(tc.size), nil)
		wantStatus(t, "thumb "+strconv.Itoa(tc.size), http.StatusOK, res)
		wantEqual(t, "content type", "image/jpeg", res.Header.Get("Content-Type"))
		wantEqual(t, "cache control", thumbnailCacheControl, res.Header.Get("Cache-Control"))
		if res.Header.Get("ETag") == "" {
			t.Errorf("size %d: missing ETag", tc.size)
		}
		wantEqual(t, "bounds for size "+strconv.Itoa(tc.size), tc.want, decodeBounds(t, body))
		if len(body) >= len(src) {
			t.Errorf("size %d: thumbnail is %d bytes, not smaller than the %d byte original", tc.size, len(body), len(src))
		}
	}
}

func TestThumbnailPortraitLongestEdge(t *testing.T) {
	t.Parallel()
	s := newThumbTestServer(t, 64<<20, map[string][]byte{"tall.jpg": jpegBytes(t, gradientImage(300, 750, 255))})
	res, body := s.get(t, "/tall.jpg?thumb=256", nil)
	wantStatus(t, "portrait", http.StatusOK, res)
	wantEqual(t, "bounds", image.Pt(102, 256), decodeBounds(t, body))
}

func TestThumbnailInvalidSize(t *testing.T) {
	t.Parallel()
	s := newThumbTestServer(t, 64<<20, map[string][]byte{"photo.jpg": jpegBytes(t, gradientImage(800, 600, 255))})
	for _, q := range []string{"300", "0", "-256", "abc", "256.5"} {
		res, _ := s.get(t, "/photo.jpg?thumb="+q, nil)
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("thumb=%s: status = %d, want %d", q, res.StatusCode, http.StatusBadRequest)
		}
	}
}

func TestThumbnailIgnoredForNonImages(t *testing.T) {
	t.Parallel()
	s := newThumbTestServer(t, 64<<20, map[string][]byte{"notes.txt": []byte("hello")})
	res, body := s.get(t, "/notes.txt?thumb=999", nil)
	wantStatus(t, "non-image", http.StatusOK, res)
	wantEqual(t, "body", "hello", string(body))
}

func TestThumbnailFallsBackToOriginal(t *testing.T) {
	t.Parallel()
	// PNG header advertising 20000x10000 (200MP), over the pixel limit.
	var huge bytes.Buffer
	huge.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := binary.BigEndian.AppendUint32(nil, 20000)
	ihdr = binary.BigEndian.AppendUint32(ihdr, 10000)
	ihdr = append(ihdr, 8, 6, 0, 0, 0)
	chunk := append([]byte("IHDR"), ihdr...)
	huge.Write(binary.BigEndian.AppendUint32(nil, uint32(len(ihdr))))
	huge.Write(chunk)
	huge.Write(binary.BigEndian.AppendUint32(nil, crc32.ChecksumIEEE(chunk)))

	files := map[string][]byte{
		"corrupt.jpg": []byte("this is not a jpeg at all"),
		"phone.heic":  []byte("pretend heic data"),
		"vector.svg":  []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"),
		"huge.png":    huge.Bytes(),
		"tiny.jpg":    jpegBytes(t, gradientImage(200, 100, 255)),
	}
	s := newThumbTestServer(t, 64<<20, files)
	for name, want := range files {
		res, body := s.get(t, "/"+name+"?thumb=256", nil)
		wantStatus(t, name, http.StatusOK, res)
		wantBytes(t, name+" must be served unchanged", want, body)
	}
	if got := s.thumbs.cacheBytes(); got != 0 {
		t.Errorf("fallbacks populated the cache with %d bytes", got)
	}
}

func TestThumbnailRemembersFallbacks(t *testing.T) {
	t.Parallel()
	s := newThumbTestServer(t, 64<<20, map[string][]byte{"corrupt.jpg": []byte("this is not a jpeg at all")})
	s.get(t, "/corrupt.jpg?thumb=256", nil)
	wantEqual(t, "source opens by the first failed generation", int64(1), s.src.opens.Load())

	// The failure is remembered, so the repeat never tries to generate again.
	// (The FileServer serving the original reads the file system directly, not
	// through the counting wrapper.)
	res, _ := s.get(t, "/corrupt.jpg?thumb=256", nil)
	wantStatus(t, "repeat", http.StatusOK, res)
	wantEqual(t, "source opens by a repeated fallback", int64(1), s.src.opens.Load())
}

func TestThumbnailAlphaProducesPNG(t *testing.T) {
	t.Parallel()
	s := newThumbTestServer(t, 64<<20, map[string][]byte{
		"clear.png":  pngBytes(t, gradientImage(600, 600, 128)),
		"opaque.png": pngBytes(t, gradientImage(600, 600, 255)),
	})
	res, body := s.get(t, "/clear.png?thumb=256", nil)
	wantStatus(t, "clear.png", http.StatusOK, res)
	wantEqual(t, "clear.png content type", "image/png", res.Header.Get("Content-Type"))
	wantEqual(t, "clear.png bounds", image.Pt(256, 256), decodeBounds(t, body))

	res, body = s.get(t, "/opaque.png?thumb=256", nil)
	wantStatus(t, "opaque.png", http.StatusOK, res)
	wantEqual(t, "opaque.png content type", "image/jpeg", res.Header.Get("Content-Type"))
	wantEqual(t, "opaque.png bounds", image.Pt(256, 256), decodeBounds(t, body))
}

func TestThumbnailAppliesEXIFOrientation(t *testing.T) {
	t.Parallel()
	base := jpegBytes(t, gradientImage(600, 400, 255))
	files := map[string][]byte{
		"upright.jpg":   base,
		"rot90.jpg":     withEXIFOrientation(t, base, 6, binary.BigEndian),
		"rot270le.jpg":  withEXIFOrientation(t, base, 8, binary.LittleEndian),
		"rot180.jpg":    withEXIFOrientation(t, base, 3, binary.BigEndian),
		"badvalue.jpg":  withEXIFOrientation(t, base, 42, binary.BigEndian),
		"mirrored5.jpg": withEXIFOrientation(t, base, 5, binary.LittleEndian),
	}
	s := newThumbTestServer(t, 64<<20, files)
	want := map[string]image.Point{
		"upright.jpg":   image.Pt(256, 170),
		"rot90.jpg":     image.Pt(170, 256),
		"rot270le.jpg":  image.Pt(170, 256),
		"rot180.jpg":    image.Pt(256, 170),
		"badvalue.jpg":  image.Pt(256, 170),
		"mirrored5.jpg": image.Pt(170, 256),
	}
	for name, size := range want {
		res, body := s.get(t, "/"+name+"?thumb=256", nil)
		wantStatus(t, name, http.StatusOK, res)
		wantEqual(t, name+" bounds", size, decodeBounds(t, body))
	}
}

func TestOrientMapsPixels(t *testing.T) {
	t.Parallel()
	// 3x2 source where each pixel encodes its own coordinates.
	const w, h = 3, 2
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			src.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), A: 255})
		}
	}
	// For each orientation: the output size, and which source pixel lands at
	// the output's top-left.
	for _, tc := range []struct {
		orientation int
		size        image.Point
		topLeftFrom image.Point
	}{
		{1, image.Pt(3, 2), image.Pt(0, 0)},
		{2, image.Pt(3, 2), image.Pt(2, 0)},
		{3, image.Pt(3, 2), image.Pt(2, 1)},
		{4, image.Pt(3, 2), image.Pt(0, 1)},
		{5, image.Pt(2, 3), image.Pt(0, 0)},
		{6, image.Pt(2, 3), image.Pt(0, 1)},
		{7, image.Pt(2, 3), image.Pt(2, 1)},
		{8, image.Pt(2, 3), image.Pt(2, 0)},
	} {
		got := orient(src, tc.orientation)
		if tc.orientation == 1 {
			if got != src {
				t.Error("orient(src, 1) must return the source unchanged")
			}
			continue
		}
		wantEqual(t, "orientation "+strconv.Itoa(tc.orientation)+" size", tc.size, got.Bounds().Size())
		c := got.RGBAAt(0, 0)
		wantEqual(t, "orientation "+strconv.Itoa(tc.orientation)+" top-left source", tc.topLeftFrom, image.Pt(int(c.R), int(c.G)))
	}
	for _, o := range []int{0, 9} {
		if orient(src, o) != src {
			t.Errorf("orient(src, %d) must return the source unchanged", o)
		}
	}
}

func TestThumbnailCacheHitAndStaleKey(t *testing.T) {
	t.Parallel()
	s := newThumbTestServer(t, 64<<20, map[string][]byte{"photo.jpg": jpegBytes(t, gradientImage(600, 400, 255))})

	res1, body1 := s.get(t, "/photo.jpg?thumb=256", nil)
	wantStatus(t, "first", http.StatusOK, res1)
	wantEqual(t, "opens after first request", int64(1), s.src.opens.Load())

	res2, body2 := s.get(t, "/photo.jpg?thumb=256", nil)
	wantStatus(t, "second", http.StatusOK, res2)
	wantBytes(t, "cached body", body1, body2)
	wantEqual(t, "ETag", res1.Header.Get("ETag"), res2.Header.Get("ETag"))
	wantEqual(t, "opens after cache hit", int64(1), s.src.opens.Load())

	// A different size is a separate cache entry.
	s.get(t, "/photo.jpg?thumb=512", nil)
	wantEqual(t, "opens after a new size", int64(2), s.src.opens.Load())

	// A changed source (new mtime) must not be served a stale thumbnail.
	later := time.Now().Add(time.Hour)
	mustNoError(t, os.Chtimes(filepath.Join(s.dir, "photo.jpg"), later, later))
	res3, _ := s.get(t, "/photo.jpg?thumb=256", nil)
	wantStatus(t, "after touch", http.StatusOK, res3)
	wantEqual(t, "opens after the source changed", int64(3), s.src.opens.Load())
	if res1.Header.Get("ETag") == res3.Header.Get("ETag") {
		t.Errorf("ETag %q did not change after the source was modified", res3.Header.Get("ETag"))
	}
}

func TestThumbnailConditionalRequest(t *testing.T) {
	t.Parallel()
	s := newThumbTestServer(t, 64<<20, map[string][]byte{"photo.jpg": jpegBytes(t, gradientImage(600, 400, 255))})
	res, _ := s.get(t, "/photo.jpg?thumb=256", nil)
	etag := res.Header.Get("ETag")
	lastModified := res.Header.Get("Last-Modified")
	if etag == "" || lastModified == "" {
		t.Fatalf("missing validators: ETag=%q Last-Modified=%q", etag, lastModified)
	}

	res, body := s.get(t, "/photo.jpg?thumb=256", http.Header{"If-None-Match": {etag}})
	wantStatus(t, "If-None-Match", http.StatusNotModified, res)
	wantEqual(t, "304 body length", 0, len(body))

	res, _ = s.get(t, "/photo.jpg?thumb=256", http.Header{"If-Modified-Since": {lastModified}})
	wantStatus(t, "If-Modified-Since", http.StatusNotModified, res)
}

func TestThumbnailRangeRequest(t *testing.T) {
	t.Parallel()
	s := newThumbTestServer(t, 64<<20, map[string][]byte{"photo.jpg": jpegBytes(t, gradientImage(600, 400, 255))})
	res, body := s.get(t, "/photo.jpg?thumb=256", http.Header{"Range": {"bytes=0-9"}})
	wantStatus(t, "range", http.StatusPartialContent, res)
	wantEqual(t, "range length", 10, len(body))
}

func TestThumbnailEvictsLeastRecentlyUsed(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{}
	for _, name := range []string{"a.jpg", "b.jpg", "c.jpg"} {
		files[name] = jpegBytes(t, gradientImage(600, 400, 255))
	}
	// Measure one thumbnail, then allow room for about one and a half.
	probe := newThumbTestServer(t, 64<<20, files)
	_, body := probe.get(t, "/a.jpg?thumb=256", nil)
	budget := int64(len(body)) * 3 / 2

	s := newThumbTestServer(t, budget, files)
	s.get(t, "/a.jpg?thumb=256", nil)
	s.get(t, "/b.jpg?thumb=256", nil)
	if got := s.thumbs.cacheBytes(); got > budget {
		t.Errorf("cache holds %d bytes, over the %d byte budget", got, budget)
	}
	s.thumbs.mu.Lock()
	entries := s.thumbs.lru.Len()
	s.thumbs.mu.Unlock()
	wantEqual(t, "entries (a.jpg must be evicted for b.jpg)", 1, entries)

	// The evicted entry is really gone from the backing file system.
	stored, err := fs.Glob(s.thumbs.cache, "t256/*")
	mustNoError(t, err)
	wantEqual(t, "files in the backing cache", 1, len(stored))

	before := s.src.opens.Load()
	s.get(t, "/a.jpg?thumb=256", nil)
	wantEqual(t, "evicted thumbnail must be regenerated", before+1, s.src.opens.Load())
	if got := s.thumbs.cacheBytes(); got > budget {
		t.Errorf("cache holds %d bytes after regeneration, over the %d byte budget", got, budget)
	}
}

func TestThumbnailCollapsesConcurrentRequests(t *testing.T) {
	t.Parallel()
	s := newThumbTestServer(t, 64<<20, map[string][]byte{"photo.jpg": jpegBytes(t, gradientImage(1200, 800, 255))})

	const n = 24
	var wg sync.WaitGroup
	bodies := make([][]byte, n)
	statuses := make([]int, n)
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, body := s.get(t, "/photo.jpg?thumb=512", nil)
			statuses[i] = res.StatusCode
			bodies[i] = body
		}()
	}
	close(start)
	wg.Wait()

	wantEqual(t, "source opens for identical concurrent requests", int64(1), s.src.opens.Load())
	if len(bodies[0]) == 0 {
		t.Fatal("empty thumbnail body")
	}
	for i := range n {
		wantEqual(t, "status", http.StatusOK, statuses[i])
		wantBytes(t, "body", bodies[0], bodies[i])
	}
}

func TestThumbnailCanceledRequestDoesNotPoisonCache(t *testing.T) {
	t.Parallel()
	s := newThumbTestServer(t, 64<<20, map[string][]byte{"photo.jpg": jpegBytes(t, gradientImage(600, 400, 255))})

	// Occupy every decode slot so the request blocks waiting for one.
	slots := cap(s.thumbs.sem)
	for range slots {
		s.thumbs.sem <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL+"/photo.jpg?thumb=256", nil)
	mustNoError(t, err)
	if res, err := s.Client().Do(req); err == nil {
		gowsTesting.DeferClose(t, res.Body)()
		t.Fatal("request finished although every decode slot was busy")
	}
	for range slots {
		<-s.thumbs.sem
	}

	// The timed-out attempt must not have been remembered as a failure.
	res, body := s.get(t, "/photo.jpg?thumb=256", nil)
	wantStatus(t, "after cancel", http.StatusOK, res)
	wantEqual(t, "content type", "image/jpeg", res.Header.Get("Content-Type"))
	wantEqual(t, "bounds", image.Pt(256, 170), decodeBounds(t, body))
}

func TestThumbnailThroughHandlerFromFS(t *testing.T) {
	t.Parallel()
	src := jpegBytes(t, gradientImage(600, 400, 255))
	for _, tc := range []struct {
		name       string
		budget     int64
		wantOrigin bool
	}{
		{"enabled", 32 << 20, false},
		{"disabled", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mustNoError(t, os.WriteFile(filepath.Join(dir, "photo.jpg"), src, 0o644))

			h, cleanup, err := newHandlerFromFS(dir, (&monitoringContext{}).getTraceProvider(), true, thumbnailConfig{BudgetBytes: tc.budget})
			mustNoError(t, err)
			defer func() { mustNoError(t, cleanup()) }()
			ts := httptest.NewServer(h)
			defer ts.Close()

			res, body := httpGet(t, ts.Client(), ts.URL+"/photo.jpg?thumb=256", nil)
			wantStatus(t, tc.name, http.StatusOK, res)
			if tc.wantOrigin {
				wantBytes(t, "disabled thumbnails must serve the original", src, body)
				return
			}
			wantEqual(t, "bounds", image.Pt(256, 170), decodeBounds(t, body))
		})
	}
}

func TestThumbnailFromZipArchive(t *testing.T) {
	t.Parallel()
	zipPath := gowsTesting.MustZipFilePath(t)
	h, cleanup, err := newHandlerFromFS(zipPath, (&monitoringContext{}).getTraceProvider(), true, thumbnailConfig{BudgetBytes: 32 << 20})
	mustNoError(t, err)
	defer func() { mustNoError(t, cleanup()) }()
	ts := httptest.NewServer(h)
	defer ts.Close()

	// ocean.jpg in the test archive is 1000x500.
	res, body := httpGet(t, ts.Client(), ts.URL+"/assets/images/ocean.jpg?thumb=256", nil)
	wantStatus(t, "zip thumbnail", http.StatusOK, res)
	wantEqual(t, "content type", "image/jpeg", res.Header.Get("Content-Type"))
	wantEqual(t, "bounds", image.Pt(256, 128), decodeBounds(t, body))
}

func TestNewThumbnailerRejectsNonPositiveBudget(t *testing.T) {
	t.Parallel()
	if _, err := newThumbnailer(t.Context(), thumbnailConfig{BudgetBytes: 0}); err == nil {
		t.Error("newThumbnailer accepted a zero budget")
	}
}
