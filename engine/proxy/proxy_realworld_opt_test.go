package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
)

func TestCacheControl_StaleWhileRevalidatePolicy(t *testing.T) {
	cfgMgr := config.NewManager(filepath.Join(t.TempDir(), "config.json"))
	srv := &ProxyServer{cfgMgr: cfgMgr}

	cfgMgr.Update(func(c *config.Config) { c.EnableBrowserCache = false })
	if got := srv.getCacheControlHeader("/assets/img/sp/ui/btn.png"); got != "no-cache" {
		t.Fatalf("expected no-cache when browser cache disabled, got %q", got)
	}

	cfgMgr.Update(func(c *config.Config) { c.EnableBrowserCache = true })
	if got := srv.getCacheControlHeader("/assets/1795000000/js/core.js"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("expected immutable for versioned path, got %q", got)
	}
	if got := srv.getCacheControlHeader("/assets/img/sp/assets/npc/m/3040001000_01.jpg"); got != "public, max-age=3600, stale-while-revalidate=86400" {
		t.Fatalf("expected SWR Cache-Control for unversioned path, got %q", got)
	}
}

func TestWriteResponseBody_TLSRecordPacking_LargePayloads(t *testing.T) {
	cfgMgr := config.NewManager(filepath.Join(t.TempDir(), "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(t.TempDir(), "certs"))
	if err != nil {
		t.Fatalf("cert.NewManager: %v", err)
	}
	srv := &ProxyServer{cfgMgr: cfgMgr}

	// Verify 0 allocs/op on >64KiB cached asset serialization via sendCachedAssetResponseFast.
	item256K := &cache.CacheItem{
		Key:         "assets/img/sp/large_256k.png",
		Data:        bytes.Repeat([]byte("Z"), 256*1024),
		ContentType: "image/png",
		ETag:        `"tls-pack-256k"`,
		Size:        256 * 1024,
	}
	allocs := testing.AllocsPerRun(50, func() {
		srv.sendCachedAssetResponseFast(io.Discard, item256K, false, "/assets/img/sp/large_256k.png", false)
	})
	if allocs != 0 {
		t.Fatalf("expected 0 allocs/op on 256KiB sendCachedAssetResponseFast, got %v", allocs)
	}

	// Verify byte-for-byte delivery over an actual *tls.Conn for 128KiB and 1MiB payloads.
	tlsCfg := certMgr.GetTLSConfig().Clone()
	tlsCfg.NextProtos = []string{"http/1.1"}

	for _, sz := range []int{128 * 1024, 1024 * 1024} {
		payload := make([]byte, sz)
		copy(payload, []byte("\x89PNG\r\n\x1a\n"))
		for i := 8; i < sz; i++ {
			payload[i] = byte(i % 251)
		}
		item := &cache.CacheItem{
			Key:         "assets/img/sp/large_tls.png",
			Data:        payload,
			ContentType: "image/png",
			ETag:        `"tls-pack"`,
			Size:        int64(sz),
		}

		ln, lErr := net.Listen("tcp", "127.0.0.1:0")
		if lErr != nil {
			t.Fatalf("listen: %v", lErr)
		}

		serverDone := make(chan error, 1)
		go func() {
			rawConn, aErr := ln.Accept()
			if aErr != nil {
				serverDone <- aErr
				return
			}
			defer rawConn.Close()
			tConn := tls.Server(rawConn, tlsCfg)
			if hErr := tConn.Handshake(); hErr != nil {
				serverDone <- hErr
				return
			}
			defer tConn.Close()
			srv.sendCachedAssetResponseFast(tConn, item, false, "/assets/img/sp/large_tls.png", true)
			serverDone <- nil
		}()

		cliConn, dErr := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "prd-game-a-granbluefantasy.akamaized.net",
		})
		if dErr != nil {
			_ = ln.Close()
			t.Fatalf("tls.Dial (%dB): %v", sz, dErr)
		}

		req, _ := http.NewRequest(http.MethodGet, "https://prd-game-a-granbluefantasy.akamaized.net/assets/img/sp/large_tls.png", nil)
		resp, rErr := http.ReadResponse(bufio.NewReader(cliConn), req)
		if rErr != nil {
			_ = cliConn.Close()
			_ = ln.Close()
			t.Fatalf("ReadResponse (%dB): %v", sz, rErr)
		}
		got, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		_ = cliConn.Close()
		_ = ln.Close()

		if sErr := <-serverDone; sErr != nil {
			t.Fatalf("server error (%dB): %v", sz, sErr)
		}
		if readErr != nil {
			t.Fatalf("read body (%dB): %v", sz, readErr)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("TLS payload mismatch for %dB: got %dB", sz, len(got))
		}
	}
}

func TestDialSOCKS5Context_AuthNoAuthAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		user     string
		pass     string
		target   string
		wantATYP byte
	}{
		{name: "NoAuthDomain", target: "prd-game-a-granbluefantasy.akamaized.net:443", wantATYP: 0x03},
		{name: "UserPassIPv4", user: "alice", pass: "secret99", target: "127.0.0.1:8443", wantATYP: 0x01},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			defer ln.Close()

			errCh := make(chan error, 1)
			go func() {
				c, aErr := ln.Accept()
				if aErr != nil {
					errCh <- aErr
					return
				}
				defer c.Close()

				// 1. Greeting
				var hdr [2]byte
				if _, e := io.ReadFull(c, hdr[:]); e != nil {
					errCh <- e
					return
				}
				methods := make([]byte, hdr[1])
				if _, e := io.ReadFull(c, methods); e != nil {
					errCh <- e
					return
				}
				if tc.user != "" {
					_, _ = c.Write([]byte{0x05, 0x02})
					var ulen [2]byte
					if _, e := io.ReadFull(c, ulen[:]); e != nil {
						errCh <- e
						return
					}
					u := make([]byte, ulen[1])
					_, _ = io.ReadFull(c, u)
					var plen [1]byte
					_, _ = io.ReadFull(c, plen[:])
					p := make([]byte, plen[0])
					_, _ = io.ReadFull(c, p)
					if string(u) != tc.user || string(p) != tc.pass {
						_, _ = c.Write([]byte{0x01, 0x01})
						errCh <- errors.New("auth mismatch")
						return
					}
					_, _ = c.Write([]byte{0x01, 0x00})
				} else {
					_, _ = c.Write([]byte{0x05, 0x00})
				}

				// 2. Connect request
				var reqHdr [4]byte
				if _, e := io.ReadFull(c, reqHdr[:]); e != nil {
					errCh <- e
					return
				}
				if reqHdr[3] != tc.wantATYP {
					errCh <- errors.New("unexpected ATYP")
					return
				}
				switch reqHdr[3] {
				case 0x01:
					var rest [6]byte
					_, _ = io.ReadFull(c, rest[:])
				case 0x03:
					var dl [1]byte
					_, _ = io.ReadFull(c, dl[:])
					rest := make([]byte, int(dl[0])+2)
					_, _ = io.ReadFull(c, rest)
				}
				// Reply with domain-type bound addr to exercise ATYP=0x03 drain path too
				bndHost := "proxy.local"
				reply := []byte{0x05, 0x00, 0x00, 0x03, byte(len(bndHost))}
				reply = append(reply, bndHost...)
				reply = binary.BigEndian.AppendUint16(reply, 1080)
				_, _ = c.Write(reply)

				// Echo 4 bytes to verify stream works
				var ping [4]byte
				if _, e := io.ReadFull(c, ping[:]); e != nil {
					errCh <- e
					return
				}
				_, _ = c.Write(ping[:])
				errCh <- nil
			}()

			conn, dErr := dialSOCKS5Context(context.Background(), ln.Addr().String(), tc.target, tc.user, tc.pass, 2*time.Second)
			if dErr != nil {
				t.Fatalf("dialSOCKS5Context failed: %v", dErr)
			}
			_, _ = conn.Write([]byte("PING"))
			var pong [4]byte
			if _, rErr := io.ReadFull(conn, pong[:]); rErr != nil {
				_ = conn.Close()
				t.Fatalf("read echo failed: %v", rErr)
			}
			_ = conn.Close()
			if string(pong[:]) != "PING" {
				t.Fatalf("unexpected echo %q", string(pong[:]))
			}
			if sErr := <-errCh; sErr != nil {
				t.Fatalf("mock socks5 server error: %v", sErr)
			}
		})
	}

	// Pre-cancelled context must fail immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := dialSOCKS5Context(ctx, "127.0.0.1:1", "example.com:443", "", "", 2*time.Second); err == nil {
		t.Fatal("expected cancelled dialSOCKS5Context to return error")
	}
}

type countingFlusherRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (r *countingFlusherRecorder) Flush() {
	r.flushes++
	r.ResponseRecorder.Flush()
}

func TestStreamClientWriter_H2FirstChunkFlushAndComplete(t *testing.T) {
	rec := &countingFlusherRecorder{ResponseRecorder: httptest.NewRecorder()}
	hw := &h2ResponseWriter{rw: rec, flusher: rec}
	sc := &streamClientWriter{w: hw, isChunked: false}

	for i := 0; i < 5; i++ {
		if err := sc.WriteChunk([]byte("chunk-")); err != nil {
			t.Fatalf("WriteChunk %d: %v", i, err)
		}
	}
	// Only the very first chunk should have triggered Flush().
	if rec.flushes != 1 {
		t.Fatalf("expected 1 Flush() after 5 WriteChunk calls, got %d", rec.flushes)
	}
	if err := sc.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if rec.flushes != 2 {
		t.Fatalf("expected 2nd Flush() on Finish(), got %d", rec.flushes)
	}
	if rec.Body.String() != "chunk-chunk-chunk-chunk-chunk-" {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
}
