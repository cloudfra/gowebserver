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

// Package server is gowebserver's standalone server: its command line and
// configuration file (Load), logging, certificates, Prometheus and OTLP
// exporters, debug endpoints, CORS and every ufs driver, around the
// handlers in package gowebserver. A program that embeds those
// (gowebserver.NewHandler) doesn't import this package, nor link any of
// it.
package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"syscall"

	"github.com/cloudfra/certtool/pkg/certtool"
	"github.com/cloudfra/gomain"
	"github.com/cloudfra/gowebserver/pkg/gowebserver"
	"github.com/rs/cors"
	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"
	"go.uber.org/zap/zapcore"

	// Every ufs driver: cloud storage, git and the rest, as -path takes them.
	_ "github.com/cloudfra/ufs/drivers/all"
)

// Run is the entry point for running gowebserver as a console or Windows Service.
func Run() {
	syncFunc := configLogger(true)
	defer func() {
		if err := syncFunc(); err != nil && !isBenignSyncError(err) {
			slog.Error("failed to sync logger on shutdown", "error", err)
		}
	}()

	gomain.Run(runInteractive, gomain.Config{
		ServiceName:        "gowebserver",
		ServiceDescription: "A simple, convenient, reliable, well tested HTTP/HTTPS web server to host static files.",
		Command:            "",
	})
}

func configLogger(verbose bool) func() error {
	zapConfig := zap.NewDevelopmentConfig()
	zapConfig.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	zapConfig.Encoding = "console"
	if verbose {
		zapConfig.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	} else {
		zapConfig.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	logger, err := zapConfig.Build()
	if err == nil {
		zap.ReplaceGlobals(logger)
	}
	if logger == nil {
		return func() error { return nil }
	}
	zap.RedirectStdLog(logger)

	slog.SetDefault(slog.New(zapslog.NewHandler(logger.Core(),
		zapslog.WithCaller(true),
		zapslog.AddStacktraceAt(slog.LevelError),
	)))

	return logger.Sync
}

func runInteractive(wait func()) error {
	return runApplication(wait, os.Args[1:])
}

// runApplication runs the server configured by the command line args
// until wait returns.
func runApplication(wait func(), args []string) error {
	conf, err := loadArgs(args)
	if err != nil {
		return err
	}
	syncFunc := configLogger(conf.Verbose)
	defer func() {
		if syncErr := syncFunc(); syncErr != nil && !isBenignSyncError(syncErr) {
			slog.Error("failed to sync logger after running application", "error", syncErr)
		}
	}()

	slog.Debug("config", "config", conf)

	checkError(createCertificate(conf))

	httpServer, err := New(conf)
	if err != nil {
		return err
	}

	return httpServer.Serve(wait)
}

// New creates the standalone server's [gowebserver.WebServer] for conf:
// with its Prometheus exporter at conf.Monitoring.Metrics.Path, OTLP
// tracing, the debug endpoints under conf.Debug.Endpoint, and CORS.
func New(conf *gowebserver.Config) (gowebserver.WebServer, error) {
	if conf == nil {
		conf = &gowebserver.Config{}
	}
	mc, err := setupMonitoring(conf.Monitoring, conf.Debug.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("cannot setup monitoring '%+v', %w", conf.Monitoring, err)
	}
	ws, err := gowebserver.NewInstrumented(conf, mc.instrumentation(cors.Default().Handler))
	if err != nil {
		mc.shutdown()
		return nil, err
	}
	return ws, nil
}

// buildCertificateHostnames returns the list of hostnames and IPs to include in the TLS
// certificate SAN. It merges user-specified hosts with the machine's hostname and all
// non-loopback local IPs so that the auto-generated cert is valid for any local-network
// address the server may be reached at (e.g. "server2", "192.168.1.10").
func buildCertificateHostnames(conf *gowebserver.Config) []string {
	seen := map[string]bool{}
	var hostnames []string

	add := func(h string) {
		h = strings.TrimSpace(h)
		if h != "" && !seen[h] {
			seen[h] = true
			hostnames = append(hostnames, h)
		}
	}

	for _, h := range strings.Split(conf.HTTPS.Certificate.CertificateHosts, ",") {
		add(h)
	}

	if hostname, err := os.Hostname(); err == nil {
		add(hostname)
	}

	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && !ip.IsLoopback() {
				add(ip.String())
			}
		}
	}

	return hostnames
}

func createCertificate(conf *gowebserver.Config) error {
	dir, err := os.Getwd()
	slog.Debug("createCertificate", "certificate", conf.HTTPS.Certificate, "directory", dir, "error", err)
	_, certErr := os.Stat(conf.HTTPS.Certificate.CertificateFilePath)
	_, privateKeyErr := os.Stat(conf.HTTPS.Certificate.PrivateKeyFilePath)
	if conf.HTTPS.Certificate.ForceOverwrite || (os.IsNotExist(certErr) && os.IsNotExist(privateKeyErr)) {
		var parentKP *certtool.KeyPair

		if conf.HTTPS.Certificate.RootCertificateFilePath != "" {
			rootCertPath := conf.HTTPS.Certificate.RootCertificateFilePath
			rootKeyPath := conf.HTTPS.Certificate.RootPrivateKeyFilePath
			kp, err := certtool.GenerateAndWriteKeyPair(&certtool.Args{
				KeyType: &certtool.KeyType{
					Algorithm: "RSA",
					KeyLength: 2048,
				},
				Validity: conf.HTTPS.Certificate.CertificateValidDuration,
				CA:       true,
			},
				rootCertPath,
				rootKeyPath)
			slog.Debug("GenerateAndWriteKeyPair", "error", err, "certificateFile", rootCertPath, "privateKeyFile", rootKeyPath)
			if err != nil {
				return fmt.Errorf("cannot write public certificate, %w", err)
			}

			parentKP = kp
		}

		_, err := certtool.GenerateAndWriteKeyPair(&certtool.Args{
			KeyType: &certtool.KeyType{
				Algorithm: "RSA",
				KeyLength: 2048,
			},
			Validity:      conf.HTTPS.Certificate.CertificateValidDuration,
			CA:            false,
			Hostnames:     buildCertificateHostnames(conf),
			ParentKeyPair: parentKP,
		},
			conf.HTTPS.Certificate.CertificateFilePath,
			conf.HTTPS.Certificate.PrivateKeyFilePath)
		slog.Debug("GenerateAndWriteKeyPair", "error", err, "certificateFile", conf.HTTPS.Certificate.CertificateFilePath, "privateKeyFile", conf.HTTPS.Certificate.PrivateKeyFilePath)
		if err != nil {
			return fmt.Errorf("cannot write public certificate, %w", err)
		}
	}
	return nil
}

// isBenignSyncError reports whether err is the well-known spurious failure
// returned by (*zap.Logger).Sync (and the underlying os.File.Sync) when
// flushing stdout/stderr on Linux: those file descriptors are not
// syncable, and the OS returns EINVAL even though nothing is wrong. See
// https://github.com/uber-go/zap/issues/328 and
// https://github.com/uber-go/zap/issues/991.
func isBenignSyncError(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTTY)
}

func checkError(err error) {
	if err != nil {
		slog.Error("error", "error", err)
		if syncErr := zap.S().Sync(); syncErr != nil && !isBenignSyncError(syncErr) {
			slog.Error("failed to sync logger after reporting error", "error", syncErr)
		}
	}
}
