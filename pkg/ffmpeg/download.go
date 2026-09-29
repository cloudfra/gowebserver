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
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ulikunitz/xz"
)

// Without Options.SourceURL, downloads come from BtbN's static builds of
// ffmpeg, https://github.com/BtbN/FFmpeg-Builds, which are rebuilt daily
// and published with SHA-256 checksums. They're full-featured GPL builds;
// see Options.AcceptLicense.

var (
	// ErrUnsupportedPlatform means there's no ffmpeg release built for
	// this OS and architecture; Options.SourceURL can name one.
	ErrUnsupportedPlatform = errors.New("ffmpeg: no release for this platform")
	// ErrUnsupportedArchive means Options.SourceURL isn't a .tar.xz,
	// .tar.gz or .zip file.
	ErrUnsupportedArchive = errors.New("ffmpeg: unsupported archive")
)

var (
	// Where releases are fetched from; variables so tests can use a local
	// server.
	releaseBaseURL = "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/"
	releaseAPIURL  = "https://api.github.com/repos/BtbN/FFmpeg-Builds/releases/latest"
	// The platform to download for; variables so tests can pick another.
	goos, goarch = runtime.GOOS, runtime.GOARCH
)

// releasePlatform returns the platform name BtbN's files use and the
// archive extension.
func releasePlatform() (string, string, error) {
	switch goos + "/" + goarch {
	case "linux/amd64":
		return "linux64", ".tar.xz", nil
	case "linux/arm64":
		return "linuxarm64", ".tar.xz", nil
	case "windows/amd64":
		return "win64", ".zip", nil
	case "windows/arm64":
		return "winarm64", ".zip", nil
	}
	return "", "", fmt.Errorf("%w (%s/%s)", ErrUnsupportedPlatform, goos, goarch)
}

// CanDownload reports whether a released ffmpeg build exists for this
// platform, i.e. whether a download without Options.SourceURL can work.
func CanDownload() bool {
	_, _, err := releasePlatform()
	return err == nil
}

func releaseAsset(version, platform, ext string) string {
	return "ffmpeg-n" + version + "-latest-" + platform + "-gpl-" + version + ext
}

// downloader installs ffmpeg into dir: a release into dir/<version>/, and
// a SourceURL into dir/url-<hash of the URL>/.
type downloader struct {
	dir       string
	sourceURL *url.URL // nil for the latest release
	client    *http.Client
}

func newDownloader(o Options) (*downloader, error) {
	d := &downloader{dir: o.DownloadDir, client: o.Client}
	if o.SourceURL != "" {
		u, err := url.Parse(o.SourceURL)
		if err != nil {
			return nil, fmt.Errorf("ffmpeg: source URL: %w", err)
		}
		if u.Scheme != "https" && u.Scheme != "http" {
			return nil, fmt.Errorf("ffmpeg: source URL %q is not http or https", o.SourceURL)
		}
		if archiveType(u.Path) == "" {
			return nil, fmt.Errorf("%w: %s", ErrUnsupportedArchive, path.Base(u.Path))
		}
		d.sourceURL = u
	} else if _, _, err := releasePlatform(); err != nil {
		return nil, err
	}
	if d.dir == "" {
		base, err := os.UserCacheDir()
		if err != nil || base == "" {
			base = os.TempDir()
		}
		d.dir = filepath.Join(base, "gowebserver", "ffmpeg")
	}
	if d.client == nil {
		d.client = &http.Client{Timeout: 15 * time.Minute}
	}
	return d, nil
}

// source describes what's downloaded, for logs.
func (d *downloader) source() string {
	if d.sourceURL != nil {
		return d.sourceURL.Redacted()
	}
	return "the latest ffmpeg release"
}

func exeName() string {
	if goos == "windows" {
		return "ffmpeg.exe"
	}
	return "ffmpeg"
}

// archiveType returns the archive extension of name, or "" if it isn't a
// supported archive.
func archiveType(name string) string {
	name = strings.ToLower(name)
	for _, ext := range []string{".tar.xz", ".txz", ".tar.gz", ".tgz", ".zip"} {
		if strings.HasSuffix(name, ext) {
			return ext
		}
	}
	return ""
}

// installed returns the ffmpeg in dir/sub if it's been downloaded.
func (d *downloader) installed(sub string) (string, bool) {
	exe := filepath.Join(d.dir, sub, exeName())
	info, err := os.Stat(exe)
	return exe, err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// install returns the path of the downloaded ffmpeg, downloading it first
// if it isn't in the download directory yet. A SourceURL is downloaded
// once and reused. Otherwise GitHub is asked which release is newest each
// time the program starts, falling back to the newest one already
// downloaded when it can't be reached.
func (d *downloader) install(ctx context.Context) (string, error) {
	if d.sourceURL != nil {
		sum := sha256.Sum256([]byte(d.sourceURL.String()))
		sub := "url-" + hex.EncodeToString(sum[:8])
		if exe, ok := d.installed(sub); ok {
			return exe, nil
		}
		return d.fetch(ctx, d.sourceURL, sub)
	}
	platform, ext, err := releasePlatform()
	if err != nil {
		return "", err
	}
	version, err := d.latestVersion(ctx, platform, ext)
	if err != nil {
		if have := d.newestInstalled(); have != "" {
			slog.Warn("can't check for the latest ffmpeg; using the newest downloaded", "version", have, "error", err)
			exe, _ := d.installed(have)
			return exe, nil
		}
		return "", err
	}
	if exe, ok := d.installed(version); ok {
		return exe, nil
	}
	u, err := url.Parse(releaseBaseURL + releaseAsset(version, platform, ext))
	if err != nil {
		return "", err
	}
	return d.fetch(ctx, u, version)
}

// latestVersion returns the newest release with a build for platform.
func (d *downloader) latestVersion(ctx context.Context, platform, ext string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseAPIURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("failed to close ffmpeg release listing", "error", err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ffmpeg: release listing: %s", resp.Status)
	}
	var release struct {
		Assets []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	re := regexp.MustCompile(`^ffmpeg-n([0-9]+(?:\.[0-9]+)*)-latest-` + regexp.QuoteMeta(platform) + `-gpl-([0-9.]+)` + regexp.QuoteMeta(ext) + `$`)
	var versions []string
	for _, a := range release.Assets {
		if m := re.FindStringSubmatch(a.Name); m != nil && m[1] == m[2] {
			versions = append(versions, m[1])
		}
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("ffmpeg: no release build for %s", platform)
	}
	sort.Slice(versions, func(i, j int) bool { return versionLess(versions[i], versions[j]) })
	return versions[len(versions)-1], nil
}

// newestInstalled returns the newest release version already downloaded.
func (d *downloader) newestInstalled() string {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return ""
	}
	newest := ""
	for _, e := range entries {
		v := e.Name()
		if _, err := strconv.Atoi(strings.Split(v, ".")[0]); err != nil {
			continue // not a release version, e.g. a SourceURL download
		}
		if _, ok := d.installed(v); !ok {
			continue
		}
		if newest == "" || versionLess(newest, v) {
			newest = v
		}
	}
	return newest
}

// versionLess compares dotted version numbers numerically: 9.0 < 10.0.
func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		x, y := versionPart(as, i), versionPart(bs, i)
		if x != y {
			return x < y
		}
	}
	return false
}

// versionPart returns part i of a split version, 0 when it's missing or
// not a number.
func versionPart(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n, err := strconv.Atoi(parts[i])
	if err != nil {
		return 0
	}
	return n
}

// fetch downloads the archive at u, checks its checksum, and installs its
// ffmpeg binary and license files into dir/sub. Files are written under
// temporary names and renamed into place, so an interrupted download never
// leaves a partial binary behind.
func (d *downloader) fetch(ctx context.Context, u *url.URL, sub string) (string, error) {
	name := path.Base(u.Path)
	dir := filepath.Join(d.dir, sub)
	exe := filepath.Join(dir, exeName())
	fail := func(err error) (string, error) {
		return "", fmt.Errorf("ffmpeg: download %s: %w", u.Redacted(), err)
	}
	want, err := d.checksum(ctx, u)
	if err != nil {
		return fail(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail(err)
	}
	archive, err := os.CreateTemp(dir, "download-*")
	if err != nil {
		return fail(err)
	}
	defer func() {
		if err := errors.Join(archive.Close(), os.Remove(archive.Name())); err != nil && !errors.Is(err, os.ErrClosed) {
			slog.Debug("cleaning up ffmpeg download", "error", err)
		}
	}()
	slog.Info("downloading ffmpeg", "url", u.Redacted(), "dir", dir)
	resp, err := d.get(ctx, withoutFragment(u))
	if err != nil {
		return fail(err)
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(archive, h), resp.Body)
	if err := errors.Join(err, resp.Body.Close()); err != nil {
		return fail(err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); want != "" && got != want {
		return fail(fmt.Errorf("checksum mismatch: got %s, want %s", got, want))
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return fail(err)
	}
	switch archiveType(name) {
	case ".zip":
		err = extractZip(archive, dir)
	case ".tar.gz", ".tgz":
		err = extractTar(archive, func(r io.Reader) (io.Reader, error) { return gzip.NewReader(r) }, dir)
	default:
		err = extractTar(archive, func(r io.Reader) (io.Reader, error) { return xz.NewReader(r) }, dir)
	}
	if err != nil {
		return fail(err)
	}
	if _, ok := d.installed(sub); !ok {
		return fail(fmt.Errorf("the archive has no %s", exeName()))
	}
	return exe, nil
}

func withoutFragment(u *url.URL) string {
	c := *u
	c.Fragment, c.RawFragment = "", ""
	return c.String()
}

// checksum returns the SHA-256 the archive at u should have, or "" when
// none is published, which is allowed only for a SourceURL. It comes from
// a "#sha256=<hex>" fragment on the URL, else from a checksums.sha256 file
// beside the archive, the layout BtbN's releases use.
func (d *downloader) checksum(ctx context.Context, u *url.URL) (string, error) {
	if v, ok := strings.CutPrefix(u.Fragment, "sha256="); ok {
		if _, err := hex.DecodeString(v); err != nil || len(v) != 64 {
			return "", fmt.Errorf("bad sha256 in source URL fragment: %q", v)
		}
		return strings.ToLower(v), nil
	}
	name := path.Base(u.Path)
	sums := *u
	sums.Fragment, sums.RawFragment, sums.RawQuery = "", "", ""
	sums.Path = path.Join(path.Dir(u.Path), "checksums.sha256")
	sums.RawPath = ""
	want, err := d.publishedChecksum(ctx, sums.String(), name)
	if err == nil {
		return want, nil
	}
	if d.sourceURL == nil {
		return "", err // releases are always published with checksums
	}
	slog.Warn("no checksum for the ffmpeg download; it can't be verified. Add #sha256=<hex> to the source URL to verify it", "url", u.Redacted(), "reason", err)
	return "", nil
}

// publishedChecksum returns the SHA-256 listed for name in the
// sha256sum-format file at sumsURL.
func (d *downloader) publishedChecksum(ctx context.Context, sumsURL, name string) (string, error) {
	resp, err := d.get(ctx, sumsURL)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("failed to close ffmpeg checksums", "error", err)
		}
	}()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		// "<hex>  <name>", sha256sum's format.
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no checksum published for %s", name)
}

func (d *downloader) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.Join(fmt.Errorf("GET %s: %s", url, resp.Status), resp.Body.Close())
	}
	return resp, nil
}

var licenseFile = regexp.MustCompile(`(?i)^(license|copying|gpl)`)

// wanted returns the name an archive member is installed as, if it's
// wanted: the ffmpeg binary, and the license files kept beside it. Builds
// lay these out differently (BtbN's are in bin/ under a top-level
// directory, others at the top), so members are matched by base name.
func wanted(member string) (string, bool) {
	base := path.Base(strings.ReplaceAll(member, `\`, "/"))
	if base == exeName() || licenseFile.MatchString(base) {
		return base, true
	}
	return "", false
}

func extractTar(r io.Reader, decompress func(io.Reader) (io.Reader, error), dir string) error {
	zr, err := decompress(bufio.NewReader(r))
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	done := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if out, ok := wanted(hdr.Name); ok && hdr.Typeflag == tar.TypeReg && !done[out] {
			if err := install(tr, filepath.Join(dir, out)); err != nil {
				return err
			}
			done[out] = true
		}
	}
}

func extractZip(f *os.File, dir string) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return err
	}
	done := map[string]bool{}
	for _, zf := range zr.File {
		out, ok := wanted(zf.Name)
		if !ok || zf.FileInfo().IsDir() || done[out] {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		err = install(rc, filepath.Join(dir, out))
		if err := errors.Join(err, rc.Close()); err != nil {
			return err
		}
		done[out] = true
	}
	return nil
}

// install writes r to dst through a temporary file renamed into place.
func install(r io.Reader, dst string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".*.tmp")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, r)
	err = errors.Join(err, tmp.Chmod(0o755), tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	return nil
}
