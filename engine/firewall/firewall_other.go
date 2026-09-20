//go:build !windows

package firewall

import "fmt"

func platformSupported() bool {
	return false
}

func platformGetStatus(port int) (Status, error) {
	if !validPort(port) {
		return Status{Supported: false, Port: port, RuleName: RuleName(port)}, NewError(CodeInvalidPort, fmt.Errorf("invalid port %d", port))
	}
	return Status{
		OK:        true,
		Supported: false,
		Allowed:   false,
		Port:      port,
		RuleName:  RuleName(port),
	}, nil
}

func platformCheckRule(port int) (bool, error) {
	if !validPort(port) {
		return false, NewError(CodeInvalidPort, fmt.Errorf("invalid port %d", port))
	}
	return false, NewError(CodeUnsupported, fmt.Errorf("Windows firewall integration is only supported on Windows"))
}

func platformApplyRule(port int) (string, error) {
	if !validPort(port) {
		return "", NewError(CodeInvalidPort, fmt.Errorf("invalid port %d", port))
	}
	return "", NewError(CodeUnsupported, fmt.Errorf("Windows firewall integration is only supported on Windows"))
}
