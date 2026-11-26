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
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	gowstesting "github.com/cloudfra/gowebserver/internal/gowebserver/testing"
)

// rawOf returns the raw entries under path, as "key: value" lines.
func rawOf(doc *mediaInfo, path string) []string {
	var out []string
	for _, r := range doc.Raw {
		if r.Path == path {
			out = append(out, r.Key+": "+r.Value)
		}
	}
	return out
}

func rawValue(doc *mediaInfo, path, key string) string {
	for _, r := range doc.Raw {
		if r.Path == path && r.Key == key {
			return r.Value
		}
	}
	return ""
}

func structureOf(doc *mediaInfo, source, name string) *mediaStructure {
	for i, s := range doc.Structures {
		if s.Source == source && s.Name == name {
			return &doc.Structures[i]
		}
	}
	return nil
}

func TestAMF0(t *testing.T) {
	b := append(amfString("event"), amfObjectBytes("n", amfNumber(2.5), "ok", []byte{1, 1}, "none", []byte{5},
		"list", append([]byte{10, 0, 0, 0, 2}, append(amfNumber(1), amfString("two")...)...),
		"ecma", append([]byte{8, 0, 0, 0, 1}, append(append(u16(1), 'k'), append(amfString("v"), 0, 0, 9)...)...))...)
	vals := amf0Values(b)
	if len(vals) != 2 || vals[0] != "event" {
		t.Fatalf("values = %#v", vals)
	}
	var lines []string
	flatten("", vals[1], func(k, v string) { lines = append(lines, k+"="+v) }, 0)
	want := []string{"n=2.5", "ok=true", "none=null", `list=[1,"two"]`, "ecma.k=v"}
	if diff := cmp.Diff(want, lines); diff != "" {
		t.Errorf("flattened (-want +got):\n%s", diff)
	}
	// Cut short, it reads what it can.
	if got := amf0Values(b[:len(b)-12]); len(got) == 0 || got[0] != "event" {
		t.Errorf("a cut message = %#v", got)
	}
	if got := amf0Values([]byte{2, 0, 9, 'a'}); len(got) != 0 {
		t.Errorf("a string longer than its message = %#v", got)
	}
	// Text holding JSON, or zlib'd JSON in base64, is what it holds; a
	// picture is its type and size.
	lines = nil
	v := `{"a":{"b":[1,2]},"z":"` + compressedSettings() + `","img":"data:image/png;base64,` + strings.Repeat("A", 80) + `"}`
	flatten("p", v, func(k, v string) { lines = append(lines, k+"="+v) }, 0)
	want = []string{"p.a.b=[1,2]", "p.img=image/png image", "p.z.settings.exposure.ev=0.3", "p.z.settings.exposure.mode=auto", "p.z.settings.fov_deg=120"}
	if diff := cmp.Diff(want, lines); diff != "" {
		t.Errorf("JSON in a string (-want +got):\n%s", diff)
	}
}

func TestSEIUserData(t *testing.T) {
	// An emulation prevention byte in the payload is taken out.
	payload := []byte("x264 - core 999 - H.264/MPEG-4 AVC codec - options: a=0\x00\x00\x01")
	nal := seiNAL("dc45e9bde6d948b7962cd820d923eeef", payload)
	if !bytes.Contains(nal, []byte{0, 0, 3, 1}) {
		t.Fatalf("the NAL isn't escaped: %q", nal)
	}
	ms := seiUserData(nal, 4, false)
	if len(ms) != 1 || ms[0].uuid != "dc45e9bde6d948b7962cd820d923eeef" || !bytes.Equal(ms[0].payload, payload) {
		t.Fatalf("messages = %+v", ms)
	}
	// JSON objects one after another, by lines or NULs.
	objs := jsonLines([]byte("{\"type\":\"a\"}\n{\"type\":\"b\"}\x00{\"type\":\"c\"}\x00\x00"))
	if len(objs) != 3 || objs[2]["type"] != "c" {
		t.Errorf("objects = %v", objs)
	}
}

func TestSampleTable(t *testing.T) {
	// Five samples in two chunks (three, then two).
	stbl := bytes.Join([][]byte{
		isoBoxBytes("stts", u32(0), u32(2), u32(3), u32(100), u32(2), u32(200)),
		isoBoxBytes("stsc", u32(0), u32(2), u32(1), u32(3), u32(1), u32(2), u32(2), u32(1)),
		isoBoxBytes("stsz", u32(0), u32(0), u32(5), u32(10), u32(20), u32(30), u32(40), u32(50)),
		isoBoxBytes("stco", u32(0), u32(2), u32(1000), u32(5000)),
	}, nil)
	st := readSampleTable(stbl, 100, 100)
	if diff := cmp.Diff([]int64{1000, 1010, 1030, 5000, 5040}, st.off); diff != "" {
		t.Errorf("offsets (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]float64{0, 1, 2, 3, 5}, st.time); diff != "" {
		t.Errorf("times (-want +got):\n%s", diff)
	}
	if st := readSampleTable(stbl, 100, 2); len(st.off) != 2 {
		t.Errorf("at most 2: %v", st.off)
	}
}

// TestMediaInfoStructures reads an MP4 with a timed JSON track, an AMF0
// track, SEI JSON, mdta keys and XMP: each kind of message is one
// structure with what its fields did, every key is in Raw, and the
// readings' coordinates are where it was made.
func TestMediaInfoStructures(t *testing.T) {
	doc := mediaInfoOf(t, "run.mp4", testSensorMP4())
	if doc.Location == nil || doc.Location.Lat != 46.5191 || doc.Location.Lon != 6.6323 || doc.Location.Country != "Switzerland" {
		t.Errorf("location = %+v", doc.Location)
	}
	if diff := cmp.Diff([]string{"Timed metadata: METT: text ×95; AMF0: onStart ×1, event ×10", "SEI: frame_info, settings (in the frames read)"}, group(doc, "Data")); diff != "" {
		t.Errorf("Data (-want +got):\n%s", diff)
	}

	// The readings: one structure, the first whole, the changing fields
	// with their range and a series.
	r := structureOf(doc, "moov/trak[2]/mett", "")
	if r == nil || r.Count != 95 || !r.Timed || r.Start != 0 || r.End != 9.4 {
		t.Fatalf("readings = %+v", r)
	}
	first := r.Value.(map[string]any)
	if jsonText(first["gps"].(map[string]any)["lat"]) != "46.5191" || first["device"].(map[string]any)["status"].(map[string]any)["led"] != "#2bd2ff" {
		t.Errorf("the first reading = %v", first)
	}
	temp := r.Fields["temperature_c"]
	if temp == nil || temp.Min == nil || *temp.Min != 18.5 || *temp.Max < 25 || len(temp.Series) != structureSeriesMax || temp.Series[0] != [2]float64{0, 18.5} {
		t.Errorf("temperature = %+v", temp)
	}
	if f := r.Fields["counter"]; f == nil || f.First != "0" || f.Last != "94" || f.Values != 95 {
		t.Errorf("counter = %+v", f)
	}
	if _, ok := r.Fields["device.id"]; ok {
		t.Error("a field that never changes is in the value, not the fields")
	}
	if f := r.Fields["device.status.ok"]; f == nil || f.Min != nil || f.Values != 2 {
		t.Errorf("a boolean = %+v", f)
	}
	if v := rawValue(doc, "moov/trak[2]/mett text", "temperature_c"); !strings.HasPrefix(v, "18.5 … ") || !strings.HasSuffix(v, " values)") {
		t.Errorf("raw temperature = %q", v)
	}
	if v := rawValue(doc, "moov/trak[2]/mett", "text"); v != "95 messages, 0:00.0 to 0:09.4" {
		t.Errorf("raw count = %q", v)
	}

	// AMF0: the JSON in its payload is a structure too.
	ev := structureOf(doc, "moov/trak[3]/amf0", "event")
	if ev == nil || ev.Count != 10 || ev.Value.(map[string]any)["payload"].(map[string]any)["color"] != "#ff7a59" {
		t.Errorf("events = %+v", ev)
	}
	if f := ev.Fields["payload.level"]; f == nil || f.Values != 2 {
		t.Errorf("event level = %+v", f)
	}

	// SEI: each type, the compressed settings unpacked.
	set := structureOf(doc, "SEI 5ea1c0de-0000-4b6d-8a2b-9c3d4e5f6a7b", "settings")
	if set == nil {
		t.Fatalf("no settings in %v", doc.Structures)
	}
	b, err := json.Marshal(set.Value)
	if err != nil || !strings.Contains(string(b), `"fov_deg":120`) {
		t.Errorf("settings = %s %v", b, err)
	}
	if structureOf(doc, "SEI 5ea1c0de-0000-4b6d-8a2b-9c3d4e5f6a7b", "frame_info") == nil {
		t.Error("no frame_info")
	}

	// XMP and mdta.
	x := structureOf(doc, "uuid/be7acfcb-97a9-42e8-9c71-999491e3afac (XMP)", "XMP")
	if x == nil || x.Value.(map[string]any)["dc:title"] != "Morning test run" || len(x.Value.(map[string]any)["dc:subject"].([]any)) != 2 {
		t.Errorf("XMP = %+v", x)
	}
	if diff := cmp.Diff([]string{"1: mdta: com.example.device.model", "2: mdta: com.example.firmware"}, rawOf(doc, "moov/meta/keys")); diff != "" {
		t.Errorf("keys (-want +got):\n%s", diff)
	}
	if v := rawValue(doc, "moov/meta/ilst", "com.example.device.model"); v != "Example Sensor 3" {
		t.Errorf("mdta value = %q", v)
	}
	// The whole document is JSON.
	if _, err := json.Marshal(doc); err != nil {
		t.Error(err)
	}
}

// TestMediaInfoRawBoxes lists what an MP4 holds that isn't read as a
// detail: unknown boxes, mdta keys, a UUID box, and XMP.
func TestMediaInfoRawBoxes(t *testing.T) {
	keys := isoBoxBytes("keys", u32(0), u32(1), isoBoxBytes("mdta", []byte("com.apple.quicktime.location.ISO6709")))
	ilst := isoBoxBytes("ilst", isoBoxBytes("\x00\x00\x00\x01", isoBoxBytes("data", u32(1), u32(0), []byte("+47.6000-122.3000/"))))
	meta := isoBoxBytes("meta", isoBoxBytes("hdlr", u32(0), u32(0), []byte("mdta"), make([]byte, 13)), keys, ilst)
	other, err := hexBytes("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	mp4 := remux(testMP4(64), remuxOptions{edit: withMeta(append(meta, isoBoxBytes("zzzz", []byte("hello box"))...))})
	mp4 = append(append(mp4, sensorXMP()...), isoBoxBytes("uuid", other, []byte{1, 2, 3})...)
	doc := mediaInfoOf(t, "clip.mp4", mp4)
	if diff := cmp.Diff([]string{"handler: mdta"}, rawOf(doc, "moov/meta/hdlr")); diff != "" {
		t.Errorf("hdlr (-want +got):\n%s", diff)
	}
	if doc.Location == nil || doc.Location.Lat != 47.6 {
		t.Errorf("location = %+v", doc.Location)
	}
	if diff := cmp.Diff([]string{"zzzz: hello box"}, rawOf(doc, "moov")); diff != "" {
		t.Errorf("an unknown box (-want +got):\n%s", diff)
	}
	if v := rawValue(doc, "uuid/be7acfcb-97a9-42e8-9c71-999491e3afac (XMP)", "dc:title"); v != "Morning test run" {
		t.Errorf("XMP = %q", v)
	}
	if diff := cmp.Diff([]string{"01234567-89ab-cdef-0123-456789abcdef: 3 bytes: 010203"}, rawOf(doc, "uuid")); diff != "" {
		t.Errorf("a UUID box (-want +got):\n%s", diff)
	}
	if !strings.Contains(strings.Join(group(doc, "Description"), "\n"), "Title: Morning test run") {
		t.Errorf("Description = %v", group(doc, "Description"))
	}
	if v := rawValue(doc, "moov/trak[1]/mdia/minf/stbl/stsd/avc1", "size"); v != "1920 × 1080" {
		t.Errorf("sample entry = %q", v)
	}
}

func TestFindLocation(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want *mediaLocation
	}{
		{map[string]any{"pos": map[string]any{"Latitude": json.Number("10.5"), "longitude": -20.25}}, &mediaLocation{Lat: 10.5, Lon: -20.25}},
		{map[string]any{"where": []any{map[string]any{"lat": "1", "lng": "2"}}}, &mediaLocation{Lat: 1, Lon: 2}},
		{map[string]any{"location": "+35.6586+139.7454/"}, &mediaLocation{Lat: 35.6586, Lon: 139.7454}},
		{map[string]any{"lat": 91.0, "lon": 0.0}, nil},
		{map[string]any{"lat": 0.0, "lon": 0.0}, nil},
		{map[string]any{"name": "+35.6586+139.7454/"}, nil},
	} {
		if diff := cmp.Diff(tc.want, findLocation(tc.v, 0)); diff != "" {
			t.Errorf("findLocation(%v) (-want +got):\n%s", tc.v, diff)
		}
	}
}

// TestMediaInfoTimedWithoutSeeking reads from files that can't seek (an
// archive's): what's after the moov is read on the way; frames before
// it can't be gone back to, and the rest is still there.
func TestMediaInfoTimedWithoutSeeking(t *testing.T) {
	video := seiVideoTrack(30, 5, sensorStart)
	ftyp := isoBoxBytes("ftyp", []byte("isom"), u32(0), []byte("isom"))
	mdat := isoBoxBytes("mdat", bytes.Join(video.samples, nil))
	mvhd := isoBoxBytes("mvhd", u32(0), u32(0), u32(0), u32(1000), u32(1000), make([]byte, 80))
	framesFirst := bytes.Join([][]byte{ftyp, mdat, isoBoxBytes("moov", mvhd, video.trakBytes(1, 1000, uint64(len(ftyp)+8)))}, nil)
	files := fstest.MapFS{"a.mp4": {Data: testSensorMP4()}, "b.mp4": {Data: framesFirst}}
	for _, seek := range []bool{true, false} {
		var fsys fs.FS = files
		if !seek {
			fsys = noSeekFS{files}
		}
		h := newMediaInfoHandler(http.NotFoundHandler(), fsys, newVideoMetaHandler(http.NotFoundHandler(), fsys, "mem", nil, nil))
		build := func(name string) *mediaInfo {
			info, err := fs.Stat(files, name)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := h.build(t.Context(), name, "video", info)
			if doc == nil {
				t.Fatalf("%s: %v", name, err)
			}
			return doc
		}
		if doc := build("a.mp4"); doc.Duration != 9.5 || structureOf(doc, "moov/trak[2]/mett", "") == nil {
			t.Errorf("seek=%t: the readings after the moov: %v", seek, doc.Structures)
		}
		doc := build("b.mp4")
		if got := len(doc.Structures) > 0; got != seek {
			t.Errorf("seek=%t: the frames before the moov read = %t", seek, got)
		}
	}
}

// TestSensorFixture checks the browsers' fixture is what sensorVideo
// makes of night-walk.mp4, and has what they look for.
func TestSensorFixture(t *testing.T) {
	want := sensorVideo(gowstesting.MustMediaFile(t, "night-walk.mp4"))
	got := gowstesting.MustMediaFile(t, sensorFixtureName)
	if !bytes.Equal(got, want) {
		t.Errorf("%s isn't what sensorVideo makes: remake it with GOWS_WRITE_MEDIA_FIXTURES=1 go test -run TestWriteSensorFixture ./pkg/gowebserver/", sensorFixtureName)
	}
	doc := mediaInfoOf(t, sensorFixtureName, got)
	if doc.Location == nil || len(doc.Text) != 0 || len(doc.Structures) < 5 {
		t.Errorf("fixture = %+v", doc)
	}
	if s := structureOf(doc, "SEI 5ea1c0de-0000-4b6d-8a2b-9c3d4e5f6a7b", "settings"); s == nil {
		t.Errorf("no settings: %v", doc.Structures)
	}
}

// TestWriteSensorFixture remakes the browsers' fixture when
// GOWS_WRITE_MEDIA_FIXTURES is set.
func TestWriteSensorFixture(t *testing.T) {
	if os.Getenv("GOWS_WRITE_MEDIA_FIXTURES") == "" {
		t.Skip("set GOWS_WRITE_MEDIA_FIXTURES to remake the fixtures")
	}
	dir := filepath.Join("..", "..", "internal", "gowebserver", "testing", "media")
	src, err := os.ReadFile(filepath.Join(dir, "night-walk.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sensorFixtureName), sensorVideo(src), 0o644); err != nil {
		t.Fatal(err)
	}
}
