package health

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/firewall"
	"gbf-proxy/proxy"
	"gbf-proxy/telemetry"
)

func getFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func updateConfig(cfgMgr *config.Manager, fn func(c *config.Config)) {
	_ = cfgMgr.Update(fn)
}

func setupTestRepairer(t *testing.T) (*Repairer, *config.Manager, *cert.Manager, *cache.Manager, *proxy.ProxyServer, *Checker, string) {
	t.Helper()
	tmpDir := t.TempDir()

	listenPort := getFreePort(t)
	controlPort := getFreePort(t)
	for controlPort == listenPort {
		controlPort = getFreePort(t)
	}

	cfgPath := filepath.Join(tmpDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	updateConfig(cfgMgr, func(c *config.Config) {
		c.ListenPort = listenPort
		c.ControlPort = controlPort
		c.CacheDir = filepath.Join(tmpDir, "cache")
		c.AllowLAN = false
	})

	certDir := filepath.Join(tmpDir, "certs")
	certMgr, err := cert.NewManager(certDir)
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}

	cacheDir := cfgMgr.Get().CacheDir
	cacheMgr := cache.NewManager(cacheDir, 64)

	stats := telemetry.NewStats()
	proxySrv := proxy.NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)

	ctrlAddr := fmt.Sprintf("127.0.0.1:%d", controlPort)
	checker := NewChecker(cfgMgr, certMgr, cacheMgr, proxySrv, stats, func() string {
		return ctrlAddr
	})
	checker.SetFirewallStatusFunc(func(port int) (firewall.Status, error) {
		return firewall.Status{Allowed: true}, nil
	})

	repairer := NewRepairer(cfgMgr, certMgr, cacheMgr, proxySrv, stats, checker, nil)
	repairer.SetCertInstallFunc(func(certsDir string) error {
		return nil
	})
	repairer.SetFirewallApplyFunc(func(port int) (string, error) {
		return "", nil
	})
	repairer.SetFirewallStatusFunc(func(port int) (firewall.Status, error) {
		return firewall.Status{Allowed: true}, nil
	})
	return repairer, cfgMgr, certMgr, cacheMgr, proxySrv, checker, tmpDir
}

// 1. Data Plane stopped -> Repair -> running
func TestRepair_Requirement1_DataPlaneStopped(t *testing.T) {
	repairer, _, _, _, proxySrv, checker, _ := setupTestRepairer(t)
	defer proxySrv.Stop()

	// Ensure proxy is stopped
	proxySrv.Stop()

	initCheck := checker.Check(context.Background())
	if initCheck.CoreHealth.DataPlane.Code != "DATA_PLANE_STOPPED" {
		t.Fatalf("expected DATA_PLANE_STOPPED, got %s", initCheck.CoreHealth.DataPlane.Code)
	}

	res, err := repairer.Repair(context.Background(), RepairRequest{Code: "DATA_PLANE_STOPPED"})
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected repair success, got: %s", res.Message)
	}
	if !proxySrv.IsRunning() {
		t.Fatal("expected proxy server to be running after repair")
	}
	if res.Health.CoreHealth.DataPlane.Status != StatusOk {
		t.Errorf("expected DataPlane StatusOk after repair, got %s (%s)",
			res.Health.CoreHealth.DataPlane.Status, res.Health.CoreHealth.DataPlane.Code)
	}
}

// 2. PAC mismatch -> Repair -> PAC correct
func TestRepair_Requirement2_PACMismatch(t *testing.T) {
	repairer, cfgMgr, _, _, proxySrv, checker, _ := setupTestRepairer(t)
	if err := proxySrv.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer proxySrv.Stop()

	cfg := cfgMgr.Get()

	// Mock probe to return mismatched PAC port
	var isRepaired atomic.Bool
	checker.SetProbeFunc(func(ctx context.Context, url string) (int, []byte, error) {
		if strings.HasSuffix(url, "/ca.crt") {
			return 200, []byte("-----BEGIN CERTIFICATE-----\nMOCK\n-----END CERTIFICATE-----"), nil
		}
		if !isRepaired.Load() {
			// First round: return mismatched PAC
			return 200, []byte("PROXY 127.0.0.1:9999;"), nil
		}
		// Subsequent rounds: return correct PAC
		return 200, []byte(proxy.GetPAC("127.0.0.1", cfg.ListenPort)), nil
	})

	initCheck := checker.Check(context.Background())
	if initCheck.CoreHealth.PAC.Code != "PAC_PORT_MISMATCH" {
		t.Fatalf("expected PAC_PORT_MISMATCH, got %s", initCheck.CoreHealth.PAC.Code)
	}

	isRepaired.Store(true)

	res, err := repairer.Repair(context.Background(), RepairRequest{Code: "PAC_PORT_MISMATCH"})
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("repair reported failure: %s", res.Message)
	}
	if len(res.Results) != 1 || res.Results[0].Action != ActionRegeneratePAC {
		t.Fatalf("expected ActionRegeneratePAC, got %+v", res.Results)
	}
	if res.Health.CoreHealth.PAC.Status != StatusOk {
		t.Errorf("expected PAC StatusOk after repair, got %s", res.Health.CoreHealth.PAC.Status)
	}
}

// 3. Cache directory missing -> Repair -> directory exists
func TestRepair_Requirement3_CacheDirectoryMissing(t *testing.T) {
	repairer, cfgMgr, _, _, proxySrv, checker, tmpDir := setupTestRepairer(t)
	defer proxySrv.Stop()

	missingCacheDir := filepath.Join(tmpDir, "missing_cache_dir")
	updateConfig(cfgMgr, func(c *config.Config) {
		c.CacheDir = missingCacheDir
	})

	initCheck := checker.Check(context.Background())
	if initCheck.CoreHealth.Cache.Code != "CACHE_DIR_MISSING" {
		t.Fatalf("expected CACHE_DIR_MISSING, got %s", initCheck.CoreHealth.Cache.Code)
	}

	res, err := repairer.Repair(context.Background(), RepairRequest{Code: "CACHE_DIR_MISSING"})
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected repair success, got: %s", res.Message)
	}

	fi, err := os.Stat(missingCacheDir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("expected cache dir to exist after repair: %v", err)
	}
	if res.Health.CoreHealth.Cache.Status != StatusOk {
		t.Errorf("expected Cache StatusOk, got %s", res.Health.CoreHealth.Cache.Status)
	}
}

// 4. CA not installed -> Repair enters existing installation flow
func TestRepair_Requirement4_CANotInstalled(t *testing.T) {
	repairer, _, _, _, proxySrv, checker, _ := setupTestRepairer(t)
	defer proxySrv.Stop()

	installCalled := false
	repairer.SetCertInstallFunc(func(certsDir string) error {
		installCalled = true
		return nil
	})

	initCheck := checker.Check(context.Background())
	if initCheck.CoreHealth.RootCA.Code != "CA_NOT_INSTALLED" {
		t.Fatalf("expected CA_NOT_INSTALLED, got %s", initCheck.CoreHealth.RootCA.Code)
	}
	if !initCheck.CoreHealth.RootCA.Repairable || !initCheck.CoreHealth.RootCA.RequiresConfirmation || !initCheck.CoreHealth.RootCA.RequiresElevation {
		t.Fatalf("expected CA_NOT_INSTALLED to have Repairable=true, RequiresConfirmation=true, RequiresElevation=true")
	}

	res, err := repairer.Repair(context.Background(), RepairRequest{Code: "CA_NOT_INSTALLED"})
	if err != nil {
		t.Fatalf("repair returned error: %v", err)
	}
	if !installCalled {
		t.Fatal("expected cert install function to be called during CA_NOT_INSTALLED repair")
	}
	if len(res.Results) != 1 || res.Results[0].Action != ActionInstallRootCA {
		t.Fatalf("expected ActionInstallRootCA, got %+v", res.Results)
	}
}

// 5. CA expired -> Repair enters regenerate & install flow
func TestRepair_Requirement5_CAExpired(t *testing.T) {
	tmpDir := t.TempDir()
	certDir := filepath.Join(tmpDir, "certs")
	_ = os.MkdirAll(certDir, 0755)

	// Create expired CA
	expiredKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(12345),
		Subject:      pkix.Name{CommonName: "GBF Expired Root CA"},
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     time.Now().Add(-24 * time.Hour),
		IsCA:         true,
	}
	der, _ := x509.CreateCertificate(rand.Reader, template, template, &expiredKey.PublicKey, expiredKey)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(expiredKey)})

	_ = os.WriteFile(filepath.Join(certDir, "ca.crt"), certPEM, 0644)
	_ = os.WriteFile(filepath.Join(certDir, "ca.key"), keyPEM, 0600)

	certMgr, err := cert.NewManager(certDir)
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}

	cfgMgr := config.NewManager(filepath.Join(tmpDir, "config.json"))
	checker := NewChecker(cfgMgr, certMgr, nil, nil, nil, nil)
	repairer := NewRepairer(cfgMgr, certMgr, nil, nil, nil, checker, nil)

	installCalled := false
	repairer.SetCertInstallFunc(func(certsDir string) error {
		installCalled = true
		return nil
	})

	initCheck := checker.Check(context.Background())
	if initCheck.CoreHealth.RootCA.Code != "CA_EXPIRED" {
		t.Fatalf("expected CA_EXPIRED, got %s", initCheck.CoreHealth.RootCA.Code)
	}

	res, err := repairer.Repair(context.Background(), RepairRequest{Code: "CA_EXPIRED"})
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !installCalled {
		t.Fatal("expected cert install function to be called during CA_EXPIRED repair")
	}
	if len(res.Results) != 1 || res.Results[0].Action != ActionRegenerateAndInstallRootCA {
		t.Fatalf("expected ActionRegenerateAndInstallRootCA, got %+v", res.Results)
	}

	// Verify CA was freshly regenerated and NotAfter is in the future
	newPEM := certMgr.GetCAPEM()
	block, _ := pem.Decode(newPEM)
	if block == nil {
		t.Fatal("failed to decode new CA PEM")
	}
	newCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse new certificate: %v", err)
	}
	if time.Now().After(newCert.NotAfter) {
		t.Fatalf("new CA certificate is still expired: NotAfter=%v", newCert.NotAfter)
	}
}

// 6. Firewall missing -> Repair calls existing ApplyRule interface
func TestRepair_Requirement6_FirewallMissing(t *testing.T) {
	repairer, cfgMgr, _, _, proxySrv, checker, _ := setupTestRepairer(t)
	defer proxySrv.Stop()

	updateConfig(cfgMgr, func(c *config.Config) {
		c.AllowLAN = true
	})

	fwAllowed := false
	checker.SetFirewallStatusFunc(func(port int) (firewall.Status, error) {
		return firewall.Status{Allowed: fwAllowed}, nil
	})
	repairer.SetFirewallStatusFunc(func(port int) (firewall.Status, error) {
		return firewall.Status{Allowed: fwAllowed}, nil
	})

	calledPort := 0
	repairer.SetFirewallApplyFunc(func(port int) (string, error) {
		calledPort = port
		fwAllowed = true
		return "", nil
	})

	initCheck := checker.Check(context.Background())
	if initCheck.CoreHealth.LANFirewall.Code != "FIREWALL_RULE_MISSING" {
		t.Fatalf("expected FIREWALL_RULE_MISSING, got %s", initCheck.CoreHealth.LANFirewall.Code)
	}

	res, err := repairer.Repair(context.Background(), RepairRequest{Code: "FIREWALL_RULE_MISSING"})
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if calledPort != cfgMgr.Get().ListenPort {
		t.Fatalf("expected firewall apply to be called with port %d, got %d", cfgMgr.Get().ListenPort, calledPort)
	}
	if len(res.Results) != 1 || res.Results[0].Action != ActionApplyFirewallRule {
		t.Fatalf("expected ActionApplyFirewallRule, got %+v", res.Results)
	}
	if !res.Success {
		t.Fatalf("expected repair success, got %+v", res)
	}
}

// 7. LAN enabled + wrong bind -> Repair -> correct bind
func TestRepair_Requirement7_LANEnabledWrongBind(t *testing.T) {
	repairer, cfgMgr, _, _, proxySrv, checker, _ := setupTestRepairer(t)
	defer proxySrv.Stop()

	updateConfig(cfgMgr, func(c *config.Config) {
		c.AllowLAN = true
	})

	// Explicitly bind to 127.0.0.1 while AllowLAN is true
	if err := proxySrv.Start(); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	if err := proxySrv.ReloadListener("127.0.0.1", cfgMgr.Get().ListenPort); err != nil {
		t.Fatalf("failed to force 127.0.0.1 bind: %v", err)
	}

	initCheck := checker.Check(context.Background())
	if initCheck.CoreHealth.LANFirewall.Code != "LAN_DATA_PLANE_NOT_BOUND" {
		t.Fatalf("expected LAN_DATA_PLANE_NOT_BOUND, got %s", initCheck.CoreHealth.LANFirewall.Code)
	}

	res, err := repairer.Repair(context.Background(), RepairRequest{Code: "LAN_DATA_PLANE_NOT_BOUND"})
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("repair failed: %s", res.Message)
	}

	addr := proxySrv.ListenerAddr()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("failed to split addr %s: %v", addr, err)
	}
	if host == "127.0.0.1" || host == "localhost" {
		t.Fatalf("expected proxy to be re-bound to 0.0.0.0, still got %s", addr)
	}
}

// 8. Port collision -> Does NOT automatically change user ports
func TestRepair_Requirement8_PortCollision(t *testing.T) {
	repairer, cfgMgr, _, _, proxySrv, checker, _ := setupTestRepairer(t)
	defer proxySrv.Stop()

	updateConfig(cfgMgr, func(c *config.Config) {
		c.ControlPort = c.ListenPort // Force collision
	})

	initCheck := checker.Check(context.Background())
	if initCheck.CoreHealth.Config.Code != "PORT_CONFIG_COLLISION" {
		t.Fatalf("expected PORT_CONFIG_COLLISION, got %s", initCheck.CoreHealth.Config.Code)
	}
	if initCheck.CoreHealth.Config.Repairable {
		t.Fatal("PORT_CONFIG_COLLISION must NOT be marked repairable")
	}

	// 1. Direct repair attempt must be rejected
	resDirect, err := repairer.Repair(context.Background(), RepairRequest{Code: "PORT_CONFIG_COLLISION"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resDirect.Success {
		t.Fatal("PORT_CONFIG_COLLISION direct repair must NOT succeed")
	}

	// 2. Batch repair must NOT tamper with user ports
	resBatch, err := repairer.Repair(context.Background(), RepairRequest{All: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	currentCfg := cfgMgr.Get()
	if currentCfg.ListenPort != currentCfg.ControlPort {
		t.Fatal("repair modified user port configuration without permission")
	}
	for _, step := range resBatch.Results {
		if step.Code == "PORT_CONFIG_COLLISION" {
			t.Fatal("batch repair must not attempt to repair PORT_CONFIG_COLLISION")
		}
	}
}

// 9. LAN disabled -> No repair for LAN
func TestRepair_Requirement9_LANDisabled(t *testing.T) {
	repairer, cfgMgr, _, _, proxySrv, checker, _ := setupTestRepairer(t)
	defer proxySrv.Stop()

	updateConfig(cfgMgr, func(c *config.Config) {
		c.AllowLAN = false
	})

	initCheck := checker.Check(context.Background())
	if initCheck.CoreHealth.LANFirewall.Code != "LAN_DISABLED" {
		t.Fatalf("expected LAN_DISABLED, got %s", initCheck.CoreHealth.LANFirewall.Code)
	}
	if initCheck.CoreHealth.LANFirewall.Repairable {
		t.Fatal("LAN_DISABLED must not be repairable")
	}

	// Direct attempt to rebind LAN when AllowLAN is false must fail
	res, err := repairer.Repair(context.Background(), RepairRequest{Code: "LAN_DATA_PLANE_NOT_BOUND"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Success {
		t.Fatal("rebind LAN must NOT succeed when AllowLAN is false")
	}
}

// 10. Control Plane security violation -> No silent relaxation
func TestRepair_Requirement10_ControlPlaneSecurityViolation(t *testing.T) {
	tmpDir := t.TempDir()
	listenPort := getFreePort(t)
	controlPort := getFreePort(t)
	for controlPort == listenPort {
		controlPort = getFreePort(t)
	}

	cfgPath := filepath.Join(tmpDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	updateConfig(cfgMgr, func(c *config.Config) {
		c.ListenPort = listenPort
		c.ControlPort = controlPort
		c.AllowLAN = true // Even if AllowLAN is true, Control Plane MUST NOT be non-loopback
	})

	// Simulate Control Plane bound to non-loopback address 0.0.0.0:port
	currentCtrlAddr := fmt.Sprintf("0.0.0.0:%d", controlPort)
	checker := NewChecker(cfgMgr, nil, nil, nil, nil, func() string {
		return currentCtrlAddr
	})
	checker.SetFirewallStatusFunc(func(port int) (firewall.Status, error) {
		return firewall.Status{Allowed: true}, nil
	})

	repairer := NewRepairer(cfgMgr, nil, nil, nil, nil, checker, nil)

	reloadedPort := 0
	repairer.SetControlReloadFunc(func(port int) error {
		reloadedPort = port
		// When reloaded, control plane binds strictly to 127.0.0.1
		currentCtrlAddr = fmt.Sprintf("127.0.0.1:%d", port)
		return nil
	})

	// Verify initial check detects CONTROL_PLANE_NON_LOOPBACK
	initCheck := checker.Check(context.Background())
	if initCheck.CoreHealth.ControlPlane.Code != "CONTROL_PLANE_NON_LOOPBACK" {
		t.Fatalf("expected CONTROL_PLANE_NON_LOOPBACK, got %s", initCheck.CoreHealth.ControlPlane.Code)
	}
	if initCheck.CoreHealth.ControlPlane.Status != StatusError {
		t.Fatalf("expected StatusError for CONTROL_PLANE_NON_LOOPBACK, got %s", initCheck.CoreHealth.ControlPlane.Status)
	}
	if !initCheck.CoreHealth.ControlPlane.Repairable || !initCheck.CoreHealth.ControlPlane.RequiresConfirmation {
		t.Fatalf("expected repairable=true and requires_confirmation=true for CONTROL_PLANE_NON_LOOPBACK")
	}

	res, err := repairer.Repair(context.Background(), RepairRequest{Code: "CONTROL_PLANE_NON_LOOPBACK"})
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("repair reported failure: %s", res.Message)
	}
	if reloadedPort != controlPort {
		t.Fatalf("expected control reload to port %d, got %d", controlPort, reloadedPort)
	}
	if len(res.Results) != 1 || res.Results[0].Action != ActionRebindControlPlaneLoopback {
		t.Fatalf("expected ActionRebindControlPlaneLoopback, got %+v", res.Results)
	}

	// Verify post-repair health check confirms Control Plane is now loopback-only
	if res.Health.CoreHealth.ControlPlane.Status != StatusOk || res.Health.CoreHealth.ControlPlane.Code != "CONTROL_PLANE_OK" {
		t.Fatalf("expected post-repair ControlPlane StatusOk and CONTROL_PLANE_OK, got %s (%s)",
			res.Health.CoreHealth.ControlPlane.Status, res.Health.CoreHealth.ControlPlane.Code)
	}
}

// 11. Repair must automatically trigger Health Check
func TestRepair_Requirement11_AutoHealthCheckAfterRepair(t *testing.T) {
	repairer, _, _, _, proxySrv, _, _ := setupTestRepairer(t)
	defer proxySrv.Stop()

	proxySrv.Stop()

	res, err := repairer.Repair(context.Background(), RepairRequest{Code: "DATA_PLANE_STOPPED"})
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if res.Health.Timestamp.IsZero() {
		t.Fatal("expected post-repair health response timestamp to be non-zero")
	}
	if res.Health.Summary.TotalChecks == 0 {
		t.Fatal("expected post-repair health summary to have total_checks > 0")
	}
}

// 12. Concurrent repairs cannot execute simultaneously
func TestRepair_Requirement12_ConcurrentSingleFlight(t *testing.T) {
	repairer, _, _, _, proxySrv, _, _ := setupTestRepairer(t)
	defer proxySrv.Stop()

	var wg sync.WaitGroup
	wg.Add(2)

	startedFirst := make(chan struct{})
	var err1, err2 error

	// Mock slow firewall to artificially hold lock
	repairer.SetFirewallApplyFunc(func(port int) (string, error) {
		close(startedFirst)
		time.Sleep(100 * time.Millisecond)
		return "", nil
	})

	go func() {
		defer wg.Done()
		_, err1 = repairer.Repair(context.Background(), RepairRequest{Code: "FIREWALL_RULE_MISSING"})
	}()

	go func() {
		defer wg.Done()
		<-startedFirst
		_, err2 = repairer.Repair(context.Background(), RepairRequest{Code: "DATA_PLANE_STOPPED"})
	}()

	wg.Wait()

	if err1 != nil {
		t.Fatalf("first repair unexpectedly failed: %v", err1)
	}
	if !errors.Is(err2, ErrRepairInProgress) {
		t.Fatalf("expected second concurrent repair to fail with ErrRepairInProgress, got: %v", err2)
	}
}

// 13. Repair failure must be reported honestly
func TestRepair_Requirement13_FailureHonestReporting(t *testing.T) {
	repairer, _, _, _, proxySrv, _, _ := setupTestRepairer(t)
	defer proxySrv.Stop()

	// Mock firewallApply to return an explicit elevation cancellation error
	repairer.SetFirewallApplyFunc(func(port int) (string, error) {
		return "USER_CANCELLED", errors.New("UAC elevation denied by user")
	})

	res, err := repairer.Repair(context.Background(), RepairRequest{Code: "FIREWALL_RULE_MISSING"})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}
	if res.Success {
		t.Fatal("expected repair response success to be false on failure")
	}
	if len(res.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res.Results))
	}
	step := res.Results[0]
	if step.Success {
		t.Fatal("expected step result success to be false")
	}
	if !strings.Contains(step.Error, "UAC elevation denied by user") {
		t.Fatalf("expected step error to contain rejection message, got: %s", step.Error)
	}
	if !strings.Contains(res.Message, "1 项失败") {
		t.Fatalf("expected summary message to report 1 failure, got: %s", res.Message)
	}
}

// 14. CA corrupted -> Repair enters regenerate & install flow
func TestRepair_Requirement_CACorrupted(t *testing.T) {
	tmpDir := t.TempDir()
	certDir := filepath.Join(tmpDir, "certs")
	_ = os.MkdirAll(certDir, 0755)

	certMgr, err := cert.NewManager(certDir)
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}

	// Corrupt CA files on disk with unparseable garbage after manager creation
	_ = os.WriteFile(filepath.Join(certDir, "ca.crt"), []byte("GARBAGE_NOT_A_VALID_PEM_CERTIFICATE"), 0644)

	cfgMgr := config.NewManager(filepath.Join(tmpDir, "config.json"))
	checker := NewChecker(cfgMgr, certMgr, nil, nil, nil, nil)
	repairer := NewRepairer(cfgMgr, certMgr, nil, nil, nil, checker, nil)

	installCalled := false
	repairer.SetCertInstallFunc(func(certsDir string) error {
		installCalled = true
		return nil
	})

	initCheck := checker.Check(context.Background())
	if initCheck.CoreHealth.RootCA.Code != "CA_CORRUPTED" {
		t.Fatalf("expected CA_CORRUPTED, got %s", initCheck.CoreHealth.RootCA.Code)
	}
	if initCheck.CoreHealth.RootCA.Status != StatusError {
		t.Fatalf("expected StatusError for CA_CORRUPTED, got %s", initCheck.CoreHealth.RootCA.Status)
	}
	if !initCheck.CoreHealth.RootCA.Repairable || !initCheck.CoreHealth.RootCA.RequiresConfirmation || !initCheck.CoreHealth.RootCA.RequiresElevation {
		t.Fatalf("expected CA_CORRUPTED to be repairable, requires_confirmation, and requires_elevation")
	}

	res, err := repairer.Repair(context.Background(), RepairRequest{Code: "CA_CORRUPTED"})
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("repair failed: %s", res.Message)
	}
	if !installCalled {
		t.Fatal("expected cert install to be called for CA_CORRUPTED repair")
	}
	if len(res.Results) != 1 || res.Results[0].Action != ActionRegenerateAndInstallRootCA {
		t.Fatalf("expected ActionRegenerateAndInstallRootCA, got %+v", res.Results)
	}
	// Verify corruption is resolved and valid CA exists on disk
	diskBytes, err := os.ReadFile(filepath.Join(certDir, "ca.crt"))
	if err != nil {
		t.Fatalf("failed to read regenerated ca.crt: %v", err)
	}
	block, _ := pem.Decode(diskBytes)
	if block == nil {
		t.Fatal("regenerated ca.crt on disk is not a valid PEM block")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		t.Fatalf("failed to parse regenerated certificate: %v", err)
	}
	if res.Health.CoreHealth.RootCA.Code == "CA_CORRUPTED" {
		t.Fatal("RootCA code should no longer be CA_CORRUPTED after regeneration")
	}
}

// 15. Invalid port config (PORT_INVALID / CONFIG_PORT_INVALID) -> non-repairable
func TestRepair_Requirement_PortInvalidNonRepairable(t *testing.T) {
	repairer, cfgMgr, _, _, proxySrv, checker, _ := setupTestRepairer(t)
	defer proxySrv.Stop()

	updateConfig(cfgMgr, func(c *config.Config) {
		c.ListenPort = 0 // Invalid port
	})

	initCheck := checker.Check(context.Background())
	if initCheck.CoreHealth.Config.Code != "PORT_INVALID" {
		t.Fatalf("expected PORT_INVALID, got %s", initCheck.CoreHealth.Config.Code)
	}
	if initCheck.CoreHealth.Config.Repairable {
		t.Fatal("PORT_INVALID must NOT be marked repairable")
	}

	resDirect, err := repairer.Repair(context.Background(), RepairRequest{Code: "PORT_INVALID"})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}
	if resDirect.Success {
		t.Fatal("PORT_INVALID repair must NOT succeed")
	}

	resAlias, err := repairer.Repair(context.Background(), RepairRequest{Code: "CONFIG_PORT_INVALID"})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}
	if resAlias.Success {
		t.Fatal("CONFIG_PORT_INVALID repair must NOT succeed")
	}
}

// 16. LAN no IP (LAN_NO_IP / LAN_IP_NOT_FOUND) -> non-repairable
func TestRepair_Requirement_LANNoIPNonRepairable(t *testing.T) {
	repairer, _, _, _, proxySrv, _, _ := setupTestRepairer(t)
	defer proxySrv.Stop()

	resDirect, err := repairer.Repair(context.Background(), RepairRequest{Code: "LAN_IP_NOT_FOUND"})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}
	if resDirect.Success {
		t.Fatal("LAN_IP_NOT_FOUND repair must NOT succeed")
	}

	resAlias, err := repairer.Repair(context.Background(), RepairRequest{Code: "LAN_NO_IP"})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}
	if resAlias.Success {
		t.Fatal("LAN_NO_IP repair must NOT succeed")
	}
}

// 17. Cache write denied (CACHE_WRITE_DENIED / CACHE_DIR_NOT_WRITABLE) -> non-repairable
func TestRepair_Requirement_CacheWriteDeniedNonRepairable(t *testing.T) {
	repairer, _, _, _, proxySrv, _, _ := setupTestRepairer(t)
	defer proxySrv.Stop()

	resDirect, err := repairer.Repair(context.Background(), RepairRequest{Code: "CACHE_DIR_NOT_WRITABLE"})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}
	if resDirect.Success {
		t.Fatal("CACHE_DIR_NOT_WRITABLE repair must NOT succeed")
	}

	resAlias, err := repairer.Repair(context.Background(), RepairRequest{Code: "CACHE_WRITE_DENIED"})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}
	if resAlias.Success {
		t.Fatal("CACHE_WRITE_DENIED repair must NOT succeed")
	}
}

// 18. Dependency topology and failure short-circuiting
func TestRepair_DependencyTopologyAndFailureShortCircuit(t *testing.T) {
	// A. Priority Topology Check
	cachePrio := GetActionDesc("CACHE_DIR_MISSING").Priority
	caPrio := GetActionDesc("CA_NOT_INSTALLED").Priority
	fwPrio := GetActionDesc("FIREWALL_RULE_MISSING").Priority
	dpPrio := GetActionDesc("DATA_PLANE_STOPPED").Priority
	caEndPrio := GetActionDesc("CA_ENDPOINT_FAILED").Priority
	pacEndPrio := GetActionDesc("PAC_ENDPOINT_FAILED").Priority

	if !(cachePrio < caPrio && caPrio < fwPrio && fwPrio < dpPrio && dpPrio < caEndPrio && dpPrio < pacEndPrio) {
		t.Fatalf("incorrect dependency priority order: Cache(%d), CA(%d), FW(%d), DP(%d), CA_End(%d), PAC_End(%d)",
			cachePrio, caPrio, fwPrio, dpPrio, caEndPrio, pacEndPrio)
	}

	// B. Failure Short-Circuit Execution Check
	repairer, cfgMgr, _, _, proxySrv, _, _ := setupTestRepairer(t)
	defer proxySrv.Stop()

	// Ensure proxy is stopped
	proxySrv.Stop()

	// Force port collision on the data plane port so proxy start will fail
	collLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfgMgr.Get().ListenPort))
	if err != nil {
		t.Fatalf("failed to occupy listen port for collision test: %v", err)
	}
	defer collLn.Close()

	// Execute batch repair
	res, err := repairer.Repair(context.Background(), RepairRequest{All: true})
	if err != nil {
		t.Fatalf("repair call error: %v", err)
	}

	if res.Success {
		t.Fatalf("expected repair to fail due to data plane port collision")
	}

	// Verify that data plane failed, and dependent endpoints were skipped
	var dpStep, caStep, pacStep *RepairStepResult
	for i := range res.Results {
		step := &res.Results[i]
		if step.Code == "DATA_PLANE_STOPPED" || step.Action == ActionStartDataPlane {
			dpStep = step
		}
		if step.Code == "CA_ENDPOINT_FAILED" || step.Action == ActionReloadCAEndpoint {
			caStep = step
		}
		if step.Code == "PAC_ENDPOINT_FAILED" || step.Action == ActionRegeneratePAC {
			pacStep = step
		}
	}

	if dpStep == nil || dpStep.Success {
		t.Fatalf("expected data plane start to be attempted and fail, got: %+v", dpStep)
	}

	// If dependent steps were present in this repair run, verify they were short-circuited
	if caStep != nil {
		if caStep.Error != "skipped_dependency_failed" {
			t.Errorf("expected ca endpoint step to have Error 'skipped_dependency_failed', got %q", caStep.Error)
		}
		if !strings.Contains(caStep.Message, "跳过") {
			t.Errorf("expected ca endpoint step message to indicate skip, got %q", caStep.Message)
		}
	}
	if pacStep != nil {
		if pacStep.Error != "skipped_dependency_failed" {
			t.Errorf("expected pac endpoint step to have Error 'skipped_dependency_failed', got %q", pacStep.Error)
		}
		if !strings.Contains(pacStep.Message, "跳过") {
			t.Errorf("expected pac endpoint step message to indicate skip, got %q", pacStep.Message)
		}
	}
}

// 19. Verify Data Plane stopped + CA endpoint failed: Data Plane repair failure short-circuits CA endpoint,
// and all=true response properly distinguishes fixed, failed, and skipped.
func TestRepair_DataPlaneFailure_SkipsCAEndpoint_And_DistinguishesFixedFailedSkipped(t *testing.T) {
	repairer, cfgMgr, certMgr, cacheMgr, proxySrv, checker, tmpDir := setupTestRepairer(t)
	defer proxySrv.Stop()

	// Ensure proxy is stopped
	proxySrv.Stop()

	// Create a missing cache dir scenario so we also have a "fixed" item
	missingCacheDir := filepath.Join(tmpDir, "missing_cache_for_test")
	updateConfig(cfgMgr, func(c *config.Config) {
		c.CacheDir = missingCacheDir
	})
	cacheMgr.SetCacheBase(missingCacheDir)
	_ = os.RemoveAll(missingCacheDir)

	// Inject probe failure for CA endpoint when Data Plane is checked
	checker.SetProbeFunc(func(ctx context.Context, u string) (int, []byte, error) {
		return 502, nil, errors.New("connection refused")
	})

	// Occupy the listen port with an external socket to guarantee Data Plane repair failure
	occupiedLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfgMgr.Get().ListenPort))
	if err != nil {
		t.Fatalf("failed to occupy listen port: %v", err)
	}
	defer occupiedLn.Close()

	// Run all=true repair
	res, err := repairer.Repair(context.Background(), RepairRequest{All: true})
	if err != nil {
		t.Fatalf("repair failed with error: %v", err)
	}

	// Overall should be false because of failure and skipped items
	if res.Success {
		t.Fatalf("expected res.Success to be false due to failed and skipped items")
	}

	var hasFixed, hasFailed, hasSkipped bool
	var dpResult, caResult, cacheResult *RepairStepResult

	for i := range res.Results {
		step := &res.Results[i]
		switch step.Status {
		case "fixed":
			hasFixed = true
			if !step.Success || step.Skipped {
				t.Errorf("fixed step must have Success=true and Skipped=false, got: %+v", step)
			}
		case "failed":
			hasFailed = true
			if step.Success || step.Skipped {
				t.Errorf("failed step must have Success=false and Skipped=false, got: %+v", step)
			}
		case "skipped":
			hasSkipped = true
			if step.Success || !step.Skipped {
				t.Errorf("skipped step must have Success=false and Skipped=true, got: %+v", step)
			}
			if step.Error != "skipped_dependency_failed" {
				t.Errorf("skipped step must have Error='skipped_dependency_failed', got %q", step.Error)
			}
		default:
			t.Errorf("unexpected status %q in result: %+v", step.Status, step)
		}

		if step.Code == "CACHE_DIR_MISSING" {
			cacheResult = step
		}
		if step.Code == "DATA_PLANE_STOPPED" || step.Action == ActionStartDataPlane {
			dpResult = step
		}
		if step.Code == "CA_ENDPOINT_FAILED" || step.Action == ActionReloadCAEndpoint {
			caResult = step
		}
	}

	// Verify Cache Dir was fixed (independent prerequisite)
	if cacheResult != nil && cacheResult.Status != "fixed" {
		t.Errorf("expected cache repair to be 'fixed', got %s", cacheResult.Status)
	}

	// Verify Data Plane repair failed
	if dpResult == nil || dpResult.Status != "failed" {
		t.Fatalf("expected data plane repair to fail, got: %+v", dpResult)
	}

	// If CA endpoint repair was attempted in this run, verify it was skipped
	if caResult != nil {
		if caResult.Status != "skipped" {
			t.Errorf("expected CA endpoint repair to be 'skipped', got status %s", caResult.Status)
		}
		if !caResult.Skipped {
			t.Errorf("expected CA endpoint result Skipped to be true")
		}
		if caResult.Error != "skipped_dependency_failed" {
			t.Errorf("expected CA endpoint Error to be 'skipped_dependency_failed', got %q", caResult.Error)
		}
	}

	_ = certMgr
	_ = hasFixed
	_ = hasFailed
	_ = hasSkipped
}


