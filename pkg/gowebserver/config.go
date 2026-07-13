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
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

var (
	// Serving Flags
	pathFlag       = flag.String("path", "", "Path to serve (local filesystem, git, zip, tarball files).")
	servePathFlag  = flag.String("servepath", "/", "The HTTP/HTTPS serving root path for the hosted path.")
	configFileFlag = flag.String("configfile", "", "YAML formatted configuration file. (overrides flag values)")
	verboseFlag    = flag.Bool("verbose", false, "Print out extra information.")

	// Upload Flags
	uploadPathFlag     = flag.String("upload.path", "uploaded-files", "Local filesystem path where uploaded files are placed.")
	uploadHTTPPathFlag = flag.String("upload.httppath", "/upload.asp", "The URL path for uploading files.")

	// HTTP Flags
	httpPortFlag *int

	// HTTPS Flags
	httpsPortFlag *int

	// HTTPS Certificate Flags
	rootPrivateKeyFilePathFlag  = flag.String("https.certificate.rootprivatekey", "", "(optional) Root private key file path for generating derived certificates.")
	rootCertificateFilePathFlag = flag.String("https.certificate.rootpath", "", "(optional) Root public certificate for derived certificates.")
	privateKeyFilePathFlag      = flag.String("https.certificate.privatekey", "web.key", "Private key for HTTPS serving.")
	certificateFilePathFlag     = flag.String("https.certificate.path", "web.cert", "Certificate to host HTTPS with.")
	certHostsFlag               = flag.String("https.certificate.hosts", "", "Comma-separated hostnames and IPs to generate a certificate for.")
	validDurationFlag           = flag.Duration("https.certificate.duration", time.Hour*43800, "Validity period of the generated certificate; the default 43800h is approximately 5 years.")
	forceOverwriteCertFlag      = flag.Bool("https.certificate.forceoverwrite", false, "Force overwrite existing certificates if they already exist.")

	// Monitoring Flags
	monitoringDebugEndpointFlag = flag.String("monitoring.debugendpoint", "", "URL path prefix for pprof and OpenTelemetry tracez debug endpoints. Leave empty to disable these endpoints.")
	monitoringTraceURIFlag      = flag.String("monitoring.trace.uri", "", "OTLP HTTP endpoint URL for tracing (e.g. http://host:4318).")
	monitoringMetricsPath       = flag.String("monitoring.metrics.path", "/metrics", "The URL path for exporting server metrics for Prometheus monitoring.")

	enhancedListFlag = flag.Bool("enhancedindex", false, "Enable the enhanced directory listing UI with file previews and sorting.")
	debugFlag        = flag.Bool("debug", false, "Expose the /diediedie shutdown endpoint for testing.")
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
	DebugEndpoint string  `yaml:"debugEndpoint"`
	Metrics       Metrics `yaml:"metrics"`
	Trace         Trace   `yaml:"trace"`
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
	Debug             bool    `yaml:"debug"`

	HTTP       HTTP       `yaml:"http"`
	HTTPS      HTTPS      `yaml:"https"`
	Monitoring Monitoring `yaml:"monitoring"`
	Upload     Serve      `yaml:"upload"`
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
	e.Close()
	return b.String()
}

// Load loads the configuration for the server.
func Load() (*Config, error) {
	flag.Parse()
	conf, err := loadFromFlags()
	if err != nil {
		return nil, err
	}
	if *configFileFlag != "" {
		err := loadWithConfigFile(*configFileFlag, conf)
		if err != nil {
			zap.S().With("error", err, "configFile", *configFileFlag).Warn("Error Loading File")
		}
	}
	return conf, nil
}

func loadWithConfigFile(filePath string, conf *Config) error {
	contents, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	err = yaml.Unmarshal(contents, &conf)
	// Config file should always be the flag value.
	conf.ConfigurationFile = *configFileFlag
	if err != nil {
		return err
	}
	return nil
}

func init() {
	defaultPortInt := 8080
	defaultSecurePortInt := 8443
	currentUser, err := user.Current()
	if err == nil {
		if currentUser.Uid == "0" {
			defaultPortInt = 80
			defaultSecurePortInt = 443
		}
	}

	defaultPort := os.Getenv("PORT")
	if defaultPort != "" {
		port, err := strconv.Atoi(defaultPort)
		if err == nil {
			defaultPortInt = port
		}
	}

	httpPortFlag = flag.Int("http.port", defaultPortInt, "Port to run HTTP server.")
	httpsPortFlag = flag.Int("https.port", defaultSecurePortInt, "Port to run HTTPS server.")
}

func loadFromFlags() (*Config, error) {
	sl, err := serveList(*pathFlag, *servePathFlag)
	if err != nil {
		return nil, err
	}
	return &Config{
		Verbose:           *verboseFlag,
		Serve:             sl,
		ConfigurationFile: *configFileFlag,
		EnhancedList:      *enhancedListFlag,
		Debug:             *debugFlag,
		HTTP: HTTP{
			Port: *httpPortFlag,
		},
		HTTPS: HTTPS{
			Port: *httpsPortFlag,
			Certificate: Certificate{
				PrivateKeyFilePath:       *privateKeyFilePathFlag,
				CertificateFilePath:      *certificateFilePathFlag,
				RootPrivateKeyFilePath:   *rootPrivateKeyFilePathFlag,
				RootCertificateFilePath:  *rootCertificateFilePathFlag,
				CertificateHosts:         *certHostsFlag,
				CertificateValidDuration: *validDurationFlag,
				ForceOverwrite:           *forceOverwriteCertFlag,
			},
		},
		Monitoring: Monitoring{
			DebugEndpoint: *monitoringDebugEndpointFlag,
			Metrics: Metrics{
				Enabled: *monitoringMetricsPath != "",
				Path:    *monitoringMetricsPath,
			},
			Trace: Trace{
				Enabled: *monitoringTraceURIFlag != "",
				URI:     *monitoringTraceURIFlag,
			},
		},
		Upload: Serve{
			Source:   *uploadPathFlag,
			Endpoint: *uploadHTTPPathFlag,
		},
	}, nil
}

func serveList(paths string, servePaths string) ([]Serve, error) {
	pl := strings.Split(paths, ",")
	spl := strings.Split(servePaths, ",")

	if len(pl) != len(spl) {
		return []Serve{}, fmt.Errorf("-path and -servepath are different lengths")
	}

	sl := []Serve{}
	for i, p := range pl {
		sl = append(sl, Serve{
			Source:   p,
			Endpoint: spl[i],
		})
	}

	return sl, nil
}
