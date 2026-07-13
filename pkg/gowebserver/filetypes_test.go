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
	"testing"
)

func TestNameToIconClass(t *testing.T) {
	testCases := []struct {
		input string
		isDir bool
		want  string
	}{
		{input: "abc.webm", isDir: false, want: "video"},
		{input: "abc", isDir: false, want: "unknown"},
		{input: "abc", isDir: true, want: "folder"},
		{input: "abc.txt", isDir: false, want: "text"},
		{input: "abc.pdf", isDir: false, want: "pdf"},
		{input: "abc.doc", isDir: false, want: "doc"},
		{input: "abc.xls", isDir: false, want: "spreadsheet"},
		{input: "abc.ppt", isDir: false, want: "presentation"},
		{input: "abc.jpg", isDir: false, want: "image"},
		{input: "abc.png", isDir: false, want: "image"},
		{input: "abc.webp", isDir: false, want: "image"},
		{input: "abc.tiff", isDir: false, want: "image"},
		{input: "abc.mp4", isDir: false, want: "video"},
		{input: "abc.m4v", isDir: false, want: "video"},
		{input: "abc.m4a", isDir: false, want: "audio"},
		{input: "abc.avi", isDir: false, want: "video"},
		{input: "abc.wmv", isDir: false, want: "video"},
		{input: "abc.flv", isDir: false, want: "video"},
		{input: "abc.3gp", isDir: false, want: "video"},
		{input: "abc.mpeg", isDir: false, want: "video"},
		{input: "abc.mov", isDir: false, want: "video"},
		{input: "abc.qt", isDir: false, want: "video"},
		{input: "abc.xvid", isDir: false, want: "video"},
		{input: "abc.divx", isDir: false, want: "video"},
		{input: "abc.mp3", isDir: false, want: "audio"},
		{input: "abc.ogg", isDir: false, want: "audio"},
		{input: "abc.m4a", isDir: false, want: "audio"},
		{input: "abc.flac", isDir: false, want: "audio"},
		{input: "abc.wav", isDir: false, want: "audio"},
		{input: "abc.zip", isDir: false, want: "archive"},
		{input: "abc.tar.gz", isDir: false, want: "archive"},
		{input: "abc.tar", isDir: false, want: "archive"},
		{input: "abc.tar.bz2", isDir: false, want: "archive"},
		{input: "abc.tar.xz", isDir: false, want: "archive"},
		{input: "abc.7z", isDir: false, want: "archive"},
		{input: "abc.rar", isDir: false, want: "archive"},
		{input: "abc.cc", isDir: false, want: "code"},
		{input: "abc.cs", isDir: false, want: "code"},
		{input: "abc.java", isDir: false, want: "code"},
		{input: "abc.sh", isDir: false, want: "terminal"},
		{input: "abc.bash", isDir: false, want: "terminal"},
		{input: "abc.cmd", isDir: false, want: "terminal"},
		{input: "abc.ps1", isDir: false, want: "terminal"},
		{input: "abc.download", isDir: false, want: "download"},
		{input: "abc.exe", isDir: false, want: "terminal"},
		{input: "abc.sqlite", isDir: false, want: "database"},
		{input: "abc.db", isDir: false, want: "database"},
		{input: "abc.js", isDir: false, want: "script"},
		{input: "abc.ts", isDir: false, want: "script"},
		{input: "abc.tsx", isDir: false, want: "script"},
		{input: "abc.cert", isDir: false, want: "certificate"},
		{input: "abc.crt", isDir: false, want: "certificate"},
		{input: "abc.pub", isDir: false, want: "certificate"},
		{input: "abc.pem", isDir: false, want: "key"},
		{input: "abc.pk", isDir: false, want: "key"},
		{input: "abc.bak", isDir: false, want: "backup"},
		{input: "abc.bin", isDir: false, want: "binary"},
		{input: "abc.json", isDir: false, want: "config"},
		{input: "abc.psd", isDir: false, want: "photoshop"},
		{input: "abc.ai", isDir: false, want: "photoshop"},
		{input: "abc.ttf", isDir: false, want: "font"},
		{input: "abc.yaml", isDir: false, want: "config"},
		{input: "abc.yml", isDir: false, want: "config"},
		{input: "abc.ini", isDir: false, want: "config"},
		{input: "abc.cfg", isDir: false, want: "config"},
		{input: "abc.go", isDir: false, want: "code"},
		{input: ".DS_Store", isDir: false, want: "database"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := nameToIconClass(tc.isDir, tc.input)
			if got != tc.want {
				t.Errorf("got: %q, want: %q", got, tc.want)
			}
		})
	}
}

func TestIsRichViewable(t *testing.T) {
	tests := []struct {
		iconClass string
		want      bool
	}{
		{"code", true},
		{"terminal", true},
		{"text", true},
		{"markup", false},
		{"stylesheet", true},
		{"script", true},
		{"config", true},
		{"log", true},
		{"data", false},
		{"doc", true},
		{"key", true},
		{"certificate", true},
		{"image", false},
		{"video", false},
		{"audio", false},
		{"archive", false},
		{"pdf", false},
		{"binary", false},
		{"folder", false},
		{"unknown", false},
		{"database", false},
		{"spreadsheet", false},
	}
	for _, tt := range tests {
		t.Run(tt.iconClass, func(t *testing.T) {
			t.Parallel()
			got := isRichViewable(tt.iconClass)
			if got != tt.want {
				t.Errorf("isRichViewable(%q) = %v, want %v", tt.iconClass, got, tt.want)
			}
		})
	}
}

func BenchmarkIsRichViewable(b *testing.B) {
	for b.Loop() {
		isRichViewable("folder")
	}
}
