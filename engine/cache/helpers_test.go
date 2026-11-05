package cache

import (
	"os"
	"path/filepath"
	"testing"
)

var (
	testValidPNG   = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	testJSHeaders  = map[string]string{"content-type": "application/javascript"}
	testPNGHeaders = map[string]string{"content-type": "image/png"}
)

func writeTestDiskFile(t *testing.T, baseDir, rel string, data []byte) string {
	t.Helper()
	p := filepath.Join(baseDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatalf("writeTestDiskFile mkdir failed for %s: %v", rel, err)
	}
	if err := os.WriteFile(p, data, 0644); err != nil {
		t.Fatalf("writeTestDiskFile write failed for %s: %v", rel, err)
	}
	return p
}

func populateMemoryStateForTest(mgr *Manager, ns, key string, body []byte) (ramKey string, gen uint64, tok opToken) {
	headers := map[string]string{"content-type": "application/javascript"}
	_, _, _, gen, tok, ok := mgr.saveRAMInternal(ns, key, headers, body, false, true)
	if !ok {
		panic("saveRAMInternal failed in populateMemoryStateForTest")
	}
	ramKey = makeRAMKey(ns, key)
	rGen := mgr.residentPool.Generation()
	if rGen == 0 {
		rGen = mgr.residentPool.NextGeneration()
	}
	mgr.residentPool.ResetForGen(rGen)
	mgr.residentPool.Set(ramKey, &CacheItem{
		Key:         ramKey,
		Data:        body,
		ContentType: "application/javascript",
		Size:        int64(len(body)),
	})
	mgr.setDiskMeta(ramKey, diskMetaEntry{
		size:      int64(len(body)),
		namespace: ns,
		exists:    true,
		hasMeta:   true,
		verified:  true,
	})
	return ramKey, gen, tok
}
