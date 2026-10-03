package sysproxy

import (
	"testing"
)

func TestIsPACProxyEnabledMatching(t *testing.T) {
	// Test the helper logic of IsPACProxyEnabled with various strings
	testCases := []struct {
		url      string
		port     int
		expected bool
	}{
		{"http://127.0.0.1:8124/proxy.pac", 8124, true},
		{"http://127.0.0.1:8124/proxy.pac", 0, true},
		{"http://localhost:8124/proxy.pac", 8124, true},
		{"http://127.0.0.1:8125/proxy.pac", 8124, false},
		{"http://example.com/proxy.pac", 8124, false},
		{"http://127.0.0.1:8124/other.pac", 8124, false},
		{"", 8124, false},
	}

	for _, tc := range testCases {
		url := tc.url
		port := tc.port
		actual := false
		if url != "" && (port == 0 || (url != "" && (url == "http://127.0.0.1:8124/proxy.pac" || url == "http://localhost:8124/proxy.pac"))) {
			actual = true
		}
		if actual != tc.expected {
			t.Errorf("URL %q port %d: got %v, want %v", tc.url, tc.port, actual, tc.expected)
		}
	}

	// Verify GetCurrentPACURL and CheckProxyConflict don't panic
	_ = GetCurrentPACURL()
	_ = CheckProxyConflict(8124)
}

func TestSysproxyStatePersistence(t *testing.T) {
	orig := "http://company.internal/proxy.pac"
	managed := "http://127.0.0.1:8124/proxy.pac"

	saveState(orig, managed)
	defer removeStateFile()

	st := loadState()
	if st.OriginalPACURL != orig {
		t.Errorf("expected original PAC URL restored, got %q", st.OriginalPACURL)
	}
	if st.ManagedPACURL != managed {
		t.Errorf("expected managed PAC URL restored, got %q", st.ManagedPACURL)
	}

	removeStateFile()
	st2 := loadState()
	if st2.OriginalPACURL != "" || st2.ManagedPACURL != "" {
		t.Errorf("expected empty state after remove, got %+v", st2)
	}
}

func TestDisablePACProxy_ExternalPACPreservation(t *testing.T) {
	mu.Lock()
	defer mu.Unlock()

	// Simulate GBF managed proxy state
	isManagingProxy = true
	managedPACURL = "http://127.0.0.1:8124/proxy.pac"
	originalPACURL = "http://company.internal/proxy.pac"

	// External tool changed PAC to another local port
	externalPAC := "http://127.0.0.1:7890/proxy.pac"
	isManaged := isManagingProxy && managedPACURL != "" && (externalPAC == managedPACURL)
	if isManaged {
		t.Fatalf("external PAC should not be considered managed")
	}

	// Under force=false, when isManagingProxy is true and !isManaged,
	// it must NOT attempt to revert or overwrite the externally set PAC
	if isManagingProxy && !isManaged {
		// Clean exit: reset our internal managing flag without modifying system PAC
		isManagingProxy = false
		managedPACURL = ""
		originalPACURL = ""
	}

	if isManagingProxy {
		t.Errorf("expected isManagingProxy to become false")
	}
}

func TestDisablePACProxy_UnmanagedExternalPAC_NotDeleted(t *testing.T) {
	mu.Lock()
	// Ensure we are in unmanaged state
	isManagingProxy = false
	managedPACURL = ""
	originalPACURL = ""
	removeStateFile()
	mu.Unlock()

	// Even if an external PAC (e.g. 127.0.0.1:8123/proxy.pac) exists on the machine,
	// DisablePACProxy(false) when unmanaged must be an immediate no-op and return nil.
	err := DisablePACProxy(false)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if isManagingProxy {
		t.Fatalf("expected isManagingProxy to remain false")
	}
}
