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
	"os"
	"testing"
	"time"

	_ "embed"

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

	if diff := cmp.Diff(emptyConfigYaml, conf.String()); diff != "" {
		t.Errorf("config.String() mismatch (-want +got):\n%s", diff)
	}
}

func TestPopulatedConfig(t *testing.T) {
	conf := &Config{
		Verbose:      true,
		EnhancedList: true,
		Debug:        true,
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
			DebugEndpoint: "/debugging",
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
	}

	if diff := cmp.Diff(populatedConfigYaml, conf.String()); diff != "" {
		t.Errorf("config.String() mismatch (-want +got):\n%s", diff)
		t.Log(populatedConfigYaml)
		t.Log(conf.String())
	}
}

func TestNoDefaultConfig(t *testing.T) {
	fp, err := writeTempFile(noDefaultsConfigFile)
	defer os.Remove(fp.Name())
	if err != nil {
		t.Fatal(err)
	}

	got := &Config{}
	err = loadWithConfigFile(fp.Name(), got)
	if err != nil {
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
		Debug:             true,
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
			DebugEndpoint: "/zdebug",
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

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("config mismatch (-want +got):\n%s", diff)
	}
}

func TestPopulatedYamlConfig(t *testing.T) {
	fp, err := writeTempFile(populatedConfigYaml)
	defer os.Remove(fp.Name())
	if err != nil {
		t.Fatal(err)
	}

	got := &Config{}
	err = loadWithConfigFile(fp.Name(), got)
	if err != nil {
		t.Fatal(err)
	}

	want := &Config{
		Verbose:      true,
		EnhancedList: true,
		Debug:        true,
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
			DebugEndpoint: "/debugging",
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
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("config mismatch (-want +got):\n%s", diff)
	}
}

func createTempFile() (*os.File, error) {
	return os.CreateTemp(os.TempDir(), "tempfile")
}

func writeTempFile(content string) (*os.File, error) {
	fp, err := createTempFile()
	if err != nil {
		return fp, err
	}
	err = os.WriteFile(fp.Name(), []byte(content), os.FileMode(0o644))
	return fp, err
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
			DebugEndpoint: "",
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
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("config mismatch (-want +got):\n%s", diff)
	}
}
