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

// Command embedhost is a host program that embeds gowebserver: its own
// API at /api/, a single-page app at / and gowebserver's file browser
// for a folder at /media/, all compressed by gowebserver.Compress. It has
// flags of its own whose names gowebserver's standalone server also uses
// (-path, -verbose, -http.port), which must not collide. The browser
// tests (pkg/gowebserver/template/custom-index/browser/embedded.browser.js)
// run it.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"

	"github.com/cloudfra/gowebserver/pkg/gowebserver"
)

//go:embed spa
var spa embed.FS

var (
	pathFlag        = flag.String("path", "", "Folder to show at /media/.")
	verboseFlag     = flag.Bool("verbose", false, "Log what the media handler does.")
	httpPortFlag    = flag.Int("http.port", 8080, "Port to listen on.")
	titleFlag       = flag.String("title", "Media", "Name of the media folder's root.")
	diagnosticsFlag = flag.String("diagnostics", "", "File for the media handler's diagnostics.")
)

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var logger *slog.Logger
	if *verboseFlag {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	media, err := gowebserver.NewHandler(ctx, gowebserver.Options{
		FS:            os.DirFS(*pathFlag),
		EnhancedIndex: true,
		Title:         *titleFlag,
		Diagnostics:   *diagnosticsFlag,
		Logger:        logger,
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := media.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "closing the media handler:", err)
		}
	}()

	dist, err := fs.Sub(spa, "spa")
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hello", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"message": "hello from the host's API"}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	mux.Handle("/media/", http.StripPrefix("/media", media))
	mux.Handle("/", gowebserver.SPA(dist, gowebserver.SPAOptions{ImmutablePrefix: "assets/"}))

	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *httpPortFlag))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: gowebserver.Compress(mux)}
	go func() {
		<-ctx.Done()
		if err := srv.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "closing the server:", err)
		}
	}()
	fmt.Fprintf(os.Stderr, "serving http://%s/\n", lis.Addr())
	if err := srv.Serve(lis); err != http.ErrServerClosed {
		return err
	}
	return nil
}
