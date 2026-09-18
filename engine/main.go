package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/control"
	"gbf-proxy/desktop"
	"gbf-proxy/proxy"
	"gbf-proxy/telemetry"
	"gbf-proxy/ui"
)

type appController struct {
	cfgMgr   *config.Manager
	proxySrv *proxy.ProxyServer
	quitChan chan struct{}
	quitOnce sync.Once
}

func (a *appController) IsRunning() bool {
	return a.proxySrv.IsRunning()
}

func (a *appController) StartProxy() error {
	return a.proxySrv.Start()
}

func (a *appController) StopProxy() {
	a.proxySrv.Stop()
}

func (a *appController) GetListenPort() int {
	return a.cfgMgr.Get().ListenPort
}

func (a *appController) GetControlPort() int {
	return a.cfgMgr.Get().ControlPort
}

func (a *appController) GetCacheDir() string {
	return a.cfgMgr.Get().CacheDir
}

func (a *appController) OpenBrowser(url string) error {
	return desktop.OpenBrowser(url)
}

func (a *appController) OpenAppWindow(url string) error {
	return desktop.OpenAppWindow(url)
}

func (a *appController) OpenFolder(path string) error {
	return desktop.OpenFolder(path)
}

func (a *appController) Quit() {
	a.quitOnce.Do(func() {
		close(a.quitChan)
	})
}

func main() {
	desktop.AttachConsole()

	showVersion := flag.Bool("v", false, "Print application version and exit")
	showVersionLong := flag.Bool("version", false, "Print application version and exit")
	proxyPort := flag.Int("proxy-port", 8124, "Target proxy listen port")
	controlPort := flag.Int("control-port", 8125, "Target control plane port")
	upstreamProxy := flag.String("upstream-proxy", "", "Upstream proxy URL (e.g. http://127.0.0.1:8080)")
	configPath := flag.String("config", "config.json", "Path to config.json")
	cacheDir := flag.String("cache-dir", "", "Path to ACGPower cache root directory")
	allowLAN := flag.Bool("allow-lan", false, "Allow connections from LAN devices")
	directMode := flag.Bool("direct-mode", false, "Force direct connection mode")
	verifyUpstreamTLS := flag.Bool("verify-upstream-tls", false, "Verify upstream TLS certificates")
	noGUI := flag.Bool("nogui", false, "Run in headless CLI mode without system tray")
	headless := flag.Bool("headless", false, "Alias for -nogui")
	openBrowser := flag.Bool("open-browser", false, "Automatically open Web console in browser on startup")
	minimized := flag.Bool("minimized", false, "Start minimized in system tray without opening window")

	flag.Parse()

	if *showVersion || *showVersionLong {
		fmt.Printf("GBF-Accelerator v%s\n", config.AppVersion)
		os.Exit(0)
	}

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

	// 6. Setup Desktop Integration & System Tray
	quitChan := make(chan struct{})
	appCtrl := &appController{
		cfgMgr:   cfgMgr,
		proxySrv: proxySrv,
		quitChan: quitChan,
	}

	var tray desktop.Tray
	if !*noGUI && !*headless {
		tray = desktop.NewTray(appCtrl, ui.AppIconBytes)
		if err := tray.Start(); err != nil {
			fmt.Printf("[*] Desktop tray not available in current session: %v\n", err)
		} else {
			defer tray.Stop()
		}
	}

	consoleURL := fmt.Sprintf("http://127.0.0.1:%d/", curCfg.ControlPort)
	if !*noGUI && !*headless && !*minimized {
		if err := desktop.OpenAppWindow(consoleURL); err != nil {
			fmt.Printf("[*] Failed to open app window: %v\n", err)
		}
	} else if *openBrowser {
		_ = desktop.OpenBrowser(consoleURL)
	}

	// 7. Wait for Shutdown Signals (OS signal or Tray quit)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sigCh:
		fmt.Println("\n[*] Received shutdown signal...")
	case <-quitChan:
		fmt.Println("\n[*] Received desktop quit command...")
	}

	fmt.Println("[*] Shutting down GBF Accelerator Go Core...")
	if tray != nil {
		tray.Stop()
	}
	ctrlSrv.Stop()
	proxySrv.Stop()
	fmt.Println("[+] Shutdown complete.")
}
