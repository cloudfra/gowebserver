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
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudfra/certtool/pkg/certtool"
	gomainTesting "github.com/jeremyje/gomain/testing"
)

func TestBuildCertificateHostnames(t *testing.T) {
	localHostname, _ := os.Hostname()

	tests := []struct {
		name        string
		hosts       string
		mustHave    []string
		mustNotHave []string
	}{
		{
			name:     "empty hosts includes machine hostname and local IPs",
			hosts:    "",
			mustHave: []string{localHostname},
		},
		{
			name:     "explicit hosts are included",
			hosts:    "myserver,10.0.0.1",
			mustHave: []string{"myserver", "10.0.0.1", localHostname},
		},
		{
			name:        "loopback excluded from auto-detection",
			hosts:       "",
			mustNotHave: []string{"::1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conf := &Config{
				HTTPS: HTTPS{
					Certificate: Certificate{
						CertificateHosts: tc.hosts,
					},
				},
			}
			got := buildCertificateHostnames(conf)
			gotSet := map[string]bool{}
			for _, h := range got {
				gotSet[h] = true
			}

			for _, want := range tc.mustHave {
				if !gotSet[want] {
					t.Errorf("expected %q in hostnames %v", want, got)
				}
			}
			for _, unwanted := range tc.mustNotHave {
				if gotSet[unwanted] {
					t.Errorf("did not expect %q in hostnames %v", unwanted, got)
				}
			}

			// No duplicates
			seen := map[string]bool{}
			for _, h := range got {
				if seen[h] {
					t.Errorf("duplicate hostname %q in %v", h, got)
				}
				seen[h] = true
			}
		})
	}

	// Verify the result is usable for cert generation: all non-loopback local IPs
	// should appear so the cert is valid for any local-network address.
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skip("cannot get interface addresses:", err)
	}
	conf := &Config{HTTPS: HTTPS{Certificate: Certificate{}}}
	got := buildCertificateHostnames(conf)
	gotSet := map[string]bool{}
	for _, h := range got {
		gotSet[h] = true
	}
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil || ip.IsLoopback() {
			continue
		}
		if !gotSet[ip.String()] {
			t.Errorf("local IP %s missing from cert hostnames %v", ip, got)
		}
	}
}

func TestConfigLogger(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		logger, close := configLogger(verbose)
		if logger == nil {
			t.Error("logger is nil")
		}
		close()
	}
}

func TestRunApplication(t *testing.T) {
	httpPortFlag = new(int)
	httpsPortFlag = new(int)
	close := gomainTesting.Main(runApplication)

	ch := make(chan error)
	go func() {
		time.Sleep(time.Second)
		ch <- close()
	}()
	err := <-ch
	if err != nil {
		if !strings.Contains(err.Error(), "closed network connection") {
			t.Error(err)
		}
	}
}

func TestCreateCertificate(t *testing.T) {
	dir := mustTempDir(t)
	cfg := &Config{
		Verbose:           false,
		Serve:             []Serve{},
		ConfigurationFile: "",
		HTTP:              HTTP{},
		HTTPS: HTTPS{
			Port: 0,
			Certificate: Certificate{
				RootPrivateKeyFilePath:   filepath.Join(dir, "root-private.key"),
				RootCertificateFilePath:  filepath.Join(dir, "root-public.cert"),
				PrivateKeyFilePath:       filepath.Join(dir, "private.key"),
				CertificateFilePath:      filepath.Join(dir, "public.cert"),
				CertificateHosts:         "localhost",
				CertificateValidDuration: time.Hour,
				ForceOverwrite:           true,
			},
		},
		Monitoring: Monitoring{
			Metrics: Metrics{},
		},
		Upload: Serve{},
	}
	if err := createCertificate(cfg); err != nil {
		t.Error(err)
	}

	rootPub, _, err := certtool.ReadKeyPair(mustFile(t, filepath.Join(dir, "root-public.cert")), mustFile(t, filepath.Join(dir, "root-private.key")))
	if err != nil {
		t.Error(err)
	}

	pub, _, err := certtool.ReadKeyPair(mustFile(t, filepath.Join(dir, "public.cert")), mustFile(t, filepath.Join(dir, "private.key")))
	if err != nil {
		t.Error(err)
	}
	if err := pub.CheckSignatureFrom(rootPub); err != nil {
		t.Error(err)
	}
}

func ExampleWebServer_Serve() {
	conf := &Config{
		Verbose: false,
		HTTP: HTTP{
			Port: 0,
		},
		HTTPS: HTTPS{
			Port: 0,
		},
	}

	logger, syncFunc := configLogger(conf.Verbose)
	defer syncFunc()

	httpServer, err := New(conf)
	if err != nil {
		logger.Sugar().Fatal(err)
	}

	termCh := make(chan error)
	go func() {
		time.Sleep(time.Second)
		termCh <- nil
	}()

	close := gomainTesting.Main(httpServer.Serve)
	close()
	// Output:
}

func TestWebServerFull(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	backback := filepath.Dir(filepath.Dir(cwd))
	conf := &Config{
		Verbose: true,
		Serve: []Serve{
			{Source: cwd, Endpoint: "/cwd"},
			{Source: backback, Endpoint: "/root"},
		},
		EnhancedList: true,
		Debug:        true,
		HTTP:         HTTP{Port: 8082},
		HTTPS:        HTTPS{Port: 0},
		Monitoring: Monitoring{
			DebugEndpoint: "/debug",
			Metrics: Metrics{
				Enabled: true,
				Path:    "/metrics",
			},
			Trace: Trace{
				Enabled: true,
				URI:     "http://jaeger:4318/v1/traces",
			},
		},
		Upload: Serve{
			Source:   "/tmp/upload",
			Endpoint: "/upload",
		},
	}

	logger, syncFunc := configLogger(conf.Verbose)
	defer syncFunc()

	httpServer, err := New(conf)
	if err != nil {
		logger.Sugar().Fatal(err)
	}

	close := gomainTesting.Main(httpServer.Serve)
	time.Sleep(time.Second)
	close()
	// Output:
}
