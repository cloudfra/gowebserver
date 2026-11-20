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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
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

// redirectTimeouts are the deadlines a test server runs with.
type redirectTimeouts struct {
	firstByte, redirect, readHeader time.Duration
}

var testTimeouts = redirectTimeouts{firstByte: 5 * time.Second, redirect: 5 * time.Second, readHeader: 5 * time.Second}

// startRedirectTLSServer serves h (by default "hello <request URI>") over
// TLS, with HTTP/2, the way Serve does: newHTTPServer on a listener
// wrapped by newHTTPSRedirectListener, with the deadlines in to.
func startRedirectTLSServer(t *testing.T, to redirectTimeouts, h http.Handler) (*httptest.Server, *sdkmetric.ManualReader) {
	t.Helper()
	if h == nil {
		h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := io.WriteString(w, "hello "+r.URL.RequestURI()); err != nil {
				t.Error(err)
			}
		})
	}
	mc, reader, _ := testMonitoring()
	srv := httptest.NewUnstartedServer(nil)
	srv.Config = newHTTPServer(h)
	srv.Config.ReadHeaderTimeout = to.readHeader
	rl, err := newHTTPSRedirectListener(srv.Listener, mc, to.firstByte, to.redirect)
	mustNoError(t, err)
	srv.Listener = rl
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, reader
}

// rawExchange writes request to addr in plain text, all of it, and only
// then reads one response, as a simple client does.
func rawExchange(t *testing.T, addr, request string) *http.Response {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	mustNoError(t, err)
	t.Cleanup(func() { closeLogged(t, c) })
	mustNoError(t, c.SetDeadline(time.Now().Add(10*time.Second)))
	_, err = io.WriteString(c, request)
	mustNoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	mustNoError(t, err)
	t.Cleanup(func() { closeLogged(t, resp.Body) })
	return resp
}

func TestHTTPSRedirectListener_PlainHTTPIsRedirected(t *testing.T) {
	srv, _ := startRedirectTLSServer(t, testTimeouts, nil)
	addr := srv.Listener.Addr().String()
	port := strconv.Itoa(srv.Listener.Addr().(*net.TCPAddr).Port)

	testCases := []struct {
		name         string
		request      string
		wantStatus   int
		wantLocation string
	}{
		{
			name:         "get keeps host, port, path and query",
			request:      "GET /a/b%20c/?x=1&y=2 HTTP/1.1\r\nHost: localhost:8443\r\n\r\n",
			wantStatus:   http.StatusFound,
			wantLocation: "https://localhost:8443/a/b%20c/?x=1&y=2",
		},
		{
			name:         "head",
			request:      "HEAD / HTTP/1.1\r\nHost: example.com:8443\r\n\r\n",
			wantStatus:   http.StatusFound,
			wantLocation: "https://example.com:8443/",
		},
		{
			name:         "post keeps its method with 307",
			request:      "POST /upload HTTP/1.1\r\nHost: localhost:8443\r\nContent-Length: 5\r\n\r\nhello",
			wantStatus:   http.StatusTemporaryRedirect,
			wantLocation: "https://localhost:8443/upload",
		},
		{
			name:         "ipv6 host",
			request:      "GET /x HTTP/1.1\r\nHost: [::1]:8443\r\n\r\n",
			wantStatus:   http.StatusFound,
			wantLocation: "https://[::1]:8443/x",
		},
		{
			name:         "a host without a port gets the listener's",
			request:      "GET /x HTTP/1.1\r\nHost: example.com\r\n\r\n",
			wantStatus:   http.StatusFound,
			wantLocation: "https://example.com:" + port + "/x",
		},
		{
			name:         "an ipv6 host without a port gets the listener's",
			request:      "GET /x HTTP/1.1\r\nHost: [::1]\r\n\r\n",
			wantStatus:   http.StatusFound,
			wantLocation: "https://[::1]:" + port + "/x",
		},
		{
			name:         "absolute form keeps only path and query",
			request:      "GET http://localhost:8443/p?q=1 HTTP/1.1\r\nHost: localhost:8443\r\n\r\n",
			wantStatus:   http.StatusFound,
			wantLocation: "https://localhost:8443/p?q=1",
		},
		{
			name:         "asterisk form goes to the root",
			request:      "OPTIONS * HTTP/1.1\r\nHost: localhost:8443\r\n\r\n",
			wantStatus:   http.StatusTemporaryRedirect,
			wantLocation: "https://localhost:8443/",
		},
		{
			name:       "no host is rejected rather than naming the listener's address",
			request:    "GET /x HTTP/1.0\r\n\r\n",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "a bad host is rejected",
			request:    "GET /x HTTP/1.1\r\nHost: evil.com/@x\r\n\r\n",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "a malformed request is rejected",
			request:    "GARBAGE\r\n\r\n",
			wantStatus: http.StatusBadRequest,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := rawExchange(t, addr, tc.request)
			expectEqual(t, tc.wantStatus, resp.StatusCode, "status")
			expectEqual(t, tc.wantLocation, resp.Header.Get("Location"), "Location")
			expectEqual(t, true, resp.Close, "the response closes the connection")
		})
	}
	if t.Failed() {
		return
	}
	resp := rawExchange(t, addr, "GET /x HTTP/1.0\r\n\r\n")
	if body, err := io.ReadAll(resp.Body); err != nil || strings.Contains(string(body), addr) {
		t.Errorf("a 400 for a missing Host says %q (%v); want no address in it", body, err)
	}
}

// A client that sends all of a large body before reading gets the
// redirect, not a reset for the body left unread.
func TestHTTPSRedirectListener_LargePostGetsTheRedirect(t *testing.T) {
	srv, _ := startRedirectTLSServer(t, testTimeouts, nil)
	body := strings.Repeat("x", 8<<20)
	for range 3 {
		resp := rawExchange(t, srv.Listener.Addr().String(),
			"POST /upload HTTP/1.1\r\nHost: localhost:8443\r\nContent-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"+body)
		expectEqual(t, http.StatusTemporaryRedirect, resp.StatusCode, "status")
		expectEqual(t, "https://localhost:8443/upload", resp.Header.Get("Location"), "Location")
	}
}

func TestHTTPSRedirectListener_Metrics(t *testing.T) {
	srv, reader := startRedirectTLSServer(t, testTimeouts, nil)
	addr := srv.Listener.Addr().String()
	for range 2 {
		rawExchange(t, addr, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
	}
	rawExchange(t, addr, "GET / HTTP/1.0\r\n\r\n")
	// TLS isn't counted.
	client := srv.Client()
	client.Timeout = 5 * time.Second
	resp, err := client.Get(srv.URL)
	mustNoError(t, err)
	mustNoError(t, resp.Body.Close())

	// Counted after the response is written; give the last one a moment.
	want := map[string]int64{"result=redirected": 2, "result=rejected": 1}
	var got map[string]int64
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if got = collected(t, reader)["https_port_plain_http_requests_total"]; cmp.Equal(want, got) {
			break
		}
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("https_port_plain_http_requests_total (-want +got):\n%s", diff)
	}
}

func TestHTTPSRedirectListener_ClientFollowsToTLSAndHTTP2(t *testing.T) {
	srv, _ := startRedirectTLSServer(t, testTimeouts, nil)
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
	srv, _ := startRedirectTLSServer(t, testTimeouts, nil)
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

// Every way of stalling ends with the server closing the connection,
// at its own deadline, and none holds up other clients meanwhile.
func TestHTTPSRedirectListener_StalledClientsAreClosed(t *testing.T) {
	short := 300 * time.Millisecond
	testCases := []struct {
		name string
		send string
		to   redirectTimeouts
	}{
		// Only the first byte's deadline is short, so it is that one.
		{name: "silent", send: "", to: redirectTimeouts{firstByte: short, redirect: time.Minute, readHeader: time.Minute}},
		{name: "partial plain request", send: "GET / HTTP/1.1\r\nHost: localhost\r\n", to: redirectTimeouts{firstByte: time.Minute, redirect: short, readHeader: time.Minute}},
		// A TLS record header cut short: the TLS server's handshake deadline.
		{name: "stalled tls handshake", send: "\x16\x03\x01", to: redirectTimeouts{firstByte: time.Minute, redirect: time.Minute, readHeader: short}},
		// Neither: handed on to TLS, which rejects the record header.
		{name: "not tls, not http", send: "\x00\x01\x02\x03\x04\x05\x06\x07", to: redirectTimeouts{firstByte: time.Minute, redirect: time.Minute, readHeader: time.Minute}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := startRedirectTLSServer(t, tc.to, nil)
			c, err := net.Dial("tcp", srv.Listener.Addr().String())
			mustNoError(t, err)
			defer closeLogged(t, c)
			if tc.send != "" {
				_, err = io.WriteString(c, tc.send)
				mustNoError(t, err)
			}

			client := srv.Client()
			client.Timeout = 5 * time.Second
			resp, err := client.Get(srv.URL + "/")
			mustNoError(t, err)
			mustNoError(t, resp.Body.Close())
			expectEqual(t, http.StatusOK, resp.StatusCode, "another client's status")

			mustNoError(t, c.SetReadDeadline(time.Now().Add(10*time.Second)))
			start := time.Now()
			_, err = io.ReadAll(c)
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				t.Fatalf("the server kept the connection open for %s", time.Since(start))
			}
		})
	}
}

// The deadlines are for a request's head: a body taking far longer to
// send, either way, is left alone.
func TestHTTPSRedirectListener_SlowTransfersAreNotCut(t *testing.T) {
	const chunks, pause = 6, 100 * time.Millisecond
	short := redirectTimeouts{firstByte: 200 * time.Millisecond, redirect: 200 * time.Millisecond, readHeader: 200 * time.Millisecond}
	srv, _ := startRedirectTLSServer(t, short, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			n, err := io.Copy(io.Discard, r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if _, err := fmt.Fprintf(w, "%d", n); err != nil {
				t.Error(err)
			}
			return
		}
		for range chunks {
			if _, err := io.WriteString(w, "chunk\n"); err != nil {
				return
			}
			http.NewResponseController(w).Flush() //nolint:errcheck // a failed flush shows in the client's read
			time.Sleep(pause)
		}
	}))
	client := srv.Client()
	client.Timeout = 10 * time.Second

	resp, err := client.Get(srv.URL + "/download")
	mustNoError(t, err)
	body, err := io.ReadAll(resp.Body)
	mustNoError(t, err)
	mustNoError(t, resp.Body.Close())
	expectEqual(t, strings.Repeat("chunk\n", chunks), string(body), "slow download")

	pr, pw := io.Pipe()
	go func() {
		for range chunks {
			if _, err := pw.Write([]byte(strings.Repeat("u", 1<<10))); err != nil {
				return
			}
			time.Sleep(pause)
		}
		mustNoError(t, pw.Close())
	}()
	resp, err = client.Post(srv.URL+"/upload", "application/octet-stream", pr)
	mustNoError(t, err)
	body, err = io.ReadAll(resp.Body)
	mustNoError(t, err)
	mustNoError(t, resp.Body.Close())
	expectEqual(t, strconv.Itoa(chunks<<10), string(body), "slow upload's bytes received")
}

func TestNewHTTPServerTimeouts(t *testing.T) {
	s := newHTTPServer(http.NotFoundHandler())
	expectEqual(t, serverReadHeaderTimeout, s.ReadHeaderTimeout, "ReadHeaderTimeout (also the TLS handshake's)")
	expectEqual(t, serverIdleTimeout, s.IdleTimeout, "IdleTimeout")
	// Either would cut off a large file being uploaded or downloaded.
	expectEqual(t, time.Duration(0), s.ReadTimeout, "ReadTimeout")
	expectEqual(t, time.Duration(0), s.WriteTimeout, "WriteTimeout")
}

func TestHTTPSRedirectListener_CloseUnblocksAccept(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	mustNoError(t, err)
	l, err := newHTTPSRedirectListener(inner, nil, time.Second, time.Second)
	mustNoError(t, err)

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

func TestRedirectHost(t *testing.T) {
	on := func(port int) net.Addr { return &net.TCPAddr{IP: net.IPv4(10, 0, 0, 7), Port: port} }
	testCases := []struct {
		host  string
		local net.Addr
		want  string
		ok    bool
	}{
		{host: "example.com:8443", local: on(8443), want: "example.com:8443", ok: true},
		{host: "example.com", local: on(8443), want: "example.com:8443", ok: true},
		{host: "example.com", local: on(443), want: "example.com", ok: true},
		{host: "[::1]", local: on(8443), want: "[::1]:8443", ok: true},
		{host: "10.0.0.7", local: on(80), want: "10.0.0.7:80", ok: true},
		{host: "", local: on(8443)},
		{host: "evil.com/x", local: on(8443)},
	}
	for _, tc := range testCases {
		got, ok := redirectHost(tc.host, tc.local)
		if got != tc.want || ok != tc.ok {
			t.Errorf("redirectHost(%q, %s) = %q, %v; want %q, %v", tc.host, tc.local, got, ok, tc.want, tc.ok)
		}
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
