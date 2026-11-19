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
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	// httpsFirstByteTimeout bounds how long a new connection on the HTTPS
	// port may stay silent before its first byte. It is longer than the
	// plain HTTP deadline because browsers open sockets ahead of need
	// (preconnect) and send their TLS ClientHello only when a request
	// comes; closing those early would cost a reconnect. Chromium drops an
	// unused socket after 10 seconds, so 30 is past any browser's own.
	// Once the first byte arrives a TLS connection is the TLS server's:
	// its handshake has the server's own deadline (see newHTTPServer).
	httpsFirstByteTimeout = 30 * time.Second
	// httpsRedirectTimeout bounds each step of answering plain HTTP on
	// the HTTPS port: reading the request head, writing the redirect, and
	// draining what the client still sends after it.
	httpsRedirectTimeout = 10 * time.Second
	// httpsRedirectMaxHeader caps the plain HTTP request head read before
	// redirecting.
	httpsRedirectMaxHeader = 16 << 10
)

// What plain HTTP on the HTTPS port came to, the "result" of
// https_port_plain_http_requests_total.
const (
	plainHTTPRedirected = "redirected" // sent to https://
	plainHTTPRejected   = "rejected"   // 400: a malformed or incomplete request, or no usable Host
)

// httpsRedirectListener wraps the HTTPS port's listener. A client that
// speaks plain HTTP to it (its first byte is an ASCII letter, the start of
// a method, where TLS begins with 0x16) gets a redirect to the same host,
// port, path and query over https:// instead of Go's "Client sent an HTTP
// request to an HTTPS server" 400. Every other connection, with the byte
// it sent, is handed on untouched for the TLS server (and its ALPN, so
// HTTP/2) to handle.
//
// Accept never waits on a client: each connection is sniffed in its own
// goroutine, under firstByteTimeout and then redirectTimeout.
type httpsRedirectListener struct {
	net.Listener
	firstByteTimeout time.Duration
	redirectTimeout  time.Duration
	// Plain HTTP requests on the HTTPS port, by result.
	requests metric.Int64Counter

	conns   chan net.Conn
	errs    chan error
	done    chan struct{}
	closing sync.Once
}

func newHTTPSRedirectListener(inner net.Listener, mc *monitoringContext, firstByteTimeout, redirectTimeout time.Duration) (*httpsRedirectListener, error) {
	requests, err := mc.getMeterProvider().Meter("httpsredirect").Int64Counter("https_port_plain_http_requests_total",
		metric.WithDescription("Plain HTTP requests that reached the HTTPS port, by result (redirected to https://, or rejected with a 400)."))
	if err != nil {
		return nil, err
	}
	l := &httpsRedirectListener{
		Listener:         inner,
		firstByteTimeout: firstByteTimeout,
		redirectTimeout:  redirectTimeout,
		requests:         requests,
		conns:            make(chan net.Conn),
		errs:             make(chan error, 1),
		done:             make(chan struct{}),
	}
	go l.acceptLoop()
	return l, nil
}

func (l *httpsRedirectListener) acceptLoop() {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			select {
			case l.errs <- err:
			case <-l.done:
				return
			}
			// http.Server retries temporary errors (such as too many open
			// files) and stops on the others.
			if tmp, ok := err.(interface{ Temporary() bool }); ok && tmp.Temporary() {
				continue
			}
			return
		}
		go l.sniff(c)
	}
}

// Accept returns the next connection that isn't plain HTTP.
func (l *httpsRedirectListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case err := <-l.errs:
		return nil, err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close closes the underlying listener and unblocks Accept.
func (l *httpsRedirectListener) Close() error {
	var err error
	l.closing.Do(func() {
		close(l.done)
		err = l.Listener.Close()
	})
	return err
}

func (l *httpsRedirectListener) sniff(c net.Conn) {
	// The smallest buffer: it outlives the sniff on every TLS connection.
	br := bufio.NewReaderSize(c, 16)
	if err := c.SetReadDeadline(time.Now().Add(l.firstByteTimeout)); err != nil {
		closeSniffed(c)
		return
	}
	first, err := br.Peek(1)
	if err != nil {
		closeSniffed(c)
		return
	}
	if isPlainHTTPStart(first[0]) {
		l.redirectToHTTPS(c, br)
		closeSniffed(c)
		return
	}
	// The TLS server sets its own handshake deadline when it takes the
	// connection (newHTTPServer's ReadHeaderTimeout), then clears it for
	// the requests, which have theirs. Leaving this one would cut off any
	// download or upload longer than it.
	if err := c.SetReadDeadline(time.Time{}); err != nil {
		closeSniffed(c)
		return
	}
	select {
	case l.conns <- &peekedConn{Conn: c, r: br}:
	case <-l.done:
		closeSniffed(c)
	}
}

// isPlainHTTPStart says whether b can begin an HTTP/1.x request: methods
// are tokens, in practice uppercase letters. A TLS record begins with its
// content type (0x16 for a handshake), never a letter.
func isPlainHTTPStart(b byte) bool {
	return b >= 'A' && b <= 'Z'
}

// redirectToHTTPS reads one plain HTTP request head from br, under
// redirectTimeout, and answers it with a redirect to the same URL over
// https://.
//
// The redirects are temporary, 302 Found for GET and HEAD and 307
// Temporary Redirect (which keeps the method and body) for the rest, so
// browsers don't cache them: the port is plain HTTP's again as soon as
// this server stops, and a remembered permanent redirect would keep
// sending, say, a localhost development port to https:// for good.
func (l *httpsRedirectListener) redirectToHTTPS(c net.Conn, br *bufio.Reader) {
	resp := &http.Response{
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{},
		Close:      true,
	}
	reject := func(req *http.Request, why string) {
		l.count(plainHTTPRejected)
		resp.StatusCode = http.StatusBadRequest
		resp.Header.Set("Content-Type", "text/plain; charset=utf-8")
		l.writeResponse(c, resp, req, "Client sent an HTTP request to an HTTPS server"+why+".\n")
	}
	if err := c.SetReadDeadline(time.Now().Add(l.redirectTimeout)); err != nil {
		return
	}
	req, err := http.ReadRequest(bufio.NewReaderSize(io.LimitReader(br, httpsRedirectMaxHeader), 4096))
	if err != nil {
		reject(nil, "")
		return
	}
	// Without a Host the only name for the server is its own address,
	// which behind a proxy or in a container may be one the client can't
	// reach.
	host, ok := redirectHost(req.Host, c.LocalAddr())
	if !ok {
		reject(req, " without a usable Host header")
		return
	}
	target := "https://" + host + req.URL.RequestURI()
	if !strings.HasPrefix(req.URL.RequestURI(), "/") {
		target = "https://" + host + "/"
	}
	resp.StatusCode = http.StatusFound
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		resp.StatusCode = http.StatusTemporaryRedirect
	}
	resp.Header.Set("Location", target)
	body := ""
	if req.Method != http.MethodHead {
		body = "<a href=\"" + htmlReplacer.Replace(target) + "\">" + http.StatusText(resp.StatusCode) + "</a>.\n"
		resp.Header.Set("Content-Type", "text/html; charset=utf-8")
	}
	l.count(plainHTTPRedirected)
	l.writeResponse(c, resp, req, body)
}

// count records a plain HTTP request's result, before its response: the
// drain after it can last as long as the client keeps the connection.
func (l *httpsRedirectListener) count(result string) {
	l.requests.Add(context.Background(), 1, metric.WithAttributes(attribute.String("result", result)))
}

var htmlReplacer = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;")

// writeResponse writes resp to c, then makes sure the client can read it.
func (l *httpsRedirectListener) writeResponse(c net.Conn, resp *http.Response, req *http.Request, body string) {
	resp.Request = req
	resp.ContentLength = int64(len(body))
	resp.Body = io.NopCloser(strings.NewReader(body))
	if err := c.SetWriteDeadline(time.Now().Add(l.redirectTimeout)); err != nil {
		return
	}
	if err := resp.Write(c); err != nil {
		slog.Debug("cannot write HTTP to HTTPS redirect", "error", err)
		return
	}
	tc, ok := c.(*net.TCPConn)
	if !ok {
		return
	}
	// Closing with request bytes (a POST's body, say) unread sends a
	// reset, and a client still sending gets that instead of the
	// response. So say we're done, then read and discard whatever the
	// client sends until it closes, for as long as redirectTimeout: the
	// time bounds it, not a byte count, since a large upload is the case
	// this is for.
	if err := tc.CloseWrite(); err != nil {
		return
	}
	if err := c.SetReadDeadline(time.Now().Add(l.redirectTimeout)); err != nil {
		return
	}
	if _, err := io.Copy(io.Discard, c); err != nil {
		slog.Debug("HTTP to HTTPS redirect: the client was still sending at the deadline", "error", err)
	}
}

// closeSniffed closes a connection the listener won't hand on.
func closeSniffed(c net.Conn) {
	if err := c.Close(); err != nil {
		slog.Debug("cannot close connection on the HTTPS port", "error", err)
	}
}

// redirectHost is the host[:port] for the redirect's Location from the
// request's Host header, or false when the header is missing or not a
// plain host[:port]. A Host without a port means the client used http's
// default, 80, to reach this listener; unless the listener is on 443, the
// redirect names its port, since https's default would miss it.
func redirectHost(host string, local net.Addr) (string, bool) {
	if !validRedirectHost(host) {
		return "", false
	}
	u, err := url.Parse("https://" + host)
	if err != nil {
		return "", false
	}
	if u.Port() != "" {
		return host, true
	}
	tcp, ok := local.(*net.TCPAddr)
	if !ok || tcp.Port == 443 {
		return host, true
	}
	return net.JoinHostPort(u.Hostname(), strconv.Itoa(tcp.Port)), true
}

// validRedirectHost says whether host, from the request's Host header, is
// a plain host[:port] that can go into the Location as is.
func validRedirectHost(host string) bool {
	if host == "" || strings.ContainsAny(host, "/\\@?#%\r\n\t ") {
		return false
	}
	u, err := url.Parse("https://" + host)
	return err == nil && u.Host == host && u.User == nil
}

// peekedConn is a connection whose first bytes were already read into r.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}
