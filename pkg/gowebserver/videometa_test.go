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
	"encoding/json"
	"errors"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
)

func TestSidecarRoles(t *testing.T) {
	bases := videoBases([]string{"talk.mp4", "talk.en.mp4", "game.webm", "notes.txt", "a.b.mov"})
	for _, tc := range []struct {
		name string
		want sidecarRole
		ok   bool
	}{
		{"talk.chapters.vtt", sidecarRole{base: "talk", kind: sidecarChapters}, true},
		{"talk.markers.json", sidecarRole{base: "talk", kind: sidecarMarkers}, true},
		{"talk.thumbs.vtt", sidecarRole{base: "talk", kind: sidecarThumbs}, true},
		{"talk.vtt", sidecarRole{base: "talk", kind: sidecarSubtitles}, true},
		{"talk.srt", sidecarRole{base: "talk", kind: sidecarSubtitles}, true},
		{"talk.fr.srt", sidecarRole{base: "talk", kind: sidecarSubtitles, lang: "fr"}, true},
		{"talk.pt-BR.vtt", sidecarRole{base: "talk", kind: sidecarSubtitles, lang: "pt-BR"}, true},
		// The video talk.en.mp4's own subtitles, not talk.mp4's English ones.
		{"talk.en.vtt", sidecarRole{base: "talk.en", kind: sidecarSubtitles}, true},
		{"TALK.CHAPTERS.VTT", sidecarRole{}, false}, // names are matched as they are
		{"game.Chapters.VTT", sidecarRole{base: "game", kind: sidecarChapters}, true},
		{"a.b.markers.json", sidecarRole{base: "a.b", kind: sidecarMarkers}, true},
		{"other.vtt", sidecarRole{}, false},
		{"talk.notes.vtt", sidecarRole{}, false}, // "notes" is no language
		{"talk.json", sidecarRole{}, false},
		{".vtt", sidecarRole{}, false},
	} {
		got, ok := sidecarOf(tc.name, bases)
		if ok != tc.ok || got != tc.want {
			t.Errorf("sidecarOf(%q) = %+v, %t, want %+v, %t", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestListingSidecars(t *testing.T) {
	names := []string{
		"talk.mp4", "talk.en.vtt", "talk.chapters.vtt", "talk.markers.json", "talk.thumbs.vtt",
		"lonely.vtt", "game.webm", "game.srt", "readme.txt",
	}
	sidecars, badges := listingSidecars(names)
	want := map[string]bool{"talk.en.vtt": true, "talk.chapters.vtt": true, "talk.markers.json": true, "talk.thumbs.vtt": true, "game.srt": true}
	if !reflect.DeepEqual(sidecars, want) {
		t.Errorf("sidecars = %v, want %v", sidecars, want)
	}
	if got := badges["talk"]; got != badgeSubtitles|badgeChapters|badgeMarkers|badgeThumbs {
		t.Errorf("talk's badges = %b", got)
	}
	if got := badges["game"]; got != badgeSubtitles {
		t.Errorf("game's badges = %b", got)
	}
	if s, b := listingSidecars([]string{"a.vtt", "b.txt"}); s != nil || b != nil {
		t.Errorf("a folder with no videos has no sidecars: %v %v", s, b)
	}
}

func TestParseVTT(t *testing.T) {
	data := "\uFEFFWEBVTT - chapters\r\n\r\nNOTE a comment\r\n\r\nintro\r\n00:00.000 --> 20:00.000\r\nPart <b>1</b>\r\n\r\n00:20:00.000 --> 00:40:00.500 line:0\r\nQ&amp;A\r\nand more\r\n\r\n00:50:00.000 --> 00:45:00.000\r\nbackwards, left out\r\n"
	got := parseVTT([]byte(data))
	want := []vttCue{{0, 1200, "Part 1"}, {1200, 2400.5, "Q&A and more"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseVTT = %+v, want %+v", got, want)
	}
}

func TestSRTToVTT(t *testing.T) {
	srt := "\uFEFF1\r\n00:00:01,500 --> 00:00:04,000 X1:10 X2:20\r\nHello, <i>world</i>\r\n\r\n2\r\n00:01:02,03 --> 00:01:05,000\r\nA line --> with an arrow\r\nand a second\r\n\r\n\r\ngarbage without a time\r\n"
	want := "WEBVTT\n\n00:00:01.500 --> 00:00:04.000\nHello, <i>world</i>\n\n00:01:02.030 --> 00:01:05.000\nA line --&gt; with an arrow\nand a second\n\n"
	if got := string(srtToVTT([]byte(srt))); got != want {
		t.Errorf("srtToVTT =\n%q\nwant\n%q", got, want)
	}
	// Latin-1, as older files often are.
	latin := []byte("1\n00:00:01,000 --> 00:00:02,000\nCaf\xe9\n")
	if got := string(srtToVTT(latin)); !strings.Contains(got, "Café") {
		t.Errorf("Latin-1 = %q", got)
	}
}

func TestParseMarkers(t *testing.T) {
	data := `{"version": 2, "markers": [
		{"time": 1500, "end": 1562.5, "kind": "stoppage", "label": "Timeout", "extra": {"x": 1}},
		{"time": 861.2, "kind": "score", "label": "Home 1–0", "group": "home"},
		{"time": -1, "label": "before the start"},
		{"time": 10, "end": 5, "label": "ends before it starts"}
	], "groups": {"home": {"color": "#2563eb"}, "away": {"color": "red; background: url(x)"}}}`
	m, err := parseMarkers([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	end := 1562.5
	want := []metaMarker{
		{Time: 10, Label: "ends before it starts"},
		{Time: 861.2, Kind: "score", Label: "Home 1–0", Group: "home"},
		{Time: 1500, End: &end, Kind: "stoppage", Label: "Timeout"},
	}
	if !reflect.DeepEqual(m.Markers, want) {
		t.Errorf("markers = %+v, want %+v", m.Markers, want)
	}
	if m.Groups["home"].Color != "#2563eb" || m.Groups["away"].Color != "" {
		t.Errorf("groups = %+v; a color that isn't one is dropped", m.Groups)
	}
	if _, err := parseMarkers([]byte("{")); err == nil {
		t.Error("invalid JSON is an error")
	}
}

func TestSpriteLayout(t *testing.T) {
	for _, tc := range []struct {
		dur  float64
		w, h int
		want spriteLayout
	}{
		// 90 minutes: 300 frames, 18s apart.
		{5400, 1920, 1080, spriteLayout{interval: 18, count: 300, cols: 10, rows: 30, width: 160, height: 90}},
		// Ten minutes: every 5s.
		{600, 1280, 720, spriteLayout{interval: 5, count: 120, cols: 10, rows: 12, width: 160, height: 90}},
		// A short portrait clip.
		{12, 1080, 1920, spriteLayout{interval: 5, count: 3, cols: 3, rows: 1, width: 160, height: 284}},
	} {
		got, ok := newSpriteLayout(secondsDuration(tc.dur), tc.w, tc.h)
		if !ok || got != tc.want {
			t.Errorf("newSpriteLayout(%g, %d, %d) = %+v, want %+v", tc.dur, tc.w, tc.h, got, tc.want)
		}
	}
	if _, ok := newSpriteLayout(0, 100, 100); ok {
		t.Error("no length, no sprite")
	}
}

// videoMetaServer serves dir as gowebserver does, with ffmpeg (bin, or
// none when it's "") and thumbnails when thumbs is set.
func videoMetaServer(t *testing.T, dir, bin string, thumbs bool) http.Handler {
	t.Helper()
	var ff *ffmpeg.FFmpeg
	if bin != "" {
		var err error
		if ff, err = ffmpeg.Find(bin); err != nil {
			t.Fatal(err)
		}
	}
	var th *thumbnailer
	if thumbs {
		var err error
		if th, err = newThumbnailer(Thumbnails{Enabled: true, CachePath: thumbnailCacheMemory}, ff, &monitoringContext{}); err != nil {
			t.Fatal(err)
		}
	}
	h, closeFS, err := newHandlerFromFS(dir, &monitoringContext{}, true, th, ff)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := errors.Join(th.close(), closeFS()); err != nil {
			t.Error(err)
		}
	})
	return h
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func serveGet(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func getMeta(t *testing.T, h http.Handler, target string) map[string]any {
	t.Helper()
	w := serveGet(t, h, target)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", target, w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// sidecarFiles is a video, not a real one, with one of each sidecar, and
// another video's beside it.
var sidecarFiles = map[string]string{
	"talk.mp4":          "not really a video",
	"talk.chapters.vtt": "WEBVTT\n\n00:00.000 --> 01:00.000\nPart 1\n\n01:00.000 --> 02:00.000\nQ&amp;A\n",
	"talk.markers.json": `{"markers":[{"time":30,"kind":"question","label":"First","group":"home"},{"time":5,"end":10,"kind":"demo"}],"groups":{"home":{"color":"#2563eb"}}}`,
	"talk.en.vtt":       "WEBVTT\n\n00:01.000 --> 00:02.000\nHello\n",
	"talk.fr.srt":       "1\n00:00:01,000 --> 00:00:02,000\nBonjour\n",
	"talk.vtt":          "WEBVTT\n",
	"talk.thumbs.vtt":   "WEBVTT\n\n00:00.000 --> 00:05.000\nthumbs.jpg#xywh=0,0,160,90\n",
	"other.mp4":         "another",
	"other.vtt":         "WEBVTT\n",
}

func TestVideoMetaEachSidecar(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, sidecarFiles)
	h := videoMetaServer(t, dir, "", true)
	m := getMeta(t, h, "/talk.mp4?meta")
	want := `{"chapters":[{"end":60,"start":0,"title":"Part 1"},{"end":120,"start":60,"title":"Q&A"}],` +
		`"groups":{"home":{"color":"#2563eb"}},` +
		`"markers":[{"end":10,"kind":"demo","time":5},{"group":"home","kind":"question","label":"First","time":30}],` +
		`"subtitles":[{"label":"English","lang":"en","src":"./talk.en.vtt"},{"label":"French","lang":"fr","src":"./talk.fr.srt?vtt"},{"label":"Subtitles","src":"./talk.vtt"}],` +
		`"thumbnails":{"vtt":"./talk.thumbs.vtt"}}`
	if got := jsonOf(t, m); got != want {
		t.Errorf("meta =\n%s\nwant\n%s", got, want)
	}
	if _, ok := m["duration"]; ok {
		t.Error("without ffmpeg the duration is left to the player")
	}

	// The other video has only its own.
	m = getMeta(t, h, "/other.mp4?meta")
	if got := jsonOf(t, m); got != `{"chapters":[],"groups":{},"markers":[],"subtitles":[{"label":"Subtitles","src":"./other.vtt"}]}` {
		t.Errorf("other's meta = %s", got)
	}

	// Kept, and revalidated by its ETag; a changed sidecar changes it.
	w := serveGet(t, h, "/talk.mp4?meta")
	etag := w.Header().Get("ETag")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/talk.mp4?meta", nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("revalidated = %d, want 304", rec.Code)
	}
	writeFiles(t, dir, map[string]string{"talk.markers.json": `{"markers":[{"time":1,"label":"new"}]}`})
	future := mustStat(t, filepath.Join(dir, "talk.mp4")).ModTime().Add(1e9)
	if err := os.Chtimes(filepath.Join(dir, "talk.markers.json"), future, future); err != nil {
		t.Fatal(err)
	}
	if w := serveGet(t, h, "/talk.mp4?meta"); w.Header().Get("ETag") == etag || !strings.Contains(w.Body.String(), `"new"`) {
		t.Errorf("a changed sidecar isn't seen: %s", w.Body)
	}
}

func secondsDuration(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

func mustStat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestVideoMetaNone(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"clip.webm": "x", "sub/clip.webm": "y", "notes.txt": "z"})
	h := videoMetaServer(t, dir, "", false)
	for _, target := range []string{"/clip.webm?meta", "/sub/clip.webm?meta"} {
		if got := jsonOf(t, getMeta(t, h, target)); got != `{"chapters":[],"groups":{},"markers":[],"subtitles":[]}` {
			t.Errorf("%s = %s", target, got)
		}
	}
	for target, code := range map[string]int{
		"/missing.mp4?meta":         http.StatusNotFound,
		"/sub?meta":                 http.StatusMovedPermanently, // a folder, not a video
		"/../clip.webm?meta":        http.StatusBadRequest,
		"/sub/../../clip.webm?meta": http.StatusBadRequest,
		"/clip.webm?sprite":         http.StatusUnsupportedMediaType, // no ffmpeg, no thumbnails
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.URL = &url.URL{Path: strings.SplitN(target, "?", 2)[0], RawQuery: strings.SplitN(target, "?", 2)[1]}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != code {
			t.Errorf("%s = %d, want %d", target, w.Code, code)
		}
	}
}

// TestVideoMetaPathSafety checks that ?meta never names a file outside
// the video's folder: not one in a folder below it, nor above it, even
// when named like a sidecar.
func TestVideoMetaPathSafety(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"talk.en.vtt":                 "WEBVTT\n", // beside the folder, not the video
		"in/talk.mp4":                 "x",
		"in/talk.fr.vtt":              "WEBVTT\n",
		"in/talk.thumbs.vtt/x.txt":    "a folder",
		"in/deeper/talk.chapters.vtt": "WEBVTT\n\n00:00.000 --> 00:01.000\nNo\n",
	})
	h := videoMetaServer(t, root, "", false)
	m := getMeta(t, h, "/in/talk.mp4?meta")
	if got := jsonOf(t, m); got != `{"chapters":[],"groups":{},"markers":[],"subtitles":[{"label":"French","lang":"fr","src":"./talk.fr.vtt"}]}` {
		t.Errorf("meta = %s", got)
	}
	checkRelativeURLs(t, m)
}

// relativeSibling is a URL of a file in the same folder: "./" then one
// escaped path segment, and an optional query.
var relativeSibling = regexp.MustCompile(`^\./[^/?#]+(\?[a-z]+(=[0-9a-f]+)?)?$`)

// checkRelativeURLs fails unless every URL in a ?meta document names a
// file in the video's own folder, relative to the video.
func checkRelativeURLs(t *testing.T, m map[string]any) {
	t.Helper()
	var urls []string
	for _, s := range m["subtitles"].([]any) {
		urls = append(urls, s.(map[string]any)["src"].(string))
	}
	if th, ok := m["thumbnails"].(map[string]any); ok {
		for _, k := range []string{"vtt", "sprite"} {
			if u, ok := th[k].(string); ok {
				urls = append(urls, u)
			}
		}
	}
	for _, u := range urls {
		if !relativeSibling.MatchString(u) || strings.Contains(u, "..") {
			t.Errorf("URL %q isn't relative to the video's folder", u)
		}
	}
}

func TestVideoMetaContentTypes(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"a.mp4": "x", "a.m4v": "x", "a.mov": "x", "a.webm": "x", "a.mkv": "x", "a.vtt": "WEBVTT\n",
		"a.srt": "1\n00:00:01,000 --> 00:00:02,000\nHi\n", "a.txt": "plain",
	})
	h := videoMetaServer(t, dir, "", false)
	for name, want := range map[string]string{
		"/a.mp4": "video/mp4", "/a.m4v": "video/mp4", "/a.mov": "video/quicktime", "/a.webm": "video/webm",
		"/a.mkv": "video/x-matroska", "/a.vtt": "text/vtt; charset=utf-8", "/a.srt": "application/x-subrip; charset=utf-8",
		"/a.srt?vtt": "text/vtt; charset=utf-8", "/a.txt": "text/plain; charset=utf-8", "/": "text/html; charset=utf-8",
		"/missing.mp4": "text/plain; charset=utf-8",
	} {
		w := serveGet(t, h, name)
		if got := w.Header().Get("Content-Type"); got != want {
			t.Errorf("%s: Content-Type = %q, want %q", name, got, want)
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q, want nosniff", name, got)
		}
	}
	if body := serveGet(t, h, "/a.srt?vtt").Body.String(); body != "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHi\n\n" {
		t.Errorf("a.srt?vtt = %q", body)
	}
	// A byte range of a video still works.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/a.mp4", nil)
	req.Header.Set("Range", "bytes=0-0")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusPartialContent || w.Header().Get("Content-Type") != "video/mp4" {
		t.Errorf("range = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
}

// TestVideoMetaMounted serves the content under /media/, as a program
// embedding it would, and checks that every URL ?meta gives and the
// listing's media resolve inside the mount.
func TestVideoMetaMounted(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{}
	for k, v := range sidecarFiles {
		files["folder/"+k] = v
	}
	writeFiles(t, dir, files)
	h := videoMetaServer(t, dir, "", true)
	mux := http.NewServeMux()
	mux.Handle("/media/", http.StripPrefix("/media", h))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a request left the mount: %s", r.URL)
		http.NotFound(w, r)
	})

	video := mustParseURL(t, "http://host/media/folder/talk.mp4")
	m := getMeta(t, mux, video.Path+"?meta")
	checkRelativeURLs(t, m)
	for _, s := range m["subtitles"].([]any) {
		ref := mustParseURL(t, s.(map[string]any)["src"].(string))
		u := video.ResolveReference(ref)
		if !strings.HasPrefix(u.Path, "/media/folder/") {
			t.Errorf("subtitle %s resolves outside the mount", u)
		}
		if w := serveGet(t, mux, u.RequestURI()); w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), "WEBVTT") {
			t.Errorf("GET %s = %d %q", u, w.Code, w.Body)
		}
	}

	// The listing gives the gallery its media as names relative to the
	// folder, which is where ?meta is asked for (it.url + '?meta').
	w := serveGet(t, mux, "/media/folder/")
	if w.Code != http.StatusOK {
		t.Fatalf("listing = %d", w.Code)
	}
	media := regexp.MustCompile(`\[1,"([^"]*)",`).FindAllStringSubmatch(w.Body.String(), -1)
	if len(media) != 2 {
		t.Fatalf("listing has %d videos, want 2", len(media))
	}
	for _, mm := range media {
		if strings.HasPrefix(mm[1], "/") || strings.Contains(mm[1], "..") {
			t.Errorf("video URL %q isn't relative to the folder", mm[1])
		}
	}
}

func TestListingFoldsSidecars(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"talk.mp4": "x", "talk.en.vtt": "WEBVTT\n", "talk.chapters.vtt": "WEBVTT\n", "talk.markers.json": "{}",
		"orphan.en.vtt": "WEBVTT\n",
	})
	h := videoMetaServer(t, dir, "", false)
	page := serveGet(t, h, "/").Body.String()
	for _, hidden := range []string{"talk.en.vtt", "talk.chapters.vtt", "talk.markers.json"} {
		if strings.Contains(page, `"`+hidden+`","`+hidden+`"`) {
			t.Errorf("%s has a row of its own", hidden)
		}
	}
	if !strings.Contains(page, `"orphan.en.vtt","orphan.en.vtt"`) {
		t.Error("a subtitle with no video is listed as a file")
	}
	if !regexp.MustCompile(`\[1,"talk.mp4","talk.mp4", 1 , \d+ , 7 \]`).MatchString(page) {
		t.Errorf("the video carries its badges (subtitles, chapters, markers = 7):\n%s", regexp.MustCompile(`media: \[[^\]]*\]`).FindString(page))
	}

	// With only sidecars beside the video there are no files to list.
	only := t.TempDir()
	writeFiles(t, only, map[string]string{"talk.mp4": "x", "talk.vtt": "WEBVTT\n"})
	if page := serveGet(t, videoMetaServer(t, only, "", false), "/").Body.String(); strings.Contains(page, `id="files"`) {
		t.Error("a folder of a video and its subtitles lists no files")
	}
}

// makeTestVideo makes a 12-second MP4 with ffmpeg (bin), with the
// chapters "Part 1", "Part 2" and "Q&A" in its container when chapters
// is set.
func makeTestVideo(t *testing.T, bin, out string, chapters bool) {
	t.Helper()
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=10:duration=12"}
	if chapters {
		meta := filepath.Join(t.TempDir(), "chapters.ffmeta")
		writeFiles(t, filepath.Dir(meta), map[string]string{"chapters.ffmeta": ";FFMETADATA1\n" +
			"[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=4000\ntitle=Part 1\n" +
			"[CHAPTER]\nTIMEBASE=1/1000\nSTART=4000\nEND=8000\ntitle=Part 2\n" +
			"[CHAPTER]\nTIMEBASE=1/1000\nSTART=8000\nEND=12000\ntitle=Q&A\n"})
		args = append(args, "-i", meta, "-map", "0:v", "-map_metadata", "1", "-map_chapters", "1")
	}
	args = append(args, "-g", "10", "-pix_fmt", "yuv420p", out)
	if b, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg can't make a test video: %v: %s", err, b)
	}
}

func TestVideoMetaEmbeddedChaptersAndSprite(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	makeTestVideo(t, bin, filepath.Join(dir, "lecture.mp4"), true)
	makeTestVideo(t, bin, filepath.Join(dir, "recording.mp4"), true)
	writeFiles(t, dir, map[string]string{"recording.chapters.vtt": "WEBVTT\n\n00:00.000 --> 00:12.000\nFrom the file\n"})
	h := videoMetaServer(t, dir, bin, true)

	m := getMeta(t, h, "/lecture.mp4?meta")
	if got := jsonOf(t, m["chapters"]); got != `[{"end":4,"start":0,"title":"Part 1"},{"end":8,"start":4,"title":"Part 2"},{"end":12,"start":8,"title":"Q&A"}]` {
		t.Errorf("embedded chapters = %s", got)
	}
	if d, _ := m["duration"].(float64); d < 11.9 || d > 12.1 {
		t.Errorf("duration = %v, want 12", m["duration"])
	}
	// The sprite's URL has the video's version in it, so a browser never
	// keeps one from before the video changed.
	if got := jsonOf(t, m["thumbnails"]); !regexp.MustCompile(`^\{"columns":3,"count":3,"height":90,"interval":5,"sprite":"\./lecture\.mp4\?sprite=[0-9a-f]{12}","width":160\}$`).MatchString(got) {
		t.Errorf("thumbnails = %s", got)
	}
	checkRelativeURLs(t, m)

	w := serveGet(t, h, "/"+strings.TrimPrefix(m["thumbnails"].(map[string]any)["sprite"].(string), "./"))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("sprite = %d %q %s", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
	if err != nil || cfg.Width != 480 || cfg.Height != 90 {
		t.Errorf("sprite is %dx%d (%v), want 480x90", cfg.Width, cfg.Height, err)
	}
	// Stored: asked again, it's the same, without running ffmpeg.
	if again := serveGet(t, h, "/lecture.mp4?sprite"); !bytes.Equal(again.Body.Bytes(), w.Body.Bytes()) {
		t.Error("the stored sprite differs")
	}

	// A chapters file is preferred to the container's.
	m = getMeta(t, h, "/recording.mp4?meta")
	if got := jsonOf(t, m["chapters"]); got != `[{"end":12,"start":0,"title":"From the file"}]` {
		t.Errorf("chapters = %s", got)
	}
}

// TestVideoMetaWithoutFFmpeg is the same video with no ffmpeg at all: no
// container chapters, no sprite, no duration, and no error.
func TestVideoMetaWithoutFFmpeg(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed, to make the video")
	}
	dir := t.TempDir()
	makeTestVideo(t, bin, filepath.Join(dir, "lecture.mp4"), true)
	h := videoMetaServer(t, dir, "", true)
	if got := jsonOf(t, getMeta(t, h, "/lecture.mp4?meta")); got != `{"chapters":[],"groups":{},"markers":[],"subtitles":[]}` {
		t.Errorf("meta = %s", got)
	}
}

// TestVideoMetaUnreadable is a "video" ffmpeg can't read: the document is
// what the sidecars say, and ffmpeg isn't asked again.
func TestVideoMetaUnreadable(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"broken.mp4": "not a video", "broken.vtt": "WEBVTT\n"})
	h := videoMetaServer(t, dir, bin, true)
	for range 2 {
		if got := jsonOf(t, getMeta(t, h, "/broken.mp4?meta")); got != `{"chapters":[],"groups":{},"markers":[],"subtitles":[{"label":"Subtitles","src":"./broken.vtt"}]}` {
			t.Errorf("meta = %s", got)
		}
	}
	if w := serveGet(t, h, "/broken.mp4?sprite"); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("sprite = %d", w.Code)
	}
}

// TestVideoMetaDiagnostics checks the events ?meta and ?sprite write to
// the diagnostics log name no file or folder.
func TestVideoMetaDiagnostics(t *testing.T) {
	_, read := withDiagnostics(t)
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"Secret Vacation/Alice beach.mp4":          "x",
		"Secret Vacation/Alice beach.en.vtt":       "WEBVTT\n",
		"Secret Vacation/Alice beach.markers.json": `{"markers":[{"time":1,"label":"Bob's birthday"}]}`,
	})
	h := videoMetaServer(t, dir, "", true)
	getMeta(t, h, "/Secret%20Vacation/Alice%20beach.mp4?meta")
	serveGet(t, h, "/Secret%20Vacation/Alice%20beach.mp4?sprite")
	log := read()
	for _, want := range []string{
		"server video-meta file=" + anon("Alice beach.mp4") + " dir=" + anonDir("Secret Vacation"),
		"chapters=0", "markers=1", "subtitles=1", "thumbs=none",
		"server video-sprite file=" + anon("Alice beach.mp4") + " dir=" + anonDir("Secret Vacation") + " result=unsupported",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("missing %q in\n%s", want, log)
		}
	}
	if m := secret.FindString(log); m != "" {
		t.Errorf("the log has %q in it:\n%s", m, log)
	}
}

// TestVideoMetaFFmpegLater is an ffmpeg that can't be started at first
// (being downloaded, say) and then can: the document made meanwhile isn't
// kept, and the next one has what ffmpeg says, under another ETag.
func TestVideoMetaFFmpegLater(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as ffmpeg")
	}
	dir, bin := t.TempDir(), filepath.Join(t.TempDir(), "ffmpeg")
	writeFiles(t, dir, map[string]string{"talk.mp4": "x"})
	script := "#!/bin/sh\necho '  Duration: 00:10:00.00, start: 0.000000, bitrate: 1 kb/s' >&2\n" +
		"printf ';FFMETADATA1\\n[CHAPTER]\\nTIMEBASE=1/1000\\nSTART=0\\nEND=600000\\ntitle=Part 1\\n'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	h := videoMetaServer(t, dir, bin, false)
	// It can't be run now.
	if err := os.Chmod(bin, 0o600); err != nil {
		t.Fatal(err)
	}
	w := serveGet(t, h, "/talk.mp4?meta")
	if body := w.Body.String(); w.Code != http.StatusOK || body != `{"chapters":[],"markers":[],"groups":{},"subtitles":[]}` {
		t.Fatalf("meta without ffmpeg = %d %s", w.Code, body)
	}
	etag := w.Header().Get("ETag")
	if err := os.Chmod(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/talk.mp4?meta", nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"duration":600`) || !strings.Contains(rec.Body.String(), `"Part 1"`) {
		t.Errorf("once ffmpeg runs = %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("ETag") == etag {
		t.Error("a different document has the same ETag")
	}
}

// TestListingFolderNamedLikeAVideo: a folder named like a video has no
// sidecars, in the listing or in ?meta.
func TestListingFolderNamedLikeAVideo(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"notes.mp4/inside.txt": "x", "notes.srt": "1\n", "talk.mp4": "x", "talk.vtt/inside.txt": "x"})
	page := serveGet(t, videoMetaServer(t, dir, "", false), "/").Body.String()
	if !strings.Contains(page, `"notes.srt","notes.srt"`) {
		t.Error("notes.srt is hidden as the sidecar of a folder")
	}
	if regexp.MustCompile(`\[1,"talk.mp4","talk.mp4", 1 , \d+ , \d+ \]`).MatchString(page) {
		t.Error("a folder named like subtitles badges the video")
	}
}
