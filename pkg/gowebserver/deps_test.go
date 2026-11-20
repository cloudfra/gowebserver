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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// forbiddenDeps are what only the standalone server (package server)
// needs, or what a program that embeds the handlers would rather not
// link: by import path prefix.
var forbiddenDeps = []string{
	"cloud.google.com/",
	"google.golang.org/grpc",
	"google.golang.org/api",
	"github.com/go-git/",
	"go.opentelemetry.io/otel/exporters/",
	"go.opentelemetry.io/otel/sdk",
	"go.opentelemetry.io/contrib/zpages",
	"go.opentelemetry.io/contrib/instrumentation/host",
	"go.opentelemetry.io/contrib/instrumentation/runtime",
	"github.com/prometheus/",
	"go.uber.org/zap",
	"github.com/shirou/gopsutil",
	"github.com/cloudfra/certtool",
	"github.com/cloudfra/gomain",
	"filippo.io/age",
	"github.com/rs/cors",
	"net/http/pprof",
	"go.etcd.io/bbolt",
	"github.com/cloudfra/gowebserver/pkg/gowebserver/server",
}

// allowedDrivers are the ufs drivers an embedding program links: the
// local folder and archive one, and the packages it's made of.
var allowedDrivers = map[string]bool{
	"github.com/cloudfra/ufs/drivers/core":           true,
	"github.com/cloudfra/ufs/drivers/types":          true,
	"github.com/cloudfra/ufs/drivers/polyfills":      true,
	"github.com/cloudfra/ufs/drivers/common/buffile": true,
}

func goTool(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("runs the go tool")
	}
	g, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool:", err)
	}
	return g
}

// A program that embeds the handlers (testdata/embedfs, with an fs.FS,
// and testdata/embedpath, with a local Path) links none of the
// standalone server's exporters, drivers, logging or certificates.
func TestEmbeddingDeps(t *testing.T) {
	g := goTool(t)
	for _, prog := range []string{"embedfs", "embedpath"} {
		out, err := exec.CommandContext(t.Context(), g, "list", "-deps", "./testdata/"+prog).Output()
		if err != nil {
			t.Fatalf("go list %s: %v", prog, err)
		}
		deps := strings.Fields(string(out))
		for _, d := range deps {
			for _, f := range forbiddenDeps {
				if strings.HasPrefix(d, f) {
					t.Errorf("%s links %s", prog, d)
				}
			}
			if strings.HasPrefix(d, "github.com/cloudfra/ufs/drivers/") && !allowedDrivers[d] {
				t.Errorf("%s links the driver %s", prog, d)
			}
		}
		if len(deps) > 500 {
			t.Errorf("%s links %d packages; the standalone server's handlers alone were about 1100", prog, len(deps))
		}
		t.Logf("%s links %d packages", prog, len(deps))
	}
}

// What importing the package puts on http.DefaultServeMux: no pprof, no
// metrics. The one route is expvar's /debug/vars, which comes with ufs's
// archive support (mholt/archives, then bodgit/sevenzip, then
// go4.org/readerutil, which imports expvar), not from this module.
func TestEmbeddingDefaultServeMux(t *testing.T) {
	g := goTool(t)
	bin := filepath.Join(t.TempDir(), "embedfs")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.CommandContext(t.Context(), g, "build", "-o", bin, "./testdata/embedfs").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	out, err := exec.CommandContext(t.Context(), bin, "-check").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(string(out)); len(got) > 0 && strings.Join(got, " ") != "GET /debug/vars" {
		t.Errorf("http.DefaultServeMux routes %v", got)
	}
	who, err := exec.CommandContext(t.Context(), g, "list", "-deps", "-f", "{{.ImportPath}} {{join .Imports \" \"}}", "./testdata/embedfs").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(who), "\n") {
		f := strings.Fields(line)
		for _, imp := range f[min(1, len(f)):] {
			if imp == "expvar" && f[0] != "go4.org/readerutil" {
				t.Errorf("%s imports expvar", f[0])
			}
		}
	}
}

// A thumbnail cache file in a program without ufs's bolt driver is an
// error that says what to import, not a cache quietly kept in memory.
func TestEmbeddingCacheNeedsBolt(t *testing.T) {
	g := goTool(t)
	dir := t.TempDir()
	out, err := exec.CommandContext(t.Context(), g, "run", "./testdata/embedpath", "-path", dir, "-cache", filepath.Join(dir, "thumbs.db")).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "drivers/boltfs") {
		t.Errorf("err %v, output:\n%s", err, out)
	}
	out, err = exec.CommandContext(t.Context(), g, "run", "./testdata/embedpath", "-path", dir).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "200" {
		t.Errorf("a local Path: %v\n%s", err, out)
	}
}
