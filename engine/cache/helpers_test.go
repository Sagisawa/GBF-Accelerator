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
	_ = os.MkdirAll(filepath.Dir(p), 0755)
	if err := os.WriteFile(p, data, 0644); err != nil {
		t.Fatalf("writeTestDiskFile write failed: %v", err)
	}
	return p
}

func populateMemoryStateForTest(mgr *Manager, ns, key string, body []byte) (string, uint64, opToken) {
	_, _, _, gen, tok, ok := mgr.saveRAMInternal(ns, key, testJSHeaders, body, false, true)
	if !ok {
		panic("saveRAMInternal failed")
	}
	ramKey := makeRAMKey(ns, key)
	if mgr.residentPool.Generation() == 0 {
		mgr.residentPool.NextGeneration()
	}
	mgr.residentPool.ResetForGen(mgr.residentPool.Generation())
	mgr.residentPool.Set(ramKey, &CacheItem{Key: ramKey, Data: body, ContentType: "application/javascript", Size: int64(len(body))})
	mgr.setDiskMeta(ramKey, diskMetaEntry{size: int64(len(body)), namespace: ns, exists: true, hasMeta: true, verified: true})
	return ramKey, gen, tok
}

func writeTestAsset(t *testing.T, baseDir, rel string, data, extContent []byte) string {
	t.Helper()
	p := writeTestDiskFile(t, baseDir, rel, data)
	if extContent != nil {
		_ = os.WriteFile(p+".ext", extContent, 0644)
	}
	return p
}

func writeTestInvalidMarker(t *testing.T, dir, kind string) {
	t.Helper()
	p := filepath.Join(dir, RootMarkerFileName)
	_ = os.Remove(p)
	switch kind {
	case "corrupt": _ = os.WriteFile(p, []byte("Wrong\n"), 0644)
	case "empty":   _ = os.WriteFile(p, nil, 0644)
	case "dir":     _ = os.Mkdir(p, 0755)
	case "symlink":
		ext := filepath.Join(t.TempDir(), "target.txt")
		_ = os.WriteFile(ext, []byte(RootMarkerExpectedContent), 0644)
		if err := os.Symlink(ext, p); err != nil {
			t.Skipf("skipping symlink marker: %v", err)
		}
	}
}

func assertPathStatus(t *testing.T, baseDir string, rels []string, wantExist bool) {
	t.Helper()
	for _, rel := range rels {
		p := filepath.Join(baseDir, filepath.FromSlash(rel))
		_, err := os.Stat(p)
		if (wantExist && err != nil) || (!wantExist && !os.IsNotExist(err)) {
			t.Errorf("assertPathStatus %s (wantExist=%v): err=%v", rel, wantExist, err)
		}
	}
}
