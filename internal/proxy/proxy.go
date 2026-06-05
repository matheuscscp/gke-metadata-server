// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

func logger() logrus.FieldLogger {
	return logging.FromContext(context.Background()).WithField("component", "proxy")
}

type proxy struct {
	net.Listener

	queue  chan net.Conn
	ctx    context.Context
	cancel context.CancelFunc

	dialLatencyMillis *prometheus.HistogramVec
	activeConnections prometheus.Gauge
}

type sniffedConn struct {
	net.Conn
	buf []byte
}

// Listen creates a new proxy listener on the given network and address.
// It sniffs incoming connections to decide whether to route them to the
// metadata server or to 169.254.169.254:80. It's supposed to be used with
// the eBPF routing mode, which allows for proxying connections due to how
// eBPF can identify the calling process. Supports only the "tcp" network.
func Listen(address string, dialLatencyMillis *prometheus.HistogramVec,
	activeConnections prometheus.Gauge) (net.Listener, error) {

	l, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())

	p := &proxy{
		Listener: l,

		queue:  make(chan net.Conn, 100),
		ctx:    ctx,
		cancel: cancel,

		dialLatencyMillis: dialLatencyMillis,
		activeConnections: activeConnections,
	}

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					logger().WithError(err).Error("error accepting connection")
				}
				p.Close()
				return
			}

			go p.handle(c)
		}
	}()

	return p, nil
}

// Accept implements net.Listener.
func (p *proxy) Accept() (net.Conn, error) {
	select {
	case conn := <-p.queue:
		return conn, nil
	case <-p.ctx.Done():
		return nil, net.ErrClosed
	}
}

// Addr implements net.Listener.
func (p *proxy) Addr() net.Addr {
	return p.Listener.Addr()
}

// Close implements net.Listener.
func (p *proxy) Close() error {
	p.cancel()
	return p.Listener.Close()
}

func (p *proxy) handle(client net.Conn) {
	l := logger().WithField("clientAddr", client.RemoteAddr().String())

	// Sniff the HTTP request line to decide where this connection goes. The
	// genuine GKE metadata server serves the root path and everything under
	// /computeMetadata, so those must reach the inner metadata server. The root
	// probe is what ADC libraries (google-auth ping, the Go SDK testOnGCE) use
	// to detect GCE, so it cannot be forwarded upstream. Any other request is
	// proxied through to 169.254.169.254:80 verbatim. We read just enough of
	// "METHOD PATH HTTP/.." to extract the path, bounded by sniffCap so a slow
	// or garbage client cannot make us buffer unboundedly.
	const sniffCap = 8192
	var buf []byte
	chunk := make([]byte, 256)
	for {
		n, err := client.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if requestPathComplete(buf) || len(buf) >= sniffCap {
			break
		}
		if err != nil {
			l.WithError(err).Error("error reading from client")
			client.Close()
			return
		}
	}
	client = &sniffedConn{Conn: client, buf: buf}

	// if it targets the metadata surface, enqueue it for the inner server
	if isMetadataRequest(buf) {
		select {
		case p.queue <- client:
		case <-p.ctx.Done():
			client.Close()
		}
		return
	}

	// not a metadata request. proxy
	const serverAddr = "169.254.169.254:80"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	server, err := (&net.Dialer{}).DialContext(ctx, "tcp", serverAddr)
	if err != nil {
		p.dialLatencyMillis.
			WithLabelValues(client.RemoteAddr().(*net.TCPAddr).IP.String()).
			Observe(float64(time.Since(start).Milliseconds()))
		client.Close()
		if !errors.Is(err, context.DeadlineExceeded) {
			l.WithError(err).Errorf("error dialing %s", serverAddr)
		}
		return
	}
	p.dialLatencyMillis.
		WithLabelValues(client.RemoteAddr().(*net.TCPAddr).IP.String()).
		Observe(float64(time.Since(start).Milliseconds()))
	p.activeConnections.Inc()
	defer p.activeConnections.Dec()
	defer client.Close()
	defer server.Close()
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(server, client)
		server.(*net.TCPConn).CloseWrite()
		done <- struct{}{}
	}()
	go func() {
		io.Copy(client, server)
		client.(*sniffedConn).Conn.(*net.TCPConn).CloseWrite()
		done <- struct{}{}
	}()
	<-done
	<-done
}

// requestPathComplete reports whether buf already contains the full HTTP
// request path, meaning the second space in "METHOD PATH HTTP/.." has been read
// or the request line ended, so sniffing can stop.
func requestPathComplete(buf []byte) bool {
	if bytes.IndexByte(buf, '\n') >= 0 {
		return true
	}
	sp := bytes.IndexByte(buf, ' ')
	return sp >= 0 && bytes.IndexByte(buf[sp+1:], ' ') >= 0
}

// isMetadataRequest reports whether the sniffed request line targets the
// metadata surface served by the emulator, meaning a GET for the root path or
// any path under /computeMetadata. This mirrors what the genuine GKE metadata
// server serves with 200 and the Metadata-Flavor Google header. Any other
// request is proxied through to the real 169.254.169.254:80 endpoint.
func isMetadataRequest(buf []byte) bool {
	const method = "GET "
	if !bytes.HasPrefix(buf, []byte(method)) {
		return false
	}
	rest := buf[len(method):]
	sp := bytes.IndexByte(rest, ' ')
	if sp < 0 {
		return false
	}
	path := string(rest[:sp])
	if q := strings.IndexByte(path, '?'); q >= 0 {
		path = path[:q]
	}
	return path == "/" || path == "/computeMetadata" || strings.HasPrefix(path, "/computeMetadata/")
}

func (s *sniffedConn) Read(b []byte) (int, error) {
	if len(s.buf) > 0 {
		n := copy(b, s.buf)
		s.buf = s.buf[n:]
		return n, nil
	}
	return s.Conn.Read(b)
}
