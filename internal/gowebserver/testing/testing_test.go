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
	"testing"

	"github.com/google/go-cmp/cmp"
)

var defaultFileData = []byte("ok")

func TestCreateTempFile(t *testing.T) {
	testCases := []struct {
		name string
		f    func(testing.TB) (string, error)
	}{
		{
			name: "MustCreateTempFile",
			f: func(tb testing.TB) (string, error) {
				fp := MustCreateTempFile(tb)
				n, err := fp.Write(defaultFileData)
				if err != nil {
					return "", err
				}
				if n != len(defaultFileData) {
					t.Errorf("bytes written, got %d, want %d", n, len(defaultFileData))
				}

				if err := fp.Close(); err != nil {
					return "", err
				}
				return fp.Name(), nil
			},
		},
		{
			name: "MustWriteTempFile",
			f: func(tb testing.TB) (string, error) {
				fp := MustWriteTempFile(tb, string(defaultFileData))
				return fp.Name(), nil
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			name, err := tc.f(t)
			if err != nil {
				t.Fatal(err)
			}

			data, err := os.ReadFile(name)
			if err != nil {
				t.Error(err)
			}
			if diff := cmp.Diff(data, defaultFileData); diff != "" {
				t.Errorf("ExpandHostnames() mismatch (-want +got):\n%s", diff)
			}
		})
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
