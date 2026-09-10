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
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/cloudfra/ufs"
	"github.com/rs/cors"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// WebServer is a convenience wrapper for Go's HTTP/HTTPS Web serving API.
type WebServer interface {
	// Serve starts serving the HTTP/HTTPS server synchronously.
	Serve(func()) error
}

type webServerImpl struct {
	httpAddr            string
	httpsAddr           string
	metricsEnabled      bool
	fileSystemServePath []servePath
	metricsServePath    string
	certificateFilePath string
	privateKeyFilePath  string
	verbose             bool
	uploadPath          string
	uploadHTTPPath      string
	enhancedListMode    bool
	enableDebugMethods  bool
	monitoringCtx       *monitoringContext

	httpListenPort  int
	httpsListenPort int

	sync.RWMutex
}

type servePath struct {
	localPath string
	httpPath  string
}

func expandPath(dir string) (string, error) {
	if len(dir) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return cwd, nil
	}
	return dir, nil
}

func (ws *webServerImpl) addHandler(serverMux *http.ServeMux, servePath string, handler http.Handler) {
	if ws.metricsEnabled {
		serverMux.Handle(servePath, otelhttp.NewHandler(handler, servePath, otelhttp.WithTracerProvider(ws.monitoringCtx.getTraceProvider()), otelhttp.WithMeterProvider(ws.monitoringCtx.getMeterProvider())))
	} else {
		serverMux.Handle(servePath, handler)
	}
}

func getPort(lis net.Listener) (int, error) {
	addr, ok := lis.Addr().(*net.TCPAddr)
	if ok {
		return addr.Port, nil
	}
	return 0, fmt.Errorf("cannot get port from '%s'", lis)
}

func (ws *webServerImpl) setPorts(httpPort int, httpsPort int) {
	ws.Lock()
	ws.httpListenPort = httpPort
	ws.httpsListenPort = httpsPort
	ws.Unlock()
}

func (ws *webServerImpl) getPorts() (int, int) {
	ws.RLock()
	httpPort := ws.httpListenPort
	httpsPort := ws.httpsListenPort
	ws.RUnlock()
	return httpPort, httpsPort
}

type killHTTPServerHandler struct {
	killFunc func()
}

func (k *killHTTPServerHandler) ServeHTTP(http.ResponseWriter, *http.Request) {
	if k.killFunc != nil {
		k.killFunc()
		os.Exit(0)
	}
}

func (ws *webServerImpl) Serve(wait func()) error {
	allCleanups := []func() error{}

	displayPath := ""
	for i, paths := range ws.fileSystemServePath {
		if i > 0 {
			displayPath += ","
		}
		displayPath += paths.localPath
	}
	serverMux := http.NewServeMux()
	if ws.monitoringCtx != nil {
		for endpoint, h := range ws.monitoringCtx.handlers {
			slog.Info("Endpoint", "http", endpoint)
			serverMux.Handle(endpoint, h)
		}
		allCleanups = append(allCleanups, func() error {
			ws.monitoringCtx.shutdown()
			return nil
		})
	}

	mounts := map[string]string{}
	rootPath := ""
	for _, paths := range ws.fileSystemServePath {
		slog.Info("Endpoint", "localPath", paths.localPath, "http", paths.httpPath)
		if paths.httpPath == "" || paths.httpPath == "/" {
			rootPath = paths.localPath
		} else {
			mounts[strings.TrimLeft(paths.httpPath, "/")] = paths.localPath
		}
	}

	if rootPath == "" && len(mounts) > 0 {
		// No root endpoint configured but non-root mounts exist: generate a root
		// index listing and register each mount with its own handler.
		servePaths := make([]string, 0, len(ws.fileSystemServePath))
		for _, paths := range ws.fileSystemServePath {
			servePaths = append(servePaths, paths.httpPath)
		}
		indexHandler, err := newIndexHTTPHandler(servePaths, ws.enhancedListMode)
		if err != nil {
			return err
		}
		ws.addHandler(serverMux, "/", indexHandler)

		for _, paths := range ws.fileSystemServePath {
			fsHandler, cleanup, err := newHandlerFromFS(paths.localPath, ws.monitoringCtx.getTraceProvider(), ws.enhancedListMode)
			if err != nil {
				return err
			}
			allCleanups = append(allCleanups, cleanup)
			httpPath := paths.httpPath
			strippedPrefix := strings.TrimRight(httpPath, "/")
			ws.addHandler(serverMux, httpPath, http.StripPrefix(strippedPrefix, fsHandler))
		}
	} else {
		if rootPath == "" {
			rootPath = "null://"
		}
		fsSpec, err := ufs.CreateURI(rootPath, mounts)
		if err != nil {
			return err
		}
		fsHandler, cleanup, err := newHandlerFromFS(fsSpec, ws.monitoringCtx.getTraceProvider(), ws.enhancedListMode)
		if err != nil {
			return err
		}
		allCleanups = append(allCleanups, cleanup)
		ws.addHandler(serverMux, "/", fsHandler)
	}

	defer func() {
		for _, cleanup := range allCleanups {
			if err := cleanup(); err != nil {
				slog.Error("cleanup error", "error", err)
			}
		}
	}()

	if len(ws.uploadHTTPPath) > 0 {
		uploadHandler, err := newUploadHandler(ws.monitoringCtx, ws.uploadHTTPPath, ws.uploadPath)
		if err != nil {
			return err
		}
		ws.addHandler(serverMux, ws.uploadHTTPPath, uploadHandler)
	}

	httpSocket, err := net.Listen("tcp", ws.httpAddr)
	if err != nil {
		return err
	}

	defer func() {
		if err := httpSocket.Close(); err != nil {
			slog.Error("failed to close HTTP listener socket", "addr", ws.httpAddr, "error", err)
		}
	}()

	httpsSocket, err := net.Listen("tcp", ws.httpsAddr)
	if err != nil {
		return err
	}

	if ws.enableDebugMethods {
		killFunc := func() {
			if httpsSocket != nil {
				if err := httpsSocket.Close(); err != nil {
					slog.Error("failed to close HTTPS listener socket from /diediedie handler", "addr", ws.httpsAddr, "error", err)
				}
			}
			if httpSocket != nil {
				if err := httpSocket.Close(); err != nil {
					slog.Error("failed to close HTTP listener socket from /diediedie handler", "addr", ws.httpAddr, "error", err)
				}
			}
		}

		slog.Info("Endpoint", "http", "/diediedie")
		ws.addHandler(serverMux, "/diediedie", &killHTTPServerHandler{killFunc: killFunc})
	}

	defer func() {
		if err := httpsSocket.Close(); err != nil {
			slog.Error("failed to close HTTPS listener socket", "addr", ws.httpsAddr, "error", err)
		}
	}()

	httpHandler := cors.Default().Handler(serverMux)

	httpPort, err := getPort(httpSocket)
	if err != nil {
		slog.Error("cannot get port from HTTP listener", "error", err)
	}
	httpsPort, err := getPort(httpsSocket)
	if err != nil {
		slog.Error("cannot get port from HTTPS listener", "error", err)
	}

	slog.Info("Serving", "HTTP", fmt.Sprintf("http://localhost:%d/", httpPort), "HTTPS", fmt.Sprintf("https://localhost:%d/", httpsPort))

	ws.setPorts(httpPort, httpsPort)

	go func() {
		if ws.certificateFilePath != "" {
			checkError(http.ServeTLS(httpsSocket, httpHandler, ws.certificateFilePath, ws.privateKeyFilePath))
		}
	}()
	go func() {
		checkError(http.Serve(httpSocket, httpHandler))
	}()

	wait()
	return nil
}

// New creates a new [WebServer] with the specified [Config].
func New(conf *Config) (WebServer, error) {
	if conf == nil {
		conf = &Config{}
	}
	sp := []servePath{}
	for _, paths := range conf.Serve {
		p, err := expandPath(paths.Source)
		if err != nil {
			return nil, fmt.Errorf("cannot expand path '%s', %w", paths.Source, err)
		}
		sp = append(sp, servePath{
			localPath: p,
			httpPath:  normalizeHTTPPath(paths.Endpoint),
		})
	}

	uploadPath := ""
	if conf.Upload.Source != "" {
		dir, err := expandPath(conf.Upload.Source)
		if err != nil {
			return nil, fmt.Errorf("cannot expand path '%s', %w", conf.Upload.Source, err)
		}
		uploadPath = dir
	}

	monitoringCtx, err := setupMonitoring(conf.Monitoring)
	if err != nil {
		return nil, fmt.Errorf("cannot setup monitoring '%+v', %w", conf.Monitoring, err)
	}
	ws := &webServerImpl{
		httpAddr:            toAddr(conf.HTTP.Port),
		httpsAddr:           toAddr(conf.HTTPS.Port),
		monitoringCtx:       monitoringCtx,
		metricsEnabled:      conf.Monitoring.Metrics.Enabled,
		fileSystemServePath: sp,
		metricsServePath:    conf.Monitoring.Metrics.Path,
		certificateFilePath: conf.HTTPS.Certificate.CertificateFilePath,
		privateKeyFilePath:  conf.HTTPS.Certificate.PrivateKeyFilePath,
		enhancedListMode:    conf.EnhancedList,
		enableDebugMethods:  conf.Debug,
		uploadPath:          uploadPath,
		uploadHTTPPath:      conf.Upload.Endpoint,
		verbose:             conf.Verbose,
	}

	return ws, nil
}

func toAddr(port int) string {
	return fmt.Sprintf(":%d", port)
}

func normalizeHTTPPath(path string) string {
	return strings.ReplaceAll("/"+strings.Trim(path, "/")+"/", "//", "/")
}
