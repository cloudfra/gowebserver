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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseVersion(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Version
		ok   bool
	}{
		{"v3.9.0", Version{3, 9, 0, 0}, true},
		{"v3.9.0-4-gabc1234", Version{3, 9, 0, 4}, true},
		{"v10.0.12-120-g0123456789ab", Version{10, 0, 12, 120}, true},
		{"UNKNOWN", Version{}, false},
		{"v3.9.0-4-gabc1234-dirty", Version{}, false},
		{"3.9.0", Version{}, false},
		{"v3.9", Version{}, false},
	} {
		got, err := ParseVersion(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("ParseVersion(%q) = %+v, %v; want %+v, ok %v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

func TestVersionLess(t *testing.T) {
	order := []string{"v3.8.1", "v3.8.1-1-ga", "v3.8.1-10-gb", "v3.9.0", "v3.9.0-2-gc", "v3.10.0", "v4.0.0"}
	for i := range order {
		for j := range order {
			a, err := ParseVersion(order[i])
			if err != nil {
				t.Fatal(err)
			}
			b, err := ParseVersion(order[j])
			if err != nil {
				t.Fatal(err)
			}
			if got := a.Less(b); got != (i < j) {
				t.Errorf("%s < %s = %v, want %v", order[i], order[j], got, i < j)
			}
		}
	}
}

func TestPlatformOfFile(t *testing.T) {
	for in, want := range map[string]string{
		"gowebserver-linux_amd64":       "linux_amd64",
		"gowebserver-windows_arm64.exe": "windows_arm64",
		"LICENSE":                       "",
		"gowebserver-linux":             "",
	} {
		got, ok := PlatformOfFile(in)
		if ok != (want != "") || got != want && want != "" {
			t.Errorf("PlatformOfFile(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

// release serves a tracks file and binaries.
type release struct {
	mu       sync.Mutex
	manifest Manifest
	files    map[string][]byte
	hits     map[string]int
	srv      *httptest.Server
}

func newRelease(t *testing.T) *release {
	t.Helper()
	r := &release{files: map[string][]byte{}, hits: map[string]int{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		name := strings.TrimPrefix(req.URL.Path, "/")
		r.hits[name]++
		if name == "tracks.json" {
			if err := json.NewEncoder(w).Encode(r.manifest); err != nil {
				t.Errorf("encoding tracks: %v", err)
			}
			return
		}
		data, ok := r.files[name]
		if !ok {
			http.NotFound(w, req)
			return
		}
		if _, err := w.Write(data); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// publish makes version the track's build, with body as its linux_amd64
// binary. A non-empty sum replaces the true checksum.
func (r *release) publish(track, version, body, sum string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := "gowebserver-" + version
	r.files[name] = []byte(body)
	if sum == "" {
		h := sha256.Sum256([]byte(body))
		sum = hex.EncodeToString(h[:])
	}
	if r.manifest.Tracks == nil {
		r.manifest.Tracks = map[string]Build{}
	}
	r.manifest.Tracks[track] = Build{
		Version:   version,
		Published: time.Now(),
		Binaries:  map[string]Binary{"linux_amd64": {URL: r.srv.URL + "/" + name, SHA256: sum}},
	}
}

// installed makes an executable with body in a temporary directory.
func installed(t *testing.T, body string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "gowebserver")
	if err := os.WriteFile(exe, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func newUpdater(t *testing.T, r *release, track, current, exe string, restarts *atomic.Int32) *Updater {
	t.Helper()
	u, err := New(Options{
		Track:       track,
		Current:     current,
		ManifestURL: r.srv.URL + "/tracks.json",
		Executable:  exe,
		Platform:    "linux_amd64",
		Restart: func(string) error {
			if restarts != nil {
				restarts.Add(1)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestNewRejectsUnknownTrack(t *testing.T) {
	if _, err := New(Options{Track: "nightly"}); err == nil {
		t.Error("New accepted the track \"nightly\"")
	}
}

func TestCheckUpToDate(t *testing.T) {
	r := newRelease(t)
	r.publish(TrackStable, "v3.9.0", "new", "")
	exe := installed(t, "old")
	for _, current := range []string{"v3.9.0", "v3.9.0-5-gabc1234"} {
		// Same version, or a newer unstable build than the stable release:
		// never downgraded.
		res, err := newUpdater(t, r, TrackStable, current, exe, nil).Check(t.Context())
		if err != nil || res.Updated || res.Latest != "v3.9.0" {
			t.Errorf("running %s: %+v, %v; want no update", current, res, err)
		}
	}
	if r.hits["gowebserver-v3.9.0"] != 0 || readFile(t, exe) != "old" {
		t.Error("downloaded or replaced the executable while up to date")
	}
}

func TestCheckInstalls(t *testing.T) {
	r := newRelease(t)
	r.publish(TrackUnstable, "v3.9.0-3-gdef5678", "new binary", "")
	exe := installed(t, "old binary")
	var restarts atomic.Int32
	u := newUpdater(t, r, TrackUnstable, "v3.9.0-2-gabc1234", exe, &restarts)
	res, err := u.Check(t.Context())
	if err != nil || !res.Updated {
		t.Fatalf("Check = %+v, %v; want an update", res, err)
	}
	if got := readFile(t, exe); got != "new binary" {
		t.Errorf("executable holds %q, want the update", got)
	}
	if got := readFile(t, exe+".old"); got != "old binary" {
		t.Errorf(".old holds %q, want the previous binary", got)
	}
	// Windows has no execute bit; there the .exe name makes it runnable.
	if info, err := os.Stat(exe); err != nil || runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Errorf("update isn't executable: %v, %v", info.Mode(), err)
	}
	if left, err := filepath.Glob(exe + ".new-*"); err != nil || len(left) != 0 {
		t.Errorf("temporary files left: %v, %v", left, err)
	}
	if s := u.Status(); s.Updated != "v3.9.0-3-gdef5678" || s.Latest != s.Updated || s.LastError != "" {
		t.Errorf("status %+v", s)
	}
	// Until the restart, a check reports the pending update without
	// downloading again.
	hits := r.hits["gowebserver-v3.9.0-3-gdef5678"]
	if res, err := u.Check(t.Context()); err != nil || !res.Updated || r.hits["gowebserver-v3.9.0-3-gdef5678"] != hits {
		t.Errorf("second check = %+v, %v; downloads %d then %d", res, err, hits, r.hits["gowebserver-v3.9.0-3-gdef5678"])
	}
	if restarts.Load() != 0 {
		t.Error("Check restarted; that's up to the caller")
	}
}

func TestCheckRefusesBadChecksum(t *testing.T) {
	r := newRelease(t)
	r.publish(TrackStable, "v3.9.0", "tampered", strings.Repeat("0", 64))
	exe := installed(t, "old")
	res, err := newUpdater(t, r, TrackStable, "v3.8.1", exe, nil).Check(t.Context())
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") || res.Updated {
		t.Fatalf("Check = %+v, %v; want a checksum mismatch", res, err)
	}
	if readFile(t, exe) != "old" {
		t.Error("replaced the executable with a binary that failed its checksum")
	}
	left, err := filepath.Glob(filepath.Join(filepath.Dir(exe), "*"))
	if err != nil || len(left) != 1 {
		t.Errorf("files left beside the executable: %v, %v", left, err)
	}
}

func TestCheckUnofficialBuild(t *testing.T) {
	r := newRelease(t)
	r.publish(TrackStable, "v3.9.0", "new", "")
	exe := installed(t, "old")
	for _, current := range []string{"UNKNOWN", "v3.8.1-2-gabc1234-dirty"} {
		if _, err := newUpdater(t, r, TrackStable, current, exe, nil).Check(t.Context()); !errors.Is(err, ErrUnofficialBuild) {
			t.Errorf("running %s: %v, want ErrUnofficialBuild", current, err)
		}
	}
	if r.hits["tracks.json"] != 0 {
		t.Error("fetched the tracks file for a build that is never replaced")
	}
}

func TestCheckNoBinaryForPlatform(t *testing.T) {
	r := newRelease(t)
	r.publish(TrackStable, "v3.9.0", "new", "")
	u := newUpdater(t, r, TrackStable, "v3.8.1", installed(t, "old"), nil)
	u.o.Platform = "plan9_386"
	if _, err := u.Check(t.Context()); !errors.Is(err, ErrNoBinary) {
		t.Errorf("error = %v, want ErrNoBinary", err)
	}
}

func TestCheckMissingTrackOrBadManifest(t *testing.T) {
	r := newRelease(t)
	r.publish(TrackUnstable, "v3.9.0-1-gabc", "new", "")
	u := newUpdater(t, r, TrackStable, "v3.8.1", installed(t, "old"), nil)
	if _, err := u.Check(t.Context()); err == nil || !strings.Contains(err.Error(), `no "stable" track`) {
		t.Errorf("error = %v, want a missing track", err)
	}
	u.o.ManifestURL = r.srv.URL + "/missing.json"
	if _, err := u.Check(t.Context()); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %v, want a 404", err)
	}
	if s := u.Status(); s.LastError == "" || s.LastCheck.IsZero() {
		t.Errorf("status doesn't record the failure: %+v", s)
	}
}

func TestServeHTTP(t *testing.T) {
	r := newRelease(t)
	r.publish(TrackStable, "v3.9.0", "new", "")
	exe := installed(t, "old")
	var restarts atomic.Int32
	u := newUpdater(t, r, TrackStable, "v3.8.1", exe, &restarts)

	rec := httptest.NewRecorder()
	u.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/upgrade", nil))
	var s Status
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil || rec.Code != http.StatusOK || s.Track != TrackStable || s.Current != "v3.8.1" {
		t.Errorf("GET: %d %s (%v)", rec.Code, rec.Body, err)
	}
	if r.hits["tracks.json"] != 0 {
		t.Error("GET checked the track; only POST should")
	}

	rec = httptest.NewRecorder()
	u.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/debug/upgrade", nil))
	var body struct {
		Result Result `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK || !body.Result.Updated {
		t.Fatalf("POST: %d %s (%v)", rec.Code, rec.Body, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for restarts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if restarts.Load() != 1 {
		t.Error("POST installed an update but didn't restart into it")
	}

	rec = httptest.NewRecorder()
	u.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/debug/upgrade", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: %d, want 405", rec.Code)
	}
}
