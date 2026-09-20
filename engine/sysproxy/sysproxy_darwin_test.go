//go:build darwin
// +build darwin

package sysproxy

import (
	"testing"
)

func TestParseServicesOrdered(t *testing.T) {
	sampleOutput := `
An asterisk (*) denotes that a network service is disabled.
(1) Wi-Fi
(Hardware Port: Wi-Fi, Device: en0)

(2) Ethernet
(Hardware Port: Ethernet, Device: en1)

(3) Thunderbolt Bridge
(Hardware Port: Thunderbolt Bridge, Device: bridge0)

(4) *Bluetooth PAN
(Hardware Port: Bluetooth PAN, Device: en2)
`
	svcs := parseServicesOrdered(sampleOutput)
	if len(svcs) != 3 {
		t.Fatalf("expected 3 enabled services, got %d: %v", len(svcs), svcs)
	}
	if svcs[0] != "Wi-Fi" {
		t.Errorf("expected svcs[0] to be Wi-Fi, got %q", svcs[0])
	}
	if svcs[1] != "Ethernet" {
		t.Errorf("expected svcs[1] to be Ethernet, got %q", svcs[1])
	}
	if svcs[2] != "Thunderbolt Bridge" {
		t.Errorf("expected svcs[2] to be Thunderbolt Bridge, got %q", svcs[2])
	}
}
