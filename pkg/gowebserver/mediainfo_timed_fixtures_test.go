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
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"strconv"
	"time"
)

// Synthetic MP4s with metadata of every kind mediainfo_timed.go reads:
// timed metadata tracks (JSON and AMF0), SEI JSON, mdta keys and XMP.
// Every name, number and time in them is made up here.

// ---- AMF0 ----

func amfString(s string) []byte {
	if len(s) > 0xffff {
		return append(append([]byte{12}, u32(uint32(len(s)))...), s...)
	}
	return append(append([]byte{2}, u16(uint16(len(s)))...), s...)
}

func amfNumber(v float64) []byte {
	return binary.BigEndian.AppendUint64([]byte{0}, math.Float64bits(v))
}

// amfObjectBytes is an AMF0 object of the keys and (encoded) values kv.
func amfObjectBytes(kv ...any) []byte {
	out := []byte{3}
	for i := 0; i+1 < len(kv); i += 2 {
		k := kv[i].(string)
		out = append(append(append(out, u16(uint16(len(k)))...), k...), kv[i+1].([]byte)...)
	}
	return append(out, 0, 0, 9)
}

// amfMessage is an AMF0 timed metadata message as streaming servers
// record them: its name, then an object wrapping the JSON payload.
func amfMessage(name string, payload any) []byte {
	b, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return append(amfString(name), amfObjectBytes("encoding", amfString("json"), "payload", amfString(string(b)))...)
}

// ---- Adding a track to a movie ----

// fixtureTrack is a track to add: its handler, the handler's name, its
// sample entry (a whole box), its timescale, and its samples, each at a
// time in the timescale's units.
type fixtureTrack struct {
	handler, name string
	entries       [][]byte
	timescale     uint32
	samples       [][]byte
	times         []uint32
	// disabled leaves the track out of playback (tkhd flags 0).
	disabled bool
}

// isoBoxesOf splits b into its boxes, each whole (header and all).
func isoBoxesOf(b []byte) [][]byte {
	var out [][]byte
	for len(b) >= 8 {
		n := int(binary.BigEndian.Uint32(b))
		if n < 8 || n > len(b) {
			break
		}
		out = append(out, b[:n])
		b = b[n:]
	}
	return out
}

// rewriteBoxes rewrites the boxes in b, going into the containers a
// track's samples are found by: fn gets each box's path and body and
// returns its new body, or nil to drop it.
func rewriteBoxes(path string, b []byte, fn func(path, typ string, body []byte) []byte) []byte {
	var out []byte
	for _, x := range isoBoxesOf(b) {
		typ, body := string(x[4:8]), x[8:]
		here := path + "/" + typ
		switch typ {
		case "moov", "trak", "mdia", "minf", "stbl", "edts":
			body = rewriteBoxes(here, body, fn)
		}
		if body = fn(here, typ, body); body == nil {
			continue
		}
		out = append(out, isoBoxBytes(typ, body)...)
	}
	return out
}

// trakBytes is a track's box: its samples in chunks of one, at chunk
// offsets from offset.
func (t fixtureTrack) trakBytes(id uint32, movieTimescale uint32, offset uint64) []byte {
	var dur uint32
	stsz := make([]byte, 0, 4*len(t.samples))
	var stts, co64 []byte
	stts = append(stts, u32(0)...)
	stts = append(stts, u32(uint32(len(t.samples)))...)
	off := offset
	for i, s := range t.samples {
		delta := uint32(1)
		if i+1 < len(t.times) {
			delta = t.times[i+1] - t.times[i]
		} else if i > 0 {
			delta = t.times[i] - t.times[i-1]
		}
		dur = t.times[i] + delta
		stts = append(append(stts, u32(1)...), u32(delta)...)
		stsz = append(stsz, u32(uint32(len(s)))...)
		co64 = binary.BigEndian.AppendUint64(co64, off)
		off += uint64(len(s))
	}
	movieDur := uint32(uint64(dur) * uint64(movieTimescale) / uint64(t.timescale))
	flags := uint32(1)
	if t.disabled {
		flags = 0
	}
	tkhd := isoBoxBytes("tkhd", u32(flags), u32(0), u32(0), u32(id), u32(0), u32(movieDur), make([]byte, 8), u16(0), u16(0), u16(0), u16(0),
		u32(0x10000), u32(0), u32(0), u32(0), u32(0x10000), u32(0), u32(0), u32(0), u32(0x40000000), u32(0), u32(0))
	mdhd := isoBoxBytes("mdhd", u32(0), u32(0), u32(0), u32(t.timescale), u32(dur), u16(0x15c7), u16(0))
	hdlr := isoBoxBytes("hdlr", u32(0), u32(0), []byte(t.handler), make([]byte, 12), []byte(t.name+"\x00"))
	stsd := isoBoxBytes("stsd", u32(0), u32(uint32(len(t.entries))), bytes.Join(t.entries, nil))
	stbl := isoBoxBytes("stbl", stsd, isoBoxBytes("stts", stts),
		isoBoxBytes("stsc", u32(0), u32(1), u32(1), u32(1), u32(1)),
		isoBoxBytes("stsz", u32(0), u32(0), u32(uint32(len(t.samples))), stsz),
		isoBoxBytes("co64", u32(0), u32(uint32(len(t.samples))), co64))
	minf := isoBoxBytes("minf", isoBoxBytes("nmhd", u32(0)), isoBoxBytes("dinf", isoBoxBytes("dref", u32(0), u32(1), isoBoxBytes("url ", u32(1)))), stbl)
	return isoBoxBytes("trak", tkhd, isoBoxBytes("mdia", mdhd, hdlr, minf))
}

type remuxOptions struct {
	// ftyp replaces the file type's body.
	ftyp []byte
	// add are tracks to add.
	add []fixtureTrack
	// edit rewrites the movie's boxes (as rewriteBoxes), before the
	// tracks are added and the chunks moved.
	edit func(path, typ string, body []byte) []byte
}

// remux rebuilds an MP4: its file type, its movie as edit makes it,
// with tracks added, and its chunk offsets put right.
func remux(mp4 []byte, o remuxOptions) []byte {
	var ftyp, moov []byte
	var rest [][]byte // the boxes after the file type, the moov a nil
	moovFirst, seenMdat := false, false
	for _, x := range isoBoxesOf(mp4) {
		switch string(x[4:8]) {
		case "ftyp":
			ftyp = x
		case "moov":
			moov = x
			moovFirst = !seenMdat
			rest = append(rest, nil)
		default:
			if string(x[4:8]) == "mdat" {
				seenMdat = true
			}
			rest = append(rest, x)
		}
	}
	oldFtyp := len(ftyp)
	if o.ftyp != nil {
		ftyp = isoBoxBytes("ftyp", o.ftyp)
	}
	body := moov[8:]
	if o.edit != nil {
		body = rewriteBoxes("", isoBoxBytes("moov", body), o.edit)[8:]
	}
	var timescale uint32 = 1000
	if mv := isoFind(body, "mvhd"); len(mv) >= 16 {
		timescale = binary.BigEndian.Uint32(mv[12:])
	}
	nextID := uint32(len(isoBoxes(body)) + 1)
	// The size of what's added, to know where its media data goes.
	added := 0
	for i, t := range o.add {
		added += len(t.trakBytes(nextID+uint32(i), timescale, 0))
	}
	newMoovLen := 8 + len(body) + added
	// The media data moves with the file type's size, and the moov's
	// when it's ahead of it.
	shift := int64(len(ftyp) - oldFtyp)
	if moovFirst {
		shift += int64(newMoovLen - len(moov))
	}
	if shift != 0 {
		body = rewriteBoxes("", isoBoxBytes("moov", body), func(_, typ string, b []byte) []byte {
			switch typ {
			case "stco":
				b = bytes.Clone(b)
				for i, n := 0, int(binary.BigEndian.Uint32(b[4:])); i < n; i++ {
					binary.BigEndian.PutUint32(b[8+4*i:], uint32(int64(binary.BigEndian.Uint32(b[8+4*i:]))+shift))
				}
			case "co64":
				b = bytes.Clone(b)
				for i, n := 0, int(binary.BigEndian.Uint32(b[4:])); i < n; i++ {
					binary.BigEndian.PutUint64(b[8+8*i:], uint64(int64(binary.BigEndian.Uint64(b[8+8*i:]))+shift))
				}
			}
			return b
		})[8:]
	}
	size := len(ftyp) + newMoovLen
	for _, x := range rest {
		size += len(x)
	}
	var mdat []byte
	off := uint64(size + 8)
	traks := make([]byte, 0, added)
	for i, t := range o.add {
		traks = append(traks, t.trakBytes(nextID+uint32(i), timescale, off)...)
		for _, s := range t.samples {
			mdat = append(mdat, s...)
			off += uint64(len(s))
		}
	}
	out := append([]byte(nil), ftyp...)
	for _, x := range rest {
		if x == nil {
			out = append(out, isoBoxBytes("moov", body, traks)...)
		} else {
			out = append(out, x...)
		}
	}
	if len(o.add) > 0 {
		out = append(out, isoBoxBytes("mdat", mdat)...)
	}
	return out
}

// amfEntries are the sample entries of an AMF0 timed metadata track.
func amfEntries() [][]byte {
	return [][]byte{isoBoxBytes("amf0", make([]byte, 6), u16(1)), isoBoxBytes("amf3", make([]byte, 6), u16(1))}
}

// jsonEntry is the sample entry of a timed text metadata track of JSON
// (mett): no content encoding, then its MIME type.
func jsonEntry() []byte {
	return isoBoxBytes("mett", make([]byte, 6), u16(1), []byte("\x00application/json\x00"))
}

// ---- A sensor's readings ----

// sensorStart is when the made-up readings start.
var sensorStart = time.Date(2025, 3, 14, 9, 30, 0, 0, time.UTC)

// sensorReading is the made-up reading i of n: where it was, how warm,
// a counter, a battery, a device's status and an accelerometer.
func sensorReading(i, n int) map[string]any {
	f := float64(i) / float64(max(1, n-1))
	return map[string]any{
		"gps":           map[string]any{"lat": 46.5191 + f*0.002, "lon": 6.6323 + f*0.003, "alt_m": 372.5},
		"temperature_c": math.Round((18.5+7*f+math.Sin(f*9)*0.8)*10) / 10,
		"counter":       i,
		"battery_pct":   math.Round(98 - 11*f),
		"timestamp":     sensorStart.Add(time.Duration(i) * 100 * time.Millisecond).UnixMilli(),
		"device": map[string]any{
			"id": "sensor-01", "firmware": "2.4.1",
			"status": map[string]any{"ok": i%17 != 16, "led": "#2bd2ff", "uptime_s": 3600 + i/10},
		},
		"accel": []any{math.Round(math.Sin(f*6)*100) / 100, math.Round(math.Cos(f*6)*100) / 100, 0.98},
	}
}

// sensorTrack is a timed JSON metadata track: a reading every tenth of
// a second over length seconds.
func sensorTrack(length float64) fixtureTrack {
	t := fixtureTrack{handler: "meta", name: "SensorHandler", entries: [][]byte{jsonEntry()}, timescale: 1000}
	n := int(length * 10)
	for i := 0; i < n; i++ {
		t.samples = append(t.samples, mustJSON(sensorReading(i, n)))
		t.times = append(t.times, uint32(i*100))
	}
	return t
}

// eventTrack is an AMF0 timed metadata track: a start message, then an
// event wrapping JSON every second.
func eventTrack(length float64) fixtureTrack {
	t := fixtureTrack{handler: "meta", name: "EventHandler", entries: amfEntries(), timescale: 1000}
	t.samples = append(t.samples, append(amfString("onStart"), amfObjectBytes("version", amfNumber(2))...))
	t.times = append(t.times, 0)
	for i := 0; float64(i) < length; i++ {
		t.samples = append(t.samples, amfMessage("event", map[string]any{"seq": i, "level": []string{"info", "info", "warn"}[i%3], "color": "#ff7a59"}))
		t.times = append(t.times, uint32(i*1000+500))
	}
	return t
}

// mdtaMeta is a moov meta box of QuickTime keys (mdta), made up.
func mdtaMeta() []byte {
	keys := isoBoxBytes("keys", u32(0), u32(2),
		isoBoxBytes("mdta", []byte("com.example.device.model")), isoBoxBytes("mdta", []byte("com.example.firmware")))
	ilst := isoBoxBytes("ilst",
		isoBoxBytes("\x00\x00\x00\x01", isoBoxBytes("data", u32(1), u32(0), []byte("Example Sensor 3"))),
		isoBoxBytes("\x00\x00\x00\x02", isoBoxBytes("data", u32(1), u32(0), []byte("2.4.1"))))
	return isoBoxBytes("meta", isoBoxBytes("hdlr", u32(0), u32(0), []byte("mdta"), make([]byte, 13)), keys, ilst)
}

// sensorXMP is an XMP packet in Adobe's UUID box, made up.
func sensorXMP() []byte {
	id, err := hexBytes(xmpUUID)
	if err != nil {
		panic(err)
	}
	xmp := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		`<rdf:Description xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:xmp="http://ns.adobe.com/xap/1.0/" xmp:CreatorTool="Example Logger 1.0">` +
		`<dc:title><rdf:Alt><rdf:li>Morning test run</rdf:li></rdf:Alt></dc:title>` +
		`<dc:subject><rdf:Bag><rdf:li>test</rdf:li><rdf:li>sensor</rdf:li></rdf:Bag></dc:subject></rdf:Description></rdf:RDF></x:xmpmeta>`
	return isoBoxBytes("uuid", id, []byte(xmp))
}

// ---- SEI ----

const sensorSEIUUID = "5ea1c0de00004b6d8a2b9c3d4e5f6a7b"

// seiNAL is an H.264 SEI NAL unit (with its 4-byte size) of
// unregistered user data: uuid (hex), then payload.
func seiNAL(uuid string, payload []byte) []byte {
	id, err := hexBytes(uuid)
	if err != nil {
		panic(err)
	}
	body := append(append([]byte(nil), id...), payload...)
	msg := []byte{6, 5}
	n := len(body)
	for ; n >= 255; n -= 255 {
		msg = append(msg, 0xff)
	}
	msg = escapeRBSP(append(append(append(msg, byte(n)), body...), 0x80))
	return append(u32(uint32(len(msg))), msg...)
}

// escapeRBSP puts in the bytes that keep a start code out of a NAL unit:
// a 3 after two zeros, before a byte of 3 or less.
func escapeRBSP(b []byte) []byte {
	var out []byte
	zeros := 0
	for _, x := range b {
		if zeros >= 2 && x <= 3 {
			out = append(out, 3)
			zeros = 0
		}
		out = append(out, x)
		if x == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}

func hexBytes(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)
	for i := range out {
		v, err := strconv.ParseUint(s[2*i:2*i+2], 16, 8)
		if err != nil {
			return nil, err
		}
		out[i] = byte(v)
	}
	return out, nil
}

// compressedSettings is JSON as some writers send it: zlib, then base64.
func compressedSettings() string {
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	if _, err := w.Write([]byte(`{"settings":{"exposure":{"mode":"auto","ev":0.3},"fov_deg":120}}`)); err != nil {
		panic(err)
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

// seiVideoTrack is an H.264 track of n frames at 30 fps whose frames
// carry JSON in their SEI (made up): each its count from frame0 and when
// it was taken; those whose count is a multiple of 1000 the writer's
// compressed settings too. Every frame is the SEI and a stand-in slice.
func seiVideoTrack(n int, frame0 int64, start time.Time) fixtureTrack {
	avcC := isoBoxBytes("avcC", []byte{1, 0x4d, 0x40, 0x28, 0xff, 0xe0, 0})
	comp := make([]byte, 32)
	comp[0] = byte(copy(comp[1:], "Example Encoder"))
	avc1 := isoBoxBytes("avc1", make([]byte, 6), u16(1), make([]byte, 16), u16(320), u16(240), u32(0x480000), u32(0x480000), u32(0), u16(1), comp, u16(24), u16(0xffff), avcC)
	t := fixtureTrack{handler: "vide", name: "VideoHandler", entries: [][]byte{avc1}, timescale: 90000}
	for i := 0; i < n; i++ {
		f := frame0 + int64(i)
		taken := start.Add(time.Duration(i) * time.Second / 30)
		lines := [][]byte{mustJSON(map[string]any{"type": "frame_info", "frame": f, "captured_us": strconv.FormatInt(taken.UnixMicro(), 10), "gain_db": 3 + i%5})}
		if f%1000 == 0 {
			lines = append(lines, mustJSON(map[string]any{"type": "settings", "frame": f, "data": compressedSettings()}))
		}
		payload := append(bytes.Join(lines, []byte("\n")), '\n')
		slice := append(u32(4), 0x41, 0x9a, 0, 0)
		t.samples = append(t.samples, append(seiNAL(sensorSEIUUID, payload), slice...))
		t.times = append(t.times, uint32(i*3000))
	}
	return t
}

// withMeta drops a movie's user data, and puts meta in its moov.
func withMeta(meta []byte) func(path, typ string, body []byte) []byte {
	return func(_, typ string, body []byte) []byte {
		switch typ {
		case "udta":
			return nil
		case "moov":
			if meta != nil {
				return append(append([]byte(nil), body...), meta...)
			}
		}
		return body
	}
}

// testSensorMP4 is testMP4 with all of it: mdta keys, a timed JSON
// track of readings, an AMF0 track of events, a second video track
// whose frames carry SEI JSON, and XMP; without its user data.
func testSensorMP4() []byte {
	mp4 := remux(testMP4(4096), remuxOptions{
		edit: withMeta(mdtaMeta()),
		add:  []fixtureTrack{sensorTrack(9.5), eventTrack(9.5), seiVideoTrack(3000, 41_234, sensorStart)},
	})
	return append(mp4, sensorXMP()...)
}

// sensorFixtureName is the browsers' fixture.
const sensorFixtureName = "sensor-data.mp4"

// sensorVideo is a video (night-walk.mp4) with all of it, for the
// browser tests and screenshots: without its user data, with mdta keys,
// a timed JSON track of readings over its length, an AMF0 track of
// events, a disabled H.264 track whose frames carry SEI JSON, and XMP.
func sensorVideo(src []byte) []byte {
	var length float64
	probe := func(_, typ string, b []byte) []byte {
		if typ == "mvhd" {
			length = float64(binary.BigEndian.Uint32(b[16:])) / float64(binary.BigEndian.Uint32(b[12:]))
		}
		return b
	}
	remux(src, remuxOptions{edit: probe})
	sei := seiVideoTrack(int(length*30), 999_940, sensorStart)
	sei.disabled = true
	mp4 := remux(src, remuxOptions{edit: withMeta(mdtaMeta()), add: []fixtureTrack{sensorTrack(length), eventTrack(length), sei}})
	return append(mp4, sensorXMP()...)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
