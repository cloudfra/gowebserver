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

package server

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/cloudfra/gowebserver/pkg/gowebserver"
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

// Load loads the configuration for the server from the command line,
// os.Args, and the configuration file it names. The flags are the
// server's own, on a FlagSet of their own: flag.CommandLine isn't
// parsed, so a program's flags (or those of the test binary) aren't
// accepted, and a program that imports the package keeps only its own.
func Load() (*gowebserver.Config, error) {
	return loadArgs(os.Args[1:])
}

// loadArgs loads the configuration from the command line args, parsed by
// a FlagSet of the server's own, and the configuration file they name.
func loadArgs(args []string) (*gowebserver.Config, error) {
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

func loadWithConfigFile(filePath string, conf *gowebserver.Config) error {
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
func (f *flags) config() (*gowebserver.Config, error) {
	sl, err := serveList(*f.pathFlag, *f.servePathFlag)
	if err != nil {
		return nil, err
	}
	return &gowebserver.Config{
		Verbose:           *f.verboseFlag,
		Serve:             sl,
		ConfigurationFile: *f.configFileFlag,
		EnhancedList:      *f.enhancedListFlag,
		HTTP: gowebserver.HTTP{
			Port: *f.httpPortFlag,
		},
		HTTPS: gowebserver.HTTPS{
			Port: *f.httpsPortFlag,
			Certificate: gowebserver.Certificate{
				PrivateKeyFilePath:       *f.privateKeyFilePathFlag,
				CertificateFilePath:      *f.certificateFilePathFlag,
				RootPrivateKeyFilePath:   *f.rootPrivateKeyFilePathFlag,
				RootCertificateFilePath:  *f.rootCertificateFilePathFlag,
				CertificateHosts:         *f.certHostsFlag,
				CertificateValidDuration: *f.validDurationFlag,
				ForceOverwrite:           *f.forceOverwriteCertFlag,
			},
		},
		Debug: gowebserver.Debug{
			Endpoint:    *f.debugEndpointFlag,
			Shutdown:    *f.debugShutdownFlag,
			Diagnostics: *f.debugDiagnosticsFlag,
		},
		Monitoring: gowebserver.Monitoring{
			Metrics: gowebserver.Metrics{
				Enabled: *f.monitoringMetricsPath != "",
				Path:    *f.monitoringMetricsPath,
			},
			Trace: gowebserver.Trace{
				Enabled: *f.monitoringTraceURIFlag != "",
				URI:     *f.monitoringTraceURIFlag,
			},
		},
		Upload: gowebserver.Serve{
			Source:   *f.uploadPathFlag,
			Endpoint: *f.uploadHTTPPathFlag,
		},
		FFmpeg: gowebserver.FFmpeg{
			AcceptLicense:   *f.ffmpegAcceptLicenseFlag,
			Path:            *f.ffmpegPathFlag,
			InstallOnDemand: *f.ffmpegInstallOnDemandFlag,
			SourceURL:       *f.ffmpegSourceURLFlag,
		},
		Thumbnails: gowebserver.Thumbnails{
			Enabled:   *f.thumbnailsEnabledFlag,
			CachePath: *f.thumbnailsCachePathFlag,
		},
	}, nil
}

func serveList(paths string, servePaths string) ([]gowebserver.Serve, error) {
	pl := strings.Split(paths, ",")
	spl := strings.Split(servePaths, ",")

	if len(pl) != len(spl) {
		return []gowebserver.Serve{}, fmt.Errorf("-path and -servepath are different lengths")
	}

	sl := []gowebserver.Serve{}
	for i, p := range pl {
		sl = append(sl, gowebserver.Serve{
			Source:   p,
			Endpoint: spl[i],
		})
	}

	return sl, nil
}
