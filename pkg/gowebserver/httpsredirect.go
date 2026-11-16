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
	"bytes"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	// tlsClientHello is the content type of the TLS record that opens every TLS
	// connection, so a connection starting with another byte is not TLS.
	tlsClientHello = 0x16

	// sniffQuiet is how long a connection may go without sending while its
	// first request is read. A browser writes the request as soon as the
	// connection opens; a proxy or a slow client can pause in the middle.
	sniffQuiet = 2 * time.Second

	// sniffBudget caps the whole sniff, so a connection trickling out a request
	// cannot hold a worker open forever.
	sniffBudget = 10 * time.Second

	// sniffLimit caps the request bytes read to name the redirect's target.
	sniffLimit = 8 * 1024

	// httpsDefaultPort is the port an HTTPS URL uses when it names none.
	httpsDefaultPort = 443
)

// endOfHeaders separates an HTTP request's headers from its body.
var endOfHeaders = []byte("\r\n\r\n")

// httpsRedirectListener wraps the TLS listener so a plaintext HTTP request
// reaching it is answered with a redirect to the same URL over HTTPS, instead of
// the "Client sent an HTTP request to an HTTPS server." that Go writes from
// inside a TLS handshake, where no handler can see the request.
type httpsRedirectListener struct {
	net.Listener

	// port is the port the TLS listener holds, added to a redirect's host when
	// the request names a host with no port in it.
	port int

	// ready carries each connection once its sniff is over, in whatever order
	// they finish; a nil conn means it was a plaintext request that has already
	// been answered and closed.
	ready chan sniffResult

	// stopping is closed by Close so the accepting and sniffing goroutines stop
	// instead of waiting on a connection nobody will take.
	stopping chan struct{}

	closeOnce sync.Once
}

// sniffResult is a connection ready for the TLS handshake, an already answered
// plaintext request (a nil conn), or the wrapped listener's accepting error.
type sniffResult struct {
	conn net.Conn
	err  error
}

// newHTTPSRedirectListener wraps l so a plaintext request is redirected to the
// same URL on port. It accepts in its own goroutine, so a connection being
// examined cannot delay the connections that arrive behind it.
func newHTTPSRedirectListener(l net.Listener, port int) *httpsRedirectListener {
	wrapped := &httpsRedirectListener{
		Listener: l,
		port:     port,
		ready:    make(chan sniffResult),
		stopping: make(chan struct{}),
	}
	go wrapped.accept()
	return wrapped
}

// accept takes connections off the wrapped listener and sniffs each in a
// goroutine of its own, which is what keeps a quiet connection from serializing
// the ones after it.
func (l *httpsRedirectListener) accept() {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			l.deliver(sniffResult{err: err})
			return
		}
		go l.sniff(conn)
	}
}

// Accept hands out connections whose first bytes have already been read, taking
// them in the order their sniffs finish and skipping the plaintext ones that
// have already been answered and closed.
func (l *httpsRedirectListener) Accept() (net.Conn, error) {
	for {
		select {
		case result := <-l.ready:
			if result.err != nil {
				return nil, result.err
			}
			if result.conn != nil {
				return result.conn, nil
			}
		case <-l.stopping:
			return nil, net.ErrClosed
		}
	}
}

// Close stops the goroutines that feed Accept and closes the wrapped listener.
func (l *httpsRedirectListener) Close() error {
	l.stop()
	return l.Listener.Close()
}

// stop ends the goroutines once and leaves the wrapped listener to whoever else
// holds it, which is the server when it is done serving, and the caller when
// serving never started.
func (l *httpsRedirectListener) stop() {
	l.closeOnce.Do(func() { close(l.stopping) })
}

// deliver hands a sniffed connection to Accept, or closes it when the listener
// is already stopping and no Accept is left to take it.
func (l *httpsRedirectListener) deliver(result sniffResult) {
	select {
	case l.ready <- result:
	case <-l.stopping:
		if result.conn == nil {
			return
		}
		if err := result.conn.Close(); err != nil {
			slog.Debug("failed to close a connection while stopping", "remoteAddr", result.conn.RemoteAddr(), "error", err)
		}
	}
}

// sniff answers a plaintext HTTP request with a redirect and closes it, and
// hands everything else to the TLS handshake with its bytes put back in front.
func (l *httpsRedirectListener) sniff(conn net.Conn) {
	request, read := readRequest(conn)
	if request == nil {
		// TLS, or something else, reaches the handshake whole.
		if len(read) > 0 {
			conn = &peekedConn{Conn: conn, read: read}
		}
		l.deliver(sniffResult{conn: conn})
		return
	}

	location, err := redirectToHTTPS(conn, l.port, request)
	if err != nil {
		// The client is usually gone already, which is nothing to report on.
		slog.Debug("failed to redirect a plaintext request to HTTPS", "remoteAddr", conn.RemoteAddr(), "error", err)
	} else {
		// Logged at info because it replaces an info line from Go's TLS
		// handshake, and because it is how a server owner tells that this
		// client is being sent to HTTPS rather than refused.
		slog.Info("redirected a plaintext request to HTTPS", "remoteAddr", conn.RemoteAddr(), "method", request.Method, "location", location)
	}
	if err := conn.Close(); err != nil {
		slog.Debug("failed to close a redirected connection", "remoteAddr", conn.RemoteAddr(), "error", err)
	}
	l.deliver(sniffResult{})
}

// readRequest returns the plaintext HTTP request opening conn, or nil when conn
// does not open with a whole HTTP/1 request. The bytes read come back so a
// connection that is not a plaintext request still reaches the handshake whole.
func readRequest(conn net.Conn) (*http.Request, []byte) {
	read := readRequestBytes(conn)
	if len(read) == 0 || read[0] == tlsClientHello {
		return nil, read
	}
	request, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(read)))
	if err != nil || request.ProtoMajor != 1 {
		// A request Go cannot parse, and one that is not HTTP/1 at all such as
		// an "PRI * HTTP/2.0" cleartext HTTP/2 handshake, is described better by
		// the TLS handshake than by a redirect naming a URL it never asked for.
		return nil, read
	}
	return request, read
}

// readRequestBytes reads conn's first byte and, unless it shows TLS, the rest of
// a plaintext request's headers. It stops at the read limit, when the headers
// are complete, or when conn stays quiet for sniffQuiet (sniffBudget in all).
func readRequestBytes(conn net.Conn) []byte {
	defer func() {
		// The TLS handshake sets the deadlines it wants.
		if err := conn.SetReadDeadline(time.Time{}); err != nil {
			slog.Debug("failed to clear the read deadline of a connection", "remoteAddr", conn.RemoteAddr(), "error", err)
		}
	}()

	budgetEnd := time.Now().Add(sniffBudget)

	first := make([]byte, 1)
	n, err := readQuiet(conn, first, budgetEnd)
	read := first[:n]
	if err != nil || n == 0 || read[0] == tlsClientHello {
		return read
	}

	for !bytes.Contains(read, endOfHeaders) && len(read) < sniffLimit {
		buf := make([]byte, sniffLimit-len(read))
		n, err = readQuiet(conn, buf, budgetEnd)
		read = append(read, buf[:n]...)
		if err != nil || n == 0 {
			break
		}
	}
	return read
}

// readQuiet reads from conn, giving up if conn goes quiet for sniffQuiet or the
// sniff has already used sniffBudget.
func readQuiet(conn net.Conn, buf []byte, budgetEnd time.Time) (int, error) {
	quietEnd := time.Now().Add(sniffQuiet)
	if quietEnd.After(budgetEnd) {
		quietEnd = budgetEnd
	}
	if !time.Now().Before(quietEnd) {
		// No time left to wait, and a deadline in the past would end the
		// connection rather than just the sniff.
		return 0, net.ErrClosed
	}
	if err := conn.SetReadDeadline(quietEnd); err != nil {
		slog.Debug("failed to set a read deadline to inspect a connection", "remoteAddr", conn.RemoteAddr(), "error", err)
		return 0, err
	}
	return conn.Read(buf)
}

// redirectToHTTPS answers a plaintext request with a redirect to the same URL
// over HTTPS, and returns the URL it names. A 308 keeps the method, which the
// 301 that turns a POST into a GET does not.
func redirectToHTTPS(conn net.Conn, port int, request *http.Request) (string, error) {
	host := request.Host
	if host == "" {
		host = request.URL.Host
	}
	if host == "" {
		// A request naming no host at all, an HTTP/1.0 one, is redirected to
		// the address it was connected to, which already carries a port.
		host = conn.LocalAddr().String()
	}

	// A request naming no port arrived at this listener's port, which is not
	// 443 unless the URL said so.
	if _, _, err := net.SplitHostPort(host); err != nil && port != httpsDefaultPort {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	location := "https://" + host + request.URL.RequestURI()

	// The reply is given the same window a connection is given to speak.
	if err := conn.SetWriteDeadline(time.Now().Add(sniffQuiet)); err != nil {
		return location, err
	}
	response := &http.Response{
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		StatusCode: http.StatusPermanentRedirect,
		Header:     http.Header{"Location": []string{location}},
		Close:      true,
	}
	return location, response.Write(conn)
}

// peekedConn is a connection whose already read bytes, taken to decide what it
// speaks, are read again before the rest of it.
type peekedConn struct {
	net.Conn
	read []byte
}

// Read returns the already read bytes before the rest of the connection.
func (c *peekedConn) Read(p []byte) (int, error) {
	if len(c.read) > 0 {
		n := copy(p, c.read)
		c.read = c.read[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}
