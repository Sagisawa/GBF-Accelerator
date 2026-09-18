package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/control"
	"gbf-proxy/proxy"
	"gbf-proxy/telemetry"
)

func main() {
	proxyPort := flag.Int("proxy-port", 8124, "Target proxy listen port")
	controlPort := flag.Int("control-port", 8125, "Target control plane port")
	upstreamProxy := flag.String("upstream-proxy", "", "Upstream proxy URL (e.g. http://127.0.0.1:8080)")
	configPath := flag.String("config", "config.json", "Path to config.json")
	cacheDir := flag.String("cache-dir", "", "Path to ACGPower cache root directory")
	allowLAN := flag.Bool("allow-lan", false, "Allow connections from LAN devices")
	directMode := flag.Bool("direct-mode", false, "Force direct connection mode")
	verifyUpstreamTLS := flag.Bool("verify-upstream-tls", false, "Verify upstream TLS certificates")

	flag.Parse()

	// 1. Initialize Configuration
	cfgMgr := config.NewManager(*configPath)
	cfgMgr.Update(func(c *config.Config) {
		if *proxyPort > 0 {
			c.ListenPort = *proxyPort
		}
		if *controlPort > 0 {
			c.ControlPort = *controlPort
		}
		if *upstreamProxy != "" {
			c.UpstreamProxy = *upstreamProxy
			c.DirectMode = false
			c.VerifyUpstreamTLS = false // Disable TLS verify for mock upstream proxy
		}
		if *directMode {
			c.DirectMode = true
		}
		if *allowLAN {
			c.AllowLAN = true
		}
		if *cacheDir != "" {
			c.CacheDir = config.NormalizeCacheDir(*cacheDir)
		}
		if *verifyUpstreamTLS {
			c.VerifyUpstreamTLS = true
		}
	})

	curCfg := cfgMgr.Get()

	// 2. Initialize Certificates
	certsDir := "certs"
	if fi, err := os.Stat("certs"); err != nil || !fi.IsDir() {
		// check if ../certs exists
		if fi2, err2 := os.Stat("../certs"); err2 == nil && fi2.IsDir() {
			certsDir = "../certs"
		}
	}
	certMgr, err := cert.NewManager(certsDir)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "[-] Failed to initialize Certificate Manager: %v\n", err)
		os.Exit(1)
	}

	// 3. Initialize Cache
	cacheMgr := cache.NewManager(curCfg.CacheDir, curCfg.RAMCacheMaxMB)
	stats := telemetry.GlobalStats

	// 4. Initialize Core Proxy
	proxySrv := proxy.NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	if err := proxySrv.Start(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "[-] Failed to start Proxy Server: %v\n", err)
		os.Exit(1)
	}

	// 5. Initialize Control Server
	ctrlSrv := control.NewControlServer(cfgMgr, cacheMgr, proxySrv, stats)
	if err := ctrlSrv.Start(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "[-] Failed to start Control Server: %v\n", err)
		proxySrv.Stop()
		os.Exit(1)
	}

	fmt.Printf("[+] GBF Accelerator Go Core v%s started\n", config.AppVersion)
	fmt.Printf("    Proxy listening on   : %s:%d\n", cfgMgr.GetEffectiveListenHost(), curCfg.ListenPort)
	fmt.Printf("    Control plane on     : 127.0.0.1:%d\n", curCfg.ControlPort)
	fmt.Printf("    Cache directory      : %s\n", filepath.Clean(curCfg.CacheDir))
	if curCfg.UpstreamProxy != "" {
		fmt.Printf("    Upstream proxy       : %s\n", curCfg.UpstreamProxy)
	}

	// 6. Wait for Shutdown Signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\n[*] Shutting down GBF Accelerator Go Core...")
	ctrlSrv.Stop()
	proxySrv.Stop()
	fmt.Println("[+] Shutdown complete.")
}
