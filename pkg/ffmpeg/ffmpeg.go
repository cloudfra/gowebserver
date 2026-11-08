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
	// ErrNotStarted is a run of ffmpeg that never started: the binary
	// can't be run, or couldn't be downloaded. Unlike ffmpeg failing on its
	// input, it fails the same way whatever the input.
	ErrNotStarted = errors.New("ffmpeg: could not start")
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

// Download downloads ffmpeg as o says, or finds the copy it downloaded
// before, and returns its path. Unlike New, it ignores o.Path and any
// ffmpeg installed on PATH, so it always gives the same build. It needs
// o.AcceptLicense.
func Download(ctx context.Context, o Options) (string, error) {
	if !o.AcceptLicense {
		return "", ErrLicenseNotAccepted
	}
	d, err := newDownloader(o)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	return d.install(ctx)
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
		if ctx.Err() == nil {
			err = fmt.Errorf("%w: %w", ErrNotStarted, err)
		}
		return nil, nil, err
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err = cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) && ctx.Err() == nil {
		// Not an exit: it never ran.
		err = fmt.Errorf("%w: %w", ErrNotStarted, err)
	}
	return out.Bytes(), errOut.Bytes(), err
}

// Frame returns the frame shown at time at in the video input, at its full
// size. See FrameWith.
func (f *FFmpeg) Frame(ctx context.Context, input string, stdin io.Reader, at time.Duration) (image.Image, error) {
	return f.FrameWith(ctx, input, stdin, FrameOptions{At: at})
}

// FrameOptions chooses the frame FrameWith returns and its size.
type FrameOptions struct {
	// At is the time in the video to take the frame from.
	At time.Duration
	// Keyframe takes the first keyframe at or after At instead of the
	// frame shown at exactly At. That decodes one frame instead of every
	// frame since the keyframe before At, which in a video with keyframes
	// far apart is hundreds.
	Keyframe bool
	// Width and Height, when either is set, shrink the frame at its own
	// ratio to the smallest size that covers Width x Height (a 0 leaves
	// that side to the other), or with Inside the largest that fits in
	// it. A frame already that small is left as it is: it's never
	// enlarged.
	Width, Height int
	Inside        bool
	// Reduce decodes a JPEG at 1/2, 1/4 or 1/8 of its size (1 to 3), in
	// a fraction of the time: the decoder skips the detail it would
	// throw away. Other inputs ignore it.
	Reduce int
	// Orientation, when set, is the EXIF orientation (1 to 8) to turn the
	// frame by instead of the rotation ffmpeg finds in the input, which
	// not every version of ffmpeg reads from a photo. Width and Height are
	// of the turned frame.
	Orientation int
}

// FrameWith returns a frame of the video input, a file path or URL, as o
// describes. For input "pipe:0" the video is read from stdin, which works
// for streamable containers only, since ffmpeg can't seek a pipe. ffmpeg
// applies the video's rotation, so the frame is the right way up. A photo
// is a video of one frame.
//
// ffmpeg runs on one thread: decoding a single frame gains little from
// more, and starting a pool per core costs more than it saves, especially
// when several frames are being made at once.
func (f *FFmpeg) FrameWith(ctx context.Context, input string, stdin io.Reader, o FrameOptions) (*image.RGBA, error) {
	// PAM is the frame's pixels after a short header giving its size.
	out, err := f.Run(ctx, stdin, append(frameArgs(input, o), "-f", "image2pipe", "-c:v", "pam", "-pix_fmt", "rgba", "pipe:1")...)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNoFrame
	}
	return decodePAM(out)
}

// FrameWebP is FrameWith, but returns the frame as a lossy WebP image at
// quality (1 to 100) for the libwebp encoder (see Encoders).
func (f *FFmpeg) FrameWebP(ctx context.Context, input string, stdin io.Reader, o FrameOptions, quality int) ([]byte, error) {
	out, err := f.Run(ctx, stdin, append(frameArgs(input, o), webpArgs(quality)...)...)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNoFrame
	}
	return out, nil
}

// EncodeWebP encodes img as a lossy WebP image at quality (1 to 100) with
// the libwebp encoder (see Encoders). An opaque image is encoded without
// an alpha channel.
func (f *FFmpeg) EncodeWebP(ctx context.Context, img *image.NRGBA, quality int) ([]byte, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	depth, tuple := 4, "RGB_ALPHA"
	if img.Opaque() {
		depth, tuple = 3, "RGB"
	}
	pam := bytes.NewBufferString(fmt.Sprintf("P7\nWIDTH %d\nHEIGHT %d\nDEPTH %d\nMAXVAL 255\nTUPLTYPE %s\nENDHDR\n", w, h, depth, tuple))
	pam.Grow(w * h * depth)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[img.PixOffset(b.Min.X, y):][:w*4]
		if depth == 4 {
			pam.Write(row)
			continue
		}
		for x := 0; x < len(row); x += 4 {
			pam.Write(row[x : x+3])
		}
	}
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-threads", "1", "-f", "image2pipe", "-c:v", "pam", "-i", "pipe:0", "-frames:v", "1"}, webpArgs(quality)...)
	return f.Run(ctx, pam, args...)
}

// webpArgs are the output arguments for a lossy WebP image on stdout.
// Compression level 2 of 6 is about half the work of the default 4, for
// files a few percent larger.
func webpArgs(quality int) []string {
	return []string{"-c:v", "libwebp", "-quality", strconv.Itoa(quality), "-compression_level", "2", "-f", "webp", "pipe:1"}
}

// frameArgs are the arguments that read the frame o describes from input
// and shrink and turn it, up to the output's.
func frameArgs(input string, o FrameOptions) []string {
	// -nostdin only stops ffmpeg reading commands from stdin; an input of
	// "pipe:0" is still read from it.
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-threads", "1", "-filter_threads", "1"}
	if o.Keyframe {
		args = append(args, "-skip_frame", "nokey")
	}
	if o.Reduce > 0 {
		args = append(args, "-lowres", strconv.Itoa(min(o.Reduce, 3)))
	}
	if o.Orientation > 0 {
		args = append(args, "-noautorotate")
	}
	// From the start needs no seek, and one in a piped photo finds nothing.
	if o.At > 0 {
		args = append(args, "-ss", strconv.FormatFloat(o.At.Seconds(), 'f', 3, 64))
	}
	args = append(args, "-i", input, "-frames:v", "1")
	var filters []string
	if o.Width > 0 || o.Height > 0 {
		w, h := o.Width, o.Height
		if o.Orientation >= 5 { // the frame is scaled before it's turned
			w, h = h, w
		}
		filters = append(filters, scaleFilter(w, h, o.Inside))
	}
	if turn := orientationFilter(o.Orientation); turn != "" {
		filters = append(filters, turn)
	}
	if len(filters) > 0 {
		args = append(args, "-vf", strings.Join(filters, ","))
	}
	return args
}

// scaleFilter is the filter that shrinks a frame to cover w x h, or fit
// inside it, as FrameOptions describes.
func scaleFilter(w, h int, inside bool) string {
	scale := fmt.Sprintf("min(1,max(%d/iw,%d/ih))", w, h)
	if inside {
		// A side left to the other doesn't limit the size.
		bw, bh := strconv.Itoa(w), strconv.Itoa(h)
		if w == 0 {
			bw = "iw"
		}
		if h == 0 {
			bh = "ih"
		}
		scale = fmt.Sprintf("min(1,min(%s/iw,%s/ih))", bw, bh)
	}
	return fmt.Sprintf("scale=w='max(1,round(iw*%[1]s))':h='max(1,round(ih*%[1]s))':flags=bicubic", scale)
}

// orientationFilter is the filter that turns a frame by an EXIF
// orientation, or "" for none.
func orientationFilter(orientation int) string {
	switch orientation {
	case 2:
		return "hflip"
	case 3:
		return "hflip,vflip"
	case 4:
		return "vflip"
	case 5:
		return "transpose=cclock_flip"
	case 6:
		return "transpose=clock"
	case 7:
		return "transpose=clock_flip"
	case 8:
		return "transpose=cclock"
	}
	return ""
}

// Encoders returns the names of the encoders this ffmpeg has, such as
// "libwebp".
func (f *FFmpeg) Encoders(ctx context.Context) (map[string]bool, error) {
	out, err := f.Run(ctx, nil, "-hide_banner", "-nostdin", "-encoders")
	if err != nil {
		return nil, err
	}
	// After a legend ending in a line of dashes, a line per encoder: its
	// capabilities, its name and a description.
	_, list, _ := strings.Cut(string(out), "------")
	encoders := map[string]bool{}
	for _, line := range strings.Split(list, "\n")[1:] {
		if f := strings.Fields(line); len(f) >= 2 {
			encoders[f[1]] = true
		}
	}
	return encoders, nil
}

// decodePAM decodes the RGB_ALPHA PAM image that ffmpeg writes for
// -c:v pam -pix_fmt rgba.
func decodePAM(data []byte) (*image.RGBA, error) {
	header, pix, ok := bytes.Cut(data, []byte("ENDHDR\n"))
	if !ok {
		return nil, errors.New("ffmpeg: frame has no PAM header")
	}
	lines := strings.Split(string(header), "\n")
	if lines[0] != "P7" {
		return nil, errors.New("ffmpeg: frame is not a PAM image")
	}
	fields := map[string]string{}
	for _, line := range lines[1:] {
		if k, v, ok := strings.Cut(line, " "); ok {
			fields[k] = v
		}
	}
	w, werr := strconv.Atoi(fields["WIDTH"])
	h, herr := strconv.Atoi(fields["HEIGHT"])
	switch {
	case werr != nil || herr != nil || w < 1 || h < 1:
		return nil, fmt.Errorf("ffmpeg: frame has no size: %q", header)
	case fields["DEPTH"] != "4" || fields["MAXVAL"] != "255":
		return nil, fmt.Errorf("ffmpeg: frame is not 8-bit RGBA: %q", header)
	case len(pix) != w*h*4:
		return nil, fmt.Errorf("ffmpeg: frame is %d bytes, want %d for %dx%d", len(pix), w*h*4, w, h)
	}
	return &image.RGBA{Pix: pix, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}, nil
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
