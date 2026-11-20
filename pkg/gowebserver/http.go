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
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func overlayURLProxy(target *url.URL, client *http.Client) *httputil.ReverseProxy {
	targetQuery := target.RawQuery
	rewrite := func(pr *httputil.ProxyRequest) {
		pr.Out.URL.Scheme = target.Scheme
		pr.Out.URL.Host = target.Host
		pr.Out.Host = target.Host
		if targetQuery == "" || pr.Out.URL.RawQuery == "" {
			pr.Out.URL.RawQuery = targetQuery + pr.Out.URL.RawQuery
		} else {
			pr.Out.URL.RawQuery = targetQuery + "&" + pr.Out.URL.RawQuery
		}
	}
	return &httputil.ReverseProxy{
		Rewrite:   rewrite,
		Transport: client.Transport,
	}
}

// newHTTPReverseProxy passes requests on to the server at httpPath. Each
// request to it is a client span under the request's own, which that
// server can continue, and is in the http_client_* metrics. Its
// connections are its own, not http.DefaultTransport's; the function it
// returns closes those left idle.
func newHTTPReverseProxy(httpPath string, mc *monitoringContext) (http.Handler, func() error, error) {
	target, err := url.Parse(httpPath)
	if err != nil || !isSupportedHTTP(httpPath) || target.Host == "" {
		return nil, nilFuncWithError, fmt.Errorf("%s is not a valid HTTP server", httpPath)
	}

	connections := http.DefaultTransport.(*http.Transport).Clone()
	transport := otelhttp.NewTransport(connections,
		otelhttp.WithTracerProvider(mc.getTraceProvider()),
		otelhttp.WithMeterProvider(mc.getMeterProvider()),
		otelhttp.WithPropagators(tracePropagator))
	reverseProxy := overlayURLProxy(target, &http.Client{Transport: transport})
	if mc != nil && mc.logger != nil {
		// An embedded handler's: its errors go to the host's logger, not
		// the log package's.
		reverseProxy.ErrorLog = slog.NewLogLogger(mc.logger.Handler(), slog.LevelError)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setRequestHandler(r, handlerProxy)
		reverseProxy.ServeHTTP(w, r)
	}), func() error {
		connections.CloseIdleConnections()
		return nil
	}, nil
}

// isSupportedHTTP reports whether filePath is an http(s) URL to pass
// requests on to, rather than a path (a folder named "httpdocs", say).
func isSupportedHTTP(filePath string) bool {
	p := strings.ToLower(filePath)
	return strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://")
}
