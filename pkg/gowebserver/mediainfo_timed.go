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
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // the size of a picture a timed metadata track carries
	_ "image/png"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// An MP4's metadata beyond its tags, for mediainfo.go:
//
//   - every box, listed where it is for the panel's "Raw metadata"
//     (rawBoxes): the movie's and tracks' headers, sample entries, user
//     data, mdta keys, XMP, boxes named by a UUID and any box not known
//     here, as text when it reads as text and as hex when it doesn't;
//   - timed metadata tracks (a track whose handler isn't video or sound,
//     with AMF0 messages, as streaming servers record them, or text):
//     every message read (readTimedTrack), up to timedSamplesMax and
//     timedBytesMax;
//   - the SEI user data an H.264 or HEVC encoder (or whatever made the
//     video) puts in its frames: an encoder's settings (x264's), JSON:
//     read from a few frames spread over the video (seiFramesRead), not
//     all of them.
//
// Nothing here is read as meaning anything in particular: it's listed as
// it is (Raw), and kept whole for the viewer (Structures).

const (
	// timedSamplesMax and timedBytesMax bound what's read of a timed
	// metadata track, and timedSampleMax the largest message read.
	timedSamplesMax = 20000
	timedBytesMax   = 12 << 20
	timedSampleMax  = 256 << 10
	// seiFramesRead is how many frames' SEI is read, spread over the
	// video, and seiFrameHead how much of each frame (its SEI comes first).
	seiFramesRead = 48
	seiFrameHead  = 16 << 10
	// rawFlattenMax is the most keys a message is listed as.
	rawFlattenMax = 400
)

// timedMessage is a sample of a timed metadata track: an AMF0 message
// (its name, then its value: the arguments, one or a list), or text.
type timedMessage struct {
	track int // in mediaCollector.tracks
	time  float64
	name  string
	value any
}

// seiMessage is an SEI message of unregistered user data: a UUID, then
// the payload, and the JSON objects in it (one a line).
type seiMessage struct {
	time    float64
	uuid    string
	payload []byte
	objs    []map[string]any
}

// ---- Boxes, as they are ----

// boxName is a box's type as text: © for the byte QuickTime starts its
// tags with, and hex for one that isn't text.
func boxName(typ string) string {
	typ = strings.ReplaceAll(typ, "\xa9", "©")
	if !utf8.ValidString(typ) {
		return "0x" + hex.EncodeToString([]byte(typ))
	}
	for _, r := range typ {
		if r < 0x20 || r == 0x7f {
			return "0x" + hex.EncodeToString([]byte(typ))
		}
	}
	return typ
}

func fmtBytesRaw(n int64) string {
	return strconv.FormatInt(n, 10) + " bytes"
}

// macTime is an MP4 time (seconds since 1904) as RFC 3339, or "0".
func macTime(v uint64) string {
	if v == 0 || v > 1<<40 {
		return strconv.FormatUint(v, 10)
	}
	return macEpoch.Add(time.Duration(v) * time.Second).Format(time.RFC3339)
}

// hdlrName is a handler's name: a C string, or a Pascal one in
// QuickTime's.
func hdlrName(b []byte) string {
	if len(b) > 0 && int(b[0]) == len(b)-1 && b[0] > 0 {
		b = b[1:]
	}
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(validUTF8(b))
}

// rawPreview is a box's content as text when it is text, else its size
// and its first bytes in hex.
func rawPreview(b []byte) string {
	if t := strings.TrimRight(string(b), "\x00"); t != "" && utf8.ValidString(t) && isPrintable(t) {
		return t
	}
	n := min(len(b), 48)
	s := fmt.Sprintf("%d bytes", len(b))
	if n > 0 {
		s += ": " + hex.EncodeToString(b[:n])
		if n < len(b) {
			s += "…"
		}
	}
	return s
}

// rawContainers are the boxes that hold only boxes.
var rawContainers = map[string]bool{
	"trak": true, "mdia": true, "minf": true, "stbl": true, "edts": true, "dinf": true, "mvex": true,
	"tref": true, "gmhd": true, "moof": true, "traf": true, "sinf": true, "schi": true,
}

// rawTables are a track's sample tables: listed by their length.
var rawTables = map[string]bool{
	"stts": true, "stsc": true, "stsz": true, "stz2": true, "stco": true, "co64": true, "stss": true,
	"ctts": true, "sdtp": true, "sgpd": true, "sbgp": true, "stps": true, "cslg": true, "subs": true, "saiz": true, "saio": true,
}

// rawBoxes lists the boxes in b, which is in the box path.
func (c *mediaCollector) rawBoxes(path string, b []byte) {
	for _, x := range isoBoxes(b) {
		c.rawBox(path, x)
	}
}

// rawBox lists a box in path, and what's in it.
func (c *mediaCollector) rawBox(path string, x isoBox) {
	d, here := x.data, path+"/"+boxName(x.typ)
	switch {
	case rawContainers[x.typ]:
		c.rawBoxes(here, d)
	case rawTables[x.typ]:
		n := 0
		if len(d) >= 8 {
			n = int(binary.BigEndian.Uint32(d[4:]))
			if x.typ == "stsz" && len(d) >= 12 {
				n = int(binary.BigEndian.Uint32(d[8:]))
			}
		}
		c.raw(here, "entries", strconv.Itoa(n))
	case x.typ == "udta":
		parseUdta(c, d, here)
	case x.typ == "meta":
		parseIlstMeta(c, d, here)
	case x.typ == "uuid":
		c.uuidBox(here, d)
	case x.typ == "XMP_":
		c.xmpBox(here, d)
	case x.typ == "tkhd" && len(d) >= 84:
		v1 := d[0] == 1
		id, dur := binary.BigEndian.Uint32(d[12:]), uint64(binary.BigEndian.Uint32(d[20:]))
		created := uint64(binary.BigEndian.Uint32(d[4:]))
		if v1 && len(d) >= 96 {
			created, id, dur = binary.BigEndian.Uint64(d[4:]), binary.BigEndian.Uint32(d[20:]), binary.BigEndian.Uint64(d[28:])
		}
		c.raw(here, "track ID", strconv.FormatUint(uint64(id), 10))
		c.raw(here, "creation time", macTime(created))
		c.raw(here, "duration", strconv.FormatUint(dur, 10))
		c.raw(here, "flags", fmt.Sprintf("0x%06x", binary.BigEndian.Uint32(d)&0xffffff))
		w, h := binary.BigEndian.Uint32(d[len(d)-8:]), binary.BigEndian.Uint32(d[len(d)-4:])
		if w > 0 || h > 0 {
			c.raw(here, "size", formatFloat(float64(w)/65536, 2)+" × "+formatFloat(float64(h)/65536, 2))
		}
	case x.typ == "mdhd" && len(d) >= 24:
		ts, dur, lang := uint64(binary.BigEndian.Uint32(d[12:])), uint64(binary.BigEndian.Uint32(d[16:])), binary.BigEndian.Uint16(d[20:])
		if d[0] == 1 && len(d) >= 36 {
			ts, dur, lang = uint64(binary.BigEndian.Uint32(d[20:])), binary.BigEndian.Uint64(d[24:]), binary.BigEndian.Uint16(d[32:])
		}
		c.raw(here, "timescale", strconv.FormatUint(ts, 10))
		c.raw(here, "duration", strconv.FormatUint(dur, 10))
		if lang != 0 && lang != 0x7fff {
			c.raw(here, "language", string([]byte{byte(lang>>10&31) + 0x60, byte(lang>>5&31) + 0x60, byte(lang&31) + 0x60}))
		}
	case x.typ == "hdlr" && len(d) >= 12:
		c.raw(here, "handler", strings.TrimRight(string(d[8:12]), "\x00"))
		if len(d) > 24 {
			c.raw(here, "name", hdlrName(d[24:]))
		}
	case x.typ == "stsd" && len(d) >= 8:
		for i, e := range isoBoxes(d[8:]) {
			c.rawSampleEntry(here+"/"+boxName(e.typ)+entryIndex(i), e)
		}
	case x.typ == "elst" && len(d) >= 8:
		n := int(binary.BigEndian.Uint32(d[4:]))
		var parts []string
		for i, q := 0, d[8:]; i < n && i < 16; i++ {
			if d[0] == 1 && len(q) >= 20 {
				parts = append(parts, fmt.Sprintf("%d@%d", binary.BigEndian.Uint64(q), int64(binary.BigEndian.Uint64(q[8:]))))
				q = q[20:]
			} else if len(q) >= 12 {
				parts = append(parts, fmt.Sprintf("%d@%d", binary.BigEndian.Uint32(q), int32(binary.BigEndian.Uint32(q[4:]))))
				q = q[12:]
			}
		}
		c.raw(here, "edits (duration@media time)", strings.Join(parts, ", "))
	case x.typ == "dref" || x.typ == "vmhd" || x.typ == "smhd" || x.typ == "nmhd" || x.typ == "sthd" || x.typ == "free" || x.typ == "skip":
		// Nothing a viewer would want.
	default:
		c.raw(path, boxName(x.typ), rawPreview(d))
	}
}

func entryIndex(i int) string {
	if i == 0 {
		return ""
	}
	return "[" + strconv.Itoa(i+1) + "]"
}

// rawSampleEntry lists a sample entry: its codec's own boxes, and what a
// video's says of its encoder and size.
func (c *mediaCollector) rawSampleEntry(path string, e isoBox) {
	d := e.data
	switch {
	case len(d) >= 78 && isVisualEntry(e.typ):
		c.raw(path, "size", strconv.Itoa(int(binary.BigEndian.Uint16(d[24:])))+" × "+strconv.Itoa(int(binary.BigEndian.Uint16(d[26:]))))
		if n := int(d[42]); n > 0 && n <= 31 {
			c.raw(path, "compressor", strings.TrimRight(validUTF8(d[43:43+n]), "\x00"))
		}
		for _, x := range isoBoxes(d[78:]) {
			c.raw(path, boxName(x.typ), fmt.Sprintf("%d bytes", len(x.data)))
		}
	case len(d) >= 28 && isAudioEntry(e.typ):
		c.raw(path, "channels", strconv.Itoa(int(binary.BigEndian.Uint16(d[16:]))))
		c.raw(path, "sample rate", strconv.Itoa(int(binary.BigEndian.Uint32(d[24:])>>16)))
		for _, x := range isoBoxes(d[28:]) {
			c.raw(path, boxName(x.typ), fmt.Sprintf("%d bytes", len(x.data)))
		}
	case len(d) >= 8:
		// Any other entry: its data reference, then its own fields, which
		// for a metadata entry (amf0, mett, metx) are names and MIME types.
		c.raw(path, "data", rawPreview(d[8:]))
	}
}

func isVisualEntry(t string) bool {
	switch t {
	case "avc1", "avc3", "hvc1", "hev1", "dvh1", "dvhe", "av01", "vp09", "vp08", "mp4v", "jpeg", "mjpa",
		"apch", "apcn", "apcs", "apco", "ap4h", "ap4x", "encv":
		return true
	}
	return false
}

func isAudioEntry(t string) bool {
	switch t {
	case "mp4a", "ac-3", "ec-3", "alac", "Opus", "fLaC", ".mp3", "lpcm", "sowt", "twos", "ipcm", "fpcm", "enca":
		return true
	}
	return false
}

// xmpUUID names the box Adobe keeps XMP in, in an MP4.
const xmpUUID = "be7acfcb97a942e89c71999491e3afac"

// uuidBox lists a box named by a UUID: XMP, or what it holds.
func (c *mediaCollector) uuidBox(path string, d []byte) {
	if len(d) < 16 {
		return
	}
	id := hex.EncodeToString(d[:16])
	if id == xmpUUID {
		c.xmpBox(path+"/"+fmtUUID(id)+" (XMP)", d[16:])
		return
	}
	c.raw(path, fmtUUID(id), rawPreview(d[16:]))
}

// xmpBox keeps an XMP packet from a box, for applyEmbedded.
func (c *mediaCollector) xmpBox(path string, d []byte) {
	if c.xmp == nil && len(d) > 0 {
		c.xmp, c.xmpPath = d, path
	}
}

func fmtUUID(h string) string {
	if len(h) != 32 {
		return h
	}
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// ---- Where a track's samples are ----

type sampleTable struct {
	off  []int64
	size []uint32
	time []float64 // decoding time, in seconds
}

// readSampleTable reads where a track's samples are and when, up to most
// of them.
func readSampleTable(stbl []byte, timescale uint64, most int) *sampleTable {
	st := &sampleTable{}
	if timescale == 0 {
		return st
	}
	stsz := isoFind(stbl, "stsz")
	if len(stsz) < 12 {
		return st
	}
	fixed, n := binary.BigEndian.Uint32(stsz[4:]), int(binary.BigEndian.Uint32(stsz[8:]))
	n = min(n, most)
	for i := 0; i < n; i++ {
		switch {
		case fixed != 0:
			st.size = append(st.size, fixed)
		case 12+4*i+4 <= len(stsz):
			st.size = append(st.size, binary.BigEndian.Uint32(stsz[12+4*i:]))
		default:
			i = n
		}
	}
	var chunks []int64
	if co := isoFind(stbl, "co64"); len(co) >= 8 {
		for i, k := 0, int(binary.BigEndian.Uint32(co[4:])); i < k && 8+8*i+8 <= len(co); i++ {
			chunks = append(chunks, int64(binary.BigEndian.Uint64(co[8+8*i:])))
		}
	} else if co := isoFind(stbl, "stco"); len(co) >= 8 {
		for i, k := 0, int(binary.BigEndian.Uint32(co[4:])); i < k && 8+4*i+4 <= len(co); i++ {
			chunks = append(chunks, int64(binary.BigEndian.Uint32(co[8+4*i:])))
		}
	}
	type run struct{ first, per uint32 }
	var runs []run
	if sc := isoFind(stbl, "stsc"); len(sc) >= 8 {
		for i, k := 0, int(binary.BigEndian.Uint32(sc[4:])); i < k && 8+12*i+12 <= len(sc); i++ {
			runs = append(runs, run{binary.BigEndian.Uint32(sc[8+12*i:]), binary.BigEndian.Uint32(sc[12+12*i:])})
		}
	}
	if len(runs) == 0 {
		return &sampleTable{}
	}
	si, ri := 0, 0
	for ci, off := range chunks {
		for ri+1 < len(runs) && uint32(ci+1) >= runs[ri+1].first {
			ri++
		}
		for k := uint32(0); k < runs[ri].per && si < len(st.size); k++ {
			st.off = append(st.off, off)
			off += int64(st.size[si])
			si++
		}
		if si >= len(st.size) {
			break
		}
	}
	st.size = st.size[:len(st.off)]
	if tt := isoFind(stbl, "stts"); len(tt) >= 8 {
		var t uint64
		for i, k := 0, int(binary.BigEndian.Uint32(tt[4:])); i < k && 8+8*i+8 <= len(tt) && len(st.time) < len(st.off); i++ {
			count, delta := binary.BigEndian.Uint32(tt[8+8*i:]), binary.BigEndian.Uint32(tt[12+8*i:])
			for j := uint32(0); j < count && len(st.time) < len(st.off); j++ {
				st.time = append(st.time, float64(t)/float64(timescale))
				t += uint64(delta)
			}
		}
	}
	for len(st.time) < len(st.off) {
		st.time = append(st.time, 0)
	}
	return st
}

// ---- Reading what the tracks carry over time ----

// timedEntries are the sample entries of timed metadata read here.
var timedEntries = map[string]bool{"amf0": true, "mett": true, "metx": true, "tx3g": true, "text": true, "stpp": true, "wvtt": true}

// readTimed reads the timed metadata tracks and the video's SEI. It
// stops quietly where the file can't be read back to (an archive's
// member, whose moov is after its media data).
func (c *mediaCollector) readTimed(s *mediaSource) error {
	budget := int64(timedBytesMax)
	for i, t := range c.tracks {
		switch {
		case t.handler == "vide" && t.nalLen > 0:
			c.readSEI(s, t)
		case t.handler != "vide" && t.handler != "soun" && timedEntries[t.entry]:
			c.readTimedTrack(s, i, t, &budget)
		}
	}
	return nil
}

func (c *mediaCollector) readTimedTrack(s *mediaSource, idx int, t *mp4Track, budget *int64) {
	st := readSampleTable(t.stbl, t.timescale, timedSamplesMax)
	for i := range st.off {
		n := int64(st.size[i])
		if n == 0 || n > timedSampleMax || n > *budget {
			continue
		}
		if err := s.seek(st.off[i]); err != nil {
			return
		}
		b, err := s.bytes(n)
		if err != nil {
			return
		}
		*budget -= n
		m := timedMessage{track: idx, time: st.time[i]}
		if t.entry == "amf0" {
			vals := amf0Values(b)
			if len(vals) == 0 {
				continue
			}
			if name, ok := vals[0].(string); ok {
				m.name, vals = name, vals[1:]
			}
			switch len(vals) {
			case 0:
			case 1:
				m.value = vals[0]
			default:
				m.value = vals
			}
		} else {
			if len(b) >= 2 && (t.entry == "tx3g" || t.entry == "text") {
				// A length, then the text.
				if k := int(binary.BigEndian.Uint16(b)); 2+k <= len(b) {
					b = b[2 : 2+k]
				}
			}
			m.value = strings.TrimRight(validUTF8(b), "\x00")
		}
		c.timed = append(c.timed, m)
	}
}

// readSEI reads the unregistered user data (SEI payload type 5) of a
// few frames spread over an H.264 or HEVC video, stopping after the
// first few if they have none.
func (c *mediaCollector) readSEI(s *mediaSource, t *mp4Track) {
	st := readSampleTable(t.stbl, t.timescale, math.MaxInt32)
	if len(st.off) == 0 {
		return
	}
	picks := []int{0}
	for k := 1; k < seiFramesRead; k++ {
		picks = append(picks, k*(len(st.off)-1)/(seiFramesRead-1))
	}
	hevc := t.entry == "hvc1" || t.entry == "hev1" || t.entry == "dvh1" || t.entry == "dvhe"
	read := map[int]bool{}
	frame0 := int64(-1)
	for n, i := range picks {
		if read[i] {
			continue
		}
		if n >= 3 && len(read) > 0 && len(c.sei) == 0 {
			return
		}
		read[i] = true
		ms, ok := c.readFrameSEI(s, st, i, t.nalLen, hevc)
		if !ok {
			return
		}
		if i == 0 {
			frame0 = frameCount(ms)
		}
	}
	// A writer that counts its frames in the JSON may write some of it
	// only with some frames, those whose count is a round number: a few
	// of those are read too, found by the count (which skips the frames
	// the recording dropped).
	if frame0 >= 0 {
		var drift int64
		for k := int64(0); k < 8; k++ {
			// The count goes up by about one a frame (more past a dropped
			// frame, less past a repeated one; drift is how far it's got
			// from the frame's index): the frame is near where the count
			// says, and is found by halving around there.
			want := (frame0/1000 + 1 + k) * 1000
			at := int(want - frame0 - drift)
			lo, hi := max(1, at-150), min(at+150, len(st.off)-1)
			for try := 0; try < 10 && lo <= hi; try++ {
				i := (lo + hi + 1) / 2
				if try == 0 {
					i = max(lo, min(at, hi))
				}
				if read[i] {
					break
				}
				read[i] = true
				ms, ok := c.readFrameSEI(s, st, i, t.nalLen, hevc)
				if !ok {
					return
				}
				got := frameCount(ms)
				if got < 0 {
					break
				}
				drift = got - frame0 - int64(i)
				if got == want {
					break
				}
				if got > want {
					hi = i - 1
				} else {
					lo = i + 1
				}
			}
		}
		sort.SliceStable(c.sei, func(a, b int) bool { return c.sei[a].time < c.sei[b].time })
	}
}

// frameCount is the frame count ("frame") the JSON in a
// frame's SEI, or -1.
func frameCount(ms []seiMessage) int64 {
	for _, m := range ms {
		for _, o := range m.objs {
			if f, err := strconv.ParseInt(scalarText(o["frame"]), 10, 64); err == nil && f >= 0 {
				return f
			}
		}
	}
	return -1
}

// readFrameSEI reads the SEI user data of frame i.
func (c *mediaCollector) readFrameSEI(s *mediaSource, st *sampleTable, i, nalLen int, hevc bool) ([]seiMessage, bool) {
	if err := s.seek(st.off[i]); err != nil {
		return nil, false
	}
	b, err := s.bytes(min(int64(st.size[i]), seiFrameHead))
	if err != nil {
		return nil, false
	}
	ms := seiUserData(b, nalLen, hevc)
	for k := range ms {
		ms[k].time = st.time[i]
	}
	c.sei = append(c.sei, ms...)
	return ms, true
}

// seiUserData reads the unregistered user data in a frame's SEI NAL
// units, each a NAL unit's size (nalLen bytes) then the unit.
func seiUserData(b []byte, nalLen int, hevc bool) []seiMessage {
	var out []seiMessage
	for len(b) > nalLen {
		var n int
		for i := 0; i < nalLen; i++ {
			n = n<<8 | int(b[i])
		}
		b = b[nalLen:]
		if n <= 0 || n > len(b) {
			break
		}
		nal := b[:n]
		b = b[n:]
		var p []byte
		switch {
		case !hevc && nal[0]&0x1f == 6:
			p = nal[1:]
		case hevc && len(nal) > 2 && (nal[0]>>1&0x3f == 39 || nal[0]>>1&0x3f == 40):
			p = nal[2:]
		default:
			continue
		}
		p = unescapeRBSP(p)
		for len(p) > 2 && p[0] != 0x80 {
			typ, size := 0, 0
			for len(p) > 0 && p[0] == 0xff {
				typ += 255
				p = p[1:]
			}
			if len(p) == 0 {
				break
			}
			typ += int(p[0])
			p = p[1:]
			for len(p) > 0 && p[0] == 0xff {
				size += 255
				p = p[1:]
			}
			if len(p) == 0 {
				break
			}
			size += int(p[0])
			p = p[1:]
			if size > len(p) {
				size = len(p)
			}
			if typ == 5 && size >= 16 {
				m := seiMessage{uuid: hex.EncodeToString(p[:16]), payload: bytes.Clone(p[16:size])}
				m.objs = jsonLines(m.payload)
				out = append(out, m)
			}
			p = p[size:]
		}
	}
	return out
}

// unescapeRBSP takes out the bytes a NAL unit puts in to keep a start
// code out of it (00 00 03).
func unescapeRBSP(b []byte) []byte {
	if !bytes.Contains(b, []byte{0, 0, 3}) {
		return b
	}
	out := make([]byte, 0, len(b))
	zeros := 0
	for _, x := range b {
		if zeros >= 2 && x == 3 {
			zeros = 0
			continue
		}
		if x == 0 {
			zeros++
		} else {
			zeros = 0
		}
		out = append(out, x)
	}
	return out
}

// jsonLines reads the JSON objects in b, one after another (a line
// each, or apart by NULs), or none when b isn't that.
func jsonLines(b []byte) []map[string]any {
	var out []map[string]any
	for len(out) < 64 {
		b = bytes.TrimLeft(b, " \t\r\n\x00")
		if len(b) == 0 || b[0] != '{' {
			return out
		}
		var m map[string]any
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		if d.Decode(&m) != nil {
			return out
		}
		out = append(out, m)
		b = b[d.InputOffset():]
	}
	return out
}

// ---- AMF0 ----

// amfObject is an AMF0 object or ECMA array: its properties, in order.
type amfObject []amfProp

type amfProp struct {
	key   string
	value any
}

// amf0Values reads the AMF0 values in b, one after another, as Go's
// own: float64, bool, string, amfObject, []any, time.Time or nil.
func amf0Values(b []byte) []any {
	var out []any
	for len(b) > 0 && len(out) < 64 {
		v, rest, ok := amf0Value(b, 0)
		if !ok {
			break
		}
		out = append(out, v)
		b = rest
	}
	return out
}

func amf0Value(b []byte, depth int) (any, []byte, bool) {
	if len(b) == 0 || depth > 16 {
		return nil, nil, false
	}
	typ, b := b[0], b[1:]
	switch typ {
	case 0: // number
		if len(b) < 8 {
			return nil, nil, false
		}
		return math.Float64frombits(binary.BigEndian.Uint64(b)), b[8:], true
	case 1: // boolean
		if len(b) < 1 {
			return nil, nil, false
		}
		return b[0] != 0, b[1:], true
	case 2, 12: // string, long string
		h := 2
		if typ == 12 {
			h = 4
		}
		if len(b) < h {
			return nil, nil, false
		}
		n := int(binary.BigEndian.Uint16(b))
		if typ == 12 {
			n = int(binary.BigEndian.Uint32(b))
		}
		if n < 0 || h+n > len(b) {
			return nil, nil, false
		}
		return validUTF8(b[h : h+n]), b[h+n:], true
	case 3, 8: // object, ECMA array
		if typ == 8 {
			if len(b) < 4 {
				return nil, nil, false
			}
			b = b[4:]
		}
		var o amfObject
		for len(b) >= 3 {
			n := int(binary.BigEndian.Uint16(b))
			if n == 0 && b[2] == 9 {
				return o, b[3:], true
			}
			if 2+n > len(b) {
				return nil, nil, false
			}
			k := validUTF8(b[2 : 2+n])
			v, rest, ok := amf0Value(b[2+n:], depth+1)
			if !ok {
				return nil, nil, false
			}
			o = append(o, amfProp{k, v})
			b = rest
		}
		return o, nil, true
	case 5, 6: // null, undefined
		return nil, b, true
	case 10: // strict array
		if len(b) < 4 {
			return nil, nil, false
		}
		n := int(binary.BigEndian.Uint32(b))
		b = b[4:]
		var list []any
		for i := 0; i < n; i++ {
			v, rest, ok := amf0Value(b, depth+1)
			if !ok {
				return nil, nil, false
			}
			list = append(list, v)
			b = rest
		}
		return list, b, true
	case 11: // date: milliseconds, then a time zone that's always 0
		if len(b) < 10 {
			return nil, nil, false
		}
		ms := math.Float64frombits(binary.BigEndian.Uint64(b))
		return time.UnixMilli(int64(ms)).UTC(), b[10:], true
	}
	return nil, nil, false
}

// ---- Listing what was read ----

// flatten lists v's keys and values as text, a key a line: an object's
// properties as key.sub, a list of plain values as JSON, text that is
// JSON (as streaming servers wrap theirs) as what it holds, and a
// picture carried as a data: URL as its type and size.
func flatten(prefix string, v any, emit func(k, v string), depth int) {
	join := func(k string) string {
		if prefix == "" {
			return k
		}
		return prefix + "." + k
	}
	switch x := v.(type) {
	case amfObject:
		for _, p := range x {
			flatten(join(p.key), p.value, emit, depth+1)
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			flatten(join(k), x[k], emit, depth+1)
		}
	case []any:
		plain := true
		for _, e := range x {
			switch e.(type) {
			case amfObject, map[string]any:
				plain = false
			}
		}
		if plain || depth > 8 {
			emit(prefix, compactJSON(x))
			return
		}
		for i, e := range x {
			if i >= 32 {
				emit(prefix+"[…]", strconv.Itoa(len(x)-i)+" more")
				break
			}
			flatten(prefix+"["+strconv.Itoa(i)+"]", e, emit, depth+1)
		}
	case string:
		if obj := structuredString(x); obj != nil && depth < 8 {
			flatten(prefix, obj, emit, depth+1)
			return
		}
		if x == "" {
			// Listed all the same: an empty field is still a field.
			x = `""`
		}
		emit(prefix, dataURLSummary(x))
	default:
		emit(prefix, scalarText(v))
	}
}

func scalarText(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case json.Number:
		return x.String()
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case string:
		return x
	}
	return fmt.Sprint(v)
}

func compactJSON(v any) string {
	var conv func(any) any
	conv = func(v any) any {
		switch x := v.(type) {
		case amfObject:
			m := map[string]any{}
			for _, p := range x {
				m[p.key] = conv(p.value)
			}
			return m
		case []any:
			out := make([]any, len(x))
			for i, e := range x {
				out[i] = conv(e)
			}
			return out
		case string:
			return dataURLSummary(x)
		}
		return v
	}
	b, err := json.Marshal(conv(v))
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// jsonObject is s's JSON object, when s is one.
func jsonObject(s string) map[string]any {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return nil
	}
	var m map[string]any
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	if d.Decode(&m) != nil {
		return nil
	}
	return m
}

// dataURLSummary is a data: URL's picture as its type and size, rather
// than the base64 it's written in; any other text as it is.
func dataURLSummary(s string) string {
	if !strings.HasPrefix(s, "data:") || len(s) < 64 {
		return s
	}
	head, body, ok := strings.Cut(s, ",")
	if !ok || !strings.HasSuffix(head, ";base64") {
		return s
	}
	mime := strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64")
	out := mime + " image"
	if raw, err := base64.StdEncoding.DecodeString(body); err == nil {
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil {
			out += fmt.Sprintf(", %d × %d", cfg.Width, cfg.Height)
		}
	}
	return out
}

// structuredString is the JSON object s holds: as text, or compressed
// (zlib, then base64); nil when it's neither.
func structuredString(s string) map[string]any {
	if m := jsonObject(s); m != nil {
		return m
	}
	return zipB64(s)
}

// zipB64 is the JSON object s carries compressed (zlib, then base64),
// or nil.
func zipB64(s string) map[string]any {
	s = strings.TrimSpace(s)
	// zlib's header is 0x78, which base64 writes as "e".
	if len(s) < 16 || s[0] != 'e' {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	r, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return nil
	}
	return jsonObject(string(b))
}

// ---- Structures ----

// mediaStructure is a structured value the file holds, whole: a timed
// metadata track's message (all of a name's messages once), an SEI
// message, an XMP packet. Value is the first as JSON (AMF0 objects as
// JSON objects, JSON wrapped in text unwrapped); Fields, when it was
// sent more than once, is what each of its fields that changed did,
// keyed as Raw's keys are ("a.b[0].c").
type mediaStructure struct {
	Source string                     `json:"source"`
	Name   string                     `json:"name,omitempty"`
	Count  int                        `json:"count"`
	Start  float64                    `json:"start"`
	End    float64                    `json:"end"`
	Timed  bool                       `json:"timed,omitempty"`
	Value  any                        `json:"value"`
	Fields map[string]*structureField `json:"fields,omitempty"`
}

// structureField is what a field of a repeated message held: how many
// different values (up to structureDistinctMax, past which it says
// structureDistinctMax+1), the first and last, and for a number its
// range and a series of [time, value] points thinned to
// structureSeriesMax.
type structureField struct {
	Values int          `json:"values"`
	First  string       `json:"first"`
	Last   string       `json:"last"`
	Min    *float64     `json:"min,omitempty"`
	Max    *float64     `json:"max,omitempty"`
	Series [][2]float64 `json:"series,omitempty"`
}

const (
	structuresMax         = 200
	structureDistinctMax  = 1000
	structureSeriesMax    = 64
	structurePointsMax    = 4000
	structureImageMax     = 96 << 10
	structureValueKeysMax = 2000
)

// plainValue is v as JSON's values: an AMF0 object as an object, text
// holding JSON (or zlib'd JSON in base64) as what it holds, a time as
// RFC 3339, a picture in a data: URL kept only when it's small.
func plainValue(v any, depth int, keys *int) any {
	if depth > 16 || *keys > structureValueKeysMax {
		return nil
	}
	switch x := v.(type) {
	case amfObject:
		m := make(map[string]any, len(x))
		for _, p := range x {
			*keys++
			m[p.key] = plainValue(p.value, depth+1, keys)
		}
		return m
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			*keys++
			m[k] = plainValue(e, depth+1, keys)
		}
		return m
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			*keys++
			out = append(out, plainValue(e, depth+1, keys))
		}
		return out
	case string:
		if obj := structuredString(x); obj != nil {
			return plainValue(obj, depth+1, keys)
		}
		if strings.HasPrefix(x, "data:") && len(x) > structureImageMax {
			return dataURLSummary(x)
		}
		return truncateRunes(x, mediaRawValueMax)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
		return x
	case time.Time:
		return x.Format(time.RFC3339Nano)
	}
	return v
}

// structureBuilder gathers the messages of one kind into a structure,
// and its raw keys.
type structureBuilder struct {
	s      mediaStructure
	keys   []string
	fields map[string]*fieldBuilder
}

type fieldBuilder struct {
	first, last string
	distinct    map[string]bool
	more        bool
	min, max    float64
	numbers     int
	points      [][2]float64
}

func newStructureBuilder(source, name string, timed bool) *structureBuilder {
	return &structureBuilder{s: mediaStructure{Source: source, Name: name, Timed: timed}, fields: map[string]*fieldBuilder{}}
}

func (b *structureBuilder) add(t float64, v any) {
	if b.s.Count == 0 {
		b.s.Start = t
		b.s.Value = plainValue(v, 0, new(int))
	}
	b.s.Count++
	b.s.End = t
	flatten("", v, func(k, val string) {
		f := b.fields[k]
		if f == nil {
			if len(b.keys) >= rawFlattenMax {
				return
			}
			f = &fieldBuilder{first: val, distinct: map[string]bool{}}
			b.fields[k] = f
			b.keys = append(b.keys, k)
		}
		f.last = val
		if !f.more {
			f.distinct[val] = true
			if len(f.distinct) > structureDistinctMax {
				f.more = true
			}
		}
		if n, err := strconv.ParseFloat(val, 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
			if f.numbers == 0 || n < f.min {
				f.min = n
			}
			if f.numbers == 0 || n > f.max {
				f.max = n
			}
			f.numbers++
			if len(f.points) < structurePointsMax {
				f.points = append(f.points, [2]float64{math.Round(t*1000) / 1000, n})
			}
		}
	}, 0)
}

// distinct is how many values a field had, as structureField says it.
func (f *fieldBuilder) values() int {
	if f.more {
		return structureDistinctMax + 1
	}
	return len(f.distinct)
}

// finish lists the raw keys (each with how it changed) under path, and
// returns the structure.
func (b *structureBuilder) finish(c *mediaCollector, path string) mediaStructure {
	for _, k := range b.keys {
		f := b.fields[k]
		v := f.first
		if n := f.values(); n > 1 {
			count := numberText(n) + " values"
			if f.more {
				count = "over " + numberText(structureDistinctMax) + " values"
			}
			v = f.first + " … " + f.last + " (" + count + ")"
			if b.s.Fields == nil {
				b.s.Fields = map[string]*structureField{}
			}
			sf := &structureField{Values: n, First: truncateRunes(f.first, mediaTagMax), Last: truncateRunes(f.last, mediaTagMax)}
			if f.numbers == b.s.Count && f.numbers > 1 {
				lo, hi := f.min, f.max
				sf.Min, sf.Max = &lo, &hi
				sf.Series = thin(f.points, structureSeriesMax)
			}
			b.s.Fields[k] = sf
		}
		c.raw(path, k, v)
	}
	return b.s
}

// thin keeps n of the points, evenly, the first and last among them.
func thin(p [][2]float64, n int) [][2]float64 {
	if len(p) <= n {
		return p
	}
	out := make([][2]float64, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, p[i*(len(p)-1)/(n-1)])
	}
	return out
}

// structure keeps a structure for the viewer.
func (c *mediaCollector) structure(s mediaStructure) {
	if len(c.doc.Structures) < structuresMax && s.Value != nil {
		c.doc.Structures = append(c.doc.Structures, s)
	}
}

// numberText writes n with thousands separators.
func numberText(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// clockText is a time into the video as m:ss.s.
func clockText(sec float64) string {
	tenths := int(math.Round(sec * 10))
	return fmt.Sprintf("%d:%02d.%d", tenths/600, tenths/10%60, tenths%10)
}

// listTimed lists the timed metadata and SEI: each kind of message as a
// structure, with how many and when, and every key it had in Raw; and
// in the Data group, a line for each.
func (c *mediaCollector) listTimed() {
	type kind struct {
		path, name string
		b          *structureBuilder
	}
	var kinds []*kind
	byKey := map[string]*kind{}
	for _, m := range c.timed {
		t := c.tracks[m.track]
		path := t.path + "/" + boxName(t.entry)
		key := path + "\x00" + m.name
		k := byKey[key]
		if k == nil {
			k = &kind{path: path, name: m.name, b: newStructureBuilder(path, m.name, true)}
			byKey[key] = k
			kinds = append(kinds, k)
		}
		k.b.add(m.time, m.value)
	}
	perTrack := map[string][]string{}
	var trackOrder []string
	for _, k := range kinds {
		name := k.name
		if name == "" {
			name = "text"
		}
		n := k.b.s.Count
		c.raw(k.path, name, numberText(n)+" messages, "+clockText(k.b.s.Start)+" to "+clockText(k.b.s.End))
		c.structure(k.b.finish(c, k.path+" "+name))
		if _, ok := perTrack[k.path]; !ok {
			trackOrder = append(trackOrder, k.path)
		}
		perTrack[k.path] = append(perTrack[k.path], name+" ×"+numberText(n))
	}
	lines := make([]string, 0, len(trackOrder))
	for _, p := range trackOrder {
		entry := p[strings.LastIndexByte(p, '/')+1:]
		lines = append(lines, strings.ToUpper(entry)+": "+strings.Join(perTrack[p], ", "))
	}
	c.add("Data", "Timed metadata", strings.Join(lines, "; "))

	// SEI: each UUID's JSON objects by their "type" when they say one.
	var seiKinds []*kind
	seiByKey := map[string]*kind{}
	for _, m := range c.sei {
		vals := []any{rawPreview(m.payload)}
		names := []string{""}
		if len(m.objs) > 0 {
			vals, names = nil, nil
			for _, o := range m.objs {
				vals = append(vals, o)
				names = append(names, jsonText(o["type"]))
			}
		}
		for i, name := range names {
			key := m.uuid + "\x00" + name
			k := seiByKey[key]
			if k == nil {
				path := "SEI " + fmtUUID(m.uuid)
				k = &kind{path: path, name: name, b: newStructureBuilder(path, name, true)}
				seiByKey[key] = k
				seiKinds = append(seiKinds, k)
			}
			k.b.add(m.time, vals[i])
		}
	}
	var seiNames []string
	for _, k := range seiKinds {
		label := k.name
		if label == "" {
			label = "user data"
		}
		c.raw(k.path, label, numberText(k.b.s.Count)+" of the frames read, "+clockText(k.b.s.Start)+" to "+clockText(k.b.s.End))
		s := k.b.finish(c, k.path+" "+label)
		if _, ok := s.Value.(map[string]any); ok {
			c.structure(s)
		}
		if !containsFold(seiNames, label) {
			seiNames = append(seiNames, label)
		}
	}
	if len(seiNames) > 0 {
		c.add("Data", "SEI", strings.Join(seiNames, ", ")+" (in the frames read)")
	}
	// x264 and x265 sign their frames with their version and settings.
	for _, m := range c.sei {
		if s := string(bytes.TrimRight(m.payload, "\x00")); strings.HasPrefix(s, "x264 - ") || strings.HasPrefix(s, "x265 (build") {
			name, _, _ := strings.Cut(s, " - H.26")
			c.add("Video", "Encoder", name)
			break
		}
	}
}

// jsonText is a JSON value as text, "" for anything but text or a number.
func jsonText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	}
	return ""
}

// Coordinates, by the names they go by.
var (
	latKeys = map[string]bool{"lat": true, "latitude": true, "gpslatitude": true, "gps_latitude": true}
	lonKeys = map[string]bool{"lon": true, "lng": true, "long": true, "longitude": true, "gpslongitude": true, "gps_longitude": true}
)

// findLocation looks through a structure for where it was: an object
// with a latitude and a longitude, or an ISO 6709 string under a key
// that says it's a location.
func findLocation(v any, depth int) *mediaLocation {
	if depth > 8 {
		return nil
	}
	switch x := v.(type) {
	case map[string]any:
		var lat, lon float64
		var okLat, okLon bool
		for k, e := range x {
			lk := strings.ToLower(k)
			n, err := strconv.ParseFloat(scalarText(e), 64)
			switch {
			case err == nil && latKeys[lk]:
				lat, okLat = n, true
			case err == nil && lonKeys[lk]:
				lon, okLon = n, true
			}
		}
		if okLat && okLon && math.Abs(lat) <= 90 && math.Abs(lon) <= 180 && (lat != 0 || lon != 0) {
			return &mediaLocation{Lat: round6(lat), Lon: round6(lon)}
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s, ok := x[k].(string); ok && (strings.Contains(strings.ToLower(k), "location") || strings.Contains(strings.ToLower(k), "6709")) {
				if l := iso6709(s); l != nil {
					return l
				}
			}
			if l := findLocation(x[k], depth+1); l != nil {
				return l
			}
		}
	case []any:
		for _, e := range x {
			if l := findLocation(e, depth+1); l != nil {
				return l
			}
		}
	}
	return nil
}

// applyTimed lists what the tracks carry over time, and takes where
// the file was made from its structures when nothing else says.
func (c *mediaCollector) applyTimed() {
	if len(c.timed) > 0 || len(c.sei) > 0 {
		c.listTimed()
	}
	// What the tracks carry first, the file's own (XMP) after.
	sort.SliceStable(c.doc.Structures, func(i, j int) bool { return c.doc.Structures[i].Timed && !c.doc.Structures[j].Timed })
	if c.doc.Location == nil {
		for _, s := range c.doc.Structures {
			if l := findLocation(s.Value, 0); l != nil {
				c.location(l)
				break
			}
		}
	}
}
