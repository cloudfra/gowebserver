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

// Command tracks sets one release track's build in a tracks file, for CI
// to publish next to the binaries. The other track is left as it was.
//
//	go run ./internal/tools/tracks -manifest tracks.json -track unstable \
//	  -version "$(git describe --tags)" -commit "$GITHUB_SHA" \
//	  -base-url https://github.com/cloudfra/gowebserver/releases/download/unstable/ \
//	  -dir build/release
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudfra/gowebserver/pkg/update"
)

func main() {
	manifest := flag.String("manifest", "tracks.json", "Tracks file to update; created if missing.")
	track := flag.String("track", "", "Track to set: stable or unstable.")
	version := flag.String("version", "", "The build's `git describe --tags`.")
	commit := flag.String("commit", "", "The build's full commit hash.")
	baseURL := flag.String("base-url", "", "URL the binaries are published under, ending in a slash.")
	dir := flag.String("dir", "build/release", "Directory of release binaries named gowebserver-<os>_<arch>[.exe].")
	flag.Parse()
	if err := run(*manifest, *track, *version, *commit, *baseURL, *dir, time.Now().UTC()); err != nil {
		fmt.Fprintln(os.Stderr, "tracks:", err)
		os.Exit(1)
	}
}

func run(manifest, track, version, commit, baseURL, dir string, now time.Time) error {
	if !update.ValidTrack(track) {
		return fmt.Errorf("unknown track %q", track)
	}
	if _, err := update.ParseVersion(version); err != nil {
		return err
	}
	if commit == "" || baseURL == "" {
		return errors.New("-commit and -base-url are required")
	}

	var m update.Manifest
	switch data, err := os.ReadFile(manifest); {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("%s: %w", manifest, err)
		}
	}
	if m.Tracks == nil {
		m.Tracks = map[string]update.Build{}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	b := update.Build{Version: version, Commit: commit, Published: now, Binaries: map[string]update.Binary{}}
	for _, e := range entries {
		platform, ok := update.PlatformOfFile(e.Name())
		if !ok || e.IsDir() {
			continue
		}
		sum, err := sha256File(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		b.Binaries[platform] = update.Binary{URL: baseURL + e.Name(), SHA256: sum}
	}
	if len(b.Binaries) == 0 {
		return fmt.Errorf("no gowebserver-<os>_<arch> binaries in %s", dir)
	}
	m.Tracks[track] = b

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(manifest, append(data, '\n'), 0o644)
}

func sha256File(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	if err := errors.Join(err, f.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
