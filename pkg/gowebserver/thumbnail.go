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
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // Registers the GIF decoder with the image package.
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cloudfra/ufs"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // Registers the WebP decoder with the image package.
)

const (
	// defaultThumbnailCacheMB is the default in-memory thumbnail cache budget.
	defaultThumbnailCacheMB = 256

	// maxThumbnailPixels is the largest source image (in pixels) that will be
	// decoded. Larger images are served as the original file.
	maxThumbnailPixels = 100_000_000

	// maxThumbnailSourceBytes is the largest source file that will be read into
	// memory for thumbnail generation.
	maxThumbnailSourceBytes = 128 << 20

	// maxThumbnailSkips bounds the memory used to remember sources that cannot
	// (or need not) be thumbnailed.
	maxThumbnailSkips = 8192

	thumbnailJPEGQuality  = 80
	thumbnailCacheControl = "public, max-age=86400"
)

// thumbnailSizes is the ladder of accepted ?thumb= values. Restricting the
// sizes keeps the cache key space bounded.
var thumbnailSizes = []int{256, 512, 1024}

// errThumbnailSkip means the source should be served as the original file.
var errThumbnailSkip = errors.New("thumbnail skipped")

func validThumbnailSize(size int) bool {
	for _, s := range thumbnailSizes {
		if s == size {
			return true
		}
	}
	return false
}

// thumbnailDecodable reports whether the file extension is a format the
// thumbnailer can decode. Everything else (HEIC, AVIF, SVG, ...) is served
// as the original.
func thumbnailDecodable(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	}
	return false
}

// thumbnailConfig configures thumbnail generation for a handler.
type thumbnailConfig struct {
	// BudgetBytes is the maximum number of bytes of encoded thumbnails held in
	// memory. Zero or less disables thumbnails.
	BudgetBytes int64
	// MeterProvider is used to publish thumbnail metrics. Nil uses a no-op meter.
	MeterProvider metric.MeterProvider
}

type thumbEntry struct {
	key         string
	size        int64
	contentType string
}

type thumbCall struct {
	done chan struct{}
}

// thumbnailer generates and caches resized copies of images. Encoded
// thumbnails are stored in a private in-memory ufs file system that is
// separate from the served content, and a byte-budget LRU evicts the least
// recently used thumbnails because the in-memory ufs has no size limit.
type thumbnailer struct {
	cache  ufs.FS
	budget int64
	sem    chan struct{}

	mu       sync.Mutex
	lru      *list.List // of *thumbEntry, front is most recently used
	entries  map[string]*list.Element
	used     int64
	inflight map[string]*thumbCall
	skip     map[string]struct{}

	requests  metric.Int64Counter
	evictions metric.Int64Counter
}

func newThumbnailer(ctx context.Context, conf thumbnailConfig) (*thumbnailer, error) {
	if conf.BudgetBytes <= 0 {
		return nil, fmt.Errorf("thumbnail cache budget must be positive, got %d", conf.BudgetBytes)
	}
	cache, err := ufs.New(ctx, "memory://")
	if err != nil {
		return nil, fmt.Errorf("cannot create thumbnail cache: %w", err)
	}
	t := &thumbnailer{
		cache:    cache,
		budget:   conf.BudgetBytes,
		sem:      make(chan struct{}, max(1, runtime.NumCPU())),
		lru:      list.New(),
		entries:  map[string]*list.Element{},
		inflight: map[string]*thumbCall{},
		skip:     map[string]struct{}{},
	}
	if err := t.setupMetrics(conf.MeterProvider); err != nil {
		return nil, errors.Join(err, cache.Close())
	}
	return t, nil
}

func (t *thumbnailer) setupMetrics(mp metric.MeterProvider) error {
	if mp == nil {
		mp = noop.NewMeterProvider()
	}
	m := mp.Meter("thumbnail")
	var err error
	t.requests, err = m.Int64Counter("thumbnail_requests_total", metric.WithDescription("Thumbnail requests by result (hit, miss, fallback)."))
	if err != nil {
		return err
	}
	t.evictions, err = m.Int64Counter("thumbnail_evictions_total", metric.WithDescription("Thumbnails evicted from the in-memory cache."))
	if err != nil {
		return err
	}
	_, err = m.Int64ObservableGauge("thumbnail_cache_bytes",
		metric.WithDescription("Bytes of encoded thumbnails held in memory."),
		metric.WithUnit("By"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(t.cacheBytes())
			return nil
		}))
	return err
}

func (t *thumbnailer) cacheBytes() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.used
}

// Close releases the thumbnail cache.
func (t *thumbnailer) Close() error {
	return t.cache.Close()
}

func (t *thumbnailer) count(ctx context.Context, result string) {
	t.requests.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
}

// serve writes a thumbnail of the image at name, whose longest edge is size.
// It reports whether it handled the request. When it returns false nothing was
// written and the caller should serve the original file.
func (t *thumbnailer) serve(w http.ResponseWriter, r *http.Request, srcFS fs.FS, name string, size int) bool {
	ctx := r.Context()
	if !thumbnailDecodable(name) {
		t.count(ctx, "fallback")
		return false
	}
	info, err := fs.Stat(srcFS, name)
	if err != nil || info.IsDir() {
		return false
	}
	sum := sha256.Sum256([]byte(name))
	key := fmt.Sprintf("t%d/%s-%d-%d", size, hex.EncodeToString(sum[:8]), info.ModTime().UnixNano(), info.Size())

	for range 3 {
		t.mu.Lock()
		if el, ok := t.entries[key]; ok {
			t.lru.MoveToFront(el)
			entry := el.Value.(*thumbEntry)
			t.mu.Unlock()
			data, err := t.cache.ReadFile(key)
			if err == nil {
				t.count(ctx, "hit")
				writeThumbnail(w, r, key, entry.contentType, info.ModTime(), data)
				return true
			}
			// Evicted between the lookup and the read; treat as a miss.
			t.mu.Lock()
			t.dropLocked(key)
			t.mu.Unlock()
			continue
		}
		if _, ok := t.skip[key]; ok {
			t.mu.Unlock()
			t.count(ctx, "fallback")
			return false
		}
		if call, ok := t.inflight[key]; ok {
			t.mu.Unlock()
			select {
			case <-call.done:
				continue
			case <-ctx.Done():
				return true
			}
		}
		call := &thumbCall{done: make(chan struct{})}
		t.inflight[key] = call
		t.mu.Unlock()

		data, contentType, err := t.generate(ctx, srcFS, name, size)
		if err == nil {
			err = t.store(key, contentType, data)
		}

		t.mu.Lock()
		delete(t.inflight, key)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			if len(t.skip) >= maxThumbnailSkips {
				t.skip = map[string]struct{}{}
			}
			t.skip[key] = struct{}{}
		}
		t.mu.Unlock()
		close(call.done)

		switch {
		case err == nil:
			t.count(ctx, "miss")
			writeThumbnail(w, r, key, contentType, info.ModTime(), data)
			return true
		case ctx.Err() != nil:
			return true
		default:
			if !errors.Is(err, errThumbnailSkip) {
				slog.Warn("thumbnail generation failed", "path", name, "size", size, "error", err)
			}
			t.count(ctx, "fallback")
			return false
		}
	}
	t.count(ctx, "fallback")
	return false
}

// writeThumbnail serves data with conditional-request and Range support. The
// modification time is the source file's, not the cache entry's, because the
// in-memory file system stamps entries with their creation time, which changes
// every time an entry is evicted and regenerated.
func writeThumbnail(w http.ResponseWriter, r *http.Request, key string, contentType string, modTime time.Time, data []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", thumbnailCacheControl)
	h.Set("ETag", `"`+strings.ReplaceAll(key, "/", "-")+`"`)
	http.ServeContent(w, r, "", modTime, bytes.NewReader(data))
}

// store writes data into the cache, registers it, and evicts least recently
// used entries until the cache is within budget.
func (t *thumbnailer) store(key string, contentType string, data []byte) error {
	dir := path.Dir(key)
	if err := t.cache.MkdirAll(dir, fs.ModePerm); err != nil {
		return err
	}
	f, err := t.cache.Create(key)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return errors.Join(err, f.Close(), t.removeFromCache(key))
	}
	if err := f.Close(); err != nil {
		return errors.Join(err, t.removeFromCache(key))
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.dropLocked(key)
	t.entries[key] = t.lru.PushFront(&thumbEntry{key: key, size: int64(len(data)), contentType: contentType})
	t.used += int64(len(data))
	for t.used > t.budget {
		oldest := t.lru.Back()
		if oldest == nil {
			break
		}
		t.dropLocked(oldest.Value.(*thumbEntry).key)
		t.evictions.Add(context.Background(), 1)
	}
	return nil
}

// removeFromCache deletes key from the backing file system. A key that is
// already gone is not an error.
func (t *thumbnailer) removeFromCache(key string) error {
	if err := t.cache.Remove(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// dropLocked removes key from the LRU and the cache. t.mu must be held.
func (t *thumbnailer) dropLocked(key string) {
	el, ok := t.entries[key]
	if !ok {
		return
	}
	t.used -= el.Value.(*thumbEntry).size
	t.lru.Remove(el)
	delete(t.entries, key)
	if err := t.removeFromCache(key); err != nil {
		slog.Warn("cannot remove thumbnail from cache", "key", key, "error", err)
	}
}

// generate decodes the source image and returns an encoded thumbnail whose
// longest edge is size. It returns an error wrapping errThumbnailSkip when the
// original should be served instead.
func (t *thumbnailer) generate(ctx context.Context, srcFS fs.FS, name string, size int) ([]byte, string, error) {
	select {
	case t.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	defer func() { <-t.sem }()

	f, err := srcFS.Open(name)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", errThumbnailSkip, err)
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxThumbnailSourceBytes+1))
	if closeErr := f.Close(); closeErr != nil {
		slog.Warn("cannot close thumbnail source", "path", name, "error", closeErr)
	}
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", errThumbnailSkip, err)
	}
	if len(raw) > maxThumbnailSourceBytes {
		return nil, "", fmt.Errorf("%w: source larger than %d bytes", errThumbnailSkip, maxThumbnailSourceBytes)
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", errThumbnailSkip, err)
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxThumbnailPixels {
		return nil, "", fmt.Errorf("%w: %dx%d exceeds pixel limit", errThumbnailSkip, cfg.Width, cfg.Height)
	}
	if max(cfg.Width, cfg.Height) <= size {
		return nil, "", fmt.Errorf("%w: already smaller than %d", errThumbnailSkip, size)
	}

	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", errThumbnailSkip, err)
	}
	thumb := resizeTo(img, size)
	if format == "jpeg" {
		thumb = orient(thumb, jpegOrientation(raw))
	}

	var buf bytes.Buffer
	contentType := "image/jpeg"
	if thumb.Opaque() {
		err = jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: thumbnailJPEGQuality})
	} else {
		contentType = "image/png"
		err = png.Encode(&buf, thumb)
	}
	if err != nil {
		return nil, "", fmt.Errorf("cannot encode thumbnail: %w", err)
	}
	return buf.Bytes(), contentType, nil
}

// resizeTo scales src so its longest edge is maxEdge, preserving aspect ratio.
func resizeTo(src image.Image, maxEdge int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	nw, nh := maxEdge, max(1, h*maxEdge/w)
	if h > w {
		nw, nh = max(1, w*maxEdge/h), maxEdge
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.BiLinear.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	return dst
}

// orient applies an EXIF orientation (1-8) to img. Go's decoders ignore EXIF
// orientation while browsers honor it for the originals, so without this
// phone photos would appear sideways in thumbnails.
func orient(src *image.RGBA, orientation int) *image.RGBA {
	if orientation < 2 || orientation > 8 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := w, h
	if orientation >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		for x := range dw {
			var sx, sy int
			switch orientation {
			case 2: // mirror horizontal
				sx, sy = w-1-x, y
			case 3: // rotate 180
				sx, sy = w-1-x, h-1-y
			case 4: // mirror vertical
				sx, sy = x, h-1-y
			case 5: // transpose
				sx, sy = y, x
			case 6: // rotate 90 clockwise
				sx, sy = y, h-1-x
			case 7: // transverse
				sx, sy = w-1-y, h-1-x
			case 8: // rotate 90 counter-clockwise
				sx, sy = w-1-y, x
			}
			si := src.PixOffset(sx, sy)
			di := dst.PixOffset(x, y)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}

// jpegOrientation returns the EXIF orientation (1-8) of a JPEG file, or 1
// when there is none.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		switch {
		case marker == 0xFF: // fill byte
			i++
			continue
		case marker == 0x01 || (marker >= 0xD0 && marker <= 0xD8): // standalone markers
			i += 2
			continue
		case marker == 0xD9 || marker == 0xDA: // end of image / start of scan
			return 1
		}
		segLen := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if segLen < 2 || i+2+segLen > len(data) {
			return 1
		}
		if marker == 0xE1 {
			if o := exifOrientation(data[i+4 : i+2+segLen]); o != 0 {
				return o
			}
		}
		i += 2 + segLen
	}
	return 1
}

// exifOrientation reads the orientation tag from an APP1 Exif segment payload.
// It returns 0 when the segment has no valid orientation.
func exifOrientation(seg []byte) int {
	if len(seg) < 14 || string(seg[:6]) != "Exif\x00\x00" {
		return 0
	}
	tiff := seg[6:]
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	if bo.Uint16(tiff[2:4]) != 42 {
		return 0
	}
	ifd := int(bo.Uint32(tiff[4:8]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 0
	}
	n := int(bo.Uint16(tiff[ifd : ifd+2]))
	for k := range n {
		off := ifd + 2 + k*12
		if off+12 > len(tiff) {
			return 0
		}
		if bo.Uint16(tiff[off:off+2]) == 0x0112 {
			if v := int(bo.Uint16(tiff[off+8 : off+10])); v >= 1 && v <= 8 {
				return v
			}
			return 0
		}
	}
	return 0
}
