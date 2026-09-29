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
	"testing"
)

func TestNewFFmpeg(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as ffmpeg")
	}
	t.Setenv("PATH", t.TempDir()) // none installed

	if ff := newFFmpeg(FFmpeg{}); ff != nil {
		t.Errorf("newFFmpeg with nothing installed = %v, want nil", ff.Source())
	}
	// Downloading needs the license accepted.
	if ff := newFFmpeg(FFmpeg{InstallOnDemand: true}); ff != nil {
		t.Errorf("newFFmpeg without accepting the license = %v, want nil", ff.Source())
	}
	if ff := newFFmpeg(FFmpeg{Path: filepath.Join(t.TempDir(), "missing")}); ff != nil {
		t.Errorf("newFFmpeg with a missing path = %v, want nil", ff.Source())
	}

	bin := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if ff := newFFmpeg(FFmpeg{Path: bin}); ff == nil || ff.Source() != bin {
		t.Errorf("newFFmpeg(path) = %v, want %q", ff, bin)
	}
	t.Setenv("PATH", filepath.Dir(bin))
	if ff := newFFmpeg(FFmpeg{}); ff == nil || ff.Source() != bin {
		t.Errorf("newFFmpeg with ffmpeg installed = %v, want %q", ff, bin)
	}
	// Installed wins over downloading, so this makes no request.
	if ff := newFFmpeg(FFmpeg{InstallOnDemand: true, AcceptLicense: true}); ff == nil || ff.Source() != bin {
		t.Errorf("newFFmpeg preferring installed = %v, want %q", ff, bin)
	}
}
