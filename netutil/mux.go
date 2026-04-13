package netutil

import (
	"bufio"
	"crypto/tls"
	"net"
	"sync"
)

// peekedConn wraps a net.Conn with a buffered reader so that bytes
// consumed during protocol detection are not lost.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(b []byte) (int, error) { return c.r.Read(b) }

// subListener is a virtual net.Listener backed by a channel of connections.
type subListener struct {
	ch   chan net.Conn
	addr net.Addr
	done chan struct{}
	once sync.Once
}

func newSubListener(addr net.Addr) *subListener {
	return &subListener{
		ch:   make(chan net.Conn, 16),
		addr: addr,
		done: make(chan struct{}),
	}
}

func (l *subListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *subListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *subListener) Addr() net.Addr { return l.addr }

// SplitListener accepts connections from a base listener and routes them
// to one of two virtual sub-listeners based on whether the first byte is
// a TLS ClientHello record (0x16). Use Plain() for non-TLS connections
// and TLS() for raw TLS connections (caller must wrap with tls.NewListener).
type SplitListener struct {
	base  net.Listener
	plain *subListener
	tls   *subListener
	done  chan struct{}
	once  sync.Once
}

// NewSplitListener creates a SplitListener from the given base listener.
func NewSplitListener(base net.Listener) *SplitListener {
	addr := base.Addr()
	return &SplitListener{
		base:  base,
		plain: newSubListener(addr),
		tls:   newSubListener(addr),
		done:  make(chan struct{}),
	}
}

// Plain returns a net.Listener that yields non-TLS connections.
func (s *SplitListener) Plain() net.Listener { return s.plain }

// TLS returns a net.Listener that yields raw TLS connections
// (not yet TLS-handshaken — wrap with tls.NewListener).
func (s *SplitListener) TLS() net.Listener { return s.tls }

// Serve runs the accept loop. It blocks until the base listener is closed.
func (s *SplitListener) Serve() {
	for {
		conn, err := s.base.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			continue
		}

		br := bufio.NewReader(conn)
		b, err := br.Peek(1)
		if err != nil {
			conn.Close()
			continue
		}

		pc := &peekedConn{Conn: conn, r: br}
		if b[0] == 0x16 { // TLS ClientHello
			select {
			case s.tls.ch <- pc:
			case <-s.done:
				conn.Close()
				return
			}
		} else {
			select {
			case s.plain.ch <- pc:
			case <-s.done:
				conn.Close()
				return
			}
		}
	}
}

// Close closes the base listener and both sub-listeners.
func (s *SplitListener) Close() error {
	s.once.Do(func() { close(s.done) })
	s.plain.Close()
	s.tls.Close()
	return s.base.Close()
}

// TransparentTLSListener wraps a base listener and automatically performs
// TLS handshake for connections starting with a TLS ClientHello (0x16).
// Plain connections are returned as-is. This lets a server (e.g. RTSP)
// accept both encrypted and unencrypted connections on the same port.
type TransparentTLSListener struct {
	base    net.Listener
	tlsCfg  *tls.Config
	done    chan struct{}
	once    sync.Once
}

// NewTransparentTLSListener creates a listener that auto-detects TLS.
func NewTransparentTLSListener(base net.Listener, cfg *tls.Config) net.Listener {
	return &TransparentTLSListener{
		base:   base,
		tlsCfg: cfg,
		done:   make(chan struct{}),
	}
}

func (l *TransparentTLSListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.base.Accept()
		if err != nil {
			return nil, err
		}

		br := bufio.NewReader(conn)
		b, err := br.Peek(1)
		if err != nil {
			conn.Close()
			continue
		}

		pc := &peekedConn{Conn: conn, r: br}
		if b[0] == 0x16 { // TLS ClientHello
			return tls.Server(pc, l.tlsCfg), nil
		}
		return pc, nil
	}
}

func (l *TransparentTLSListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.base.Close()
}

func (l *TransparentTLSListener) Addr() net.Addr {
	return l.base.Addr()
}
