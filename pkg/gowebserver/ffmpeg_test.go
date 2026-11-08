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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
)

func TestNewFFmpeg(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as ffmpeg")
	}
	t.Setenv("PATH", t.TempDir()) // none installed

	mustNew := func(conf FFmpeg) *ffmpeg.FFmpeg {
		t.Helper()
		ff, err := newFFmpeg(conf)
		if err != nil {
			t.Fatalf("newFFmpeg(%+v): %v", conf, err)
		}
		return ff
	}
	if ff := mustNew(FFmpeg{}); ff != nil {
		t.Errorf("newFFmpeg with nothing installed = %v, want nil", ff.Source())
	}
	// Downloading needs the license accepted.
	if ff := mustNew(FFmpeg{InstallOnDemand: true}); ff != nil {
		t.Errorf("newFFmpeg without accepting the license = %v, want nil", ff.Source())
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "bin", "ffmpeg")
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'ffmpeg version test'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if ff := mustNew(FFmpeg{Path: bin}); ff == nil || ff.Source() != bin {
		t.Errorf("newFFmpeg(path) = %v, want %q", ff, bin)
	}
	// The folder ffmpeg is in, or the one above its bin folder.
	for _, folder := range []string{filepath.Dir(bin), dir} {
		if ff := mustNew(FFmpeg{Path: folder}); ff == nil || ff.Source() != bin {
			t.Errorf("newFFmpeg(%q) = %v, want %q", folder, ff, bin)
		}
	}
	t.Setenv("PATH", filepath.Dir(bin))
	if ff := mustNew(FFmpeg{}); ff == nil || ff.Source() != bin {
		t.Errorf("newFFmpeg with ffmpeg installed = %v, want %q", ff, bin)
	}
	// Installed wins over downloading, so this makes no request.
	if ff := mustNew(FFmpeg{InstallOnDemand: true, AcceptLicense: true}); ff == nil || ff.Source() != bin {
		t.Errorf("newFFmpeg preferring installed = %v, want %q", ff, bin)
	}
}

// A path that is set has to be ffmpeg, or its folder, and run: anything
// else stops the server with an error saying what's wrong.
func TestNewFFmpegBadPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as ffmpeg")
	}
	t.Setenv("PATH", t.TempDir())
	empty := t.TempDir()
	notProgram := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(notProgram, []byte("not a program"), 0o600); err != nil {
		t.Fatal(err)
	}
	failing := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(failing, []byte("#!/bin/sh\nexit 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		filepath.Join(empty, "missing"): "no such file",
		empty:                           "is a folder with no ffmpeg in it or in its bin folder",
		notProgram:                      "is not a program that can be run",
		failing:                         "doesn't run",
	} {
		ff, err := newFFmpeg(FFmpeg{Path: path})
		if err == nil || !strings.Contains(err.Error(), want) || !strings.HasPrefix(err.Error(), "ffmpeg.path: ") {
			t.Errorf("newFFmpeg(%q) = %v, %v; want an error with %q", path, ff, err, want)
		}
	}
}
