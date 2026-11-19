package tlspin

import (
	"bufio"
	"net"
	"net/http"
	"sync"
	"time"
)

// Split serves one port for both: TLS connections go to the returned
// listener, plain HTTP gets a redirect to https:// (someone typing
// http://pi:7421 in a browser). Nothing is served in clear.
func Split(l net.Listener) net.Listener {
	s := &split{Listener: l, conns: make(chan net.Conn), done: make(chan struct{})}
	s.plain = &chanListener{addr: l.Addr(), conns: make(chan net.Conn), done: s.done}
	redirect := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Connection", "close")
			http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusMovedPermanently)
		}),
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	go redirect.Serve(s.plain)
	go s.loop()
	return s
}

type split struct {
	net.Listener
	conns chan net.Conn
	plain *chanListener
	done  chan struct{}
	once  sync.Once
}

func (s *split) loop() {
	for {
		c, err := s.Listener.Accept()
		if err != nil {
			s.Close()
			return
		}
		go s.sort(c)
	}
}

// sort peeks at the first byte: 0x16 starts a TLS handshake.
func (s *split) sort(c net.Conn) {
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReaderSize(c, 1)
	b, err := br.Peek(1)
	c.SetReadDeadline(time.Time{})
	if err != nil {
		c.Close()
		return
	}
	pc := &peeked{Conn: c, r: br}
	dst := s.plain.conns
	if b[0] == 0x16 {
		dst = s.conns
	}
	select {
	case dst <- pc:
	case <-s.done:
		c.Close()
	}
}

func (s *split) Accept() (net.Conn, error) {
	select {
	case c := <-s.conns:
		return c, nil
	case <-s.done:
		return nil, net.ErrClosed
	}
}

func (s *split) Close() error {
	var err error
	s.once.Do(func() {
		close(s.done)
		err = s.Listener.Close()
	})
	return err
}

type peeked struct {
	net.Conn
	r *bufio.Reader
}

func (p *peeked) Read(b []byte) (int, error) { return p.r.Read(b) }

type chanListener struct {
	addr  net.Addr
	conns chan net.Conn
	done  chan struct{}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *chanListener) Close() error   { return nil }
func (l *chanListener) Addr() net.Addr { return l.addr }
