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

// Issue #424: each size of a video's thumbnail runs ffmpeg on the video
// again. These benchmarks compare that with grabbing the frame once and
// making every size from it in Go.
//
//	go test -run '^$' -bench 'VideoThumbnailSizes|DeriveFromFrame' -benchtime 20x ./pkg/gowebserver/
//
// GOWS_BENCH_VIDEO names a video to use (a real camera or phone clip says
// more than the generated one); otherwise a 1080p H.264 clip is made.

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
	"golang.org/x/image/webp"
)

// The sizes the file browser asks for a video, on a 2x screen: the
// folder strip's tile, the grid's tile and the slideshow's poster.
var deriveSizes = []thumbnailSpec{
	{width: 128, height: 72, fit: fitOutside, format: "webp", quality: thumbnailQuality},
	{width: 512, height: 288, fit: fitOutside, format: "webp", quality: thumbnailQuality},
	{width: 1024, height: 1024, fit: fitInside, format: "webp", quality: thumbnailQuality},
}

// deriveSource is the one frame every size is made from: one that covers
// the biggest box any of them needs, 1024 x 1024 (a 16:9 video's is
// 1820 x 1024).
var deriveSource = ffmpeg.FrameOptions{Keyframe: true, At: thumbnailVideoFrameAt, Width: 1024, Height: 1024}

func benchFFmpeg(b *testing.B) (*ffmpeg.FFmpeg, string) {
	b.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		b.Skip("ffmpeg is not installed")
	}
	ff, err := ffmpeg.Find(bin)
	if err != nil {
		b.Fatal(err)
	}
	if video := os.Getenv("GOWS_BENCH_VIDEO"); video != "" {
		return ff, video
	}
	video := filepath.Join(b.TempDir(), "clip.mp4")
	if out, err := exec.Command(bin, "-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=30:duration=20",
		"-c:v", "libx264", "-g", "60", "-pix_fmt", "yuv420p", video).CombinedOutput(); err != nil {
		b.Skipf("cannot make a test video: %v: %s", err, out)
	}
	return ff, video
}

// separateSizes is what the server does now: ffmpeg finds, decodes and
// encodes the frame for each size.
func separateSizes(ctx context.Context, ff *ffmpeg.FFmpeg, video string) ([][]byte, error) {
	var out [][]byte
	for _, spec := range deriveSizes {
		w, h, inside := spec.ffmpegBox()
		data, err := ff.FrameWebP(ctx, video, nil, ffmpeg.FrameOptions{Keyframe: true, At: thumbnailVideoFrameAt, Width: w, Height: h, Inside: inside}, spec.quality)
		if err != nil {
			return nil, err
		}
		out = append(out, data)
	}
	return out, nil
}

// derivedSizes grabs the frame once, then scales it in Go for each size,
// which ffmpeg only encodes.
func derivedSizes(ctx context.Context, ff *ffmpeg.FFmpeg, src image.Image) ([][]byte, error) {
	var out [][]byte
	for i := range deriveSizes {
		data, err := ff.EncodeWebP(ctx, renderThumbnail(src, 1, &deriveSizes[i]), deriveSizes[i].quality)
		if err != nil {
			return nil, err
		}
		out = append(out, data)
	}
	return out, nil
}

func BenchmarkVideoThumbnailSizes(b *testing.B) {
	ff, video := benchFFmpeg(b)
	ctx := context.Background()

	b.Run("separate", func(b *testing.B) {
		for b.Loop() {
			if _, err := separateSizes(ctx, ff, video); err != nil {
				b.Fatal(err)
			}
		}
	})
	// The frame grabbed and every size made in one go, as the first
	// request for a video would.
	b.Run("derived", func(b *testing.B) {
		for b.Loop() {
			src, err := ff.FrameWith(ctx, video, nil, deriveSource)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := derivedSizes(ctx, ff, src); err != nil {
				b.Fatal(err)
			}
		}
	})
	// As the issue proposes: the frame is stored (here as a WebP at
	// quality 90), and each size is made from the stored copy when it's
	// asked for, so every size after the first pays a decode in Go.
	b.Run("stored", func(b *testing.B) {
		for b.Loop() {
			stored, err := ff.FrameWebP(ctx, video, nil, deriveSource, 90)
			if err != nil {
				b.Fatal(err)
			}
			for i := range deriveSizes {
				src, err := webp.Decode(bytes.NewReader(stored))
				if err != nil {
					b.Fatal(err)
				}
				if _, err := ff.EncodeWebP(ctx, renderThumbnail(src, 1, &deriveSizes[i]), deriveSizes[i].quality); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	// The same stored frame, but each size made by ffmpeg from it: a
	// small still to decode instead of a video to seek, and ffmpeg's
	// scaler instead of Go's.
	b.Run("stored-ffmpeg", func(b *testing.B) {
		for b.Loop() {
			stored, err := ff.FrameWebP(ctx, video, nil, deriveSource, 90)
			if err != nil {
				b.Fatal(err)
			}
			for _, spec := range deriveSizes {
				w, h, inside := spec.ffmpegBox()
				if _, err := ff.FrameWebP(ctx, "pipe:0", bytes.NewReader(stored), ffmpeg.FrameOptions{Width: w, Height: h, Inside: inside}, spec.quality); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	// The pieces, to see where the time goes.
	b.Run("grab-source", func(b *testing.B) {
		for b.Loop() {
			if _, err := ff.FrameWith(ctx, video, nil, deriveSource); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("encode-tile", func(b *testing.B) {
		tile := renderThumbnail(noisyFrame(1820, 1024), 1, &deriveSizes[1])
		for b.Loop() {
			if _, err := ff.EncodeWebP(ctx, tile, thumbnailQuality); err != nil {
				b.Fatal(err)
			}
		}
	})

	// How far the derived sizes are from ffmpeg's own (bicubic from the
	// full frame), in PSNR: above 35 dB the two are hard to tell apart.
	sep, err := separateSizes(ctx, ff, video)
	if err != nil {
		b.Fatal(err)
	}
	src, err := ff.FrameWith(ctx, video, nil, deriveSource)
	if err != nil {
		b.Fatal(err)
	}
	der, err := derivedSizes(ctx, ff, src)
	if err != nil {
		b.Fatal(err)
	}
	for i := range deriveSizes {
		a, err := webp.Decode(bytes.NewReader(sep[i]))
		if err != nil {
			b.Fatal(err)
		}
		d, err := webp.Decode(bytes.NewReader(der[i]))
		if err != nil {
			b.Fatal(err)
		}
		b.Logf("%dx%d: separate %d bytes %v, derived %d bytes %v, PSNR %.1f dB",
			deriveSizes[i].width, deriveSizes[i].height, len(sep[i]), a.Bounds().Size(), len(der[i]), d.Bounds().Size(), psnr(a, d))
	}
}

// BenchmarkDeriveFromFrame is the Go side of deriving, which needs no
// ffmpeg: every size scaled from a frame, and the frame kept as a JPEG
// and read back (a stand-in for keeping it as a WebP, which Go can read
// but not write).
func BenchmarkDeriveFromFrame(b *testing.B) {
	frame := noisyFrame(1820, 1024)
	for _, spec := range deriveSizes {
		b.Run("scale-"+specName(spec), func(b *testing.B) {
			for b.Loop() {
				renderThumbnail(frame, 1, &spec)
			}
		})
	}
	var stored bytes.Buffer
	if err := jpeg.Encode(&stored, frame, &jpeg.Options{Quality: 90}); err != nil {
		b.Fatal(err)
	}
	b.Logf("frame as a JPEG at quality 90: %d bytes", stored.Len())
	b.Run("store-jpeg", func(b *testing.B) {
		for b.Loop() {
			if err := jpeg.Encode(&bytes.Buffer{}, frame, &jpeg.Options{Quality: 90}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("load-jpeg", func(b *testing.B) {
		for b.Loop() {
			if _, err := jpeg.Decode(bytes.NewReader(stored.Bytes())); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func specName(s thumbnailSpec) string {
	name := "outside"
	if s.fit == fitInside {
		name = "inside"
	}
	return itoa(s.width) + "x" + itoa(s.height) + "-" + name
}

func itoa(n int) string { return string(appendInt(nil, n)) }

func appendInt(b []byte, n int) []byte {
	if n >= 10 {
		b = appendInt(b, n/10)
	}
	return append(b, byte('0'+n%10))
}

// noisyFrame is a w x h stand-in for a video frame: smooth gradients with
// fine noise, so scaling has detail to filter.
func noisyFrame(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewPCG(1, 2))
	for y := range h {
		for x := range w {
			n := uint8(r.IntN(32))
			img.SetRGBA(x, y, color.RGBA{R: uint8(x*255/w) ^ n, G: uint8(y*255/h) + n, B: uint8((x + y) * 255 / (w + h)), A: 255})
		}
	}
	return img
}

// psnr is the peak signal-to-noise ratio between a and b in dB, over the
// area both cover.
func psnr(a, b image.Image) float64 {
	r := a.Bounds().Intersect(b.Bounds())
	var sum float64
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			ar, ag, ab, _ := a.At(x, y).RGBA()
			br, bg, bb, _ := b.At(x, y).RGBA()
			for _, d := range []float64{float64(ar>>8) - float64(br>>8), float64(ag>>8) - float64(bg>>8), float64(ab>>8) - float64(bb>>8)} {
				sum += d * d
			}
			n += 3
		}
	}
	if sum == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(255*255/(sum/float64(n)))
}
