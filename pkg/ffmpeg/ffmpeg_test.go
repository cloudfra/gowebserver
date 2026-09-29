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
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

func pngFile(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "frame.png")
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
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
	frame := pngFile(t, 16, 9)
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
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-ss 1.500 -i pipe:0", "-frames:v 1", "-c:v png pipe:1"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q missing %q", args, want)
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
