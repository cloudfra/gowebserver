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

package update

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Release tracks.
const (
	// TrackStable follows tagged releases, checked daily.
	TrackStable = "stable"
	// TrackUnstable follows every change merged to main, checked hourly.
	TrackUnstable = "unstable"
)

// DefaultManifestURL is where CI publishes the tracks file: the "unstable"
// release, next to the latest unstable binaries.
const DefaultManifestURL = "https://github.com/cloudfra/gowebserver/releases/download/unstable/tracks.json"

// Interval is how often a track is checked for a new build.
func Interval(track string) time.Duration {
	if track == TrackUnstable {
		return time.Hour
	}
	return 24 * time.Hour
}

// ValidTrack reports whether track names a release track.
func ValidTrack(track string) bool {
	return track == TrackStable || track == TrackUnstable
}

// Manifest is the tracks file: the build each track currently offers.
type Manifest struct {
	Tracks map[string]Build `json:"tracks"`
}

// Build is one build of gowebserver.
type Build struct {
	// Version is the build's `git describe --tags`, e.g. "v3.9.0" for a
	// release or "v3.9.0-4-gabc1234" four commits after it.
	Version string `json:"version"`
	// Commit is the full commit hash it was built from.
	Commit string `json:"commit"`
	// Published is when CI published it.
	Published time.Time `json:"published"`
	// Binaries are the build's executables by platform (see Platform).
	Binaries map[string]Binary `json:"binaries"`
}

// Binary is one platform's executable.
type Binary struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// Platform names an OS and architecture the way release files do, e.g.
// "linux_amd64" for gowebserver-linux_amd64.
func Platform(goos, goarch string) string {
	return goos + "_" + goarch
}

// PlatformOfFile returns the platform of a release file name such as
// "gowebserver-windows_arm64.exe".
func PlatformOfFile(name string) (string, bool) {
	p, ok := strings.CutPrefix(strings.TrimSuffix(name, ".exe"), "gowebserver-")
	return p, ok && strings.Count(p, "_") == 1
}

// Version is a build's position along main: a release plus the number of
// commits since it.
type Version struct {
	Major, Minor, Patch int
	// Commits since the release; 0 for the release itself.
	Commits int
}

var versionPattern = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(?:-(\d+)-g[0-9a-f]+)?$`)

// ParseVersion parses a `git describe --tags` version such as "v3.9.0" or
// "v3.9.0-4-gabc1234". Anything else, like a local build's "UNKNOWN" or a
// "-dirty" tree, isn't an official build and doesn't parse.
func ParseVersion(s string) (Version, error) {
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("update: %q is not a release version", s)
	}
	n := make([]int, 4)
	for i, part := range m[1:] {
		if part == "" {
			continue
		}
		v, err := strconv.Atoi(part)
		if err != nil {
			return Version{}, fmt.Errorf("update: %q: %w", s, err)
		}
		n[i] = v
	}
	return Version{Major: n[0], Minor: n[1], Patch: n[2], Commits: n[3]}, nil
}

// Less reports whether v comes before o.
func (v Version) Less(o Version) bool {
	a := [4]int{v.Major, v.Minor, v.Patch, v.Commits}
	b := [4]int{o.Major, o.Minor, o.Patch, o.Commits}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
