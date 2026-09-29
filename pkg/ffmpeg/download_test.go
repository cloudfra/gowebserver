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
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ulikunitz/xz"
)

// fakeRelease serves a BtbN-style release: archives, checksums.sha256 and
// the GitHub release listing.
type fakeRelease struct {
	mu       sync.Mutex
	files    map[string][]byte
	hits     map[string]int
	badSums  bool
	notFound bool
}

func (r *fakeRelease) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := strings.TrimPrefix(req.URL.Path, "/")
	r.hits[name]++
	if r.notFound {
		http.NotFound(w, req)
		return
	}
	switch name {
	case "api":
		var assets []map[string]string
		for n := range r.files {
			assets = append(assets, map[string]string{"name": n})
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"assets": assets}); err != nil {
			panic(err)
		}
	case "checksums.sha256":
		for n, data := range r.files {
			sum := sha256.Sum256(data)
			if r.badSums {
				sum[0]++
			}
			if _, err := fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), n); err != nil {
				panic(err)
			}
		}
	default:
		data, ok := r.files[name]
		if !ok {
			http.NotFound(w, req)
			return
		}
		if _, err := w.Write(data); err != nil {
			panic(err)
		}
	}
}

// serveRelease points the package at a fake release for goos/goarch and
// restores it afterwards.
func serveRelease(t *testing.T, os, arch string) *fakeRelease {
	t.Helper()
	r := &fakeRelease{files: map[string][]byte{}, hits: map[string]int{}}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	oldBase, oldAPI, oldOS, oldArch := releaseBaseURL, releaseAPIURL, goos, goarch
	releaseBaseURL, releaseAPIURL, goos, goarch = srv.URL+"/", srv.URL+"/api", os, arch
	t.Cleanup(func() { releaseBaseURL, releaseAPIURL, goos, goarch = oldBase, oldAPI, oldOS, oldArch })
	return r
}

const fakeFFmpegScript = "#!/bin/sh\necho downloaded \"$@\"\n"

// tarXZ builds a release archive with top-level directory top holding
// bin/<exe> and LICENSE.txt.
func tarXZ(t *testing.T, top, exe, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(xw)
	for _, f := range []struct{ name, body string }{
		{top + "/LICENSE.txt", "GNU GENERAL PUBLIC LICENSE"},
		{top + "/bin/ffprobe", "not this one"},
		{top + "/bin/" + exe, body},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(tw.Close(), xw.Close()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipArchive(t *testing.T, top, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct{ name, body string }{
		{top + "/LICENSE.txt", "GNU GENERAL PUBLIC LICENSE"},
		{top + "/bin/ffmpeg.exe", body},
	} {
		w, err := zw.Create(f.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// tarGz builds a .tar.gz with files laid out flat under top, as some
// builds (e.g. johnvansickle.com's) are.
func tarGz(t *testing.T, top, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, f := range []struct{ name, body string }{
		{top + "/GPLv3.txt", "GNU GENERAL PUBLIC LICENSE"},
		{top + "/ffprobe", "not this one"},
		{top + "/ffmpeg", body},
		{top + "/manpages/ffmpeg.txt", "not this one either"},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(tw.Close(), gw.Close()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func linuxRelease(t *testing.T, r *fakeRelease, versions ...string) {
	t.Helper()
	for _, v := range versions {
		top := "ffmpeg-n" + v + "-latest-linux64-gpl-" + v
		r.files[top+".tar.xz"] = tarXZ(t, top, "ffmpeg", strings.Replace(fakeFFmpegScript, "downloaded", "downloaded-"+v, 1))
	}
}

// noInstalled hides any ffmpeg on the test machine's PATH.
func noInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
}

func skipScripts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs a shell script as ffmpeg")
	}
}

func onDemand(dir string) Options {
	return Options{InstallOnDemand: true, AcceptLicense: true, DownloadDir: dir}
}

func TestNotFound(t *testing.T) {
	r := serveRelease(t, "linux", "amd64")
	noInstalled(t)
	// Accepting the license alone doesn't download.
	if _, err := New(Options{AcceptLicense: true, DownloadDir: t.TempDir()}); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
	if len(r.hits) != 0 {
		t.Errorf("made requests %v without InstallOnDemand", r.hits)
	}
}

func TestDownloadNeedsLicense(t *testing.T) {
	serveRelease(t, "linux", "amd64")
	noInstalled(t)
	_, err := New(Options{InstallOnDemand: true, DownloadDir: t.TempDir()})
	if !errors.Is(err, ErrLicenseNotAccepted) || !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrLicenseNotAccepted wrapping ErrNotFound", err)
	}
}

func TestExplicitPathMissing(t *testing.T) {
	r := serveRelease(t, "linux", "amd64")
	o := onDemand(t.TempDir())
	o.Path = filepath.Join(t.TempDir(), "missing")
	if _, err := New(o); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
	if len(r.hits) != 0 {
		t.Errorf("downloaded %v instead of reporting the missing path", r.hits)
	}
}

func TestInstalledWins(t *testing.T) {
	skipScripts(t)
	r := serveRelease(t, "linux", "amd64")
	linuxRelease(t, r, "9.0")
	bin, _ := script(t, "echo installed")
	t.Setenv("PATH", filepath.Dir(bin))
	f, err := New(onDemand(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if v, err := f.Version(t.Context()); err != nil || v != "installed" {
		t.Errorf("ran %q, %v; want the installed ffmpeg", v, err)
	}
	if len(r.hits) != 0 {
		t.Errorf("made requests %v though ffmpeg is installed", r.hits)
	}
}

func TestDownloadOnFirstUse(t *testing.T) {
	skipScripts(t)
	r := serveRelease(t, "linux", "amd64")
	linuxRelease(t, r, "9.0")
	noInstalled(t)
	dir := t.TempDir()

	f, err := New(onDemand(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.Source(), "latest ffmpeg release") {
		t.Errorf("Source = %q", f.Source())
	}
	if len(r.hits) != 0 {
		t.Errorf("New made requests %v; it should wait until ffmpeg is used", r.hits)
	}
	v, err := f.Version(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v, "downloaded-9.0") {
		t.Errorf("ran %q, want the downloaded ffmpeg", v)
	}
	exe := filepath.Join(dir, "9.0", "ffmpeg")
	if p, err := f.Path(t.Context()); err != nil || p != exe {
		t.Errorf("Path = %q, %v; want %q", p, err, exe)
	}
	if lic, err := os.ReadFile(filepath.Join(dir, "9.0", "LICENSE.txt")); err != nil || !strings.Contains(string(lic), "GNU") {
		t.Errorf("license not kept with the binary: %q, %v", lic, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "9.0", "ffprobe")); err == nil {
		t.Error("extracted files other than ffmpeg")
	}
	if left, err := filepath.Glob(filepath.Join(dir, "9.0", "*.tmp")); err != nil || len(left) != 0 {
		t.Errorf("temporary files left behind: %v, %v", left, err)
	}

	// A later run checks the release listing but reuses the download.
	archive := "ffmpeg-n9.0-latest-linux64-gpl-9.0.tar.xz"
	g, err := New(onDemand(dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Version(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.hits[archive] != 1 {
		t.Errorf("downloaded %d times, want once", r.hits[archive])
	}
}

func TestDownloadChecksumMismatch(t *testing.T) {
	skipScripts(t)
	r := serveRelease(t, "linux", "amd64")
	linuxRelease(t, r, "9.0")
	r.badSums = true
	noInstalled(t)
	dir := t.TempDir()
	f, err := New(onDemand(dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Version(t.Context()); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %v, want a checksum mismatch", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "9.0", "ffmpeg")); err == nil {
		t.Error("installed a binary that failed its checksum")
	}
}

func TestDownloadLatest(t *testing.T) {
	skipScripts(t)
	r := serveRelease(t, "linux", "amd64")
	linuxRelease(t, r, "8.1", "9.0", "10.0")
	noInstalled(t)
	dir := t.TempDir()
	f, err := New(onDemand(dir))
	if err != nil {
		t.Fatal(err)
	}
	if v, err := f.Version(t.Context()); err != nil || !strings.HasPrefix(v, "downloaded-10.0") {
		t.Errorf("latest ran %q, %v; want 10.0 (numeric, not string, order)", v, err)
	}

	// Offline, it falls back to the newest one downloaded.
	r.notFound = true
	g, err := New(onDemand(dir))
	if err != nil {
		t.Fatal(err)
	}
	if v, err := g.Version(t.Context()); err != nil || !strings.HasPrefix(v, "downloaded-10.0") {
		t.Errorf("offline latest ran %q, %v; want the downloaded 10.0", v, err)
	}
}

func TestDownloadWindowsZip(t *testing.T) {
	// Windows builds are zips with bin/ffmpeg.exe; only the extraction is
	// checked here, since the "binary" can't run on the test machine.
	r := serveRelease(t, "windows", "arm64")
	top := "ffmpeg-n9.0-latest-winarm64-gpl-9.0"
	r.files[top+".zip"] = zipArchive(t, top, "MZ fake exe")
	dir := t.TempDir()
	d, err := newDownloader(Options{DownloadDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	exe, err := d.install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if exe != filepath.Join(dir, "9.0", "ffmpeg.exe") {
		t.Errorf("installed at %q", exe)
	}
	if data, err := os.ReadFile(exe); err != nil || string(data) != "MZ fake exe" {
		t.Errorf("ffmpeg.exe = %q, %v", data, err)
	}
}

func TestDownloadUnsupportedPlatform(t *testing.T) {
	serveRelease(t, "freebsd", "amd64")
	noInstalled(t)
	if CanDownload() {
		t.Error("CanDownload() = true on freebsd")
	}
	if _, err := New(onDemand(t.TempDir())); !errors.Is(err, ErrNotFound) || !errors.Is(err, ErrUnsupportedPlatform) {
		t.Errorf("error = %v, want ErrNotFound and ErrUnsupportedPlatform", err)
	}
}

func TestDownloadRetryAfterFailure(t *testing.T) {
	skipScripts(t)
	r := serveRelease(t, "linux", "amd64")
	linuxRelease(t, r, "9.0")
	r.notFound = true
	noInstalled(t)
	f, err := New(onDemand(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Version(t.Context()); err == nil {
		t.Fatal("download should have failed")
	}
	hits := r.hits["api"]
	if _, err := f.Version(t.Context()); err == nil {
		t.Fatal("second use should report the remembered failure")
	}
	if r.hits["api"] != hits {
		t.Error("retried the download immediately instead of waiting")
	}
	// Once the wait is over it tries again, and succeeds.
	r.notFound = false
	f.mu.Lock()
	f.failed = time.Now().Add(-retryAfter)
	f.mu.Unlock()
	if v, err := f.Version(t.Context()); err != nil || !strings.HasPrefix(v, "downloaded") {
		t.Errorf("after the wait ran %q, %v", v, err)
	}
}

// sourceURL serves archive at /custom/<name> and returns its URL.
func sourceURL(t *testing.T, r *fakeRelease, name string, archive []byte) string {
	t.Helper()
	r.files["custom/"+name] = archive
	return strings.TrimSuffix(releaseBaseURL, "/") + "/custom/" + name
}

func TestSourceURL(t *testing.T) {
	skipScripts(t)
	// Works on a platform with no release, since the URL names the build.
	r := serveRelease(t, "freebsd", "amd64")
	noInstalled(t)
	u := sourceURL(t, r, "ffmpeg-static.tar.gz", tarGz(t, "ffmpeg-7.0.2-amd64-static", strings.Replace(fakeFFmpegScript, "downloaded", "custom", 1)))
	dir := t.TempDir()
	o := onDemand(dir)
	o.SourceURL = u
	f, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.Source(), u) {
		t.Errorf("Source = %q, want it to name %q", f.Source(), u)
	}
	// No checksum is published beside it, so it's installed unverified.
	if v, err := f.Version(t.Context()); err != nil || !strings.HasPrefix(v, "custom") {
		t.Fatalf("ran %q, %v; want the source URL's ffmpeg", v, err)
	}
	p, err := f.Path(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Base(filepath.Dir(p))
	if !strings.HasPrefix(sub, "url-") || filepath.Dir(filepath.Dir(p)) != dir {
		t.Errorf("installed at %q, want %s/url-<hash>/ffmpeg", p, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, sub, "GPLv3.txt")); err != nil {
		t.Errorf("license not kept with the binary: %v", err)
	}
	for _, other := range []string{"ffprobe", "ffmpeg.txt"} {
		if _, err := os.Stat(filepath.Join(dir, sub, other)); err == nil {
			t.Errorf("extracted %s", other)
		}
	}
	if r.hits["api"] != 0 {
		t.Error("looked up the latest release though a source URL was set")
	}

	// A later run reuses the download without touching the network.
	hits := len(r.hits)
	g, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Version(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := len(r.hits); n != hits {
		t.Errorf("second run made requests: %v", r.hits)
	}
}

func TestSourceURLChecksumFragment(t *testing.T) {
	skipScripts(t)
	r := serveRelease(t, "linux", "amd64")
	noInstalled(t)
	archive := tarGz(t, "top", fakeFFmpegScript)
	u := sourceURL(t, r, "ffmpeg.tgz", archive)
	sum := sha256.Sum256(archive)

	good := onDemand(t.TempDir())
	good.SourceURL = u + "#sha256=" + hex.EncodeToString(sum[:])
	f, err := New(good)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Version(t.Context()); err != nil {
		t.Errorf("matching checksum: %v", err)
	}

	sum[0]++
	bad := onDemand(t.TempDir())
	bad.SourceURL = u + "#sha256=" + hex.EncodeToString(sum[:])
	f, err = New(bad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Version(t.Context()); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %v, want a checksum mismatch", err)
	}
}

func TestSourceURLPublishedChecksum(t *testing.T) {
	skipScripts(t)
	// A source URL beside a checksums.sha256 (e.g. a pinned BtbN build)
	// is checked against it.
	r := serveRelease(t, "linux", "amd64")
	linuxRelease(t, r, "9.0")
	r.badSums = true
	noInstalled(t)
	o := onDemand(t.TempDir())
	o.SourceURL = releaseBaseURL + "ffmpeg-n9.0-latest-linux64-gpl-9.0.tar.xz"
	f, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Version(t.Context()); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %v, want a checksum mismatch", err)
	}
}

func TestSourceURLInvalid(t *testing.T) {
	serveRelease(t, "linux", "amd64")
	noInstalled(t)
	for _, u := range []string{"https://example.com/ffmpeg.7z", "ftp://example.com/ffmpeg.zip", "https://example.com/ffmpeg.tar.xz#sha256=nothex"} {
		o := onDemand(t.TempDir())
		o.SourceURL = u
		f, err := New(o)
		if err == nil {
			_, err = f.Path(t.Context())
		}
		if err == nil {
			t.Errorf("source URL %q accepted", u)
		}
	}
	o := onDemand(t.TempDir())
	o.SourceURL = "https://example.com/ffmpeg.7z"
	if _, err := New(o); !errors.Is(err, ErrUnsupportedArchive) {
		t.Errorf("error = %v, want ErrUnsupportedArchive", err)
	}
}

func TestVersionLess(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"9.0", "10.0", true},
		{"10.0", "9.0", false},
		{"8.1", "8.10", true},
		{"9", "9.0.1", true},
		{"9.0", "9.0", false},
	} {
		if got := versionLess(tc.a, tc.b); got != tc.want {
			t.Errorf("versionLess(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}
