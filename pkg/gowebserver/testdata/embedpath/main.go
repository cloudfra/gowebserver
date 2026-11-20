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

// Command embedpath embeds gowebserver's handler for a local folder,
// with Options.Path: the package's tests list what it links (go list
// -deps), which is the local driver and no other. With -cache it asks for
// a thumbnail cache file, which needs the bolt driver it doesn't import.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/cloudfra/gowebserver/pkg/gowebserver"
)

func main() {
	dir := flag.String("path", ".", "Folder to serve.")
	cache := flag.String("cache", "", "Thumbnail cache file.")
	flag.Parse()
	media, err := gowebserver.NewHandler(context.Background(), gowebserver.Options{
		Path:          *dir,
		EnhancedIndex: true,
		Thumbnails:    gowebserver.ThumbnailOptions{CachePath: *cache},
	})
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer func() {
		if err := media.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}()
	rec := httptest.NewRecorder()
	media.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	fmt.Println(rec.Code)
}
