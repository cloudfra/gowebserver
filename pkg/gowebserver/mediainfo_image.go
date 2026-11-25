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
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// A photo's metadata, for mediainfo.go: the containers (JPEG, PNG, WebP,
// TIFF) hand their EXIF, XMP and ICC profile to the collector, and say
// what they know themselves (size, bit depth, color model);
// applyEmbedded then reads the three.

// ---- JPEG ----

func parseJPEG(s *mediaSource, c *mediaCollector) error {
	c.doc.Format = "JPEG"
	var icc [][]byte
	if _, err := s.bytes(2); err != nil {
		return err
	}
	for {
		b, err := s.bytes(2)
		if err != nil {
			return err
		}
		// Fill bytes (0xFF) may come before a marker.
		for b[0] == 0xFF && b[1] == 0xFF {
			n, err := s.bytes(1)
			if err != nil {
				return err
			}
			b[1] = n[0]
		}
		if b[0] != 0xFF {
			return fmt.Errorf("mediainfo: a JPEG marker was expected at %d", s.pos-2)
		}
		m := b[1]
		if m == 0xD8 || m == 0x01 || (m >= 0xD0 && m <= 0xD7) {
			continue
		}
		if m == 0xDA || m == 0xD9 {
			break // the picture itself: the metadata comes before it
		}
		lb, err := s.bytes(2)
		if err != nil {
			return err
		}
		n := int64(binary.BigEndian.Uint16(lb)) - 2
		if n < 0 {
			return fmt.Errorf("mediainfo: a JPEG segment is too short")
		}
		switch {
		case m == 0xE0 || m == 0xE1 || m == 0xE2 || m == 0xEE || (m >= 0xC0 && m <= 0xCF && m != 0xC4 && m != 0xC8 && m != 0xCC):
			p, err := s.bytes(n)
			if err != nil {
				return err
			}
			switch {
			case m == 0xE1 && bytes.HasPrefix(p, []byte("Exif\x00")) && len(p) > 6:
				if c.exif == nil {
					c.exif = p[6:]
				}
			case m == 0xE1 && bytes.HasPrefix(p, []byte("http://ns.adobe.com/xap/1.0/\x00")):
				if c.xmp == nil {
					c.xmp = p[29:]
				}
			case m == 0xE2 && bytes.HasPrefix(p, []byte("ICC_PROFILE\x00")) && len(p) > 14:
				// Chunks are numbered from 1; they're joined in order.
				seq := int(p[12])
				for len(icc) < seq {
					icc = append(icc, nil)
				}
				if seq > 0 {
					icc[seq-1] = p[14:]
				}
			case m == 0xE0 && bytes.HasPrefix(p, []byte("JFIF\x00")) && len(p) >= 12:
				c.tag("JFIF", "Version", fmt.Sprintf("%d.%02d", p[5], p[6]))
				if x, y := binary.BigEndian.Uint16(p[8:]), binary.BigEndian.Uint16(p[10:]); p[7] != 0 && x > 0 {
					unit := map[byte]string{1: "dpi", 2: "dots per cm"}[p[7]]
					c.add("Image", "Resolution", resolution(float64(x), float64(y), unit))
				}
			case m == 0xEE && bytes.HasPrefix(p, []byte("Adobe")) && len(p) >= 12:
				c.tag("Adobe", "Transform", map[byte]string{0: "RGB or CMYK", 1: "YCbCr", 2: "YCCK"}[p[11]])
			case m >= 0xC0 && len(p) >= 6:
				jpegFrame(c, m, p)
			}
		default:
			if err := s.skip(n); err != nil {
				return err
			}
		}
	}
	if len(icc) > 0 {
		c.icc = bytes.Join(icc, nil)
	}
	return nil
}

// jpegFrame reads a start of frame segment: the picture's size, bit
// depth and color components.
func jpegFrame(c *mediaCollector, m byte, p []byte) {
	h, w, n := int(binary.BigEndian.Uint16(p[1:])), int(binary.BigEndian.Uint16(p[3:])), int(p[5])
	c.size(w, h)
	c.add("Image", "Bit depth", fmt.Sprintf("%d bits per channel", p[0]))
	kind := "Baseline"
	switch m {
	case 0xC2, 0xC6, 0xCA, 0xCE:
		kind = "Progressive"
	case 0xC3, 0xC7, 0xCB, 0xCF:
		kind = "Lossless"
	}
	c.add("Image", "Compression", "JPEG, "+strings.ToLower(kind))
	model := map[int]string{1: "Grayscale", 3: "YCbCr", 4: "CMYK"}[n]
	if n == 3 && len(p) >= 6+3*3 {
		hy, vy := int(p[7]>>4), int(p[7]&15)
		hc, vc := int(p[10]>>4), int(p[10]&15)
		if hc > 0 && vc > 0 {
			switch {
			case hy == hc && vy == vc:
				model += " 4:4:4"
			case hy == 2*hc && vy == 2*vc:
				model += " 4:2:0"
			case hy == 2*hc && vy == vc:
				model += " 4:2:2"
			case hy == 4*hc && vy == vc:
				model += " 4:1:1"
			case hy == hc && vy == 2*vc:
				model += " 4:4:0"
			}
		}
	}
	c.add("Image", "Color model", model)
}

// ---- PNG ----

func parsePNG(s *mediaSource, c *mediaCollector) error {
	c.doc.Format = "PNG"
	if _, err := s.bytes(8); err != nil {
		return err
	}
	for {
		h, err := s.bytes(8)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		n, typ := int64(binary.BigEndian.Uint32(h)), string(h[4:8])
		switch typ {
		case "IDAT", "IEND":
			// The picture: what's written after it is rarely metadata.
			return nil
		case "IHDR", "gAMA", "sRGB", "iCCP", "cICP", "pHYs", "eXIf", "tEXt", "zTXt", "iTXt", "acTL", "cHRM":
			if n > 8<<20 {
				if err := s.skip(n + 4); err != nil {
					return err
				}
				continue
			}
			p, err := s.bytes(n)
			if err != nil {
				return err
			}
			pngChunk(c, typ, p)
			if err := s.skip(4); err != nil {
				return err
			}
		default:
			if err := s.skip(n + 4); err != nil {
				return err
			}
		}
	}
}

func pngChunk(c *mediaCollector, typ string, p []byte) {
	switch typ {
	case "IHDR":
		if len(p) < 13 {
			return
		}
		c.size(int(binary.BigEndian.Uint32(p)), int(binary.BigEndian.Uint32(p[4:])))
		c.add("Image", "Bit depth", fmt.Sprintf("%d bits per channel", p[8]))
		c.add("Image", "Color model", map[byte]string{0: "Grayscale", 2: "RGB", 3: "Indexed color", 4: "Grayscale with alpha", 6: "RGBA"}[p[9]])
		if p[12] == 1 {
			c.add("Image", "Compression", "Deflate, interlaced (Adam7)")
		} else {
			c.add("Image", "Compression", "Deflate")
		}
	case "gAMA":
		if len(p) >= 4 {
			if g := float64(binary.BigEndian.Uint32(p)) / 100000; g > 0 {
				c.add("Image", "Gamma", formatFloat(1/g, 2))
				c.tag("PNG", "gAMA", formatFloat(g, 5))
			}
		}
	case "sRGB":
		c.add("Image", "Color space", "sRGB")
		if len(p) >= 1 {
			c.tag("PNG", "sRGB rendering intent", map[byte]string{0: "Perceptual", 1: "Relative colorimetric", 2: "Saturation", 3: "Absolute colorimetric"}[p[0]])
		}
	case "cICP":
		if len(p) >= 4 {
			cicp(c, int(p[0]), int(p[1]), int(p[2]), p[3] == 1)
		}
	case "iCCP":
		name, rest, ok := bytes.Cut(p, []byte{0})
		if !ok || len(rest) < 1 {
			return
		}
		c.tag("PNG", "ICC profile name", latin1(name))
		if data, err := inflate(rest[1:], 4<<20); err == nil {
			c.icc = data
		}
	case "pHYs":
		if len(p) >= 9 && p[8] == 1 {
			c.add("Image", "Resolution", resolution(float64(binary.BigEndian.Uint32(p))*0.0254, float64(binary.BigEndian.Uint32(p[4:]))*0.0254, "dpi"))
		}
	case "eXIf":
		c.exif = p
	case "acTL":
		if len(p) >= 4 {
			c.add("Image", "Frames", strconv.FormatUint(uint64(binary.BigEndian.Uint32(p)), 10)+" (animated)")
		}
	case "cHRM":
		if len(p) >= 32 {
			var v []string
			for i := 0; i < 32; i += 4 {
				v = append(v, formatFloat(float64(binary.BigEndian.Uint32(p[i:]))/100000, 4))
			}
			c.tag("PNG", "cHRM", strings.Join(v, ", "))
		}
	case "tEXt", "zTXt", "iTXt":
		key, val := pngText(typ, p)
		if key == "" {
			return
		}
		if key == "XML:com.adobe.xmp" {
			if c.xmp == nil {
				c.xmp = []byte(val)
			}
			return
		}
		c.tag("PNG", key, val)
		switch strings.ToLower(key) {
		case "description", "comment":
			c.text(key, "", val, false)
		case "title":
			c.add("Description", "Title", val)
		case "author":
			c.add("Description", "Creator", val)
		case "copyright":
			c.add("Description", "Copyright", val)
		case "software":
			c.add("Description", "Software", val)
		}
	}
}

// pngText reads a text chunk's keyword and text.
func pngText(typ string, p []byte) (string, string) {
	key, rest, ok := bytes.Cut(p, []byte{0})
	if !ok {
		return "", ""
	}
	switch typ {
	case "tEXt":
		return latin1(key), latin1(rest)
	case "zTXt":
		if len(rest) < 1 {
			return "", ""
		}
		data, err := inflate(rest[1:], mediaTextMax)
		if err != nil {
			return "", ""
		}
		return latin1(key), latin1(data)
	default: // iTXt: compressed, method, language, translated keyword, text
		if len(rest) < 2 {
			return "", ""
		}
		compressed := rest[0] == 1
		rest = rest[2:]
		_, rest, _ = bytes.Cut(rest, []byte{0})
		_, text, _ := bytes.Cut(rest, []byte{0})
		if compressed {
			data, err := inflate(text, 4<<20)
			if err != nil {
				return "", ""
			}
			text = data
		}
		return latin1(key), validUTF8(text)
	}
}

func inflate(b []byte, limit int64) ([]byte, error) {
	z, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(z, limit))
	if cerr := z.Close(); err == nil && cerr != nil && int64(len(data)) < limit {
		err = cerr
	}
	return data, err
}

// cicp names coding-independent code points (ITU-T H.273), which PNG,
// HEIF and AVIF use to say a picture is HDR.
func cicp(c *mediaCollector, primaries, transfer, matrix int, full bool) {
	p := map[int]string{1: "BT.709 (sRGB)", 9: "BT.2020", 11: "DCI-P3", 12: "Display P3"}[primaries]
	t := map[int]string{1: "BT.709", 4: "Gamma 2.2", 5: "Gamma 2.8", 8: "Linear", 13: "sRGB", 14: "BT.2020", 15: "BT.2020", 16: "PQ (SMPTE ST 2084)", 18: "HLG"}[transfer]
	if p == "" {
		p = strconv.Itoa(primaries)
	}
	if t == "" {
		t = strconv.Itoa(transfer)
	}
	c.add("Image", "Primaries", p)
	c.add("Image", "Transfer", t)
	if transfer == 16 || transfer == 18 {
		c.add("Image", "HDR", map[int]string{16: "HDR10 (PQ)", 18: "HLG"}[transfer])
	}
	switch {
	case primaries == 1 && transfer == 13:
		c.add("Image", "Color space", "sRGB")
	case primaries == 12 && transfer == 13:
		c.add("Image", "Color space", "Display P3")
	case primaries == 9:
		c.add("Image", "Color space", "BT.2020")
	}
	rng := "limited range"
	if full {
		rng = "full range"
	}
	c.tag("CICP", "Code points", fmt.Sprintf("%d/%d/%d, %s", primaries, transfer, matrix, rng))
}

// ---- WebP ----

func parseWebP(s *mediaSource, c *mediaCollector) error {
	c.doc.Format = "WebP"
	if _, err := s.bytes(12); err != nil {
		return err
	}
	frames := 0
	for {
		h, err := s.bytes(8)
		if err != nil {
			break
		}
		typ, n := string(h[:4]), int64(binary.LittleEndian.Uint32(h[4:]))
		pad := n & 1
		switch typ {
		case "VP8X", "VP8 ", "VP8L", "ICCP", "EXIF", "XMP ":
			if n > 8<<20 {
				if err := s.skip(n + pad); err != nil {
					return err
				}
				continue
			}
			p, err := s.bytes(n)
			if err != nil {
				return err
			}
			webpChunk(c, typ, p)
			if err := s.skip(pad); err != nil {
				return err
			}
		case "ANMF":
			frames++
			if err := s.skip(n + pad); err != nil {
				return err
			}
		default:
			if err := s.skip(n + pad); err != nil {
				return err
			}
		}
	}
	if frames > 1 {
		c.add("Image", "Frames", strconv.Itoa(frames)+" (animated)")
	}
	return nil
}

func webpChunk(c *mediaCollector, typ string, p []byte) {
	switch typ {
	case "VP8X":
		if len(p) >= 10 {
			w := int(uint32(p[4])|uint32(p[5])<<8|uint32(p[6])<<16) + 1
			h := int(uint32(p[7])|uint32(p[8])<<8|uint32(p[9])<<16) + 1
			c.size(w, h)
			if p[0]&0x10 != 0 {
				c.add("Image", "Color model", "RGBA")
			}
		}
	case "VP8 ":
		if len(p) >= 10 && p[3] == 0x9d && p[4] == 0x01 && p[5] == 0x2a {
			c.size(int(binary.LittleEndian.Uint16(p[6:])&0x3fff), int(binary.LittleEndian.Uint16(p[8:])&0x3fff))
		}
		c.add("Image", "Compression", "VP8, lossy")
		c.add("Image", "Color model", "YCbCr 4:2:0")
		c.add("Image", "Bit depth", "8 bits per channel")
	case "VP8L":
		if len(p) >= 5 && p[0] == 0x2f {
			v := binary.LittleEndian.Uint32(p[1:])
			c.size(int(v&0x3fff)+1, int(v>>14&0x3fff)+1)
			if v>>28&1 == 1 {
				c.add("Image", "Color model", "RGBA")
			}
		}
		c.add("Image", "Compression", "VP8L, lossless")
		c.add("Image", "Color model", "RGB")
		c.add("Image", "Bit depth", "8 bits per channel")
	case "ICCP":
		c.icc = p
	case "EXIF":
		c.exif = bytes.TrimPrefix(p, []byte("Exif\x00\x00"))
	case "XMP ":
		c.xmp = p
	}
}

// ---- TIFF (and the raw formats built on it) ----

func parseTIFFFile(s *mediaSource, c *mediaCollector) error {
	c.doc.Format = "TIFF"
	n := s.size
	if n <= 0 || n > mediaInfoReadMax {
		n = mediaInfoReadMax
	}
	b, err := s.bytes(n)
	if len(b) < 8 {
		return err
	}
	c.exif = b
	c.tiffFile = true
	return nil
}

// ---- EXIF ----

// tiffEntry is an IFD entry, its value's bytes resolved.
type tiffEntry struct {
	tag, typ uint16
	count    int
	val      []byte
	bo       binary.ByteOrder
}

var tiffTypeSize = map[uint16]int{1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 6: 1, 7: 1, 8: 2, 9: 4, 10: 8, 11: 4, 12: 8, 13: 4}

// tiffIFD reads the IFD at off in b: its entries by tag, in order, and
// the next IFD's offset.
func tiffIFD(b []byte, bo binary.ByteOrder, off uint32) ([]tiffEntry, uint32) {
	if int64(off)+2 > int64(len(b)) || off < 8 {
		return nil, 0
	}
	n := int(bo.Uint16(b[off:]))
	if n > 1000 || int(off)+2+n*12+4 > len(b) {
		return nil, 0
	}
	entries := make([]tiffEntry, 0, n)
	for i := 0; i < n; i++ {
		e := b[int(off)+2+i*12:]
		tag, typ, count := bo.Uint16(e), bo.Uint16(e[2:]), bo.Uint32(e[4:])
		size, ok := tiffTypeSize[typ]
		if !ok || count > 1<<20 {
			continue
		}
		total := size * int(count)
		var val []byte
		if total <= 4 {
			val = e[8 : 8+total]
		} else {
			vo := int64(bo.Uint32(e[8:]))
			if vo+int64(total) > int64(len(b)) {
				continue
			}
			val = b[vo : vo+int64(total)]
		}
		entries = append(entries, tiffEntry{tag: tag, typ: typ, count: int(count), val: val, bo: bo})
	}
	return entries, bo.Uint32(b[int(off)+2+n*12:])
}

func (e tiffEntry) uint(i int) (uint64, bool) {
	if i >= e.count {
		return 0, false
	}
	switch e.typ {
	case 1, 7:
		return uint64(e.val[i]), true
	case 3:
		return uint64(e.bo.Uint16(e.val[2*i:])), true
	case 4, 13:
		return uint64(e.bo.Uint32(e.val[4*i:])), true
	case 8:
		return uint64(int16(e.bo.Uint16(e.val[2*i:]))), true
	case 9:
		return uint64(int32(e.bo.Uint32(e.val[4*i:]))), true
	}
	return 0, false
}

// float is value i as a number: rationals divided out.
func (e tiffEntry) float(i int) (float64, bool) {
	if i >= e.count {
		return 0, false
	}
	switch e.typ {
	case 5:
		n, d := e.bo.Uint32(e.val[8*i:]), e.bo.Uint32(e.val[8*i+4:])
		if d == 0 {
			return 0, false
		}
		return float64(n) / float64(d), true
	case 10:
		n, d := int32(e.bo.Uint32(e.val[8*i:])), int32(e.bo.Uint32(e.val[8*i+4:]))
		if d == 0 {
			return 0, false
		}
		return float64(n) / float64(d), true
	case 11:
		return float64(math.Float32frombits(e.bo.Uint32(e.val[4*i:]))), true
	case 12:
		return math.Float64frombits(e.bo.Uint64(e.val[8*i:])), true
	case 6:
		return float64(int8(e.val[i])), true
	case 8, 9:
		v, ok := e.uint(i)
		return float64(int64(v)), ok
	}
	v, ok := e.uint(i)
	return float64(v), ok
}

func (e tiffEntry) str() string {
	if e.typ == 2 || e.typ == 7 || e.typ == 1 {
		return strings.TrimSpace(strings.TrimRight(validUTF8(bytes.TrimRight(e.val, "\x00")), "\x00"))
	}
	return ""
}

// text is the value as the "All tags" list shows it.
func (e tiffEntry) text() string {
	switch e.typ {
	case 2:
		return e.str()
	case 7, 1:
		if s := e.str(); s != "" && e.count <= 64 && isPrintable(s) {
			return s
		}
		return fmt.Sprintf("(%d bytes)", e.count)
	}
	var parts []string
	for i := 0; i < e.count && i < 8; i++ {
		if e.typ == 5 || e.typ == 10 {
			n, d := int64(e.bo.Uint32(e.val[8*i:])), int64(e.bo.Uint32(e.val[8*i+4:]))
			if e.typ == 10 {
				n, d = int64(int32(n)), int64(int32(d))
			}
			if d == 1 || d == 0 {
				parts = append(parts, strconv.FormatInt(n, 10))
			} else {
				parts = append(parts, fmt.Sprintf("%d/%d", n, d))
			}
			continue
		}
		f, _ := e.float(i)
		parts = append(parts, formatFloat(f, 4))
	}
	if e.count > 8 {
		parts = append(parts, fmt.Sprintf("… (%d values)", e.count))
	}
	return strings.Join(parts, ", ")
}

func isPrintable(s string) bool {
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}

// exifTagNames names the tags of IFD0 and the EXIF IFD, gpsTagNames the
// GPS IFD's.
var exifTagNames = map[uint16]string{
	0x0100: "ImageWidth", 0x0101: "ImageLength", 0x0102: "BitsPerSample", 0x0103: "Compression",
	0x0106: "PhotometricInterpretation", 0x010E: "ImageDescription", 0x010F: "Make", 0x0110: "Model",
	0x0112: "Orientation", 0x0115: "SamplesPerPixel", 0x011A: "XResolution", 0x011B: "YResolution",
	0x0128: "ResolutionUnit", 0x0131: "Software", 0x0132: "DateTime", 0x013B: "Artist",
	0x013E: "WhitePoint", 0x013F: "PrimaryChromaticities", 0x0211: "YCbCrCoefficients",
	0x0213: "YCbCrPositioning", 0x0214: "ReferenceBlackWhite", 0x8298: "Copyright",
	0x829A: "ExposureTime", 0x829D: "FNumber", 0x8822: "ExposureProgram", 0x8824: "SpectralSensitivity",
	0x8827: "ISOSpeedRatings", 0x8830: "SensitivityType", 0x8832: "RecommendedExposureIndex",
	0x9000: "ExifVersion", 0x9003: "DateTimeOriginal", 0x9004: "DateTimeDigitized",
	0x9010: "OffsetTime", 0x9011: "OffsetTimeOriginal", 0x9012: "OffsetTimeDigitized",
	0x9101: "ComponentsConfiguration", 0x9102: "CompressedBitsPerPixel", 0x9201: "ShutterSpeedValue",
	0x9202: "ApertureValue", 0x9203: "BrightnessValue", 0x9204: "ExposureBiasValue",
	0x9205: "MaxApertureValue", 0x9206: "SubjectDistance", 0x9207: "MeteringMode", 0x9208: "LightSource",
	0x9209: "Flash", 0x920A: "FocalLength", 0x9214: "SubjectArea", 0x927C: "MakerNote",
	0x9286: "UserComment", 0x9290: "SubSecTime", 0x9291: "SubSecTimeOriginal", 0x9292: "SubSecTimeDigitized",
	0x9C9B: "XPTitle", 0x9C9C: "XPComment", 0x9C9D: "XPAuthor", 0x9C9E: "XPKeywords", 0x9C9F: "XPSubject",
	0xA000: "FlashpixVersion", 0xA001: "ColorSpace", 0xA002: "PixelXDimension", 0xA003: "PixelYDimension",
	0xA20E: "FocalPlaneXResolution", 0xA20F: "FocalPlaneYResolution", 0xA210: "FocalPlaneResolutionUnit",
	0xA217: "SensingMethod", 0xA300: "FileSource", 0xA301: "SceneType", 0xA401: "CustomRendered",
	0xA402: "ExposureMode", 0xA403: "WhiteBalance", 0xA404: "DigitalZoomRatio",
	0xA405: "FocalLengthIn35mmFilm", 0xA406: "SceneCaptureType", 0xA407: "GainControl", 0xA408: "Contrast",
	0xA409: "Saturation", 0xA40A: "Sharpness", 0xA40C: "SubjectDistanceRange", 0xA420: "ImageUniqueID",
	0xA430: "CameraOwnerName", 0xA431: "BodySerialNumber", 0xA432: "LensSpecification", 0xA433: "LensMake",
	0xA434: "LensModel", 0xA435: "LensSerialNumber", 0xA460: "CompositeImage", 0xA500: "Gamma",
	0xC612: "DNGVersion", 0xC614: "UniqueCameraModel", 0x8769: "ExifIFD", 0x8825: "GPSIFD", 0xA005: "InteropIFD",
	0x4746: "Rating", 0x0201: "JPEGInterchangeFormat", 0x0202: "JPEGInterchangeFormatLength",
}

var gpsTagNames = map[uint16]string{
	0x00: "GPSVersionID", 0x01: "GPSLatitudeRef", 0x02: "GPSLatitude", 0x03: "GPSLongitudeRef",
	0x04: "GPSLongitude", 0x05: "GPSAltitudeRef", 0x06: "GPSAltitude", 0x07: "GPSTimeStamp",
	0x08: "GPSSatellites", 0x09: "GPSStatus", 0x0A: "GPSMeasureMode", 0x0B: "GPSDOP", 0x0C: "GPSSpeedRef",
	0x0D: "GPSSpeed", 0x0E: "GPSTrackRef", 0x0F: "GPSTrack", 0x10: "GPSImgDirectionRef",
	0x11: "GPSImgDirection", 0x12: "GPSMapDatum", 0x1B: "GPSProcessingMethod", 0x1D: "GPSDateStamp",
	0x1F: "GPSHPositioningError",
}

// exifData is the tags of a TIFF structure's IFD0, EXIF and GPS IFDs.
type exifData struct {
	ifd0, exif, gps map[uint16]tiffEntry
}

func readExif(b []byte, c *mediaCollector) *exifData {
	if len(b) < 8 {
		return nil
	}
	var bo binary.ByteOrder
	switch string(b[:4]) {
	case "II*\x00":
		bo = binary.LittleEndian
	case "MM\x00*":
		bo = binary.BigEndian
	default:
		return nil
	}
	x := &exifData{ifd0: map[uint16]tiffEntry{}, exif: map[uint16]tiffEntry{}, gps: map[uint16]tiffEntry{}}
	read := func(off uint32, into map[uint16]tiffEntry, group string, names map[uint16]string) {
		entries, _ := tiffIFD(b, bo, off)
		for _, e := range entries {
			into[e.tag] = e
			name := names[e.tag]
			switch {
			case e.tag == 0x8769 || e.tag == 0x8825 || e.tag == 0xA005 || e.tag == 0x0201 || e.tag == 0x0202:
				continue // pointers
			case name == "":
				name = fmt.Sprintf("0x%04X", e.tag)
			}
			v := e.text()
			switch {
			case e.tag == 0x927C:
				v = fmt.Sprintf("(%d bytes)", e.count)
			case e.tag == 0x9286:
				v = userComment(e)
			case e.tag >= 0x9C9B && e.tag <= 0x9C9F:
				v = utf16LE(e.val)
			case group == "GPS" && (e.tag == 0x02 || e.tag == 0x04):
				v = "(see location)"
			}
			c.tag(group, name, v)
		}
	}
	read(bo.Uint32(b[4:]), x.ifd0, "EXIF", exifTagNames)
	if e, ok := x.ifd0[0x8769]; ok {
		if off, ok := e.uint(0); ok {
			read(uint32(off), x.exif, "EXIF", exifTagNames)
		}
	}
	if e, ok := x.ifd0[0x8825]; ok {
		if off, ok := e.uint(0); ok {
			read(uint32(off), x.gps, "GPS", gpsTagNames)
		}
	}
	return x
}

// get finds tag in the EXIF IFD, then IFD0.
func (x *exifData) get(tag uint16) (tiffEntry, bool) {
	if e, ok := x.exif[tag]; ok {
		return e, true
	}
	e, ok := x.ifd0[tag]
	return e, ok
}

func (x *exifData) str(tag uint16) string {
	if e, ok := x.get(tag); ok {
		if tag >= 0x9C9B && tag <= 0x9C9F {
			return utf16LE(e.val)
		}
		return e.str()
	}
	return ""
}

func (x *exifData) num(tag uint16) (float64, bool) {
	if e, ok := x.get(tag); ok {
		return e.float(0)
	}
	return 0, false
}

// userComment decodes UserComment: an 8-byte character code, then text.
func userComment(e tiffEntry) string {
	if len(e.val) < 8 {
		return ""
	}
	code, text := string(bytes.TrimRight(e.val[:8], "\x00 ")), e.val[8:]
	switch code {
	case "UNICODE":
		if len(text) >= 2 && text[0] == 0 {
			return utf16BE(text)
		}
		return utf16LE(text)
	case "ASCII", "":
		return strings.TrimSpace(validUTF8(bytes.TrimRight(text, "\x00 ")))
	}
	return ""
}

// junkDescriptions are what cameras put in ImageDescription when the
// photographer wrote nothing.
var junkDescriptions = map[string]bool{
	"OLYMPUS DIGITAL CAMERA": true, "SONY DSC": true, "DCIM": true, "Default": true, "default": true,
	"DIGITAL CAMERA": true, "KODAK Digital Still Camera": true, "MINOLTA DIGITAL CAMERA": true,
	"Samsung Digital Camera": true, "LG Electronics": true, "Untitled": true, "image": true,
}

func (c *mediaCollector) applyExif(x *exifData) {
	makeName, model := x.str(0x010F), x.str(0x0110)
	switch {
	case model == "":
		c.add("Camera", "Camera", makeName)
	case makeName == "" || strings.HasPrefix(strings.ToLower(model), strings.ToLower(strings.Fields(makeName)[0])):
		c.add("Camera", "Camera", model)
	default:
		c.add("Camera", "Camera", makeName+" "+model)
	}
	lensMake, lens := x.str(0xA433), x.str(0xA434)
	// The lens' maker, when it isn't the camera's and the lens' name
	// doesn't say it.
	if lens != "" && lensMake != "" && !strings.EqualFold(lensMake, makeName) && !strings.HasPrefix(strings.ToLower(lens), strings.ToLower(lensMake)) {
		lens = lensMake + " " + lens
	}
	c.add("Camera", "Lens", lens)
	if e, ok := x.get(0xA432); ok && e.count == 4 {
		lo, _ := e.float(0)
		hi, _ := e.float(1)
		flo, ok1 := e.float(2)
		fhi, ok2 := e.float(3)
		spec := formatFloat(lo, 1) + " mm"
		if hi > lo {
			spec = formatFloat(lo, 1) + "–" + formatFloat(hi, 1) + " mm"
		}
		if ok1 && flo > 0 {
			spec += " ƒ/" + formatFloat(flo, 1)
			if ok2 && fhi > flo {
				spec += "–" + formatFloat(fhi, 1)
			}
		}
		if lo > 0 {
			c.add("Camera", "Lens specification", spec)
		}
	}

	if v, ok := x.num(0x829D); ok && v > 0 {
		c.add("Exposure", "Aperture", "ƒ/"+formatFloat(v, 1))
	} else if v, ok := x.num(0x9202); ok {
		c.add("Exposure", "Aperture", "ƒ/"+formatFloat(math.Pow(2, v/2), 1))
	}
	if v, ok := x.num(0x829A); ok && v > 0 {
		c.add("Exposure", "Shutter speed", shutter(v))
	} else if v, ok := x.num(0x9201); ok {
		c.add("Exposure", "Shutter speed", shutter(math.Pow(2, -v)))
	}
	if v, ok := x.num(0x8827); ok && v > 0 {
		c.add("Exposure", "ISO", formatFloat(v, 0))
	}
	if v, ok := x.num(0x920A); ok && v > 0 {
		f := formatFloat(v, 1) + " mm"
		if e, ok := x.num(0xA405); ok && e > 0 && math.Abs(e-v) > 0.5 {
			f += " (" + formatFloat(e, 0) + " mm in 35 mm)"
		}
		c.add("Exposure", "Focal length", f)
	}
	if v, ok := x.num(0x9204); ok {
		sign := ""
		if v > 0 {
			sign = "+"
		}
		c.add("Exposure", "Exposure bias", sign+formatFloat(v, 2)+" EV")
	}
	if e, ok := x.get(0x9209); ok {
		if v, ok := e.uint(0); ok {
			c.add("Exposure", "Flash", flashText(v))
		}
	}
	enum := func(group, label string, tag uint16, names map[uint64]string) {
		if e, ok := x.get(tag); ok {
			if v, ok := e.uint(0); ok && names[v] != "" {
				c.add(group, label, names[v])
			}
		}
	}
	enum("Exposure", "Program", 0x8822, map[uint64]string{1: "Manual", 2: "Program AE", 3: "Aperture priority", 4: "Shutter priority", 5: "Creative", 6: "Action", 7: "Portrait", 8: "Landscape"})
	enum("Exposure", "Mode", 0xA402, map[uint64]string{0: "Auto", 1: "Manual", 2: "Auto bracket"})
	enum("Exposure", "Metering", 0x9207, map[uint64]string{1: "Average", 2: "Center-weighted average", 3: "Spot", 4: "Multi-spot", 5: "Multi-segment", 6: "Partial"})
	enum("Exposure", "White balance", 0xA403, map[uint64]string{0: "Auto", 1: "Manual"})
	enum("Exposure", "Light source", 0x9208, map[uint64]string{1: "Daylight", 2: "Fluorescent", 3: "Tungsten", 4: "Flash", 9: "Fine weather", 10: "Cloudy", 11: "Shade", 17: "Standard light A", 20: "D55", 21: "D65", 22: "D75"})
	enum("Exposure", "Scene", 0xA406, map[uint64]string{1: "Landscape", 2: "Portrait", 3: "Night"})
	enum("Exposure", "Contrast", 0xA408, map[uint64]string{1: "Low", 2: "High"})
	enum("Exposure", "Saturation", 0xA409, map[uint64]string{1: "Low", 2: "High"})
	enum("Exposure", "Sharpness", 0xA40A, map[uint64]string{1: "Soft", 2: "Hard"})
	if v, ok := x.num(0xA404); ok && v > 1 {
		c.add("Exposure", "Digital zoom", formatFloat(v, 1)+"×")
	}
	if v, ok := x.num(0x9206); ok && v > 0 && v < 1e6 {
		c.add("Exposure", "Subject distance", formatFloat(v, 2)+" m")
	}

	// The date taken, with its offset from UTC when the camera wrote it.
	if t := exifTime(x.str(0x9003), x.str(0x9011)); t != "" {
		c.taken(t)
	}
	if t := exifTime(x.str(0x9004), x.str(0x9012)); t != "" {
		c.add("Description", "Digitized", t)
	}
	if t := exifTime(x.str(0x0132), x.str(0x9010)); t != "" {
		c.add("Description", "Modified", t)
	}
	c.add("Description", "Title", x.str(0x9C9B))
	c.add("Description", "Creator", firstOf(x.str(0x013B), x.str(0x9C9D)))
	c.add("Description", "Copyright", x.str(0x8298))
	c.add("Description", "Keywords", strings.ReplaceAll(x.str(0x9C9E), ";", ", "))
	c.add("Description", "Software", x.str(0x0131))
	if v, ok := x.num(0x4746); ok && v > 0 && v <= 5 {
		c.add("Description", "Rating", stars(int(v)))
	}
	if d := x.str(0x010E); d != "" && !junkDescriptions[d] {
		c.text("Description", "", d, false)
	}
	if e, ok := x.get(0x9286); ok {
		if t := userComment(e); t != "" && !junkDescriptions[t] {
			c.text("Comment", "", t, false)
		}
	}
	c.text("Comment", "", x.str(0x9C9C), false)

	enum("Image", "Color space", 0xA001, map[uint64]string{1: "sRGB", 2: "Adobe RGB", 0xFFFF: "Uncalibrated"})
	if v, ok := x.num(0xA500); ok && v > 0 {
		c.add("Image", "Gamma", formatFloat(v, 2))
	}
	if e, ok := x.get(0x0112); ok {
		if v, ok := e.uint(0); ok && v >= 1 && v <= 8 {
			c.add("Image", "Orientation", orientationText[v])
			c.orientation = int(v)
		}
	}
	if v, ok := x.num(0x011A); ok && v > 0 {
		y, _ := x.num(0x011B)
		unit := "dpi"
		if u, ok := x.num(0x0128); ok && u == 3 {
			unit = "dots per cm"
		}
		c.add("Image", "Resolution", resolution(v, y, unit))
	}
	if c.tiffFile {
		if _, ok := x.ifd0[0xC612]; ok {
			c.doc.Format = "DNG"
		}
		w, _ := x.num(0x0100)
		h, _ := x.num(0x0101)
		c.size(int(w), int(h))
		if e, ok := x.ifd0[0x0102]; ok {
			if v, ok := e.uint(0); ok {
				c.add("Image", "Bit depth", fmt.Sprintf("%d bits per channel", v))
			}
		}
		enum("Image", "Color model", 0x0106, map[uint64]string{0: "Grayscale", 1: "Grayscale", 2: "RGB", 3: "Indexed color", 5: "CMYK", 6: "YCbCr", 8: "CIELAB", 32803: "Color filter array (raw)", 34892: "Linear raw"})
		enum("Image", "Compression", 0x0103, map[uint64]string{1: "None", 5: "LZW", 6: "JPEG (old)", 7: "JPEG", 8: "Deflate", 32773: "PackBits", 34892: "Lossy JPEG", 52546: "JPEG XL"})
	}
	if w, ok := x.num(0xA002); ok {
		h, _ := x.num(0xA003)
		c.size(int(w), int(h))
	}

	c.location(exifLocation(x))
}

var orientationText = map[uint64]string{
	1: "Normal", 2: "Mirrored", 3: "Rotated 180°", 4: "Mirrored vertically",
	5: "Mirrored, rotated 90° counterclockwise", 6: "Rotated 90° clockwise",
	7: "Mirrored, rotated 90° clockwise", 8: "Rotated 90° counterclockwise",
}

func exifLocation(x *exifData) *mediaLocation {
	coord := func(tag, ref uint16, neg string) (float64, bool) {
		e, ok := x.gps[tag]
		if !ok || e.count < 1 {
			return 0, false
		}
		d, ok := e.float(0)
		if !ok {
			return 0, false
		}
		m, _ := e.float(1)
		s, _ := e.float(2)
		v := d + m/60 + s/3600
		if r, ok := x.gps[ref]; ok && strings.EqualFold(r.str(), neg) {
			v = -v
		}
		return v, true
	}
	lat, ok1 := coord(0x02, 0x01, "S")
	lon, ok2 := coord(0x04, 0x03, "W")
	if !ok1 || !ok2 {
		return nil
	}
	l := &mediaLocation{Lat: round6(lat), Lon: round6(lon)}
	if e, ok := x.gps[0x06]; ok {
		if a, ok := e.float(0); ok {
			if r, ok := x.gps[0x05]; ok {
				if v, _ := r.uint(0); v == 1 {
					a = -a
				}
			}
			a = math.Round(a*10) / 10
			l.Alt = &a
		}
	}
	if e, ok := x.gps[0x11]; ok {
		if d, ok := e.float(0); ok {
			d = math.Round(math.Mod(d, 360)*10) / 10
			l.Direction = &d
			l.DirectionRef = "T"
			if r, ok := x.gps[0x10]; ok && strings.EqualFold(r.str(), "M") {
				l.DirectionRef = "M"
			}
		}
	}
	if e, ok := x.gps[0x1F]; ok {
		if a, ok := e.float(0); ok && a > 0 {
			a = math.Round(a*10) / 10
			l.Accuracy = &a
		}
	}
	return l
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// exifTime turns EXIF's "2006:01:02 15:04:05" and an offset ("+02:00")
// into 2006-01-02T15:04:05+02:00.
func exifTime(t, offset string) string {
	t = strings.TrimSpace(t)
	if len(t) < 19 || t[4] != ':' || t[7] != ':' || strings.HasPrefix(t, "0000") {
		return ""
	}
	out := t[:4] + "-" + t[5:7] + "-" + t[8:10] + "T" + t[11:19]
	if len(offset) == 6 && (offset[0] == '+' || offset[0] == '-') && offset[3] == ':' {
		out += offset
	}
	return out
}

func shutter(v float64) string {
	if v >= 1 {
		return formatFloat(v, 1) + " s"
	}
	return "1/" + formatFloat(math.Round(1/v), 0) + " s"
}

func flashText(v uint64) string {
	fired := "Fired"
	if v&1 == 0 {
		fired = "Did not fire"
	}
	switch v >> 3 & 3 {
	case 1:
		return fired + ", compulsory"
	case 2:
		return "Off, did not fire"
	case 3:
		return fired + ", auto"
	}
	if v&0x20 != 0 {
		return "No flash"
	}
	return fired
}

func stars(n int) string {
	return strings.Repeat("★", n) + strings.Repeat("☆", 5-n)
}

func resolution(x, y float64, unit string) string {
	if y == 0 || math.Abs(x-y) < 0.5 {
		return formatFloat(x, 0) + " " + unit
	}
	return formatFloat(x, 0) + " × " + formatFloat(y, 0) + " " + unit
}

// formatFloat writes v with at most prec decimals, none trailing.
func formatFloat(v float64, prec int) string {
	s := strconv.FormatFloat(v, 'f', prec, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		s = "0"
	}
	return s
}

func firstOf(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// ---- Text ----

func latin1(b []byte) string {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

func validUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return latin1(b)
}

func utf16LE(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, binary.LittleEndian.Uint16(b[i:]))
	}
	return strings.TrimSpace(strings.TrimRight(string(utf16.Decode(u)), "\x00"))
}

func utf16BE(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, binary.BigEndian.Uint16(b[i:]))
	}
	return strings.TrimSpace(strings.TrimRight(string(utf16.Decode(u)), "\x00"))
}

// ---- XMP ----

// xmpPrefixes are the namespaces XMP's properties are named by here,
// whatever prefix the file gave them.
var xmpPrefixes = map[string]string{
	"http://purl.org/dc/elements/1.1/":                     "dc",
	"http://ns.adobe.com/xap/1.0/":                         "xmp",
	"http://ns.adobe.com/photoshop/1.0/":                   "photoshop",
	"http://iptc.org/std/Iptc4xmpCore/1.0/xmlns/":          "Iptc4xmpCore",
	"http://iptc.org/std/Iptc4xmpExt/2008-02-29/":          "Iptc4xmpExt",
	"http://ns.adobe.com/exif/1.0/":                        "exif",
	"http://cipa.jp/exif/1.0/":                             "exifEX",
	"http://ns.adobe.com/tiff/1.0/":                        "tiff",
	"http://ns.adobe.com/exif/1.0/aux/":                    "aux",
	"http://ns.adobe.com/xap/1.0/rights/":                  "xmpRights",
	"http://ns.adobe.com/xap/1.0/mm/":                      "xmpMM",
	"http://ns.google.com/photos/1.0/panorama/":            "GPano",
	"http://ns.adobe.com/camera-raw-settings/1.0/":         "crs",
	"http://ns.adobe.com/lightroom/1.0/":                   "lr",
	"http://ns.apple.com/adjustment-settings/1.0/":         "aas",
	"http://ns.adobe.com/xap/1.0/sType/ResourceEvent#":     "stEvt",
	"http://ns.adobe.com/xap/1.0/sType/ResourceRef#":       "stRef",
	"http://iptc.org/std/Iptc4xmpExt/2008-02-29/xmlns/":    "Iptc4xmpExt",
	"http://ns.microsoft.com/photo/1.0/":                   "MicrosoftPhoto",
	"http://ns.adobe.com/xmp/note/":                        "xmpNote",
	"http://ns.adobe.com/photoshop/1.0/camera-profile":     "photoshop",
	"http://www.metadataworkinggroup.com/schemas/regions/": "mwg-rs",
}

const rdfNS = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"

// xmpProps is XMP's properties by "prefix:Name", in the order found,
// each with its values (a list's items, or an alternative's, the
// default language first).
type xmpProps struct {
	keys []string
	vals map[string][]string
}

func (p *xmpProps) add(k, v string) {
	if p.vals[k] == nil {
		p.keys = append(p.keys, k)
	}
	p.vals[k] = append(p.vals[k], v)
}

func (p *xmpProps) get(k string) string {
	if v := p.vals[k]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func xmpName(n xml.Name) string {
	prefix, ok := xmpPrefixes[n.Space]
	if !ok {
		prefix = strings.Trim(n.Space, "/#")
		if i := strings.LastIndexAny(prefix, "/#"); i >= 0 {
			prefix = prefix[i+1:]
		}
	}
	return prefix + ":" + n.Local
}

func parseXMP(b []byte) *xmpProps {
	p := &xmpProps{vals: map[string][]string{}}
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	var stack []xml.Name
	var text strings.Builder
	for i := 0; i < 100000; i++ {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name)
			text.Reset()
			for _, a := range t.Attr {
				if a.Name.Space == rdfNS || a.Name.Space == "xmlns" || a.Name.Local == "xmlns" || a.Name.Space == "xml" ||
					a.Name.Space == "http://www.w3.org/XML/1998/namespace" || a.Name.Space == "adobe:ns:meta/" || a.Name.Space == "" {
					continue
				}
				p.add(xmpName(a.Name), a.Value)
			}
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			if v := strings.TrimSpace(text.String()); v != "" {
				// The property is the nearest element that isn't RDF's
				// own (Bag, Seq, Alt, li, Description).
				for j := len(stack) - 1; j >= 0; j-- {
					if stack[j].Space != rdfNS {
						p.add(xmpName(stack[j]), v)
						break
					}
				}
			}
			text.Reset()
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return p
}

func (c *mediaCollector) applyXMP(p *xmpProps) {
	for _, k := range p.keys {
		c.tag("XMP", k, strings.Join(p.vals[k], "; "))
	}
	group := "Description"
	if c.doc.Kind == "audio" {
		group = "Song"
	}
	c.add(group, "Title", p.get("dc:title"))
	c.add("Description", "Creator", strings.Join(p.vals["dc:creator"], ", "))
	c.add("Description", "Copyright", p.get("dc:rights"))
	c.add("Description", "Keywords", strings.Join(p.vals["dc:subject"], ", "))
	if r, err := strconv.Atoi(p.get("xmp:Rating")); err == nil && r > 0 && r <= 5 {
		c.add("Description", "Rating", stars(r))
	}
	c.add("Description", "Software", p.get("xmp:CreatorTool"))
	c.add("Camera", "Lens", firstOf(p.get("aux:Lens"), p.get("exifEX:LensModel")))
	c.text("Description", "", p.get("dc:description"), false)
	c.text("Instructions", "", p.get("photoshop:Instructions"), false)
	for _, k := range []string{"exif:DateTimeOriginal", "photoshop:DateCreated", "xmp:CreateDate"} {
		if t := xmpTime(p.get(k)); t != "" {
			c.taken(t)
			break
		}
	}
	lat, ok1 := xmpCoord(p.get("exif:GPSLatitude"))
	lon, ok2 := xmpCoord(p.get("exif:GPSLongitude"))
	if ok1 && ok2 {
		c.location(&mediaLocation{Lat: round6(lat), Lon: round6(lon)})
	}
	var place []string
	for _, k := range []string{"Iptc4xmpCore:Location", "photoshop:City", "photoshop:State", "photoshop:Country"} {
		if v := p.get(k); v != "" && !containsFold(place, v) {
			place = append(place, v)
		}
	}
	if len(place) > 0 && c.doc.Place == "" {
		c.doc.Place = strings.Join(place, ", ")
	}
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// xmpTime takes XMP's dates, ISO 8601, to the length the panel shows:
// to the second, with the offset when there is one.
func xmpTime(t string) string {
	if len(t) < 16 || t[4] != '-' || t[10] != 'T' {
		return ""
	}
	date, rest := t[:16], t[16:]
	if len(rest) >= 3 && rest[0] == ':' {
		date += rest[:3]
		rest = rest[3:]
	} else {
		date += ":00"
	}
	if i := strings.IndexAny(rest, "+-Z"); i >= 0 {
		rest = rest[i:]
	} else {
		rest = ""
	}
	return date + rest
}

// xmpCoord reads XMP's GPS coordinates: "48,51.504N" or "48,51,30N".
func xmpCoord(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return 0, false
	}
	dir := s[len(s)-1]
	parts := strings.Split(s[:len(s)-1], ",")
	var v float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || i > 2 {
			return 0, false
		}
		v += f / math.Pow(60, float64(i))
	}
	switch dir {
	case 'S', 's', 'W', 'w':
		v = -v
	case 'N', 'n', 'E', 'e':
	default:
		return 0, false
	}
	return v, true
}

// ---- ICC ----

func (c *mediaCollector) applyICC(b []byte) {
	if len(b) < 132 || string(b[36:40]) != "acsp" {
		return
	}
	class := map[string]string{"mntr": "Display", "scnr": "Input", "prtr": "Output", "spac": "Color space", "abst": "Abstract", "link": "Device link", "nmcl": "Named color"}[string(b[12:16])]
	version := fmt.Sprintf("%d.%d", b[8], b[9]>>4)
	desc, cprt := "", ""
	var trc []byte
	n := int(binary.BigEndian.Uint32(b[128:]))
	for i := 0; i < n && i < 100 && 132+12*i+12 <= len(b); i++ {
		e := b[132+12*i:]
		sig, off, size := string(e[:4]), int(binary.BigEndian.Uint32(e[4:])), int(binary.BigEndian.Uint32(e[8:]))
		if off < 0 || size < 8 || off+size > len(b) {
			continue
		}
		data := b[off : off+size]
		switch sig {
		case "desc":
			desc = iccText(data)
		case "cprt":
			cprt = iccText(data)
		case "rTRC", "kTRC":
			if trc == nil {
				trc = data
			}
		}
	}
	c.add("Image", "ICC profile", desc)
	if class != "" {
		c.add("Image", "Profile class", class+", ICC v"+version)
	}
	if g := iccGamma(trc); g != "" {
		c.add("Image", "Gamma", g)
	}
	// EXIF's "uncalibrated" means "see the profile" (as a phone shooting
	// in Display P3 writes it), so the profile names the color space.
	for _, f := range c.fields["Image"] {
		if f.Label == "Color space" && f.Value == "Uncalibrated" && desc != "" {
			c.set("Image", "Color space", desc)
		}
	}
	c.add("Image", "Color space", desc)
	c.tag("ICC", "Description", desc)
	c.tag("ICC", "Version", version)
	c.tag("ICC", "Class", class)
	c.tag("ICC", "Color space", strings.TrimSpace(string(b[16:20])))
	c.tag("ICC", "Connection space", strings.TrimSpace(string(b[20:24])))
	c.tag("ICC", "CMM", strings.Trim(string(b[4:8]), "\x00 "))
	c.tag("ICC", "Platform", strings.Trim(string(b[40:44]), "\x00 "))
	c.tag("ICC", "Copyright", cprt)
	if y := binary.BigEndian.Uint16(b[24:]); y > 0 {
		c.tag("ICC", "Created", fmt.Sprintf("%04d-%02d-%02d", y, binary.BigEndian.Uint16(b[26:]), binary.BigEndian.Uint16(b[28:])))
	}
	c.tag("ICC", "Size", fmt.Sprintf("%d bytes", len(b)))
}

// iccText reads an ICC text tag: desc (v2), mluc (v4, its first
// record) or text.
func iccText(d []byte) string {
	switch string(d[:4]) {
	case "desc":
		if len(d) < 12 {
			return ""
		}
		n := int(binary.BigEndian.Uint32(d[8:]))
		if n <= 0 || 12+n > len(d) {
			return ""
		}
		return strings.TrimSpace(strings.TrimRight(validUTF8(d[12:12+n]), "\x00"))
	case "mluc":
		if len(d) < 28 {
			return ""
		}
		l, o := int(binary.BigEndian.Uint32(d[20:])), int(binary.BigEndian.Uint32(d[24:]))
		if o+l > len(d) || l <= 0 {
			return ""
		}
		return utf16BE(d[o : o+l])
	case "text":
		return strings.TrimSpace(strings.TrimRight(validUTF8(d[8:]), "\x00"))
	}
	return ""
}

// iccGamma describes a tone curve: a gamma, or the sRGB curve.
func iccGamma(d []byte) string {
	if len(d) < 12 {
		return ""
	}
	switch string(d[:4]) {
	case "curv":
		n := binary.BigEndian.Uint32(d[8:])
		switch {
		case n == 0:
			return "1 (linear)"
		case n == 1 && len(d) >= 14:
			return formatFloat(float64(binary.BigEndian.Uint16(d[12:]))/256, 2)
		default:
			return fmt.Sprintf("Tone curve (%d points)", n)
		}
	case "para":
		if len(d) < 16 {
			return ""
		}
		g := float64(int32(binary.BigEndian.Uint32(d[12:]))) / 65536
		if fn := binary.BigEndian.Uint16(d[8:]); fn == 3 && math.Abs(g-2.4) < 0.01 {
			return "sRGB curve (≈2.2)"
		}
		return formatFloat(g, 2)
	}
	return ""
}

// applyEmbedded reads the EXIF, XMP and ICC profile the parsers found.
// EXIF comes first, so where two say the same thing, EXIF's is kept.
func (c *mediaCollector) applyEmbedded() {
	if c.exif != nil {
		if x := readExif(c.exif, c); x != nil {
			c.applyExif(x)
		}
	}
	if c.xmp != nil {
		c.applyXMP(parseXMP(c.xmp))
	}
	if c.icc != nil {
		c.applyICC(c.icc)
	}
	// The size as the photo is shown, turned by its orientation.
	if c.orientation >= 5 && c.orientation <= 8 && c.doc.Kind == "image" {
		c.doc.Width, c.doc.Height = c.doc.Height, c.doc.Width
	}
	if c.doc.Format != "" {
		c.add("File", "Format", c.doc.Format)
	}
}
