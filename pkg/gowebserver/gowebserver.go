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

// Package gowebserver is a simple, convenient, reliable, well tested HTTP/HTTPS web server to host static files.
package gowebserver

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/cloudfra/certtool/pkg/certtool"
	"github.com/jeremyje/gomain"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Run is the entry point for running gowebserver as a console or Windows Service.
func Run() {
	_, syncFunc := configLogger(true)
	defer syncFunc()

	gomain.Run(runInteractive, gomain.Config{
		ServiceName:        "gowebserver",
		ServiceDescription: "A simple, convenient, reliable, well tested HTTP/HTTPS web server to host static files.",
		Command:            "",
	})
}

func configLogger(verbose bool) (*zap.Logger, func() error) {
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
		return nil, func() error { return nil }
	}
	zap.RedirectStdLog(logger)
	return logger, logger.Sync
}

func runInteractive(wait func()) error {
	return runApplication(wait)
}

func runApplication(wait func()) error {
	conf, err := Load()
	if err != nil {
		return err
	}
	logger, syncFunc := configLogger(conf.Verbose)
	defer syncFunc()

	logger.Sugar().Debug(conf)

	checkError(createCertificate(conf))

	httpServer, err := New(conf)
	if err != nil {
		return err
	}

	return httpServer.Serve(wait)
}

// buildCertificateHostnames returns the list of hostnames and IPs to include in the TLS
// certificate SAN. It merges user-specified hosts with the machine's hostname and all
// non-loopback local IPs so that the auto-generated cert is valid for any local-network
// address the server may be reached at (e.g. "server2", "192.168.1.10").
func buildCertificateHostnames(conf *Config) []string {
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

func createCertificate(conf *Config) error {
	dir, err := os.Getwd()
	zap.S().With("certificate", conf.HTTPS.Certificate, "directory", dir, "error", err).Debug("createCertificate")
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
			zap.S().With("error", err, "certificateFile", rootCertPath, "privateKeyFile", rootKeyPath).Debug("GenerateAndWriteKeyPair")
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
		zap.S().With("error", err, "certificateFile", conf.HTTPS.Certificate.CertificateFilePath, "privateKeyFile", conf.HTTPS.Certificate.PrivateKeyFilePath).Debug("GenerateAndWriteKeyPair")
		if err != nil {
			return fmt.Errorf("cannot write public certificate, %w", err)
		}
	}
	return nil
}
