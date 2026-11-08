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
	"image"
	"image/color"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// solid is a w x h image of one color.
func solid(w, h int, c color.Color) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// framesAt makes the fake ffmpeg at bin print frames[at] for a frame asked
// for at at ("10.000" and the like), and the frame it was made with for
// any other time.
func framesAt(t *testing.T, bin string, frames map[string]image.Image) {
	t.Helper()
	script, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	cases := ""
	for at, img := range frames {
		p := filepath.Join(t.TempDir(), "frame.pam")
		if err := os.WriteFile(p, encodePAM(img), 0o600); err != nil {
			t.Fatal(err)
		}
		cases += "*'-ss " + at + " '*) cat '" + p + "'; exit 0;; "
	}
	s := strings.Replace(string(script), "cat > /dev/null\n",
		"cat > /dev/null\ncase \"$*\" in *-frames:v*) case \"$*\" in "+cases+"esac;; esac\n", 1)
	if err := os.WriteFile(bin, []byte(s), 0o700); err != nil {
		t.Fatal(err)
	}
}

// ffmpegRuns is how many times the fake ffmpeg has run.
func ffmpegRuns(t *testing.T, argsFile string) int {
	t.Helper()
	data, err := os.ReadFile(argsFile)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Split(strings.TrimSpace(string(data)), "\n"))
}

func TestThumbnailVideoDarkFrames(t *testing.T) {
	video := fstest.MapFS{"clip.mp4": {Data: []byte("not really a video")}}
	black := solid(64, 36, color.Black)
	dim := solid(64, 36, color.Gray{Y: 12})

	t.Run("past a black intro", func(t *testing.T) {
		bin, argsFile := fakeFFmpeg(t, stripes(64, 36), "00:02:00.00")
		framesAt(t, bin, map[string]image.Image{"10.000": black})
		h, _ := newTestThumbnailHandler(t, video, bin)
		img := decodeThumb(t, thumbGet(t, h, "/clip.mp4?width=32", nil))
		if l := meanLuma(img); l < thumbnailDarkLuma {
			t.Errorf("thumbnail brightness %.0f: still the black frame", l)
		}
		// Ten seconds in was black, so half a minute in was next.
		runs := frameArgs(t, argsFile)
		if len(runs) != 2 || !strings.Contains(runs[0], "-ss 10.000 ") || !strings.Contains(runs[1], "-ss 30.000 ") {
			t.Errorf("frame runs %q: want 10s, then 30s", runs)
		}
	})

	t.Run("the brightest when all are dark", func(t *testing.T) {
		bin, argsFile := fakeFFmpeg(t, black, "00:02:00.00")
		framesAt(t, bin, map[string]image.Image{"60.000": dim})
		h, _ := newTestThumbnailHandler(t, video, bin)
		img := decodeThumb(t, thumbGet(t, h, "/clip.mp4?width=32", nil))
		if l := meanLuma(img); l < 6 {
			t.Errorf("thumbnail brightness %.0f: want the dim frame, the brightest", l)
		}
		// 10s, then 30s, 60s and 120/4 = 30s again (once), 120/2 = 60s
		// again: at most thumbnailLaterFrames later ones.
		if runs := frameArgs(t, argsFile); len(runs) > 1+thumbnailLaterFrames {
			t.Errorf("frame runs %q: want at most %d", runs, 1+thumbnailLaterFrames)
		}
	})

	t.Run("a bright frame is the only run", func(t *testing.T) {
		bin, argsFile := fakeFFmpeg(t, stripes(64, 36), "00:02:00.00")
		h, _ := newTestThumbnailHandler(t, video, bin)
		decodeThumb(t, thumbGet(t, h, "/clip.mp4?width=32", nil))
		if n := ffmpegRuns(t, argsFile); n != 1 {
			t.Errorf("ffmpeg ran %d times, want once", n)
		}
	})
}

func TestThumbnailVideoUnsupportedRemembered(t *testing.T) {
	video := fstest.MapFS{"clip.mp4": {Data: []byte("not really a video")}}
	bin, argsFile := fakeFFmpeg(t, stripes(64, 36), "N/A")
	// No frame anywhere: ffmpeg runs, and fails on this video.
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\" >> '"+argsFile+"'\ncat > /dev/null\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	h, _ := newTestThumbnailHandler(t, video, bin)
	first := thumbGet(t, h, "/clip.mp4?width=32", nil)
	if first.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d, want 415", first.Code)
	}
	if cc := first.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=") {
		t.Errorf("Cache-Control %q: want the browser to keep the answer", cc)
	}
	runs := ffmpegRuns(t, argsFile)
	// Another size, as the filmstrip asks for, or the page reloaded.
	for _, target := range []string{"/clip.mp4?width=32", "/clip.mp4?width=128"} {
		if rec := thumbGet(t, h, target, nil); rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s: status %d, want 415", target, rec.Code)
		}
	}
	if n := ffmpegRuns(t, argsFile); n != runs {
		t.Errorf("ffmpeg ran %d more times for a video it can't do", n-runs)
	}
}

func TestThumbnailFFmpegWontStart(t *testing.T) {
	video := fstest.MapFS{
		"a.mp4":     {Data: []byte("not really a video")},
		"b.mp4":     {Data: []byte("not really a video")},
		"photo.png": {Data: encodePNG(t, stripes(64, 36))},
	}
	bin, _ := fakeFFmpeg(t, stripes(64, 36), "00:00:20.00")
	h, _ := newTestThumbnailHandler(t, video, bin)
	th := h.(*thumbnailHandler).t
	// Once found, the program can't be run any more.
	if err := os.Chmod(bin, 0o600); err != nil {
		t.Fatal(err)
	}
	if !th.videos() {
		t.Fatal("video thumbnails off before ffmpeg was tried")
	}
	start := time.Now()
	rec := thumbGet(t, h, "/a.mp4?width=32", nil)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d, want 415", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control %q: a video isn't to blame for ffmpeg, so the answer isn't kept", cc)
	}
	if th.videos() {
		t.Error("video thumbnails still offered with an ffmpeg that won't start")
	}
	// The next video doesn't try at all, and photos still work.
	if rec := thumbGet(t, h, "/b.mp4?width=32", nil); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("second video: status %d, want 415", rec.Code)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v to give up", d)
	}
	decodeThumb(t, thumbGet(t, h, "/photo.png?width=32", nil))
}

// osFileWrapper stands in for ufs on Windows, which hands out its own
// type around *os.File.
type osFileWrapper struct{ *os.File }

type wrappingFS struct{ fs.FS }

func (w wrappingFS) Open(name string) (fs.File, error) {
	f, err := w.FS.Open(name)
	if osf, ok := f.(*os.File); ok {
		return osFileWrapper{osf}, nil
	}
	return f, err
}

func TestThumbnailVideoWrappedLocalFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "clip.mp4"), []byte("not really a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin, argsFile := fakeFFmpeg(t, stripes(64, 36), "00:00:20.00")
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	h, _ := newTestThumbnailHandler(t, wrappingFS{root.FS()}, bin)
	decodeThumb(t, thumbGet(t, h, "/clip.mp4?width=32", nil))
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "-i "+filepath.Join(dir, "clip.mp4")) {
		t.Errorf("ffmpeg args %q: want the file's path, not a pipe or a copy", args)
	}
}

func TestLocalFilePath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.mp4")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "b.mp4")
	if err := os.WriteFile(other, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := localFilePath(f, info); !ok || got != p {
		t.Errorf("*os.File: %q, %v", got, ok)
	}
	if got, ok := localFilePath(osFileWrapper{f}, info); !ok || got != p {
		t.Errorf("wrapped: %q, %v", got, ok)
	}
	// A name that isn't this file.
	otherInfo, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := localFilePath(f, otherInfo); ok {
		t.Errorf("another file's info: %q, want not local", got)
	}
	mf, err := fstest.MapFS{"a.mp4": {Data: []byte("x")}}.Open("a.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := localFilePath(mf, info); ok {
		t.Errorf("not on disk: %q", got)
	}
}
