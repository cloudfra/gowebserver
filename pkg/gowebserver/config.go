// Copyright 2022 Cloudfra
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

// Package gowebserver serves files, folders and archives over HTTP: the
// file browser and gallery, thumbnails, the text viewer and uploads.
// NewHandler is all of it as an http.Handler for another program to
// mount; SPA serves a single-page app's build and Compress compresses a
// whole mux. A program that embeds these links only what they need (see
// NewHandler). The standalone server, with its command line,
// configuration file, exporters and listeners, is package server.
package gowebserver

import (
	"bytes"
	"log/slog"
	"time"

	"gopkg.in/yaml.v3"
)

// HTTP holds the configuration for HTTP serving.
type HTTP struct {
	Port int `yaml:"port"`
}

// HTTPS holds the configuration for HTTPS serving.
type HTTPS struct {
	Port        int         `yaml:"port"`
	Certificate Certificate `yaml:"certificate"`
}

// Certificate holds the certificate/private key configuration for HTTPS.
type Certificate struct {
	RootPrivateKeyFilePath   string        `yaml:"rootPrivateKey"`
	RootCertificateFilePath  string        `yaml:"rootPath"`
	PrivateKeyFilePath       string        `yaml:"privateKey"`
	CertificateFilePath      string        `yaml:"path"`
	CertificateHosts         string        `yaml:"hosts"`
	CertificateValidDuration time.Duration `yaml:"duration"`
	ForceOverwrite           bool          `yaml:"-"`
}

// Monitoring holds the monitoring configuration.
type Monitoring struct {
	Metrics Metrics `yaml:"metrics"`
	Trace   Trace   `yaml:"trace"`
}

// Debug holds what's only for debugging and diagnosing the server; all of
// it is off unless set.
type Debug struct {
	// Endpoint is the URL path prefix for the pprof and OpenTelemetry
	// tracez endpoints. Empty turns them off.
	Endpoint string `yaml:"endpoint"`
	// Shutdown exposes /diediedie, which shuts the server down, for tests.
	Shutdown bool `yaml:"shutdown"`
	// Diagnostics is a file to write anonymized diagnostics of browsing to
	// (see diagnostics.go). Empty turns them off.
	Diagnostics string `yaml:"diagnostics"`
}

// Trace holds the trace configuration.
type Trace struct {
	Enabled bool   `yaml:"enabled"`
	URI     string `yaml:"uri"`
}

// Metrics holds the metrics configuration.
type Metrics struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
}

// Config is the root of the server configuration.
type Config struct {
	Verbose           bool    `yaml:"verbose"`
	Serve             []Serve `yaml:"serve"`
	ConfigurationFile string  `yaml:"-"`
	EnhancedList      bool    `yaml:"enhancedList"`

	HTTP       HTTP       `yaml:"http"`
	HTTPS      HTTPS      `yaml:"https"`
	Monitoring Monitoring `yaml:"monitoring"`
	Upload     Serve      `yaml:"upload"`
	FFmpeg     FFmpeg     `yaml:"ffmpeg"`
	Thumbnails Thumbnails `yaml:"thumbnails"`
	Debug      Debug      `yaml:"debug"`
}

// FFmpeg configures the ffmpeg binary used for video features.
type FFmpeg struct {
	// AcceptLicense accepts ffmpeg's license; the downloadable builds are
	// GPL. ffmpeg is never downloaded without it.
	AcceptLicense bool `yaml:"acceptLicense"`
	// Path is the ffmpeg program to use, or the folder it's in (it's
	// looked for there and in the folder's bin folder). Empty uses the
	// installed ffmpeg, if there is one. When set, the server won't start
	// unless it finds ffmpeg there and it runs.
	Path string `yaml:"path"`
	// InstallOnDemand downloads ffmpeg the first time it's needed when
	// none is installed. Requires AcceptLicense.
	InstallOnDemand bool `yaml:"installOnDemand"`
	// SourceURL is the ffmpeg build to download (.tar.xz, .tar.gz or
	// .zip). Append #sha256=<hex> to verify it. Empty downloads the latest
	// release for this platform.
	SourceURL string `yaml:"sourceUrl"`
	// Required, for an embedded handler (Options.FFmpeg), fails
	// NewHandler when Path is set but has no ffmpeg that runs. Without
	// it, that's logged once and the handler goes without ffmpeg. The
	// standalone server always requires a configured path.
	Required bool `yaml:"-"`
}

// Thumbnails configures resized images and video frames, served when a
// file is requested with width or height query parameters.
type Thumbnails struct {
	// Enabled turns thumbnails on. Video thumbnails also need ffmpeg; see
	// FFmpeg.
	Enabled bool `yaml:"enabled"`
	// CachePath is the BoltDB file that caches thumbnails across restarts.
	// Empty uses <user cache dir>/gowebserver/thumbnails.db; "memory:"
	// keeps them in memory only, for the life of the server.
	CachePath string `yaml:"cachePath"`
}

// Serve maps the source to endpoint serving of content.
type Serve struct {
	// Source location to serve.
	Source string `yaml:"source"`
	// Endpoint on the HTTP server to serve the content.
	Endpoint string `yaml:"endpoint"`
}

// String returns a string representation of the config.
func (c *Config) String() string {
	b := &bytes.Buffer{}
	e := yaml.NewEncoder(b)
	e.SetIndent(2)
	if err := e.Encode(c); err != nil {
		return err.Error()
	}
	if err := e.Close(); err != nil {
		slog.Error("failed to close YAML encoder while rendering config string", "error", err)
	}
	return b.String()
}
