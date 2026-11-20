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

// Command embedfs is the smallest program that embeds gowebserver's
// handlers with an fs.FS: NewHandler, SPA, Compress and Mount. The
// package's tests list what it links (go list -deps) and run it with
// -check, which prints what importing the package put on
// http.DefaultServeMux.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing/fstest"

	"github.com/cloudfra/gowebserver/pkg/gowebserver"
)

func main() {
	check := flag.Bool("check", false, "Print what http.DefaultServeMux routes, one pattern a line.")
	flag.Parse()
	if *check {
		for _, p := range []string{"/", "/debug/pprof/", "/debug/pprof/heap", "/debug/vars", "/metrics"} {
			if _, pattern := http.DefaultServeMux.Handler(httptest.NewRequest(http.MethodGet, p, nil)); pattern != "" {
				fmt.Println(pattern)
			}
		}
		return
	}
	media, err := gowebserver.NewHandler(context.Background(), gowebserver.Options{
		FS:            fstest.MapFS{"a.txt": {Data: []byte("a")}},
		EnhancedIndex: true,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() {
		if err := media.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}()
	mux := http.NewServeMux()
	gowebserver.Mount(mux, "/media", media)
	mux.Handle("/", gowebserver.SPA(fstest.MapFS{"index.html": {Data: []byte("app")}}, gowebserver.SPAOptions{}))
	rec := httptest.NewRecorder()
	gowebserver.Compress(mux).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/media/a.txt", nil))
	fmt.Println(rec.Code, rec.Body.String())
}
