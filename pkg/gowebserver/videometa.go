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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/singleflight"
)

// A video's metadata comes from files beside it, its sidecars, and from
// the video's own container. For a video NAME.EXT (one of
// metaVideoTypes) in a folder, these are its sidecars:
//
//	NAME.chapters.vtt   chapters: each WebVTT cue is one, its text the title
//	NAME.markers.json   event markers (see videoMarkers)
//	NAME.vtt, NAME.LANG.vtt, NAME.srt, NAME.LANG.srt
//	                    subtitles; SRT is served as WebVTT (NAME.srt?vtt)
//	NAME.thumbs.vtt     a WebVTT thumbnails track (image.jpg#xywh=x,y,w,h)
//
// NAME.EXT?meta answers with all of it as one JSON document (videoMeta):
// the chapters (from the sidecar, or else the container's, read with
// ffmpeg), the markers, the subtitles and the scrub previews (the
// thumbnails track, or else a sprite of frames, NAME.EXT?sprite, made
// with ffmpeg the first time it's asked for and stored like a
// thumbnail). Without ffmpeg there are no container chapters, no sprite
// and no duration, and that's not an error. Every URL in it is relative
// to the video's own, and names a file in the video's folder.

// The sidecars' kinds.
const (
	sidecarChapters  = "chapters"
	sidecarMarkers   = "markers"
	sidecarThumbs    = "thumbs"
	sidecarSubtitles = "subtitles"
)

// What a video has beside it, for the badges on its tile in the file
// browser: a bit per kind of sidecar.
const (
	badgeSubtitles = 1 << iota
	badgeChapters
	badgeMarkers
	badgeThumbs
)

var sidecarBadge = map[string]int{
	sidecarSubtitles: badgeSubtitles,
	sidecarChapters:  badgeChapters,
	sidecarMarkers:   badgeMarkers,
	sidecarThumbs:    badgeThumbs,
}

// metaVideoTypes are the videos that have metadata, by extension, with
// the Content-Type each is served with.
var metaVideoTypes = map[string]string{
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".mov":  "video/quicktime",
	".webm": "video/webm",
	".mkv":  "video/x-matroska",
}

// servedTypes are the Content-Types of the files whose type would
// otherwise depend on the host's MIME table, which a small container
// image often doesn't have: a video could go out as
// application/octet-stream and a caption as text/plain. They're set on
// each response rather than registered in the mime package, which is
// shared with whatever program embeds this one.
var servedTypes = func() map[string]string {
	m := map[string]string{
		".vtt": "text/vtt; charset=utf-8",
		".srt": "application/x-subrip; charset=utf-8",
	}
	for ext, t := range metaVideoTypes {
		m[ext] = t
	}
	return m
}()

// isMetaVideo reports whether name is a video that can have metadata.
func isMetaVideo(name string) bool {
	_, ok := metaVideoTypes[strings.ToLower(path.Ext(name))]
	return ok
}

// sidecarRole is what a sidecar file is to a video: the video's name
// without its extension (base), the kind of sidecar and, for subtitles,
// the language.
type sidecarRole struct {
	base, kind, lang string
}

// sidecarLang is a language tag in a subtitle's name, as in talk.en.vtt
// or talk.pt-BR.srt.
var sidecarLang = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,2}$`)

// sidecarRoles lists what the file name could be a sidecar of, most
// specific first: talk.en.vtt is the subtitles of a video talk.en.EXT,
// or else the English subtitles of talk.EXT. The first whose video is in
// the folder is the one (see sidecarOf).
func sidecarRoles(name string) []sidecarRole {
	lower := strings.ToLower(name)
	var roles []sidecarRole
	for _, s := range []struct{ suffix, kind string }{
		{".chapters.vtt", sidecarChapters},
		{".markers.json", sidecarMarkers},
		{".thumbs.vtt", sidecarThumbs},
	} {
		if strings.HasSuffix(lower, s.suffix) && len(name) > len(s.suffix) {
			roles = append(roles, sidecarRole{base: name[:len(name)-len(s.suffix)], kind: s.kind})
		}
	}
	ext := path.Ext(lower)
	if ext != ".vtt" && ext != ".srt" {
		return roles
	}
	rest := name[:len(name)-len(ext)]
	if rest == "" {
		return roles
	}
	roles = append(roles, sidecarRole{base: rest, kind: sidecarSubtitles})
	if i := strings.LastIndexByte(rest, '.'); i > 0 && sidecarLang.MatchString(rest[i+1:]) {
		roles = append(roles, sidecarRole{base: rest[:i], kind: sidecarSubtitles, lang: rest[i+1:]})
	}
	return roles
}

// videoBases maps the base name (the name without its extension) of each
// video in names that can have metadata to its name.
func videoBases(names []string) map[string]string {
	bases := map[string]string{}
	for _, n := range names {
		if isMetaVideo(n) {
			base := strings.TrimSuffix(n, path.Ext(n))
			if _, ok := bases[base]; !ok {
				bases[base] = n
			}
		}
	}
	return bases
}

// sidecarOf returns what name is to a video in the folder whose videos
// are bases (see videoBases), or false when it's no video's sidecar.
func sidecarOf(name string, bases map[string]string) (sidecarRole, bool) {
	for _, r := range sidecarRoles(name) {
		if _, ok := bases[r.base]; ok {
			return r, true
		}
	}
	return sidecarRole{}, false
}

// listingSidecars says which of a folder's entries (names) are sidecars
// of a video in it, which the file browser lists under the video instead
// of as files of their own, and what each video has (its badges). It
// reads nothing: it goes by the names alone.
func listingSidecars(names []string) (sidecars map[string]bool, badges map[string]int) {
	bases := videoBases(names)
	if len(bases) == 0 {
		return nil, nil
	}
	sidecars, badges = map[string]bool{}, map[string]int{}
	for _, n := range names {
		if r, ok := sidecarOf(n, bases); ok && !isMetaVideo(n) {
			sidecars[n] = true
			badges[r.base] |= sidecarBadge[r.kind]
		}
	}
	return sidecars, badges
}

// videoSidecar is a sidecar file found beside a video.
type videoSidecar struct {
	sidecarRole
	name string
	info fs.FileInfo
}

// findSidecars lists the sidecars of the video name (a path in fsys)
// from its folder's entries, in name order. Only regular files directly
// in the video's folder count.
func findSidecars(fsys fs.FS, name string) ([]videoSidecar, error) {
	dir, file := path.Split(name)
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" {
		dir = "."
	}
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		// A folder named like a video has no sidecars (listingSidecars is
		// given the same names).
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	bases := videoBases(names)
	base := strings.TrimSuffix(file, path.Ext(file))
	var found []videoSidecar
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || strings.ContainsAny(n, `/\`) || isMetaVideo(n) {
			continue
		}
		r, ok := sidecarOf(n, bases)
		if !ok || r.base != base {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		found = append(found, videoSidecar{sidecarRole: r, name: n, info: info})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].name < found[j].name })
	return found, nil
}

// videoMeta is the document NAME.EXT?meta answers with. Times are in
// seconds.
type videoMeta struct {
	// Duration is from ffmpeg; without it, it's left out for the player
	// to read from the video.
	Duration float64 `json:"duration,omitempty"`
	// FPS is the frame rate, from ffmpeg, for stepping a frame at a time.
	FPS        float64              `json:"fps,omitempty"`
	Chapters   []metaChapter        `json:"chapters"`
	Markers    []metaMarker         `json:"markers"`
	Groups     map[string]metaGroup `json:"groups"`
	Subtitles  []metaSubtitle       `json:"subtitles"`
	Thumbnails *metaThumbnails      `json:"thumbnails,omitempty"`
}

type metaChapter struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

// metaMarker is a moment in the video (Time), or a stretch of it when it
// has an End. Kind is free text to filter by and step between, and Group
// picks the color (videoMeta.Groups).
type metaMarker struct {
	Time  float64  `json:"time"`
	End   *float64 `json:"end,omitempty"`
	Kind  string   `json:"kind,omitempty"`
	Label string   `json:"label,omitempty"`
	Group string   `json:"group,omitempty"`
}

type metaGroup struct {
	Color string `json:"color,omitempty"`
	Label string `json:"label,omitempty"`
}

type metaSubtitle struct {
	Src   string `json:"src"`
	Lang  string `json:"lang,omitempty"`
	Label string `json:"label"`
}

// metaThumbnails is where the scrub previews come from: a WebVTT
// thumbnails track (VTT), or else a sprite (Sprite), a sheet of Count
// frames Width x Height, Columns across, one every Interval seconds from
// the start.
type metaThumbnails struct {
	VTT      string  `json:"vtt,omitempty"`
	Sprite   string  `json:"sprite,omitempty"`
	Interval float64 `json:"interval,omitempty"`
	Columns  int     `json:"columns,omitempty"`
	Count    int     `json:"count,omitempty"`
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
}

// The limits on what's read of a sidecar: past them it's ignored, with
// a warning, rather than read into memory.
const (
	maxChaptersBytes = 1 << 20
	maxMarkersBytes  = 16 << 20
	maxSubtitleBytes = 16 << 20
	maxMarkerText    = 200 // runes of a marker's label or kind
)

// videoMarkers is a markers sidecar (NAME.markers.json):
//
//	{
//	  "markers": [
//	    { "time": 861.2, "kind": "score", "label": "Home 1–0", "group": "home" },
//	    { "time": 1500, "end": 1562.5, "kind": "stoppage", "label": "Timeout" }
//	  ],
//	  "groups": { "home": { "color": "#2563eb" }, "away": { "color": "#dc2626" } }
//	}
//
// Unknown fields are ignored, so a generator can carry data of its own.
type videoMarkers struct {
	Markers []metaMarker         `json:"markers"`
	Groups  map[string]metaGroup `json:"groups"`
}

// markerColor is the colors a group may have: hex, or a color's name.
var markerColor = regexp.MustCompile(`^(#[0-9A-Fa-f]{3,4}|#[0-9A-Fa-f]{6}|#[0-9A-Fa-f]{8}|[A-Za-z]{3,20})$`)

// parseMarkers reads a markers sidecar. Markers at no valid time are
// dropped, an end not after its time is dropped, and they're sorted by
// time.
func parseMarkers(data []byte) (*videoMarkers, error) {
	var m videoMarkers
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	out := &videoMarkers{Markers: []metaMarker{}, Groups: map[string]metaGroup{}}
	for _, mk := range m.Markers {
		if math.IsNaN(mk.Time) || math.IsInf(mk.Time, 0) || mk.Time < 0 {
			continue
		}
		if mk.End != nil && (math.IsNaN(*mk.End) || math.IsInf(*mk.End, 0) || *mk.End <= mk.Time) {
			mk.End = nil
		}
		mk.Kind = truncateRunes(mk.Kind, maxMarkerText)
		mk.Label = truncateRunes(mk.Label, maxMarkerText)
		mk.Group = truncateRunes(mk.Group, maxMarkerText)
		out.Markers = append(out.Markers, mk)
	}
	sort.SliceStable(out.Markers, func(i, j int) bool { return out.Markers[i].Time < out.Markers[j].Time })
	for k, g := range m.Groups {
		if !markerColor.MatchString(g.Color) {
			g.Color = ""
		}
		g.Label = truncateRunes(g.Label, maxMarkerText)
		out.Groups[truncateRunes(k, maxMarkerText)] = g
	}
	return out, nil
}

func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// vttCue is a WebVTT cue: its times in seconds and its text.
type vttCue struct {
	start, end float64
	text       string
}

var (
	vttTiming = regexp.MustCompile(`^\s*((?:\d+:)?\d{1,2}:\d{2}[.,]\d{1,3})\s+-->\s+((?:\d+:)?\d{1,2}:\d{2}[.,]\d{1,3})`)
	vttTag    = regexp.MustCompile(`<[^>]*>`)
)

// parseVTTTime reads a WebVTT (or SRT) timestamp, hh:mm:ss.ttt or
// mm:ss.ttt, as seconds.
func parseVTTTime(s string) (float64, bool) {
	s = strings.Replace(s, ",", ".", 1)
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var total float64
	for _, p := range parts {
		var v float64
		if _, err := fmt.Sscanf(p, "%g", &v); err != nil || v < 0 {
			return 0, false
		}
		total = total*60 + v
	}
	return total, true
}

// parseVTT reads the cues of a WebVTT file. A cue's text is its lines
// joined by a space, without tags, with the character references a
// title would use decoded.
func parseVTT(data []byte) []vttCue {
	text := strings.ReplaceAll(strings.ReplaceAll(string(bytes.TrimPrefix(data, []byte("\uFEFF"))), "\r\n", "\n"), "\r", "\n")
	var cues []vttCue
	for _, block := range strings.Split(text, "\n\n") {
		lines := strings.Split(strings.Trim(block, "\n"), "\n")
		for i, line := range lines {
			m := vttTiming.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			start, ok1 := parseVTTTime(m[1])
			end, ok2 := parseVTTTime(m[2])
			if ok1 && ok2 && end > start {
				body := strings.Join(lines[i+1:], " ")
				body = vttTag.ReplaceAllString(body, "")
				body = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&nbsp;", " ", "&lrm;", "", "&rlm;", "").Replace(body)
				cues = append(cues, vttCue{start: start, end: end, text: strings.TrimSpace(body)})
			}
			break
		}
	}
	return cues
}

// srtToVTT converts SubRip subtitles to WebVTT: a header, timestamps
// with a dot, and anything after a cue's times (SRT's positions) left
// out. A file that isn't UTF-8 is taken to be Latin-1, as older SRT
// files often are.
func srtToVTT(data []byte) []byte {
	data = bytes.TrimPrefix(data, []byte("\uFEFF"))
	text := string(data)
	if !utf8.Valid(data) {
		r := make([]rune, len(data))
		for i, b := range data {
			r[i] = rune(b)
		}
		text = string(r)
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for _, block := range strings.Split(text, "\n\n") {
		lines := strings.Split(strings.Trim(block, "\n"), "\n")
		timing := -1
		for i, line := range lines {
			if vttTiming.MatchString(line) {
				timing = i
				break
			}
		}
		if timing < 0 {
			continue
		}
		m := vttTiming.FindStringSubmatch(lines[timing])
		b.WriteString(srtTime(m[1]) + " --> " + srtTime(m[2]) + "\n")
		for _, line := range lines[timing+1:] {
			// A line that looks like a cue's times would end the cue early.
			b.WriteString(strings.ReplaceAll(line, "-->", "--&gt;") + "\n")
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// srtTime is an SRT timestamp written the WebVTT way: hh:mm:ss.ttt.
func srtTime(s string) string {
	s = strings.Replace(s, ",", ".", 1)
	sec, frac, _ := strings.Cut(s, ".")
	for len(frac) < 3 {
		frac += "0"
	}
	if strings.Count(sec, ":") == 1 {
		sec = "00:" + sec
	}
	return sec + "." + frac
}

// subtitleLabels are the names the subtitles menu shows for the common
// languages; any other is shown by its tag, which the gallery names in
// the viewer's own language where the browser can.
var subtitleLabels = map[string]string{
	"ar": "Arabic", "de": "German", "en": "English", "es": "Spanish", "fr": "French", "hi": "Hindi",
	"it": "Italian", "ja": "Japanese", "ko": "Korean", "nl": "Dutch", "pl": "Polish", "pt": "Portuguese",
	"ru": "Russian", "sv": "Swedish", "tr": "Turkish", "uk": "Ukrainian", "zh": "Chinese",
}

func subtitleLabel(lang string) string {
	if lang == "" {
		return "Subtitles"
	}
	primary, _, _ := strings.Cut(strings.ToLower(lang), "-")
	if l, ok := subtitleLabels[primary]; ok {
		if lang != primary {
			return l + " (" + lang + ")"
		}
		return l
	}
	return lang
}

// The scrub preview sprite: frames spriteWidth wide, one every
// max(spriteMinInterval, duration/spriteMaxFrames), spriteColumns
// across.
const (
	spriteWidth       = 160
	spriteMinInterval = 5 * time.Second
	spriteMaxFrames   = 300
	spriteColumns     = 10
	// spriteTimeout bounds making a sprite: unlike a thumbnail's frame,
	// it decodes every keyframe of a video that may last hours.
	spriteTimeout = 5 * time.Minute
)

// spriteLayout is the sprite of a video lasting dur, w x h: its interval
// in seconds, its frame count, the size of each frame and the columns
// and rows they're in.
type spriteLayout struct {
	interval          float64
	count, cols, rows int
	width, height     int
}

func newSpriteLayout(dur time.Duration, w, h int) (spriteLayout, bool) {
	if dur <= 0 || w <= 0 || h <= 0 {
		return spriteLayout{}, false
	}
	interval := math.Max(spriteMinInterval.Seconds(), dur.Seconds()/spriteMaxFrames)
	interval = math.Ceil(interval*1000) / 1000
	count := min(spriteMaxFrames, max(1, int(math.Ceil(dur.Seconds()/interval))))
	cols := min(spriteColumns, count)
	th := max(2, int(math.Round(float64(spriteWidth)*float64(h)/float64(w)/2))*2)
	return spriteLayout{
		interval: interval, count: count, cols: cols, rows: (count + cols - 1) / cols,
		width: spriteWidth, height: th,
	}, true
}

// spriteVersion is in the sprite's URL (NAME.EXT?sprite=VERSION), so a
// browser never shows a sprite it kept from before the video changed.
func spriteVersion(info fs.FileInfo, l spriteLayout) string {
	return hashHex(fileVersion(info) + "|" + l.key())
}

// key names the layout in the store, so a sprite made for another one
// isn't served for this.
func (l spriteLayout) key() string {
	return fmt.Sprintf("sprite-%gs-%dx%d-%dx%d", l.interval, l.cols, l.rows, l.width, l.height)
}

// The ffmpeg runs for metadata, the "operation" of
// thumbnail_ffmpeg_duration_seconds.
const (
	ffmpegProbe  = "probe"  // a video's length, size and chapters
	ffmpegSprite = "sprite" // a video's scrub preview sprite
)

// videoMetaCacheMax bounds the documents and probes kept in memory.
const videoMetaCacheMax = 10000

// videoProbesAtOnce is how many videos ffmpeg describes at once for
// ?meta: flipping through a folder of videos asks for one each.
const videoProbesAtOnce = 2

// errVideoUnreadable is a video ffmpeg couldn't describe lately, which
// isn't tried again until thumbnailUnsupportedFor has passed.
var errVideoUnreadable = errors.New("videometa: ffmpeg can't read this video")

// probeResult is what ffmpeg said about a video: its description, or
// that it couldn't read it (until a time).
type probeResult struct {
	info  *ffmpeg.Info
	until time.Time // for a failure, when to try again
}

// videoMetaHandler answers ?meta, ?sprite and ?vtt (see the top of this
// file), and sets the Content-Type of the videos and captions it knows
// (servedTypes) and X-Content-Type-Options: nosniff on everything else
// it passes to base.
type videoMetaHandler struct {
	base   http.Handler
	fsys   fs.FS
	source string         // hash of the served filesystem's URI, as thumbnailHandler's
	t      *thumbnailer   // nil when thumbnails, and so sprites, are off
	ff     *ffmpeg.FFmpeg // nil when ffmpeg is unavailable

	probes   singleflight.Group
	probeSem chan struct{}
	mu       sync.Mutex
	docs     map[string][]byte      // ?meta documents, by the versions they're made from
	info     map[string]probeResult // probes, by video version
}

func newVideoMetaHandler(base http.Handler, fsys fs.FS, source string, t *thumbnailer, ff *ffmpeg.FFmpeg) *videoMetaHandler {
	sum := sha256.Sum256([]byte(source))
	return &videoMetaHandler{
		base: base, fsys: fsys, source: hex.EncodeToString(sum[:8]), t: t, ff: ff,
		probeSem: make(chan struct{}, videoProbesAtOnce),
		docs:     map[string][]byte{}, info: map[string]probeResult{},
	}
}

func (h *videoMetaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	name := cleanPath(strings.TrimPrefix(r.URL.Path, "/"))
	q := r.URL.Query()
	ext := strings.ToLower(path.Ext(name))
	switch {
	case q.Has("meta") && isMetaVideo(name):
		h.serveMeta(w, r, name)
		return
	case q.Has("sprite") && isMetaVideo(name):
		h.serveSprite(w, r, name)
		return
	case q.Has("vtt") && ext == ".srt":
		h.serveSRT(w, r, name)
		return
	}
	if t, ok := servedTypes[ext]; ok && !strings.HasSuffix(r.URL.Path, "/") && r.URL.RawQuery == "" {
		w.Header().Set("Content-Type", t)
	}
	h.base.ServeHTTP(w, r)
}

// statFile returns the regular file name's info, or false after
// answering 404 (or 400 for a path that isn't one, such as one that
// climbs out of the served folder with "..").
func (h *videoMetaHandler) statFile(w http.ResponseWriter, r *http.Request, name string) (fs.FileInfo, bool) {
	if !fs.ValidPath(name) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return nil, false
	}
	info, err := fs.Stat(h.fsys, name)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return nil, false
	}
	return info, true
}

func fileVersion(info fs.FileInfo) string {
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}

func (h *videoMetaHandler) serveMeta(w http.ResponseWriter, r *http.Request, name string) {
	setRequestHandler(r, handlerVideoMeta)
	start := time.Now()
	info, ok := h.statFile(w, r, name)
	if !ok {
		return
	}
	sidecars, err := findSidecars(h.fsys, name)
	if err != nil {
		slog.Warn("cannot list a video's folder for its sidecars", "path", name, "error", err)
	}
	// The document changes with the video, any of its sidecars, and what
	// ffmpeg can do (it may be installed, or start working, later).
	key := h.source + "/" + name + "@" + fileVersion(info)
	for _, s := range sidecars {
		key += "|" + s.name + "@" + fileVersion(s.info)
	}
	key += fmt.Sprintf("|ffmpeg=%t,sprites=%t", h.ff != nil, h.t.videos())

	h.mu.Lock()
	doc, cached := h.docs[key]
	h.mu.Unlock()
	var meta *videoMeta
	var probeErr error
	if !cached {
		meta, probeErr = h.buildMeta(r.Context(), name, info, sidecars)
		if r.Context().Err() != nil {
			return
		}
		if doc, err = json.Marshal(meta); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// A document made while the container couldn't be read for a
		// passing reason (ffmpeg not started yet, out of time, the client
		// gone) is served but not kept, so the next request reads it again.
		if probeErr == nil {
			h.mu.Lock()
			if len(h.docs) >= videoMetaCacheMax {
				h.docs = map[string][]byte{}
			}
			h.docs[key] = doc
			h.mu.Unlock()
		}
	}
	// The ETag is the document's own, so a different document is never
	// taken for one the browser has.
	sum := sha256.Sum256(doc)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", `"meta-`+hex.EncodeToString(sum[:12])+`"`)
	if diagEnabled() {
		fields := map[string]any{
			"file": anon(path.Base(name)), "dir": anonDir(path.Dir(name)), "cached": cached,
			"sidecars": len(sidecars), "ms": time.Since(start).Milliseconds(), "err": errCategory(probeErr),
		}
		if meta != nil {
			fields["chapters"], fields["markers"], fields["subtitles"] = len(meta.Chapters), len(meta.Markers), len(meta.Subtitles)
			fields["thumbs"] = "none"
			if meta.Thumbnails != nil && meta.Thumbnails.VTT != "" {
				fields["thumbs"] = "vtt"
			} else if meta.Thumbnails != nil {
				fields["thumbs"] = "sprite"
			}
		}
		diagEvent("server", "video-meta", fields)
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(doc))
}

// buildMeta gathers the video name's metadata from its sidecars and,
// when they don't have it, its container. It returns the error probing
// the container gave, if any, for diagnostics: the document is still
// whatever else was found.
func (h *videoMetaHandler) buildMeta(ctx context.Context, name string, info fs.FileInfo, sidecars []videoSidecar) (*videoMeta, error) {
	dir := path.Dir(name)
	meta := &videoMeta{Chapters: []metaChapter{}, Markers: []metaMarker{}, Groups: map[string]metaGroup{}, Subtitles: []metaSubtitle{}}
	for _, s := range sidecars {
		switch s.kind {
		case sidecarChapters:
			data, err := h.readSidecar(path.Join(dir, s.name), maxChaptersBytes)
			if err != nil {
				continue
			}
			meta.Chapters = meta.Chapters[:0]
			for _, c := range parseVTT(data) {
				meta.Chapters = append(meta.Chapters, metaChapter{Start: c.start, End: c.end, Title: c.text})
			}
		case sidecarMarkers:
			data, err := h.readSidecar(path.Join(dir, s.name), maxMarkersBytes)
			if err != nil {
				continue
			}
			m, err := parseMarkers(data)
			if err != nil {
				slog.Warn("markers file isn't valid", "path", path.Join(dir, s.name), "error", err)
				continue
			}
			meta.Markers, meta.Groups = m.Markers, m.Groups
		case sidecarThumbs:
			meta.Thumbnails = &metaThumbnails{VTT: relativeURL(s.name)}
		case sidecarSubtitles:
			src := relativeURL(s.name)
			if strings.EqualFold(path.Ext(s.name), ".srt") {
				src += "?vtt"
			}
			meta.Subtitles = append(meta.Subtitles, metaSubtitle{Src: src, Lang: s.lang, Label: subtitleLabel(s.lang)})
		}
	}
	// A plain subtitle (NAME.vtt) beside one in the same language
	// (NAME.en.vtt) is listed after the ones with a language.
	sort.SliceStable(meta.Subtitles, func(i, j int) bool {
		return meta.Subtitles[i].Lang != "" && meta.Subtitles[j].Lang == ""
	})

	probe, err := h.probe(ctx, name, info)
	if probe == nil {
		return meta, err
	}
	meta.Duration = math.Round(probe.Duration.Seconds()*1000) / 1000
	meta.FPS = probe.FPS
	if len(meta.Chapters) == 0 {
		for _, c := range probe.Chapters {
			meta.Chapters = append(meta.Chapters, metaChapter{Start: c.Start.Seconds(), End: c.End.Seconds(), Title: c.Title})
		}
	}
	if meta.Thumbnails == nil && h.t.videos() {
		if l, ok := newSpriteLayout(probe.Duration, probe.Width, probe.Height); ok {
			meta.Thumbnails = &metaThumbnails{
				Sprite: relativeURL(path.Base(name)) + "?sprite=" + spriteVersion(info, l), Interval: l.interval,
				Columns: l.cols, Count: l.count, Width: l.width, Height: l.height,
			}
		}
	}
	return meta, err
}

// readSidecar reads the sidecar name, which must be no larger than
// limit bytes.
func (h *videoMetaHandler) readSidecar(name string, limit int64) ([]byte, error) {
	f, err := h.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Error("failed to close a video's sidecar", "path", name, "error", err)
		}
	}()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		slog.Warn("a video's sidecar is too large and is ignored", "path", name, "limit", limit)
		return nil, errors.New("sidecar too large")
	}
	return data, nil
}

// probe returns what ffmpeg says about the video name: its length,
// size and chapters. It's kept by the video's path and version, in the
// thumbnail store when there is one (so it lasts across restarts) and in
// memory, and asked of ffmpeg once however many requests want it at
// once. Without ffmpeg it's nil, and so is the error.
func (h *videoMetaHandler) probe(ctx context.Context, name string, info fs.FileInfo) (*ffmpeg.Info, error) {
	if h.ff == nil {
		return nil, nil
	}
	version := fileVersion(info)
	// probe2: a description stored before ffmpeg.Info had the
	// container's tags and streams isn't used for one that has them.
	key := path.Join(h.source, name, "probe2@"+hashHex(version))
	h.mu.Lock()
	cached, ok := h.info[key]
	h.mu.Unlock()
	switch {
	case ok && cached.until.IsZero():
		return cached.info, nil
	case ok && time.Now().Before(cached.until):
		return cached.info, errVideoUnreadable
	}
	if h.t != nil {
		if th, ok := h.t.store.get(key); ok {
			var pi ffmpeg.Info
			if err := json.Unmarshal(th.data, &pi); err == nil {
				h.remember(key, probeResult{info: &pi})
				return &pi, nil
			}
		}
	}
	// One run per video however many ask, a few videos at a time, ended
	// when the request that started it goes (as when the viewer has
	// already moved on to the next video).
	v, err, _ := h.probes.Do(key, func() (any, error) {
		select {
		case h.probeSem <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-h.probeSem }()
		pctx, cancel := context.WithTimeout(ctx, thumbnailVideoTimeout)
		defer cancel()
		var pi *ffmpeg.Info
		err := h.withVideoInput(pctx, name, info, ffmpegProbe, func(ctx context.Context, input string, stdin io.Reader) (err error) {
			pi, err = h.ff.Probe(ctx, input, stdin)
			return err
		})
		if errors.Is(err, ffmpeg.ErrNotStarted) && h.t != nil {
			h.t.ffmpegFailed(err)
		}
		if errors.Is(err, ffmpeg.ErrNotStarted) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return nil, err // not the video's fault: try again next time
		}
		if err != nil {
			// The video can't be read: remembered for a while as having
			// nothing, so it isn't tried on every request.
			slog.Debug("ffmpeg could not describe a video", "path", name, "error", err)
			pi = &ffmpeg.Info{}
			h.remember(key, probeResult{info: pi, until: time.Now().Add(thumbnailUnsupportedFor)})
			return pi, errVideoUnreadable
		}
		h.remember(key, probeResult{info: pi})
		if h.t != nil {
			if data, merr := json.Marshal(pi); merr == nil {
				if perr := h.t.store.put(key, &thumbnail{data: data}); perr != nil {
					slog.Warn("failed to store a video's description", "path", key, "error", perr)
				}
			}
		}
		return pi, err
	})
	pi, _ := v.(*ffmpeg.Info)
	return pi, err
}

func (h *videoMetaHandler) remember(key string, r probeResult) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.info) >= videoMetaCacheMax {
		h.info = map[string]probeResult{}
	}
	h.info[key] = r
}

func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

// withVideoInput runs fn, one run of ffmpeg for operation, on the video
// name: by its path when it's a file on local disk, so ffmpeg can seek,
// or else piped to it. It's timed with the thumbnails' ffmpeg runs when
// there's a thumbnailer.
func (h *videoMetaHandler) withVideoInput(ctx context.Context, name string, info fs.FileInfo, operation string, fn func(ctx context.Context, input string, stdin io.Reader) error) (err error) {
	f, err := h.fsys.Open(name)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			slog.Error("failed to close a video after running ffmpeg", "path", name, "error", cerr)
		}
	}()
	input, stdin := "pipe:0", io.Reader(f)
	if p, local := localFilePath(f, info); local {
		input, stdin = p, nil
	}
	if h.t != nil {
		parent := ctx
		ran := h.t.metrics.ffmpegRun(&ctx, operation, stdin != nil)
		defer func() { ran(ffmpegOutcome(parent, ctx, err)) }()
	}
	return fn(ctx, input, stdin)
}

// serveSprite answers NAME.EXT?sprite with the video's scrub preview
// sprite (see metaThumbnails), made the first time it's asked for and
// stored like a thumbnail.
func (h *videoMetaHandler) serveSprite(w http.ResponseWriter, r *http.Request, name string) {
	setRequestHandler(r, handlerVideoMeta)
	info, ok := h.statFile(w, r, name)
	if !ok {
		return
	}
	start := time.Now()
	result := thumbnailCached
	var spriteErr error
	defer func() {
		if diagEnabled() {
			diagEvent("server", "video-sprite", map[string]any{
				"file": anon(path.Base(name)), "dir": anonDir(path.Dir(name)), "result": result,
				"ms": time.Since(start).Milliseconds(), "err": errCategory(spriteErr),
			})
		}
	}()
	unavailable := func() {
		result = thumbnailUnsupported
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "no preview sprite for this video", http.StatusUnsupportedMediaType)
	}
	if !h.t.videos() {
		unavailable()
		return
	}
	probe, err := h.probe(r.Context(), name, info)
	spriteErr = err
	if probe == nil {
		unavailable()
		return
	}
	l, ok := newSpriteLayout(probe.Duration, probe.Width, probe.Height)
	if !ok {
		unavailable()
		return
	}
	key := path.Join(h.source, name, l.key()+"@"+hashHex(fileVersion(info)))
	sum := sha256.Sum256([]byte(key))
	etag := `"` + hex.EncodeToString(sum[:12]) + `"`
	ctx, span := h.t.tp.Tracer("thumbnail").Start(r.Context(), "sprite")
	defer span.End()
	span.SetAttributes(attribute.String("path", name), attribute.Int("frames", l.count))
	th, cached := h.t.store.get(key)
	if !cached {
		// Once started, a sprite is finished and stored even if the page
		// that asked goes (the viewer moved on, or reloaded): it can take
		// minutes, and starting over each time would never finish it.
		th, err = h.t.shared(context.WithoutCancel(ctx), key, func(ctx context.Context) (*thumbnail, error) {
			return h.makeSprite(ctx, name, info, l)
		})
		spriteErr = err
		switch {
		case err == nil:
			result = thumbnailMade
		case r.Context().Err() != nil:
			result = thumbnailCanceled
			return
		default:
			slog.Warn("preview sprite failed", "path", name, "error", err)
			unavailable()
			result = thumbnailFailed
			return
		}
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, "", info.ModTime(), bytes.NewReader(th.data))
}

// makeSprite runs ffmpeg for the sprite, in one of the thumbnailer's
// render slots.
func (h *videoMetaHandler) makeSprite(ctx context.Context, name string, info fs.FileInfo, l spriteLayout) (*thumbnail, error) {
	select {
	case h.t.spriteSem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-h.t.spriteSem }()
	ctx, cancel := context.WithTimeout(ctx, spriteTimeout)
	defer cancel()
	var data []byte
	err := h.withVideoInput(ctx, name, info, ffmpegSprite, func(ctx context.Context, input string, stdin io.Reader) (err error) {
		data, err = h.ff.Sprite(ctx, input, stdin, ffmpeg.SpriteOptions{
			Interval: time.Duration(l.interval * float64(time.Second)), Columns: l.cols, Rows: l.rows, Width: l.width, Height: l.height,
		})
		return err
	})
	if errors.Is(err, ffmpeg.ErrNotStarted) {
		h.t.ffmpegFailed(err)
	}
	if err != nil {
		return nil, err
	}
	// The sheet has to be the size the document said it would be.
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width != l.cols*l.width || cfg.Height != l.rows*l.height {
		return nil, fmt.Errorf("sprite is %dx%d, want %dx%d", cfg.Width, cfg.Height, l.cols*l.width, l.rows*l.height)
	}
	return &thumbnail{data: data, contentType: "image/jpeg", kind: thumbnailKind(true)}, nil
}

// serveSRT answers NAME.srt?vtt with the subtitles as WebVTT.
func (h *videoMetaHandler) serveSRT(w http.ResponseWriter, r *http.Request, name string) {
	setRequestHandler(r, handlerVideoMeta)
	info, ok := h.statFile(w, r, name)
	if !ok {
		return
	}
	data, err := h.readSidecar(name, maxSubtitleBytes)
	if err != nil {
		http.Error(w, "cannot read the subtitles", http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", servedTypes[".vtt"])
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "", info.ModTime(), bytes.NewReader(srtToVTT(data)))
}
