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

// Package testing contains test assets to verify gowebserver.
package testing

import (
	_ "embed"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const testFileMode = os.FileMode(0o644)

var (
	//go:embed nodir-testassets.zip
	nodirZipAssets []byte
	//go:embed single-testassets.zip
	singleZipAssets []byte
	//go:embed nested-testassets.zip
	nestedZipAssets []byte
	//go:embed testassets.zip
	zipAssets []byte
	//go:embed testassets.rar
	rarAssets []byte
	//go:embed testassets.7z
	sevenZipAssets []byte
	//go:embed testassets.tar
	tarAssets []byte
	//go:embed testassets.tar.gz
	tarGzAssets []byte
	//go:embed testassets.tar.bz2
	tarBzip2Assets []byte
	//go:embed testassets.tar.xz
	tarXzAssets []byte
	//go:embed testassets.tar.lz4
	tarLz4Assets []byte
)

// RemoveLineFeed normalizes Windows newlines to Linux style newlines.
func RemoveLineFeed(content string) string {
	if runtime.GOOS == "windows" {
		return strings.ReplaceAll(content, "\r\n", "\n")
	}
	return content
}

// IgnoreCarriageReturns normalizes Windows newlines to Linux style newlines.
func IgnoreCarriageReturns() cmp.Option {
	return cmp.Transformer("IgnoreCarriageReturns", RemoveLineFeed)
}

// DeferClose returns an error checked close that's deferred at the end of the test.
func DeferClose(tb testing.TB, closer io.Closer) func() {
	return func() {
		if err := closer.Close(); err != nil {
			tb.Fatalf("error closing file: %s", err)
		}
	}
}

// MustFile writes a file with the contents to the file system.
func MustFile(tb testing.TB, filename string, content []byte) {
	fatalOnFail(tb, os.WriteFile(filename, content, testFileMode))
}

// MustNoDirZipFilePath gets the .zip test asset file without explicit directory lists.
func MustNoDirZipFilePath(tb testing.TB) string {
	return mustWriteTempFileWithName(tb, "archive-nodir.zip", nodirZipAssets)
}

// MustSingleZipFilePath gets the .zip test asset file.
func MustSingleZipFilePath(tb testing.TB) string {
	return mustWriteTempFileWithName(tb, "archive-single.zip", singleZipAssets)
}

// MustNestedZipFilePath gets the .zip test asset file.
func MustNestedZipFilePath(tb testing.TB) string {
	return mustWriteTempFileWithName(tb, "archive-nested.zip", nestedZipAssets)
}

// MustZipFilePath gets the .zip test asset file.
func MustZipFilePath(tb testing.TB) string {
	return mustWriteTempFileWithName(tb, "archive.zip", zipAssets)
}

// MustRarFilePath gets the .rar test asset file.
func MustRarFilePath(tb testing.TB) string {
	return mustWriteTempFileWithName(tb, "archive.rar", rarAssets)
}

// MustSevenZipFilePath gets the .7z test asset file.
func MustSevenZipFilePath(tb testing.TB) string {
	return mustWriteTempFileWithName(tb, "archive.7z", sevenZipAssets)
}

// MustTarFilePath gets the .tar test asset file.
func MustTarFilePath(tb testing.TB) string {
	return mustWriteTempFileWithName(tb, "archive.tar", tarAssets)
}

// MustTarGzFilePath gets the .tar.gz test asset file.
func MustTarGzFilePath(tb testing.TB) string {
	return mustWriteTempFileWithName(tb, "archive.tar.gz", tarGzAssets)
}

// MustTarBzip2FilePath gets .tar.bz2 test asset file.
func MustTarBzip2FilePath(tb testing.TB) string {
	return mustWriteTempFileWithName(tb, "archive.tar.bz2", tarBzip2Assets)
}

// MustTarXzFilePath gets .tar.xz test asset file.
func MustTarXzFilePath(tb testing.TB) string {
	return mustWriteTempFileWithName(tb, "archive.tar.xz", tarXzAssets)
}

// MustTarLz4FilePath gets .tar.lz4 test asset file.
func MustTarLz4FilePath(tb testing.TB) string {
	return mustWriteTempFileWithName(tb, "archive.tar.lz4", tarLz4Assets)
}

func mustWriteTempFileWithName(tb testing.TB, filename string, content []byte) string {
	dir := tb.TempDir()
	name := filepath.Join(dir, filename)

	fatalOnFail(tb, os.WriteFile(name, content, testFileMode))
	return name
}

func assertFile(tb testing.TB, filename string, want []byte) {
	got, err := os.ReadFile(filename)
	if err != nil {
		tb.Error(err)
	}
	if diff := cmp.Diff(got, want); diff != "" {
		tb.Errorf("assertFile() mismatch (-want +got):\n%s", diff)
	}
}

func fatalOnFail(tb testing.TB, err error) {
	if err != nil {
		tb.Fatal(err)
	}
}
