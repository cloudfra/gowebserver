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

// TEMPORARY instrumentation for diagnosing thumbnails while browsing. With
// -debug, the server and the file browser page write what happened to
// gowebserver-diagnostics.jsonl in the working directory, one JSON object
// a line, in clear text.
//
// No file or folder name is written. Every path segment becomes a token,
// the start of an HMAC-SHA256 of it under a key made at startup and never
// written: the same name gets the same token for the whole run, so a file
// can be followed through the log, but the tokens can't be turned back
// into names. Only extensions are kept. Errors are written as a category
// from a fixed list, never their text, which can hold paths and a video's
// metadata.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
	"golang.org/x/image/webp"
)

const diagFile = "gowebserver-diagnostics.jsonl"

type diagLog struct {
	mu  sync.Mutex
	f   *os.File
	key []byte
}

// diag is nil unless diagnostics are on.
var diag *diagLog

func startDiagnostics() (http.Handler, func() error, error) {
	f, err := os.OpenFile(diagFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, nil, errors.Join(err, f.Close())
	}
	diag = &diagLog{f: f, key: key}
	slog.Warn("DIAGNOSTICS ON: writing " + diagFile)
	diagEvent("server", "start", nil)
	return http.HandlerFunc(diagHandler), f.Close, nil
}

func diagEvent(source, event string, fields map[string]any) {
	if diag == nil {
		return
	}
	rec := map[string]any{"t": time.Now().Format(time.RFC3339Nano), "src": source, "ev": event}
	for k, v := range fields {
		rec[k] = v
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	diag.mu.Lock()
	defer diag.mu.Unlock()
	_, _ = diag.f.Write(append(b, '\n'))
}

// diagHandler takes the page's events: JSON objects, one a line.
func diagHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	sc := bufio.NewScanner(io.LimitReader(r.Body, 4<<20))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	ua := r.Header.Get("User-Agent")
	for sc.Scan() {
		var in map[string]any
		if json.Unmarshal(sc.Bytes(), &in) != nil {
			continue
		}
		// Only known fields, with any URL anonymized: the page could
		// otherwise put a name in anything.
		fields := map[string]any{"ua": ua}
		for k, v := range in {
			switch k {
			case "url", "page":
				if s, ok := v.(string); ok {
					fields[k] = anonURL(s)
				}
			case "ok", "ms", "status", "w", "h", "nw", "nh", "kind", "phase", "n", "idx", "count",
				"radius", "hidden", "fs", "stripOn", "thumbs", "videoThumbs", "vw", "vh", "dpr",
				"proto", "loading", "queued", "visible", "rect", "display", "opacity", "children",
				"ready", "err", "plain", "rows", "tiles", "why", "size", "decoded", "bytes", "cache", "timing", "pt":
				if b, _ := json.Marshal(v); len(b) <= 512 && !strings.Contains(string(b), "/") {
					fields[k] = v
				}
			}
		}
		ev, _ := in["ev"].(string)
		if len(ev) > 40 {
			ev = ev[:40]
		}
		diagEvent("client", ev, fields)
	}
	w.WriteHeader(http.StatusNoContent)
}

// meanLuma is img's average brightness, 0 to 255, from a grid of samples.
func meanLuma(img image.Image) float64 {
	b := img.Bounds()
	if b.Empty() {
		return -1
	}
	const n = 32
	var sum float64
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			r, g, bl, _ := img.At(b.Min.X+x*b.Dx()/n, b.Min.Y+y*b.Dy()/n).RGBA()
			sum += 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(bl>>8)
		}
	}
	return sum / (n * n)
}

// anon is a token for the name, keeping only its extension.
func anon(name string) string {
	if diag == nil || name == "" || name == "." {
		return name
	}
	mac := hmac.New(sha256.New, diag.key)
	mac.Write([]byte(name))
	tok := hex.EncodeToString(mac.Sum(nil))[:10]
	if ext := path.Ext(name); ext != "" && len(ext) <= 6 {
		tok += strings.ToLower(ext)
	}
	return tok
}

// anonPath anonymizes each segment of a slash-separated path.
func anonPath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = anon(s)
	}
	return strings.Join(segs, "/")
}

// anonURL anonymizes a URL's path and keeps only the thumbnail query
// parameters; the scheme and host are dropped.
func anonURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "unparseable"
	}
	p, err := url.PathUnescape(u.EscapedPath())
	if err != nil {
		p = u.EscapedPath()
	}
	out := anonPath(p)
	q := url.Values{}
	for _, k := range []string{"width", "height", "fit", "format", "quality", "view"} {
		if v := u.Query().Get(k); v != "" && len(v) < 16 {
			q.Set(k, v)
		}
	}
	if len(q) > 0 {
		out += "?" + q.Encode()
	}
	return out
}

// errCategory names what kind of error err is, from a fixed list.
func errCategory(err error) string {
	if err == nil {
		return ""
	}
	var exit *exec.ExitError
	msg := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, errThumbnailUnsupported):
		return "unsupported"
	case errors.Is(err, ffmpeg.ErrNotFound):
		return "ffmpeg-not-found"
	case errors.Is(err, ffmpeg.ErrNoFrame):
		return "no-frame"
	case errors.Is(err, ffmpeg.ErrNoDuration):
		return "no-duration"
	case errors.Is(err, fs.ErrNotExist):
		return "not-exist"
	case errors.Is(err, fs.ErrPermission):
		return "permission"
	case strings.Contains(msg, "moov atom not found"):
		return "ffmpeg-moov-not-found"
	case strings.Contains(msg, "invalid data found"):
		return "ffmpeg-invalid-data"
	case strings.Contains(msg, "output file is empty"), strings.Contains(msg, "output file does not contain any stream"):
		return "ffmpeg-empty-output"
	case strings.Contains(msg, "does not contain any stream"), strings.Contains(msg, "matches no streams"):
		return "ffmpeg-no-video-stream"
	case strings.Contains(msg, "decoder") && strings.Contains(msg, "not found"),
		strings.Contains(msg, "unknown decoder"), strings.Contains(msg, "decoding requested"):
		return "ffmpeg-no-decoder"
	case strings.Contains(msg, "signal: killed"):
		return "ffmpeg-killed"
	case errors.As(err, &exit):
		return "ffmpeg-exit-" + strings.TrimPrefix(exit.ProcessState.String(), "exit status ")
	}
	return "other"
}

// diagName is the file a run of ffmpeg reads, for anonPath.
func diagName(input, pipe string) string {
	if pipe != "" {
		return pipe
	}
	return strings.TrimPrefix(input, "/")
}

// diagVideoFrame records how bright the frame taken from a video was: a
// black one is from an intro or a fade.
func diagVideoFrame(name string, src image.Image, whole []byte, err error) {
	if diag == nil {
		return
	}
	fields := map[string]any{"file": anon(path.Base(name)), "err": errCategory(err)}
	if src == nil && whole != nil {
		src, _ = webp.Decode(bytes.NewReader(whole))
	}
	if src != nil {
		fields["luma"] = int(meanLuma(src))
		b := src.Bounds()
		fields["w"], fields["h"] = b.Dx(), b.Dy()
	}
	diagEvent("server", "video-frame", fields)
}
