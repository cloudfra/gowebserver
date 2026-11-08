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
// -diagnostics=FILE (on by default in a debug build, see
// defaultDiagnosticsLog), the server and the file browser page write what
// happened to FILE, one event a line, as plain text:
//
//	2026-11-08T19:33:25.327Z server thumbnail file=07c02dbee4.jpg dir=489653d9a3 kind=video result=made ms=812
//
// No file or folder name is written. Each one becomes a token, the first
// 10 hex digits of an HMAC-SHA256 of it under a random key made at startup
// and never written: the same name gets the same token for the whole run,
// so a file can be followed through the log, but a token can't be turned
// back into its name, nor matched against another run's. Only extensions
// are kept. Errors are written as a category from a fixed list, never
// their text, which can hold paths and a video's metadata. What the page
// sends is checked the same way: only known fields, URLs anonymized, and
// text only where a field has a fixed set of values it can be.

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
	"fmt"
	"image"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
	"golang.org/x/image/webp"
)

// defaultDiagnosticsLog is -diagnostics' default: empty, or a file name
// in a debug build, which sets it with
// -ldflags "-X github.com/cloudfra/gowebserver/pkg/gowebserver.defaultDiagnosticsLog=FILE".
var defaultDiagnosticsLog = ""

// diagEndpoint is where the page sends its events, under the server root.
const diagEndpoint = "__diag"

type diagLog struct {
	mu  sync.Mutex
	f   *os.File
	key []byte
}

// diag is nil unless diagnostics are on.
var diag *diagLog

// startDiagnostics opens the log at file and returns the handler for the
// page's events.
func startDiagnostics(file string) (http.Handler, func() error, error) {
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, nil, errors.Join(err, f.Close())
	}
	diag = &diagLog{f: f, key: key}
	slog.Warn("Diagnostics on: writing anonymized events", "file", file)
	_, _ = fmt.Fprintf(f, "# gowebserver diagnostics, started %s. Names are tokens (truncated HMAC-SHA256 under a key\n"+
		"# that is not stored), so they match only within this run, up to the next \"start\" line.\n", time.Now().UTC().Format(time.RFC3339))
	diagEvent("server", "start", nil)
	return http.HandlerFunc(diagHandler), func() error {
		diag.mu.Lock()
		defer diag.mu.Unlock()
		return f.Close()
	}, nil
}

// diagFirst are the fields written first, in this order; the rest follow
// sorted by name.
var diagFirst = []string{"file", "dir", "url", "page", "kind", "result", "ok", "err", "why", "ms"}

// diagEvent writes one event. Field values are strings, numbers, bools or
// lists of those.
func diagEvent(source, event string, fields map[string]any) {
	if diag == nil {
		return
	}
	var b strings.Builder
	b.WriteString(time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	b.WriteString(" " + source + " " + event)
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if !slices.Contains(diagFirst, k) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range append(slices.Clone(diagFirst), keys...) {
		v, ok := fields[k]
		if !ok {
			continue
		}
		s, ok := diagValue(v)
		if !ok {
			continue
		}
		b.WriteString(" " + k + "=" + s)
	}
	b.WriteString("\n")
	diag.mu.Lock()
	defer diag.mu.Unlock()
	_, _ = diag.f.WriteString(b.String())
}

// diagValue formats v for the log, quoted when it has a space; false for
// nothing worth writing.
func diagValue(v any) (string, bool) {
	var s string
	switch v := v.(type) {
	case nil:
		return "", false
	case string:
		if v == "" {
			return "", false
		}
		s = v
	case bool:
		s = strconv.FormatBool(v)
	case int:
		s = strconv.Itoa(v)
	case int64:
		s = strconv.FormatInt(v, 10)
	case float64:
		if v == math.Trunc(v) && math.Abs(v) < 1e15 {
			s = strconv.FormatInt(int64(v), 10)
		} else {
			s = strconv.FormatFloat(v, 'f', 2, 64)
		}
	case []any:
		parts := make([]string, 0, len(v))
		for _, e := range v {
			if p, ok := diagValue(e); ok {
				parts = append(parts, strings.Trim(p, `"`))
			}
		}
		s = strings.Join(parts, ",")
	default:
		s = fmt.Sprint(v)
	}
	if strings.ContainsAny(s, " \"=") {
		s = strconv.Quote(s)
	}
	return s, true
}

// diagSafeText is what an extension may be to be kept.
var diagSafeText = regexp.MustCompile(`^[A-Za-z0-9.]{0,8}$`)

var (
	diagEventName  = regexp.MustCompile(`^[a-z-]{1,24}$`)
	diagQueryValue = regexp.MustCompile(`^[a-z0-9]{1,8}$`)
)

// diagEnum accepts only the given values.
func diagEnum(values ...string) func(string) bool {
	return func(s string) bool { return slices.Contains(values, s) }
}

// diagCSS is a few CSS keywords, the page's display and visibility values.
var diagCSS = regexp.MustCompile(`^(none|block|flex|grid|inline|inline-block|inline-flex|contents|visible|hidden|collapse)(,(none|block|flex|grid|inline|inline-block|inline-flex|contents|visible|hidden|collapse)){0,5}$`)

// diagClientText is, for each field the page may send as text, the
// values it may have. A rule on characters isn't enough: a name like
// "Alice beach" is made of ordinary ones. Numbers and true/false are
// always fine; a field missing here can't be text.
var diagClientText = map[string]func(string) bool{
	"kind":    diagEnum("grid", "strip"),
	"phase":   diagEnum("metadata", "frame"),
	"cache":   diagEnum("hit", "net"),
	"why":     diagEnum("no thumbnails", "no metadata in 10s"),
	"proto":   diagEnum("h2", "h3", "http/1.1", "http/1.0", ""),
	"name":    diagEnum("Error", "TypeError", "RangeError", "ReferenceError", "SyntaxError", "EvalError", "URIError", "AbortError", "NotSupportedError", "NotAllowedError", "SecurityError", "InvalidStateError", "DOMException"),
	"display": diagCSS.MatchString,
	"opacity": regexp.MustCompile(`^[0-9.]{1,6}(,[0-9.]{1,6}){0,3}$`).MatchString,
	// Server-Timing, as the thumbnail handler writes it.
	"timing": regexp.MustCompile(`^((wait|decode|scale|encode|store|cache):[0-9]{1,7})?(,(wait|decode|scale|encode|store|cache):[0-9]{1,7}){0,6}$`).MatchString,
}

// diagClientFields are the fields the page may send, besides URLs.
var diagClientFields = map[string]bool{
	"ok": true, "ms": true, "pt": true, "status": true, "nw": true, "nh": true, "kind": true, "phase": true,
	"n": true, "idx": true, "count": true, "radius": true, "hidden": true, "fs": true, "stripOn": true,
	"thumbs": true, "videoThumbs": true, "vw": true, "vh": true, "dpr": true, "proto": true, "loading": true,
	"queued": true, "rect": true, "display": true, "opacity": true, "children": true, "ready": true,
	"err": true, "plain": true, "tiles": true, "why": true, "size": true, "bytes": true, "cache": true,
	"timing": true, "line": true, "col": true, "name": true,
}

// diagClean is field k's value v when it's safe to write: a number, a
// bool, one of the field's known texts, or a short list of numbers.
func diagClean(k string, v any) (any, bool) {
	switch v := v.(type) {
	case bool, float64:
		return v, true
	case string:
		ok := diagClientText[k]
		return v, ok != nil && ok(v)
	case []any:
		if len(v) > 8 {
			return nil, false
		}
		for _, e := range v {
			if _, num := e.(float64); !num {
				return nil, false
			}
		}
		return v, true
	}
	return nil, false
}

// diagHandler takes the page's events: JSON objects, one a line.
func diagHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	sc := bufio.NewScanner(io.LimitReader(r.Body, 4<<20))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var in map[string]any
		if json.Unmarshal(sc.Bytes(), &in) != nil {
			continue
		}
		ev, _ := in["ev"].(string)
		if !diagEventName.MatchString(ev) {
			continue
		}
		fields := map[string]any{}
		for k, v := range in {
			switch {
			case k == "url" || k == "page":
				if s, ok := v.(string); ok {
					fields[k] = anonURL(s)
				}
			case diagClientFields[k]:
				if c, ok := diagClean(k, v); ok {
					fields[k] = c
				}
			}
		}
		if ev == "page" {
			// The browser and device, once a page rather than every line.
			fields["browser"] = diagBrowser(r.Header.Get("User-Agent"))
		}
		diagEvent("client", ev, fields)
	}
	w.WriteHeader(http.StatusNoContent)
}

// diagBrowser is a short name for the browser in a User-Agent.
func diagBrowser(ua string) string {
	device := "desktop"
	switch {
	case strings.Contains(ua, "iPad"), strings.Contains(ua, "Macintosh") && strings.Contains(ua, "Mobile"):
		device = "ipad"
	case strings.Contains(ua, "iPhone"):
		device = "iphone"
	case strings.Contains(ua, "Android"):
		device = "android"
	}
	name := "other"
	for _, b := range []struct{ token, name string }{
		{"Edg/", "edge"}, {"CriOS/", "chrome"}, {"FxiOS/", "firefox"}, {"Firefox/", "firefox"},
		{"Chrome/", "chrome"}, {"Version/", "safari"},
	} {
		if i := strings.Index(ua, b.token); i >= 0 {
			name = b.name
			v := ua[i+len(b.token):]
			if j := strings.IndexAny(v, ". "); j > 0 {
				name += "-" + v[:j]
			}
			break
		}
	}
	return device + "," + name
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
	if diag == nil || name == "" || name == "." || name == "/" {
		return name
	}
	mac := hmac.New(sha256.New, diag.key)
	mac.Write([]byte(name))
	tok := hex.EncodeToString(mac.Sum(nil))[:10]
	if ext := strings.ToLower(path.Ext(name)); len(ext) > 1 && len(ext) <= 6 && diagSafeText.MatchString(ext) {
		tok += ext
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
		return "unparseable"
	}
	out := anonPath(p)
	q := url.Values{}
	for _, k := range []string{"width", "height", "fit", "format", "quality", "view"} {
		if v := u.Query().Get(k); v != "" && diagQueryValue.MatchString(v) {
			q.Set(k, v)
		}
	}
	if len(q) > 0 {
		out += "?" + q.Encode()
	}
	return out
}

// diagFile is the token for the file ffmpeg reads (input, or pipe when
// it's piped), matching the thumbnail's.
func diagFile(input, pipe string) string {
	if pipe != "" {
		return anon(path.Base(pipe))
	}
	base := path.Base(strings.ReplaceAll(input, `\`, "/"))
	if strings.HasPrefix(base, "gowebserver-video-") {
		return "temporary-copy" + strings.ToLower(path.Ext(base))
	}
	return anon(base)
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
		return "ffmpeg-exit-" + strconv.Itoa(exit.ExitCode())
	}
	return "other"
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

// diagPageEndpoint is the URL, relative to the page requested as
// requestURI, that its events go to: the server root's diagEndpoint, so
// it's found under a StripPrefix mount or behind a proxy's path too.
func diagPageEndpoint(requestURI string) string {
	if diag == nil {
		return ""
	}
	p, _, _ := strings.Cut(requestURI, "?")
	return "./" + strings.Repeat("../", max(strings.Count(p, "/")-1, 0)) + diagEndpoint
}
