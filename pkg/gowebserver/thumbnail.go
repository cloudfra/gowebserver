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
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
	"github.com/cloudfra/ufs"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/image/draw"
	"golang.org/x/sync/singleflight"

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
		case fitCover, fitContain, fitFill, fitInside:
			spec.fit = f
		default:
			return nil, true, fmt.Errorf("fit must be one of cover, contain, fill, inside")
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
	sem   chan struct{}
	group singleflight.Group
	tp    trace.TracerProvider
}

func newThumbnailer(conf Thumbnails, tp trace.TracerProvider) (*thumbnailer, error) {
	if !conf.Enabled {
		return nil, nil
	}
	// An explicit path, else the embedded ffmpeg (extracted the first
	// time a video thumbnail is made), else ffmpeg on PATH.
	ff, err := ffmpeg.Find(conf.FFmpeg)
	if err != nil {
		slog.Info("video thumbnails are disabled", "reason", err)
		ff = nil
	} else {
		slog.Info("video thumbnails use ffmpeg", "source", ff.Source())
	}
	store, err := newThumbnailStore()
	if err != nil {
		return nil, err
	}
	return &thumbnailer{
		store:  store,
		ffmpeg: ff,
		sem:    make(chan struct{}, runtime.NumCPU()),
		tp:     tp,
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
// request to base.
func (t *thumbnailer) handler(base http.Handler, fsys fs.FS) http.Handler {
	return &thumbnailHandler{t: t, base: base, fsys: fsys}
}

type thumbnailHandler struct {
	t    *thumbnailer
	base http.Handler
	fsys fs.FS
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

	key := thumbnailPath(name, info, spec)
	sum := sha256.Sum256([]byte(key))
	etag := `"` + hex.EncodeToString(sum[:12]) + `"`

	th, cached := h.t.store.get(key)
	span.SetAttributes(attribute.Bool("cached", cached))
	if !cached {
		// Concurrent requests for the same thumbnail share one render. It
		// isn't tied to the first request's cancellation, since others may
		// be waiting on it.
		v, err, _ := h.t.group.Do(key, func() (any, error) {
			th, err := h.make(context.WithoutCancel(ctx), name, video, spec)
			if err != nil {
				return nil, err
			}
			if err := h.t.store.put(key, th); err != nil {
				slog.Warn("failed to store thumbnail", "path", key, "error", err)
			}
			return th, nil
		})
		switch {
		case err == nil:
			th = v.(*thumbnail)
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

// videoFrame grabs a frame about a second in (or the first frame, for
// very short videos) with ffmpeg, which also applies the video's
// rotation. Files on local disk are passed by path so ffmpeg can seek.
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

// ffmpegFrames tries a frame a second in, then the first frame (for very
// short videos). When pipe is set, input is "pipe:0" and the file pipe (in
// h.fsys) is opened afresh for each attempt and fed to ffmpeg's stdin.
func (h *thumbnailHandler) ffmpegFrames(ctx context.Context, input, pipe string) (image.Image, error) {
	var last error
	for _, at := range []time.Duration{time.Second, 0} {
		img, err := h.ffmpegFrame(ctx, input, pipe, at)
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

func (h *thumbnailHandler) ffmpegFrame(ctx context.Context, input, pipe string, at time.Duration) (image.Image, error) {
	ctx, cancel := context.WithTimeout(ctx, thumbnailVideoTimeout)
	defer cancel()
	var stdin io.Reader
	if pipe != "" {
		f, err := h.fsys.Open(pipe)
		if err != nil {
			return nil, err
		}
		defer func() {
			if err := f.Close(); err != nil {
				slog.Error("failed to close video after thumbnailing", "path", pipe, "error", err)
			}
		}()
		stdin = f
	}
	return h.t.ffmpeg.Frame(ctx, input, stdin, at)
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
	// fill and inside results are already the right size.
	if spec.width == 0 || spec.height == 0 || spec.fit == fitFill || spec.fit == fitInside {
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
// but costs about twice what bilinear does on a large photo, so big
// reductions first go bilinear to twice the target size, then Catmull-Rom
// the rest of the way; the result is nearly identical.
func scaleImage(src image.Image, w, h int) *image.NRGBA {
	b := src.Bounds()
	if b.Dx() > 4*w && b.Dy() > 4*h {
		mid := image.NewNRGBA(image.Rect(0, 0, 2*w, 2*h))
		draw.BiLinear.Scale(mid, mid.Bounds(), src, b, draw.Src, nil)
		src, b = mid, mid.Bounds()
	}
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(out, out.Bounds(), src, b, draw.Src, nil)
	return out
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

// thumbnailStore keeps generated thumbnails as files in an in-memory
// filesystem (ufs "memory:"), laid out beside the path of the file each was
// made from: the 512x512 cover thumbnail of photos/a.jpg is stored as
// photos/a.jpg/512x512-cover@<version>, where the version changes whenever
// the source file's size or modification time does. Requests load from
// there first and only decode the source when it isn't stored.
//
// Nothing is evicted: the store grows with the number of distinct
// thumbnails made while the server runs.
type thumbnailStore struct {
	fsys ufs.WriteFS
}

func newThumbnailStore() (*thumbnailStore, error) {
	fsys, err := ufs.New(context.Background(), "memory:")
	if err != nil {
		return nil, err
	}
	wfs, ok := fsys.(ufs.WriteFS)
	if !ok {
		return nil, errors.New("memory filesystem is not writable")
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

// put stores th at name.
func (s *thumbnailStore) put(name string, th *thumbnail) error {
	if err := s.fsys.MkdirAll(path.Dir(name), 0o755); err != nil {
		return err
	}
	f, err := s.fsys.Create(name)
	if err != nil {
		return err
	}
	if _, err := f.Write(th.data); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

func (s *thumbnailStore) close() error {
	return s.fsys.Close()
}
