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
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// A photo's, video's or song's details, for the slideshow's info panel:
// NAME.EXT?info answers with a JSON document (mediaInfo) read from the
// file's own metadata, never from anything beside it:
//
//   - photos (JPEG, PNG, WebP, TIFF, HEIF/HEIC/AVIF): EXIF (camera, lens,
//     exposure, the date taken, GPS), XMP (title, description, place
//     names, rating), the ICC profile, and the container's own facts
//     (size, bit depth, color model, gamma);
//   - videos: MP4 and QuickTime boxes (length, size, codecs, the date
//     recorded, ISO 6709 location, iTunes-style tags such as ©lyr), and
//     with ffmpeg, any container ffmpeg reads (videometa.go's probe);
//   - songs: ID3v2 (USLT and SYLT lyrics), FLAC and Ogg Vorbis comments,
//     MP4 tags.
//
// Only the headers are read: a file is read from the start, and what
// comes after the picture or the media data (WebP's EXIF, an MP4's
// moov at the end) is reached by seeking, or, when the filesystem can't
// seek (an archive), by reading past it up to mediaInfoSkipMax bytes.
//
// The document is ranked for the panel: the location, the date, the
// size and length come first as fields of their own; the details come
// in groups (mediaGroupRank), their fields in mediaFieldRank's order;
// embedded text (lyrics, descriptions) is its own list; and every tag
// read is in Tags, for the panel's "All tags".
//
// The location's country is looked up here, offline, in the world map
// the panel draws it on (geodata/worldmap.json, served as ?worldmap):
// nothing about where a photo was taken leaves the server.

const (
	// mediaInfoReadMax is the most of a file read for its metadata, and
	// mediaInfoSkipMax the most read and thrown away to get past media
	// data when the file can't seek.
	mediaInfoReadMax = 16 << 20
	mediaInfoSkipMax = 64 << 20
	// mediaTextMax is the longest embedded text kept (lyrics, a
	// description), in bytes, and mediaTagMax the longest tag value.
	mediaTextMax = 32 << 10
	mediaTagMax  = 300
	// mediaTagsMax is the most tags listed.
	mediaTagsMax = 500
	// mediaInfoCacheMax bounds the documents kept in memory.
	mediaInfoCacheMax = 5000
)

// mediaInfo is the ?info document.
type mediaInfo struct {
	Kind     string `json:"kind"` // image, video or audio
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
	// Format names the file's format: "JPEG", "HEIF", "MPEG-4"...
	Format string `json:"format,omitempty"`
	// Width and Height are the picture's size as shown (after its
	// orientation), Duration its length in seconds.
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	// Taken is when the photo was taken or the video recorded, as
	// 2006-01-02T15:04:05 with its UTC offset when it's known (the
	// camera's wall clock otherwise), or a song's year or date.
	Taken    string         `json:"taken,omitempty"`
	Location *mediaLocation `json:"location,omitempty"`
	// Place is where the file says it was taken, in words ("Eiffel
	// Tower, Paris, France", from XMP), with or without a Location.
	Place  string       `json:"place,omitempty"`
	Text   []mediaText  `json:"text,omitempty"`
	Groups []mediaGroup `json:"groups,omitempty"`
	Tags   []mediaTag   `json:"tags,omitempty"`
}

// mediaLocation is where a photo or video was taken.
type mediaLocation struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
	// Alt is the altitude in meters, Direction which way the camera
	// faced in degrees (true or magnetic north, DirectionRef "T" or "M")
	// and Accuracy the horizontal error in meters, when known.
	Alt          *float64 `json:"alt,omitempty"`
	Direction    *float64 `json:"direction,omitempty"`
	DirectionRef string   `json:"directionRef,omitempty"`
	Accuracy     *float64 `json:"accuracy,omitempty"`
	// Country is the country the point falls in on the map.
	Country string `json:"country,omitempty"`
}

// mediaText is a block of embedded text: lyrics, a description, a comment.
type mediaText struct {
	Label string `json:"label"`
	Lang  string `json:"lang,omitempty"`
	Text  string `json:"text"`
	// Synced is set for lyrics with times (SYLT), each line "[m:ss] text".
	Synced bool `json:"synced,omitempty"`
}

// mediaGroup is a titled list of details, mediaField a line of one.
type mediaGroup struct {
	Title  string       `json:"title"`
	Fields []mediaField `json:"fields"`
}

type mediaField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// mediaTag is a tag as the file has it: its group (EXIF, GPS, XMP,
// ID3...), name and value as text.
type mediaTag struct {
	Group string `json:"group"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// mediaGroupRank is the order of the detail groups, most telling first.
var mediaGroupRank = []string{"Camera", "Exposure", "Song", "Video", "Audio", "Image", "Description", "File"}

// mediaFieldRank orders each group's fields; fields it doesn't name go
// last, in the order they were found.
var mediaFieldRank = map[string][]string{
	"Camera":      {"Camera", "Lens", "Lens specification", "Device"},
	"Exposure":    {"Aperture", "Shutter speed", "ISO", "Focal length", "Exposure bias", "Flash", "Program", "Mode", "Metering", "White balance", "Light source", "Scene", "Digital zoom", "Subject distance", "Contrast", "Saturation", "Sharpness"},
	"Song":        {"Title", "Artist", "Album", "Album artist", "Track", "Disc", "Year", "Genre", "Composer", "Cover art"},
	"Video":       {"Title", "Codec", "Frame rate", "Bit rate", "Rotation", "Color"},
	"Audio":       {"Codec", "Sample rate", "Channels", "Bit depth", "Bit rate"},
	"Image":       {"Color model", "Bit depth", "Color space", "ICC profile", "Profile class", "Gamma", "Transfer", "Primaries", "HDR", "Compression", "Orientation", "Resolution", "Frames"},
	"Description": {"Title", "Creator", "Copyright", "Keywords", "Rating", "Software", "Encoder", "Digitized", "Modified"},
	"File":        {"Format", "MIME type", "Brand"},
}

// mediaCollector gathers what the parsers find in a file.
type mediaCollector struct {
	doc    *mediaInfo
	fields map[string][]mediaField
	seen   map[string]bool
	exif   []byte // a TIFF structure (from its byte order mark)
	xmp    []byte
	icc    []byte
	// tiffFile says exif is the whole file, a TIFF, whose IFD0 describes
	// the picture itself.
	tiffFile    bool
	orientation int // EXIF's, 1 to 8
	// An MP4's camera and the movie's creation time (finishMP4).
	mp4Make, mp4Model, mp4Created string
}

func newMediaCollector(doc *mediaInfo) *mediaCollector {
	return &mediaCollector{doc: doc, fields: map[string][]mediaField{}, seen: map[string]bool{}}
}

// add puts a detail in group, unless the group has that label already
// (the first source to say wins) or value is empty.
func (c *mediaCollector) add(group, label, value string) {
	value = strings.TrimSpace(value)
	if value == "" || c.seen[group+"\x00"+label] {
		return
	}
	c.seen[group+"\x00"+label] = true
	c.fields[group] = append(c.fields[group], mediaField{Label: label, Value: truncateRunes(value, mediaTagMax)})
}

// tag lists a tag for "All tags".
func (c *mediaCollector) tag(group, name, value string) {
	value = strings.TrimSpace(value)
	if value == "" || len(c.doc.Tags) >= mediaTagsMax {
		return
	}
	c.doc.Tags = append(c.doc.Tags, mediaTag{Group: group, Name: name, Value: truncateRunes(value, mediaTagMax)})
}

// text adds a block of embedded text, unless one with the same text is
// there already (a description in both EXIF and XMP).
func (c *mediaCollector) text(label, lang, text string, synced bool) {
	text = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))
	if text == "" {
		return
	}
	if len(text) > mediaTextMax {
		text = truncateRunes(text[:mediaTextMax], mediaTextMax)
	}
	for _, t := range c.doc.Text {
		if t.Text == text {
			return
		}
	}
	c.doc.Text = append(c.doc.Text, mediaText{Label: label, Lang: strings.TrimSpace(lang), Text: text, Synced: synced})
}

func (c *mediaCollector) size(w, h int) {
	if c.doc.Width == 0 && w > 0 && h > 0 {
		c.doc.Width, c.doc.Height = w, h
	}
}

func (c *mediaCollector) taken(t string) {
	if c.doc.Taken == "" {
		c.doc.Taken = t
	}
}

func (c *mediaCollector) location(l *mediaLocation) {
	if c.doc.Location != nil || l == nil || math.IsNaN(l.Lat) || math.IsNaN(l.Lon) ||
		math.Abs(l.Lat) > 90 || math.Abs(l.Lon) > 180 || (l.Lat == 0 && l.Lon == 0) {
		return
	}
	c.doc.Location = l
}

// finish ranks the groups and their fields.
func (c *mediaCollector) finish() {
	rank := func(list []string, s string) int {
		for i, v := range list {
			if v == s {
				return i
			}
		}
		return len(list)
	}
	for _, g := range mediaGroupRank {
		fields := c.fields[g]
		if len(fields) == 0 {
			continue
		}
		order := mediaFieldRank[g]
		sort.SliceStable(fields, func(i, j int) bool { return rank(order, fields[i].Label) < rank(order, fields[j].Label) })
		c.doc.Groups = append(c.doc.Groups, mediaGroup{Title: g, Fields: fields})
	}
}

// mediaKind is "image", "video" or "audio" for a file that can have
// details, or "".
func mediaKind(name string) string {
	switch ext := strings.ToLower(path.Ext(name)); {
	case isVideo(name):
		return "video"
	case isImage(name) || mediaImageExts[ext]:
		return "image"
	case isAudio(name) || mediaAudioExts[ext]:
		return "audio"
	}
	return ""
}

// The photos and songs that have details whatever the host's MIME table
// says (a small container image's may not know HEIC or FLAC).
var (
	mediaImageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".tif": true, ".tiff": true, ".heic": true, ".heif": true, ".avif": true, ".dng": true}
	mediaAudioExts = map[string]bool{".mp3": true, ".flac": true, ".ogg": true, ".oga": true, ".opus": true, ".m4a": true}
)

// mediaInfoHandler answers NAME.EXT?info and FOLDER/?worldmap, and passes
// everything else to base.
type mediaInfoHandler struct {
	base   http.Handler
	fsys   fs.FS
	source string
	vm     *videoMetaHandler // for ffmpeg's description of a video

	mu   sync.Mutex
	docs map[string][]byte
}

func newMediaInfoHandler(base http.Handler, fsys fs.FS, vm *videoMetaHandler) *mediaInfoHandler {
	return &mediaInfoHandler{base: base, fsys: fsys, source: vm.source, vm: vm, docs: map[string][]byte{}}
}

func (h *mediaInfoHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dir := strings.HasSuffix(r.URL.Path, "/") || r.URL.Path == ""
	switch {
	case dir && q.Has("worldmap"):
		serveWorldMap(w, r)
		return
	case !dir && q.Has("info"):
		name := cleanPath(strings.TrimPrefix(r.URL.Path, "/"))
		if kind := mediaKind(name); kind != "" {
			h.serveInfo(w, r, name, kind)
			return
		}
	}
	h.base.ServeHTTP(w, r)
}

func (h *mediaInfoHandler) serveInfo(w http.ResponseWriter, r *http.Request, name, kind string) {
	setRequestHandler(r, handlerMediaInfo)
	start := time.Now()
	info, ok := h.vm.statFile(w, r, name)
	if !ok {
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	key := h.source + "/" + name + "@" + fileVersion(info) + fmt.Sprintf("|ffmpeg=%t", h.vm.ff != nil)
	h.mu.Lock()
	doc, cached := h.docs[key]
	h.mu.Unlock()
	var mi *mediaInfo
	var err error
	if !cached {
		mi, err = h.build(r.Context(), name, kind, info)
		if r.Context().Err() != nil {
			return
		}
		if mi == nil {
			slog.Warn("cannot read a file's details", "path", name, "error", err)
			http.Error(w, "cannot read the file", http.StatusInternalServerError)
			return
		}
		if doc, err = json.Marshal(mi); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.mu.Lock()
		if len(h.docs) >= mediaInfoCacheMax {
			h.docs = map[string][]byte{}
		}
		h.docs[key] = doc
		h.mu.Unlock()
	}
	sum := sha256.Sum256(doc)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", `"info-`+hex.EncodeToString(sum[:12])+`"`)
	if diagEnabled() {
		fields := map[string]any{
			"file": anon(path.Base(name)), "dir": anonDir(path.Dir(name)), "kind": kind, "cached": cached,
			"ms": time.Since(start).Milliseconds(), "bytes": len(doc), "err": errCategory(err),
		}
		if mi != nil {
			fields["gps"], fields["texts"], fields["tags"] = mi.Location != nil, len(mi.Text), len(mi.Tags)
		}
		diagEvent("server", "media-info", fields)
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(doc))
}

// build reads name's details. A file whose metadata can't be read still
// has a document, with what the filesystem says; the error is for the
// log and diagnostics.
func (h *mediaInfoHandler) build(ctx context.Context, name, kind string, info fs.FileInfo) (*mediaInfo, error) {
	doc := &mediaInfo{Kind: kind, Name: path.Base(name), Size: info.Size(), Modified: info.ModTime().UTC().Format(time.RFC3339)}
	c := newMediaCollector(doc)
	f, err := h.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			slog.Error("failed to close a file after reading its details", "path", name, "error", cerr)
		}
	}()
	perr := parseMedia(newMediaSource(f, info.Size()), c)
	if perr != nil {
		slog.Debug("a file's metadata is incomplete", "path", name, "error", perr)
	}
	c.applyEmbedded()
	if kind == "video" || (kind == "audio" && doc.Duration == 0) {
		if probe, err := h.vm.probe(ctx, name, info); probe != nil {
			c.applyProbe(probe)
		} else if err != nil && perr == nil {
			perr = err
		}
	}
	c.finishMP4()
	if doc.Location != nil && doc.Location.Country == "" {
		doc.Location.Country = countryAt(doc.Location.Lat, doc.Location.Lon)
	}
	c.finish()
	return doc, perr
}

// mediaSource reads a file from its start, keeping count, and gets past
// what isn't metadata by seeking when it can.
type mediaSource struct {
	r       io.Reader
	seeker  io.Seeker // nil when the file can't seek
	pos     int64
	size    int64
	read    int64 // bytes read, against mediaInfoReadMax
	skipped int64 // bytes read past without a seek, against mediaInfoSkipMax
}

var errMediaTooLong = fmt.Errorf("mediainfo: the metadata is past what's read of a file")

func newMediaSource(f fs.File, size int64) *mediaSource {
	s := &mediaSource{r: f, size: size}
	if sk, ok := f.(io.Seeker); ok {
		// A seeker that can't (an archive's member may claim to) is
		// treated as none.
		if _, err := sk.Seek(0, io.SeekCurrent); err == nil {
			s.seeker = sk
		}
	}
	return s
}

// bytes reads the next n bytes.
func (s *mediaSource) bytes(n int64) ([]byte, error) {
	if n < 0 || s.read+n > mediaInfoReadMax {
		return nil, errMediaTooLong
	}
	b := make([]byte, n)
	k, err := io.ReadFull(s.r, b)
	s.pos += int64(k)
	s.read += int64(k)
	if err != nil {
		return b[:k], err
	}
	return b, nil
}

// skip moves on n bytes.
func (s *mediaSource) skip(n int64) error {
	if n <= 0 {
		return nil
	}
	if s.seeker != nil {
		if _, err := s.seeker.Seek(n, io.SeekCurrent); err == nil {
			s.pos += n
			return nil
		}
	}
	if s.skipped+n > mediaInfoSkipMax {
		return errMediaTooLong
	}
	k, err := io.CopyN(io.Discard, s.r, n)
	s.pos += k
	s.skipped += k
	return err
}

// seek moves to the offset off from the start: back only when the file
// can seek.
func (s *mediaSource) seek(off int64) error {
	if off == s.pos {
		return nil
	}
	if s.seeker != nil {
		if _, err := s.seeker.Seek(off, io.SeekStart); err != nil {
			return err
		}
		s.pos = off
		return nil
	}
	if off < s.pos {
		return errMediaTooLong
	}
	return s.skip(off - s.pos)
}

// unread goes back over b, the bytes just read.
func (s *mediaSource) unread(b []byte) error {
	if s.seeker != nil {
		return s.seek(s.pos - int64(len(b)))
	}
	s.r = io.MultiReader(bytes.NewReader(b), s.r)
	s.pos -= int64(len(b))
	return nil
}

// parseMedia reads the file's metadata into c, by what its first bytes
// say it is rather than its name.
func parseMedia(s *mediaSource, c *mediaCollector) error {
	head, err := s.bytes(12)
	if len(head) < 4 {
		return err
	}
	// Each parser reads the file from its start.
	if err := s.unread(head); err != nil {
		return err
	}
	switch {
	case head[0] == 0xFF && head[1] == 0xD8:
		return parseJPEG(s, c)
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return parsePNG(s, c)
	case len(head) == 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return parseWebP(s, c)
	case string(head[:4]) == "II*\x00" || string(head[:4]) == "MM\x00*":
		return parseTIFFFile(s, c)
	case len(head) >= 8 && string(head[4:8]) == "ftyp":
		return parseISOBMFF(s, c)
	case string(head[:3]) == "ID3" || (head[0] == 0xFF && head[1]&0xE0 == 0xE0):
		return parseMP3(s, c)
	case string(head[:4]) == "fLaC":
		return parseFLAC(s, c)
	case string(head[:4]) == "OggS":
		return parseOgg(s, c)
	case bytes.HasPrefix(head, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		c.add("File", "Format", "Matroska")
		c.doc.Format = "Matroska"
		if strings.EqualFold(path.Ext(c.doc.Name), ".webm") {
			c.doc.Format = "WebM"
			c.add("File", "Format", "WebM")
		}
	}
	return nil
}

// ---- The world map, and the country a point is in ----

// worldMapJSON is Natural Earth's countries, simplified (see
// internal/tools/worldmap): {"q": Q, "c": [[NAME, RING...], ...]}, each
// ring's points in 1/Q degrees, each after the first as a step from the
// one before.
//
//go:embed geodata/worldmap.json
var worldMapJSON []byte

var worldMapETag = func() string {
	sum := sha256.Sum256(worldMapJSON)
	return `"worldmap-` + hex.EncodeToString(sum[:8]) + `"`
}()

// serveWorldMap answers ?worldmap with the map, which never changes for a
// build: the browser keeps it a day and then asks if it changed.
func serveWorldMap(w http.ResponseWriter, r *http.Request) {
	setRequestHandler(r, handlerMediaInfo)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("ETag", worldMapETag)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(worldMapJSON))
}

type geoRing struct {
	pts                    [][2]float64
	minX, minY, maxX, maxY float64
}

type geoCountry struct {
	name  string
	rings []geoRing
}

var worldCountries = sync.OnceValue(func() []geoCountry {
	var m struct {
		Q float64             `json:"q"`
		C [][]json.RawMessage `json:"c"`
	}
	if err := json.Unmarshal(worldMapJSON, &m); err != nil || m.Q <= 0 {
		slog.Error("the embedded world map can't be read", "error", err)
		return nil
	}
	out := make([]geoCountry, 0, len(m.C))
	for _, row := range m.C {
		if len(row) < 2 {
			continue
		}
		var gc geoCountry
		if json.Unmarshal(row[0], &gc.name) != nil {
			continue
		}
		for _, raw := range row[1:] {
			var enc []int
			if json.Unmarshal(raw, &enc) != nil || len(enc) < 6 {
				continue
			}
			g := geoRing{minX: math.Inf(1), minY: math.Inf(1), maxX: math.Inf(-1), maxY: math.Inf(-1)}
			x, y := 0, 0
			for i := 0; i+1 < len(enc); i += 2 {
				x, y = x+enc[i], y+enc[i+1]
				p := [2]float64{float64(x) / m.Q, float64(y) / m.Q}
				g.pts = append(g.pts, p)
				g.minX, g.maxX = math.Min(g.minX, p[0]), math.Max(g.maxX, p[0])
				g.minY, g.maxY = math.Min(g.minY, p[1]), math.Max(g.maxY, p[1])
			}
			gc.rings = append(gc.rings, g)
		}
		out = append(out, gc)
	}
	return out
})

// countryNear is how far off a country's simplified coast a point may be,
// in degrees, and still be named in it: a photo from a beach or a pier.
const countryNear = 0.25

// countryAt names the country the point is in, or the nearest within
// countryNear, or "" at sea.
func countryAt(lat, lon float64) string {
	best, bestD := "", countryNear
	for _, c := range worldCountries() {
		for _, r := range c.rings {
			if lon < r.minX-countryNear || lon > r.maxX+countryNear || lat < r.minY-countryNear || lat > r.maxY+countryNear {
				continue
			}
			if pointInRing(r.pts, lon, lat) {
				return c.name
			}
			if d := ringDistance(r.pts, lon, lat); d < bestD {
				best, bestD = c.name, d
			}
		}
	}
	return best
}

func pointInRing(pts [][2]float64, x, y float64) bool {
	in := false
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		a, b := pts[i], pts[j]
		if (a[1] > y) != (b[1] > y) && x < (b[0]-a[0])*(y-a[1])/(b[1]-a[1])+a[0] {
			in = !in
		}
	}
	return in
}

func ringDistance(pts [][2]float64, x, y float64) float64 {
	d := math.Inf(1)
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		a, b := pts[j], pts[i]
		dx, dy := b[0]-a[0], b[1]-a[1]
		t := 0.0
		if l := dx*dx + dy*dy; l > 0 {
			t = math.Max(0, math.Min(1, ((x-a[0])*dx+(y-a[1])*dy)/l))
		}
		d = math.Min(d, math.Hypot(x-(a[0]+t*dx), y-(a[1]+t*dy)))
	}
	return d
}
