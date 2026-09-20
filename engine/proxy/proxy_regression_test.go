package proxy

import (
    "net/http"
    "testing"

    "gbf-proxy/cache"
    "gbf-proxy/cert"
    "gbf-proxy/config"
    "gbf-proxy/telemetry"
    )

func TestTelemetryToggleSkipsTraceWhenDisabled(t *testing.T) {
    cfgMgr := config.NewManager("")
    cfgMgr.Update(func(c *config.Config) {
        c.EnableAPITelemetry = false
    })
    mgr := cache.NewManager(t.TempDir(), 16)
    defer mgr.Close()
    stats := telemetry.NewStats()
    defer stats.Close()

    srv := NewProxyServer(cfgMgr, &cert.Manager{}, mgr, stats)
    req, err := http.NewRequest(http.MethodGet, "https://example.com/", nil)
    if err != nil { t.Fatal(err) }
    if got := srv.tracedRequest(req); got != req {
        t.Fatal("disabled telemetry should leave request untouched")
    }

    cfgMgr.Update(func(c *config.Config) { c.EnableAPITelemetry = true })
    got := srv.tracedRequest(req)
    if got == req {
        t.Fatal("enabled telemetry should attach a trace request clone")
    }
}
