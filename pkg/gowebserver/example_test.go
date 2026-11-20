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

package gowebserver_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing/fstest"

	"github.com/cloudfra/gowebserver/pkg/gowebserver"
)

// A host program with its own API at /api/ and a single-page app at /,
// built by a bundler such as Vite, shows a folder at /media/ with
// gowebserver's file browser and gallery, and compresses it all.
func ExampleNewHandler() {
	// The folder to show: os.DirFS("/data/photos") in a real program, or
	// Path: "/data/photos" for archives and git repositories too.
	photos := fstest.MapFS{"2024/rome.txt": {Data: []byte("notes from Rome")}}
	// The app's build: an embed.FS of its dist folder, say.
	dist := fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>Host app</title>")},
		"assets/app-3f2a1c.js": {Data: []byte("console.log('app')")},
	}

	media, err := gowebserver.NewHandler(context.Background(), gowebserver.Options{
		FS:            photos,
		EnhancedIndex: true,
		Title:         "Photos",
		// Logger, MeterProvider and TracerProvider are the host's to
		// give; left nil, nothing is logged or measured.
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := media.Close(); err != nil {
			log.Print(err)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/hello", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"message":"hello"}`); err != nil {
			log.Print(err)
		}
	})
	mux.Handle("/media/", http.StripPrefix("/media", media))
	mux.Handle("/", gowebserver.SPA(dist, gowebserver.SPAOptions{ImmutablePrefix: "assets/"}))
	srv := httptest.NewServer(gowebserver.Compress(mux)) // http.Server{Handler: ...} in a real program
	defer srv.Close()

	for _, path := range []string{"/api/hello", "/media/2024/rome.txt", "/settings?tab=2", "/assets/app-3f2a1c.js"} {
		if err := show(srv.URL, path); err != nil {
			log.Print(err)
			return
		}
	}
	// Output:
	// /api/hello: 200  "{\"message\":\"hello\"}"
	// /media/2024/rome.txt: 200  "notes from Rome"
	// /settings?tab=2: 200 no-cache "<!doctype html><title>Host app</title>"
	// /assets/app-3f2a1c.js: 200 public, max-age=31536000, immutable "console.log('app')"
}

// show prints what the server at base answers for path.
func show(base, path string) error {
	res, err := http.Get(base + path)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(res.Body)
	if closeErr := res.Body.Close(); err == nil {
		err = closeErr
	}
	fmt.Printf("%s: %d %s %q\n", path, res.StatusCode, res.Header.Get("Cache-Control"), body)
	return err
}
