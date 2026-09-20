package config

import (
    "os"
    "path/filepath"
    "testing"
    )

func TestRAMCacheMaxMBIsClampedToDocumentedRange(t *testing.T) {
    cases := []struct { value, want int }{
        {0, 16}, {8, 16}, {256, 256}, {8192, 8192}, {99999, 8192},
    }
    for i, tc := range cases {
        t.Run(string(rune('a'+i)), func(t *testing.T) {
            path := filepath.Join(t.TempDir(), "config.json")
            payload := `{"ram_cache_max_mb":` + itoa(tc.value) + `}`
            if err := os.WriteFile(path, []byte(payload), 0644); err != nil {
                t.Fatal(err)
            }
            got := NewManager(path).Get().RAMCacheMaxMB
            if got != tc.want {
                t.Fatalf("RAMCacheMaxMB(%d)=%d, want %d", tc.value, got, tc.want)
            }
        })
    }
}

func TestUpdateWithErrorRollsBackOnSaveFailure(t *testing.T) {
    dir := t.TempDir()
    mgr := NewManager(dir)
    before := mgr.Get()

    after, err := mgr.UpdateWithError(func(c *Config) {
        c.RAMCacheMaxMB = 4096
    })
    if err == nil {
        t.Fatal("expected Save error when config path is a directory")
    }
    if after.RAMCacheMaxMB != before.RAMCacheMaxMB {
        t.Fatalf("UpdateWithError returned uncommitted config: got %d want %d", after.RAMCacheMaxMB, before.RAMCacheMaxMB)
    }
    if got := mgr.Get().RAMCacheMaxMB; got != before.RAMCacheMaxMB {
        t.Fatalf("in-memory config was not rolled back: got %d want %d", got, before.RAMCacheMaxMB)
    }
}

func itoa(v int) string {
    if v == 0 { return "0" }
    buf := make([]byte, 0, 8)
    for v > 0 {
        buf = append([]byte{byte('0' + v%10)}, buf...)
        v /= 10
    }
    return string(buf)
}
