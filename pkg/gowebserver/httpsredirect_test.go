// Copyright 2026 Cloudfra
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
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mustNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func expectEqual[T comparable](t *testing.T, want, got T, what string) {
	t.Helper()
	if want != got {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

// closeLogged closes c, logging rather than failing on an error: the
// server may have closed its side first.
func closeLogged(t *testing.T, c io.Closer) {
	t.Helper()
	if err := c.Close(); err != nil {
		t.Logf("close: %s", err)
	}
}

// startRedirectTLSServer serves "hello" over TLS (with HTTP/2) on a
// listener wrapped by newHTTPSRedirectListener, sniffing with timeout.
func startRedirectTLSServer(t *testing.T, timeout time.Duration) (*httptest.Server, *httpsRedirectListener) {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.WriteString(w, "hello "+r.URL.RequestURI()); err != nil {
			t.Error(err)
		}
	}))
	rl := newHTTPSRedirectListener(srv.Listener, timeout)
	srv.Listener = rl
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, rl
}

// rawExchange writes request to addr in plain text and reads one response.
func rawExchange(t *testing.T, addr, request string) *http.Response {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	mustNoError(t, err)
	t.Cleanup(func() { closeLogged(t, c) })
	mustNoError(t, c.SetDeadline(time.Now().Add(5*time.Second)))
	_, err = io.WriteString(c, request)
	mustNoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	mustNoError(t, err)
	t.Cleanup(func() { closeLogged(t, resp.Body) })
	return resp
}

func TestHTTPSRedirectListener_PlainHTTPIsRedirected(t *testing.T) {
	srv, _ := startRedirectTLSServer(t, time.Second)
	addr := srv.Listener.Addr().String()

	testCases := []struct {
		name         string
		request      string
		wantStatus   int
		wantLocation string
	}{
		{
			name:         "get keeps host, port, path and query",
			request:      "GET /a/b%20c/?x=1&y=2 HTTP/1.1\r\nHost: localhost:8443\r\n\r\n",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: "https://localhost:8443/a/b%20c/?x=1&y=2",
		},
		{
			name:         "head",
			request:      "HEAD / HTTP/1.1\r\nHost: example.com:8443\r\n\r\n",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: "https://example.com:8443/",
		},
		{
			name:         "post keeps its method with 308",
			request:      "POST /upload HTTP/1.1\r\nHost: localhost:8443\r\nContent-Length: 5\r\n\r\nhello",
			wantStatus:   http.StatusPermanentRedirect,
			wantLocation: "https://localhost:8443/upload",
		},
		{
			name:         "ipv6 host",
			request:      "GET /x HTTP/1.1\r\nHost: [::1]:8443\r\n\r\n",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: "https://[::1]:8443/x",
		},
		{
			name:         "no host falls back to the listener address",
			request:      "GET /x HTTP/1.0\r\n\r\n",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: "https://" + addr + "/x",
		},
		{
			name:         "bad host falls back to the listener address",
			request:      "GET /x HTTP/1.1\r\nHost: evil.com/@x\r\n\r\n",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: "https://" + addr + "/x",
		},
		{
			name:         "absolute form keeps only path and query",
			request:      "GET http://localhost:8443/p?q=1 HTTP/1.1\r\nHost: localhost:8443\r\n\r\n",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: "https://localhost:8443/p?q=1",
		},
		{
			name:         "asterisk form goes to the root",
			request:      "OPTIONS * HTTP/1.1\r\nHost: localhost:8443\r\n\r\n",
			wantStatus:   http.StatusPermanentRedirect,
			wantLocation: "https://localhost:8443/",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := rawExchange(t, addr, tc.request)
			expectEqual(t, tc.wantStatus, resp.StatusCode, "status")
			expectEqual(t, tc.wantLocation, resp.Header.Get("Location"), "Location")
			expectEqual(t, true, resp.Close, "the redirect closes the connection")
		})
	}
}

func TestHTTPSRedirectListener_MalformedPlainHTTP(t *testing.T) {
	srv, _ := startRedirectTLSServer(t, time.Second)
	resp := rawExchange(t, srv.Listener.Addr().String(), "GARBAGE\r\n\r\n")
	expectEqual(t, http.StatusBadRequest, resp.StatusCode, "status")
	expectEqual(t, "", resp.Header.Get("Location"), "Location")
}

func TestHTTPSRedirectListener_ClientFollowsToTLSAndHTTP2(t *testing.T) {
	srv, _ := startRedirectTLSServer(t, time.Second)
	addr := srv.Listener.Addr().String()

	// The test server's client trusts its certificate and speaks HTTP/2.
	client := srv.Client()
	client.Timeout = 5 * time.Second
	resp, err := client.Get("http://" + addr + "/dir/file.txt?v=1")
	mustNoError(t, err)
	defer closeLogged(t, resp.Body)
	body, err := io.ReadAll(resp.Body)
	mustNoError(t, err)

	expectEqual(t, http.StatusOK, resp.StatusCode, "status")
	expectEqual(t, "hello /dir/file.txt?v=1", string(body), "body")
	expectEqual(t, "https", resp.Request.URL.Scheme, "final scheme")
	expectEqual(t, 2, resp.ProtoMajor, "HTTP major version (HTTP/2 is still negotiated over TLS)")
	expectEqual(t, true, resp.TLS != nil, "served over TLS")
}

func TestHTTPSRedirectListener_TLSUnchanged(t *testing.T) {
	srv, _ := startRedirectTLSServer(t, time.Second)
	client := srv.Client()
	client.Timeout = 5 * time.Second
	for range 3 {
		resp, err := client.Get(srv.URL + "/x?y=z")
		mustNoError(t, err)
		body, err := io.ReadAll(resp.Body)
		mustNoError(t, err)
		mustNoError(t, resp.Body.Close())
		expectEqual(t, "hello /x?y=z", string(body), "body")
		expectEqual(t, "HTTP/2.0", resp.Proto, "protocol")
	}
}

func TestHTTPSRedirectListener_SlowClientsTimeOutAndDoNotBlock(t *testing.T) {
	srv, _ := startRedirectTLSServer(t, 200*time.Millisecond)
	addr := srv.Listener.Addr().String()

	for name, send := range map[string]string{
		"silent":          "",
		"partial request": "GET / HTTP/1.1\r\nHost: localhost\r\n",
		// Neither: handed on to TLS, which rejects the record header.
		"not tls, not http": "\x00\x01\x02\x03\x04\x05\x06\x07",
	} {
		t.Run(name, func(t *testing.T) {
			c, err := net.Dial("tcp", addr)
			mustNoError(t, err)
			defer closeLogged(t, c)
			if send != "" {
				_, err = io.WriteString(c, send)
				mustNoError(t, err)
			}

			// A stalled client doesn't hold up anyone else.
			client := srv.Client()
			client.Timeout = 5 * time.Second
			resp, err := client.Get(srv.URL + "/")
			mustNoError(t, err)
			mustNoError(t, resp.Body.Close())
			expectEqual(t, http.StatusOK, resp.StatusCode, "status")

			// And the server lets go of it rather than waiting forever.
			mustNoError(t, c.SetReadDeadline(time.Now().Add(5*time.Second)))
			start := time.Now()
			_, err = io.ReadAll(c)
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				t.Fatalf("the server kept the connection open for %s", time.Since(start))
			}
		})
	}
}

func TestHTTPSRedirectListener_CloseUnblocksAccept(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	mustNoError(t, err)
	l := newHTTPSRedirectListener(inner, time.Second)

	errCh := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		errCh <- err
	}()
	mustNoError(t, l.Close())
	mustNoError(t, l.Close())

	select {
	case err := <-errCh:
		expectEqual(t, true, errors.Is(err, net.ErrClosed), fmt.Sprintf("Accept error %v is net.ErrClosed", err))
	case <-time.After(5 * time.Second):
		t.Fatal("Accept did not return after Close")
	}
}

func TestValidRedirectHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost":             true,
		"localhost:8443":        true,
		"example.com":           true,
		"192.168.1.2:443":       true,
		"[::1]:8443":            true,
		"":                      false,
		"evil.com/path":         false,
		"user@evil.com":         false,
		"a b":                   false,
		"evil.com\r\nX: y":      false,
		"host?x":                false,
		"host#x":                false,
		`host\evil`:             false,
		"%65vil.com":            false,
		strings.Repeat("a", 10): true,
	} {
		expectEqual(t, want, validRedirectHost(host), fmt.Sprintf("validRedirectHost(%q)", host))
	}
}
