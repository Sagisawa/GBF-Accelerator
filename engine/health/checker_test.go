package health

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/proxy"
	"gbf-proxy/sysproxy"
	"gbf-proxy/telemetry"
)

func TestChecker_CoreVersionValidation(t *testing.T) {
	// 1. Verify that standard versions (2.4.1, 2.4.2, 2.5.0, etc.) are valid
	// and that 2.4.1 is NOT hardcoded as a requirement.
	validVersions := []string{"2.4.1", "2.4.2", "2.5.0", "3.0.0-rc1", "v2.4.1", "1.0.0.0", "2.4.1+build123"}
	for _, v := range validVersions {
		if !semverPattern.MatchString(v) {
			t.Errorf("expected %q to match semver pattern", v)
		}
	}

	invalidVersions := []string{"", "invalid_ver", "dev"}
	for _, v := range invalidVersions {
		if semverPattern.MatchString(v) {
			t.Errorf("expected %q to NOT match semver pattern", v)
		}
	}

	checker := NewChecker(nil, nil, nil, nil, nil, nil)
	item := checker.checkCore()

	// Default config.AppVersion should be valid
	if item.Status != StatusOk {
		t.Fatalf("expected StatusOk for core, got %s (code: %s)", item.Status, item.Code)
	}
	if item.Code != "CORE_OK" {
		t.Fatalf("expected CORE_OK, got %s", item.Code)
	}
	if !strings.Contains(item.Message, config.AppVersion) {
		t.Fatalf("message %q does not contain AppVersion %q", item.Message, config.AppVersion)
	}
	if item.Repairable {
		t.Errorf("core item should not be repairable")
	}
}

func TestChecker_ControlPlaneLoopbackInvariant(t *testing.T) {
	checker := NewChecker(nil, nil, nil, nil, nil, nil)

	// Test 1: Loopback IPv4
	item := checker.checkControlPlane("127.0.0.1:8125", 8125)
	if item.Status != StatusOk || item.Code != "CONTROL_PLANE_OK" {
		t.Fatalf("expected CONTROL_PLANE_OK, got %s (%s)", item.Code, item.Status)
	}

	// Test 2: Loopback localhost
	itemLocalhost := checker.checkControlPlane("localhost:8125", 8125)
	if itemLocalhost.Status != StatusOk || itemLocalhost.Code != "CONTROL_PLANE_OK" {
		t.Fatalf("expected CONTROL_PLANE_OK, got %s (%s)", itemLocalhost.Code, itemLocalhost.Status)
	}

	// Test 3: Loopback IPv6
	itemIPv6 := checker.checkControlPlane("[::1]:8125", 8125)
	if itemIPv6.Status != StatusOk || itemIPv6.Code != "CONTROL_PLANE_OK" {
		t.Fatalf("expected CONTROL_PLANE_OK, got %s (%s)", itemIPv6.Code, itemIPv6.Status)
	}

	// Test 4: P0 invariant: Non-loopback MUST trigger CONTROL_PLANE_NON_LOOPBACK
	itemNonLoop := checker.checkControlPlane("0.0.0.0:8125", 8125)
	if itemNonLoop.Status != StatusError || itemNonLoop.Code != "CONTROL_PLANE_NON_LOOPBACK" {
		t.Fatalf("expected CONTROL_PLANE_NON_LOOPBACK with StatusError, got %s (%s)", itemNonLoop.Code, itemNonLoop.Status)
	}
	if !itemNonLoop.Repairable || !itemNonLoop.RequiresConfirmation || itemNonLoop.Action != ActionRebindControlPlaneLoopback {
		t.Errorf("expected CONTROL_PLANE_NON_LOOPBACK to be repairable with confirmation and action %s", ActionRebindControlPlaneLoopback)
	}

	// Test 5: Strict loopback: 127.0.0.2 is not allowed per strict 127.0.0.1/[::1] invariant
	itemOtherLoop := checker.checkControlPlane("127.0.0.2:8125", 8125)
	if itemOtherLoop.Status != StatusError || itemOtherLoop.Code != "CONTROL_PLANE_NON_LOOPBACK" {
		t.Fatalf("expected CONTROL_PLANE_NON_LOOPBACK for 127.0.0.2, got %s (%s)", itemOtherLoop.Code, itemOtherLoop.Status)
	}

	// Test 6: Missing listener
	itemMissing := checker.checkControlPlane("", 8125)
	if itemMissing.Status != StatusError || itemMissing.Code != "CONTROL_PLANE_LISTENER_MISSING" {
		t.Fatalf("expected CONTROL_PLANE_LISTENER_MISSING, got %s (%s)", itemMissing.Code, itemMissing.Status)
	}

	// Test 7: Port mismatch
	itemPortMismatch := checker.checkControlPlane("127.0.0.1:8126", 8125)
	if itemPortMismatch.Status != StatusWarning || itemPortMismatch.Code != "CONTROL_PLANE_PORT_MISMATCH" {
		t.Fatalf("expected CONTROL_PLANE_PORT_MISMATCH, got %s (%s)", itemPortMismatch.Code, itemPortMismatch.Status)
	}

	// Test 8: Invalid port
	itemInvalidPort := checker.checkControlPlane("127.0.0.1:0", 8125)
	if itemInvalidPort.Status != StatusError || itemInvalidPort.Code != "CONTROL_PLANE_INVALID_ADDR" {
		t.Fatalf("expected CONTROL_PLANE_INVALID_ADDR for port 0, got %s (%s)", itemInvalidPort.Code, itemInvalidPort.Status)
	}
}

func TestChecker_DataPlaneAndProbes(t *testing.T) {
	// 1. Stopped proxy server
	checker := NewChecker(nil, nil, nil, nil, nil, nil)
	item, _ := checker.checkDataPlane(context.Background(), 8124)
	if item.Status != StatusWarning || item.Code != "DATA_PLANE_STOPPED" {
		t.Fatalf("expected DATA_PLANE_STOPPED, got %s (%s)", item.Code, item.Status)
	}
	if !item.Repairable {
		t.Errorf("DATA_PLANE_STOPPED should be repairable")
	}

	// 2. Running proxy server with successful probes
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cacheMgr := cache.NewManager(tmpDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	// Update port to 0 for ephemeral port allocation
	cfgMgr.Update(func(c *config.Config) {
		c.ListenPort = 0
	})
	proxySrv := proxy.NewProxyServer(cfgMgr, nil, cacheMgr, stats)
	if err := proxySrv.Start(); err != nil {
		t.Fatalf("failed to start proxy server: %v", err)
	}
	defer proxySrv.Stop()

	checkerRunning := NewChecker(cfgMgr, nil, cacheMgr, proxySrv, stats, nil)
	// Mock successful probes
	checkerRunning.SetProbeFunc(func(ctx context.Context, url string) (int, []byte, error) {
		if strings.HasSuffix(url, "/proxy.pac") {
			return http.StatusOK, []byte("function FindProxyForURL(url, host) { return 'PROXY 127.0.0.1:8124; DIRECT'; }"), nil
		}
		if strings.HasSuffix(url, "/ca.crt") {
			return http.StatusOK, []byte("-----BEGIN CERTIFICATE-----\nMOCK\n-----END CERTIFICATE-----"), nil
		}
		return http.StatusNotFound, nil, fmt.Errorf("not found")
	})

	_, portStr, _ := net.SplitHostPort(proxySrv.ListenerAddr())
	actualPort, _ := strconv.Atoi(portStr)

	itemRunning, pacBody := checkerRunning.checkDataPlane(context.Background(), actualPort)
	if itemRunning.Status != StatusOk || itemRunning.Code != "DATA_PLANE_OK" {
		t.Fatalf("expected DATA_PLANE_OK, got %s (%s, msg: %s)", itemRunning.Code, itemRunning.Status, itemRunning.Message)
	}
	if pacBody == "" {
		t.Errorf("expected non-empty pacBody")
	}

	// 3. Running proxy server with failing probes
	checkerFailed := NewChecker(cfgMgr, nil, cacheMgr, proxySrv, stats, nil)
	checkerFailed.SetProbeFunc(func(ctx context.Context, url string) (int, []byte, error) {
		return http.StatusServiceUnavailable, nil, errors.New("connection failed")
	})
	itemFailed, _ := checkerFailed.checkDataPlane(context.Background(), 8124)
	if itemFailed.Status != StatusWarning || itemFailed.Code != "DATA_PLANE_PROBE_FAILED" {
		t.Fatalf("expected DATA_PLANE_PROBE_FAILED, got %s (%s)", itemFailed.Code, itemFailed.Status)
	}
	if !itemFailed.Repairable {
		t.Errorf("DATA_PLANE_PROBE_FAILED should be repairable")
	}

	// 4. Port mismatch between running proxy and configured port
	checkerMismatch := NewChecker(cfgMgr, nil, cacheMgr, proxySrv, stats, nil)
	checkerMismatch.SetProbeFunc(func(ctx context.Context, url string) (int, []byte, error) {
		return http.StatusOK, []byte("ok"), nil
	})
	itemMismatch, _ := checkerMismatch.checkDataPlane(context.Background(), 9999)
	if itemMismatch.Status != StatusWarning || itemMismatch.Code != "DATA_PLANE_PORT_MISMATCH" {
		t.Fatalf("expected DATA_PLANE_PORT_MISMATCH, got %s (%s)", itemMismatch.Code, itemMismatch.Status)
	}
}

func TestChecker_PACVerification(t *testing.T) {
	checker := NewChecker(nil, nil, nil, nil, nil, nil)

	// 1. Matching PAC content
	pacOk := checker.checkPAC(8124, "PROXY 127.0.0.1:8124; DIRECT")
	if pacOk.Status != StatusOk || pacOk.Code != "PAC_OK" {
		t.Fatalf("expected PAC_OK, got %s (%s)", pacOk.Code, pacOk.Status)
	}
	if pacOk.Repairable {
		t.Errorf("healthy PAC should not be repairable")
	}

	// 2. Mismatched PAC content
	pacMismatch := checker.checkPAC(8124, "PROXY 127.0.0.1:8099; DIRECT")
	if pacMismatch.Status != StatusWarning || pacMismatch.Code != "PAC_PORT_MISMATCH" {
		t.Fatalf("expected PAC_PORT_MISMATCH, got %s (%s)", pacMismatch.Code, pacMismatch.Status)
	}
	if !pacMismatch.Repairable {
		t.Errorf("PAC_PORT_MISMATCH should be repairable")
	}

	// 3. Port boundary check: port 80 must NOT match 8080
	pacBoundary80 := checker.checkPAC(80, "function FindProxyForURL(url, host) { return 'PROXY 127.0.0.1:8080; DIRECT'; }")
	if pacBoundary80.Status != StatusWarning || pacBoundary80.Code != "PAC_PORT_MISMATCH" {
		t.Fatalf("expected PAC_PORT_MISMATCH when comparing port 80 to 8080, got %s (%s)", pacBoundary80.Code, pacBoundary80.Status)
	}

	// 4. Port boundary check: port 8124 must NOT match 18124
	pacBoundary18124 := checker.checkPAC(8124, "function FindProxyForURL(url, host) { return 'PROXY 127.0.0.1:18124; DIRECT'; }")
	if pacBoundary18124.Status != StatusWarning || pacBoundary18124.Code != "PAC_PORT_MISMATCH" {
		t.Fatalf("expected PAC_PORT_MISMATCH when comparing port 8124 to 18124, got %s (%s)", pacBoundary18124.Code, pacBoundary18124.Status)
	}

	// 5. Invalid port (0 or negative)
	pacInvalidPort := checker.checkPAC(0, "")
	if pacInvalidPort.Status != StatusError || pacInvalidPort.Code != "PAC_INVALID_PORT" {
		t.Fatalf("expected PAC_INVALID_PORT for port 0, got %s (%s)", pacInvalidPort.Code, pacInvalidPort.Status)
	}
	if pacInvalidPort.Repairable {
		t.Errorf("PAC_INVALID_PORT should NOT be repairable")
	}

	// 6. Empty probe body falls back to generated PAC
	pacExplicitEmpty := checker.checkPAC(8124, "")
	if pacExplicitEmpty.Status != StatusOk {
		t.Fatalf("expected fallback generation to be valid for empty probe body")
	}
}

func TestChecker_CacheProbeVerification(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gbf_health_cache_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	checker := NewChecker(nil, nil, nil, nil, nil, nil)

	// 1. Normal cache directory
	item := checker.checkCache(tmpDir)
	if item.Status != StatusOk || item.Code != "CACHE_OK" {
		t.Fatalf("expected CACHE_OK, got %s (%s)", item.Code, item.Status)
	}

	// Ensure probe file was cleaned up (reversible!)
	files, _ := os.ReadDir(tmpDir)
	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".health_probe_") {
			t.Errorf("temporary probe file %s was not deleted", f.Name())
		}
	}

	// 2. Missing cache directory -> CACHE_DIR_MISSING, repairable = true
	missingDir := filepath.Join(tmpDir, "non_existent_subdir")
	itemMissing := checker.checkCache(missingDir)
	if itemMissing.Status != StatusWarning || itemMissing.Code != "CACHE_DIR_MISSING" {
		t.Fatalf("expected CACHE_DIR_MISSING, got %s (%s)", itemMissing.Code, itemMissing.Status)
	}
	if !itemMissing.Repairable {
		t.Errorf("CACHE_DIR_MISSING must be repairable")
	}

	// 3. Unconfigured / empty cache dir -> CACHE_DIR_NOT_CONFIGURED, repairable = false
	itemUnconfigured := checker.checkCache("")
	if itemUnconfigured.Status != StatusWarning || itemUnconfigured.Code != "CACHE_DIR_NOT_CONFIGURED" {
		t.Fatalf("expected CACHE_DIR_NOT_CONFIGURED, got %s (%s)", itemUnconfigured.Code, itemUnconfigured.Status)
	}
	if itemUnconfigured.Repairable {
		t.Errorf("CACHE_DIR_NOT_CONFIGURED must NOT be repairable")
	}

	// 4. Cache path is a file, not a directory -> CACHE_PATH_NOT_DIR, repairable = false
	filePath := filepath.Join(tmpDir, "some_file.txt")
	_ = os.WriteFile(filePath, []byte("data"), 0644)
	itemFile := checker.checkCache(filePath)
	if itemFile.Status != StatusError || itemFile.Code != "CACHE_PATH_NOT_DIR" {
		t.Fatalf("expected CACHE_PATH_NOT_DIR, got %s (%s)", itemFile.Code, itemFile.Status)
	}
	if itemFile.Repairable {
		t.Errorf("CACHE_PATH_NOT_DIR must NOT be repairable")
	}
}

func TestChecker_CacheProbeCorruptGuardrail(t *testing.T) {
	// Guardrail 4:
	// 不要对 CACHE_PROBE_CORRUPT 做过度诊断
	// 如果缓存目录的临时探针出现：写入 → 读取 → 校验 不一致，请只报告：
	// `缓存读写校验失败`
	// 例如：code: CACHE_PROBE_CORRUPT, message: 缓存目录临时探针读写校验失败
	// 不要直接把原因描述成：“物理磁盘损坏”。
	tmpDir := t.TempDir()
	checker := NewChecker(nil, nil, nil, nil, nil, nil)

	// Simulate corrupted probe readback
	checker.SetReadProbeFunc(func(path string) ([]byte, error) {
		return []byte("CORRUPTED_PROBE_CONTENT"), nil
	})

	item := checker.checkCache(tmpDir)

	if item.Status != StatusError {
		t.Fatalf("expected StatusError for corrupt probe, got %s", item.Status)
	}
	if item.Code != "CACHE_PROBE_CORRUPT" {
		t.Fatalf("expected code CACHE_PROBE_CORRUPT, got %s", item.Code)
	}
	if item.Message != "缓存目录临时探针读写校验失败" {
		t.Fatalf("expected message '缓存目录临时探针读写校验失败', got %q", item.Message)
	}
	if strings.Contains(item.Message, "物理磁盘损坏") {
		t.Fatalf("violation of Guardrail 4: message must NOT state 物理磁盘损坏")
	}
	if item.Repairable {
		t.Errorf("CACHE_PROBE_CORRUPT must not be auto-repairable")
	}
}

func TestChecker_ConfigValidation(t *testing.T) {
	checker := NewChecker(nil, nil, nil, nil, nil, nil)

	// 1. Normal configuration (AutoSystemProxy is false by default)
	cfg := config.DefaultConfig()
	cfg.AutoSystemProxy = false
	item := checker.checkConfig(cfg)
	if item.Status != StatusOk || item.Code != "CONFIG_OK" {
		t.Fatalf("expected CONFIG_OK when AutoSystemProxy=false, got %s (%s)", item.Code, item.Status)
	}

	// 2. Port conflict (ListenPort == ControlPort) with AutoSystemProxy=false
	cfgConflict := cfg
	cfgConflict.ControlPort = cfgConflict.ListenPort
	itemConflict := checker.checkConfig(cfgConflict)
	if itemConflict.Status != StatusError || itemConflict.Code != "PORT_CONFIG_COLLISION" {
		t.Fatalf("expected PORT_CONFIG_COLLISION, got %s (%s)", itemConflict.Code, itemConflict.Status)
	}
	// Per requirement 3: Port conflict must NOT automatically revert, repairable must be FALSE
	if itemConflict.Repairable {
		t.Errorf("PORT_CONFIG_COLLISION must NOT be auto-repairable")
	}

	// 3. Invalid port with AutoSystemProxy=false
	cfgInvalid := cfg
	cfgInvalid.ListenPort = -1
	itemInvalid := checker.checkConfig(cfgInvalid)
	if itemInvalid.Status != StatusError || itemInvalid.Code != "PORT_INVALID" {
		t.Fatalf("expected PORT_INVALID, got %s (%s)", itemInvalid.Code, itemInvalid.Status)
	}

	// 4. Invalid control port with AutoSystemProxy=false
	cfgInvalidCtrl := cfg
	cfgInvalidCtrl.ControlPort = 70000
	itemInvalidCtrl := checker.checkConfig(cfgInvalidCtrl)
	if itemInvalidCtrl.Status != StatusError || itemInvalidCtrl.Code != "PORT_INVALID" {
		t.Fatalf("expected PORT_INVALID for control port 70000, got %s (%s)", itemInvalidCtrl.Code, itemInvalidCtrl.Status)
	}

	// 5. When AutoSystemProxy=true, verify conflict detection matches underlying system state
	cfgAutoPac := cfg
	cfgAutoPac.AutoSystemProxy = true
	itemAutoPac := checker.checkConfig(cfgAutoPac)
	envConflict := sysproxy.CheckProxyConflict(cfgAutoPac.ListenPort)
	if envConflict != "" {
		if itemAutoPac.Status != StatusWarning || itemAutoPac.Code != "SYSPROXY_CONFLICT" {
			t.Fatalf("expected SYSPROXY_CONFLICT when AutoSystemProxy=true and system has conflict, got %s (%s)", itemAutoPac.Code, itemAutoPac.Status)
		}
	} else {
		if itemAutoPac.Status != StatusOk || itemAutoPac.Code != "CONFIG_OK" {
			t.Fatalf("expected CONFIG_OK when AutoSystemProxy=true and no conflict, got %s (%s)", itemAutoPac.Code, itemAutoPac.Status)
		}
	}
}

func TestChecker_LANDisabledCondition(t *testing.T) {
	checker := NewChecker(nil, nil, nil, nil, nil, nil)

	// When AllowLAN == false, LAN IP and firewall checks MUST be completely skipped,
	// returning LAN_DISABLED with StatusOk and no warnings.
	cfg := config.DefaultConfig()
	cfg.AllowLAN = false

	item := checker.checkLANFirewall(cfg)
	if item.Status != StatusOk {
		t.Fatalf("expected StatusOk when LAN is disabled, got %s", item.Status)
	}
	if item.Code != "LAN_DISABLED" {
		t.Fatalf("expected LAN_DISABLED, got %s", item.Code)
	}
	if item.Repairable {
		t.Errorf("LAN_DISABLED must not be repairable")
	}
	if item.Details["allow_lan"] != false {
		t.Errorf("expected allow_lan false in details")
	}
}

func TestChecker_SummaryAndRuntimeStatus(t *testing.T) {
	cfgMgr := config.NewManager("test_config.json")
	stats := telemetry.NewStats()
	stats.TotalAPIs.Store(50)
	stats.TotalAssets.Store(200)
	stats.TotalHits.Store(180)
	stats.RAMHits.Store(100)
	stats.DiskHits.Store(80)
	stats.CacheMisses.Store(20)
	stats.APIRetries.Store(2)

	stats.Log("INFO", "normal log")
	stats.Log("ERROR", "test error log entry")

	checker := NewChecker(cfgMgr, nil, nil, nil, stats, func() string {
		return "127.0.0.1:8125"
	})

	checker.SetProbeFunc(func(ctx context.Context, url string) (int, []byte, error) {
		return http.StatusOK, []byte("PROXY 127.0.0.1:8124"), nil
	})

	resp := checker.Check(context.Background())

	if resp.Summary.TotalChecks != 8 {
		t.Fatalf("expected 8 total checks, got %d", resp.Summary.TotalChecks)
	}

	// Runtime status verification
	if resp.RuntimeStatus.APIRetries != 2 {
		t.Errorf("expected 2 API retries, got %d", resp.RuntimeStatus.APIRetries)
	}
	if len(resp.RuntimeStatus.RecentErrors) != 1 {
		t.Errorf("expected 1 recent error, got %d", len(resp.RuntimeStatus.RecentErrors))
	} else if !strings.Contains(resp.RuntimeStatus.RecentErrors[0].Msg, "test error log entry") {
		t.Errorf("unexpected error msg: %s", resp.RuntimeStatus.RecentErrors[0].Msg)
	}

	// Verify JSON serialization: recent_errors must NOT be null even when empty
	emptyStats := telemetry.NewStats()
	emptyChecker := NewChecker(cfgMgr, nil, nil, nil, emptyStats, func() string {
		return "127.0.0.1:8125"
	})
	emptyResp := emptyChecker.Check(context.Background())
	jsonBytes, err := json.Marshal(emptyResp)
	if err != nil {
		t.Fatalf("failed to marshal emptyResp: %v", err)
	}
	var rawMap map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &rawMap); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	rtStatus := rawMap["runtime_status"].(map[string]interface{})
	if rtStatus["recent_errors"] == nil {
		t.Errorf("recent_errors must serialize to [] not null")
	}
}

func createTestCert(notBefore, notAfter time.Time) ([]byte, []byte, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "Test CA",
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	return certPEM, keyPEM, nil
}

func TestChecker_RootCAValidation(t *testing.T) {
	// 1. Nil cert manager -> CA_NOT_CONFIGURED
	checkerNil := NewChecker(nil, nil, nil, nil, nil, nil)
	itemNil := checkerNil.checkRootCA()
	if itemNil.Status != StatusWarning || itemNil.Code != "CA_NOT_CONFIGURED" {
		t.Fatalf("expected CA_NOT_CONFIGURED, got %s (%s)", itemNil.Code, itemNil.Status)
	}
	if itemNil.Repairable {
		t.Errorf("CA_NOT_CONFIGURED should not be repairable")
	}

	// 2. Real cert manager with freshly generated CA -> CA_NOT_INSTALLED
	tmpDir := t.TempDir()
	certMgr, err := cert.NewManager(tmpDir)
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	checkerFresh := NewChecker(nil, certMgr, nil, nil, nil, nil)
	itemFresh := checkerFresh.checkRootCA()
	if itemFresh.Status != StatusWarning || itemFresh.Code != "CA_NOT_INSTALLED" {
		t.Fatalf("expected CA_NOT_INSTALLED, got %s (%s)", itemFresh.Code, itemFresh.Status)
	}
	if !itemFresh.Repairable || !itemFresh.RequiresConfirmation || !itemFresh.RequiresElevation || itemFresh.Action != ActionInstallRootCA {
		t.Errorf("CA_NOT_INSTALLED must be repairable with confirmation, elevation, and action %s", ActionInstallRootCA)
	}

	// 3. Expired CA -> CA_EXPIRED
	tmpDirExpired := t.TempDir()
	expiredCertPEM, expiredKeyPEM, err := createTestCert(time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("failed to create expired cert: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDirExpired, "ca.crt"), expiredCertPEM, 0644); err != nil {
		t.Fatalf("failed to write ca.crt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDirExpired, "ca.key"), expiredKeyPEM, 0600); err != nil {
		t.Fatalf("failed to write ca.key: %v", err)
	}
	certMgrExpired, err := cert.NewManager(tmpDirExpired)
	if err != nil {
		t.Fatalf("failed to load expired cert manager: %v", err)
	}
	checkerExpired := NewChecker(nil, certMgrExpired, nil, nil, nil, nil)
	itemExpired := checkerExpired.checkRootCA()
	if itemExpired.Status != StatusError || itemExpired.Code != "CA_EXPIRED" {
		t.Fatalf("expected CA_EXPIRED with StatusError, got %s (%s)", itemExpired.Code, itemExpired.Status)
	}
	if !itemExpired.Repairable || !itemExpired.RequiresConfirmation || !itemExpired.RequiresElevation || itemExpired.Action != ActionRegenerateAndInstallRootCA {
		t.Errorf("CA_EXPIRED must be repairable with confirmation, elevation, and action %s", ActionRegenerateAndInstallRootCA)
	}
}
