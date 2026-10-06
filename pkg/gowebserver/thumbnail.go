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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
	"github.com/cloudfra/ufs"

	// Registers the bolt: filesystem that stores thumbnails.
	_ "github.com/cloudfra/ufs/drivers/boltfs"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/image/draw"

	// Decoders for image.Decode.
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// Thumbnails are requested by adding query parameters to an image or video
// URL:
//
//	width, height  Size in pixels, 1 to thumbnailMaxSize. With only one of
//	               them the other follows the file's aspect ratio.
//	fit            How the file fills a width x height box:
//	                 cover    fill the box, cropping what overflows (default)
//	                 contain  fit inside the box, padding with background
//	                 fill     stretch to the box
//	                 inside   fit inside the box at the file's ratio; the
//	                          result can be smaller than the box
//	format         jpeg, png or gif. Defaults to png for images with
//	               transparency and jpeg otherwise.
//	background     Padding color for fit=contain: RRGGBB, RRGGBBAA or
//	               "transparent". Defaults to transparent for png and gif
//	               and black for jpeg.
//	quality        JPEG quality, 1 to 100 (default 82).
//
// Without width and height the file is served as usual. Files that can't
// be thumbnailed (formats Go can't decode, or videos when ffmpeg isn't
// available) are also served as usual for images; videos get 415 so an
// <img> fails fast and the page can fall back.
const (
	thumbnailMaxSize      = 4096
	thumbnailMaxPixels    = 100_000_000
	thumbnailMaxFileBytes = 256 << 20
	thumbnailJPEGQuality  = 82
	thumbnailVideoTimeout = 30 * time.Second
	// thumbnailMaxVideoCopy bounds the temporary copy made of a video in an
	// archive that ffmpeg can't read through a pipe.
	thumbnailMaxVideoCopy = 1 << 30
)

var errThumbnailUnsupported = errors.New("thumbnail: unsupported file")

type thumbnailFit string

const (
	fitCover   thumbnailFit = "cover"
	fitContain thumbnailFit = "contain"
	fitFill    thumbnailFit = "fill"
	fitInside  thumbnailFit = "inside"
	// fitOutside covers the box without cropping: the file's ratio, just
	// big enough that both sides reach the box, never enlarged. A grid
	// tile crops it with CSS and still knows the file's shape.
	fitOutside thumbnailFit = "outside"
)

// thumbnailSpec is a parsed thumbnail request.
type thumbnailSpec struct {
	width, height int
	fit           thumbnailFit
	format        string // "" picks jpeg or png from the image
	background    *color.NRGBA
	quality       int
}

// parseThumbnailSpec reads the thumbnail parameters from q. ok is false
// when the request isn't for a thumbnail (no width or height).
func parseThumbnailSpec(q url.Values) (spec *thumbnailSpec, ok bool, err error) {
	if !q.Has("width") && !q.Has("height") {
		return nil, false, nil
	}
	spec = &thumbnailSpec{fit: fitCover, quality: thumbnailJPEGQuality}
	size := func(name string) (int, error) {
		v := q.Get(name)
		if v == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > thumbnailMaxSize {
			return 0, fmt.Errorf("%s must be an integer from 1 to %d", name, thumbnailMaxSize)
		}
		return n, nil
	}
	if spec.width, err = size("width"); err != nil {
		return nil, true, err
	}
	if spec.height, err = size("height"); err != nil {
		return nil, true, err
	}
	if spec.width == 0 && spec.height == 0 {
		return nil, true, errors.New("width or height must be set")
	}
	if v := q.Get("fit"); v != "" {
		switch f := thumbnailFit(strings.ToLower(v)); f {
		case fitCover, fitContain, fitFill, fitInside, fitOutside:
			spec.fit = f
		default:
			return nil, true, fmt.Errorf("fit must be one of cover, contain, fill, inside, outside")
		}
	}
	switch v := strings.ToLower(q.Get("format")); v {
	case "":
	case "jpeg", "jpg":
		spec.format = "jpeg"
	case "png", "gif":
		spec.format = v
	default:
		return nil, true, fmt.Errorf("format must be one of jpeg, png, gif")
	}
	if v := q.Get("background"); v != "" {
		c, err := parseHexColor(v)
		if err != nil {
			return nil, true, err
		}
		spec.background = &c
	}
	if v := q.Get("quality"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			return nil, true, errors.New("quality must be an integer from 1 to 100")
		}
		spec.quality = n
	}
	return spec, true, nil
}

// key identifies the output of spec. It's also the thumbnail's file name
// in the store, e.g. "512x512-cover", "512x0-cover-png" or
// "200x200-contain-bgffffffff-q90". A 0 is a dimension left to the ratio.
func (s *thumbnailSpec) key() string {
	k := fmt.Sprintf("%dx%d-%s", s.width, s.height, s.fit)
	if s.format != "" {
		k += "-" + s.format
	}
	if s.background != nil {
		k += fmt.Sprintf("-bg%02x%02x%02x%02x", s.background.R, s.background.G, s.background.B, s.background.A)
	}
	if s.quality != thumbnailJPEGQuality {
		k += fmt.Sprintf("-q%d", s.quality)
	}
	return k
}

func parseHexColor(v string) (color.NRGBA, error) {
	if strings.EqualFold(v, "transparent") {
		return color.NRGBA{}, nil
	}
	h := strings.TrimPrefix(v, "#")
	if len(h) == 6 {
		h += "ff"
	}
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 4 {
		return color.NRGBA{}, errors.New("background must be RRGGBB, RRGGBBAA or transparent")
	}
	return color.NRGBA{R: b[0], G: b[1], B: b[2], A: b[3]}, nil
}

// thumbnailer makes and caches thumbnails. One is shared by every served
// filesystem.
type thumbnailer struct {
	store  *thumbnailStore
	ffmpeg *ffmpeg.FFmpeg // nil when video thumbnails are unavailable
	// Decoding a large photo takes a lot of memory, so at most one
	// thumbnail per CPU is made at a time.
	sem chan struct{}
	tp  trace.TracerProvider

	mu      sync.Mutex
	renders map[string]*render // by store key, while being made
}

// render is a thumbnail being made, shared by every request waiting for
// it. It's cancelled when they have all gone, such as when the grid drops
// tiles that were scrolled past, so it stops using a render slot or ffmpeg.
type render struct {
	done      chan struct{}
	th        *thumbnail // set before done closes
	err       error
	waiters   int  // guarded by thumbnailer.mu
	abandoned bool // guarded by thumbnailer.mu; cancelled, don't join
	cancel    context.CancelFunc
}

// shared returns the thumbnail at key, made by mk, which is shared with
// any other request for the same key, and stores it. It returns ctx's
// error if ctx ends first.
func (t *thumbnailer) shared(ctx context.Context, key string, mk func(context.Context) (*thumbnail, error)) (*thumbnail, error) {
	t.mu.Lock()
	r := t.renders[key]
	if r == nil || r.abandoned {
		rctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		r = &render{done: make(chan struct{}), cancel: cancel}
		t.renders[key] = r
		go t.run(rctx, key, r, mk)
	}
	r.waiters++
	t.mu.Unlock()

	select {
	case <-r.done:
		return r.th, r.err
	case <-ctx.Done():
		t.mu.Lock()
		r.waiters--
		if r.waiters == 0 {
			select {
			case <-r.done:
			default:
				r.abandoned = true
				r.cancel()
			}
		}
		t.mu.Unlock()
		return nil, ctx.Err()
	}
}

// run makes and stores r's thumbnail, then hands it to the waiting
// requests.
func (t *thumbnailer) run(ctx context.Context, key string, r *render, mk func(context.Context) (*thumbnail, error)) {
	defer r.cancel()
	r.th, r.err = mk(ctx)
	if r.err == nil {
		if err := t.store.put(key, r.th); err != nil {
			slog.Warn("failed to store thumbnail", "path", key, "error", err)
		}
	}
	close(r.done)
	t.mu.Lock()
	if t.renders[key] == r {
		delete(t.renders, key)
	}
	t.mu.Unlock()
}

// thumbnailCacheMemory is the Thumbnails.CachePath that keeps thumbnails
// in memory only.
const thumbnailCacheMemory = "memory:"

// newThumbnailer makes thumbnails and keeps them in the bolt database at
// conf.CachePath (see Thumbnails), or in memory when that's
// thumbnailCacheMemory or can't be opened.
func newThumbnailer(conf Thumbnails, ff *ffmpeg.FFmpeg, tp trace.TracerProvider) (*thumbnailer, error) {
	if !conf.Enabled {
		return nil, nil
	}
	if ff == nil {
		slog.Info("video thumbnails are disabled because ffmpeg is unavailable")
	}
	cachePath := conf.CachePath
	switch cachePath {
	case "":
		cachePath = defaultThumbnailCachePath()
	case thumbnailCacheMemory:
		cachePath = ""
	}
	store, err := newThumbnailStore(cachePath)
	if err != nil {
		return nil, err
	}
	return &thumbnailer{
		store:   store,
		ffmpeg:  ff,
		sem:     make(chan struct{}, runtime.NumCPU()),
		tp:      tp,
		renders: map[string]*render{},
	}, nil
}

// close releases the thumbnail store. It's safe on a nil thumbnailer.
func (t *thumbnailer) close() error {
	if t == nil {
		return nil
	}
	return t.store.close()
}

// videos reports whether video thumbnails are available.
func (t *thumbnailer) videos() bool {
	return t != nil && t.ffmpeg != nil
}

// handler serves thumbnails for files in fsys and passes every other
// request to base. source names what fsys serves (its ufs URI): the store
// is shared by every served filesystem and outlives the server, so each
// source's thumbnails are kept apart.
func (t *thumbnailer) handler(base http.Handler, fsys fs.FS, source string) http.Handler {
	sum := sha256.Sum256([]byte(source))
	return &thumbnailHandler{t: t, base: base, fsys: fsys, source: hex.EncodeToString(sum[:8])}
}

type thumbnailHandler struct {
	t      *thumbnailer
	base   http.Handler
	fsys   fs.FS
	source string // hash of the served filesystem's URI
}

// thumbnail is a generated thumbnail, or a failed one (data nil).
type thumbnail struct {
	data        []byte
	contentType string
}

func (h *thumbnailHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	spec, ok, err := parseThumbnailSpec(r.URL.Query())
	if !ok {
		h.base.ServeHTTP(w, r)
		return
	}
	name := cleanPath(strings.TrimPrefix(r.URL.Path, "/"))
	video := isVideo(name)
	if !video && !isImage(name) {
		h.base.ServeHTTP(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	info, err := fs.Stat(h.fsys, name)
	if err != nil || info.IsDir() {
		h.base.ServeHTTP(w, r)
		return
	}

	ctx, span := h.t.tp.Tracer("thumbnail").Start(r.Context(), "thumbnail")
	defer span.End()
	span.SetAttributes(attribute.String("path", name), attribute.String("spec", spec.key()))

	key := path.Join(h.source, thumbnailPath(name, info, spec))
	sum := sha256.Sum256([]byte(key))
	etag := `"` + hex.EncodeToString(sum[:12]) + `"`

	th, cached := h.t.store.get(key)
	span.SetAttributes(attribute.Bool("cached", cached))
	if !cached {
		var err error
		th, err = h.t.shared(ctx, key, func(ctx context.Context) (*thumbnail, error) {
			return h.make(ctx, name, video, spec)
		})
		switch {
		case err == nil:
		case r.Context().Err() != nil:
			return // the client went away
		case errors.Is(err, errThumbnailUnsupported):
			th = &thumbnail{}
		default:
			slog.Warn("thumbnail failed", "path", name, "error", err)
			th = &thumbnail{}
		}
	}

	if th.data == nil {
		if video {
			http.Error(w, "cannot make a thumbnail of this video", http.StatusUnsupportedMediaType)
			return
		}
		// The browser may still be able to show the original (e.g. SVG).
		h.base.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", th.contentType)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, "", info.ModTime(), bytes.NewReader(th.data))
}

// thumbnailPath is where the thumbnail of the file name (with info) for
// spec lives in the store. The version, from the file's size and
// modification time, gives an edited file a new thumbnail and ETag.
func thumbnailPath(name string, info fs.FileInfo, spec *thumbnailSpec) string {
	v := sha256.Sum256([]byte(fmt.Sprintf("%d/%d", info.Size(), info.ModTime().UnixNano())))
	return path.Join(name, spec.key()+"@"+hex.EncodeToString(v[:6]))
}

// make decodes the file (or a frame of the video) and renders spec.
func (h *thumbnailHandler) make(ctx context.Context, name string, video bool, spec *thumbnailSpec) (*thumbnail, error) {
	select {
	case h.t.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-h.t.sem }()

	var (
		src         image.Image
		orientation = 1
		err         error
	)
	if video {
		src, err = h.videoFrame(ctx, name)
	} else {
		src, orientation, err = h.decodeImage(name)
	}
	if err != nil {
		return nil, err
	}
	// Decoding can't be interrupted, but scaling can be skipped.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Without a format, the file's own transparency decides: png keeps it,
	// jpeg is smaller for everything else. It's decided before rendering
	// so contain's padding is black in a jpeg, not transparent in a png.
	if spec.format == "" {
		s := *spec
		s.format = "jpeg"
		if o, ok := src.(interface{ Opaque() bool }); ok && !o.Opaque() {
			s.format = "png"
		}
		spec = &s
	}
	out := renderThumbnail(src, orientation, spec)
	return encodeThumbnail(out, spec)
}

func (h *thumbnailHandler) decodeImage(name string) (image.Image, int, error) {
	f, err := h.fsys.Open(name)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Error("failed to close image after thumbnailing", "path", name, "error", err)
		}
	}()
	data, err := io.ReadAll(io.LimitReader(f, thumbnailMaxFileBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if len(data) > thumbnailMaxFileBytes {
		return nil, 0, errThumbnailUnsupported
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width*cfg.Height > thumbnailMaxPixels {
		return nil, 0, errThumbnailUnsupported
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, errThumbnailUnsupported
	}
	orientation := 1
	if format == "jpeg" {
		orientation = jpegOrientation(data)
	}
	return img, orientation, nil
}

// videoFrame grabs a frame 10% of the way through the video (or the first
// frame, for very short videos) with ffmpeg, which also applies the
// video's rotation. Files on local disk are passed by path so ffmpeg can seek.
// Others, inside archives, are piped in, which works for streamable
// containers; when it doesn't (an MP4 with its index at the end, the
// usual layout), the file is copied to a temporary file up to
// thumbnailMaxVideoCopy bytes.
func (h *thumbnailHandler) videoFrame(ctx context.Context, name string) (image.Image, error) {
	if h.t.ffmpeg == nil {
		return nil, errThumbnailUnsupported
	}
	f, err := h.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	info, statErr := f.Stat()
	osf, local := f.(*os.File)
	if err := f.Close(); err != nil {
		slog.Error("failed to close video after thumbnailing", "path", name, "error", err)
	}
	if local {
		return h.ffmpegFrames(ctx, osf.Name(), "")
	}

	img, err := h.ffmpegFrames(ctx, "pipe:0", name)
	if err == nil || ctx.Err() != nil {
		return img, err
	}
	if statErr != nil || info.Size() > thumbnailMaxVideoCopy {
		return nil, errThumbnailUnsupported
	}
	tmp, err := h.copyToTemp(name)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := os.Remove(tmp); err != nil {
			slog.Error("failed to remove temporary video copy", "path", tmp, "error", err)
		}
	}()
	return h.ffmpegFrames(ctx, tmp, "")
}

// copyToTemp copies the file name to a new temporary file and returns its
// path.
func (h *thumbnailHandler) copyToTemp(name string) (string, error) {
	src, err := h.fsys.Open(name)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := src.Close(); err != nil {
			slog.Error("failed to close video after copying", "path", name, "error", err)
		}
	}()
	tmp, err := os.CreateTemp("", "gowebserver-video-*"+path.Ext(name))
	if err != nil {
		return "", err
	}
	_, err = io.Copy(tmp, src)
	err = errors.Join(err, tmp.Close())
	if err != nil {
		return "", errors.Join(err, os.Remove(tmp.Name()))
	}
	return tmp.Name(), nil
}

// ffmpegFrames returns the frame 10% of the way through the video, or
// the first frame when that fails (a clip too short for ffmpeg to seek
// in). When pipe is set, input is "pipe:0" and the file pipe (in h.fsys)
// is opened afresh for each run of ffmpeg and fed to its stdin.
func (h *thumbnailHandler) ffmpegFrames(ctx context.Context, input, pipe string) (image.Image, error) {
	var times []time.Duration
	var dur time.Duration
	err := h.withVideo(ctx, pipe, func(ctx context.Context, stdin io.Reader) (err error) {
		dur, err = h.t.ffmpeg.Duration(ctx, input, stdin)
		return err
	})
	switch {
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case err == nil:
		times = []time.Duration{dur / 10, 0}
	case pipe != "":
		// Unknown through a pipe usually means ffmpeg can't read the file
		// that way at all (an MP4 with its index at the end), so make one
		// attempt, not several, before the caller copies the file.
		times = []time.Duration{0}
	default:
		times = []time.Duration{time.Second, 0}
	}
	var last error
	for _, at := range times {
		var img image.Image
		err := h.withVideo(ctx, pipe, func(ctx context.Context, stdin io.Reader) (err error) {
			img, err = h.t.ffmpeg.Frame(ctx, input, stdin, at)
			return err
		})
		if err == nil {
			return img, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		last = err
	}
	slog.Debug("ffmpeg could not read a frame", "input", input, "error", last)
	return nil, errThumbnailUnsupported
}

// withVideo runs one ffmpeg call, fn, with a timeout, and with the file
// pipe opened as its stdin when pipe is set.
func (h *thumbnailHandler) withVideo(ctx context.Context, pipe string, fn func(context.Context, io.Reader) error) error {
	ctx, cancel := context.WithTimeout(ctx, thumbnailVideoTimeout)
	defer cancel()
	if pipe == "" {
		return fn(ctx, nil)
	}
	f, err := h.fsys.Open(pipe)
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Error("failed to close video after thumbnailing", "path", pipe, "error", err)
		}
	}()
	return fn(ctx, f)
}

// renderThumbnail scales src (whose EXIF orientation is orientation) to
// spec. The scaling happens in the file's own orientation and only the
// small result is rotated.
func renderThumbnail(src image.Image, orientation int, spec *thumbnailSpec) *image.NRGBA {
	b := src.Bounds()
	// The image as displayed ("logical") is transposed for orientations
	// 5 to 8.
	transposed := orientation >= 5 && orientation <= 8
	lw, lh := float64(b.Dx()), float64(b.Dy())
	if transposed {
		lw, lh = lh, lw
	}

	bw, bh := float64(spec.width), float64(spec.height)
	var sx, sy float64
	switch {
	case spec.width == 0:
		sx = math.Min(1, bh/lh)
		sy = sx
	case spec.height == 0:
		sx = math.Min(1, bw/lw)
		sy = sx
	case spec.fit == fitFill:
		sx, sy = bw/lw, bh/lh
	case spec.fit == fitCover:
		sx = math.Max(bw/lw, bh/lh)
		sy = sx
	case spec.fit == fitContain:
		sx = math.Min(bw/lw, bh/lh)
		sy = sx
	case spec.fit == fitOutside: // never larger than the file
		sx = math.Min(1, math.Max(bw/lw, bh/lh))
		sy = sx
	default: // fitInside: never larger than the file.
		sx = math.Min(1, math.Min(bw/lw, bh/lh))
		sy = sx
	}
	tw := max(1, int(math.Round(lw*sx)))
	th := max(1, int(math.Round(lh*sy)))

	// Scale in the file's orientation.
	stw, sth := tw, th
	if transposed {
		stw, sth = th, tw
	}
	scaled := scaleImage(src, stw, sth)
	oriented := orient(scaled, orientation)

	// Crop (cover) or pad (contain) to the requested box. Single-dimension,
	// fill, inside and outside results are already the right size.
	if spec.width == 0 || spec.height == 0 || spec.fit == fitFill || spec.fit == fitInside || spec.fit == fitOutside {
		return oriented
	}
	out := image.NewNRGBA(image.Rect(0, 0, spec.width, spec.height))
	if spec.fit == fitContain {
		bg := color.NRGBA{}
		if spec.background != nil {
			bg = *spec.background
		} else if spec.format == "jpeg" {
			bg = color.NRGBA{A: 255}
		}
		stddraw.Draw(out, out.Bounds(), &image.Uniform{C: bg}, image.Point{}, stddraw.Src)
	}
	ox := (spec.width - oriented.Bounds().Dx()) / 2
	oy := (spec.height - oriented.Bounds().Dy()) / 2
	stddraw.Draw(out, oriented.Bounds().Add(image.Pt(ox, oy)), oriented, image.Point{}, stddraw.Over)
	return out
}

// scaleImage resizes src to w x h. Catmull-Rom gives the sharpest result
// but costs a lot on a large photo, so big reductions of a JPEG (or other
// *image.YCbCr) first shrink by a whole factor on every core, averaging
// blocks, to between two and four times the target, and big reductions of
// other images go bilinear to twice the target; Catmull-Rom does the rest.
//
// Scaling writes to *image.RGBA because x/image/draw has fast paths for it;
// into *image.NRGBA every pixel goes through the slow generic path.
func scaleImage(src image.Image, w, h int) *image.NRGBA {
	b := src.Bounds()
	if m, ok := src.(*image.YCbCr); ok {
		// Leave Catmull-Rom a 2-4x reduction for sharpness, or for 2-4x
		// overall (a slideshow's screen-sized version), shrink by 2 and let
		// it do the last 1-2x on a quarter of the pixels.
		ratio := min(float64(b.Dx())/float64(w), float64(b.Dy())/float64(h))
		k := int(ratio / 2)
		if ratio >= 2 {
			k = max(k, 2)
		}
		if k >= 2 {
			src = shrinkYCbCr(m, k)
			b = src.Bounds()
		}
	}
	if b.Dx() > 4*w && b.Dy() > 4*h {
		mid := image.NewRGBA(image.Rect(0, 0, 2*w, 2*h))
		draw.BiLinear.Scale(mid, mid.Bounds(), src, b, draw.Src, nil)
		src, b = mid, mid.Bounds()
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(out, out.Bounds(), src, b, draw.Src, nil)
	return toNRGBA(out)
}

// minShrinkRows is the fewest rows of output a goroutine shrinks; smaller
// bands cost more to coordinate than they save.
const minShrinkRows = 16

// shrinkYCbCr shrinks m by the factor k in each direction, each output
// pixel the average of a k x k block (smaller at the right and bottom
// edges), with rows split across every core. Y, Cb and Cr are averaged and
// converted to RGB once per output pixel: the conversion is linear (up to
// clamping), so that matches averaging RGB, at a fraction of the cost.
func shrinkYCbCr(m *image.YCbCr, k int) *image.RGBA {
	r := m.Rect
	w, h := (r.Dx()+k-1)/k, (r.Dy()+k-1)/k
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	cx, cy := chromaShift(m.SubsampleRatio)
	n := max(1, min(runtime.GOMAXPROCS(0), h/minShrinkRows))
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(y0, y1 int) {
			defer wg.Done()
			for oy := y0; oy < y1; oy++ {
				ya := r.Min.Y + oy*k
				yb := min(ya+k, r.Max.Y)
				px := out.Pix[oy*out.Stride:]
				for ox := range w {
					xa := r.Min.X + ox*k
					xb := min(xa+k, r.Max.X)
					var ys, cbs, crs uint32
					for y := ya; y < yb; y++ {
						off := (y-r.Min.Y)*m.YStride - r.Min.X
						for _, v := range m.Y[off+xa : off+xb] {
							ys += uint32(v)
						}
					}
					// Chroma samples covering the block, which with
					// subsampling cover fewer, larger areas.
					var cn uint32
					for y := ya >> cy; y <= (yb-1)>>cy; y++ {
						off := (y-r.Min.Y>>cy)*m.CStride - r.Min.X>>cx
						for x := xa >> cx; x <= (xb-1)>>cx; x++ {
							cbs += uint32(m.Cb[off+x])
							crs += uint32(m.Cr[off+x])
							cn++
						}
					}
					yn := uint32((yb - ya) * (xb - xa))
					cr, cg, cb := color.YCbCrToRGB(uint8((ys+yn/2)/yn), uint8((cbs+cn/2)/cn), uint8((crs+cn/2)/cn))
					px[ox*4], px[ox*4+1], px[ox*4+2], px[ox*4+3] = cr, cg, cb, 0xff
				}
			}
		}(h*i/n, h*(i+1)/n)
	}
	wg.Wait()
	return out
}

// chromaShift returns how many bits a pixel's x and y shift right to find
// its chroma sample, for a subsample ratio.
func chromaShift(ratio image.YCbCrSubsampleRatio) (int, int) {
	switch ratio {
	case image.YCbCrSubsampleRatio422:
		return 1, 0
	case image.YCbCrSubsampleRatio420:
		return 1, 1
	case image.YCbCrSubsampleRatio440:
		return 0, 1
	case image.YCbCrSubsampleRatio411:
		return 2, 0
	case image.YCbCrSubsampleRatio410:
		return 2, 1
	}
	return 0, 0
}

// toNRGBA converts m, which is alpha-premultiplied, to non-premultiplied
// color in place. Opaque pixels, all of them for a photo, are the same in
// both.
func toNRGBA(m *image.RGBA) *image.NRGBA {
	p := m.Pix
	for i := 0; i+3 < len(p); i += 4 {
		if a := uint32(p[i+3]); a != 0 && a != 0xff {
			p[i] = uint8(min(0xff, (uint32(p[i])*0xff+a/2)/a))
			p[i+1] = uint8(min(0xff, (uint32(p[i+1])*0xff+a/2)/a))
			p[i+2] = uint8(min(0xff, (uint32(p[i+2])*0xff+a/2)/a))
		}
	}
	return &image.NRGBA{Pix: p, Stride: m.Stride, Rect: m.Rect}
}

// orient applies an EXIF orientation (1 to 8) to img.
func orient(img *image.NRGBA, orientation int) *image.NRGBA {
	if orientation < 2 || orientation > 8 {
		return img
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	ow, oh := w, h
	if orientation >= 5 {
		ow, oh = h, w
	}
	out := image.NewNRGBA(image.Rect(0, 0, ow, oh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch orientation {
			case 2: // mirrored
				dx, dy = w-1-x, y
			case 3: // rotated 180
				dx, dy = w-1-x, h-1-y
			case 4: // mirrored vertically
				dx, dy = x, h-1-y
			case 5: // transposed
				dx, dy = y, x
			case 6: // rotated 90 clockwise
				dx, dy = h-1-y, x
			case 7: // transversed
				dx, dy = h-1-y, w-1-x
			case 8: // rotated 90 counter-clockwise
				dx, dy = y, w-1-x
			}
			si := img.PixOffset(x, y)
			di := out.PixOffset(dx, dy)
			copy(out.Pix[di:di+4], img.Pix[si:si+4])
		}
	}
	return out
}

// encodeThumbnail encodes img in spec's format, which make has resolved.
func encodeThumbnail(img *image.NRGBA, spec *thumbnailSpec) (*thumbnail, error) {
	format := spec.format
	var buf bytes.Buffer
	var err error
	switch format {
	case "png":
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img)
	case "gif":
		err = gif.Encode(&buf, img, nil)
	default:
		format = "jpeg"
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: spec.quality})
	}
	if err != nil {
		return nil, err
	}
	return &thumbnail{data: buf.Bytes(), contentType: "image/" + format}, nil
}

// jpegOrientation returns the EXIF orientation (1 to 8) of a JPEG file, or
// 1 when there is none. Phones store photos sideways and record how to
// turn them in this tag; browsers apply it to the original file, so
// thumbnails have to as well.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		if marker == 0xDA || marker == 0xD9 { // start of scan, end of image
			return 1
		}
		size := int(data[i+2])<<8 | int(data[i+3])
		if size < 2 || i+2+size > len(data) {
			return 1
		}
		seg := data[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) > 6 && string(seg[:6]) == "Exif\x00\x00" {
			return exifOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

// exifOrientation reads the orientation tag from the first IFD of a TIFF
// structure.
func exifOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var u16 func([]byte) int
	var u32 func([]byte) int
	switch string(t[:2]) {
	case "II":
		u16 = func(b []byte) int { return int(b[0]) | int(b[1])<<8 }
		u32 = func(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 | int(b[3])<<24 }
	case "MM":
		u16 = func(b []byte) int { return int(b[0])<<8 | int(b[1]) }
		u32 = func(b []byte) int { return int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3]) }
	default:
		return 1
	}
	ifd := u32(t[4:8])
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	n := u16(t[ifd : ifd+2])
	for k := 0; k < n; k++ {
		e := ifd + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if u16(t[e:e+2]) == 0x0112 { // Orientation, a SHORT
			if o := u16(t[e+8 : e+10]); o >= 1 && o <= 8 {
				return o
			}
			return 1
		}
	}
	return 1
}

// thumbnailStore keeps generated thumbnails as files in a ufs filesystem:
// a BoltDB file ("bolt:"), so they survive restarts, or memory ("memory:")
// when there's no database. Files are laid out beside the path of the file
// each was made from, under a directory for the served filesystem: the
// 512x512 cover thumbnail of photos/a.jpg is stored as
// <source>/photos/a.jpg/512x512-cover@<version>, where the version changes
// whenever the source file's size or modification time does. Requests
// load from there first and only decode the source when it isn't stored.
//
// Storing a new version removes the older versions' thumbnails of the same
// size. Nothing else is evicted: the store grows with the number of
// distinct thumbnails made.
type thumbnailStore struct {
	fsys ufs.WriteFS
}

// defaultThumbnailCachePath is where thumbnails are kept between runs.
func defaultThumbnailCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "gowebserver", "thumbnails.db")
}

// newThumbnailStore opens the bolt database at dbPath, creating it if
// needed. When dbPath is empty or can't be opened (for example because
// another server holds its lock), thumbnails are kept in memory instead.
func newThumbnailStore(dbPath string) (*thumbnailStore, error) {
	if dbPath != "" {
		s, err := openBoltThumbnailStore(dbPath)
		if err == nil {
			slog.Info("thumbnails are cached", "path", dbPath)
			return s, nil
		}
		slog.Warn("cannot open the thumbnail cache; keeping thumbnails in memory", "path", dbPath, "error", err)
	}
	return openThumbnailStore(thumbnailCacheMemory)
}

// openBoltThumbnailStore opens (creating if needed) the bolt database at
// dbPath.
func openBoltThumbnailStore(dbPath string) (*thumbnailStore, error) {
	// ufs parses the path as part of a URI, so a # or ? would cut it short
	// and the database would silently land somewhere else.
	if strings.ContainsAny(dbPath, "#?") {
		return nil, errors.New("the path can't contain # or ?")
	}
	dbPath, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	s, err := openThumbnailStore("bolt:" + dbPath)
	if err != nil {
		return nil, err
	}
	// Make sure the database is where it was asked to be.
	if _, err := os.Stat(dbPath); err != nil {
		return nil, errors.Join(fmt.Errorf("the database wasn't created at %s: %w", dbPath, err), s.close())
	}
	return s, nil
}

func openThumbnailStore(uri string) (*thumbnailStore, error) {
	fsys, err := ufs.New(context.Background(), uri)
	if err != nil {
		return nil, err
	}
	wfs, ok := fsys.(ufs.WriteFS)
	if !ok {
		return nil, errors.Join(fmt.Errorf("%s is not writable", uri), fsys.Close())
	}
	return &thumbnailStore{fsys: wfs}, nil
}

// get returns the thumbnail stored at name.
func (s *thumbnailStore) get(name string) (*thumbnail, bool) {
	data, err := s.fsys.ReadFile(name)
	if err != nil {
		return nil, false
	}
	return &thumbnail{data: data, contentType: http.DetectContentType(data)}, true
}

// put stores th at name, a path from thumbnailPath, and removes the
// thumbnails of the same size made from older versions of the file.
func (s *thumbnailStore) put(name string, th *thumbnail) error {
	dir, base := path.Dir(name), path.Base(name)
	if err := s.fsys.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := s.fsys.Create(name)
	if err != nil {
		return err
	}
	if _, err := f.Write(th.data); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return err
	}
	spec, _, _ := strings.Cut(base, "@")
	entries, err := fs.ReadDir(s.fsys, dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if n := e.Name(); n != base && strings.HasPrefix(n, spec+"@") {
			if err := s.fsys.Remove(path.Join(dir, n)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *thumbnailStore) close() error {
	return s.fsys.Close()
}
