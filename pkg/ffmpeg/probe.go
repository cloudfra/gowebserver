// Copyright 2026 Cloudfra
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
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Chapter is a chapter of a video, as its container lists it.
type Chapter struct {
	Start, End time.Duration
	Title      string
}

// Info is what Probe learns about a video from its container.
type Info struct {
	// Duration is how long it lasts, or 0 when that isn't known.
	Duration time.Duration
	// Width and Height are the size its picture is shown at, after its
	// rotation; 0 when it has no video stream ffmpeg can describe.
	Width, Height int
	// FPS is its frame rate, or 0 when that isn't known.
	FPS float64
	// Chapters are the chapters its container lists, in order.
	Chapters []Chapter
}

var (
	// A video stream's line: its size comes after the codec and pixel
	// format, as ", 1920x1080"; a hex tag such as "0x31637661" isn't one.
	streamSize = regexp.MustCompile(`(?m)^\s*Stream #\d+:\d+.*?: Video: .*?[\s,](\d{1,5})x(\d{1,5})[\s,\[]`)
	streamFPS  = regexp.MustCompile(`(?m)^\s*Stream #\d+:\d+.*?: Video: .*?, (\d+(?:\.\d+)?) fps`)
	rotation   = regexp.MustCompile(`rotation of (-?\d+(?:\.\d+)?) degrees`)
)

// Probe describes the media input (a file path or URL, or "pipe:0" for
// stdin, as for Frame): its length, its picture's size and its chapters.
// It reads only the container's header and chapter list, so it's quick
// even for long videos. A video with no chapters gives none, not an
// error.
func (f *FFmpeg) Probe(ctx context.Context, input string, stdin io.Reader) (*Info, error) {
	// ffmetadata is a text format holding the input's metadata and
	// chapters and no streams, so nothing is decoded. ffmpeg describes the
	// input on stderr as it opens it.
	stdout, stderr, err := f.run(ctx, stdin, "-hide_banner", "-nostdin", "-i", input, "-f", "ffmetadata", "pipe:1")
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if msg := lastLine(stderr); msg != "" {
				return nil, fmt.Errorf("ffmpeg: %w: %s", err, msg)
			}
		}
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	info := &Info{Chapters: parseChapters(stdout)}
	if m := durationLine.FindSubmatch(stderr); m != nil {
		if d, err := time.ParseDuration(string(m[1]) + "h" + string(m[2]) + "m" + string(m[3]) + "s"); err == nil && d > 0 {
			info.Duration = d
		}
	}
	// The regular expressions only match digits, so these parse.
	if m := streamSize.FindSubmatch(stderr); m != nil {
		w, werr := strconv.Atoi(string(m[1]))
		h, herr := strconv.Atoi(string(m[2]))
		if werr == nil && herr == nil {
			info.Width, info.Height = w, h
		}
		if r := rotation.FindSubmatch(stderr); r != nil {
			if deg, err := strconv.ParseFloat(string(r[1]), 64); err == nil && math.Mod(math.Abs(math.Round(deg)), 180) == 90 {
				info.Width, info.Height = info.Height, info.Width
			}
		}
	}
	if m := streamFPS.FindSubmatch(stderr); m != nil {
		if fps, err := strconv.ParseFloat(string(m[1]), 64); err == nil {
			info.FPS = fps
		}
	}
	return info, nil
}

// lastLine is the last line of ffmpeg's stderr with something on it,
// which is where it says what went wrong.
func lastLine(stderr []byte) string {
	lines := strings.Split(strings.TrimSpace(string(stderr)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// parseChapters reads the chapters from ffmpeg's ffmetadata format: a
// [CHAPTER] section each, with its TIMEBASE, START, END and title. A
// chapter without a usable start and end is left out.
func parseChapters(data []byte) []Chapter {
	var chapters []Chapter
	var cur map[string]string
	flush := func() {
		if cur == nil {
			return
		}
		num, den := int64(1), int64(1000)
		if tb, ok := cur["TIMEBASE"]; ok {
			n, d, _ := strings.Cut(tb, "/")
			pn, err1 := strconv.ParseInt(n, 10, 64)
			pd, err2 := strconv.ParseInt(d, 10, 64)
			if err1 == nil && err2 == nil && pn > 0 && pd > 0 {
				num, den = pn, pd
			}
		}
		start, err1 := strconv.ParseInt(cur["START"], 10, 64)
		end, err2 := strconv.ParseInt(cur["END"], 10, 64)
		if err1 == nil && err2 == nil && start >= 0 && end > start {
			at := func(v int64) time.Duration {
				return time.Duration(float64(v) * float64(num) / float64(den) * float64(time.Second))
			}
			chapters = append(chapters, Chapter{Start: at(start), End: at(end), Title: cur["title"]})
		}
		cur = nil
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var pending string // a line continued with a trailing backslash
	for sc.Scan() {
		line := pending + sc.Text()
		pending = ""
		if strings.HasSuffix(line, `\`) && !strings.HasSuffix(line, `\\`) {
			pending = strings.TrimSuffix(line, `\`) + "\n"
			continue
		}
		switch {
		case line == "[CHAPTER]":
			flush()
			cur = map[string]string{}
		case strings.HasPrefix(line, "["):
			flush() // another section, such as [STREAM]
		case strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#"):
		case cur != nil:
			k, v := splitMeta(line)
			cur[k] = v
		}
	}
	flush()
	return chapters
}

// splitMeta splits an ffmetadata line at its first unescaped '=' and
// unescapes both sides: a backslash escapes '=', ';', '#', '\' and a
// newline.
func splitMeta(line string) (key, value string) {
	var b strings.Builder
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case escaped:
			b.WriteByte(c)
			escaped = false
		case c == '\\':
			escaped = true
		case c == '=' && key == "" && b.Len() > 0:
			key = b.String()
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	if key == "" {
		return b.String(), ""
	}
	return key, b.String()
}

// SpriteOptions describes a sheet of preview frames.
type SpriteOptions struct {
	// Interval is the time between frames: the first is from the start,
	// the next Interval later and so on.
	Interval time.Duration
	// Columns and Rows are how many frames the sheet holds across and
	// down, in reading order.
	Columns, Rows int
	// Width and Height are each frame's size on the sheet.
	Width, Height int
	// Quality is the JPEG quality ffmpeg uses, 2 (best) to 31.
	Quality int
}

// Sprite returns a JPEG sheet of frames from the video input (with input
// and stdin as for Frame) as o describes, for a preview of each point in
// the video. Only keyframes are decoded, so each frame is the keyframe
// at or before its time, and the sheet takes a fraction of the time
// decoding every frame would. Places past the end of a short video are
// left black.
func (f *FFmpeg) Sprite(ctx context.Context, input string, stdin io.Reader, o SpriteOptions) ([]byte, error) {
	if o.Interval <= 0 || o.Columns < 1 || o.Rows < 1 || o.Width < 1 || o.Height < 1 {
		return nil, errors.New("ffmpeg: sprite needs an interval, a layout and a frame size")
	}
	q := o.Quality
	if q < 2 || q > 31 {
		q = 5
	}
	filter := fmt.Sprintf("fps=fps=1/%s:round=down,scale=w=%d:h=%d:flags=bilinear,tile=%dx%d",
		strconv.FormatFloat(o.Interval.Seconds(), 'f', 3, 64), o.Width, o.Height, o.Columns, o.Rows)
	out, err := f.Run(ctx, stdin, "-hide_banner", "-loglevel", "error", "-nostdin",
		"-skip_frame", "nokey", "-i", input, "-an", "-sn", "-dn",
		"-vf", filter, "-frames:v", "1", "-c:v", "mjpeg", "-q:v", strconv.Itoa(q), "-f", "image2pipe", "pipe:1")
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNoFrame
	}
	return out, nil
}
