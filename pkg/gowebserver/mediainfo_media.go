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
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cloudfra/gowebserver/pkg/ffmpeg"
)

// Videos' and songs' metadata, for mediainfo.go: ISO base media files
// (MP4, QuickTime, M4A, and HEIF photos), MP3 with ID3v2, FLAC and Ogg,
// and what ffmpeg says of any video.

// ---- ISO base media (MP4, QuickTime, HEIF) ----

type isoBox struct {
	typ  string
	data []byte
}

// isoBoxes splits b into its boxes.
func isoBoxes(b []byte) []isoBox {
	var out []isoBox
	for len(b) >= 8 {
		n, typ, h := uint64(binary.BigEndian.Uint32(b)), string(b[4:8]), uint64(8)
		switch n {
		case 1:
			if len(b) < 16 {
				return out
			}
			n, h = binary.BigEndian.Uint64(b[8:]), 16
		case 0:
			n = uint64(len(b))
		}
		if n < h || n > uint64(len(b)) {
			return out
		}
		out = append(out, isoBox{typ: typ, data: b[h:n]})
		b = b[n:]
	}
	return out
}

func isoFind(b []byte, typ string) []byte {
	for _, x := range isoBoxes(b) {
		if x.typ == typ {
			return x.data
		}
	}
	return nil
}

// isoBrands are the major brands that are photos (HEIF and AVIF) and
// songs; any other is a video.
var isoBrands = map[string]string{
	"heic": "HEIF", "heix": "HEIF", "heim": "HEIF", "heis": "HEIF", "mif1": "HEIF", "msf1": "HEIF",
	"hevc": "HEIF", "avif": "AVIF", "avis": "AVIF", "M4A ": "MPEG-4 audio", "M4B ": "MPEG-4 audiobook",
	"qt  ": "QuickTime", "3gp4": "3GPP", "3gp5": "3GPP", "3gp6": "3GPP",
}

// parseISOBMFF reads the top-level boxes: the movie (moov) or the
// photo's items (meta), skipping the media data.
func parseISOBMFF(s *mediaSource, c *mediaCollector) error {
	var heif *heifMeta
	for s.size <= 0 || s.pos < s.size {
		h, err := s.bytes(8)
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		n, typ, hl := int64(binary.BigEndian.Uint32(h)), string(h[4:8]), int64(8)
		switch n {
		case 1:
			l, err := s.bytes(8)
			if err != nil {
				return err
			}
			n, hl = int64(binary.BigEndian.Uint64(l)), 16
		case 0:
			n = s.size - s.pos + 8
		}
		if n < hl {
			return fmt.Errorf("mediainfo: a box is too short")
		}
		body := n - hl
		if typ != "ftyp" && typ != "moov" && typ != "meta" && typ != "uuid" {
			c.raw(boxName(typ), "size", fmtBytesRaw(n))
		}
		switch typ {
		case "ftyp":
			p, err := s.bytes(body)
			if err != nil {
				return err
			}
			if len(p) >= 4 {
				brand := string(p[:4])
				c.raw("ftyp", "major brand", brand)
				if len(p) >= 8 {
					c.raw("ftyp", "minor version", strconv.FormatUint(uint64(binary.BigEndian.Uint32(p[4:])), 10))
				}
				c.tag("MP4", "Major brand", strings.TrimSpace(brand))
				format, ok := isoBrands[brand]
				if !ok {
					format = "MPEG-4"
				}
				c.doc.Format = format
				var compat []string
				for i := 8; i+4 <= len(p); i += 4 {
					compat = append(compat, strings.TrimSpace(string(p[i:i+4])))
				}
				c.tag("MP4", "Compatible brands", strings.Join(compat, ", "))
				c.raw("ftyp", "compatible brands", strings.Join(compat, ", "))
			}
		case "moov":
			p, err := s.bytes(body)
			if err != nil {
				return err
			}
			parseMoov(c, p)
			if heif == nil {
				// What the tracks carry over time, read where it is
				// (usually before the moov, in the media data); then, when
				// the file can seek, the boxes after the moov (a UUID
				// box's XMP).
				after := s.pos
				if err := c.readTimed(s); err != nil || s.seeker == nil || s.seek(after) != nil {
					return err
				}
			}
		case "uuid":
			// A box of someone's own, named by a UUID: XMP's, or another
			// to show as it is.
			if body > 1<<20 {
				c.raw("uuid", "size", fmtBytesRaw(n))
				if err := s.skip(body); err != nil {
					return err
				}
				continue
			}
			p, err := s.bytes(body)
			if err != nil {
				return err
			}
			c.uuidBox("uuid", p)
		case "meta":
			p, err := s.bytes(body)
			if err != nil {
				return err
			}
			if heif = parseHEIFMeta(c, p); heif != nil {
				return heif.readItems(s, c)
			}
		default:
			if err := s.skip(body); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- HEIF ----

type heifExtent struct{ off, n int64 }

type heifItem struct {
	typ, mime string
	method    int
	extents   []heifExtent
}

type heifMeta struct {
	items map[uint32]*heifItem
	idat  []byte
}

func parseHEIFMeta(c *mediaCollector, p []byte) *heifMeta {
	if len(p) < 4 {
		return nil
	}
	m := &heifMeta{items: map[uint32]*heifItem{}}
	var primary uint32
	var props []isoBox
	assoc := map[uint32][]int{}
	item := func(id uint32) *heifItem {
		if m.items[id] == nil {
			m.items[id] = &heifItem{}
		}
		return m.items[id]
	}
	for _, b := range isoBoxes(p[4:]) {
		d := b.data
		if len(d) < 4 {
			continue
		}
		v := d[0]
		switch b.typ {
		case "pitm":
			if v == 0 && len(d) >= 6 {
				primary = uint32(binary.BigEndian.Uint16(d[4:]))
			} else if len(d) >= 8 {
				primary = binary.BigEndian.Uint32(d[4:])
			}
		case "iinf":
			off := 6
			if v != 0 {
				off = 8
			}
			if off > len(d) {
				continue
			}
			for _, e := range isoBoxes(d[off:]) {
				if e.typ != "infe" || len(e.data) < 12 || e.data[0] < 2 {
					continue
				}
				q := e.data[4:]
				var id uint32
				if e.data[0] == 2 {
					id, q = uint32(binary.BigEndian.Uint16(q)), q[2:]
				} else {
					id, q = binary.BigEndian.Uint32(q), q[4:]
				}
				if len(q) < 6 {
					continue
				}
				it := item(id)
				it.typ = string(q[2:6])
				q = q[6:]
				_, q, _ = bytes.Cut(q, []byte{0}) // the item's name
				if it.typ == "mime" {
					ct, _, _ := bytes.Cut(q, []byte{0})
					it.mime = string(ct)
				}
			}
		case "iloc":
			parseIloc(d, item)
		case "idat":
			m.idat = d
		case "iprp":
			for _, x := range isoBoxes(d) {
				switch x.typ {
				case "ipco":
					props = isoBoxes(x.data)
				case "ipma":
					parseIpma(x.data, assoc)
				}
			}
		}
	}
	if len(m.items) == 0 {
		return nil
	}
	pi := m.items[primary]
	if pi != nil {
		codec := map[string]string{"hvc1": "HEVC", "av01": "AV1", "jpeg": "JPEG", "grid": "tiled", "avc1": "H.264", "j2k1": "JPEG 2000"}[pi.typ]
		if pi.typ == "grid" {
			// A grid's tiles carry the codec.
			for _, it := range m.items {
				if it.typ == "hvc1" || it.typ == "av01" {
					codec = map[string]string{"hvc1": "HEVC", "av01": "AV1"}[it.typ] + ", tiled"
					break
				}
			}
		}
		if codec != "" {
			c.add("Image", "Compression", codec)
		}
	}
	rot := 0
	for _, idx := range assoc[primary] {
		if idx < 1 || idx > len(props) {
			continue
		}
		pr := props[idx-1]
		d := pr.data
		switch pr.typ {
		case "ispe":
			if len(d) >= 12 {
				c.size(int(binary.BigEndian.Uint32(d[4:])), int(binary.BigEndian.Uint32(d[8:])))
			}
		case "pixi":
			if len(d) >= 6 && d[4] > 0 {
				c.add("Image", "Bit depth", fmt.Sprintf("%d bits per channel", d[5]))
				c.add("Image", "Color model", map[byte]string{1: "Grayscale", 3: "YCbCr", 4: "YCbCr with alpha"}[d[4]])
			}
		case "colr":
			if len(d) < 4 {
				continue
			}
			switch string(d[:4]) {
			case "nclx":
				if len(d) >= 11 {
					cicp(c, int(binary.BigEndian.Uint16(d[4:])), int(binary.BigEndian.Uint16(d[6:])), int(binary.BigEndian.Uint16(d[8:])), d[10]&0x80 != 0)
				}
			case "prof", "rICC":
				if c.icc == nil {
					c.icc = d[4:]
				}
			}
		case "irot":
			if len(d) >= 1 {
				rot = int(d[0] & 3)
			}
		}
	}
	if rot == 1 || rot == 3 {
		c.doc.Width, c.doc.Height = c.doc.Height, c.doc.Width
		c.add("Image", "Orientation", map[int]string{1: "Rotated 90° counterclockwise", 3: "Rotated 90° clockwise"}[rot])
	}
	return m
}

func parseIloc(d []byte, item func(uint32) *heifItem) {
	if len(d) < 8 {
		return
	}
	v := d[0]
	offSize, lenSize, baseSize := int(d[4]>>4), int(d[4]&15), int(d[5]>>4)
	idxSize := 0
	if v == 1 || v == 2 {
		idxSize = int(d[5] & 15)
	}
	q := d[6:]
	read := func(n int) (uint64, bool) {
		if n == 0 {
			return 0, true
		}
		if len(q) < n {
			return 0, false
		}
		var x uint64
		for i := 0; i < n; i++ {
			x = x<<8 | uint64(q[i])
		}
		q = q[n:]
		return x, true
	}
	idWidth := 2
	if v == 2 {
		idWidth = 4
	}
	count, ok := read(idWidth)
	if !ok {
		return
	}
	for i := uint64(0); i < count && i < 10000; i++ {
		id, ok := read(idWidth)
		if !ok {
			return
		}
		it := item(uint32(id))
		if v == 1 || v == 2 {
			cm, ok := read(2)
			if !ok {
				return
			}
			it.method = int(cm & 15)
		}
		if _, ok := read(2); !ok { // data reference index
			return
		}
		base, ok := read(baseSize)
		if !ok {
			return
		}
		n, ok := read(2)
		if !ok {
			return
		}
		for j := uint64(0); j < n; j++ {
			if _, ok := read(idxSize); !ok {
				return
			}
			off, ok1 := read(offSize)
			l, ok2 := read(lenSize)
			if !ok1 || !ok2 {
				return
			}
			it.extents = append(it.extents, heifExtent{off: int64(base + off), n: int64(l)})
		}
	}
}

func parseIpma(d []byte, assoc map[uint32][]int) {
	if len(d) < 8 {
		return
	}
	v, flags := d[0], d[3]
	n := binary.BigEndian.Uint32(d[4:])
	q := d[8:]
	for i := uint32(0); i < n && len(q) > 0; i++ {
		var id uint32
		if v < 1 {
			if len(q) < 3 {
				return
			}
			id, q = uint32(binary.BigEndian.Uint16(q)), q[2:]
		} else {
			if len(q) < 5 {
				return
			}
			id, q = binary.BigEndian.Uint32(q), q[4:]
		}
		k := int(q[0])
		q = q[1:]
		for j := 0; j < k; j++ {
			if flags&1 != 0 {
				if len(q) < 2 {
					return
				}
				assoc[id] = append(assoc[id], int(binary.BigEndian.Uint16(q)&0x7fff))
				q = q[2:]
			} else {
				if len(q) < 1 {
					return
				}
				assoc[id] = append(assoc[id], int(q[0]&0x7f))
				q = q[1:]
			}
		}
	}
}

// readItems reads the photo's EXIF and XMP items, wherever the file has
// them: after the meta box (in the media data), or inside it (idat).
func (m *heifMeta) readItems(s *mediaSource, c *mediaCollector) error {
	type want struct {
		it   *heifItem
		exif bool
	}
	var wants []want
	for _, it := range m.items {
		switch {
		case it.typ == "Exif":
			wants = append(wants, want{it, true})
		case it.typ == "mime" && strings.Contains(it.mime, "rdf+xml"):
			wants = append(wants, want{it, false})
		}
	}
	// In file order, so a file that can't seek is read forward only.
	sort.Slice(wants, func(i, j int) bool { return first(wants[i].it) < first(wants[j].it) })
	for _, w := range wants {
		var data []byte
		for _, e := range w.it.extents {
			if e.n <= 0 || e.n > 4<<20 {
				break
			}
			if w.it.method == 1 {
				if e.off+e.n <= int64(len(m.idat)) {
					data = append(data, m.idat[e.off:e.off+e.n]...)
				}
				continue
			}
			if err := s.seek(e.off); err != nil {
				return err
			}
			p, err := s.bytes(e.n)
			if err != nil {
				return err
			}
			data = append(data, p...)
		}
		if w.exif {
			// A 4-byte offset to the TIFF header comes first.
			if len(data) > 4 {
				if o := int(binary.BigEndian.Uint32(data)); 4+o < len(data) {
					c.exif = data[4+o:]
				}
			}
		} else if c.xmp == nil {
			c.xmp = data
		}
	}
	return nil
}

func first(it *heifItem) int64 {
	if len(it.extents) == 0 || it.method == 1 {
		return -1
	}
	return it.extents[0].off
}

// ---- MP4 and QuickTime movies ----

// macEpoch is when MP4's times count from.
var macEpoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)

type mp4Track struct {
	handler, codec, lang string
	width, height        float64
	rotation             int
	channels, sampleRate int
	sampleBits           int
	timescale, samples   uint64
	duration             uint64
	// path is the track's box ("moov/trak[2]"), entry its sample entry's
	// type (avc1, mp4a, amf0...), compressor the encoder's name that
	// entry may give, name the handler's name, and nalLen the length of
	// an H.264 or HEVC sample's NAL unit sizes (0 for other codecs).
	path, entry, compressor, name string
	nalLen                        int
	stbl                          []byte
}

func parseMoov(c *mediaCollector, moov []byte) {
	var timescale, duration uint64
	var tracks []*mp4Track
	for _, b := range isoBoxes(moov) {
		d := b.data
		switch b.typ {
		case "mvhd":
			if len(d) < 32 {
				continue
			}
			var created, modified uint64
			if d[0] == 1 {
				created, modified, timescale, duration = binary.BigEndian.Uint64(d[4:]), binary.BigEndian.Uint64(d[12:]), uint64(binary.BigEndian.Uint32(d[20:])), binary.BigEndian.Uint64(d[24:])
			} else {
				created, modified, timescale, duration = uint64(binary.BigEndian.Uint32(d[4:])), uint64(binary.BigEndian.Uint32(d[8:])), uint64(binary.BigEndian.Uint32(d[12:])), uint64(binary.BigEndian.Uint32(d[16:]))
			}
			c.raw("moov/mvhd", "creation time", macTime(created))
			c.raw("moov/mvhd", "modification time", macTime(modified))
			c.raw("moov/mvhd", "timescale", strconv.FormatUint(timescale, 10))
			c.raw("moov/mvhd", "duration", strconv.FormatUint(duration, 10))
			if created > 0 {
				t := macEpoch.Add(time.Duration(created) * time.Second)
				if t.Year() > 1970 {
					c.tag("MP4", "Created", t.Format(time.RFC3339))
					c.mp4Created = t.Format("2006-01-02T15:04:05Z")
				}
			}
		case "trak":
			path := "moov/trak[" + strconv.Itoa(len(tracks)+1) + "]"
			t := parseTrak(d)
			t.path = path
			tracks = append(tracks, t)
			c.rawBoxes(path, d)
		case "udta":
			parseUdta(c, d, "moov/udta")
		case "meta":
			parseIlstMeta(c, d, "moov/meta")
		default:
			c.rawBox("moov", b)
		}
	}
	c.tracks = tracks
	if timescale > 0 && duration > 0 && duration != math.MaxUint32 && duration != math.MaxUint64 {
		c.doc.Duration = math.Round(float64(duration)/float64(timescale)*1000) / 1000
	}
	for _, t := range tracks {
		switch t.handler {
		case "vide":
			if t.width > 0 && c.doc.Width == 0 {
				w, h := int(math.Round(t.width)), int(math.Round(t.height))
				if t.rotation == 90 || t.rotation == 270 {
					w, h = h, w
				}
				c.size(w, h)
			}
			c.add("Video", "Codec", t.codec)
			c.add("Video", "Encoder", t.compressor)
			if t.timescale > 0 && t.duration > 0 && t.samples > 1 {
				fps := float64(t.samples) / (float64(t.duration) / float64(t.timescale))
				c.add("Video", "Frame rate", formatFloat(fps, 2)+" fps")
			}
			if t.rotation != 0 {
				c.add("Video", "Rotation", strconv.Itoa(t.rotation)+"°")
			}
		case "soun":
			c.add("Audio", "Codec", t.codec)
			if t.sampleRate > 0 {
				c.add("Audio", "Sample rate", sampleRate(t.sampleRate))
			}
			c.add("Audio", "Channels", channels(t.channels))
			if t.lang != "" && t.lang != "und" {
				c.tag("MP4", "Audio language", t.lang)
			}
		}
	}
}

var mp4Codecs = map[string]string{
	"avc1": "H.264", "avc3": "H.264", "hvc1": "HEVC (H.265)", "hev1": "HEVC (H.265)", "dvh1": "Dolby Vision (HEVC)",
	"dvhe": "Dolby Vision (HEVC)", "av01": "AV1", "vp09": "VP9", "vp08": "VP8", "mp4v": "MPEG-4 Visual",
	"apch": "Apple ProRes 422 HQ", "apcn": "Apple ProRes 422", "apcs": "Apple ProRes 422 LT", "apco": "Apple ProRes 422 Proxy",
	"ap4h": "Apple ProRes 4444", "ap4x": "Apple ProRes 4444 XQ", "jpeg": "Motion JPEG", "mjpa": "Motion JPEG",
	"mp4a": "AAC", "ac-3": "AC-3", "ec-3": "E-AC-3", "alac": "Apple Lossless", "Opus": "Opus", "fLaC": "FLAC",
	".mp3": "MP3", "lpcm": "PCM", "sowt": "PCM", "twos": "PCM", "ipcm": "PCM", "fpcm": "PCM",
}

func parseTrak(d []byte) *mp4Track {
	t := &mp4Track{}
	if tk := isoFind(d, "tkhd"); len(tk) >= 84 {
		off := 40 // the matrix, after the times, ids and volume
		if tk[0] == 1 {
			off = 52
		}
		if off+44 <= len(tk) {
			a, b := int32(binary.BigEndian.Uint32(tk[off:])), int32(binary.BigEndian.Uint32(tk[off+4:]))
			switch {
			case a == 0 && b > 0:
				t.rotation = 90
			case a == 0 && b < 0:
				t.rotation = 270
			case a < 0 && b == 0:
				t.rotation = 180
			}
			t.width = float64(binary.BigEndian.Uint32(tk[off+36:])) / 65536
			t.height = float64(binary.BigEndian.Uint32(tk[off+40:])) / 65536
		}
	}
	mdia := isoFind(d, "mdia")
	if hd := isoFind(mdia, "hdlr"); len(hd) >= 12 {
		t.handler = string(hd[8:12])
		if len(hd) > 24 {
			t.name = hdlrName(hd[24:])
		}
	}
	if md := isoFind(mdia, "mdhd"); len(md) >= 24 {
		var lang uint16
		if md[0] == 1 && len(md) >= 36 {
			t.timescale, t.duration, lang = uint64(binary.BigEndian.Uint32(md[20:])), binary.BigEndian.Uint64(md[24:]), binary.BigEndian.Uint16(md[32:])
		} else {
			t.timescale, t.duration, lang = uint64(binary.BigEndian.Uint32(md[12:])), uint64(binary.BigEndian.Uint32(md[16:])), binary.BigEndian.Uint16(md[20:])
		}
		if lang != 0 && lang != 0x7fff {
			t.lang = string([]byte{byte(lang>>10&31) + 0x60, byte(lang>>5&31) + 0x60, byte(lang&31) + 0x60})
		}
	}
	stbl := isoFind(isoFind(mdia, "minf"), "stbl")
	t.stbl = stbl
	if sd := isoFind(stbl, "stsd"); len(sd) >= 16 {
		e := sd[8:]
		if len(e) >= 8 {
			fourcc := string(e[4:8])
			t.entry = fourcc
			t.codec = mp4Codecs[fourcc]
			if t.codec == "" {
				t.codec = strings.TrimSpace(fourcc)
			}
			if n := int(binary.BigEndian.Uint32(e)); n >= 8 && n <= len(e) {
				e = e[:n]
			}
			body := e[8:]
			if t.handler == "soun" && len(body) >= 28 {
				t.channels = int(binary.BigEndian.Uint16(body[16:]))
				t.sampleBits = int(binary.BigEndian.Uint16(body[18:]))
				t.sampleRate = int(binary.BigEndian.Uint32(body[24:]) >> 16)
			}
			// A visual sample entry: its compressor's name, a Pascal string
			// of up to 31 bytes, then its codec's configuration.
			if t.handler == "vide" && len(body) >= 78 {
				if n := int(body[42]); n > 0 && n <= 31 {
					t.compressor = strings.TrimSpace(strings.TrimRight(validUTF8(body[43:43+n]), "\x00"))
				}
				for _, x := range isoBoxes(body[78:]) {
					switch {
					case x.typ == "avcC" && len(x.data) >= 5:
						t.nalLen = int(x.data[4]&3) + 1
					case x.typ == "hvcC" && len(x.data) >= 22:
						t.nalLen = int(x.data[21]&3) + 1
					}
				}
			}
		}
	}
	if st := isoFind(stbl, "stts"); len(st) >= 8 {
		n := int(binary.BigEndian.Uint32(st[4:]))
		for i := 0; i < n && 8+i*8+8 <= len(st); i++ {
			t.samples += uint64(binary.BigEndian.Uint32(st[8+i*8:]))
		}
	}
	return t
}

// parseUdta reads QuickTime's user data: ©xyz (the location), ©mak and
// ©mod (the camera), and the like, each a 16-bit length and language
// then the text; an iTunes meta box; and XMP (XMP_). Every other box
// is listed as it is, for "Raw metadata".
func parseUdta(c *mediaCollector, d []byte, path string) {
	for _, b := range isoBoxes(d) {
		switch {
		case b.typ == "meta":
			parseIlstMeta(c, b.data, path+"/meta")
		case b.typ == "XMP_":
			c.xmpBox(path+"/XMP_", b.data)
		case strings.HasPrefix(b.typ, "\xa9") && len(b.data) >= 4:
			n := int(binary.BigEndian.Uint16(b.data))
			if 4+n > len(b.data) {
				n = len(b.data) - 4
			}
			v := strings.TrimRight(validUTF8(b.data[4:4+n]), "\x00")
			c.raw(path, boxName(b.typ), v)
			c.mp4Item(b.typ, v)
		default:
			c.rawBox(path, b)
		}
	}
}

// parseIlstMeta reads a meta box's item list: iTunes-style (the title, ©lyr)
// or QuickTime's, named by a keys box (com.apple.quicktime.location.ISO6709).
func parseIlstMeta(c *mediaCollector, d []byte, path string) {
	// iTunes' meta is a full box, QuickTime's isn't.
	if len(d) >= 12 && string(d[8:12]) == "hdlr" && string(d[4:8]) != "hdlr" {
		d = d[4:]
	}
	var keys []string
	if k := isoFind(d, "keys"); len(k) >= 8 {
		n := int(binary.BigEndian.Uint32(k[4:]))
		q := k[8:]
		for i := 0; i < n && len(q) >= 8; i++ {
			l := int(binary.BigEndian.Uint32(q))
			if l < 8 || l > len(q) {
				break
			}
			keys = append(keys, string(q[8:l]))
			c.raw(path+"/keys", strconv.Itoa(i+1), string(q[4:8])+": "+string(q[8:l]))
			q = q[l:]
		}
	}
	if hd := isoFind(d, "hdlr"); len(hd) >= 12 {
		c.raw(path+"/hdlr", "handler", strings.TrimRight(string(hd[8:12]), "\x00"))
	}
	for _, it := range isoBoxes(isoFind(d, "ilst")) {
		name, rawKey := it.typ, boxName(it.typ)
		if len(keys) > 0 {
			if i := int(binary.BigEndian.Uint32([]byte(it.typ))); i >= 1 && i <= len(keys) {
				name, rawKey = keys[i-1], keys[i-1]
			}
		}
		data := isoFind(it.data, "data")
		if len(data) < 8 {
			c.raw(path+"/ilst", rawKey, rawPreview(it.data))
			continue
		}
		typ, v := binary.BigEndian.Uint32(data)&0xffffff, data[8:]
		var s string
		switch {
		case name == "trkn" || name == "disk":
			if len(v) >= 6 {
				n, of := binary.BigEndian.Uint16(v[2:]), binary.BigEndian.Uint16(v[4:])
				s = strconv.Itoa(int(n))
				if of > 0 {
					s += " of " + strconv.Itoa(int(of))
				}
				c.mp4Item(name, s)
			}
		case name == "gnre":
			if len(v) >= 2 {
				s = id3Genre(strconv.Itoa(int(binary.BigEndian.Uint16(v)) - 1))
				c.mp4Item("©gen", s)
			}
		case name == "covr":
			s = map[uint32]string{13: "JPEG", 14: "PNG", 27: "BMP"}[typ] + fmt.Sprintf(" (%s)", fmtSize(len(v)))
			c.mp4Item(name, s)
		case typ == 1:
			s = validUTF8(v)
			c.mp4Item(name, s)
		case typ == 2:
			s = utf16BE(v)
			c.mp4Item(name, s)
		case typ == 21 || typ == 22:
			var x uint64
			for _, b := range v {
				x = x<<8 | uint64(b)
			}
			s = strconv.FormatUint(x, 10)
			c.mp4Item(name, s)
		case typ == 23 && len(v) == 4:
			s = formatFloat(float64(math.Float32frombits(binary.BigEndian.Uint32(v))), 6)
		case typ == 24 && len(v) == 8:
			s = formatFloat(math.Float64frombits(binary.BigEndian.Uint64(v)), 9)
		default:
			s = rawPreview(v)
		}
		c.raw(path+"/ilst", rawKey, s)
	}
}

func fmtSize(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%s kB", formatFloat(float64(n)/1000, 0))
}

// mp4Item puts an MP4 tag where it belongs in the panel.
func (c *mediaCollector) mp4Item(name, v string) {
	v = strings.TrimSpace(v)
	if v == "" {
		return
	}
	label := strings.TrimPrefix(name, "com.apple.quicktime.")
	c.tag("MP4", strings.ReplaceAll(label, "\xa9", "©"), v)
	// QuickTime keys that are plain words (as ffmpeg writes its tags with
	// -movflags use_metadata_tags) are iTunes' items by other names.
	if alias, ok := mp4Aliases[name]; ok {
		name = alias
	}
	song := "Description"
	if c.doc.Kind == "audio" {
		song = "Song"
	}
	switch name {
	case "\xa9nam", "com.apple.quicktime.title":
		c.add(song, "Title", v)
	case "\xa9ART", "com.apple.quicktime.artist":
		c.add("Song", "Artist", v)
	case "aART":
		c.add("Song", "Album artist", v)
	case "\xa9alb", "com.apple.quicktime.album":
		c.add("Song", "Album", v)
	case "\xa9gen":
		c.add("Song", "Genre", v)
	case "\xa9wrt":
		c.add("Song", "Composer", v)
	case "trkn":
		c.add("Song", "Track", v)
	case "disk":
		c.add("Song", "Disc", v)
	case "covr":
		c.add("Song", "Cover art", v)
	case "\xa9day", "com.apple.quicktime.creationdate":
		if t := xmpTime(isoOffset(v)); t != "" {
			c.taken(t)
		} else if c.doc.Kind == "audio" {
			c.taken(v)
			c.add("Song", "Year", v)
		}
	case "\xa9lyr":
		c.text("Lyrics", "", v, false)
	case "\xa9cmt", "com.apple.quicktime.comment":
		c.text("Comment", "", v, false)
	case "desc", "ldes", "\xa9des", "com.apple.quicktime.description":
		c.text("Description", "", v, false)
	case "\xa9too", "\xa9enc", "\xa9swr", "com.apple.quicktime.software":
		c.add("Description", "Software", v)
	case "cprt", "\xa9cpy", "com.apple.quicktime.copyright":
		c.add("Description", "Copyright", v)
	case "\xa9mak", "com.apple.quicktime.make":
		c.mp4Make = v
	case "\xa9mod", "com.apple.quicktime.model":
		c.mp4Model = v
	case "com.android.version":
		c.add("Camera", "Device", "Android "+v)
	case "\xa9xyz", "com.apple.quicktime.location.ISO6709":
		c.location(iso6709(v))
	}
}

var mp4Aliases = map[string]string{
	"title": "\xa9nam", "artist": "\xa9ART", "album_artist": "aART", "album": "\xa9alb", "genre": "\xa9gen",
	"composer": "\xa9wrt", "date": "\xa9day", "lyrics": "\xa9lyr", "comment": "\xa9cmt", "description": "desc",
	"encoder": "\xa9too", "copyright": "cprt", "location": "\xa9xyz", "make": "\xa9mak", "model": "\xa9mod",
	"com.apple.quicktime.location.iso6709": "\xa9xyz",
}

// isoOffset puts a colon in an offset written +0200, which xmpTime
// reads as +02:00.
func isoOffset(t string) string {
	if n := len(t); n >= 5 && (t[n-5] == '+' || t[n-5] == '-') && !strings.Contains(t[n-5:], ":") {
		return t[:n-2] + ":" + t[n-2:]
	}
	return t
}

// iso6709 reads a location as ISO 6709 writes it: "+48.8584+002.2945
// +035.000/" (degrees), or in degrees and minutes ("+4851.50+00217.70")
// or seconds, with an optional altitude.
var iso6709Part = regexp.MustCompile(`([+-])(\d+)(\.\d+)?`)

// parseNum reads a number the pattern matched, or NaN, which
// mediaCollector.location turns away.
func parseNum(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return v
}

func iso6709(s string) *mediaLocation {
	parts := iso6709Part.FindAllStringSubmatch(s, 3)
	if len(parts) < 2 {
		return nil
	}
	coord := func(p []string, degDigits int) float64 {
		whole, frac := p[2], p[3]
		var v float64
		switch len(whole) - degDigits {
		case 2: // DDMM.mm
			v = parseNum(whole[:degDigits]) + parseNum(whole[degDigits:]+frac)/60
		case 4: // DDMMSS.ss
			v = parseNum(whole[:degDigits]) + parseNum(whole[degDigits:degDigits+2])/60 + parseNum(whole[degDigits+2:]+frac)/3600
		default:
			v = parseNum(whole + frac)
		}
		if p[1] == "-" {
			v = -v
		}
		return v
	}
	l := &mediaLocation{Lat: round6(coord(parts[0], 2)), Lon: round6(coord(parts[1], 3))}
	if len(parts) == 3 {
		a, err := strconv.ParseFloat(parts[2][1]+parts[2][2]+parts[2][3], 64)
		if err == nil {
			a = math.Round(a*10) / 10
			l.Alt = &a
		}
	}
	return l
}

// ---- MP3 and ID3v2 ----

func parseMP3(s *mediaSource, c *mediaCollector) error {
	c.doc.Format = "MP3"
	start := int64(0)
	h, err := s.bytes(10)
	if err != nil {
		return err
	}
	if string(h[:3]) == "ID3" {
		ver, flags := h[3], h[5]
		size := int64(syncsafe(h[6:10]))
		tag, err := s.bytes(size)
		if err != nil {
			return err
		}
		c.tag("ID3", "Version", fmt.Sprintf("2.%d", ver))
		parseID3(c, tag, ver, flags)
		start = 10 + size
		if flags&0x10 != 0 {
			start += 10 // a footer
			if err := s.skip(10); err != nil {
				return err
			}
		}
	} else if err := s.unread(h); err != nil {
		return err
	}
	// The first frame's header: padding may come before it.
	// A file shorter than that is read to its end, which is no error.
	buf, err := s.bytes(16 << 10)
	if len(buf) == 0 {
		return err
	}
	for i := 0; i+4 <= len(buf); i++ {
		if buf[i] == 0xFF && buf[i+1]&0xE0 == 0xE0 {
			if mp3Frame(c, buf[i:], s.size-start-int64(i)) {
				break
			}
		}
	}
	return nil
}

func syncsafe(b []byte) uint32 {
	return uint32(b[0]&0x7f)<<21 | uint32(b[1]&0x7f)<<14 | uint32(b[2]&0x7f)<<7 | uint32(b[3]&0x7f)
}

// unsync undoes ID3's unsynchronisation: 0xFF 0x00 is 0xFF.
func unsync(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte{0xFF, 0x00}, []byte{0xFF})
}

var mp3Bitrates = [2][16]int{
	{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}, // MPEG-1 layer III
	{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},     // MPEG-2 and 2.5 layer III
}

// mp3Frame reads an MPEG audio frame header (and a Xing or Info header
// in the first frame, for a variable bit rate's length). It reports
// whether b starts with one.
func mp3Frame(c *mediaCollector, b []byte, audioBytes int64) bool {
	version, layer := b[1]>>3&3, b[1]>>1&3
	bi, si, mode := int(b[2]>>4), int(b[2]>>2&3), b[3]>>6
	if version == 1 || layer != 1 || bi == 0 || bi == 15 || si == 3 {
		return false // only layer III: MP3
	}
	rates := map[byte][3]int{3: {44100, 48000, 32000}, 2: {22050, 24000, 16000}, 0: {11025, 12000, 8000}}
	rate := rates[version][si]
	row := 0
	if version != 3 {
		row = 1
	}
	kbps := mp3Bitrates[row][bi]
	name := map[byte]string{3: "MPEG-1", 2: "MPEG-2", 0: "MPEG-2.5"}[version]
	c.add("Audio", "Codec", "MP3 ("+name+" Layer III)")
	c.add("Audio", "Sample rate", sampleRate(rate))
	c.add("Audio", "Channels", map[byte]string{0: "Stereo", 1: "Joint stereo", 2: "Dual channel", 3: "Mono"}[mode])
	// The Xing header sits after the side information.
	side := map[bool]int{true: 32, false: 17}[mode != 3]
	if version != 3 {
		side = map[bool]int{true: 17, false: 9}[mode != 3]
	}
	samples := 1152
	if version != 3 {
		samples = 576
	}
	if x := 4 + side; x+12 <= len(b) && (string(b[x:x+4]) == "Xing" || string(b[x:x+4]) == "Info") {
		if flags := binary.BigEndian.Uint32(b[x+4:]); flags&1 != 0 {
			frames := binary.BigEndian.Uint32(b[x+8:])
			if d := float64(frames) * float64(samples) / float64(rate); d > 0 {
				c.doc.Duration = math.Round(d*1000) / 1000
				if string(b[x:x+4]) == "Xing" {
					c.add("Audio", "Bit rate", "Variable, about "+strconv.Itoa(int(float64(audioBytes)*8/d/1000))+" kb/s")
				}
			}
		}
	}
	c.add("Audio", "Bit rate", strconv.Itoa(kbps)+" kb/s")
	if c.doc.Duration == 0 && kbps > 0 && audioBytes > 0 {
		c.doc.Duration = math.Round(float64(audioBytes)*8/float64(kbps*1000)*1000) / 1000
	}
	return true
}

// id3v22 maps ID3v2.2's three-letter frames to v2.3's.
var id3v22 = map[string]string{
	"TT2": "TIT2", "TP1": "TPE1", "TP2": "TPE2", "TAL": "TALB", "TYE": "TYER", "TCO": "TCON", "TRK": "TRCK",
	"TPA": "TPOS", "TCM": "TCOM", "ULT": "USLT", "SLT": "SYLT", "COM": "COMM", "PIC": "APIC", "TEN": "TENC",
	"TSS": "TSSE", "TCR": "TCOP", "TXX": "TXXX", "TLE": "TLEN",
}

func parseID3(c *mediaCollector, tag []byte, ver, flags byte) {
	if flags&0x80 != 0 && ver < 4 {
		tag = unsync(tag)
	}
	if flags&0x40 != 0 && ver >= 3 && len(tag) >= 4 {
		// An extended header: its size counts itself in v2.4, not in v2.3.
		n := int(binary.BigEndian.Uint32(tag))
		if ver == 4 {
			n = int(syncsafe(tag))
		} else {
			n += 4
		}
		if n > len(tag) {
			return
		}
		tag = tag[n:]
	}
	for len(tag) > 0 {
		var id string
		var size int
		var fflags uint16
		switch ver {
		case 2:
			if len(tag) < 6 {
				return
			}
			id, size = id3v22[string(tag[:3])], int(tag[3])<<16|int(tag[4])<<8|int(tag[5])
			if id == "" {
				id = string(tag[:3])
			}
			tag = tag[6:]
		default:
			if len(tag) < 10 {
				return
			}
			id = string(tag[:4])
			if ver == 4 {
				size = int(syncsafe(tag[4:8]))
			} else {
				size = int(binary.BigEndian.Uint32(tag[4:]))
			}
			fflags = binary.BigEndian.Uint16(tag[8:])
			tag = tag[10:]
		}
		if id == "" || id[0] == 0 || size > len(tag) || size < 0 {
			return
		}
		data := tag[:size]
		tag = tag[size:]
		if ver == 4 {
			if fflags&0x000C != 0 { // compressed or encrypted
				continue
			}
			if fflags&0x0001 != 0 && len(data) >= 4 {
				data = data[4:]
			}
			if fflags&0x0002 != 0 {
				data = unsync(data)
			}
		} else if ver == 3 && fflags&0x00C0 != 0 {
			continue
		}
		id3Frame(c, id, data, ver)
	}
}

// id3Text decodes ID3 text: enc 0 is ISO-8859-1, 1 UTF-16 with a byte
// order mark, 2 UTF-16BE, 3 UTF-8.
func id3Text(enc byte, b []byte) string {
	switch enc {
	case 1:
		if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
			return utf16BE(b[2:])
		}
		if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
			b = b[2:]
		}
		return utf16LE(b)
	case 2:
		return utf16BE(b)
	case 3:
		return strings.TrimRight(validUTF8(b), "\x00")
	}
	return strings.TrimRight(latin1(b), "\x00")
}

// id3Cut splits b at the end of its first string, in encoding enc.
func id3Cut(enc byte, b []byte) ([]byte, []byte) {
	if enc == 1 || enc == 2 {
		for i := 0; i+1 < len(b); i += 2 {
			if b[i] == 0 && b[i+1] == 0 {
				return b[:i], b[i+2:]
			}
		}
		return b, nil
	}
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return b[:i], b[i+1:]
	}
	return b, nil
}

func id3Frame(c *mediaCollector, id string, d []byte, ver byte) {
	if len(d) < 1 {
		return
	}
	enc := d[0]
	switch {
	case id == "TXXX":
		desc, val := id3Cut(enc, d[1:])
		k, v := id3Text(enc, desc), id3Text(enc, val)
		c.tag("ID3", "TXXX:"+k, v)
		switch strings.ToUpper(k) {
		case "LYRICS", "UNSYNCEDLYRICS", "UNSYNCED LYRICS":
			c.text("Lyrics", "", v, false)
		}
	case strings.HasPrefix(id, "T"):
		v := strings.ReplaceAll(strings.Trim(id3Text(enc, d[1:]), "\x00"), "\x00", "; ")
		c.tag("ID3", id, v)
		switch id {
		case "TIT2":
			c.add("Song", "Title", v)
		case "TPE1":
			c.add("Song", "Artist", v)
		case "TPE2":
			c.add("Song", "Album artist", v)
		case "TALB":
			c.add("Song", "Album", v)
		case "TRCK":
			c.add("Song", "Track", strings.Replace(v, "/", " of ", 1))
		case "TPOS":
			c.add("Song", "Disc", strings.Replace(v, "/", " of ", 1))
		case "TYER", "TDRC", "TORY", "TDOR":
			c.add("Song", "Year", v)
			c.taken(v)
		case "TCON":
			c.add("Song", "Genre", id3Genre(v))
		case "TCOM":
			c.add("Song", "Composer", v)
		case "TENC", "TSSE":
			c.add("Description", "Encoder", v)
		case "TCOP":
			c.add("Description", "Copyright", v)
		case "TLEN":
			if ms, err := strconv.Atoi(v); err == nil && ms > 0 && c.doc.Duration == 0 {
				c.doc.Duration = float64(ms) / 1000
			}
		}
	case id == "USLT" || id == "COMM":
		if len(d) < 4 {
			return
		}
		lang := strings.TrimRight(latin1(d[1:4]), "\x00")
		desc, text := id3Cut(enc, d[4:])
		v := id3Text(enc, text)
		label := "Lyrics"
		if id == "COMM" {
			label = "Comment"
			// iTunes keeps its own data in comments: not for people.
			if dd := id3Text(enc, desc); strings.HasPrefix(dd, "iTun") {
				c.tag("ID3", "COMM:"+dd, v)
				return
			}
		}
		c.tag("ID3", id, v)
		c.text(label, lang, v, false)
	case id == "SYLT":
		if len(d) < 6 {
			return
		}
		lang, format := strings.TrimRight(latin1(d[1:4]), "\x00"), d[4]
		_, q := id3Cut(enc, d[6:]) // the content descriptor
		var lines []string
		for len(q) > 0 {
			var t []byte
			t, q = id3Cut(enc, q)
			if len(q) < 4 {
				break
			}
			at := binary.BigEndian.Uint32(q)
			q = q[4:]
			line := strings.TrimLeft(id3Text(enc, t), "\n")
			if format == 2 {
				line = "[" + lyricTime(at) + "] " + line
			}
			lines = append(lines, line)
		}
		c.tag("ID3", "SYLT", fmt.Sprintf("%d lines", len(lines)))
		c.text("Synced lyrics", lang, strings.Join(lines, "\n"), format == 2)
	case id == "APIC":
		mime := "image"
		if ver == 2 && len(d) >= 4 {
			mime = map[string]string{"JPG": "JPEG", "PNG": "PNG"}[strings.ToUpper(string(d[1:4]))]
		} else if m, _, ok := bytes.Cut(d[1:], []byte{0}); ok {
			mime = strings.ToUpper(strings.TrimPrefix(string(m), "image/"))
		}
		c.add("Song", "Cover art", fmt.Sprintf("%s (%s)", mime, fmtSize(len(d))))
		c.tag("ID3", id, fmt.Sprintf("%s, %s", mime, fmtSize(len(d))))
	default:
		c.tag("ID3", id, fmt.Sprintf("(%d bytes)", len(d)))
	}
}

func lyricTime(ms uint32) string {
	s := ms / 1000
	return fmt.Sprintf("%d:%02d.%02d", s/60, s%60, ms%1000/10)
}

// id3Genres are ID3v1's genres, which ID3v2 and MP4 refer to by number.
var id3Genres = []string{
	"Blues", "Classic Rock", "Country", "Dance", "Disco", "Funk", "Grunge", "Hip-Hop", "Jazz", "Metal",
	"New Age", "Oldies", "Other", "Pop", "R&B", "Rap", "Reggae", "Rock", "Techno", "Industrial",
	"Alternative", "Ska", "Death Metal", "Pranks", "Soundtrack", "Euro-Techno", "Ambient", "Trip-Hop", "Vocal", "Jazz+Funk",
	"Fusion", "Trance", "Classical", "Instrumental", "Acid", "House", "Game", "Sound Clip", "Gospel", "Noise",
	"Alternative Rock", "Bass", "Soul", "Punk", "Space", "Meditative", "Instrumental Pop", "Instrumental Rock", "Ethnic", "Gothic",
	"Darkwave", "Techno-Industrial", "Electronic", "Pop-Folk", "Eurodance", "Dream", "Southern Rock", "Comedy", "Cult", "Gangsta",
	"Top 40", "Christian Rap", "Pop/Funk", "Jungle", "Native American", "Cabaret", "New Wave", "Psychedelic", "Rave", "Showtunes",
	"Trailer", "Lo-Fi", "Tribal", "Acid Punk", "Acid Jazz", "Polka", "Retro", "Musical", "Rock & Roll", "Hard Rock",
}

var id3GenreRef = regexp.MustCompile(`^\((\d+)\)|^(\d+)$`)

func id3Genre(v string) string {
	if m := id3GenreRef.FindStringSubmatch(v); m != nil {
		n, err := strconv.Atoi(m[1] + m[2])
		rest := strings.TrimSpace(v[len(m[0]):])
		if rest != "" {
			return rest
		}
		if err == nil && n >= 0 && n < len(id3Genres) {
			return id3Genres[n]
		}
	}
	return v
}

// ---- FLAC, Ogg and Vorbis comments ----

func parseFLAC(s *mediaSource, c *mediaCollector) error {
	c.doc.Format = "FLAC"
	if _, err := s.bytes(4); err != nil {
		return err
	}
	for {
		h, err := s.bytes(4)
		if err != nil {
			return err
		}
		last, typ, n := h[0]&0x80 != 0, h[0]&0x7f, int64(h[1])<<16|int64(h[2])<<8|int64(h[3])
		switch typ {
		case 0, 4:
			p, err := s.bytes(n)
			if err != nil {
				return err
			}
			if typ == 4 {
				vorbisComment(c, p)
			} else if len(p) >= 18 {
				rate := int(binary.BigEndian.Uint32(p[10:]) >> 12)
				ch := int(p[12]>>1&7) + 1
				bits := int(binary.BigEndian.Uint16(p[12:])>>4&31) + 1
				total := uint64(p[13]&15)<<32 | uint64(binary.BigEndian.Uint32(p[14:]))
				c.add("Audio", "Codec", "FLAC")
				c.add("Audio", "Sample rate", sampleRate(rate))
				c.add("Audio", "Channels", channels(ch))
				c.add("Audio", "Bit depth", strconv.Itoa(bits)+" bits")
				if rate > 0 && total > 0 {
					c.doc.Duration = math.Round(float64(total)/float64(rate)*1000) / 1000
				}
			}
		case 6:
			c.add("Song", "Cover art", "Picture ("+fmtSize(int(n))+")")
			if err := s.skip(n); err != nil {
				return err
			}
		default:
			if err := s.skip(n); err != nil {
				return err
			}
		}
		if last {
			return nil
		}
	}
}

// vorbisComment reads a Vorbis comment block (FLAC's, Ogg Vorbis' and
// Opus'): a vendor string, then KEY=value pairs.
func vorbisComment(c *mediaCollector, p []byte) {
	if len(p) < 8 {
		return
	}
	vl := int(binary.LittleEndian.Uint32(p))
	if 4+vl+4 > len(p) {
		return
	}
	c.tag("Vorbis", "Vendor", validUTF8(p[4:4+vl]))
	q := p[4+vl:]
	n := int(binary.LittleEndian.Uint32(q))
	q = q[4:]
	for i := 0; i < n && len(q) >= 4; i++ {
		l := int(binary.LittleEndian.Uint32(q))
		if l > len(q)-4 {
			return
		}
		kv := q[4 : 4+l]
		q = q[4+l:]
		k, v, ok := bytes.Cut(kv, []byte("="))
		if !ok {
			continue
		}
		key := strings.ToUpper(string(k))
		val := validUTF8(v)
		if key == "METADATA_BLOCK_PICTURE" {
			c.add("Song", "Cover art", "Picture")
			continue
		}
		c.tag("Vorbis", key, val)
		switch key {
		case "TITLE":
			c.add("Song", "Title", val)
		case "ARTIST":
			c.add("Song", "Artist", val)
		case "ALBUMARTIST", "ALBUM ARTIST":
			c.add("Song", "Album artist", val)
		case "ALBUM":
			c.add("Song", "Album", val)
		case "TRACKNUMBER":
			c.add("Song", "Track", val)
		case "DISCNUMBER":
			c.add("Song", "Disc", val)
		case "DATE", "YEAR":
			c.add("Song", "Year", val)
			c.taken(val)
		case "GENRE":
			c.add("Song", "Genre", val)
		case "COMPOSER":
			c.add("Song", "Composer", val)
		case "LYRICS", "UNSYNCEDLYRICS", "UNSYNCED LYRICS":
			c.text("Lyrics", "", val, false)
		case "COMMENT", "DESCRIPTION":
			c.text("Comment", "", val, false)
		case "ENCODER", "ENCODED-BY":
			c.add("Description", "Encoder", val)
		case "COPYRIGHT":
			c.add("Description", "Copyright", val)
		}
	}
}

// parseOgg reads an Ogg stream's first two packets: what codec it is,
// and its comments. The length is the last page's position, read from
// the end when the file can seek.
func parseOgg(s *mediaSource, c *mediaCollector) error {
	c.doc.Format = "Ogg"
	var packets [][]byte
	var cur []byte
	var serial uint32
	rate, preskip := 0, 0
	for pages := 0; len(packets) < 2 && pages < 500; pages++ {
		h, err := s.bytes(27)
		if err != nil || string(h[:4]) != "OggS" {
			break
		}
		ns := int(h[26])
		segs, err := s.bytes(int64(ns))
		if err != nil {
			return err
		}
		total := 0
		for _, l := range segs {
			total += int(l)
		}
		body, err := s.bytes(int64(total))
		if err != nil {
			return err
		}
		sn := binary.LittleEndian.Uint32(h[14:])
		if pages == 0 {
			serial = sn
		} else if sn != serial {
			continue
		}
		off := 0
		for _, l := range segs {
			cur = append(cur, body[off:off+int(l)]...)
			off += int(l)
			if l < 255 {
				packets = append(packets, cur)
				cur = nil
			}
		}
		if len(cur) > 4<<20 {
			break
		}
	}
	if len(packets) == 0 {
		return nil
	}
	id := packets[0]
	switch {
	case bytes.HasPrefix(id, []byte("\x01vorbis")) && len(id) >= 16:
		c.add("Audio", "Codec", "Vorbis")
		c.add("Audio", "Channels", channels(int(id[11])))
		rate = int(binary.LittleEndian.Uint32(id[12:]))
		if len(id) >= 24 {
			if br := int(int32(binary.LittleEndian.Uint32(id[20:]))); br > 0 {
				c.add("Audio", "Bit rate", strconv.Itoa(br/1000)+" kb/s")
			}
		}
		if len(packets) > 1 && bytes.HasPrefix(packets[1], []byte("\x03vorbis")) {
			vorbisComment(c, packets[1][7:])
		}
	case bytes.HasPrefix(id, []byte("OpusHead")) && len(id) >= 16:
		c.add("Audio", "Codec", "Opus")
		c.add("Audio", "Channels", channels(int(id[9])))
		preskip = int(binary.LittleEndian.Uint16(id[10:]))
		c.add("Audio", "Sample rate", sampleRate(int(binary.LittleEndian.Uint32(id[12:])))+" (input)")
		rate = 48000 // Opus' granule positions are at 48 kHz, whatever the input
		if len(packets) > 1 && bytes.HasPrefix(packets[1], []byte("OpusTags")) {
			vorbisComment(c, packets[1][8:])
		}
	case bytes.HasPrefix(id, []byte("\x7fFLAC")):
		c.add("Audio", "Codec", "FLAC")
		if len(packets) > 1 && len(packets[1]) > 4 {
			vorbisComment(c, packets[1][4:])
		}
	}
	if c.doc.Kind == "audio" && id != nil && bytes.HasPrefix(id, []byte("\x01vorbis")) && rate > 0 {
		c.add("Audio", "Sample rate", sampleRate(rate))
	}
	// The last page's granule position is the stream's length in samples.
	if s.seeker != nil && rate > 0 && s.size > 27 {
		back := min(s.size, 64<<10)
		if err := s.seek(s.size - back); err == nil {
			if tail, err := s.bytes(back); err == nil {
				if i := bytes.LastIndex(tail, []byte("OggS")); i >= 0 && i+14 <= len(tail) {
					g := int64(binary.LittleEndian.Uint64(tail[i+6:]))
					if d := float64(g-int64(preskip)) / float64(rate); g > 0 && d > 0 {
						c.doc.Duration = math.Round(d*1000) / 1000
					}
				}
			}
		}
	}
	return nil
}

func sampleRate(hz int) string {
	return formatFloat(float64(hz)/1000, 3) + " kHz"
}

func channels(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "Mono"
	case 2:
		return "Stereo"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	}
	return strconv.Itoa(n) + " channels"
}

// ---- ffmpeg's description of a video ----

// applyProbe adds what ffmpeg says about a video (or a song) to what was
// read from the file itself, which it fills in and, for the streams,
// describes better.
func (c *mediaCollector) applyProbe(p *ffmpeg.Info) {
	if c.doc.Duration == 0 && p.Duration > 0 {
		c.doc.Duration = math.Round(p.Duration.Seconds()*1000) / 1000
	}
	c.size(p.Width, p.Height)
	if p.Video != "" {
		c.set("Video", "Codec", p.Video)
	}
	if p.FPS > 0 {
		c.add("Video", "Frame rate", formatFloat(p.FPS, 3)+" fps")
	}
	if p.Audio != "" {
		c.set("Audio", "Codec", p.Audio)
	}
	if p.Bitrate > 0 {
		group := "Video"
		if c.doc.Kind == "audio" {
			group = "Audio"
		}
		c.add(group, "Bit rate", strconv.Itoa(p.Bitrate)+" kb/s")
	}
	keys := make([]string, 0, len(p.Tags))
	for k := range p.Tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	song := "Description"
	if c.doc.Kind == "audio" {
		song = "Song"
	}
	for _, k := range keys {
		v := p.Tags[k]
		c.tag("Container", k, v)
		switch {
		case k == "title":
			c.add(song, "Title", v)
		case k == "artist":
			c.add("Song", "Artist", v)
		case k == "album":
			c.add("Song", "Album", v)
		case k == "genre":
			c.add("Song", "Genre", v)
		case k == "lyrics" || strings.HasPrefix(k, "lyrics-") || k == "unsyncedlyrics":
			c.text("Lyrics", strings.TrimPrefix(k, "lyrics-"), v, false)
		case k == "comment" || k == "description" || k == "synopsis":
			c.text(strings.ToUpper(k[:1])+k[1:], "", v, false)
		case k == "creation_time" || k == "date" || k == "com.apple.quicktime.creationdate":
			if t := xmpTime(isoOffset(v)); t != "" {
				if strings.HasSuffix(v, "Z") || strings.HasSuffix(v, "000000Z") {
					t = strings.TrimSuffix(t, "Z") + "Z"
				}
				c.taken(t)
			}
		case k == "location" || k == "com.apple.quicktime.location.iso6709":
			c.location(iso6709(v))
		case k == "make" || k == "com.apple.quicktime.make":
			if c.mp4Make == "" {
				c.mp4Make = v
			}
		case k == "model" || k == "com.apple.quicktime.model":
			if c.mp4Model == "" {
				c.mp4Model = v
			}
		case k == "encoder" || k == "com.apple.quicktime.software":
			c.add("Description", "Encoder", v)
		case k == "copyright":
			c.add("Description", "Copyright", v)
		}
	}
}

// set replaces a detail, or adds it.
func (c *mediaCollector) set(group, label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	for i, f := range c.fields[group] {
		if f.Label == label {
			c.fields[group][i].Value = truncateRunes(value, mediaTagMax)
			return
		}
	}
	c.add(group, label, value)
}

// finishMP4 puts what an MP4 says that's spread over several tags
// together: the camera, and the date recorded when nothing better says.
func (c *mediaCollector) finishMP4() {
	switch {
	case c.mp4Model != "" && c.mp4Make != "" && !strings.HasPrefix(strings.ToLower(c.mp4Model), strings.ToLower(c.mp4Make)):
		c.add("Camera", "Camera", c.mp4Make+" "+c.mp4Model)
	default:
		c.add("Camera", "Camera", firstOf(c.mp4Model, c.mp4Make))
	}
	if c.mp4Created != "" {
		c.taken(c.mp4Created)
	}
}
