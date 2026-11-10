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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/dustin/go-humanize"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// How a log is followed while it is written. fs.FS has no notifications, so
// growth has to be asked about on a timer, and a stream ends on its own: a
// page that was closed without saying so must not hold a connection forever.
const (
	followPollInterval  = 500 * time.Millisecond
	followPingInterval  = 20 * time.Second
	followMaxDuration   = 30 * time.Minute
	followMaxChunkBytes = 256 << 10
	// followMaxStreams bounds how many logs one server follows at once.
	followMaxStreams = 32
	// followMaxReadErrors bounds how many looks at a followed log may fail in
	// a row before its stream gives up. A log that is being deleted or moved
	// can be unreadable for a moment - on Windows a log whose deletion is
	// still waiting for its last handle to close says "Access is denied"
	// rather than that it is gone - and the next look is what finds out which
	// it turned out to be.
	followMaxReadErrors = 5
)

// What ending a followed log came to, the "result" of
// richview_follow_connections_total.
const (
	followEnded   = "ended"   // the browser went away
	followGone    = "gone"    // the file was removed
	followLimit   = "limit"   // the stream reached its time limit
	followRefused = "refused" // not a log file, or too many logs followed already
	followFailed  = richViewFailed
)

// errFollowNotText is a file whose bytes would only reach the browser as
// noise, so its log is not followed at all.
var errFollowNotText = errors.New("not text")

// isFollowable reports whether a file's name is a log's: something written a
// line at a time as it goes, which is what makes streaming it worth while.
func isFollowable(name string) bool {
	base := strings.ToLower(path.Base(name))
	switch nameToIconClass(false, base) {
	case "log":
		return true
	case "archive":
		// A log that has been compressed away is finished being written.
		return false
	}
	// A log rotated by date or number keeps .log in the middle of its name,
	// and a few of them have no extension to speak for them.
	if strings.Contains(base, ".log.") {
		return true
	}
	switch base {
	case "syslog", "messages", "dmesg":
		return true
	}
	return false
}

// followState is where a followed log left off: how far it has been read,
// the line number the next line it sends will carry, and whatever was written
// since its last whole line, held back until that line's newline arrives.
type followState struct {
	offset  int64
	line    int
	pending []byte
}

// eventID says the same thing to the browser, which sends it back in
// Last-Event-ID when it reconnects after a dropped connection.
func (st followState) eventID() string {
	return strconv.FormatInt(st.offset, 10) + ":" + strconv.Itoa(st.line)
}

// followStateFrom is where a stream picks a log up: the browser's
// Last-Event-ID first, since it says what that browser has already seen, and
// otherwise the offset and line number the page rendered up to.
func followStateFrom(r *http.Request) followState {
	if offsetStr, lineStr, ok := strings.Cut(r.Header.Get("Last-Event-ID"), ":"); ok {
		offset, errOffset := strconv.ParseInt(offsetStr, 10, 64)
		line, errLine := strconv.Atoi(lineStr)
		if errOffset == nil && errLine == nil && offset >= 0 && line >= 1 {
			return followState{offset: offset, line: line}
		}
	}
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		offset = 0
	}
	line, err := strconv.Atoi(r.URL.Query().Get("line"))
	if err != nil || line < 1 {
		line = 1
	}
	return followState{offset: offset, line: line}
}

// followChunk is what a followed log added between two looks at it.
type followChunk struct {
	start int    // the number the chunk's first line carries
	count int    // its lines
	size  int64  // the file's size now, which the page shows as its own
	text  string // its text, ending in a newline
	lines string // its text, highlighted the way the page highlights the rest
}

// followFrame is one event of the stream: the highlighted lines to add, the
// number the first of them carries (the page drops the lines at or after it,
// which is how a truncated or rotated log starts over), and the size the page
// should now show.
type followFrame struct {
	Start int    `json:"start"`
	Count int    `json:"count"`
	Size  string `json:"size,omitempty"`
	Lines string `json:"lines,omitempty"`
}

func (chunk *followChunk) frame() followFrame {
	return followFrame{
		Start: chunk.start,
		Count: chunk.count,
		Size:  humanize.Bytes(uint64(chunk.size)),
		Lines: chunk.lines,
	}
}

// followStream is the part of the connection a followed log is written to: a
// writer that can be flushed, which is what makes lines arrive as they are
// read instead of when the buffer fills.
type followStream interface {
	io.Writer
	Flush() error
}

// followSink joins the response's writer to the controller that flushes it,
// which is how a followed log reaches the browser through the gzip and
// metrics writers wrapped around it.
type followSink struct {
	w        io.Writer
	streamer *http.ResponseController
}

func (s followSink) Write(p []byte) (int, error) { return s.w.Write(p) }

func (s followSink) Flush() error { return s.streamer.Flush() }

// serveFollow sends a log file's later lines to a browser as they are
// written, as server-sent events the text viewer's page listens to.
func (h *richViewHandler) serveFollow(w http.ResponseWriter, r *http.Request, fsPath string) {
	setRequestHandler(r, handlerRichView)
	ctx, span := h.metrics.tracer.Start(r.Context(), "richview.follow", trace.WithAttributes(attribute.String("path", fsPath)))
	defer span.End()
	result := followFailed
	defer func() {
		span.SetAttributes(attribute.String("result", result))
		h.metrics.followConnections.Add(context.WithoutCancel(ctx), 1, metric.WithAttributes(attribute.String("result", result)))
	}()

	fileName := path.Base(fsPath)
	if !isFollowable(fileName) {
		result = followRefused
		http.Error(w, fileName+" is not a log file", http.StatusNotFound)
		return
	}
	select {
	case h.followSlots <- struct{}{}:
		defer func() { <-h.followSlots }()
	default:
		result = followRefused
		w.Header().Set("Retry-After", "5")
		http.Error(w, "too many logs are being followed", http.StatusServiceUnavailable)
		return
	}
	// The writers between here and the socket all pass Flush through, so the
	// lines reach the browser as they are read.
	stream := followSink{w: w, streamer: http.NewResponseController(w)}

	state, err := h.followStart(fsPath, followStateFrom(r))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		result = followGone
		http.Error(w, "no such file", http.StatusNotFound)
		return
	case errors.Is(err, errFollowNotText):
		result = followRefused
		http.Error(w, err.Error(), http.StatusUnsupportedMediaType)
		return
	case err != nil:
		result = followFailed
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.metrics.followActive.Add(ctx, 1)
	defer h.metrics.followActive.Add(context.WithoutCancel(ctx), -1)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// nginx and the like would otherwise hold the response in a buffer.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := stream.Flush(); err != nil {
		result = followEnded
		return
	}

	deadline := time.Now().Add(followMaxDuration)
	ticker := time.NewTicker(h.pollInterval)
	defer ticker.Stop()
	pinged := time.Now()
	readFails := 0
	for {
		select {
		case <-ctx.Done():
			result = followEnded
			return
		case <-ticker.C:
		}
		if time.Now().After(deadline) {
			result = followLimit
			return
		}
		chunk, err := h.followRead(fsPath, &state)
		if errors.Is(err, fs.ErrNotExist) {
			result = followGone
			if err := writeFollowEvent(stream, "gone", "", "{}"); err != nil {
				result = followEnded
			}
			return
		}
		if err != nil {
			// A log that is mid-deletion or mid-rename can be there to ask
			// about and still be unopenable for a moment, so the look after
			// it is what says whether it went away or came back.
			readFails++
			if readFails >= followMaxReadErrors {
				result = followFailed
				slog.Error("failed to read a followed log", "path", fsPath, "error", err)
				return
			}
			slog.Warn("failed to read a followed log, looking again", "path", fsPath, "error", err, "looks left", followMaxReadErrors-readFails)
			continue
		}
		readFails = 0
		if chunk == nil {
			if time.Since(pinged) >= followPingInterval {
				pinged = time.Now()
				if err := writeFollowComment(stream, "still following"); err != nil {
					result = followEnded
					return
				}
			}
			continue
		}
		frame, err := json.Marshal(chunk.frame())
		if err != nil {
			result = followFailed
			return
		}
		if err := writeFollowEvent(stream, "lines", state.eventID(), string(frame)); err != nil {
			result = followEnded
			return
		}
		h.metrics.followLines.Add(ctx, int64(chunk.count))
	}
}

// followStart checks that a log can be followed at all. Where it says to
// start may be past the end, which is a log that has been replaced since the
// page rendered: the first look at it in followRead notices and starts over.
func (h *richViewHandler) followStart(fsPath string, state followState) (followState, error) {
	f, err := h.baseFS.Open(fsPath)
	if err != nil {
		return state, err
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Error("failed to close a followed log", "path", fsPath, "error", err)
		}
	}()
	stat, err := f.Stat()
	if err != nil {
		return state, err
	}
	if stat.IsDir() {
		return state, fs.ErrNotExist
	}
	sniff, err := io.ReadAll(io.LimitReader(f, 512))
	if err != nil {
		return state, err
	}
	if !strings.HasPrefix(http.DetectContentType(sniff), "text/") {
		return state, errFollowNotText
	}
	return state, nil
}

// followRead looks at a followed log once and takes the whole lines written
// since the last look, leaving nothing to send when the file has none yet.
func (h *richViewHandler) followRead(fsPath string, state *followState) (*followChunk, error) {
	stat, err := fs.Stat(h.baseFS, fsPath)
	if err != nil {
		return nil, err
	}
	if stat.IsDir() {
		return nil, fs.ErrNotExist
	}
	chunk := &followChunk{start: state.line, size: stat.Size()}
	if stat.Size() < state.offset {
		// A log that got smaller is a different log: start over from its top.
		state.offset = 0
		state.line = 1
		state.pending = nil
		chunk.start = 1
	}

	f, err := h.baseFS.Open(fsPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Error("failed to close a followed log", "path", fsPath, "error", err)
		}
	}()
	if state.offset > 0 {
		if seeker, ok := f.(io.Seeker); ok {
			if _, err := seeker.Seek(state.offset, io.SeekStart); err != nil {
				return nil, err
			}
		} else if _, err := io.CopyN(io.Discard, f, state.offset); err != nil {
			return nil, err
		}
	}
	added, err := io.ReadAll(io.LimitReader(f, followMaxChunkBytes))
	if err != nil {
		return nil, err
	}
	state.pending = append(state.pending, added...)
	end := bytes.LastIndexByte(state.pending, '\n')
	if end < 0 {
		// Half a line: hold it back until the writer finishes it. A log that
		// has just been truncated has nothing to say yet either, and a frame
		// with no lines in it would only empty the page before the new log's
		// first line arrives with its own start.
		return nil, nil
	}
	done := state.pending[:end+1]
	state.pending = append([]byte(nil), state.pending[end+1:]...)
	state.offset += int64(len(done))
	chunk.count = bytes.Count(done, []byte{'\n'})
	chunk.text, state.line = string(done), state.line+chunk.count

	lines, err := followHighlight(path.Base(fsPath), chunk.text, chunk.start)
	if err != nil {
		return nil, err
	}
	chunk.lines = lines
	return chunk, nil
}

// followHighlight colors the lines a log added with the page's own classes,
// and with line numbers that go on from the lines the page already shows, so
// they splice into the code the browser has. Each chunk is tokenised on its
// own: a log line is colored as it is written, not as part of the whole file.
func followHighlight(fileName, text string, baseLine int) (string, error) {
	lexer := richViewLexer(fileName, text)
	iterator, err := lexer.Tokenise(nil, text)
	if err != nil {
		return "", err
	}
	formatter := chromahtml.New(append(slices.Clone(richViewFormatterOptions),
		chromahtml.WithPreWrapper(barePreWrapper{}),
		chromahtml.BaseLineNumber(baseLine))...)
	var b strings.Builder
	if err := formatter.Format(&b, styles.Fallback, iterator); err != nil {
		return "", err
	}
	return b.String(), nil
}

// barePreWrapper leaves a highlighted fragment without its own <pre> element,
// which is what makes it join the one on the page.
type barePreWrapper struct{}

func (barePreWrapper) Start(_ bool, _ string) string {
	return ""
}

func (barePreWrapper) End(_ bool) string {
	return ""
}

// writeFollowEvent sends one named event, and writeFollowComment a keep-alive
// the browser ignores. Both flush, since a followed log sits behind proxies
// that would otherwise hold its lines back.
func writeFollowEvent(stream followStream, event, id, data string) error {
	var b strings.Builder
	if id != "" {
		b.WriteString("id: " + id + "\n")
	}
	if event != "" {
		b.WriteString("event: " + event + "\n")
	}
	b.WriteString("data: " + data + "\n\n")
	return writeFollow(stream, b.String())
}

func writeFollowComment(stream followStream, text string) error {
	return writeFollow(stream, ": "+text+"\n\n")
}

func writeFollow(stream followStream, s string) error {
	if _, err := stream.Write([]byte(s)); err != nil {
		return err
	}
	return stream.Flush()
}
