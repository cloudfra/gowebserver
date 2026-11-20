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
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// flags are the standalone server's command line. They are registered on
// a FlagSet that Load makes, never on flag.CommandLine, so importing the
// package adds no flags to a program's own.
type flags struct {
	pathFlag                    *string
	servePathFlag               *string
	configFileFlag              *string
	verboseFlag                 *bool
	uploadPathFlag              *string
	uploadHTTPPathFlag          *string
	httpPortFlag                *int
	httpsPortFlag               *int
	rootPrivateKeyFilePathFlag  *string
	rootCertificateFilePathFlag *string
	privateKeyFilePathFlag      *string
	certificateFilePathFlag     *string
	certHostsFlag               *string
	validDurationFlag           *time.Duration
	forceOverwriteCertFlag      *bool
	monitoringTraceURIFlag      *string
	monitoringMetricsPath       *string
	debugEndpointFlag           *string
	debugShutdownFlag           *bool
	debugDiagnosticsFlag        *string
	enhancedListFlag            *bool
	ffmpegAcceptLicenseFlag     *bool
	ffmpegPathFlag              *string
	ffmpegInstallOnDemandFlag   *bool
	ffmpegSourceURLFlag         *string
	thumbnailsEnabledFlag       *bool
	thumbnailsCachePathFlag     *string
}

// registerFlags registers the server's flags on fs.
func registerFlags(fs *flag.FlagSet) *flags {
	f := &flags{}
	// Serving Flags
	f.pathFlag = fs.String("path", "", "Path to serve (local filesystem, git, zip, tarball files).")
	f.servePathFlag = fs.String("servepath", "/", "The HTTP/HTTPS serving root path for the hosted path.")
	f.configFileFlag = fs.String("configfile", "", "YAML formatted configuration file. (overrides flag values)")
	f.verboseFlag = fs.Bool("verbose", false, "Print out extra information.")

	// Upload Flags
	f.uploadPathFlag = fs.String("upload.path", "uploaded-files", "Local filesystem path where uploaded files are placed.")
	f.uploadHTTPPathFlag = fs.String("upload.httppath", "/upload.asp", "The URL path for uploading files.")

	// HTTPS Certificate Flags
	f.rootPrivateKeyFilePathFlag = fs.String("https.certificate.rootprivatekey", "", "(optional) Root private key file path for generating derived certificates.")
	f.rootCertificateFilePathFlag = fs.String("https.certificate.rootpath", "", "(optional) Root public certificate for derived certificates.")
	f.privateKeyFilePathFlag = fs.String("https.certificate.privatekey", "web.key", "Private key for HTTPS serving.")
	f.certificateFilePathFlag = fs.String("https.certificate.path", "web.cert", "Certificate to host HTTPS with.")
	f.certHostsFlag = fs.String("https.certificate.hosts", "", "Comma-separated hostnames and IPs to generate a certificate for.")
	f.validDurationFlag = fs.Duration("https.certificate.duration", time.Hour*43800, "Validity period of the generated certificate; the default 43800h is approximately 5 years.")
	f.forceOverwriteCertFlag = fs.Bool("https.certificate.forceoverwrite", false, "Force overwrite existing certificates if they already exist.")

	// Monitoring Flags
	f.monitoringTraceURIFlag = fs.String("monitoring.trace.uri", "", "OTLP HTTP endpoint URL for tracing (e.g. http://host:4318).")
	f.monitoringMetricsPath = fs.String("monitoring.metrics.path", "/metrics", "The URL path for exporting server metrics for Prometheus monitoring.")

	// Debug Flags
	f.debugEndpointFlag = fs.String("debug.endpoint", "", "URL path prefix for the pprof and OpenTelemetry tracez debug endpoints. Empty turns them off.")
	f.debugShutdownFlag = fs.Bool("debug.shutdown", false, "Expose the /diediedie endpoint, which shuts the server down; for testing.")
	f.debugDiagnosticsFlag = fs.String("debug.diagnostics", "", "Write anonymized diagnostics of browsing (thumbnails, ffmpeg, what the file browser loads and shows) to this file, one event a line. Names are replaced by tokens. Empty turns them off.")

	f.enhancedListFlag = fs.Bool("enhancedindex", false, "Enable the enhanced directory listing UI with file previews and sorting.")

	// FFmpeg Flags
	f.ffmpegAcceptLicenseFlag = fs.Bool("ffmpeg.acceptlicense", false, "Accept ffmpeg's license (downloadable builds are GPL). Required before ffmpeg is downloaded.")
	f.ffmpegPathFlag = fs.String("ffmpeg.path", "", "ffmpeg program to use, or the folder it's in (also searched: its bin folder). Empty uses the installed ffmpeg, if any. When set, the server won't start unless ffmpeg is there and runs.")
	f.ffmpegInstallOnDemandFlag = fs.Bool("ffmpeg.installondemand", false, "Download ffmpeg the first time it's needed when none is installed. Requires -ffmpeg.acceptlicense.")
	f.ffmpegSourceURLFlag = fs.String("ffmpeg.sourceurl", "", "ffmpeg build to download (.tar.xz, .tar.gz or .zip); append #sha256=<hex> to verify it. Empty downloads the latest release for this platform.")

	// Thumbnail Flags
	f.thumbnailsEnabledFlag = fs.Bool("thumbnails.enabled", true, "Serve resized images and video frames when a file is requested with width or height query parameters.")
	f.thumbnailsCachePathFlag = fs.String("thumbnails.cachepath", "", "BoltDB file that caches thumbnails across restarts. Empty uses <user cache dir>/gowebserver/thumbnails.db; \"memory:\" keeps them in memory only.")

	defaultPort, defaultSecurePort := defaultPorts()
	f.httpPortFlag = fs.Int("http.port", defaultPort, "Port to run HTTP server.")
	f.httpsPortFlag = fs.Int("https.port", defaultSecurePort, "Port to run HTTPS server.")
	return f
}

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

// Load loads the configuration for the server from the command line,
// os.Args, and the configuration file it names. The flags are the
// server's own, on a FlagSet of their own: flag.CommandLine isn't
// parsed, so a program's flags (or those of the test binary) aren't
// accepted, and a program that imports the package keeps only its own.
func Load() (*Config, error) {
	return loadArgs(os.Args[1:])
}

// loadArgs loads the configuration from the command line args, parsed by
// a FlagSet of the server's own, and the configuration file they name.
func loadArgs(args []string) (*Config, error) {
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	f := registerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	conf, err := f.config()
	if err != nil {
		return nil, err
	}
	if conf.ConfigurationFile != "" {
		err := loadWithConfigFile(conf.ConfigurationFile, conf)
		if errors.Is(err, errSettingMoved) {
			// The rest of the file would load, and the server start without
			// what was asked for.
			return nil, fmt.Errorf("configuration file %s: %w", conf.ConfigurationFile, err)
		}
		if err != nil {
			slog.Warn("Error Loading File", "error", err, "configFile", conf.ConfigurationFile)
		}
	}
	return conf, nil
}

func loadWithConfigFile(filePath string, conf *Config) error {
	contents, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	if err := movedSettings(contents); err != nil {
		return err
	}
	// Config file should always be the flag value.
	configFile := conf.ConfigurationFile
	err = yaml.Unmarshal(contents, &conf)
	conf.ConfigurationFile = configFile
	if err != nil {
		return err
	}
	return nil
}

// errSettingMoved is a configuration file that uses a setting where it
// used to be.
var errSettingMoved = errors.New("a setting has moved")

// movedSettings is an error naming each setting in the configuration file
// contents that has moved, and where to, or nil.
func movedSettings(contents []byte) error {
	var old struct {
		Debug      yaml.Node `yaml:"debug"`
		Monitoring struct {
			DebugEndpoint *string `yaml:"debugEndpoint"`
		} `yaml:"monitoring"`
	}
	if err := yaml.Unmarshal(contents, &old); err != nil {
		return nil // loading the file says what's wrong with it
	}
	var errs []error
	if old.Debug.Kind == yaml.ScalarNode {
		errs = append(errs, fmt.Errorf("%w: \"debug: %s\" is now \"debug:\" with \"shutdown: %s\" under it", errSettingMoved, old.Debug.Value, old.Debug.Value))
	}
	if old.Monitoring.DebugEndpoint != nil {
		errs = append(errs, fmt.Errorf("%w: \"monitoring: debugEndpoint:\" is now \"debug:\" with \"endpoint: %s\" under it", errSettingMoved, *old.Monitoring.DebugEndpoint))
	}
	return errors.Join(errs...)
}

// defaultPorts are the HTTP and HTTPS ports used unless set: 80 and 443
// for root, else 8080 and 8443, and the PORT environment variable's for
// HTTP when it's set.
func defaultPorts() (int, int) {
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
	return defaultPortInt, defaultSecurePortInt
}

// config is the configuration the flags give.
func (f *flags) config() (*Config, error) {
	sl, err := serveList(*f.pathFlag, *f.servePathFlag)
	if err != nil {
		return nil, err
	}
	return &Config{
		Verbose:           *f.verboseFlag,
		Serve:             sl,
		ConfigurationFile: *f.configFileFlag,
		EnhancedList:      *f.enhancedListFlag,
		HTTP: HTTP{
			Port: *f.httpPortFlag,
		},
		HTTPS: HTTPS{
			Port: *f.httpsPortFlag,
			Certificate: Certificate{
				PrivateKeyFilePath:       *f.privateKeyFilePathFlag,
				CertificateFilePath:      *f.certificateFilePathFlag,
				RootPrivateKeyFilePath:   *f.rootPrivateKeyFilePathFlag,
				RootCertificateFilePath:  *f.rootCertificateFilePathFlag,
				CertificateHosts:         *f.certHostsFlag,
				CertificateValidDuration: *f.validDurationFlag,
				ForceOverwrite:           *f.forceOverwriteCertFlag,
			},
		},
		Debug: Debug{
			Endpoint:    *f.debugEndpointFlag,
			Shutdown:    *f.debugShutdownFlag,
			Diagnostics: *f.debugDiagnosticsFlag,
		},
		Monitoring: Monitoring{
			Metrics: Metrics{
				Enabled: *f.monitoringMetricsPath != "",
				Path:    *f.monitoringMetricsPath,
			},
			Trace: Trace{
				Enabled: *f.monitoringTraceURIFlag != "",
				URI:     *f.monitoringTraceURIFlag,
			},
		},
		Upload: Serve{
			Source:   *f.uploadPathFlag,
			Endpoint: *f.uploadHTTPPathFlag,
		},
		FFmpeg: FFmpeg{
			AcceptLicense:   *f.ffmpegAcceptLicenseFlag,
			Path:            *f.ffmpegPathFlag,
			InstallOnDemand: *f.ffmpegInstallOnDemandFlag,
			SourceURL:       *f.ffmpegSourceURLFlag,
		},
		Thumbnails: Thumbnails{
			Enabled:   *f.thumbnailsEnabledFlag,
			CachePath: *f.thumbnailsCachePathFlag,
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
