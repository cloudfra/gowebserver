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
// The binary comes from, in order: a path the caller gives, a copy
// embedded in this program, or ffmpeg on PATH. Programs built with
// "-tags ffmpeg" embed a static ffmpeg for the platforms listed in
// Makefile_ffmpeg.mk; the embedded copy is extracted to the user's cache
// directory the first time ffmpeg is run, and reused after that.
package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrNotFound means no ffmpeg is available: none was given, none is
	// embedded, and none is on PATH.
	ErrNotFound = errors.New("ffmpeg: not found")
	// ErrNoFrame means ffmpeg ran but produced no frame, e.g. because the
	// video is shorter than the requested time.
	ErrNoFrame = errors.New("ffmpeg: no frame")
)

// FFmpeg runs one ffmpeg binary.
type FFmpeg struct {
	// path is the binary to run; empty means the embedded copy, which is
	// extracted on first use.
	path string
}

// Find returns an FFmpeg for the binary at path, or when path is empty,
// the embedded binary or else ffmpeg on PATH. It doesn't extract the
// embedded binary; that happens the first time ffmpeg is run.
func Find(path string) (*FFmpeg, error) {
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("ffmpeg: %w", err)
		}
		return &FFmpeg{path: path}, nil
	}
	if Embedded() {
		return &FFmpeg{}, nil
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return &FFmpeg{path: p}, nil
	}
	return nil, ErrNotFound
}

// Source describes where the binary comes from, for logs: its path, or
// "embedded".
func (f *FFmpeg) Source() string {
	if f.path == "" {
		return "embedded"
	}
	return f.path
}

// Path returns the binary's path, extracting the embedded binary if that's
// the one in use and it hasn't been extracted yet.
func (f *FFmpeg) Path() (string, error) {
	if f.path != "" {
		return f.path, nil
	}
	return extract()
}

// Command returns an exec.Cmd that runs ffmpeg with args, for callers that
// need more control than Run gives.
func (f *FFmpeg) Command(ctx context.Context, args ...string) (*exec.Cmd, error) {
	p, err := f.Path()
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, p, args...), nil
}

// Run runs ffmpeg with args, feeding it stdin (which may be nil), and
// returns what it wrote to stdout. A failure includes ffmpeg's stderr.
func (f *FFmpeg) Run(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	cmd, err := f.Command(ctx, args...)
	if err != nil {
		return nil, err
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("ffmpeg: %w: %s", err, msg)
		}
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	return stdout.Bytes(), nil
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

// Version returns the first line of "ffmpeg -version".
func (f *FFmpeg) Version(ctx context.Context) (string, error) {
	out, err := f.Run(ctx, nil, "-version")
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line), nil
}
