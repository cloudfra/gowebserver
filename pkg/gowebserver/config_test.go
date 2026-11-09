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

package gowebserver

import (
	_ "embed"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gowsTesting "github.com/cloudfra/gowebserver/internal/gowebserver/testing"
	"github.com/google/go-cmp/cmp"
)

var (
	//go:embed testdata/empty.yaml
	emptyConfigYaml string
	//go:embed testdata/populated.yaml
	populatedConfigYaml string
	//go:embed testdata/nodefaults.yaml
	noDefaultsConfigFile string
)

func TestEmptyConfig(t *testing.T) {
	conf := &Config{}

	if diff := cmp.Diff(emptyConfigYaml, conf.String(), gowsTesting.IgnoreCarriageReturns()); diff != "" {
		t.Errorf("config.String() mismatch (-want +got):\n%s", diff)
	}
}

func TestPopulatedConfig(t *testing.T) {
	conf := &Config{
		Verbose:      true,
		EnhancedList: true,
		Serve: []Serve{{
			Source:   "/home/folder",
			Endpoint: "/serving",
		}},
		HTTP: HTTP{
			Port: 1000,
		},
		HTTPS: HTTPS{
			Port: 2000,
			Certificate: Certificate{
				RootPrivateKeyFilePath:   "root-private-key.pem",
				RootCertificateFilePath:  "root-public-certificate.pem",
				PrivateKeyFilePath:       "private-key.pem",
				CertificateFilePath:      "public-certificate.pem",
				CertificateHosts:         "gowebserver.com",
				CertificateValidDuration: time.Hour * 24,
				ForceOverwrite:           true,
			},
		},
		Monitoring: Monitoring{
			Metrics: Metrics{
				Enabled: true,
				Path:    "/prometheus",
			},
			Trace: Trace{
				Enabled: true,
				URI:     "remotehost",
			},
		},
		Upload: Serve{
			Source:   "/home/upload",
			Endpoint: "/postage",
		},
		FFmpeg: FFmpeg{
			AcceptLicense:   true,
			Path:            "/usr/bin/ffmpeg",
			InstallOnDemand: true,
			SourceURL:       "https://example.com/ffmpeg.tar.xz",
		},
		Thumbnails: Thumbnails{
			CachePath: "/var/cache/gowebserver/thumbnails.db",
		},
		Debug: Debug{Endpoint: "/debugging", Shutdown: true, Diagnostics: "/var/log/gowebserver-diagnostics.log"},
	}

	if diff := cmp.Diff(populatedConfigYaml, conf.String(), gowsTesting.IgnoreCarriageReturns()); diff != "" {
		t.Errorf("config.String() mismatch (-want +got):\n%s", diff)
		t.Log(populatedConfigYaml)
		t.Log(conf.String())
	}
}

func TestNoDefaultConfig(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "config.yaml")
	gowsTesting.MustFile(t, filename, []byte(noDefaultsConfigFile))

	got := &Config{}

	if err := loadWithConfigFile(filename, got); err != nil {
		t.Fatal(err)
	}

	want := &Config{
		Verbose:      true,
		EnhancedList: true,
		Serve: []Serve{
			{
				Source:   "/home/example",
				Endpoint: "/serving",
			},
		},
		ConfigurationFile: "",
		Debug:             Debug{Endpoint: "/zdebug", Shutdown: true, Diagnostics: "diag.log"},
		HTTP: HTTP{
			Port: 1,
		},
		HTTPS: HTTPS{
			Port: 2,
			Certificate: Certificate{
				RootPrivateKeyFilePath:   "root-private.pem",
				RootCertificateFilePath:  "root-public.pem",
				PrivateKeyFilePath:       "private.pem",
				CertificateFilePath:      "public.pem",
				CertificateHosts:         "hosts",
				CertificateValidDuration: time.Minute,
				ForceOverwrite:           false,
			},
		},
		Monitoring: Monitoring{
			Metrics: Metrics{
				Enabled: false,
				Path:    "/metrics",
			},
			Trace: Trace{
				Enabled: true,
				URI:     "somewhere",
			},
		},
		Upload: Serve{
			Source:   "dropsite",
			Endpoint: "/upload.jspx",
		},
	}

	if diff := cmp.Diff(want, got, gowsTesting.IgnoreCarriageReturns()); diff != "" {
		t.Errorf("config mismatch (-want +got):\n%s", diff)
	}
}

func TestPopulatedYamlConfig(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "config.yaml")
	gowsTesting.MustFile(t, filename, []byte(populatedConfigYaml))

	got := &Config{}

	if err := loadWithConfigFile(filename, got); err != nil {
		t.Fatal(err)
	}

	want := &Config{
		Verbose:      true,
		EnhancedList: true,
		Serve: []Serve{
			{
				Source:   "/home/folder",
				Endpoint: "/serving",
			},
		},
		ConfigurationFile: "",
		HTTP: HTTP{
			Port: 1000,
		},
		HTTPS: HTTPS{
			Port: 2000,
			Certificate: Certificate{
				RootPrivateKeyFilePath:   "root-private-key.pem",
				RootCertificateFilePath:  "root-public-certificate.pem",
				PrivateKeyFilePath:       "private-key.pem",
				CertificateFilePath:      "public-certificate.pem",
				CertificateHosts:         "gowebserver.com",
				CertificateValidDuration: time.Hour * 24,
				ForceOverwrite:           false,
			},
		},
		Monitoring: Monitoring{
			Metrics: Metrics{
				Enabled: true,
				Path:    "/prometheus",
			},
			Trace: Trace{
				Enabled: true,
				URI:     "remotehost",
			},
		},

		Upload: Serve{
			Source:   "/home/upload",
			Endpoint: "/postage",
		},
		FFmpeg: FFmpeg{
			AcceptLicense:   true,
			Path:            "/usr/bin/ffmpeg",
			InstallOnDemand: true,
			SourceURL:       "https://example.com/ffmpeg.tar.xz",
		},
		Thumbnails: Thumbnails{
			CachePath: "/var/cache/gowebserver/thumbnails.db",
		},
		Debug: Debug{Endpoint: "/debugging", Shutdown: true, Diagnostics: "/var/log/gowebserver-diagnostics.log"},
	}

	if diff := cmp.Diff(want, got, gowsTesting.IgnoreCarriageReturns()); diff != "" {
		t.Errorf("config mismatch (-want +got):\n%s", diff)
	}
}

func TestDefaultConfiguration(t *testing.T) {
	got, err := loadFromFlags()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("got is nil")
	}

	want := &Config{
		Verbose:      false,
		EnhancedList: false,
		Serve: []Serve{
			{
				Source:   "",
				Endpoint: "/",
			},
		},
		ConfigurationFile: "",
		HTTP: HTTP{
			Port: *httpPortFlag,
		},
		HTTPS: HTTPS{
			Port: *httpsPortFlag,
			Certificate: Certificate{
				RootPrivateKeyFilePath:   "",
				RootCertificateFilePath:  "",
				PrivateKeyFilePath:       "web.key",
				CertificateFilePath:      "web.cert",
				CertificateHosts:         "",
				CertificateValidDuration: 43800 * time.Hour,
				ForceOverwrite:           false,
			},
		},
		Monitoring: Monitoring{
			Metrics: Metrics{
				Enabled: true,
				Path:    "/metrics",
			},
			Trace: Trace{
				Enabled: false,
				URI:     "",
			},
		},
		Upload: Serve{
			Source:   "uploaded-files",
			Endpoint: "/upload.asp",
		},
		Thumbnails: Thumbnails{
			Enabled: true,
		},
	}

	if diff := cmp.Diff(want, got, gowsTesting.IgnoreCarriageReturns()); diff != "" {
		t.Errorf("config mismatch (-want +got):\n%s", diff)
	}
}

// A configuration file with a setting where it used to be is an error that
// says where it is now, rather than a server started without it.
func TestMovedSettings(t *testing.T) {
	for name, tc := range map[string]struct {
		yaml string
		want []string
	}{
		"debug: true": {
			yaml: "debug: true\n",
			want: []string{`"debug: true" is now "debug:" with "shutdown: true" under it`},
		},
		"monitoring.debugEndpoint": {
			yaml: "monitoring:\n  debugEndpoint: /debug\n  metrics:\n    enabled: true\n",
			want: []string{`"monitoring: debugEndpoint:" is now "debug:" with "endpoint: /debug" under it`},
		},
		"both": {
			yaml: "debug: false\nmonitoring:\n  debugEndpoint: /x\n",
			want: []string{`"debug: false"`, `"endpoint: /x"`},
		},
	} {
		t.Run(name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "config.yaml")
			gowsTesting.MustFile(t, filename, []byte(tc.yaml))
			err := loadWithConfigFile(filename, &Config{})
			if !errors.Is(err, errSettingMoved) {
				t.Fatalf("loading %q: %v, want errSettingMoved", tc.yaml, err)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q: missing %q", err, w)
				}
			}
		})
	}

	// The new block, and a file without either, load.
	for _, ok := range []string{"debug:\n  endpoint: /debug\n  shutdown: true\n", "verbose: true\n", ""} {
		filename := filepath.Join(t.TempDir(), "config.yaml")
		gowsTesting.MustFile(t, filename, []byte(ok))
		if err := loadWithConfigFile(filename, &Config{}); err != nil {
			t.Errorf("loading %q: %v", ok, err)
		}
	}
}
