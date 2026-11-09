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
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
)

// The first thumbnails after the server starts are WebP already: they
// wait for the look at what ffmpeg writes, rather than being made, and
// stored, as JPEG meanwhile.
func TestThumbnailWebPFromTheStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as ffmpeg")
	}
	bin := filepath.Join(t.TempDir(), "ffmpeg")
	// Lists its encoders as ffmpeg does, a little slowly, and nothing else.
	script := "#!/bin/sh\ncase \"$*\" in *-encoders*) sleep 0.3; printf 'Encoders:\\n ------\\n V....D libwebp  libwebp WebP image\\n V....D mjpeg  MJPEG\\n';; esac\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ff, err := ffmpeg.Find(bin)
	if err != nil {
		t.Fatal(err)
	}
	th, err := newThumbnailer(Thumbnails{Enabled: true, CachePath: thumbnailCacheMemory}, ff, &monitoringContext{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := th.close(); err != nil {
			t.Error(err)
		}
	})
	if got := th.chooseWebP(&thumbnailSpec{}, browserAccept.Get("Accept")).format; got != "webp" {
		t.Errorf("first thumbnail's format %q, want webp", got)
	}
	// A browser without WebP doesn't wait for anything it can't use.
	if got := th.chooseWebP(&thumbnailSpec{}, "image/png,*/*").format; got != "" {
		t.Errorf("without WebP in Accept: %q, want it left to the image", got)
	}
}

// A WebP thumbnail replaces the JPEG or PNG of the same size, so the store
// keeps one of them; other sizes, and formats asked for by name, stay.
func TestThumbnailStoreWebPReplacesItsJPEG(t *testing.T) {
	s, err := newThumbnailStore("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.close(); err != nil {
			t.Error(err)
		}
	})
	put := func(name string) {
		t.Helper()
		if err := s.put(name, &thumbnail{data: []byte(name)}); err != nil {
			t.Fatal(err)
		}
	}
	put("src/a.jpg/512x512-cover@v1")      // made before WebP was known
	put("src/a.jpg/256x256-cover@v1")      // another size
	put("src/a.jpg/512x512-cover-jpeg@v1") // asked for as JPEG
	put("src/b.jpg/512x512-cover@v1")      // another file
	put("src/a.jpg/512x512-cover-webp@v1")
	for name, want := range map[string]bool{
		"src/a.jpg/512x512-cover@v1":      false,
		"src/a.jpg/512x512-cover-webp@v1": true,
		"src/a.jpg/256x256-cover@v1":      true,
		"src/a.jpg/512x512-cover-jpeg@v1": true,
		"src/b.jpg/512x512-cover@v1":      true,
	} {
		if _, ok := s.get(name); ok != want {
			t.Errorf("%s stored: %v, want %v", name, ok, want)
		}
	}
}
