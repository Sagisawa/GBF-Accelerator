package proxy

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type cachedHTTPDate struct {
	sec int64
	str string
}

var httpDateCache atomic.Pointer[cachedHTTPDate]

// fastHTTPDateString returns a RFC 1123 GMT date string cached at 1-second granularity
// to avoid repeated time formatting allocations on high-frequency cache hits.
func fastHTTPDateString() string {
	now := time.Now()
	sec := now.Unix()
	if cur := httpDateCache.Load(); cur != nil && cur.sec == sec {
		return cur.str
	}
	s := now.UTC().Format(http.TimeFormat)
	httpDateCache.Store(&cachedHTTPDate{sec: sec, str: s})
	return s
}

type h2ConnMetaKey struct{}

type h2ConnMeta struct {
	rawConn    net.Conn
	targetHost string
}

// connFeedListener feeds already-handshaked HTTP/2 *tls.Conn connections into
// a long-lived stdlib *http.Server without opening a second TCP socket.
type connFeedListener struct {
	mu     sync.Mutex
	closed bool
	ch     chan net.Conn
	done   chan struct{}
}

func newConnFeedListener() *connFeedListener {
	return &connFeedListener{
		ch:   make(chan net.Conn, 64),
		done: make(chan struct{}),
	}
}

func (l *connFeedListener) Enqueue(c net.Conn) bool {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return false
	}
	done := l.done
	ch := l.ch
	l.mu.Unlock()

	select {
	case ch <- c:
		return true
	case <-done:
		return false
	}
}

func (l *connFeedListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *connFeedListener) Close() error {
	l.mu.Lock()
	if !l.closed {
		l.closed = true
		close(l.done)
	}
	l.mu.Unlock()
	return nil
}

func (l *connFeedListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

// h2ResponseWriter adapts http.ResponseWriter to the io.Writer interface shared
// across ProxyServer's decrypted request handlers, allowing HTTP/2 multiplexed
// streams to coexist with the zero-allocation HTTP/1.1 raw-wire fast path.
type h2ResponseWriter struct {
	rw      http.ResponseWriter
	flusher http.Flusher
}

func (h *h2ResponseWriter) Write(p []byte) (int, error) {
	return h.rw.Write(p)
}

func (h *h2ResponseWriter) Flush() {
	if h.flusher != nil {
		h.flusher.Flush()
	}
}

func writeH2HTTPResponse(hw *h2ResponseWriter, statusCode int, header http.Header, body []byte, isHead bool) {
	dst := hw.rw.Header()
	hasDate := header != nil && header.Get("Date") != ""
	if !hasDate {
		dst.Set("Date", fastHTTPDateString())
	}
	if header != nil {
		for k, vv := range header {
			if strings.EqualFold(k, "set-cookie") {
				// P0: Preserve each Set-Cookie header independently without folding.
				for _, cookie := range vv {
					dst.Add("Set-Cookie", cookie)
				}
				continue
			}
			if isHopByHop(k) || strings.EqualFold(k, "content-length") || strings.EqualFold(k, "connection") {
				continue
			}
			if !hasDate && strings.EqualFold(k, "date") {
				continue
			}
			// P0: Zero header pollution.
			if hasCaseInsensitivePrefix(k, "x-proxy-") ||
				hasCaseInsensitivePrefix(k, "x-cache-") ||
				hasCaseInsensitivePrefix(k, "x-acceleration-") {
				continue
			}
			fk := formatHeaderKey(k)
			for _, v := range vv {
				dst.Add(fk, v)
			}
		}
	}

	if statusCode != http.StatusNoContent && statusCode != http.StatusNotModified && (statusCode < 100 || statusCode >= 200) {
		if isHead && len(body) == 0 && header != nil && header.Get("Content-Length") != "" {
			dst.Set("Content-Length", header.Get("Content-Length"))
		} else {
			dst.Set("Content-Length", strconv.Itoa(len(body)))
		}
	}

	hw.rw.WriteHeader(statusCode)
	if !isHead && len(body) > 0 && statusCode != http.StatusNoContent && statusCode != http.StatusNotModified && (statusCode < 100 || statusCode >= 200) {
		_, _ = hw.rw.Write(body)
	}
}

func (s *ProxyServer) ensureH2Server() (*connFeedListener, *http.Server) {
	s.h2Mu.Lock()
	defer s.h2Mu.Unlock()

	if s.h2Stopped.Load() {
		return nil, nil
	}
	if s.h2Listener != nil && s.h2Server != nil {
		return s.h2Listener, s.h2Server
	}

	ln := newConnFeedListener()
	srv := &http.Server{
		IdleTimeout: 5 * time.Minute,
		Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			targetHost := ""
			if meta, ok := r.Context().Value(h2ConnMetaKey{}).(h2ConnMeta); ok {
				targetHost = meta.targetHost
			}
			if targetHost == "" {
				targetHost = r.Host
				if h, p, err := net.SplitHostPort(targetHost); err == nil && (p == "443" || p == "80") {
					targetHost = h
				}
				targetHost = strings.ToLower(strings.TrimRight(strings.Trim(targetHost, "[]"), "."))
			}
			if r.RequestURI == "" && r.URL != nil {
				r.RequestURI = r.URL.RequestURI()
			}
			fl, _ := rw.(http.Flusher)
			hw := &h2ResponseWriter{rw: rw, flusher: fl}
			s.handleDecryptedRequest(hw, r, targetHost)
		}),
		HTTP2: &http.HTTP2Config{
			MaxConcurrentStreams: 256,
			MaxReadFrameSize:     1 << 20, // 1 MiB
		},
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if meta, ok := s.h2Meta.Load(c); ok {
				return context.WithValue(ctx, h2ConnMetaKey{}, meta)
			}
			return ctx
		},
		ConnState: func(c net.Conn, state http.ConnState) {
			if state == http.StateClosed {
				if raw, ok := s.h2Meta.LoadAndDelete(c); ok {
					if meta, ok2 := raw.(h2ConnMeta); ok2 && meta.rawConn != nil {
						s.untrackConn(meta.rawConn)
					}
				}
			}
		},
	}

	s.h2Listener = ln
	s.h2Server = srv
	go func() {
		_ = srv.Serve(ln)
	}()
	return ln, srv
}

func (s *ProxyServer) stopH2Server() {
	s.h2Mu.Lock()
	ln := s.h2Listener
	srv := s.h2Server
	s.h2Listener = nil
	s.h2Server = nil
	s.h2Mu.Unlock()

	if ln != nil {
		_ = ln.Close()
	}
	if srv != nil {
		_ = srv.Close()
	}
	s.h2Meta.Range(func(k, v any) bool {
		if meta, ok := v.(h2ConnMeta); ok && meta.rawConn != nil {
			s.untrackConn(meta.rawConn)
		}
		if c, ok := k.(*tls.Conn); ok {
			_ = c.Close()
		}
		s.h2Meta.Delete(k)
		return true
	})
}

func (s *ProxyServer) handoffH2Conn(tlsConn *tls.Conn, rawConn net.Conn, targetHost string) bool {
	ln, _ := s.ensureH2Server()
	if ln == nil {
		return false
	}
	s.h2Meta.Store(tlsConn, h2ConnMeta{
		rawConn:    rawConn,
		targetHost: targetHost,
	})
	if !ln.Enqueue(tlsConn) {
		s.h2Meta.Delete(tlsConn)
		return false
	}
	return true
}
