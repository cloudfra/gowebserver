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

// Diagnostics: with -debug.diagnostics=FILE (debug: diagnostics: in the
// YAML configuration), the server and the file browser page write what
// happened while someone browses to FILE, one event a line, as plain
// text, to find out where browsing is slow or goes wrong:
//
//	2026-11-08T19:33:25.327Z server thumbnail file=07c02dbee4.jpg dir=489653d9a3 kind=video result=made ms=812 spec=512x288-outside
//	2026-11-08T19:33:25.401Z client thumb url="07c02dbee4.jpg?fit=outside&height=288&width=512" kind=grid ok=true ms=640 cache=net
//
// It's off unless asked for. No file or folder name is written. Each one
// becomes a token, the first 10 hex digits of an HMAC-SHA256 of it under a
// random key made at startup and never written: the same name gets the
// same token for the whole run, so a file can be followed through the
// log, but a token can't be turned back into its name, nor matched against
// another run's. A file keeps its extension; a folder keeps none. Errors
// are written as a category from a fixed list (see errCategory), never
// their text, which can hold paths and a video's metadata. What the page
// sends is checked the same way: only known fields, URLs anonymized, and
// text only where a field has a fixed set of values it can be. The file
// stops growing at diagMaxBytes, since anyone who can reach the server can
// send the page's events.

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
)

// diagEndpoint is where the page sends its events, under the server root.
const diagEndpoint = "__diag"

// diagMaxBytes is as large as the diagnostics file grows; past it events
// are dropped, and the file says so.
const diagMaxBytes = 256 << 20

type diagLog struct {
	key []byte // for anon; never written

	mu      sync.Mutex
	f       *os.File
	written int64
	full    bool // diagMaxBytes reached
	failed  bool // a write has failed, and been reported
}

// diagActive is the diagnostics log, or nil when diagnostics are off.
var diagActive atomic.Pointer[diagLog]

// diagEnabled reports whether diagnostics are on; callers check it before
// gathering an event's fields.
func diagEnabled() bool {
	return diagActive.Load() != nil
}

// startDiagnostics opens the log at file, appending, turns diagnostics on
// and returns the handler for the page's events and a function that turns
// them off and closes the file.
func startDiagnostics(file string) (http.Handler, func() error, error) {
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		return nil, nil, errors.Join(err, f.Close())
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, nil, errors.Join(err, f.Close())
	}
	d := &diagLog{key: key, f: f, written: info.Size()}
	if _, err := fmt.Fprintf(f, "# gowebserver diagnostics, started %s. Names are tokens (truncated HMAC-SHA256 under a key\n"+
		"# that is not stored), so they match only within this run, up to the next \"start\" line.\n", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return nil, nil, errors.Join(err, f.Close())
	}
	diagActive.Store(d)
	slog.Warn("Diagnostics on: writing anonymized browsing events", "file", file)
	diagEvent("server", "start", nil)
	return http.HandlerFunc(diagHandler), func() error {
		diagActive.CompareAndSwap(d, nil)
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.f.Close()
	}, nil
}

// diagFirst are the fields written first, in this order; the rest follow
// sorted by name.
var diagFirst = []string{"file", "dir", "url", "page", "kind", "result", "ok", "err", "why", "ms"}

// diagEvent writes one event, when diagnostics are on. Field values are
// strings, numbers, bools or lists of those.
func diagEvent(source, event string, fields map[string]any) {
	d := diagActive.Load()
	if d == nil {
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
	d.write(b.String())
}

// write appends line, up to diagMaxBytes.
func (d *diagLog) write(line string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.full {
		return
	}
	if d.written+int64(len(line)) > diagMaxBytes {
		d.full = true
		line = fmt.Sprintf("# diagnostics stopped: the file reached %d MB\n", diagMaxBytes>>20)
		slog.Warn("Diagnostics stopped: the file is full", "max MB", diagMaxBytes>>20)
	}
	n, err := d.f.WriteString(line)
	d.written += int64(n)
	if err != nil && !d.failed {
		// Once: a full disk would otherwise say so for every event.
		d.failed = true
		slog.Error("cannot write diagnostics", "error", err)
	}
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
	// Function names from a script error's stack: identifiers in this
	// page's own scripts, and "anonymous".
	"stack":   regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$.]{0,40}(<[A-Za-z_$][A-Za-z0-9_$.]{0,40}){0,7}$`).MatchString,
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
	"timing": true, "line": true, "col": true, "name": true, "stack": true,
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
		{"Edg/", "edge"},
		{"CriOS/", "chrome"},
		{"FxiOS/", "firefox"},
		{"Firefox/", "firefox"},
		{"Chrome/", "chrome"},
		{"Version/", "safari"},
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

// anon is a token for the name, keeping only its extension.
func anon(name string) string {
	d := diagActive.Load()
	if d == nil || name == "" || name == "." || name == "/" {
		return name
	}
	mac := hmac.New(sha256.New, d.key)
	mac.Write([]byte(name))
	tok := hex.EncodeToString(mac.Sum(nil))[:10]
	if ext := strings.ToLower(path.Ext(name)); len(ext) > 1 && len(ext) <= 6 && diagSafeText.MatchString(ext) {
		tok += ext
	}
	return tok
}

// anonPath anonymizes each segment of the slash-separated path of a
// file, or of a folder when it ends in a slash. Only the file's own name
// keeps its extension: a folder's "extension" ("Holiday.2024") is part of
// its name.
func anonPath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if i == len(segs)-1 {
			segs[i] = anon(s)
		} else {
			segs[i] = anonFolder(s)
		}
	}
	return strings.Join(segs, "/")
}

// anonDir anonymizes a folder's slash-separated path.
func anonDir(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = anonFolder(s)
	}
	return strings.Join(segs, "/")
}

// anonFolder is anon without the extension.
func anonFolder(name string) string {
	tok := anon(name)
	if i := strings.IndexByte(tok, '.'); i > 0 && tok != name {
		tok = tok[:i]
	}
	return tok
}

// anonURL anonymizes a URL's path and keeps only the thumbnail query
// parameters; the scheme and host are dropped.
func anonURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "unparsable"
	}
	p, err := url.PathUnescape(u.EscapedPath())
	if err != nil {
		return "unparsable"
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
	case errors.Is(err, ffmpeg.ErrNotStarted):
		return "ffmpeg-not-started:" + startFailure(err)
	case errors.Is(err, errFFmpegUnavailable):
		return "ffmpeg-unavailable"
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

// diagPageEndpoint is the URL, relative to the page requested as
// requestURI, that its events go to: the server root's diagEndpoint, so
// it's found under a StripPrefix mount or behind a proxy's path too.
func diagPageEndpoint(requestURI string) string {
	if !diagEnabled() {
		return ""
	}
	p, _, _ := strings.Cut(requestURI, "?")
	return "./" + strings.Repeat("../", max(strings.Count(p, "/")-1, 0)) + diagEndpoint
}

// startFailure is why ffmpeg couldn't be started, from a fixed list, or
// the operating system's error number, which names nothing.
func startFailure(err error) string {
	var errno syscall.Errno
	switch {
	case errors.Is(err, exec.ErrDot):
		return "found-in-working-folder"
	case errors.Is(err, exec.ErrNotFound):
		return "not-found"
	case errors.Is(err, fs.ErrNotExist):
		return "missing"
	case errors.Is(err, fs.ErrPermission):
		return "permission-denied"
	case errors.As(err, &errno):
		return "errno-" + strconv.Itoa(int(errno))
	}
	return "other"
}
