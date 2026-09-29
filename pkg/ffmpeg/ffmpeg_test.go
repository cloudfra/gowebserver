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
	"sync"
	"testing"
	"time"

	"github.com/ulikunitz/xz"
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
	if p, err := f.Path(); err != nil || p != bin {
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

// withEmbedded makes the package behave as if it embedded bin, extracting
// into a fresh cache directory, and restores it afterwards.
func withEmbedded(t *testing.T, bin []byte) string {
	t.Helper()
	var b bytes.Buffer
	w, err := xz.NewWriter(&b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(bin); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	oldData, oldCache := embeddedXZ, cacheDir
	embeddedXZ = b.Bytes()
	cacheDir = func() (string, error) { return cache, nil }
	extractOnce = sync.Once{}
	t.Cleanup(func() {
		embeddedXZ, cacheDir = oldData, oldCache
		extractOnce = sync.Once{}
	})
	return cache
}

func TestEmbedded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as ffmpeg")
	}
	cache := withEmbedded(t, []byte("#!/bin/sh\necho embedded \"$@\"\n"))
	if !Embedded() {
		t.Fatal("Embedded() = false with embedded data")
	}
	f, err := Find("")
	if err != nil {
		t.Fatal(err)
	}
	if f.Source() != "embedded" {
		t.Errorf("Source = %q, want embedded", f.Source())
	}
	// Nothing is extracted until ffmpeg is used.
	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 0 {
		t.Errorf("extracted before use: %v (err %v)", entries, err)
	}
	out, err := f.Run(t.Context(), nil, "-version")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "embedded -version\n" {
		t.Errorf("stdout = %q", out)
	}
	path, err := f.Path()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, filepath.Join(cache, "gowebserver", "ffmpeg")) {
		t.Errorf("extracted to %q, want under the cache directory", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("extracted binary mode %v is not executable", info.Mode())
	}
	tmps, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*.tmp"))
	if err != nil || len(tmps) != 0 {
		t.Errorf("temporary files left behind: %v (err %v)", tmps, err)
	}

	// A later run of the same program reuses the extracted copy.
	extractOnce = sync.Once{}
	stamp := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	again, err := extract()
	if err != nil || again != path {
		t.Fatalf("second extract = %q, %v; want %q", again, err, path)
	}
	if info, err := os.Stat(path); err != nil || !info.ModTime().Equal(stamp) {
		t.Error("second extract rewrote the binary instead of reusing it")
	}
}

func TestEmbeddedChangesDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as ffmpeg")
	}
	withEmbedded(t, []byte("#!/bin/sh\necho one\n"))
	first, err := extract()
	if err != nil {
		t.Fatal(err)
	}
	withEmbedded(t, []byte("#!/bin/sh\necho two\n"))
	second, err := extract()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(first) == filepath.Dir(second) {
		t.Error("a different embedded ffmpeg extracted to the same directory")
	}
}

func TestNotEmbedded(t *testing.T) {
	withEmbedded(t, nil)
	embeddedXZ = nil
	if Embedded() {
		t.Error("Embedded() = true with no data")
	}
	if _, err := extract(); !errors.Is(err, ErrNotFound) {
		t.Errorf("extract error = %v, want ErrNotFound", err)
	}
}
