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
	"archive/zip"
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	gowstesting "github.com/cloudfra/gowebserver/internal/gowebserver/testing"
)

// mediaInfoOf reads data's details as ?info would, for a file called name.
func mediaInfoOf(t *testing.T, name string, data []byte) *mediaInfo {
	t.Helper()
	fsys := fstest.MapFS{name: {Data: data, ModTime: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)}}
	h := newMediaInfoHandler(http.NotFoundHandler(), fsys, newVideoMetaHandler(http.NotFoundHandler(), fsys, "mem", nil, nil))
	info, err := fs.Stat(fsys, name)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := h.build(t.Context(), name, mediaKind(name), info)
	if doc == nil {
		t.Fatalf("build(%s): %v", name, err)
	}
	return doc
}

// group returns a details group's fields as "Label: value" lines.
func group(doc *mediaInfo, title string) []string {
	for _, g := range doc.Groups {
		if g.Title == title {
			var out []string
			for _, f := range g.Fields {
				out = append(out, f.Label+": "+f.Value)
			}
			return out
		}
	}
	return nil
}

func groupTitles(doc *mediaInfo) []string {
	out := make([]string, 0, len(doc.Groups))
	for _, g := range doc.Groups {
		out = append(out, g.Title)
	}
	return out
}

func hasTag(doc *mediaInfo, group, name, value string) bool {
	for _, tg := range doc.Tags {
		if tg.Group == group && tg.Name == name && (value == "" || tg.Value == value) {
			return true
		}
	}
	return false
}

func ptr(v float64) *float64 { return &v }

func TestMediaInfoJPEG(t *testing.T) {
	doc := mediaInfoOf(t, "tower.jpg", testJPEG(t, 64, 48, photoExif(), photoXMP, testICC("Display P3")))

	// Orientation 6: the 64x48 picture is shown 48x64.
	if doc.Kind != "image" || doc.Format != "JPEG" || doc.Width != 48 || doc.Height != 64 {
		t.Errorf("kind, format, size = %s %s %dx%d", doc.Kind, doc.Format, doc.Width, doc.Height)
	}
	if doc.Taken != "2024-05-01T18:45:10+02:00" {
		t.Errorf("taken = %q", doc.Taken)
	}
	want := &mediaLocation{Lat: 48.8584, Lon: 2.2945, Alt: ptr(35.1), Direction: ptr(135.3), DirectionRef: "T", Accuracy: ptr(5), Country: "France"}
	if diff := cmp.Diff(want, doc.Location); diff != "" {
		t.Errorf("location (-want +got):\n%s", diff)
	}
	if doc.Place != "Eiffel Tower, Paris, France" {
		t.Errorf("place = %q", doc.Place)
	}
	if diff := cmp.Diff([]string{"Camera", "Exposure", "Image", "Description", "File"}, groupTitles(doc)); diff != "" {
		t.Errorf("groups (-want +got):\n%s", diff)
	}
	for title, want := range map[string][]string{
		"Camera": {"Camera: Apple iPhone 15 Pro", "Lens: iPhone 15 Pro back triple camera 6.765mm f/1.78", "Lens specification: 2.2–9 mm ƒ/1.8–2.8"},
		"Exposure": {
			"Aperture: ƒ/1.8", "Shutter speed: 1/250 s", "ISO: 64", "Focal length: 6.8 mm (24 mm in 35 mm)",
			"Exposure bias: -0.33 EV", "Flash: Off, did not fire", "Program: Program AE", "Metering: Multi-segment", "White balance: Auto",
		},
		"Image": {
			"Color model: YCbCr 4:2:0", "Bit depth: 8 bits per channel", "Color space: Display P3", "ICC profile: Display P3",
			"Profile class: Display, ICC v2.1", "Gamma: 2.2", "Compression: JPEG, baseline", "Orientation: Rotated 90° clockwise", "Resolution: 72 dpi",
		},
		"Description": {"Title: Evening at the tower", "Keywords: paris, sunset", "Rating: ★★★★☆", "Software: 17.4", "Modified: 2024-05-01T18:45:10"},
		"File":        {"Format: JPEG"},
	} {
		if diff := cmp.Diff(want, group(doc, title)); diff != "" {
			t.Errorf("%s (-want +got):\n%s", title, diff)
		}
	}
	// The camera's stock description is left out; the photographer's
	// words aren't.
	wantText := []mediaText{
		{Label: "Comment", Text: "Sunset from the Trocadéro"},
		{Label: "Description", Text: "The tower lights up as the sun goes down."},
	}
	if diff := cmp.Diff(wantText, doc.Text); diff != "" {
		t.Errorf("text (-want +got):\n%s", diff)
	}
	for _, tg := range [][3]string{
		{"EXIF", "Make", "Apple"},
		{"EXIF", "MakerNote", "(40 bytes)"},
		{"GPS", "GPSLatitudeRef", "N"},
		{"XMP", "dc:title", "Evening at the tower"},
		{"XMP", "xmp:Rating", "4"},
		{"ICC", "Description", "Display P3"},
		{"ICC", "Created", "2024-03-09"},
	} {
		if !hasTag(doc, tg[0], tg[1], tg[2]) {
			t.Errorf("no tag %v in %+v", tg, doc.Tags)
		}
	}
}

func TestMediaInfoNoMetadata(t *testing.T) {
	doc := mediaInfoOf(t, "plain.jpg", testJPEG(t, 30, 20, nil, "", nil))
	if doc.Width != 30 || doc.Height != 20 || doc.Location != nil || doc.Taken != "" || len(doc.Text) != 0 {
		t.Errorf("doc = %+v", doc)
	}
	if doc.Modified != "2024-06-01T00:00:00Z" || doc.Size == 0 || doc.Name != "plain.jpg" {
		t.Errorf("file facts = %q %d %q", doc.Modified, doc.Size, doc.Name)
	}
	// Not what its name says: read by what it is.
	doc = mediaInfoOf(t, "really-a-png.jpg", testPNG(t))
	if doc.Format != "PNG" || doc.Width != 40 {
		t.Errorf("format = %q, width %d", doc.Format, doc.Width)
	}
	// Not a picture at all.
	doc = mediaInfoOf(t, "notes.jpg", []byte("just some text"))
	if doc.Format != "" || len(doc.Groups) != 0 {
		t.Errorf("doc = %+v", doc)
	}
}

func TestMediaInfoPNG(t *testing.T) {
	itxt := append([]byte("Description\x00\x00\x00en\x00\x00"), "A gradient, for testing"...)
	doc := mediaInfoOf(t, "g.png", testPNG(t,
		pngChunkBytes("gAMA", u32(45455)),
		pngChunkBytes("cICP", []byte{9, 16, 0, 1}),
		pngChunkBytes("pHYs", append(append(u32(11811), u32(11811)...), 1)),
		pngChunkBytes("tEXt", []byte("Software\x00GIMP 2.10")),
		pngChunkBytes("iTXt", itxt),
		pngChunkBytes("eXIf", buildTIFF([]tiffField{tASCII(0x0110, "Scanner")}, nil, nil)),
	))
	want := []string{
		"Color model: RGB", "Bit depth: 8 bits per channel", "Color space: BT.2020", "Gamma: 2.2",
		"Transfer: PQ (SMPTE ST 2084)", "Primaries: BT.2020", "HDR: HDR10 (PQ)", "Compression: Deflate", "Resolution: 300 dpi",
	}
	if diff := cmp.Diff(want, group(doc, "Image")); diff != "" {
		t.Errorf("Image (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"Camera: Scanner"}, group(doc, "Camera")); diff != "" {
		t.Errorf("Camera (-want +got):\n%s", diff)
	}
	if len(doc.Text) != 1 || doc.Text[0].Text != "A gradient, for testing" {
		t.Errorf("text = %+v", doc.Text)
	}
	if diff := cmp.Diff([]string{"Software: GIMP 2.10"}, group(doc, "Description")); diff != "" {
		t.Errorf("Description (-want +got):\n%s", diff)
	}
}

func TestMediaInfoWebP(t *testing.T) {
	exif := buildTIFF([]tiffField{tASCII(0x010F, "Google"), tASCII(0x0110, "Pixel 8")}, nil,
		[]tiffField{tASCII(0x01, "S"), tRational(0x02, 33, 1, 51, 1, 0, 1), tASCII(0x03, "E"), tRational(0x04, 151, 1, 12, 1, 0, 1)})
	chunk := func(typ string, data []byte) []byte {
		out := append([]byte(typ), byte(len(data)), byte(len(data)>>8), byte(len(data)>>16), byte(len(data)>>24))
		out = append(out, data...)
		if len(data)%2 == 1 {
			out = append(out, 0)
		}
		return out
	}
	vp8x := []byte{0x08 | 0x10, 0, 0, 0, 0x7F, 0x07, 0, 0x37, 0x04, 0} // 1920x1080, EXIF and alpha
	vp8l := append([]byte{0x2f}, make([]byte, 100)...)
	body := append(append(append([]byte("WEBP"), chunk("VP8X", vp8x)...), chunk("VP8L", vp8l)...), chunk("EXIF", exif)...)
	file := append(append([]byte("RIFF"), byte(len(body)), byte(len(body)>>8), byte(len(body)>>16), byte(len(body)>>24)), body...)
	doc := mediaInfoOf(t, "harbour.webp", file)
	if doc.Format != "WebP" || doc.Width != 1920 || doc.Height != 1080 {
		t.Errorf("format, size = %s %dx%d", doc.Format, doc.Width, doc.Height)
	}
	if doc.Location == nil || doc.Location.Lat != -33.85 || doc.Location.Lon != 151.2 || doc.Location.Country != "Australia" {
		t.Errorf("location = %+v", doc.Location)
	}
	if diff := cmp.Diff([]string{"Camera: Google Pixel 8"}, group(doc, "Camera")); diff != "" {
		t.Errorf("Camera (-want +got):\n%s", diff)
	}
}

func TestMediaInfoTIFF(t *testing.T) {
	file := buildTIFF([]tiffField{
		tLong(0x0100, 6000), tLong(0x0101, 4000), tShort(0x0102, 16, 16, 16), tShort(0x0103, 1), tShort(0x0106, 2),
		tASCII(0x010F, "NIKON CORPORATION"), tASCII(0x0110, "NIKON Z 8"), tByte(0xC612, 1, 6, 0, 0),
	}, []tiffField{tRational(0x829D, 8, 1)}, nil)
	doc := mediaInfoOf(t, "raw.dng", file)
	if doc.Format != "DNG" || doc.Width != 6000 || doc.Height != 4000 {
		t.Errorf("format, size = %s %dx%d", doc.Format, doc.Width, doc.Height)
	}
	if diff := cmp.Diff([]string{"Camera: NIKON Z 8"}, group(doc, "Camera")); diff != "" {
		t.Errorf("Camera (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"Color model: RGB", "Bit depth: 16 bits per channel", "Compression: None"}, group(doc, "Image")); diff != "" {
		t.Errorf("Image (-want +got):\n%s", diff)
	}
}

func TestMediaInfoHEIF(t *testing.T) {
	doc := mediaInfoOf(t, "IMG_0001.HEIC", testHEIF(photoExif()))
	// Orientation 6 again.
	if doc.Format != "HEIF" || doc.Width != 3024 || doc.Height != 4032 {
		t.Errorf("format, size = %s %dx%d", doc.Format, doc.Width, doc.Height)
	}
	if doc.Location == nil || doc.Location.Country != "France" || doc.Taken != "2024-05-01T18:45:10+02:00" {
		t.Errorf("location, taken = %+v %q", doc.Location, doc.Taken)
	}
	want := []string{
		"Color model: YCbCr", "Bit depth: 10 bits per channel", "Color space: Display P3", "Transfer: sRGB",
		"Primaries: Display P3", "Compression: HEVC", "Orientation: Rotated 90° clockwise", "Resolution: 72 dpi",
	}
	if diff := cmp.Diff(want, group(doc, "Image")); diff != "" {
		t.Errorf("Image (-want +got):\n%s", diff)
	}
}

func TestMediaInfoMP4(t *testing.T) {
	doc := mediaInfoOf(t, "walk.mp4", testMP4(1<<20))
	if doc.Kind != "video" || doc.Format != "MPEG-4" || doc.Duration != 9.5 || doc.Width != 1080 || doc.Height != 1920 {
		t.Errorf("kind, format, duration, size = %s %s %v %dx%d", doc.Kind, doc.Format, doc.Duration, doc.Width, doc.Height)
	}
	if doc.Taken != "2024-05-02T08:00:00Z" {
		t.Errorf("taken = %q", doc.Taken)
	}
	if doc.Location == nil || doc.Location.Lat != 35.6586 || doc.Location.Lon != 139.7454 || doc.Location.Country != "Japan" {
		t.Errorf("location = %+v", doc.Location)
	}
	if diff := cmp.Diff([]mediaText{{Label: "Lyrics", Text: "Neon on the water\nTrains in the rain"}}, doc.Text); diff != "" {
		t.Errorf("text (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"Codec: H.264", "Frame rate: 30 fps", "Rotation: 90°"}, group(doc, "Video")); diff != "" {
		t.Errorf("Video (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"Title: Night walk", "Software: Lavf61.1.100"}, group(doc, "Description")); diff != "" {
		t.Errorf("Description (-want +got):\n%s", diff)
	}
}

// noSeek hides a file's Seek, as an archive's members may not have one.
type noSeek struct{ fs.File }

type noSeekFS struct{ fs.FS }

func (n noSeekFS) Open(name string) (fs.File, error) {
	f, err := n.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return noSeek{f}, nil
}

// TestMediaInfoWithoutSeeking reads an MP4 whose moov is after its media
// data, and a WebP whose EXIF is after its picture, from files that can't
// seek: read past, up to mediaInfoSkipMax, and no further.
func TestMediaInfoWithoutSeeking(t *testing.T) {
	fsys := noSeekFS{fstest.MapFS{
		"walk.mp4": {Data: testMP4(1 << 20)},
		"huge.mp4": {Data: testMP4(mediaInfoSkipMax + 1)},
		"p.heic":   {Data: testHEIF(photoExif())},
	}}
	h := newMediaInfoHandler(http.NotFoundHandler(), fsys, newVideoMetaHandler(http.NotFoundHandler(), fsys, "mem", nil, nil))
	build := func(name string) *mediaInfo {
		info, err := fs.Stat(fsys.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		// The huge one's moov is out of reach: that's its error.
		doc, err := h.build(t.Context(), name, mediaKind(name), info)
		if doc == nil {
			t.Fatalf("%s: %v", name, err)
		}
		return doc
	}
	if doc := build("walk.mp4"); len(doc.Text) != 1 || doc.Duration != 9.5 {
		t.Errorf("walk.mp4 = %+v", doc)
	}
	if doc := build("p.heic"); doc.Location == nil {
		t.Errorf("p.heic = %+v", doc)
	}
	if doc := build("huge.mp4"); doc.Duration != 0 || doc.Size == 0 {
		t.Errorf("huge.mp4 = %+v", doc)
	}
}

func TestMediaInfoMP3(t *testing.T) {
	doc := mediaInfoOf(t, "harbor.mp3", testMP3())
	if doc.Kind != "audio" || doc.Format != "MP3" || doc.Taken != "2019" {
		t.Errorf("kind, format, taken = %s %s %q", doc.Kind, doc.Format, doc.Taken)
	}
	want := []string{"Title: Harbor Lights", "Artist: The Example Band", "Album: Test Tones", "Track: 3 of 12", "Year: 2019", "Genre: Rock", "Cover art: JPEG (2 kB)"}
	if diff := cmp.Diff(want, group(doc, "Song")); diff != "" {
		t.Errorf("Song (-want +got):\n%s", diff)
	}
	want = []string{"Codec: MP3 (MPEG-1 Layer III)", "Sample rate: 44.1 kHz", "Channels: Joint stereo", "Bit rate: 128 kb/s"}
	if diff := cmp.Diff(want, group(doc, "Audio")); diff != "" {
		t.Errorf("Audio (-want +got):\n%s", diff)
	}
	wantText := []mediaText{
		{Label: "Lyrics", Lang: "eng", Text: "Lights on the harbor\nShips on the sea"},
		{Label: "Synced lyrics", Lang: "eng", Text: "[0:01.50] First line\n[1:05.25] Second line", Synced: true},
	}
	if diff := cmp.Diff(wantText, doc.Text); diff != "" {
		t.Errorf("text (-want +got):\n%s", diff)
	}
	if doc.Duration <= 0 {
		t.Errorf("duration = %v", doc.Duration)
	}
}

func TestMediaInfoFLACAndOgg(t *testing.T) {
	doc := mediaInfoOf(t, "tide.flac", testFLAC())
	if doc.Duration != 3 || len(doc.Text) != 1 || doc.Text[0].Text != "Low tide\nhigh moon" {
		t.Errorf("flac = %+v", doc)
	}
	if diff := cmp.Diff([]string{"Codec: FLAC", "Sample rate: 48 kHz", "Channels: Stereo", "Bit depth: 24 bits"}, group(doc, "Audio")); diff != "" {
		t.Errorf("Audio (-want +got):\n%s", diff)
	}
	doc = mediaInfoOf(t, "morning.opus", testOpus())
	if doc.Format != "Ogg" || doc.Duration != 2 || len(doc.Text) != 1 || doc.Text[0].Text != "Sun comes up" {
		t.Errorf("opus = %+v", doc)
	}
	if diff := cmp.Diff([]string{"Title: Morning"}, group(doc, "Song")); diff != "" {
		t.Errorf("Song (-want +got):\n%s", diff)
	}
}

func TestISO6709(t *testing.T) {
	for in, want := range map[string][3]float64{
		"+48.8584+002.2945+035.000/": {48.8584, 2.2945, 35},
		"-33.8568+151.2153/":         {-33.8568, 151.2153, 0},
		"+4851.504+00217.670/":       {48.8584, 2.294500, 0},
		"+405042-0735943/":           {40.845, -73.995278, 0},
	} {
		l := iso6709(in)
		if l == nil || l.Lat != want[0] || l.Lon != want[1] || (want[2] != 0 && (l.Alt == nil || *l.Alt != want[2])) {
			t.Errorf("iso6709(%q) = %+v", in, l)
		}
	}
	if iso6709("nowhere") != nil {
		t.Error("iso6709 read a location from nothing")
	}
}

func TestCountryAt(t *testing.T) {
	for _, c := range []struct {
		lat, lon float64
		want     string
	}{
		{48.8584, 2.2945, "France"},
		{35.6586, 139.7454, "Japan"},
		{-33.8568, 151.2153, "Australia"},
		{40.6892, -74.0445, "United States of America"}, // the Statue of Liberty, on its island
		{-22.9519, -43.2105, "Brazil"},
		{30, -40, ""}, // the middle of the Atlantic
	} {
		if got := countryAt(c.lat, c.lon); got != c.want {
			t.Errorf("countryAt(%v, %v) = %q, want %q", c.lat, c.lon, got, c.want)
		}
	}
}

func TestMediaInfoHandler(t *testing.T) {
	dir := t.TempDir()
	photo := testJPEG(t, 64, 48, photoExif(), photoXMP, nil)
	writeFiles(t, dir, map[string]string{"Trip/tower.jpg": string(photo), "Trip/notes.txt": "hello"})
	h := videoMetaServer(t, dir, "", true)

	w := serveGet(t, h, "/Trip/tower.jpg?info")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("GET ?info = %d %v %s", w.Code, w.Header(), w.Body)
	}
	var doc mediaInfo
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Name != "tower.jpg" || doc.Location == nil || doc.Location.Country != "France" {
		t.Errorf("doc = %+v", doc)
	}
	etag := w.Header().Get("ETag")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/Trip/tower.jpg?info", nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("again with its ETag = %d", rec.Code)
	}
	// The file changes, and so does its document.
	later := time.Now().Add(time.Hour)
	if err := os.WriteFile(filepath.Join(dir, "Trip", "tower.jpg"), testJPEG(t, 64, 48, nil, "", nil), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "Trip", "tower.jpg"), later, later); err != nil {
		t.Fatal(err)
	}
	if w := serveGet(t, h, "/Trip/tower.jpg?info"); w.Header().Get("ETag") == etag || strings.Contains(w.Body.String(), "France") {
		t.Errorf("a changed file kept its document: %s", w.Body)
	}

	// Not media: the file itself, as before.
	if w := serveGet(t, h, "/Trip/notes.txt?info"); w.Code != http.StatusOK || w.Body.String() != "hello" {
		t.Errorf("notes.txt?info = %d %s", w.Code, w.Body)
	}
	if w := serveGet(t, h, "/Trip/missing.jpg?info"); w.Code != http.StatusNotFound {
		t.Errorf("missing.jpg?info = %d", w.Code)
	}
	if w := serveGet(t, h, "/../etc/passwd.jpg?info"); w.Code == http.StatusOK {
		t.Errorf("a path out of the folder = %d", w.Code)
	}
	// A folder's listing is still its listing.
	if w := serveGet(t, h, "/Trip/"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "tower.jpg") {
		t.Errorf("listing = %d", w.Code)
	}
}

func TestWorldMap(t *testing.T) {
	h := videoMetaServer(t, t.TempDir(), "", false)
	w := serveGet(t, h, "/?worldmap")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" || !strings.Contains(w.Header().Get("Cache-Control"), "max-age") {
		t.Fatalf("?worldmap = %d %v", w.Code, w.Header())
	}
	var m struct {
		Q int     `json:"q"`
		C [][]any `json:"c"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || m.Q != 20 || len(m.C) < 200 {
		t.Fatalf("map = q %d, %d countries, %v", m.Q, len(m.C), err)
	}
	// Small enough to fetch when the panel first shows a place.
	if n := len(worldMapJSON); n > 200<<10 {
		t.Errorf("the map is %d bytes", n)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?worldmap", nil)
	req.Header.Set("If-None-Match", w.Header().Get("ETag"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("again with its ETag = %d", rec.Code)
	}
}

// TestMediaInfoInArchive reads details from inside a zip, through the
// served filesystem.
func TestMediaInfoInArchive(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range map[string][]byte{"album/tower.jpg": testJPEG(t, 64, 48, photoExif(), "", nil), "album/walk.mp4": testMP4(4096)} {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(t.TempDir(), "photos.zip")
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	h := videoMetaServer(t, zipPath, "", false)
	m := getMeta(t, h, "/album/tower.jpg?info")
	if loc, _ := m["location"].(map[string]any); loc == nil || loc["country"] != "France" {
		t.Errorf("tower.jpg in a zip = %v", m)
	}
	m = getMeta(t, h, "/album/walk.mp4?info")
	if m["duration"] != 9.5 {
		t.Errorf("walk.mp4 in a zip = %v", m)
	}
}

func TestMediaInfoDiagnostics(t *testing.T) {
	_, read := withDiagnostics(t)
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"Secret Vacation/Alice beach.jpg": string(testJPEG(t, 64, 48, photoExif(), photoXMP, nil))})
	h := videoMetaServer(t, dir, "", false)
	getMeta(t, h, "/Secret%20Vacation/Alice%20beach.jpg?info")
	log := read()
	for _, want := range []string{"server media-info file=" + anon("Alice beach.jpg") + " dir=" + anonDir("Secret Vacation"), "kind=image", "gps=true", "texts=2"} {
		if !strings.Contains(log, want) {
			t.Errorf("missing %q in\n%s", want, log)
		}
	}
	if m := secret.FindString(log); m != "" {
		t.Errorf("the log has %q in it:\n%s", m, log)
	}
}

// TestMediaInfoFFmpeg is a video ffmpeg describes: its streams, and the
// lyrics and location in its container's tags.
func TestMediaInfoFFmpeg(t *testing.T) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "clip.mkv")
	if b, err := exec.Command(bin, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=10:duration=2",
		"-metadata", "LYRICS=Line one\nLine two", "-metadata", "title=A clip", "-pix_fmt", "yuv420p", out).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg can't make a test video: %v: %s", err, b)
	}
	h := videoMetaServer(t, dir, bin, false)
	var doc mediaInfo
	if err := json.Unmarshal(serveGet(t, h, "/clip.mkv?info").Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Format != "Matroska" || doc.Width != 320 || doc.Height != 180 || doc.Duration < 1.9 {
		t.Errorf("doc = %+v", doc)
	}
	if len(doc.Text) != 1 || doc.Text[0].Text != "Line one\nLine two" {
		t.Errorf("text = %+v", doc.Text)
	}
	if v := group(&doc, "Video"); len(v) == 0 || !strings.HasPrefix(v[0], "Codec: ") {
		t.Errorf("Video = %v", v)
	}
}

// TestMediaFixtures checks the media the browser tests and screenshots
// use (internal/gowebserver/testing/media) say what those expect.
func TestMediaFixtures(t *testing.T) {
	doc := mediaInfoOf(t, "eiffel-tower.jpg", gowstesting.MustMediaFile(t, "eiffel-tower.jpg"))
	if doc.Location == nil || doc.Location.Country != "France" || len(doc.Text) != 2 {
		t.Errorf("eiffel-tower.jpg = %+v", doc)
	}
	doc = mediaInfoOf(t, "night-walk.mp4", gowstesting.MustMediaFile(t, "night-walk.mp4"))
	if doc.Location == nil || doc.Location.Country != "Japan" || len(doc.Text) == 0 || doc.Text[0].Label != "Lyrics" {
		t.Errorf("night-walk.mp4 = %+v", doc)
	}
}

// nightWalkLyrics are night-walk.mp4's.
const nightWalkLyrics = `Neon on the water,
trains hum in the rain.
Every window a story,
every street a refrain.

Walk me home slowly,
the city's awake —
we'll count all the lights
that the morning will take.`

// TestWriteMediaFixtures remakes internal/gowebserver/testing/media when
// GOWS_WRITE_MEDIA_FIXTURES is set: the photo from testJPEG, and the
// video with ffmpeg (on PATH, as make puts it), VP9 and Opus so every
// browser the browser tests run in plays it, with lyrics, a title and a
// location in its tags.
func TestWriteMediaFixtures(t *testing.T) {
	if os.Getenv("GOWS_WRITE_MEDIA_FIXTURES") == "" {
		t.Skip("set GOWS_WRITE_MEDIA_FIXTURES to remake the fixtures")
	}
	dir := filepath.Join("..", "..", "internal", "gowebserver", "testing", "media")
	if err := os.WriteFile(filepath.Join(dir, "eiffel-tower.jpg"), testJPEG(t, 1200, 800, photoExifWith(1), photoXMP, testICC("Display P3")), 0o644); err != nil {
		t.Fatal(err)
	}
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("ffmpeg is not installed")
	}
	cmd := exec.Command(bin, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "gradients=s=640x360:c0=0x1b2a6b:c1=0xff7a59:c2=0x2bd2ff:speed=0.02:duration=4:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=330:duration=4",
		"-pix_fmt", "yuv420p", "-c:v", "libvpx-vp9", "-b:v", "120k", "-c:a", "libopus", "-b:a", "48k",
		"-metadata", "title=Night walk", "-metadata", "lyrics="+nightWalkLyrics,
		"-metadata", "com.apple.quicktime.location.ISO6709=+35.6586+139.7454+040.000/", "-metadata", "creation_time=2024-10-12T21:04:00Z",
		"-movflags", "+faststart+use_metadata_tags", filepath.Join(dir, "night-walk.mp4"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, b)
	}
}
