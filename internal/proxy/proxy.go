// MIT License
//
// Copyright (c) 2025 Matheus Pimenta
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package proxy

import (
	"context"
	"errors"
	"io"
	"net"
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
				select {
				case <-ctx.Done():
					return
				default:
				}
				logger().WithError(err).Error("error accepting connection")
				continue
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
	// read first line to determine if it's a metadata request
	var buf []byte
	matches := true
	toRead := "GET /computeMetadata/v1"
	l := logger().WithField("clientAddr", client.RemoteAddr().String())
	for len(toRead) > 0 {
		b := make([]byte, len(toRead))
		n, err := client.Read(b)
		if err != nil {
			l.WithError(err).Error("error reading from client")
			client.Close()
			return
		}
		buf = append(buf, b[:n]...)
		if string(toRead[:n]) != string(b[:n]) {
			// not a metadata request
			matches = false
			break
		}
		toRead = toRead[n:]
	}
	client = &sniffedConn{Conn: client, buf: buf}

	// if it's a metadata request, enqueue it and return
	if matches {
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

func (s *sniffedConn) Read(b []byte) (int, error) {
	if len(s.buf) > 0 {
		n := copy(b, s.buf)
		s.buf = s.buf[n:]
		return n, nil
	}
	return s.Conn.Read(b)
}
