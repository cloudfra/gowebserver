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

// The standalone server's pprof endpoints (-debug.endpoint), served from
// runtime/pprof directly. net/http/pprof would do the same, but importing
// it registers /debug/pprof/ on http.DefaultServeMux, which a program
// that embeds this package may serve: so it's never imported.

import (
	"bufio"
	"bytes"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"sort"
	"strconv"
	"strings"
	"time"
)

// pprofHandlers are the pprof endpoints under prefix (such as /debug):
// prefix/pprof/ lists the profiles and serves each by name (heap,
// goroutine...), and cmdline, profile (CPU), symbol and trace are beside
// it, as net/http/pprof lays them out, so go tool pprof reads them.
func pprofHandlers(prefix string) map[string]http.Handler {
	base := prefix + "/pprof/"
	return map[string]http.Handler{
		base:             pprofIndex(base),
		base + "cmdline": http.HandlerFunc(pprofCmdline),
		base + "profile": http.HandlerFunc(pprofCPU),
		base + "symbol":  http.HandlerFunc(pprofSymbol),
		base + "trace":   http.HandlerFunc(pprofTrace),
	}
}

func pprofNoCache(w http.ResponseWriter, contentType string) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", contentType)
}

// pprofIndex lists the profiles at base, and serves the one named after
// it: ?debug=1 or 2 as text, else in the binary format go tool pprof
// reads, and ?gc=1 collects garbage before a heap profile.
func pprofIndex(base string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, base)
		if name != "" && name != r.URL.Path {
			p := pprof.Lookup(name)
			if p == nil {
				http.Error(w, "unknown profile", http.StatusNotFound)
				return
			}
			debug, err := strconv.Atoi(r.URL.Query().Get("debug"))
			if err != nil {
				debug = 0 // the binary format
			}
			if name == "heap" && r.URL.Query().Get("gc") != "" && r.URL.Query().Get("gc") != "0" {
				runtime.GC()
			}
			if debug > 0 {
				pprofNoCache(w, "text/plain; charset=utf-8")
			} else {
				pprofNoCache(w, "application/octet-stream")
				w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name))
			}
			if err := p.WriteTo(w, debug); err != nil {
				http.Error(w, "cannot write the profile", http.StatusInternalServerError)
			}
			return
		}
		profiles := pprof.Profiles()
		sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name() < profiles[j].Name() })
		var b bytes.Buffer
		b.WriteString("<!doctype html><title>pprof</title><h1>Profiles</h1><table><tr><th>Count</th><th>Profile</th></tr>\n")
		for _, p := range profiles {
			n := html.EscapeString(p.Name())
			fmt.Fprintf(&b, "<tr><td>%d</td><td><a href=\"%s?debug=1\">%s</a></td></tr>\n", p.Count(), n, n)
		}
		b.WriteString("</table><p><a href=\"cmdline\">cmdline</a> · <a href=\"profile\">profile</a> (CPU, ?seconds=30) · " +
			"<a href=\"trace\">trace</a> (?seconds=1) · <a href=\"goroutine?debug=2\">full goroutine stacks</a></p>\n")
		pprofNoCache(w, "text/html; charset=utf-8")
		if _, err := w.Write(b.Bytes()); err != nil {
			return // the client left
		}
	})
}

// pprofCmdline is the command line, its arguments separated by NULs.
func pprofCmdline(w http.ResponseWriter, _ *http.Request) {
	pprofNoCache(w, "text/plain; charset=utf-8")
	if _, err := io.WriteString(w, strings.Join(os.Args, "\x00")); err != nil {
		return // the client left
	}
}

// pprofSeconds is the request's ?seconds, or def.
func pprofSeconds(r *http.Request, def float64) time.Duration {
	if s, err := strconv.ParseFloat(r.URL.Query().Get("seconds"), 64); err == nil && s > 0 {
		def = s
	}
	return time.Duration(def * float64(time.Second))
}

// pprofWait waits d, or until the client leaves.
func pprofWait(r *http.Request, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-r.Context().Done():
	}
}

// pprofCPU profiles the CPU for ?seconds (30).
func pprofCPU(w http.ResponseWriter, r *http.Request) {
	pprofNoCache(w, "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="profile"`)
	if err := pprof.StartCPUProfile(w); err != nil {
		w.Header().Set("Content-Disposition", "")
		http.Error(w, "cannot profile the CPU: "+err.Error(), http.StatusInternalServerError)
		return
	}
	pprofWait(r, pprofSeconds(r, 30))
	pprof.StopCPUProfile()
}

// pprofTrace traces the program for ?seconds (1).
func pprofTrace(w http.ResponseWriter, r *http.Request) {
	pprofNoCache(w, "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="trace"`)
	if err := trace.Start(w); err != nil {
		w.Header().Set("Content-Disposition", "")
		http.Error(w, "cannot trace: "+err.Error(), http.StatusInternalServerError)
		return
	}
	pprofWait(r, pprofSeconds(r, 1))
	trace.Stop()
}

// pprofSymbol names the functions at the program counters asked for, as
// hex numbers joined by "+" in the query or a POST's body; asked nothing,
// it says that it can.
func pprofSymbol(w http.ResponseWriter, r *http.Request) {
	pprofNoCache(w, "text/plain; charset=utf-8")
	var buf bytes.Buffer
	// go tool pprof checks for this line to see that symbols are served.
	buf.WriteString("num_symbols: 1\n")
	var in *bufio.Reader
	if r.Method == http.MethodPost {
		in = bufio.NewReader(io.LimitReader(r.Body, 1<<20))
	} else {
		in = bufio.NewReader(strings.NewReader(r.URL.RawQuery))
	}
	for {
		word, err := in.ReadSlice('+')
		if err == nil {
			word = word[:len(word)-1]
		}
		if pc, perr := strconv.ParseUint(string(word), 0, 64); perr == nil && pc != 0 {
			if f := runtime.FuncForPC(uintptr(pc)); f != nil {
				fmt.Fprintf(&buf, "%#x %s\n", pc, f.Name())
			}
		}
		if err != nil {
			break
		}
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		return // the client left
	}
}
