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

// Package update keeps gowebserver up to date with a release track.
//
// CI publishes a tracks file (Manifest) listing the build each track
// offers: "stable" is the latest tagged release and "unstable" the latest
// change merged to main. An Updater polls it (daily for stable, hourly for
// unstable), and when its track has a newer build than the one running,
// downloads the binary for its platform, checks its SHA-256 against the
// tracks file, swaps it in place of the running executable (keeping the
// previous one as <executable>.old), and restarts into it.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

var (
	// ErrUnofficialBuild means the running binary isn't a release or CI
	// build (its version doesn't parse), so it's never replaced.
	ErrUnofficialBuild = errors.New("update: not an official build")
	// ErrNoBinary means the track has no binary for this platform.
	ErrNoBinary = errors.New("update: no binary for this platform")
)

const (
	// startDelay is the wait before the first check, so a binary that
	// fails soon after starting doesn't loop through updates.
	startDelay = time.Minute
	// maxBinarySize bounds a download.
	maxBinarySize = 512 << 20
)

// Options configures an Updater.
type Options struct {
	// Track to follow: TrackStable or TrackUnstable.
	Track string
	// Current is the running binary's version.
	Current string
	// ManifestURL is the tracks file; empty means DefaultManifestURL.
	ManifestURL string
	// Executable is the binary to replace; empty means the running one.
	Executable string
	// Platform to download for; empty means this one.
	Platform string
	// Client makes requests; nil uses a default client.
	Client *http.Client
	// Restart switches to the new binary at the executable's path. The
	// default re-executes it in place of this process (Linux, macOS), or
	// exits for the service manager to start it (Windows). It returns only
	// on failure.
	Restart func(executable string) error
}

// Result is the outcome of a check.
type Result struct {
	// Latest is the version the track offers.
	Latest string `json:"latest"`
	// Updated is whether that version was installed; a restart follows.
	Updated bool `json:"updated"`
}

// Status describes the updater, for the debug endpoint.
type Status struct {
	Track     string    `json:"track"`
	Current   string    `json:"current"`
	Latest    string    `json:"latest,omitempty"`
	LastCheck time.Time `json:"lastCheck,omitzero"`
	LastError string    `json:"lastError,omitempty"`
	NextCheck time.Time `json:"nextCheck,omitzero"`
	Updated   string    `json:"updated,omitempty"` // version installed, awaiting restart
}

// Updater checks a release track for new builds and installs them.
type Updater struct {
	o     Options
	check sync.Mutex // one check at a time

	mu     sync.Mutex
	status Status
}

// New returns an Updater following o.
func New(o Options) (*Updater, error) {
	if !ValidTrack(o.Track) {
		return nil, fmt.Errorf("update: unknown track %q: use %q or %q", o.Track, TrackStable, TrackUnstable)
	}
	if o.ManifestURL == "" {
		o.ManifestURL = DefaultManifestURL
	}
	if o.Executable == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("update: %w", err)
		}
		if exe, err = filepath.EvalSymlinks(exe); err != nil {
			return nil, fmt.Errorf("update: %w", err)
		}
		o.Executable = exe
	}
	if o.Platform == "" {
		o.Platform = Platform(runtime.GOOS, runtime.GOARCH)
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 10 * time.Minute}
	}
	if o.Restart == nil {
		o.Restart = restart
	}
	return &Updater{o: o, status: Status{Track: o.Track, Current: o.Current}}, nil
}

// Status returns the updater's state.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.status
}

// Run checks the track after a short delay and then at its interval (with
// some jitter, so servers don't all ask at once) until ctx ends, and
// restarts into any build it installs.
func (u *Updater) Run(ctx context.Context) {
	wait := startDelay
	for {
		u.mu.Lock()
		u.status.NextCheck = time.Now().Add(wait)
		u.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		res, err := u.Check(ctx)
		switch {
		case errors.Is(err, ErrUnofficialBuild):
			slog.Info("automatic updates are off for this build", "version", u.o.Current, "reason", err)
			return
		case err != nil:
			slog.Warn("update check failed", "track", u.o.Track, "error", err)
		case res.Updated:
			u.RestartNow()
			return
		}
		interval := Interval(u.o.Track)
		wait = interval + time.Duration(rand.Int64N(int64(interval/10)))
	}
}

// Check asks the track for its build and installs it if it's newer than
// the running one. When Result.Updated is set, call RestartNow to switch.
func (u *Updater) Check(ctx context.Context) (Result, error) {
	u.check.Lock()
	defer u.check.Unlock()
	res, err := u.checkLocked(ctx)
	u.mu.Lock()
	u.status.LastCheck = time.Now()
	u.status.LastError = ""
	if err != nil {
		u.status.LastError = err.Error()
	}
	if res.Latest != "" {
		u.status.Latest = res.Latest
	}
	if res.Updated {
		u.status.Updated = res.Latest
	}
	u.mu.Unlock()
	return res, err
}

func (u *Updater) checkLocked(ctx context.Context) (Result, error) {
	if s := u.Status(); s.Updated != "" {
		return Result{Latest: s.Updated, Updated: true}, nil // installed, restart pending
	}
	current, err := ParseVersion(u.o.Current)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrUnofficialBuild, err)
	}
	m, err := u.manifest(ctx)
	if err != nil {
		return Result{}, err
	}
	b, ok := m.Tracks[u.o.Track]
	if !ok {
		return Result{}, fmt.Errorf("update: the tracks file has no %q track", u.o.Track)
	}
	res := Result{Latest: b.Version}
	latest, err := ParseVersion(b.Version)
	if err != nil {
		return res, err
	}
	if !current.Less(latest) {
		return res, nil
	}
	bin, ok := b.Binaries[u.o.Platform]
	if !ok {
		return res, fmt.Errorf("%w (%s)", ErrNoBinary, u.o.Platform)
	}
	slog.Info("installing update", "track", u.o.Track, "from", u.o.Current, "to", b.Version, "executable", u.o.Executable)
	if err := u.install(ctx, bin); err != nil {
		return res, fmt.Errorf("update: installing %s: %w", b.Version, err)
	}
	res.Updated = true
	return res, nil
}

func (u *Updater) manifest(ctx context.Context) (*Manifest, error) {
	resp, err := u.get(ctx, u.o.ManifestURL)
	if err != nil {
		return nil, err
	}
	defer closeBody(resp)
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return nil, fmt.Errorf("update: reading the tracks file: %w", err)
	}
	return &m, nil
}

// install downloads bin next to the executable, checks its SHA-256, and
// swaps it in: the running executable becomes <executable>.old (a running
// executable can be renamed on every platform, Windows included) and the
// download takes its name.
func (u *Updater) install(ctx context.Context, bin Binary) error {
	exe := u.o.Executable
	info, err := os.Stat(exe)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(exe), filepath.Base(exe)+".new-*")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			if err := errors.Join(tmp.Close(), os.Remove(tmp.Name())); err != nil && !errors.Is(err, os.ErrClosed) {
				slog.Debug("cleaning up update download", "error", err)
			}
		}
	}()

	resp, err := u.get(ctx, bin.URL)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxBinarySize+1))
	closeBody(resp)
	if err != nil {
		return err
	}
	if n > maxBinarySize {
		return fmt.Errorf("download is over %d bytes", maxBinarySize)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != bin.SHA256 {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, bin.SHA256)
	}
	if err := tmp.Chmod(info.Mode().Perm() | 0o100); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	old := exe + ".old"
	if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		return errors.Join(err, os.Rename(old, exe))
	}
	keep = true
	return nil
}

// RestartNow switches to the installed build. It returns only if that
// fails.
func (u *Updater) RestartNow() {
	slog.Info("restarting into the update", "version", u.Status().Updated, "executable", u.o.Executable)
	if err := u.o.Restart(u.o.Executable); err != nil {
		slog.Error("restarting into the update failed; it takes effect on the next start", "error", err)
	}
}

func (u *Updater) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gowebserver/"+u.o.Current)
	resp, err := u.o.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		closeBody(resp)
		return nil, fmt.Errorf("update: GET %s: %s", url, resp.Status)
	}
	return resp, nil
}

func closeBody(resp *http.Response) {
	if err := resp.Body.Close(); err != nil {
		slog.Debug("closing update response", "error", err)
	}
}

// ServeHTTP serves the debug endpoint: GET reports the Status as JSON, and
// POST checks the track now, installs a newer build, and restarts into it
// once the response is sent.
func (u *Updater) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writeJSON(w, http.StatusOK, u.Status())
	case http.MethodPost:
		res, err := u.Check(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"result": res, "error": err.Error(), "status": u.Status()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": res, "status": u.Status()})
		if res.Updated {
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			go func() {
				time.Sleep(500 * time.Millisecond) // let the response reach the client
				u.RestartNow()
			}()
		}
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET for status or POST to check now"})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		slog.Debug("writing update status", "error", err)
	}
}
