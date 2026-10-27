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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gowsTesting "github.com/cloudfra/gowebserver/internal/gowebserver/testing"
	"github.com/cloudfra/ufs"
)

func TestSlowDiskOff(t *testing.T) {
	d := newSlowDisk(SlowDisk{})
	if d != nil {
		t.Fatalf("newSlowDisk(SlowDisk{}) = %+v, want nil", d)
	}
	if got, err := d.fsSpec("/srv/photos"); err != nil || got != "/srv/photos" {
		t.Errorf("fsSpec = %q, %v; want the spec unchanged", got, err)
	}
	next := http.NotFoundHandler()
	if got := d.wrap(next); got == nil {
		t.Error("wrap returned nil")
	}
	// Latency alone: files are sent at full speed.
	if d := newSlowDisk(SlowDisk{Latency: time.Millisecond}); d.conf.Seek != slowDiskDefaultSeek {
		t.Errorf("seek = %s, want the default %s", d.conf.Seek, slowDiskDefaultSeek)
	}
}

func TestSlowDiskLatency(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := newSlowDisk(SlowDisk{Latency: 30 * time.Millisecond})
	spec, err := d.fsSpec(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(spec, "options=") || !strings.Contains(spec, "fault") {
		t.Fatalf("fsSpec = %q, want the fault decorator in its options", spec)
	}
	fsys, err := ufs.New(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer gowsTesting.DeferClose(t, fsys)()
	start := time.Now()
	b, err := fsys.ReadFile("a.txt")
	if err != nil || string(b) != "hello" {
		t.Fatalf("ReadFile = %q, %v", b, err)
	}
	if took := time.Since(start); took < 30*time.Millisecond {
		t.Errorf("reading took %s, want at least the 30ms latency", took)
	}
}

func TestSlowDiskCharge(t *testing.T) {
	d := &slowDisk{conf: SlowDisk{ReadMBps: 100, Seek: 10 * time.Millisecond}}
	mb := time.Duration(float64(slowDiskReadAhead) / 100e6 * float64(time.Second))
	type step struct {
		name     string
		off, n   int64
		wait     time.Duration
		winStart int64
		winEnd   int64
	}
	var ws, we int64
	for i, s := range []step{
		// A first read seeks, and reads ahead a whole read ahead.
		{name: "a", off: 0, n: 32 << 10, wait: 10*time.Millisecond + mb, winStart: 0, winEnd: slowDiskReadAhead},
		// The rest of that comes from the read ahead.
		{name: "a", off: 32 << 10, n: 32 << 10, wait: 0, winStart: 0, winEnd: slowDiskReadAhead},
		// Carrying on from where it ended needs no seek.
		{name: "a", off: slowDiskReadAhead, n: 32 << 10, wait: mb, winStart: slowDiskReadAhead, winEnd: 2 * slowDiskReadAhead},
	} {
		wait, s0, e0 := d.charge(s.name, s.off, s.n, ws, we)
		if wait != s.wait || s0 != s.winStart || e0 != s.winEnd {
			t.Errorf("step %d: charge = %s [%d, %d), want %s [%d, %d)", i, wait, s0, e0, s.wait, s.winStart, s.winEnd)
		}
		ws, we = s0, e0
	}
	// Another file in between means seeking back.
	if wait, _, _ := d.charge("b", 0, 1, 0, 0); wait != 10*time.Millisecond+mb {
		t.Errorf("other file: wait = %s", wait)
	}
	if wait, _, _ := d.charge("a", 2*slowDiskReadAhead, 1, 0, 0); wait != 10*time.Millisecond+mb {
		t.Errorf("back to the first: wait = %s, want a seek", wait)
	}
}

func TestSlowDiskSendsFilesAtItsSpeed(t *testing.T) {
	d := newSlowDisk(SlowDisk{ReadMBps: 20, Seek: time.Millisecond})
	content := bytes.Repeat([]byte("x"), 4<<20)
	h := d.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "big.bin", time.Time{}, bytes.NewReader(content))
	}))
	serve := func(target string) time.Duration {
		start := time.Now()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if n, err := io.Copy(io.Discard, rec.Body); err != nil || n != int64(len(content)) {
			t.Fatalf("GET %s sent %d bytes (%v), want %d", target, n, err, len(content))
		}
		return time.Since(start)
	}
	// 4 MiB at 20 MB/s is about 210ms.
	if took := serve("/big.bin"); took < 150*time.Millisecond {
		t.Errorf("the file took %s, want about 210ms", took)
	}
	// A thumbnail is made from the file, not sent as it is.
	d.last = ""
	serve("/big.bin?width=128")
	if d.last != "" {
		t.Errorf("a thumbnail request was charged to the disk (%q)", d.last)
	}
}

func TestRangeStart(t *testing.T) {
	for h, want := range map[string]int64{
		"":                0,
		"bytes=0-1":       0,
		"bytes=100-":      100,
		"bytes=5-9,20-29": 5,
		"bytes=-500":      0,
		"items=3-4":       0,
		"bytes=x-":        0,
	} {
		if got := rangeStart(h); got != want {
			t.Errorf("rangeStart(%q) = %d, want %d", h, got, want)
		}
	}
}
