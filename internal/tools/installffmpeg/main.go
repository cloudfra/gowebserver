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

// Command installffmpeg puts a copy of ffmpeg at -o for the build: the
// latest release of BtbN's GPL build for this platform, which the tests
// that run ffmpeg find on PATH. The download is kept in -cache (by default
// the user cache directory, outside the repository, so `make clean` keeps
// it) and reused until a newer ffmpeg version is released.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
)

func main() {
	out := flag.String("o", "", "Where to put ffmpeg.")
	cache := flag.String("cache", "", "Where downloads are kept. Empty means <user cache dir>/gowebserver/ffmpeg.")
	source := flag.String("url", "", "The ffmpeg archive to install. Empty means the latest release.")
	flag.Parse()
	if err := run(*out, *cache, *source); err != nil {
		fmt.Fprintln(os.Stderr, "installffmpeg:", err)
		os.Exit(1)
	}
}

func run(out, cache, source string) error {
	if out == "" {
		return errors.New("-o is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	// The builds are GPL; the build only runs them, it doesn't ship them.
	exe, err := ffmpeg.Download(ctx, ffmpeg.Options{InstallOnDemand: true, AcceptLicense: true, DownloadDir: cache, SourceURL: source})
	if err != nil {
		return err
	}
	return copyFile(exe, out)
}

// copyFile copies src to dst, executable, replacing dst whole.
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".ffmpeg-*")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, in)
	err = errors.Join(err, tmp.Chmod(0o755), tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	return nil
}
