package control

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"gbf-proxy/config"
	"gbf-proxy/health"
)

// configTxRecorder captures the injected system side effects of the config transaction so tests
// can assert that the persisted config, the runtime config and the system state agree after
// concurrent operations. Nothing touches the real PAC settings, registry or listeners.
type configTxRecorder struct {
	mu          sync.Mutex
	pacEnabled  []string
	pacDisabled int
	startupSet  []bool
	rebindPorts []int
}

func (r *configTxRecorder) enablePAC(url string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pacEnabled = append(r.pacEnabled, url)
	return nil
}

func (r *configTxRecorder) disablePAC(bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pacDisabled++
	return nil
}

func (r *configTxRecorder) setStartup(enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.startupSet = append(r.startupSet, enabled)
	return nil
}

func (r *configTxRecorder) reloadControlPort(port int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rebindPorts = append(r.rebindPorts, port)
	return nil
}

type configTxState struct {
	pacEnabled  []string
	pacDisabled int
	startupSet  []bool
	rebindPorts []int
}

func (r *configTxRecorder) state() configTxState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return configTxState{
		pacEnabled:  append([]string(nil), r.pacEnabled...),
		pacDisabled: r.pacDisabled,
		startupSet:  append([]bool(nil), r.startupSet...),
		rebindPorts: append([]int(nil), r.rebindPorts...),
	}
}

func installConfigTxRecorder(ctrl *testControlServer) *configTxRecorder {
	rec := &configTxRecorder{}
	ctrl.enablePACProxyFn = rec.enablePAC
	ctrl.disablePACProxyFn = rec.disablePAC
	ctrl.setStartupEnabledFn = rec.setStartup
	return rec
}

// pauseApply installs the interleaving hook: the next apply transaction signals the returned
// channel after reading its candidate snapshot and then blocks until release runs. This makes
// the "stale candidate snapshot" interleaving deterministic instead of timing-dependent.
func pauseApply(t *testing.T) (entered <-chan struct{}, release func()) {
	t.Helper()
	enteredCh := make(chan struct{})
	releaseCh := make(chan struct{})
	var enteredOnce sync.Once
	var releaseOnce sync.Once
	testBeforeApplyCommitHook = func() {
		enteredOnce.Do(func() { close(enteredCh) })
		<-releaseCh
	}
	release = func() { releaseOnce.Do(func() { close(releaseCh) }) }
	t.Cleanup(func() {
		release()
		testBeforeApplyCommitHook = nil
	})
	return enteredCh, release
}

// postControl issues a loopback control plane request and returns its recorder.
func postControl(ctrl *testControlServer, path string, body []byte) *httptest.ResponseRecorder {
	var reader *bytes.Buffer
	if body != nil {
		reader = bytes.NewBuffer(body)
	}
	var req *http.Request
	if reader == nil {
		req = httptest.NewRequest(http.MethodPost, path, nil)
	} else {
		req = httptest.NewRequest(http.MethodPost, path, reader)
	}
	req.Host = "127.0.0.1:8125"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)
	return w
}

// TestApplyConfigConcurrentWithSysProxyToggleDoesNotLoseUpdate verifies that a system proxy
// toggle issued while an apply transaction holds a stale candidate snapshot is not silently
// reverted by that transaction. The persisted config must always agree with the system proxy
// state that was actually applied.
func TestApplyConfigConcurrentWithSysProxyToggleDoesNotLoseUpdate(t *testing.T) {
	for _, tc := range []struct {
		name         string
		startEnabled bool
		writerPath   string
		wantEnabled  bool
	}{
		{name: "Enable", startEnabled: false, writerPath: "/api/sysproxy/enable", wantEnabled: true},
		{name: "Disable", startEnabled: true, writerPath: "/api/sysproxy/disable", wantEnabled: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl, cleanup := setupTestControlServer(t)
			defer cleanup()
			rec := installConfigTxRecorder(ctrl)

			if tc.startEnabled {
				if _, err := ctrl.cfgMgr.UpdateWithError(func(c *config.Config) {
					c.AutoSystemProxy = true
				}); err != nil {
					t.Fatalf("failed to seed auto_system_proxy: %v", err)
				}
			}

			entered, release := pauseApply(t)

			// Records whether the concurrent write managed to land inside apply's window.
			writerCommitted := make(chan struct{})
			var commitOnce sync.Once
			ctrl.cfgMgr.OnUpdate(func(c *config.Config) {
				if c.AutoSystemProxy == tc.wantEnabled {
					commitOnce.Do(func() { close(writerCommitted) })
				}
			})

			var wg sync.WaitGroup
			wg.Add(2)

			var applyResp *httptest.ResponseRecorder
			go func() {
				defer wg.Done()
				applyResp = postControl(ctrl, "/api/config/apply", []byte(`{"enable_prefetch":false}`))
			}()

			<-entered

			go func() {
				defer wg.Done()
				postControl(ctrl, tc.writerPath, nil)
			}()

			select {
			case <-writerCommitted:
			case <-time.After(300 * time.Millisecond):
			}

			release()
			wg.Wait()

			if applyResp.Code != http.StatusOK {
				t.Fatalf("apply returned %d: %s", applyResp.Code, applyResp.Body.String())
			}

			st := rec.state()
			osEnabled := len(st.pacEnabled) > 0 && st.pacDisabled == 0
			if osEnabled != tc.wantEnabled {
				t.Fatalf("injected system state mismatch: PAC enables=%v disables=%d, want enabled=%v",
					st.pacEnabled, st.pacDisabled, tc.wantEnabled)
			}

			final := ctrl.cfgMgr.Get()
			if final.AutoSystemProxy != tc.wantEnabled {
				t.Fatalf("lost update: persisted auto_system_proxy=%v but the applied system state says enabled=%v "+
					"(the apply transaction committed a stale candidate over the concurrent %s)",
					final.AutoSystemProxy, tc.wantEnabled, tc.writerPath)
			}
		})
	}
}

// TestApplyConfigConcurrentWithStartupSetDoesNotLoseUpdate verifies the same invariant for the
// auto start registration written by /api/startup/set.
func TestApplyConfigConcurrentWithStartupSetDoesNotLoseUpdate(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()
	rec := installConfigTxRecorder(ctrl)

	entered, release := pauseApply(t)

	writerCommitted := make(chan struct{})
	var commitOnce sync.Once
	ctrl.cfgMgr.OnUpdate(func(c *config.Config) {
		if c.AutoStart {
			commitOnce.Do(func() { close(writerCommitted) })
		}
	})

	var wg sync.WaitGroup
	wg.Add(2)

	var applyResp *httptest.ResponseRecorder
	go func() {
		defer wg.Done()
		applyResp = postControl(ctrl, "/api/config/apply", []byte(`{"enable_prefetch":false}`))
	}()

	<-entered

	go func() {
		defer wg.Done()
		postControl(ctrl, "/api/startup/set", []byte(`{"enabled":true}`))
	}()

	select {
	case <-writerCommitted:
	case <-time.After(300 * time.Millisecond):
	}

	release()
	wg.Wait()

	if applyResp.Code != http.StatusOK {
		t.Fatalf("apply returned %d: %s", applyResp.Code, applyResp.Body.String())
	}

	st := rec.state()
	if len(st.startupSet) != 1 || !st.startupSet[0] {
		t.Fatalf("injected startup state mismatch: %v", st.startupSet)
	}
	final := ctrl.cfgMgr.Get()
	if !final.AutoStart {
		t.Fatalf("lost update: persisted auto_start=false while the applied startup state is enabled " +
			"(the apply transaction committed a stale candidate over /api/startup/set)")
	}
}

// TestApplyConfigConcurrentWithHealthRepairRebindsToCommittedPort verifies that a health repair
// which rebinds the control plane uses the configuration committed by a concurrent apply instead
// of the stale snapshot it read before, so the listener never ends up on a port that differs from
// the persisted control_port.
func TestApplyConfigConcurrentWithHealthRepairRebindsToCommittedPort(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()
	rec := installConfigTxRecorder(ctrl)

	// Inject the repairer so the control plane rebind is recorded rather than performed.
	checker := health.NewChecker(ctrl.cfgMgr, nil, ctrl.cacheMgr, nil, ctrl.stats, ctrl.ListenerAddr)
	ctrl.SetRepairer(health.NewRepairer(ctrl.cfgMgr, nil, ctrl.cacheMgr, nil, ctrl.stats, checker, rec.reloadControlPort))

	const newControlPort = 8126
	entered, release := pauseApply(t)

	var wg sync.WaitGroup
	wg.Add(2)

	var applyResp *httptest.ResponseRecorder
	go func() {
		defer wg.Done()
		applyResp = postControl(ctrl, "/api/config/apply", []byte(fmt.Sprintf(`{"control_port":%d}`, newControlPort)))
	}()

	<-entered

	go func() {
		defer wg.Done()
		postControl(ctrl, "/api/health/repair", []byte(`{"code":"CONTROL_PLANE_NON_LOOPBACK"}`))
	}()

	// The apply transaction only commits after release(), so the repair either reads the config
	// before that point (pre-fix: stale port) or is held off until after it (fixed: new port).
	deadline := time.Now().Add(1500 * time.Millisecond)
	for len(rec.state().rebindPorts) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	release()
	wg.Wait()

	if applyResp.Code != http.StatusOK {
		t.Fatalf("apply returned %d: %s", applyResp.Code, applyResp.Body.String())
	}

	final := ctrl.cfgMgr.Get()
	if final.ControlPort != newControlPort {
		t.Fatalf("apply did not persist control_port=%d, got %d", newControlPort, final.ControlPort)
	}

	ports := rec.state().rebindPorts
	if len(ports) != 1 {
		t.Fatalf("expected exactly one control plane rebind, got %v", ports)
	}
	if ports[0] != final.ControlPort {
		t.Fatalf("listener/config divergence: the control plane was rebound to port %d while the persisted control_port is %d",
			ports[0], final.ControlPort)
	}
}
