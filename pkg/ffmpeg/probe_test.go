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

package ffmpeg

import (
	"bytes"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// readArgs is what a script stood in for ffmpeg was run with.
func readArgs(t *testing.T, argsFile string) []byte {
	t.Helper()
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	return args
}

func TestParseChapters(t *testing.T) {
	data := `;FFMETADATA1
title=A talk
[CHAPTER]
TIMEBASE=1/1000
START=0
END=1200000
title=Part 1
[CHAPTER]
TIMEBASE=1/90000
START=108000000
END=216000000
title=Part 2 \= more \; and \\ backslash
[CHAPTER]
TIMEBASE=1/1000
START=5
END=5
title=empty, left out
[STREAM]
title=not a chapter
[CHAPTER]
START=2400000
END=2500000
title=Q&A\
over two lines
`
	got := parseChapters([]byte(data))
	want := []Chapter{
		{Start: 0, End: 20 * time.Minute, Title: "Part 1"},
		{Start: 20 * time.Minute, End: 40 * time.Minute, Title: `Part 2 = more ; and \ backslash`},
		{Start: 40 * time.Minute, End: 2500 * time.Second / 1000 * 1000, Title: "Q&A\nover two lines"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseChapters =\n%#v\nwant\n%#v", got, want)
	}
	if got := parseChapters([]byte(";FFMETADATA1\ntitle=no chapters\n")); got != nil {
		t.Errorf("no chapters = %#v, want nil", got)
	}
}

func TestProbeStandIn(t *testing.T) {
	stderr := `Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'clip.mp4':
  Duration: 01:30:00.03, start: 0.000000, bitrate: 215 kb/s
  Stream #0:0[0x1](und): Video: h264 (High) (avc1 / 0x31637661), yuv420p(tv, bt709, progressive), 1920x1080 [SAR 1:1 DAR 16:9], 4000 kb/s, 29.97 fps, 29.97 tbr, 30k tbn (default)
      Side data:
        displaymatrix: rotation of -90.00 degrees
`
	errFile := filepath.Join(t.TempDir(), "stderr.txt")
	if err := os.WriteFile(errFile, []byte(stderr), 0o600); err != nil {
		t.Fatal(err)
	}
	bin, argsFile := script(t, "cat '"+errFile+"' >&2; printf ';FFMETADATA1\\n[CHAPTER]\\nTIMEBASE=1/1000\\nSTART=0\\nEND=1000\\ntitle=One\\n'")
	f, err := Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.Probe(t.Context(), "clip.mp4", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := &Info{
		Duration: 90*time.Minute + 30*time.Millisecond, Width: 1080, Height: 1920, FPS: 29.97,
		Chapters: []Chapter{{Start: 0, End: time.Second, Title: "One"}},
	}
	if !reflect.DeepEqual(info, want) {
		t.Errorf("Probe = %+v, want %+v", info, want)
	}
	args := readArgs(t, argsFile)
	if !strings.Contains(string(args), "-i clip.mp4 -f ffmetadata pipe:1") {
		t.Errorf("args = %q", args)
	}
}

func TestProbeNonSquarePixels(t *testing.T) {
	stderr := "  Duration: 00:01:00.00, start: 0.000000, bitrate: 900 kb/s\n" +
		"  Stream #0:0: Video: mpeg2video (Main), yuv420p(tv, top first), 720x480 [SAR 32:27 DAR 16:9], 25 fps, 25 tbr\n"
	errFile := filepath.Join(t.TempDir(), "stderr.txt")
	if err := os.WriteFile(errFile, []byte(stderr), 0o600); err != nil {
		t.Fatal(err)
	}
	bin, _ := script(t, "cat '"+errFile+"' >&2")
	f, err := Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.Probe(t.Context(), "disc.mkv", nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 853 || info.Height != 480 {
		t.Errorf("size = %dx%d, want 853x480, as it's shown", info.Width, info.Height)
	}
}

func TestProbeFails(t *testing.T) {
	bin, _ := script(t, "echo 'clip.mp4: Invalid data found when processing input' >&2; exit 1")
	f, err := Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Probe(t.Context(), "clip.mp4", nil); err == nil || !strings.Contains(err.Error(), "Invalid data") {
		t.Errorf("err = %v, want ffmpeg's reason", err)
	}
}

func TestSpriteArgs(t *testing.T) {
	bin, argsFile := script(t, "printf 'jpeg'")
	f, err := Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Sprite(t.Context(), "clip.mp4", nil, SpriteOptions{}); err == nil {
		t.Error("a sprite with no layout is an error")
	}
	out, err := f.Sprite(t.Context(), "clip.mp4", nil, SpriteOptions{Interval: 18 * time.Second, Columns: 10, Rows: 30, Width: 160, Height: 90})
	if err != nil || string(out) != "jpeg" {
		t.Fatalf("Sprite = %q, %v", out, err)
	}
	args := readArgs(t, argsFile)
	for _, want := range []string{"-skip_frame nokey -i clip.mp4", "fps=fps=1/18.000:round=up:eof_action=pass,scale=w=160:h=90:flags=bilinear,format=yuvj420p,tile=10x30", "-frames:v 1", "-c:v mjpeg"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
}

// TestProbeAndSpriteReal runs the ffmpeg installed on PATH on a video it
// makes with chapters in its container.
func TestProbeAndSpriteReal(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	meta := filepath.Join(dir, "chapters.ffmeta")
	if err := os.WriteFile(meta, []byte(";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=3000\ntitle=Part 1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=3000\nEND=6000\ntitle=Q&A\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	clip := filepath.Join(dir, "clip.mp4")
	if out, err := exec.Command(bin, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=10:duration=6",
		"-i", meta, "-map", "0:v", "-map_metadata", "1", "-map_chapters", "1", "-g", "10", "-pix_fmt", "yuv420p", clip).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg can't make a test video: %v: %s", err, out)
	}
	f, err := Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.Probe(t.Context(), clip, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration < 5900*time.Millisecond || info.Duration > 6100*time.Millisecond || info.Width != 320 || info.Height != 180 || info.FPS != 10 {
		t.Errorf("Probe = %+v, want 6s of 320x180 at 10 fps", info)
	}
	want := []Chapter{{Start: 0, End: 3 * time.Second, Title: "Part 1"}, {Start: 3 * time.Second, End: 6 * time.Second, Title: "Q&A"}}
	if !reflect.DeepEqual(info.Chapters, want) {
		t.Errorf("chapters = %+v, want %+v", info.Chapters, want)
	}

	sheet, err := f.Sprite(t.Context(), clip, nil, SpriteOptions{Interval: 2 * time.Second, Columns: 2, Rows: 2, Width: 64, Height: 36})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(sheet))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 128 || cfg.Height != 72 {
		t.Errorf("sprite is %dx%d, want 128x72", cfg.Width, cfg.Height)
	}

	// Under a second, in full range, as a browser records one.
	short := filepath.Join(dir, "short.webm")
	if out, err := exec.Command(bin, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30:duration=0.9",
		"-c:v", "libvpx", "-pix_fmt", "yuv420p", "-color_range", "pc", short).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg can't make a full-range video: %v: %s", err, out)
	}
	if sheet, err = f.Sprite(t.Context(), short, nil, SpriteOptions{Interval: 5 * time.Second, Columns: 1, Rows: 1, Width: 64, Height: 36}); err != nil {
		t.Fatalf("a short full-range video: %v", err)
	}
	if cfg, err = jpeg.DecodeConfig(bytes.NewReader(sheet)); err != nil || cfg.Width != 64 || cfg.Height != 36 {
		t.Errorf("short sprite is %dx%d (%v), want 64x36", cfg.Width, cfg.Height, err)
	}
}

// TestSpriteFrameTimes checks each frame of the sprite is the keyframe at
// or before its time, as a player shows there, not one up to an interval
// later. The video's brightness is its time (40 + 10 a second), and it has a
// keyframe every 3 seconds, so frame n, for 5n seconds, is the keyframe
// at 0, 3, 9 and 15 seconds.
func TestSpriteFrameTimes(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	clip := filepath.Join(t.TempDir(), "ramp.mp4")
	if out, err := exec.Command(bin, "-hide_banner", "-loglevel", "error", "-f", "lavfi",
		"-i", "color=black:s=64x36:r=10:d=20,geq=lum='40+10*T':cb=128:cr=128",
		"-g", "30", "-keyint_min", "30", "-sc_threshold", "0", "-pix_fmt", "yuv420p", clip).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg can't make the test video: %v: %s", err, out)
	}
	f, err := Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := f.Sprite(t.Context(), clip, nil, SpriteOptions{Interval: 5 * time.Second, Columns: 4, Rows: 1, Width: 16, Height: 9, Quality: 2})
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(sheet))
	if err != nil {
		t.Fatal(err)
	}
	for n, want := range []float64{0, 3, 9, 15} {
		// The middle of frame n.
		y, _, _, _ := img.At(n*16+8, 4).RGBA()
		// The video's luma is limited range, the JPEG's full.
		got := (float64(y>>8)*219/255 + 16 - 40) / 10
		if got < want-0.8 || got > want+0.8 {
			t.Errorf("frame %d (%ds) is from %.1fs, want the keyframe at %.0fs", n, n*5, got, want)
		}
	}
}
