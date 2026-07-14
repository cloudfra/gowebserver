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

package testing

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var defaultFileData = []byte("ok")

type memCloser struct{}

func (memCloser) Close() error {
	return nil
}

func TestMustFile(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "filename")
	MustFile(t, filename, defaultFileData)
	assertFile(t, filename, defaultFileData)
}

func TestDeferClose(t *testing.T) {
	f := DeferClose(t, memCloser{})
	f()
	f()
}

func TestRemoveLineFeed(t *testing.T) {
	want := "abc\r\n123"
	got := RemoveLineFeed(want)
	if runtime.GOOS == "windows" {
		want = "abc\n123"
	}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("RemoveLineFeed() mismatch (-want +got):\n%s", diff)
	}
}

func TestMustFilePath(t *testing.T) {
	testCases := []struct {
		name string
		f    func(testing.TB) string
	}{
		{name: "MustNoDirZipFilePath", f: MustNoDirZipFilePath},
		{name: "MustNestedZipFilePath", f: MustNestedZipFilePath},
		{name: "MustSingleZipFilePath", f: MustSingleZipFilePath},
		{name: "MustZipFilePath", f: MustZipFilePath},
		{name: "MustRarFilePath", f: MustRarFilePath},
		{name: "MustSevenZipFilePath", f: MustSevenZipFilePath},
		{name: "MustTarFilePath", f: MustTarFilePath},
		{name: "MustTarGzFilePath", f: MustTarGzFilePath},
		{name: "MustTarBzip2FilePath", f: MustTarBzip2FilePath},
		{name: "MustTarXzFilePath", f: MustTarXzFilePath},
		{name: "MustTarLz4FilePath", f: MustTarLz4FilePath},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			name := tc.f(t)
			if name == "" {
				t.Error("file name is empty")
			}
			data, err := os.ReadFile(name)
			if err != nil {
				t.Error(err)
			}
			if len(data) == 0 {
				t.Error("file contents are empty")
			}
		})
	}
}
