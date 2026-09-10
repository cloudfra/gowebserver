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
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
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

func newHTTPReverseProxy(httpPath string) (http.Handler, error) {
	if !isSupportedHTTP(httpPath) {
		return nil, fmt.Errorf("%s is not a valid HTTP server", httpPath)
	}

	reverseProxy := overlayURLProxy(mustURLParse(httpPath), &http.Client{})

	return reverseProxy, nil
}

func isSupportedHTTP(filePath string) bool {
	return strings.HasPrefix(strings.ToLower(filePath), "http")
}

func mustURLParse(u string) *url.URL {
	url, err := url.Parse(u)
	if err != nil {
		panic(err)
	}
	return url
}
