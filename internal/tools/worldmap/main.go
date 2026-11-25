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

// Command worldmap makes pkg/gowebserver/geodata/worldmap.json, the
// offline world map the file browser's info panel draws a photo's
// location on, and the server names its country from (mediainfo.go).
//
// Its input is Natural Earth's countries (public domain), as GeoJSON:
//
//	go run ./internal/tools/worldmap -in ne_50m_admin_0_countries.geojson \
//	    -o pkg/gowebserver/geodata/worldmap.json
//
// from https://github.com/nvkelso/natural-earth-vector/tree/master/geojson.
// Each outline is simplified (Douglas-Peucker, -tolerance degrees) and
// its points rounded to 1/-q of a degree, each written as the difference
// from the one before it, so the file is small enough to fetch when the
// panel first shows a place.
//
// The output is {"q": Q, "c": [[NAME, RING, RING...], ...]}, a RING
// being [x0, y0, dx1, dy1, ...] in units of 1/Q degree (x the longitude,
// y the latitude).
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
)

type feature struct {
	Properties map[string]any `json:"properties"`
	Geometry   struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	} `json:"geometry"`
}

type point struct{ x, y float64 }

func main() {
	in := flag.String("in", "", "Natural Earth countries, as GeoJSON.")
	out := flag.String("o", "", "Where to write the map.")
	q := flag.Int("q", 20, "Points per degree.")
	tol := flag.Float64("tolerance", 0.06, "Simplification tolerance, in degrees.")
	minArea := flag.Float64("minarea", 0.02, "Rings smaller than this, in square degrees, are dropped.")
	flag.Parse()
	if err := run(*in, *out, *q, *tol, *minArea); err != nil {
		fmt.Fprintln(os.Stderr, "worldmap:", err)
		os.Exit(1)
	}
}

func run(in, out string, q int, tol, minArea float64) error {
	if in == "" || out == "" {
		return errors.New("-in and -o are required")
	}
	data, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	var fc struct {
		Features []feature `json:"features"`
	}
	if err := json.Unmarshal(data, &fc); err != nil {
		return err
	}
	type country struct {
		name  string
		rings [][]int
	}
	var cs []country
	for _, f := range fc.Features {
		name, _ := f.Properties["NAME"].(string)
		var polys [][][][2]float64
		switch f.Geometry.Type {
		case "Polygon":
			var p [][][2]float64
			if err := json.Unmarshal(f.Geometry.Coordinates, &p); err != nil {
				return err
			}
			polys = [][][][2]float64{p}
		case "MultiPolygon":
			if err := json.Unmarshal(f.Geometry.Coordinates, &polys); err != nil {
				return err
			}
		default:
			continue
		}
		c := country{name: name}
		for _, poly := range polys {
			// The outer ring only: lakes and enclaves aren't drawn.
			if len(poly) == 0 {
				continue
			}
			ring := make([]point, len(poly[0]))
			for i, p := range poly[0] {
				ring[i] = point{p[0], p[1]}
			}
			if math.Abs(area(ring)) < minArea {
				continue
			}
			ring = simplify(ring, tol)
			if enc := encode(ring, q); len(enc) >= 6 {
				c.rings = append(c.rings, enc)
			}
		}
		if len(c.rings) > 0 {
			cs = append(cs, c)
		}
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].name < cs[j].name })
	doc := struct {
		Q int     `json:"q"`
		C [][]any `json:"c"`
	}{Q: q}
	for _, c := range cs {
		row := []any{c.name}
		for _, r := range c.rings {
			row = append(row, r)
		}
		doc.C = append(doc.C, row)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(out, append(b, '\n'), 0o644)
}

func area(r []point) float64 {
	var a float64
	for i := range r {
		j := (i + 1) % len(r)
		a += r[i].x*r[j].y - r[j].x*r[i].y
	}
	return a / 2
}

// simplify is Douglas-Peucker on a closed ring.
func simplify(r []point, tol float64) []point {
	if len(r) < 4 {
		return r
	}
	keep := make([]bool, len(r))
	keep[0], keep[len(r)-1] = true, true
	// A closed ring's ends are the same point: split it at the point
	// farthest from them so the line has two ends to keep.
	far, best := 0, -1.0
	for i, p := range r {
		if d := math.Hypot(p.x-r[0].x, p.y-r[0].y); d > best {
			far, best = i, d
		}
	}
	keep[far] = true
	var dp func(a, b int)
	dp = func(a, b int) {
		if b <= a+1 {
			return
		}
		idx, dmax := -1, tol
		for i := a + 1; i < b; i++ {
			if d := segDist(r[i], r[a], r[b]); d > dmax {
				idx, dmax = i, d
			}
		}
		if idx >= 0 {
			keep[idx] = true
			dp(a, idx)
			dp(idx, b)
		}
	}
	dp(0, far)
	dp(far, len(r)-1)
	var out []point
	for i, p := range r {
		if keep[i] {
			out = append(out, p)
		}
	}
	return out
}

func segDist(p, a, b point) float64 {
	dx, dy := b.x-a.x, b.y-a.y
	l := dx*dx + dy*dy
	if l == 0 {
		return math.Hypot(p.x-a.x, p.y-a.y)
	}
	t := math.Max(0, math.Min(1, ((p.x-a.x)*dx+(p.y-a.y)*dy)/l))
	return math.Hypot(p.x-(a.x+t*dx), p.y-(a.y+t*dy))
}

// encode rounds the ring's points, drops repeats and the closing point,
// and writes each as the step from the last.
func encode(r []point, q int) []int {
	var out []int
	px, py := 0, 0
	for i, p := range r {
		x, y := int(math.Round(p.x*float64(q))), int(math.Round(p.y*float64(q)))
		if i > 0 && x == px && y == py {
			continue
		}
		if i == len(r)-1 && len(out) >= 2 && x == out[0] && y == out[1] {
			break
		}
		if len(out) == 0 {
			out = append(out, x, y)
		} else {
			out = append(out, x-px, y-py)
		}
		px, py = x, y
	}
	return out
}
