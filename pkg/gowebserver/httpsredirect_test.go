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
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gomainTesting "github.com/cloudfra/gomain/testing"
	gowsTesting "github.com/cloudfra/gowebserver/internal/gowebserver/testing"
)

// serveGeneratedHTTPS starts a server with a certificate created for the test
// and returns the HTTPS port it is listening on.
func serveGeneratedHTTPS(t *testing.T) int {
	t.Helper()

	dir := mustTempDir(t)
	cfg := &Config{
		HTTP: HTTP{Port: 0},
		HTTPS: HTTPS{
			Port: 0,
			Certificate: Certificate{
				RootPrivateKeyFilePath:   filepath.Join(dir, "root-private.key"),
				RootCertificateFilePath:  filepath.Join(dir, "root-public.cert"),
				PrivateKeyFilePath:       filepath.Join(dir, "private.key"),
				CertificateFilePath:      filepath.Join(dir, "public.cert"),
				CertificateHosts:         "localhost",
				CertificateValidDuration: time.Hour,
				ForceOverwrite:           true,
			},
		},
	}
	if err := createCertificate(cfg); err != nil {
		t.Fatal(err)
	}

	ws, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	wsi, ok := ws.(*webServerImpl)
	if !ok {
		t.Fatalf("WebServer is not of type *webServerImpl, %+v", ws)
	}

	closer := gomainTesting.Main(wsi.Serve)
	t.Cleanup(func() {
		if err := closer(); err != nil {
			t.Errorf("closer() failed: %s", err)
		}
	})

	var httpsPort int
	for range 600 {
		_, httpsPort = wsi.getPorts()
		if httpsPort != 0 {
			return httpsPort
		}
		time.Sleep(time.Millisecond * 100)
	}

	t.Fatal("the server never opened an HTTPS port")
	return 0
}

// plaintextProbe writes the raw bytes of a request to the HTTPS port and reads
// one response back, as a browser does when it speaks HTTP to an HTTPS port.
func plaintextProbe(t *testing.T, port int, request string) (*http.Response, error) {
	t.Helper()

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("failed to close the probe connection, %s", err)
		}
	})

	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}

	return http.ReadResponse(bufio.NewReader(conn), nil)
}

// slowPlaintextProbe writes a request in pieces, waiting pauses[i] before piece
// i, and reads one response back. It is how a client that pauses mid-request,
// or only starts speaking after a while, reaches the server.
func slowPlaintextProbe(t *testing.T, port int, pauses []time.Duration, pieces ...string) (*http.Response, error) {
	t.Helper()

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("failed to close the probe connection, %s", err)
		}
	})

	for i, piece := range pieces {
		if i < len(pauses) {
			time.Sleep(pauses[i])
		}
		if _, err := conn.Write([]byte(piece)); err != nil {
			t.Fatal(err)
		}
	}

	return http.ReadResponse(bufio.NewReader(conn), nil)
}

func TestHTTPSRedirectsPlaintextRequest(t *testing.T) {
	port := serveGeneratedHTTPS(t)

	testCases := []struct {
		name    string
		request string
		want    string
	}{
		{
			name:    "GET keeps its path and query",
			request: "GET /deep/path.txt?a=b&c=1 HTTP/1.1\r\nHost: 127.0.0.1:%d\r\nUser-Agent: curl/8.14.1\r\nAccept: */*\r\n\r\n",
			want:    "https://127.0.0.1:%d/deep/path.txt?a=b&c=1",
		},
		{
			name:    "POST keeps its path and query",
			request: "POST /upload.asp?filename=x HTTP/1.1\r\nHost: 127.0.0.1:%d\r\nContent-Length: 0\r\n\r\n",
			want:    "https://127.0.0.1:%d/upload.asp?filename=x",
		},
		{
			name:    "a Host without a port gets the listening port",
			request: "GET /index.html HTTP/1.0\r\nHost: localhost\r\n\r\n",
			want:    "https://localhost:%d/index.html",
		},
		{
			name:    "an escaped path and query stay escaped",
			request: "GET /a%%20b/c%%2Bd.txt?q=x%%20y HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n",
			want:    "https://127.0.0.1:%d/a%%20b/c%%2Bd.txt?q=x%%20y",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			request := fmt.Sprintf(tc.request, port)
			resp, err := plaintextProbe(t, port, request)
			if err != nil {
				t.Fatal(err)
			}
			defer gowsTesting.DeferClose(t, resp.Body)()

			if resp.StatusCode != http.StatusPermanentRedirect {
				t.Errorf("want: %d, got: %d", http.StatusPermanentRedirect, resp.StatusCode)
			}
			wantLocation := fmt.Sprintf(tc.want, port)
			gotLocation := resp.Header.Get("Location")
			if wantLocation != gotLocation {
				t.Errorf("want: %v, got: %v", wantLocation, gotLocation)
			}
		})
	}
}

func TestHTTPSRedirectKeepsTheListenerUsable(t *testing.T) {
	port := serveGeneratedHTTPS(t)

	if resp, err := plaintextProbe(t, port, "PROBE-NOT-HTTP-NOR-TLS\r\n\r\n"); err == nil {
		defer gowsTesting.DeferClose(t, resp.Body)()
		t.Errorf("a probe speaking neither HTTP nor TLS got a response, %v", resp.Status)
	}

	resp, err := plaintextProbe(t, port, fmt.Sprintf("GET /after-the-probe.txt HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n", port))
	if err != nil {
		t.Fatal(err)
	}
	defer gowsTesting.DeferClose(t, resp.Body)()
	if resp.StatusCode != http.StatusPermanentRedirect {
		t.Errorf("want: %d, got: %d", http.StatusPermanentRedirect, resp.StatusCode)
	}

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	httpsResp, err := client.Get(fmt.Sprintf("https://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatal(err)
	}
	defer gowsTesting.DeferClose(t, httpsResp.Body)()
	if httpsResp.StatusCode != http.StatusOK {
		t.Errorf("want: %d, got: %d", http.StatusOK, httpsResp.StatusCode)
	}
}

// A client that has not sent its request yet is indistinguishable from one that
// never will be, so the redirect waits. A pause longer than the old single
// second window used to hand the connection to the TLS layer, which answered
// with its own error instead.
func TestHTTPSRedirectsPlaintextRequestAfterAPause(t *testing.T) {
	port := serveGeneratedHTTPS(t)

	testCases := []struct {
		name   string
		pauses []time.Duration
		pieces []string
		want   string
	}{
		{
			name:   "a request that starts after a pause",
			pauses: []time.Duration{1500 * time.Millisecond},
			pieces: []string{fmt.Sprintf("GET /after-a-pause.txt HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n", port)},
			want:   fmt.Sprintf("https://127.0.0.1:%d/after-a-pause.txt", port),
		},
		{
			name:   "a request split across a pause",
			pauses: []time.Duration{0, 1500 * time.Millisecond},
			pieces: []string{
				"GET /split-across-a-pause.txt HTTP/1.1\r\nHost: 127.0.0.1:",
				fmt.Sprintf("%d\r\n\r\n", port),
			},
			want: fmt.Sprintf("https://127.0.0.1:%d/split-across-a-pause.txt", port),
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp, err := slowPlaintextProbe(t, port, tc.pauses, tc.pieces...)
			if err != nil {
				t.Fatal(err)
			}
			defer gowsTesting.DeferClose(t, resp.Body)()

			if resp.StatusCode != http.StatusPermanentRedirect {
				t.Errorf("want: %d, got: %d", http.StatusPermanentRedirect, resp.StatusCode)
			}
			gotLocation := resp.Header.Get("Location")
			if tc.want != gotLocation {
				t.Errorf("want: %v, got: %v", tc.want, gotLocation)
			}
		})
	}
}

// Every accepted connection gets its own sniffing goroutine, so a connection
// that says nothing yet cannot keep the connections behind it from being
// redirected. Sniffing inside Accept would have cost one quiet window each.
func TestHTTPSRedirectDoesNotStallBehindQuietConnections(t *testing.T) {
	port := serveGeneratedHTTPS(t)

	const quietConnections = 10
	quiet := make([]net.Conn, 0, quietConnections)
	defer func() {
		for _, conn := range quiet {
			if err := conn.Close(); err != nil {
				t.Errorf("failed to close the quiet connection, %s", err)
			}
		}
	}()
	for i := 0; i < quietConnections; i++ {
		conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatal(err)
		}
		quiet = append(quiet, conn)
	}
	// Give the server a chance to accept the quiet connections before the
	// request that must not wait behind them arrives.
	time.Sleep(500 * time.Millisecond)

	start := time.Now()
	resp, err := plaintextProbe(t, port, fmt.Sprintf("GET /behind-quiet-connections.txt HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n", port))
	if err != nil {
		t.Fatal(err)
	}
	defer gowsTesting.DeferClose(t, resp.Body)()
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusPermanentRedirect {
		t.Errorf("want: %d, got: %d", http.StatusPermanentRedirect, resp.StatusCode)
	}
	if elapsed > time.Second {
		t.Errorf("redirecting took %v, which is quiet enough to be a queued connection", elapsed)
	}
}

// HTTP/2 prior knowledge says so in cleartext bytes that only look like a
// request. Redirecting those bytes would send the client to a URL built from a
// made-up path, so they go to the TLS layer as any other unknown protocol.
func TestHTTPSRedirectSkipsCleartextHTTP2(t *testing.T) {
	port := serveGeneratedHTTPS(t)

	if resp, err := plaintextProbe(t, port, "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"); err == nil {
		defer gowsTesting.DeferClose(t, resp.Body)()
		t.Errorf("a cleartext HTTP/2 preface got a response, %v with Location %q", resp.Status, resp.Header.Get("Location"))
	}

	resp, err := plaintextProbe(t, port, fmt.Sprintf("GET /after-the-preface.txt HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n", port))
	if err != nil {
		t.Fatal(err)
	}
	defer gowsTesting.DeferClose(t, resp.Body)()
	if resp.StatusCode != http.StatusPermanentRedirect {
		t.Errorf("want: %d, got: %d", http.StatusPermanentRedirect, resp.StatusCode)
	}
}

// Serving can end before the TLS layer takes a sniffed connection, which leaves
// the wrapping with a connection nobody is going to accept. Closing the wrapping
// has to release both the accepting side and the connection being examined.
func TestHTTPSRedirectListenerCloseReleasesSniffedConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("failed to close the listener, %s", err)
		}
	})

	wrapped := newHTTPSRedirectListener(listener, 443)

	// A connection that stays quiet, so its sniff is still reading when the
	// wrapping is closed.
	quiet, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := quiet.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("failed to close the quiet connection, %s", err)
		}
	}()

	if err := wrapped.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	if _, err := wrapped.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Errorf("want: %v, got: %v", net.ErrClosed, err)
	}

	// The sniff ends when its quiet window closes, and the connection it was
	// reading then has to be closed rather than left open.
	if err := quiet.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := quiet.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("the sniffed connection stayed open after the listener closed, read gave: %v", err)
	}
}

// Go's server reports a failed TLS handshake through a log.Logger it builds
// itself, which lands outside slog. The HTTPS server is given one that goes
// through slog, so the report keeps the level and the caller of everything else
// logged.
func TestHTTPSHandshakeErrorIsLoggedThroughSlog(t *testing.T) {
	recorder := recordSlog(t)
	port := serveGeneratedHTTPS(t)

	if resp, err := plaintextProbe(t, port, "PROBE-NOT-HTTP-NOR-TLS\r\n\r\n"); err == nil {
		defer gowsTesting.DeferClose(t, resp.Body)()
		t.Errorf("a probe speaking neither HTTP nor TLS got a response, %v", resp.Status)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		handshake := recorder.recordsContaining("TLS handshake error")
		if len(handshake) > 0 {
			for _, record := range handshake {
				if record.Level != slog.LevelInfo {
					t.Errorf("the handshake report logged at %v, want %v", record.Level, slog.LevelInfo)
				}
				if record.Source() == nil {
					t.Errorf("the handshake report %q has no caller, so it did not come from slog", record.Message)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("no TLS handshake error was logged through slog, only these messages: %v", recorder.messages())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// slogRecorder keeps every record logged through slog's default handler so a
// test can assert on what a server logged.
type slogRecorder struct {
	mutex   sync.Mutex
	records []slog.Record
}

func recordSlog(t *testing.T) *slogRecorder {
	t.Helper()

	recorder := &slogRecorder{}
	previous := slog.Default()
	slog.SetDefault(slog.New(recorder))
	t.Cleanup(func() { slog.SetDefault(previous) })

	return recorder
}

func (r *slogRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *slogRecorder) Handle(_ context.Context, record slog.Record) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.records = append(r.records, record.Clone())
	return nil
}

func (r *slogRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }

func (r *slogRecorder) WithGroup(string) slog.Handler { return r }

func (r *slogRecorder) recordsContaining(substring string) []slog.Record {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	var found []slog.Record
	for _, record := range r.records {
		if strings.Contains(record.Message, substring) {
			found = append(found, record)
		}
	}
	return found
}

func (r *slogRecorder) messages() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	messages := make([]string, 0, len(r.records))
	for _, record := range r.records {
		messages = append(messages, record.Message)
	}
	return messages
}
