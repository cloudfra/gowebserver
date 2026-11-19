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
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// httpsSniffTimeout bounds how long a new connection on the HTTPS port
	// may take to send its first byte, and a plain HTTP one its request
	// head, so a silent or slow client can't hold a goroutine forever.
	httpsSniffTimeout = 10 * time.Second
	// httpsRedirectMaxHeader caps the plain HTTP request head read before
	// redirecting.
	httpsRedirectMaxHeader = 16 << 10
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
// goroutine, under httpsSniffTimeout.
type httpsRedirectListener struct {
	net.Listener
	timeout time.Duration

	conns   chan net.Conn
	errs    chan error
	done    chan struct{}
	closing sync.Once
}

func newHTTPSRedirectListener(inner net.Listener, timeout time.Duration) *httpsRedirectListener {
	l := &httpsRedirectListener{
		Listener: inner,
		timeout:  timeout,
		conns:    make(chan net.Conn),
		errs:     make(chan error, 1),
		done:     make(chan struct{}),
	}
	go l.acceptLoop()
	return l
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
			if te, ok := err.(interface{ Temporary() bool }); ok && te.Temporary() {
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
	if err := c.SetReadDeadline(time.Now().Add(l.timeout)); err != nil {
		closeSniffed(c)
		return
	}
	first, err := br.Peek(1)
	if err != nil {
		closeSniffed(c)
		return
	}
	if isPlainHTTPStart(first[0]) {
		redirectToHTTPS(c, br)
		closeSniffed(c)
		return
	}
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

// redirectToHTTPS reads one plain HTTP request head from br, still under
// the connection's read deadline, and answers it with a redirect to the
// same URL over https://. GET and HEAD get 301 Moved Permanently; other
// methods 308 Permanent Redirect, which keeps the method and body.
func redirectToHTTPS(c net.Conn, br *bufio.Reader) {
	resp := &http.Response{
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{},
		Close:      true,
	}
	req, err := http.ReadRequest(bufio.NewReaderSize(io.LimitReader(br, httpsRedirectMaxHeader), 4096))
	if err != nil {
		resp.StatusCode = http.StatusBadRequest
		writeRedirectResponse(c, resp, nil, "Client sent an HTTP request to an HTTPS server.\n")
		return
	}
	host := req.Host
	if !validRedirectHost(host) {
		host = c.LocalAddr().String()
	}
	target := (&url.URL{Scheme: "https", Host: host}).String() + req.URL.RequestURI()
	if !strings.HasPrefix(req.URL.RequestURI(), "/") {
		target = (&url.URL{Scheme: "https", Host: host, Path: "/"}).String()
	}
	resp.StatusCode = http.StatusMovedPermanently
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		resp.StatusCode = http.StatusPermanentRedirect
	}
	resp.Header.Set("Location", target)
	body := ""
	if req.Method != http.MethodHead {
		body = "<a href=\"" + htmlReplacer.Replace(target) + "\">" + http.StatusText(resp.StatusCode) + "</a>.\n"
		resp.Header.Set("Content-Type", "text/html; charset=utf-8")
	}
	writeRedirectResponse(c, resp, req, body)
}

var htmlReplacer = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;")

func writeRedirectResponse(c net.Conn, resp *http.Response, req *http.Request, body string) {
	resp.Request = req
	resp.ContentLength = int64(len(body))
	resp.Body = io.NopCloser(strings.NewReader(body))
	if err := c.SetWriteDeadline(time.Now().Add(httpsSniffTimeout)); err != nil {
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
	// Let the client read the response before the close, rather than see
	// a reset because of request bytes (a body, say) left unread.
	if err := tc.CloseWrite(); err != nil {
		return
	}
	if err := c.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return
	}
	if _, err := io.Copy(io.Discard, io.LimitReader(c, 64<<10)); err != nil {
		slog.Debug("HTTP to HTTPS redirect: draining the request ended early", "error", err)
	}
}

// closeSniffed closes a connection the listener won't hand on.
func closeSniffed(c net.Conn) {
	if err := c.Close(); err != nil {
		slog.Debug("cannot close connection on the HTTPS port", "error", err)
	}
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
