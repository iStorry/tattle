package serve

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"time"
)

// firstByteWait is how long a new connection has to say something before
// it is dropped.
const firstByteWait = 10 * time.Second

// HTTPS serves handler over HTTPS on ln until ctx is cancelled. A browser
// that arrives over plain HTTP on the same port, as happens when someone
// types the address without "https://", is sent to the HTTPS address
// instead of being shown an error.
func HTTPS(ctx context.Context, ln net.Listener, certs *Certs, handler http.Handler) error {
	cfg := &tls.Config{
		GetCertificate: certs.GetCertificate,
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"h2", "http/1.1"},
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS == nil {
				// Temporary, so browsers do not remember it if the owner
				// later moves Tattle behind a reverse proxy.
				http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusTemporaryRedirect)
				return
			}
			handler.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return run(ctx, srv, newSorter(ln, cfg))
}

// HTTP serves handler over plain HTTP, for use behind a reverse proxy that
// provides HTTPS, and for the demo.
func HTTP(ctx context.Context, ln net.Listener, handler http.Handler) error {
	return run(ctx, &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}, ln)
}

func run(ctx context.Context, srv *http.Server, ln net.Listener) error {
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(stop); err != nil {
		return srv.Close()
	}
	return nil
}

// sorter looks at the first byte of each connection. A TLS handshake starts
// with byte 22; anything else is taken to be plain HTTP. TLS connections
// are handed on as such, which net/http recognises and handles.
type sorter struct {
	net.Listener
	cfg    *tls.Config
	ready  chan net.Conn
	closed chan struct{}
	err    error
}

func newSorter(ln net.Listener, cfg *tls.Config) *sorter {
	s := &sorter{Listener: ln, cfg: cfg, ready: make(chan net.Conn), closed: make(chan struct{})}
	go s.acceptLoop()
	return s
}

func (s *sorter) acceptLoop() {
	for {
		conn, err := s.Listener.Accept()
		if err != nil {
			s.err = err
			close(s.closed)
			return
		}
		// Sorted on its own goroutine, so a client that connects and says
		// nothing cannot hold up everyone else.
		go s.sort(conn)
	}
}

func (s *sorter) sort(conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(firstByteWait))
	r := bufio.NewReaderSize(conn, 512)
	first, err := r.Peek(1)
	if err != nil {
		conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	var out net.Conn = &peeked{Conn: conn, r: r}
	if first[0] == 0x16 {
		out = tls.Server(out, s.cfg)
	}
	select {
	case s.ready <- out:
	case <-s.closed:
		conn.Close()
	}
}

func (s *sorter) Accept() (net.Conn, error) {
	select {
	case conn := <-s.ready:
		return conn, nil
	case <-s.closed:
		if s.err == nil {
			return nil, errors.New("listener closed")
		}
		return nil, s.err
	}
}

// peeked is a connection whose first bytes have already been read into r.
type peeked struct {
	net.Conn
	r *bufio.Reader
}

func (p *peeked) Read(b []byte) (int, error) { return p.r.Read(b) }
