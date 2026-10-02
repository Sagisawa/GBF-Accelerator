package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

// --- Mocks and Helpers for copyStreamBuffer semantic verification ---

type mockWriterToReader struct {
	r              io.Reader
	writerToCalled bool
}

func (m *mockWriterToReader) Read(p []byte) (int, error) {
	return m.r.Read(p)
}

func (m *mockWriterToReader) WriteTo(w io.Writer) (int64, error) {
	m.writerToCalled = true
	return io.Copy(w, m.r)
}

type readSizeSpy struct {
	r         io.Reader
	readSizes []int
}

func (s *readSizeSpy) Read(p []byte) (int, error) {
	s.readSizes = append(s.readSizes, len(p))
	return s.r.Read(p)
}

type shortWriter struct {
	limit int
	buf   bytes.Buffer
}

func (s *shortWriter) Write(p []byte) (int, error) {
	if len(p) > s.limit {
		n, _ := s.buf.Write(p[:s.limit])
		return n, nil // short write with nil error
	}
	return s.buf.Write(p)
}

type simultaneousEOFReader struct {
	data   []byte
	offset int
}

func (s *simultaneousEOFReader) Read(p []byte) (int, error) {
	if s.offset >= len(s.data) {
		return 0, io.EOF
	}
	n := copy(p, s.data[s.offset:])
	s.offset += n
	return n, io.EOF
}

type partialReadErrorReader struct {
	data       []byte
	errToThrow error
	called     bool
}

func (r *partialReadErrorReader) Read(p []byte) (int, error) {
	if !r.called {
		r.called = true
		n := copy(p, r.data)
		return n, r.errToThrow
	}
	return 0, r.errToThrow
}

type failingWriteStepWriter struct {
	failAfterBytes int
	writtenSoFar   int
	errToReturn    error
}

func (w *failingWriteStepWriter) Write(p []byte) (int, error) {
	if w.writtenSoFar+len(p) > w.failAfterBytes {
		allowed := w.failAfterBytes - w.writtenSoFar
		if allowed <= 0 {
			return 0, w.errToReturn
		}
		w.writtenSoFar += allowed
		return allowed, w.errToReturn
	}
	w.writtenSoFar += len(p)
	return len(p), nil
}

type invalidNegativeWriter struct{}

func (w *invalidNegativeWriter) Write(p []byte) (int, error) {
	return -1, nil
}

type invalidOverflowWriter struct{}

func (w *invalidOverflowWriter) Write(p []byte) (int, error) {
	return len(p) + 10, nil
}

// --- Unit Tests for copyStreamBuffer Semantics ---

func TestCopyStreamBuffer_StandardEOF(t *testing.T) {
	payload := bytes.Repeat([]byte("A"), 1024*1024) // 1MB
	src := bytes.NewReader(payload)
	var dst bytes.Buffer
	buf := make([]byte, 512*1024)

	written, err := copyStreamBuffer(&dst, src, buf)
	if err != nil {
		t.Fatalf("expected nil error on EOF, got: %v", err)
	}
	if written != int64(len(payload)) {
		t.Fatalf("written mismatch: got %d, want %d", written, len(payload))
	}
	if !bytes.Equal(dst.Bytes(), payload) {
		t.Fatal("content mismatch")
	}
}

func TestCopyStreamBuffer_ReadWithEOFSimultaneous(t *testing.T) {
	payload := []byte("hello world simultaneous eof test")
	src := &simultaneousEOFReader{data: payload}
	var dst bytes.Buffer
	buf := make([]byte, 512*1024)

	written, err := copyStreamBuffer(&dst, src, buf)
	if err != nil {
		t.Fatalf("expected nil error when Read returns nr > 0 and EOF, got: %v", err)
	}
	if written != int64(len(payload)) {
		t.Fatalf("written mismatch: got %d, want %d", written, len(payload))
	}
	if !bytes.Equal(dst.Bytes(), payload) {
		t.Fatalf("content mismatch: got %q, want %q", dst.Bytes(), payload)
	}
}

func TestCopyStreamBuffer_ReadError_EmptyRead(t *testing.T) {
	customErr := errors.New("simulated underlying disk read error")
	src := &partialReadErrorReader{data: nil, errToThrow: customErr}
	var dst bytes.Buffer
	buf := make([]byte, 512*1024)

	written, err := copyStreamBuffer(&dst, src, buf)
	if !errors.Is(err, customErr) {
		t.Fatalf("expected customErr, got: %v", err)
	}
	if written != 0 {
		t.Fatalf("expected 0 written on empty read error, got: %d", written)
	}
	if dst.Len() != 0 {
		t.Fatalf("expected 0 dst bytes, got: %d", dst.Len())
	}
}

func TestCopyStreamBuffer_ReadError_PartialRead(t *testing.T) {
	customErr := errors.New("read error after partial chunk")
	partialData := []byte("chunk before disk corruption")
	src := &partialReadErrorReader{data: partialData, errToThrow: customErr}
	var dst bytes.Buffer
	buf := make([]byte, 512*1024)

	written, err := copyStreamBuffer(&dst, src, buf)
	if !errors.Is(err, customErr) {
		t.Fatalf("expected customErr, got: %v", err)
	}
	if written != int64(len(partialData)) {
		t.Fatalf("expected written %d, got %d", len(partialData), written)
	}
	if !bytes.Equal(dst.Bytes(), partialData) {
		t.Fatalf("expected partialData written, got: %q", dst.Bytes())
	}
}

func TestCopyStreamBuffer_WriteError(t *testing.T) {
	payload := bytes.Repeat([]byte("B"), 256*1024)
	src := bytes.NewReader(payload)
	writeErr := errors.New("broken pipe / client disconnected")
	dst := &failingWriteStepWriter{
		failAfterBytes: 64 * 1024,
		errToReturn:    writeErr,
	}
	buf := make([]byte, 128*1024)

	written, err := copyStreamBuffer(dst, src, buf)
	if !errors.Is(err, writeErr) {
		t.Fatalf("expected writeErr, got: %v", err)
	}
	if written != 64*1024 {
		t.Fatalf("expected written to be 64KB, got %d", written)
	}
}

func TestCopyStreamBuffer_ShortWrite(t *testing.T) {
	payload := bytes.Repeat([]byte("C"), 100*1024)
	src := bytes.NewReader(payload)
	dst := &shortWriter{limit: 50 * 1024}
	buf := make([]byte, 128*1024)

	written, err := copyStreamBuffer(dst, src, buf)
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("expected io.ErrShortWrite, got: %v", err)
	}
	if written != 50*1024 {
		t.Fatalf("expected written to be 50KB, got %d", written)
	}
}

func TestCopyStreamBuffer_InvalidWriteResult(t *testing.T) {
	payload := []byte("some test payload")
	buf := make([]byte, 512*1024)

	// Case A: Writer returns negative nw
	srcA := bytes.NewReader(payload)
	dstA := &invalidNegativeWriter{}
	writtenA, errA := copyStreamBuffer(dstA, srcA, buf)
	if errA == nil || !strings.Contains(errA.Error(), "invalid write result") {
		t.Fatalf("expected invalid write result error for negative nw, got: %v", errA)
	}
	if writtenA != 0 {
		t.Fatalf("expected 0 written on negative write, got %d", writtenA)
	}

	// Case B: Writer returns nw > nr
	srcB := bytes.NewReader(payload)
	dstB := &invalidOverflowWriter{}
	writtenB, errB := copyStreamBuffer(dstB, srcB, buf)
	if errB == nil || !strings.Contains(errB.Error(), "invalid write result") {
		t.Fatalf("expected invalid write result error for overflow nw, got: %v", errB)
	}
	if writtenB != 0 {
		t.Fatalf("expected 0 written on overflow write, got %d", writtenB)
	}
}

func TestCopyStreamBuffer_BypassesWriterTo(t *testing.T) {
	payload := []byte("stream payload")
	inner := bytes.NewReader(payload)
	src := &mockWriterToReader{r: inner}
	var dst bytes.Buffer
	buf := make([]byte, 512*1024)

	written, err := copyStreamBuffer(&dst, src, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if written != int64(len(payload)) {
		t.Fatalf("written mismatch: got %d, want %d", written, len(payload))
	}
	if src.writerToCalled {
		t.Fatal("copyStreamBuffer must NOT invoke WriteTo on src (it must bypass WriterTo to enforce buffer sizing)")
	}
}

func TestCopyStreamBuffer_HonorsBufferSize(t *testing.T) {
	payload := make([]byte, 2*1024*1024) // 2MB
	spy := &readSizeSpy{r: bytes.NewReader(payload)}
	var dst bytes.Buffer
	buf := make([]byte, 512*1024)

	_, err := copyStreamBuffer(&dst, spy, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spy.readSizes) == 0 {
		t.Fatal("expected at least one Read call")
	}
	for i, sz := range spy.readSizes {
		if sz != 512*1024 {
			t.Fatalf("read call %d requested size %d, want exactly %d (512KB)", i, sz, 512*1024)
		}
	}
}

func TestCopyStreamBuffer_ZeroAlloc(t *testing.T) {
	payload := make([]byte, 1024*1024)
	buf := make([]byte, 512*1024)

	var src bytes.Reader
	allocs := testing.AllocsPerRun(10, func() {
		src.Reset(payload)
		_, err := copyStreamBuffer(io.Discard, &src, buf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if allocs != 0 {
		t.Fatalf("expected 0 allocs during copyStreamBuffer, got %v", allocs)
	}
}

func TestCopyStreamBuffer_EmptyBufferPanics(t *testing.T) {
	src := bytes.NewReader([]byte("test"))
	var dst bytes.Buffer

	// Empty slice should panic
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on empty buffer, but did not panic")
		}
	}()
	_, _ = copyStreamBuffer(&dst, src, []byte{})
}

func TestCopyStreamBuffer_NilBufferPanics(t *testing.T) {
	src := bytes.NewReader([]byte("test"))
	var dst bytes.Buffer

	// Nil slice should panic
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on nil buffer, but did not panic")
		}
	}()
	_, _ = copyStreamBuffer(&dst, src, nil)
}

func TestStreamBufferPool_AllocationSize(t *testing.T) {
	p := streamBufferPool.Get().(*[]byte)
	defer streamBufferPool.Put(p)

	if len(*p) != 512*1024 {
		t.Fatalf("expected streamBufferPool buffer length to be %d (512KB), got %d", 512*1024, len(*p))
	}
	if cap(*p) < 512*1024 {
		t.Fatalf("expected streamBufferPool buffer cap >= %d, got %d", 512*1024, cap(*p))
	}
}

// --- End-to-End Regression Tests for sendCachedDiskAssetStream ---

func setupStream512KProxy(t *testing.T) (*ProxyServer, *cache.Manager, string) {
	t.Helper()
	tempDir := t.TempDir()

	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to init cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(filepath.Join(tempDir, "cache"), 32)
	stats := telemetry.NewStats()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	t.Cleanup(func() {
		srv.Stop()
		cacheMgr.Close()
	})
	return srv, cacheMgr, tempDir
}

func TestProxy_DiskStream_512K_ByteForByteIntegrity(t *testing.T) {
	srv, cacheMgr, _ := setupStream512KProxy(t)

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	urlPath := "/assets/sound/bgm_512k_test.mp3"
	cleanKey := strings.TrimPrefix(urlPath, "/")

	// 3.5 MB file (> 2MB MaxDiskDirectReadSize)
	fileSize := int64(3500 * 1024)
	payload := makeTestPayload(int(fileSize))
	for i := 16; i < len(payload); i++ {
		payload[i] = byte((i * 31) ^ (i >> 3))
	}

	filePath, ok := cacheMgr.ResolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)
	if err := os.WriteFile(filePath, payload, 0644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filePath)
	extMeta := fmt.Sprintf(`{"ct":"audio/mpeg","ETag":"\"512k-etag\"","LastModified":"Wed, 21 Oct 2026 07:28:00 GMT","v":1,"verified":true,"size":%d,"mtime":%d}`, fi.Size(), fi.ModTime().UnixNano())
	_ = os.WriteFile(filePath+".ext", []byte(extMeta), 0644)

	cacheMgr.ClearRAM()

	// 1. GET with keep-alive
	reqKeep, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+urlPath, nil)
	reqKeep.RequestURI = urlPath
	var bufKeep bytes.Buffer
	keepAlive := srv.handleDecryptedRequest(&bufKeep, reqKeep, targetHost)
	if !keepAlive {
		t.Fatal("expected keepAlive == true for GET request")
	}

	rawKeep := bufKeep.String()
	if !strings.HasPrefix(rawKeep, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("expected 200 OK, got:\n%s", rawKeep)
	}
	if cl := extractHeader(rawKeep, "Content-Length"); cl != strconv.FormatInt(fileSize, 10) {
		t.Fatalf("expected Content-Length %d, got %q", fileSize, cl)
	}
	if conn := extractHeader(rawKeep, "Connection"); conn != "keep-alive" {
		t.Fatalf("expected Connection: keep-alive, got %q", conn)
	}

	_, _, bodyKeep := splitRawResponse(t, bufKeep.Bytes())
	if !bytes.Equal(bodyKeep, payload) {
		t.Fatalf("streamed body mismatch: got %d bytes, want %d bytes", len(bodyKeep), len(payload))
	}

	// 2. GET with req.Close = true
	cacheMgr.ClearRAM()
	reqClose, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+urlPath, nil)
	reqClose.RequestURI = urlPath
	reqClose.Close = true
	var bufClose bytes.Buffer
	keepAliveClose := srv.handleDecryptedRequest(&bufClose, reqClose, targetHost)
	if keepAliveClose {
		t.Fatal("expected keepAlive == false when req.Close is true")
	}

	rawClose := bufClose.String()
	if !strings.HasPrefix(rawClose, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("expected 200 OK, got:\n%s", rawClose)
	}
	if conn := extractHeader(rawClose, "Connection"); conn != "close" {
		t.Fatalf("expected Connection: close, got %q", conn)
	}

	_, _, bodyClose := splitRawResponse(t, bufClose.Bytes())
	if !bytes.Equal(bodyClose, payload) {
		t.Fatalf("streamed body mismatch with req.Close: got %d bytes, want %d", len(bodyClose), len(payload))
	}
}

func TestProxy_DiskStream_SmallAsset_NeverEntersDiskStream(t *testing.T) {
	srv, cacheMgr, _ := setupStream512KProxy(t)

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	urlPath := "/assets/img/small_asset_1mb.png"
	cleanKey := strings.TrimPrefix(urlPath, "/")

	// 1 MB <= MaxDiskDirectReadSize (2MB)
	fileSize := int64(1024 * 1024)
	payload := makeTestPayload(int(fileSize))

	filePath, ok := cacheMgr.ResolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)
	if err := os.WriteFile(filePath, payload, 0644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filePath)
	extMeta := fmt.Sprintf(`{"ct":"image/png","ETag":"\"small-etag\"","LastModified":"Wed, 21 Oct 2026 07:28:00 GMT","v":1,"verified":true,"size":%d,"mtime":%d}`, fi.Size(), fi.ModTime().UnixNano())
	_ = os.WriteFile(filePath+".ext", []byte(extMeta), 0644)

	cacheMgr.ClearRAM()

	req, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+urlPath, nil)
	req.RequestURI = urlPath
	var buf bytes.Buffer
	keepAlive := srv.handleDecryptedRequest(&buf, req, targetHost)
	if !keepAlive {
		t.Fatal("expected keepAlive == true for small asset")
	}

	raw := buf.String()
	if !strings.HasPrefix(raw, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("expected 200 OK, got:\n%s", raw)
	}

	_, _, body := splitRawResponse(t, buf.Bytes())
	if !bytes.Equal(body, payload) {
		t.Fatalf("body mismatch for small asset: got %d bytes, want %d", len(body), len(payload))
	}
}

func TestProxy_DiskStream_ShortWrite_FailsGracefully(t *testing.T) {
	srv, cacheMgr, _ := setupStream512KProxy(t)

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	urlPath := "/assets/sound/short_write_bgm.mp3"
	cleanKey := strings.TrimPrefix(urlPath, "/")

	fileSize := int64(3 * 1024 * 1024)
	payload := makeTestPayload(int(fileSize))

	filePath, ok := cacheMgr.ResolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)
	_ = os.WriteFile(filePath, payload, 0644)
	fi, _ := os.Stat(filePath)
	extMeta := fmt.Sprintf(`{"ct":"audio/mpeg","ETag":"\"short-etag\"","LastModified":"Wed, 21 Oct 2026 07:28:00 GMT","v":1,"verified":true,"size":%d,"mtime":%d}`, fi.Size(), fi.ModTime().UnixNano())
	_ = os.WriteFile(filePath+".ext", []byte(extMeta), 0644)

	cacheMgr.ClearRAM()

	req, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+urlPath, nil)
	req.RequestURI = urlPath

	// Write headers normally, but fail body with short write
	sw := &shortWriter{limit: 100 * 1024}
	keepAlive := srv.handleDecryptedRequest(sw, req, targetHost)
	if keepAlive {
		t.Fatal("expected keepAlive == false when short write occurs during streaming")
	}
}

// --- Benchmark: 512KB vs 32KB Stream Buffer ---

type countingReader struct {
	r         io.Reader
	readCount atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	c.readCount.Add(1)
	return c.r.Read(p)
}

func BenchmarkStreamThrough_512K_vs_32K(b *testing.B) {
	// 3 MB file
	const size = 3 * 1024 * 1024
	payload := make([]byte, size)

	b.Run("Buffer_512KB", func(b *testing.B) {
		bufPtr := streamBufferPool.Get().(*[]byte)
		defer streamBufferPool.Put(bufPtr)
		copyBuf := *bufPtr

		var r bytes.Reader
		cr := &countingReader{r: &r}

		b.ReportAllocs()
		b.SetBytes(size)
		b.ResetTimer()

		var totalReads int64
		for i := 0; i < b.N; i++ {
			r.Reset(payload)
			cr.readCount.Store(0)
			_, err := copyStreamBuffer(io.Discard, cr, copyBuf)
			if err != nil {
				b.Fatalf("unexpected error: %v", err)
			}
			totalReads += cr.readCount.Load()
		}
		b.ReportMetric(float64(totalReads)/float64(b.N), "reads/op")
	})

	b.Run("Buffer_32KB_Simulated", func(b *testing.B) {
		buf32k := make([]byte, 32*1024)

		var r bytes.Reader
		cr := &countingReader{r: &r}

		b.ReportAllocs()
		b.SetBytes(size)
		b.ResetTimer()

		var totalReads int64
		for i := 0; i < b.N; i++ {
			r.Reset(payload)
			cr.readCount.Store(0)
			_, err := copyStreamBuffer(io.Discard, cr, buf32k)
			if err != nil {
				b.Fatalf("unexpected error: %v", err)
			}
			totalReads += cr.readCount.Load()
		}
		b.ReportMetric(float64(totalReads)/float64(b.N), "reads/op")
	})
}

func BenchmarkCopyStreamBuffer_AllocPurity(b *testing.B) {
	const size = 3 * 1024 * 1024
	payload := make([]byte, size)
	bufPtr := streamBufferPool.Get().(*[]byte)
	defer streamBufferPool.Put(bufPtr)
	copyBuf := *bufPtr

	var r bytes.Reader
	b.ReportAllocs()
	b.SetBytes(size)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Reset(payload)
		_, _ = copyStreamBuffer(io.Discard, &r, copyBuf)
	}
}
