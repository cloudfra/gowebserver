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
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"sort"
	"testing"
)

// Files with metadata for the ?info tests, made here byte by byte, so
// each test says exactly what its file holds.

// tiffField is a TIFF tag with its value's bytes, little-endian.
type tiffField struct {
	tag, typ uint16
	count    uint32
	data     []byte
}

func tASCII(tag uint16, s string) tiffField {
	return tiffField{tag, 2, uint32(len(s) + 1), append([]byte(s), 0)}
}

func tShort(tag uint16, v ...uint16) tiffField {
	b := make([]byte, 2*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint16(b[2*i:], x)
	}
	return tiffField{tag, 3, uint32(len(v)), b}
}

func tLong(tag uint16, v ...uint32) tiffField {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], x)
	}
	return tiffField{tag, 4, uint32(len(v)), b}
}

// tRational takes numerator, denominator pairs.
func tRational(tag uint16, v ...uint32) tiffField {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], x)
	}
	return tiffField{tag, 5, uint32(len(v) / 2), b}
}

func tSRational(tag uint16, n, d int32) tiffField {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint32(b, uint32(n))
	binary.LittleEndian.PutUint32(b[4:], uint32(d))
	return tiffField{tag, 10, 1, b}
}

func tUndefined(tag uint16, b []byte) tiffField {
	return tiffField{tag, 7, uint32(len(b)), b}
}

func tByte(tag uint16, v ...byte) tiffField {
	return tiffField{tag, 1, uint32(len(v)), v}
}

// buildTIFF lays out a little-endian TIFF structure: IFD0, and the EXIF
// and GPS IFDs when they have fields, pointed to from IFD0.
func buildTIFF(ifd0, exif, gps []tiffField) []byte {
	ifds := [][]tiffField{append([]tiffField(nil), ifd0...)}
	if len(exif) > 0 {
		ifds[0] = append(ifds[0], tLong(0x8769, 0))
		ifds = append(ifds, exif)
	}
	if len(gps) > 0 {
		ifds[0] = append(ifds[0], tLong(0x8825, 0))
		ifds = append(ifds, gps)
	}
	size := func(fs []tiffField) int {
		n := 2 + 12*len(fs) + 4
		for _, f := range fs {
			if len(f.data) > 4 {
				n += len(f.data) + len(f.data)%2
			}
		}
		return n
	}
	offs := make([]int, 1, 1+len(ifds))
	offs[0] = 8
	for i := range ifds {
		sort.Slice(ifds[i], func(a, b int) bool { return ifds[i][a].tag < ifds[i][b].tag })
		offs = append(offs, offs[i]+size(ifds[i]))
	}
	for i, f := range ifds[0] {
		switch f.tag {
		case 0x8769:
			ifds[0][i] = tLong(0x8769, uint32(offs[1]))
		case 0x8825:
			ifds[0][i] = tLong(0x8825, uint32(offs[len(ifds)-1]))
		}
	}
	out := []byte("II*\x00\x08\x00\x00\x00")
	for i, fs := range ifds {
		data := offs[i] + 2 + 12*len(fs) + 4
		var tail []byte
		out = binary.LittleEndian.AppendUint16(out, uint16(len(fs)))
		for _, f := range fs {
			out = binary.LittleEndian.AppendUint16(out, f.tag)
			out = binary.LittleEndian.AppendUint16(out, f.typ)
			out = binary.LittleEndian.AppendUint32(out, f.count)
			if len(f.data) <= 4 {
				v := make([]byte, 4)
				copy(v, f.data)
				out = append(out, v...)
				continue
			}
			out = binary.LittleEndian.AppendUint32(out, uint32(data+len(tail)))
			tail = append(tail, f.data...)
			if len(f.data)%2 == 1 {
				tail = append(tail, 0)
			}
		}
		out = binary.LittleEndian.AppendUint32(out, 0)
		out = append(out, tail...)
	}
	return out
}

// photoExif is a phone's EXIF: the camera, exposure, the time taken in
// Paris, and where, by the Eiffel Tower, facing south-east.
func photoExif() []byte { return photoExifWith(6) }

// photoExifWith is photoExif with the given orientation.
func photoExifWith(orientation uint16) []byte {
	return buildTIFF(
		[]tiffField{
			tASCII(0x010F, "Apple"), tASCII(0x0110, "iPhone 15 Pro"), tShort(0x0112, orientation),
			tRational(0x011A, 72, 1), tRational(0x011B, 72, 1), tShort(0x0128, 2),
			tASCII(0x0131, "17.4"), tASCII(0x0132, "2024:05:01 18:45:10"), tASCII(0x010E, "OLYMPUS DIGITAL CAMERA"),
		},
		[]tiffField{
			tRational(0x829A, 1, 250), tRational(0x829D, 178, 100), tShort(0x8822, 2), tShort(0x8827, 64),
			tASCII(0x9003, "2024:05:01 18:45:10"), tASCII(0x9011, "+02:00"), tSRational(0x9204, -1, 3),
			tShort(0x9207, 5), tShort(0x9209, 16), tRational(0x920A, 678, 100), tShort(0xA001, 0xFFFF),
			tShort(0xA403, 0), tShort(0xA405, 24), tASCII(0xA433, "Apple"),
			tASCII(0xA434, "iPhone 15 Pro back triple camera 6.765mm f/1.78"),
			tRational(0xA432, 2220, 1000, 9000, 1000, 178, 100, 280, 100),
			tUndefined(0x9286, append([]byte("ASCII\x00\x00\x00"), "Sunset from the Trocadéro"...)),
			tUndefined(0x927C, bytes.Repeat([]byte{1}, 40)),
		},
		[]tiffField{
			tByte(0x00, 2, 2, 0, 0), tASCII(0x01, "N"), tRational(0x02, 48, 1, 51, 1, 3024, 100),
			tASCII(0x03, "E"), tRational(0x04, 2, 1, 17, 1, 4020, 100), tByte(0x05, 0),
			tRational(0x06, 351, 10), tASCII(0x10, "T"), tRational(0x11, 13525, 100), tRational(0x1F, 5, 1),
		},
	)
}

const photoXMP = `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
<rdf:Description rdf:about="" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:xmp="http://ns.adobe.com/xap/1.0/"
 xmlns:photoshop="http://ns.adobe.com/photoshop/1.0/" xmlns:Iptc4xmpCore="http://iptc.org/std/Iptc4xmpCore/1.0/xmlns/" xmp:Rating="4"
 photoshop:City="Paris" photoshop:Country="France" Iptc4xmpCore:Location="Eiffel Tower">
<dc:title><rdf:Alt><rdf:li xml:lang="x-default">Evening at the tower</rdf:li></rdf:Alt></dc:title>
<dc:subject><rdf:Bag><rdf:li>paris</rdf:li><rdf:li>sunset</rdf:li></rdf:Bag></dc:subject>
<dc:description><rdf:Alt><rdf:li xml:lang="x-default">The tower lights up as the sun goes down.</rdf:li></rdf:Alt></dc:description>
</rdf:Description></rdf:RDF></x:xmpmeta><?xpacket end="w"?>`

// testICC is a small ICC v2 display profile: its description and a
// gamma 2.2 tone curve.
func testICC(desc string) []byte {
	descTag := append([]byte("desc\x00\x00\x00\x00"), binary.BigEndian.AppendUint32(nil, uint32(len(desc)+1))...)
	descTag = append(descTag, desc...)
	descTag = append(descTag, 0)
	for len(descTag)%4 != 0 {
		descTag = append(descTag, 0)
	}
	curv := []byte("curv\x00\x00\x00\x00\x00\x00\x00\x01\x02\x33\x00\x00") // 0x0233/256 = 2.2
	h := make([]byte, 128, 128+4+2*12+len(descTag)+len(curv))
	copy(h[4:], "lcms")
	h[8], h[9] = 2, 0x10
	copy(h[12:], "mntrRGB XYZ ")
	binary.BigEndian.PutUint16(h[24:], 2024)
	binary.BigEndian.PutUint16(h[26:], 3)
	binary.BigEndian.PutUint16(h[28:], 9)
	copy(h[36:], "acspAPPL")
	table := binary.BigEndian.AppendUint32(nil, 2)
	off := 128 + 4 + 2*12
	table = append(table, "desc"...)
	table = binary.BigEndian.AppendUint32(table, uint32(off))
	table = binary.BigEndian.AppendUint32(table, uint32(len(descTag)))
	table = append(table, "rTRC"...)
	table = binary.BigEndian.AppendUint32(table, uint32(off+len(descTag)))
	table = binary.BigEndian.AppendUint32(table, uint32(len(curv)))
	out := append(append(append(h, table...), descTag...), curv...)
	binary.BigEndian.PutUint32(out, uint32(len(out)))
	return out
}

// testPicture is a sunset-like gradient, w by h.
func testPicture(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		t := float64(y) / float64(h)
		for x := 0; x < w; x++ {
			u := float64(x) / float64(w)
			glow := math.Exp(-((u-0.62)*(u-0.62)*9 + (t-0.58)*(t-0.58)*30))
			r := 40 + 200*t + 15*glow
			g := 50 + 90*t*t + 120*glow
			b := 120 - 60*t + 40*glow
			if t > 0.66 {
				r, g, b = r*0.35, g*0.3, b*0.4 // the city below the horizon
			}
			img.Set(x, y, color.RGBA{clamp8(r), clamp8(g), clamp8(b), 255})
		}
	}
	return img
}

func clamp8(v float64) uint8 {
	return uint8(math.Max(0, math.Min(255, v)))
}

func jpegSegment(marker byte, payload []byte) []byte {
	out := []byte{0xFF, marker}
	out = binary.BigEndian.AppendUint16(out, uint16(len(payload)+2))
	return append(out, payload...)
}

// testJPEG is a w by h JPEG with the given EXIF, XMP and ICC profile
// (each left out when nil), as a camera writes them: after the start.
func testJPEG(t testing.TB, w, h int, exif []byte, xmp string, icc []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, testPicture(w, h), &jpeg.Options{Quality: 82}); err != nil {
		t.Fatal(err)
	}
	var segs []byte
	if exif != nil {
		segs = append(segs, jpegSegment(0xE1, append([]byte("Exif\x00\x00"), exif...))...)
	}
	if xmp != "" {
		segs = append(segs, jpegSegment(0xE1, append([]byte("http://ns.adobe.com/xap/1.0/\x00"), xmp...))...)
	}
	if icc != nil {
		// In two chunks, to show they're joined.
		half := len(icc) / 2
		segs = append(segs, jpegSegment(0xE2, append([]byte("ICC_PROFILE\x00\x01\x02"), icc[:half]...))...)
		segs = append(segs, jpegSegment(0xE2, append([]byte("ICC_PROFILE\x00\x02\x02"), icc[half:]...))...)
	}
	b := buf.Bytes()
	return append(append(append([]byte{}, b[:2]...), segs...), b[2:]...)
}

func pngChunkBytes(typ string, data []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	body := append([]byte(typ), data...)
	out = append(out, body...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(body))
}

// testPNG is a PNG with the given chunks after its header.
func testPNG(t testing.TB, chunks ...[]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, testPicture(40, 30)); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	ihdrEnd := 8 + 8 + 13 + 4
	out := append([]byte{}, b[:ihdrEnd]...)
	for _, c := range chunks {
		out = append(out, c...)
	}
	return append(out, b[ihdrEnd:]...)
}

// isoBox writes an ISO base media box.
func isoBoxBytes(typ string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
	return append(append(out, typ...), body...)
}

func u16(v uint16) []byte { return binary.BigEndian.AppendUint16(nil, v) }
func u32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

// ilstItem is an iTunes metadata item holding UTF-8 text.
func ilstItem(name, text string) []byte {
	return isoBoxBytes(name, isoBoxBytes("data", u32(1), u32(0), []byte(text)))
}

// testMP4 is a movie's boxes, as a phone writes them, with its media
// data (mdatSize bytes) first, so its moov is at the end.
func testMP4(mdatSize int) []byte {
	mvhd := isoBoxBytes("mvhd", u32(0), u32(3797481600), u32(3797481600), u32(600), u32(600*95/10), make([]byte, 80))
	matrix := bytes.Join([][]byte{u32(0), u32(0x10000), u32(0), u32(0xFFFF0000), u32(0), u32(0), u32(0), u32(0), u32(0x40000000)}, nil)
	tkhd := isoBoxBytes("tkhd", u32(3), u32(0), u32(0), u32(1), u32(0), u32(0), make([]byte, 8), make([]byte, 8), matrix, u32(1920<<16), u32(1080<<16))
	mdhd := isoBoxBytes("mdhd", u32(0), u32(0), u32(0), u32(30000), u32(285000), u16(0x55c4), u16(0))
	hdlr := isoBoxBytes("hdlr", u32(0), u32(0), []byte("vide"), make([]byte, 12), []byte("VideoHandler\x00"))
	avc1 := isoBoxBytes("avc1", make([]byte, 6), u16(1), make([]byte, 16), u16(1920), u16(1080), make([]byte, 50))
	stsd := isoBoxBytes("stsd", u32(0), u32(1), avc1)
	stts := isoBoxBytes("stts", u32(0), u32(1), u32(285), u32(1000))
	trak := isoBoxBytes("trak", tkhd, isoBoxBytes("mdia", mdhd, hdlr, isoBoxBytes("minf", isoBoxBytes("stbl", stsd, stts))))
	xyz := isoBoxBytes("\xa9xyz", u16(18), u16(0x15c7), []byte("+35.6586+139.7454/"))
	ilst := isoBoxBytes("ilst", ilstItem("\xa9nam", "Night walk"), ilstItem("\xa9lyr", "Neon on the water\nTrains in the rain"), ilstItem("\xa9too", "Lavf61.1.100"))
	meta := isoBoxBytes("meta", u32(0), isoBoxBytes("hdlr", u32(0), u32(0), []byte("mdirappl"), make([]byte, 9)), ilst)
	udta := isoBoxBytes("udta", xyz, meta)
	moov := isoBoxBytes("moov", mvhd, trak, udta)
	ftyp := isoBoxBytes("ftyp", []byte("isom"), u32(512), []byte("isomiso2avc1mp41"))
	return append(append(ftyp, isoBoxBytes("mdat", make([]byte, mdatSize))...), moov...)
}

// testHEIF is a HEIF photo: an HEVC item with its size, bit depth and
// Display P3 color, and an EXIF item in the media data.
func testHEIF(exif []byte) []byte {
	ftyp := isoBoxBytes("ftyp", []byte("heic"), u32(0), []byte("mif1heic"))
	infe := func(id uint16, typ string) []byte {
		return isoBoxBytes("infe", []byte{2, 0, 0, 0}, u16(id), u16(0), []byte(typ), []byte("\x00"))
	}
	iinf := isoBoxBytes("iinf", u32(0), u16(2), infe(1, "hvc1"), infe(2, "Exif"))
	ispe := isoBoxBytes("ispe", u32(0), u32(4032), u32(3024))
	pixi := isoBoxBytes("pixi", u32(0), []byte{3, 10, 10, 10})
	colr := isoBoxBytes("colr", []byte("nclx"), u16(12), u16(13), u16(6), []byte{0x80})
	ipco := isoBoxBytes("ipco", ispe, pixi, colr)
	ipma := isoBoxBytes("ipma", u32(0), u32(1), u16(1), []byte{3, 0x81, 0x02, 0x83})
	item := append(u32(6), append([]byte("Exif\x00\x00"), exif...)...)
	build := func(off uint32) []byte {
		iloc := isoBoxBytes("iloc", u32(0), []byte{0x44, 0x00}, u16(1), u16(2), u16(0), u16(1), u32(off), u32(uint32(len(item))))
		meta := isoBoxBytes("meta", u32(0), isoBoxBytes("hdlr", u32(0), u32(0), []byte("pict"), make([]byte, 13)),
			isoBoxBytes("pitm", u32(0), u16(1)), iinf, iloc, isoBoxBytes("iprp", ipco, ipma))
		return append(append([]byte{}, ftyp...), meta...)
	}
	head := build(0)
	head = build(uint32(len(head) + 8))
	return append(head, isoBoxBytes("mdat", item)...)
}

// id3Frame24 is an ID3v2.4 frame.
func id3Frame24(id string, data []byte) []byte {
	n := len(data)
	out := append([]byte(id), byte(n>>21&0x7f), byte(n>>14&0x7f), byte(n>>7&0x7f), byte(n&0x7f), 0, 0)
	return append(out, data...)
}

// testMP3 is an MP3 with an ID3v2.4 tag: title, artist, album, year,
// lyrics (USLT), synced lyrics (SYLT) and cover art, then a few frames
// of 128 kb/s, 44.1 kHz joint stereo.
func testMP3() []byte {
	sylt := append([]byte{3}, "eng"...)
	sylt = append(sylt, 2, 1, 0) // milliseconds, lyrics, no descriptor
	sylt = append(sylt, "First line\x00"...)
	sylt = append(sylt, u32(1500)...)
	sylt = append(sylt, "Second line\x00"...)
	sylt = append(sylt, u32(65250)...)
	frames := bytes.Join([][]byte{
		id3Frame24("TIT2", append([]byte{3}, "Harbor Lights"...)),
		id3Frame24("TPE1", append([]byte{3}, "The Example Band"...)),
		id3Frame24("TALB", append([]byte{3}, "Test Tones"...)),
		id3Frame24("TDRC", append([]byte{3}, "2019"...)),
		id3Frame24("TCON", append([]byte{3}, "(17)"...)),
		id3Frame24("TRCK", append([]byte{3}, "3/12"...)),
		id3Frame24("USLT", append(append([]byte{3}, "eng\x00"...), "Lights on the harbor\nShips on the sea"...)),
		id3Frame24("SYLT", sylt),
		id3Frame24("APIC", append(append([]byte{0}, "image/jpeg\x00\x03\x00"...), make([]byte, 2000)...)),
	}, nil)
	n := len(frames)
	out := append([]byte("ID3\x04\x00\x00"), byte(n>>21&0x7f), byte(n>>14&0x7f), byte(n>>7&0x7f), byte(n&0x7f))
	out = append(out, frames...)
	for i := 0; i < 20; i++ {
		frame := make([]byte, 417)
		copy(frame, []byte{0xFF, 0xFB, 0x90, 0x44})
		out = append(out, frame...)
	}
	return out
}

func vorbisCommentBytes(vendor string, kv ...string) []byte {
	out := binary.LittleEndian.AppendUint32(nil, uint32(len(vendor)))
	out = append(out, vendor...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(kv)))
	for _, s := range kv {
		out = binary.LittleEndian.AppendUint32(out, uint32(len(s)))
		out = append(out, s...)
	}
	return out
}

// testFLAC is a FLAC's metadata: 3 seconds at 48 kHz, 24-bit stereo,
// with lyrics in its Vorbis comment.
func testFLAC() []byte {
	si := make([]byte, 34)
	binary.BigEndian.PutUint16(si, 4096)
	binary.BigEndian.PutUint16(si[2:], 4096)
	// 20 bits of rate, 3 of channels-1, 5 of bits-1, 36 of samples.
	v := uint64(48000)<<44 | uint64(1)<<41 | uint64(23)<<36 | uint64(144000)
	binary.BigEndian.PutUint64(si[10:], v)
	vc := vorbisCommentBytes("reference libFLAC 1.4.3", "TITLE=Quiet Tide", "ARTIST=Example Ensemble", "DATE=2021", "LYRICS=Low tide\nhigh moon")
	out := []byte("fLaC")
	out = append(out, 0, 0, 0, 34)
	out = append(out, si...)
	out = append(out, 0x84, byte(len(vc)>>16), byte(len(vc)>>8), byte(len(vc)))
	return append(out, vc...)
}

// oggPage is an Ogg page holding one packet.
func oggPage(serial, seq uint32, granule uint64, packet []byte) []byte {
	var segs []byte
	n := len(packet)
	for n >= 255 {
		segs = append(segs, 255)
		n -= 255
	}
	segs = append(segs, byte(n))
	h := []byte("OggS\x00\x00")
	h = binary.LittleEndian.AppendUint64(h, granule)
	h = binary.LittleEndian.AppendUint32(h, serial)
	h = binary.LittleEndian.AppendUint32(h, seq)
	h = append(h, 0, 0, 0, 0, byte(len(segs)))
	return append(append(h, segs...), packet...)
}

// testOpus is an Ogg Opus stream: 2 seconds, stereo, with lyrics.
func testOpus() []byte {
	head := append([]byte("OpusHead\x01\x02"), 0x38, 0x01)
	head = binary.LittleEndian.AppendUint32(head, 44100)
	head = append(head, 0, 0, 0)
	tags := append([]byte("OpusTags"), vorbisCommentBytes("libopus 1.4", "TITLE=Morning", "LYRICS=Sun comes up")...)
	out := append(oggPage(7, 0, 0, head), oggPage(7, 1, 0, tags)...)
	return append(out, oggPage(7, 2, 96000+312, make([]byte, 100))...)
}
