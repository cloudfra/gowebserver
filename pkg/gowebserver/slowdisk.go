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

package gowebserver

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/decorators/faultfs"
)

// A slow disk (monitoring.slowDisk) is for reproducing on a test server,
// often on an SSD with the files in memory, what a hard disk does to
// browsing. It comes in two parts:
//
//   - Latency and jitter on every file system call (open, stat, read
//     dir), with ufs's fault decorator.
//   - Files sent as a hard disk reads them: one read at a time, at a
//     set speed, with a seek to move anywhere but where the last read
//     ended, and read ahead, so reading a file through costs one seek.
//
// The second is done where files are sent, not in the file system:
// there the files would no longer be *os.File, which the thumbnailer
// hands to ffmpeg by name, and thumbnails would be made the slow way.

const (
	slowDiskDefaultSeek = 10 * time.Millisecond
	// slowDiskReadAhead is how much the disk reads at once, as the
	// kernel's read ahead would.
	slowDiskReadAhead = 1 << 20
)

type slowDisk struct {
	conf SlowDisk

	mu   sync.Mutex // the one read at a time
	last string     // file the last read was of
	end  int64      // and where in it that read ended
}

// newSlowDisk is the slow disk conf describes, or nil when it's off.
func newSlowDisk(conf SlowDisk) *slowDisk {
	if conf == (SlowDisk{}) {
		return nil
	}
	if conf.Seek == 0 {
		conf.Seek = slowDiskDefaultSeek
	}
	slog.Warn("serving files as from a slow disk (monitoring.slowDisk); for testing only",
		"latency", conf.Latency, "jitter", conf.Jitter, "readMBps", conf.ReadMBps, "seek", conf.Seek)
	return &slowDisk{conf: conf}
}

// fsSpec puts the file system spec behind the fault decorator, for the
// latency on each call.
func (d *slowDisk) fsSpec(spec string) (string, error) {
	if d == nil || (d.conf.Latency == 0 && d.conf.Jitter == 0) {
		return spec, nil
	}
	u, err := url.Parse(spec)
	if err != nil {
		return "", err
	}
	u, err = ufs.AppendURIOption(u, "fault", faultfs.Options{Latency: d.conf.Latency, LatencyJitter: d.conf.Jitter})
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// wrap sends the files next serves at the disk's speed. Thumbnails and
// the text viewer, which are made from files rather than sent as they
// are, go at full speed.
func (d *slowDisk) wrap(next http.Handler) http.Handler {
	if d == nil || d.conf.ReadMBps <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Has("width") || q.Has("height") || q.Has("view") {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(&slowDiskWriter{ResponseWriter: w, d: d, name: r.URL.Path, pos: rangeStart(r.Header.Get("Range")), status: http.StatusOK}, r)
	})
}

// read waits as long as the disk would take to read n bytes of name at
// off, unless a read ahead of the same response, [winStart, winEnd),
// already holds them, and returns the response's new read ahead. The
// disk does one read at a time, so a read waits for those before it.
func (d *slowDisk) read(name string, off, n, winStart, winEnd int64) (int64, int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	wait, winStart, winEnd := d.charge(name, off, n, winStart, winEnd)
	time.Sleep(wait)
	return winStart, winEnd
}

// charge is how long read takes, and the read ahead after it. d.mu is
// held.
func (d *slowDisk) charge(name string, off, n, winStart, winEnd int64) (time.Duration, int64, int64) {
	if n <= 0 || (off >= winStart && off+n <= winEnd) {
		return 0, winStart, winEnd
	}
	size := max(n, slowDiskReadAhead)
	wait := time.Duration(float64(size) / (d.conf.ReadMBps * 1e6) * float64(time.Second))
	if d.last != name || d.end != off {
		wait += d.conf.Seek
	}
	d.last, d.end = name, off+size
	return wait, off, off + size
}

// rangeStart is where the first range of a Range header starts, or 0.
func rangeStart(h string) int64 {
	spec, ok := strings.CutPrefix(h, "bytes=")
	if !ok {
		return 0
	}
	first, _, _ := strings.Cut(spec, ",")
	start, _, _ := strings.Cut(first, "-")
	n, err := strconv.ParseInt(strings.TrimSpace(start), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// slowDiskWriter charges the disk for a file's body as it's written, at
// the place in the file it comes from. It has no ReadFrom, so a file is
// copied to it in pieces rather than with sendfile.
type slowDiskWriter struct {
	http.ResponseWriter
	d                *slowDisk
	name             string
	pos              int64
	status           int
	winStart, winEnd int64
}

func (w *slowDiskWriter) WriteHeader(code int) {
	if code >= http.StatusOK {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *slowDiskWriter) Write(p []byte) (int, error) {
	if w.status == http.StatusOK || w.status == http.StatusPartialContent {
		w.winStart, w.winEnd = w.d.read(w.name, w.pos, int64(len(p)), w.winStart, w.winEnd)
		w.pos += int64(len(p))
	}
	return w.ResponseWriter.Write(p)
}

func (w *slowDiskWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap is for http.ResponseController.
func (w *slowDiskWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
