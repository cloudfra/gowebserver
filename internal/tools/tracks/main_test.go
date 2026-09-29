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

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudfra/gowebserver/pkg/update"
)

func TestRun(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"gowebserver-linux_amd64":       "linux",
		"gowebserver-windows_arm64.exe": "windows",
		"LICENSE":                       "not a binary",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(t.TempDir(), "tracks.json")
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	// The first run creates the file; the second sets the other track and
	// keeps the first.
	if err := run(manifest, update.TrackUnstable, "v3.9.0-4-gabc1234", "abc1234full", "https://example.com/unstable/", dir, now); err != nil {
		t.Fatal(err)
	}
	if err := run(manifest, update.TrackStable, "v3.9.0", "def5678full", "https://example.com/v3.9.0/", dir, now); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var m update.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	u, s := m.Tracks[update.TrackUnstable], m.Tracks[update.TrackStable]
	if u.Version != "v3.9.0-4-gabc1234" || u.Commit != "abc1234full" || !u.Published.Equal(now) {
		t.Errorf("unstable = %+v", u)
	}
	if s.Version != "v3.9.0" || s.Binaries["linux_amd64"].URL != "https://example.com/v3.9.0/gowebserver-linux_amd64" {
		t.Errorf("stable = %+v", s)
	}
	if len(u.Binaries) != 2 {
		t.Errorf("binaries %v, want linux_amd64 and windows_arm64 only", u.Binaries)
	}
	sum := sha256.Sum256([]byte("windows"))
	if got := u.Binaries["windows_arm64"]; got.SHA256 != hex.EncodeToString(sum[:]) || got.URL != "https://example.com/unstable/gowebserver-windows_arm64.exe" {
		t.Errorf("windows_arm64 = %+v", got)
	}
}

func TestRunRejects(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(t.TempDir(), "tracks.json")
	for name, args := range map[string][5]string{
		"unknown track":  {"nightly", "v3.9.0", "c", "https://x/", dir},
		"dirty version":  {"stable", "v3.9.0-1-gabc-dirty", "c", "https://x/", dir},
		"no commit":      {"stable", "v3.9.0", "", "https://x/", dir},
		"no binaries":    {"stable", "v3.9.0", "c", "https://x/", dir},
		"no such folder": {"stable", "v3.9.0", "c", "https://x/", filepath.Join(dir, "missing")},
	} {
		if err := run(manifest, args[0], args[1], args[2], args[3], args[4], time.Now()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := os.Stat(manifest); err == nil {
		t.Error("wrote a tracks file for rejected input")
	}
}
