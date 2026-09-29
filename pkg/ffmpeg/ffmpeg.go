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

// Package ffmpeg runs ffmpeg as a separate process.
//
// The binary is one the caller names, the one installed on PATH, or one
// this package downloads. Downloading is off unless the caller asks for it
// (Options.InstallOnDemand) and accepts ffmpeg's license
// (Options.AcceptLicense): the downloadable builds are GPL, and this
// package never ships ffmpeg itself, so a program using it stays under its
// own license. A download happens the first time ffmpeg is needed, not
// when New is called.
package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// ErrNotFound means no ffmpeg is available: none was named, none is
	// installed, and downloading one wasn't allowed or isn't possible.
	ErrNotFound = errors.New("ffmpeg: not found")
	// ErrLicenseNotAccepted means a download was asked for without
	// accepting ffmpeg's license. It wraps ErrNotFound.
	ErrLicenseNotAccepted = fmt.Errorf("%w: accept ffmpeg's license to let it be downloaded", ErrNotFound)
	// ErrNoFrame means ffmpeg ran but produced no frame, e.g. because the
	// video is shorter than the requested time.
	ErrNoFrame = errors.New("ffmpeg: no frame")
	// ErrNoDuration means ffmpeg couldn't tell how long the input is, as
	// with some streams read through a pipe.
	ErrNoDuration = errors.New("ffmpeg: duration unknown")
)

// retryAfter is how long a failed download is remembered before the next
// use of ffmpeg tries again.
const retryAfter = 10 * time.Minute

// Options chooses the ffmpeg binary.
type Options struct {
	// Path is the ffmpeg binary to use. Empty uses ffmpeg on PATH.
	Path string
	// InstallOnDemand downloads ffmpeg the first time it's needed when
	// Path is empty and none is installed. It needs AcceptLicense.
	InstallOnDemand bool
	// AcceptLicense accepts ffmpeg's license for a downloaded copy. The
	// builds are GPL (https://ffmpeg.org/legal.html).
	AcceptLicense bool
	// SourceURL is the archive (.tar.xz, .tar.gz or .zip) to download
	// ffmpeg from. Empty downloads the latest release built for this
	// platform.
	SourceURL string
	// DownloadDir is where downloads are kept. Empty means
	// <user cache dir>/gowebserver/ffmpeg.
	DownloadDir string
	// Client makes download requests; nil uses a default client.
	Client *http.Client
}

// FFmpeg runs one ffmpeg binary, which may be downloaded on first use.
type FFmpeg struct {
	mu      sync.Mutex
	path    string      // resolved binary, once known
	dl      *downloader // set when the binary is to be downloaded
	failed  time.Time   // last failed download
	failErr error
}

// New chooses an ffmpeg binary following o: o.Path, else ffmpeg on PATH,
// else a download when o allows one. It returns an error wrapping
// ErrNotFound when none is available. It doesn't download anything; that
// happens the first time ffmpeg is run.
func New(o Options) (*FFmpeg, error) {
	if o.Path != "" {
		if _, err := os.Stat(o.Path); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNotFound, err)
		}
		return &FFmpeg{path: o.Path}, nil
	}
	if installed, err := exec.LookPath("ffmpeg"); err == nil {
		return &FFmpeg{path: installed}, nil
	}
	if !o.InstallOnDemand {
		return nil, ErrNotFound
	}
	if !o.AcceptLicense {
		return nil, ErrLicenseNotAccepted
	}
	dl, err := newDownloader(o)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	return &FFmpeg{dl: dl}, nil
}

// Find returns an FFmpeg for the binary at path, or ffmpeg on PATH when
// path is empty. It never downloads.
func Find(path string) (*FFmpeg, error) {
	return New(Options{Path: path})
}

// Source describes the binary, for logs: its path, or the download it
// will come from.
func (f *FFmpeg) Source() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.path != "" {
		return f.path
	}
	return "download of " + f.dl.source() + " into " + f.dl.dir
}

// Path returns the binary's path, downloading it first if that's where it
// comes from and it isn't downloaded yet. After a failed download, it
// returns that error without trying again for a while.
func (f *FFmpeg) Path(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.path != "" {
		return f.path, nil
	}
	if f.failErr != nil && time.Since(f.failed) < retryAfter {
		return "", f.failErr
	}
	p, err := f.dl.install(ctx)
	if err != nil {
		f.failed, f.failErr = time.Now(), err
		slog.Warn("ffmpeg download failed", "error", err, "retry after", retryAfter)
		return "", err
	}
	f.path = p
	return p, nil
}

// Command returns an exec.Cmd that runs ffmpeg with args, for callers that
// need more control than Run gives.
func (f *FFmpeg) Command(ctx context.Context, args ...string) (*exec.Cmd, error) {
	p, err := f.Path(ctx)
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, p, args...), nil
}

// Run runs ffmpeg with args, feeding it stdin (which may be nil), and
// returns what it wrote to stdout. A failure includes ffmpeg's stderr.
func (f *FFmpeg) Run(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	stdout, stderr, err := f.run(ctx, stdin, args...)
	if err != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return nil, fmt.Errorf("ffmpeg: %w: %s", err, msg)
		}
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	return stdout, nil
}

func (f *FFmpeg) run(ctx context.Context, stdin io.Reader, args ...string) (stdout, stderr []byte, err error) {
	cmd, err := f.Command(ctx, args...)
	if err != nil {
		return nil, nil, err
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err = cmd.Run()
	return out.Bytes(), errOut.Bytes(), err
}

// Frame returns the frame shown at time at in the video input, a file path
// or URL. For input "pipe:0" the video is read from stdin, which works for
// streamable containers only, since ffmpeg can't seek a pipe. ffmpeg
// applies the video's rotation, so the frame is the right way up.
func (f *FFmpeg) Frame(ctx context.Context, input string, stdin io.Reader, at time.Duration) (image.Image, error) {
	out, err := f.Run(ctx, stdin,
		// -nostdin only stops ffmpeg reading commands from stdin; an input
		// of "pipe:0" is still read from it.
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-ss", strconv.FormatFloat(at.Seconds(), 'f', 3, 64), "-i", input,
		"-frames:v", "1", "-f", "image2pipe", "-c:v", "png", "pipe:1")
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNoFrame
	}
	return png.Decode(bytes.NewReader(out))
}

var durationLine = regexp.MustCompile(`Duration: (\d+):(\d{2}):(\d{2}(?:\.\d+)?)`)

// Duration returns how long the media input lasts, with input and stdin
// as for Frame. It reads only the container's header, so it's quick even
// for long videos. It returns ErrNoDuration when the length isn't known.
func (f *FFmpeg) Duration(ctx context.Context, input string, stdin io.Reader) (time.Duration, error) {
	// With no output ffmpeg describes the input on stderr and exits with
	// an error, so the exit status says nothing here.
	_, stderr, err := f.run(ctx, stdin, "-hide_banner", "-nostdin", "-i", input)
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	m := durationLine.FindSubmatch(stderr)
	if m == nil {
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			return 0, fmt.Errorf("ffmpeg: %w", err) // it didn't run
		}
		return 0, ErrNoDuration
	}
	d, err := time.ParseDuration(string(m[1]) + "h" + string(m[2]) + "m" + string(m[3]) + "s")
	if err != nil || d <= 0 {
		return 0, ErrNoDuration
	}
	return d, nil
}

// Version returns the first line of "ffmpeg -version".
func (f *FFmpeg) Version(ctx context.Context) (string, error) {
	out, err := f.Run(ctx, nil, "-version")
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line), nil
}
