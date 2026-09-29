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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/ulikunitz/xz"
)

// embeddedXZ is the xz-compressed ffmpeg binary for this platform, set by
// the embedded_<os>_<arch>.go files in builds with -tags ffmpeg, and empty
// otherwise.

// Embedded reports whether this program carries an ffmpeg binary.
func Embedded() bool {
	return len(embeddedXZ) > 0
}

var (
	extractOnce sync.Once
	extracted   string
	extractErr  error

	// cacheDir is where the embedded binary is extracted; a variable so
	// tests can redirect it.
	cacheDir = os.UserCacheDir
)

// extract decompresses the embedded binary to
// <cache dir>/gowebserver/ffmpeg/<content hash>/ffmpeg the first time it's
// called and returns that path. The hash is of the embedded data, so a
// program with a different ffmpeg extracts to a new directory, and a copy
// extracted by an earlier run of the same program is reused. The binary is
// written to a temporary file and renamed into place, so a crash can't
// leave a partial binary where a later run would find it.
func extract() (string, error) {
	extractOnce.Do(func() {
		extracted, extractErr = extractTo()
	})
	return extracted, extractErr
}

func extractTo() (string, error) {
	if !Embedded() {
		return "", ErrNotFound
	}
	base, err := cacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	sum := sha256.Sum256(embeddedXZ)
	dir := filepath.Join(base, "gowebserver", "ffmpeg", hex.EncodeToString(sum[:8]))
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	exe := filepath.Join(dir, name)
	if info, err := os.Stat(exe); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
		return exe, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	r, err := xz.NewReader(bytes.NewReader(embeddedXZ))
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, name+".*.tmp")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(tmp, r)
	err = errors.Join(err, tmp.Chmod(0o700), tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), exe)
	}
	if err != nil {
		rmErr := os.Remove(tmp.Name())
		// Another process may have extracted it first (on Windows, rename
		// fails when the target exists); its copy is complete.
		if info, statErr := os.Stat(exe); statErr == nil && info.Size() > 0 {
			return exe, nil
		}
		return "", errors.Join(err, rmErr)
	}
	return exe, nil
}
