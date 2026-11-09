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

package ffmpeg

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/webp"
)

// script writes an executable shell script standing in for ffmpeg. It
// records its arguments in <dir>/args.txt and then runs body.
func script(t *testing.T, body string) (bin, argsFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as ffmpeg")
	}
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args.txt")
	bin = filepath.Join(dir, "ffmpeg")
	src := "#!/bin/sh\necho \"$@\" > '" + argsFile + "'\n" + body + "\n"
	if err := os.WriteFile(bin, []byte(src), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile
}

// pamImage is a w x h frame as ffmpeg writes it for -c:v pam
// -pix_fmt rgba, red at its top left.
func pamImage(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	header := fmt.Sprintf("P7\nWIDTH %d\nHEIGHT %d\nDEPTH 4\nMAXVAL 255\nTUPLTYPE RGB_ALPHA\nENDHDR\n", w, h)
	return append([]byte(header), img.Pix...)
}

func pamFile(t *testing.T, w, h int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "frame.pam")
	if err := os.WriteFile(p, pamImage(w, h), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindExplicit(t *testing.T) {
	if _, err := Find(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("Find of a missing path succeeded")
	}
	bin, _ := script(t, "true")
	f, err := Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	if f.Source() != bin {
		t.Errorf("Source = %q, want %q", f.Source(), bin)
	}
	if p, err := f.Path(t.Context()); err != nil || p != bin {
		t.Errorf("Path = %q, %v; want %q", p, err, bin)
	}
}

func TestResolve(t *testing.T) {
	bin, _ := script(t, "true")
	dir := filepath.Dir(bin)
	nested := filepath.Join(t.TempDir(), "ffmpeg-release")
	if err := os.MkdirAll(filepath.Join(nested, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	nestedBin := filepath.Join(nested, "bin", "ffmpeg")
	if err := os.Link(bin, nestedBin); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{bin: bin, dir: bin, nested: nestedBin} {
		got, err := Resolve(path)
		if err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
	for _, path := range []string{filepath.Join(dir, "missing"), t.TempDir()} {
		if got, err := Resolve(path); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q) = %q, %v; want ErrNotFound", path, got, err)
		}
	}
}

// A run that never starts says so, unlike one that fails on its input.
func TestRunNotStarted(t *testing.T) {
	dir := t.TempDir()
	f := &FFmpeg{path: dir} // a folder can't be run
	if _, err := f.Run(t.Context(), nil, "-version"); !errors.Is(err, ErrNotStarted) {
		t.Errorf("running a folder: %v, want ErrNotStarted", err)
	}
	if _, err := f.Duration(t.Context(), "x.mp4", nil); !errors.Is(err, ErrNotStarted) {
		t.Errorf("Duration with a folder: %v, want ErrNotStarted", err)
	}
	bin, _ := script(t, "exit 1")
	if _, err := (&FFmpeg{path: bin}).Run(t.Context(), nil, "-version"); err == nil || errors.Is(err, ErrNotStarted) {
		t.Errorf("ffmpeg exiting 1: %v, want an error that isn't ErrNotStarted", err)
	}
}

func TestRun(t *testing.T) {
	bin, argsFile := script(t, "cat; echo out")
	f, err := Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Run(t.Context(), strings.NewReader("in\n"), "-a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "in\nout\n" {
		t.Errorf("stdout = %q, want stdin then \"out\"", out)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(args)) != "-a b" {
		t.Errorf("args = %q, want \"-a b\"", args)
	}
}

func TestRunError(t *testing.T) {
	bin, _ := script(t, "echo 'Invalid data found' >&2; exit 1")
	f, err := Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Run(t.Context(), nil, "-i", "x")
	if err == nil || !strings.Contains(err.Error(), "Invalid data found") {
		t.Errorf("error = %v, want one carrying ffmpeg's stderr", err)
	}
}

func TestFrame(t *testing.T) {
	frame := pamFile(t, 16, 9)
	bin, argsFile := script(t, "cat > /dev/null; cat '"+frame+"'")
	f, err := Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	img, err := f.Frame(t.Context(), "pipe:0", strings.NewReader("video"), 1500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 16 || b.Dy() != 9 {
		t.Errorf("frame is %dx%d, want 16x9", b.Dx(), b.Dy())
	}
	if r, _, _, a := img.At(0, 0).RGBA(); r != 0xffff || a != 0xffff {
		t.Errorf("top left pixel = %v, want red", img.At(0, 0))
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-threads 1 ", "-ss 1.500 -i pipe:0", "-frames:v 1", "-c:v pam -pix_fmt rgba pipe:1"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
	// The exact frame at its full size.
	for _, unwanted := range []string{"-skip_frame", "-vf"} {
		if strings.Contains(string(args), unwanted) {
			t.Errorf("args %q have %q", args, unwanted)
		}
	}
}

func TestFrameWith(t *testing.T) {
	bin, argsFile := script(t, "cat '"+pamFile(t, 8, 8)+"'")
	f, err := Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.FrameWith(t.Context(), "clip.mp4", nil, FrameOptions{At: 10 * time.Second, Keyframe: true, Width: 512, Height: 288}); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		// Keyframes only is a decoder option, so it comes before the input.
		"-skip_frame nokey -ss 10.000 -i clip.mp4",
		"-vf scale=w='max(1,round(iw*min(1,max(512/iw,288/ih))))':h='max(1,round(ih*min(1,max(512/iw,288/ih))))':flags=bicubic",
	} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
}

func TestFrameArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    FrameOptions
		want string
	}{
		{"exact frame", FrameOptions{At: time.Second}, "-ss 1.000 -i in -frames:v 1"},
		{"first frame", FrameOptions{}, "-filter_threads 1 -i in -frames:v 1"},
		{
			"fit inside",
			FrameOptions{Width: 100, Height: 50, Inside: true},
			"-vf scale=w='max(1,round(iw*min(1,min(100/iw,50/ih))))':h='max(1,round(ih*min(1,min(100/iw,50/ih))))':flags=bicubic",
		},
		{"fit inside a width", FrameOptions{Width: 100, Inside: true}, "min(1,min(100/iw,ih/ih))"},
		// Decoder options, so before the input.
		{"reduced", FrameOptions{Reduce: 2}, "-lowres 2 -i in"},
		{"reduced at most 8 times", FrameOptions{Reduce: 5}, "-lowres 3 "},
		// Turned after scaling, which happens in the input's orientation.
		{
			"turned",
			FrameOptions{Width: 100, Height: 50, Orientation: 6},
			"-noautorotate -i in -frames:v 1 -vf scale=w='max(1,round(iw*min(1,max(50/iw,100/ih))))':h='max(1,round(ih*min(1,max(50/iw,100/ih))))':flags=bicubic,transpose=clock",
		},
		{"mirrored", FrameOptions{Orientation: 2}, "-noautorotate -i in -frames:v 1 -vf hflip"},
	} {
		if got := strings.Join(frameArgs("in", tc.o), " "); !strings.Contains(got, tc.want) {
			t.Errorf("%s: args %q missing %q", tc.name, got, tc.want)
		}
	}
	if got := strings.Join(frameArgs("in", FrameOptions{Orientation: 1}), " "); strings.Contains(got, "-vf") {
		t.Errorf("upright: args %q have a filter", got)
	}
}

func TestEncoders(t *testing.T) {
	bin, _ := script(t, "printf 'Encoders:\\n V..... = Video\\n ------\\n V....D libwebp              libwebp WebP image (codec webp)\\n VFS..D mjpeg                MJPEG (Motion JPEG)\\n'")
	f, err := Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.Encoders(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !got["libwebp"] || !got["mjpeg"] || got["="] || len(got) != 2 {
		t.Errorf("Encoders = %v, want libwebp and mjpeg", got)
	}
}

func TestFrameWebP(t *testing.T) {
	bin, argsFile := script(t, "printf RIFF")
	f, err := Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.FrameWebP(t.Context(), "photo.jpg", nil, FrameOptions{Width: 256, Height: 256, Reduce: 3}, 70)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "RIFF" {
		t.Errorf("FrameWebP = %q, want what ffmpeg wrote", out)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := "-c:v libwebp -quality 70 -compression_level 2 -f webp pipe:1"; !strings.Contains(string(args), want) {
		t.Errorf("args %q missing %q", args, want)
	}
}

func TestDecodePAM(t *testing.T) {
	img, err := decodePAM(pamImage(3, 2))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 3 || b.Dy() != 2 || img.Stride != 12 {
		t.Errorf("image is %dx%d with stride %d, want 3x2 with stride 12", b.Dx(), b.Dy(), img.Stride)
	}
	for name, data := range map[string][]byte{
		"png":       {0x89, 'P', 'N', 'G'},
		"no header": []byte("P7\nWIDTH 1\n"),
		"not P7":    []byte("P6\nENDHDR\n"),
		"no size":   []byte("P7\nDEPTH 4\nMAXVAL 255\nENDHDR\n"),
		"rgb":       []byte("P7\nWIDTH 1\nHEIGHT 1\nDEPTH 3\nMAXVAL 255\nTUPLTYPE RGB\nENDHDR\nabc"),
		"truncated": pamImage(3, 2)[:40],
	} {
		if _, err := decodePAM(data); err == nil {
			t.Errorf("%s: decoded, want an error", name)
		}
	}
}

// TestFrameWithFFmpeg runs the ffmpeg installed on PATH, when there is
// one, on a video with a keyframe every 3 seconds.
func TestFrameWithFFmpeg(t *testing.T) {
	f, err := Find("")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	// 12 seconds at 10 frames a second, each frame's number in its blue,
	// keyframes at 0, 3, 6 and 9 seconds.
	video := filepath.Join(t.TempDir(), "clip.mp4")
	if _, err := f.Run(t.Context(), nil, "-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", "lavfi", "-i", "color=c=black:size=320x180:rate=10:duration=12,geq=r=0:g=0:b='N*2'",
		"-c:v", "libx264", "-qp", "0", "-g", "30", "-keyint_min", "30", "-sc_threshold", "0", "-pix_fmt", "yuv444p", video); err != nil {
		t.Skipf("cannot make a test video: %v", err)
	}
	blue := func(img image.Image) int {
		_, _, b, _ := img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2).RGBA()
		return int(b >> 8)
	}

	// The first keyframe at or after 4s is frame 60, 6 seconds in.
	img, err := f.FrameWith(t.Context(), video, nil, FrameOptions{At: 4 * time.Second, Keyframe: true, Width: 64, Height: 64})
	if err != nil {
		t.Fatal(err)
	}
	if got := blue(img); got < 118 || got > 122 {
		t.Errorf("keyframe after 4s has blue %d, want frame 60's 120", got)
	}
	// Shrunk to cover 64x64 at the video's 16:9.
	if b := img.Bounds(); b.Dx() != 114 || b.Dy() != 64 {
		t.Errorf("frame is %dx%d, want 114x64", b.Dx(), b.Dy())
	}
	// Never enlarged.
	if img, err := f.FrameWith(t.Context(), video, nil, FrameOptions{Keyframe: true, Width: 4096}); err != nil {
		t.Error(err)
	} else if b := img.Bounds(); b.Dx() != 320 || b.Dy() != 180 {
		t.Errorf("frame is %dx%d, want the video's 320x180", b.Dx(), b.Dy())
	}
	// Without Keyframe it's the frame at exactly 4s, frame 40.
	if img, err := f.FrameWith(t.Context(), video, nil, FrameOptions{At: 4 * time.Second}); err != nil {
		t.Error(err)
	} else if got := blue(img); got < 78 || got > 82 {
		t.Errorf("frame at 4s has blue %d, want frame 40's 80", got)
	}
	// No keyframe after the last one: no frame.
	if _, err := f.FrameWith(t.Context(), video, nil, FrameOptions{At: 10 * time.Second, Keyframe: true}); !errors.Is(err, ErrNoFrame) {
		t.Errorf("keyframe after 10s: error = %v, want ErrNoFrame", err)
	}
}

// cornersJPEG is a 64x32 JPEG photo, red at its top left and green at its
// top right, whose EXIF orientation says to turn it upside down.
func cornersJPEG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for y := range 32 {
		for x := range 64 {
			c := color.RGBA{R: 128, G: 128, B: 128, A: 255}
			switch {
			case x < 16 && y < 16:
				c = color.RGBA{R: 255, A: 255}
			case x >= 48 && y < 16:
				c = color.RGBA{G: 255, A: 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	jpg := buf.Bytes()
	tiff := []byte{
		'M', 'M', 0, 42, 0, 0, 0, 8, // big-endian header, IFD at 8
		0, 1, // one entry
		0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, 3, 0, 0, // Orientation, SHORT, 1, upside down
		0, 0, 0, 0, // no next IFD
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	exif := append([]byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}, payload...)
	p := filepath.Join(t.TempDir(), "corners.jpg")
	if err := os.WriteFile(p, append(append(append([]byte{}, jpg[:2]...), exif...), jpg[2:]...), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// corners returns the colors at the four corners of img: top left, top
// right, bottom left, bottom right, as r (red), g (green) or . (gray).
func corners(img image.Image) string {
	b := img.Bounds()
	name := func(x, y int) string {
		r, g, _, _ := img.At(x, y).RGBA()
		switch {
		case r > 0xc000 && g < 0x4000:
			return "r"
		case g > 0xc000 && r < 0x4000:
			return "g"
		}
		return "."
	}
	return name(b.Min.X+1, b.Min.Y+1) + name(b.Max.X-2, b.Min.Y+1) + name(b.Min.X+1, b.Max.Y-2) + name(b.Max.X-2, b.Max.Y-2)
}

// TestFrameOrientationFFmpeg runs the ffmpeg installed on PATH, when there
// is one, turning a photo by each EXIF orientation in place of the one
// the file has.
func TestFrameOrientationFFmpeg(t *testing.T) {
	f, err := Find("")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	photo := cornersJPEG(t)
	for o, want := range map[int]string{
		1: "rg..", 2: "gr..", 3: "..gr", 4: "..rg",
		5: "r.g.", 6: ".r.g", 7: ".g.r", 8: "g.r.",
	} {
		img, err := f.FrameWith(t.Context(), photo, nil, FrameOptions{Orientation: o})
		if err != nil {
			t.Fatal(err)
		}
		if got := corners(img); got != want {
			t.Errorf("orientation %d: corners %q, want %q", o, got, want)
		}
		wantW, wantH := 64, 32
		if o >= 5 {
			wantW, wantH = 32, 64
		}
		if b := img.Bounds(); b.Dx() != wantW || b.Dy() != wantH {
			t.Errorf("orientation %d: frame is %dx%d, want %dx%d", o, b.Dx(), b.Dy(), wantW, wantH)
		}
	}
	// Turned, the box is the turned photo's: 16x32 covers 16x32 exactly.
	if img, err := f.FrameWith(t.Context(), photo, nil, FrameOptions{Orientation: 6, Width: 16, Height: 16}); err != nil {
		t.Error(err)
	} else if b := img.Bounds(); b.Dx() != 16 || b.Dy() != 32 {
		t.Errorf("turned and shrunk to cover 16x16: frame is %dx%d, want 16x32", b.Dx(), b.Dy())
	}
	// Fit inside, and decoded at a quarter of the size.
	if img, err := f.FrameWith(t.Context(), photo, nil, FrameOptions{Orientation: 1, Width: 32, Height: 32, Inside: true}); err != nil {
		t.Error(err)
	} else if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 16 {
		t.Errorf("inside 32x32: frame is %dx%d, want 32x16", b.Dx(), b.Dy())
	}
	if img, err := f.FrameWith(t.Context(), photo, nil, FrameOptions{Orientation: 1, Reduce: 2}); err != nil {
		t.Error(err)
	} else if b := img.Bounds(); b.Dx() != 16 || b.Dy() != 8 {
		t.Errorf("reduced by 4: frame is %dx%d, want 16x8", b.Dx(), b.Dy())
	}
}

// TestWebPFFmpeg runs the ffmpeg installed on PATH, when there is one
// with libwebp, to make WebP images.
func TestWebPFFmpeg(t *testing.T) {
	f, err := Find("")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if enc, err := f.Encoders(t.Context()); err != nil || !enc["libwebp"] {
		t.Skipf("ffmpeg has no libwebp encoder (%v)", err)
	}
	data, err := f.FrameWebP(t.Context(), cornersJPEG(t), nil, FrameOptions{Orientation: 2, Width: 32, Height: 32, Inside: true}, 90)
	if err != nil {
		t.Fatal(err)
	}
	img, err := webp.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 16 {
		t.Errorf("WebP frame is %dx%d, want 32x16", b.Dx(), b.Dy())
	}
	if got := corners(img); got != "gr.." {
		t.Errorf("WebP frame corners %q, want \"gr..\"", got)
	}

	for name, src := range map[string]*image.NRGBA{
		"opaque": image.NewNRGBA(image.Rect(0, 0, 40, 20)),
		"clear":  image.NewNRGBA(image.Rect(0, 0, 40, 20)),
	} {
		for i := range src.Pix {
			if name == "opaque" || i%4 != 3 {
				src.Pix[i] = 0xff
			}
		}
		// A sub-image, whose rows don't start at the start of Pix.
		sub := src.SubImage(image.Rect(4, 2, 36, 18)).(*image.NRGBA)
		data, err := f.EncodeWebP(t.Context(), sub, 80)
		if err != nil {
			t.Fatal(name, err)
		}
		img, err := webp.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(name, err)
		}
		if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 16 {
			t.Errorf("%s: WebP image is %dx%d, want 32x16", name, b.Dx(), b.Dy())
		}
		_, _, _, a := img.At(5, 5).RGBA()
		if want := map[string]uint32{"opaque": 0xffff, "clear": 0}[name]; a != want {
			t.Errorf("%s: alpha = %#x, want %#x", name, a, want)
		}
	}
}

func TestFrameNone(t *testing.T) {
	bin, _ := script(t, "true") // succeeds, writes nothing
	f, err := Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Frame(t.Context(), "short.mp4", nil, time.Second); !errors.Is(err, ErrNoFrame) {
		t.Errorf("error = %v, want ErrNoFrame", err)
	}
}

func TestVersion(t *testing.T) {
	bin, _ := script(t, "printf 'ffmpeg version 7.0.2-static\\nbuilt with gcc\\n'")
	f, err := Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := f.Version(t.Context()); err != nil || v != "ffmpeg version 7.0.2-static" {
		t.Errorf("Version = %q, %v", v, err)
	}
}

func TestDuration(t *testing.T) {
	// ffmpeg describes the input on stderr, then fails for want of an
	// output.
	bin, argsFile := script(t, `cat > /dev/null
echo "Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'pipe:0':" >&2
echo "  Duration: 01:02:03.50, start: 0.000000, bitrate: N/A" >&2
echo "At least one output file must be specified" >&2
exit 1`)
	f, err := Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	d, err := f.Duration(t.Context(), "pipe:0", strings.NewReader("video"))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Hour + 2*time.Minute + 3500*time.Millisecond; d != want {
		t.Errorf("Duration = %v, want %v", d, want)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "-i pipe:0") {
		t.Errorf("args = %q", args)
	}
}

func TestDurationUnknown(t *testing.T) {
	for name, body := range map[string]string{
		"N/A":      `echo "  Duration: N/A, start: 0.000000, bitrate: N/A" >&2; exit 1`,
		"zero":     `echo "  Duration: 00:00:00.00, start: 0.000000" >&2; exit 1`,
		"not read": `echo "pipe:0: Invalid data found when processing input" >&2; exit 1`,
	} {
		t.Run(name, func(t *testing.T) {
			bin, _ := script(t, body)
			f, err := Find(bin)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Duration(t.Context(), "x.mp4", nil); !errors.Is(err, ErrNoDuration) {
				t.Errorf("error = %v, want ErrNoDuration", err)
			}
		})
	}
}
