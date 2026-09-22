package proxy

import (
	"errors"
	"testing"
	"time"
)

func TestFailoverManagerRequiresConsecutiveFailures(t *testing.T) {
	m := newFailoverManager()
	m.configure(true, true, 2*time.Second, 3, 60*time.Second, true)
	now := time.Unix(100, 0)

	for i := 0; i < 2; i++ {
		if tr := m.observe(failoverRoutePrimary, false, 3*time.Second, nil, now.Add(time.Duration(i)*time.Second)); tr != nil {
			t.Fatalf("unexpected transition: %+v", tr)
		}
	}
	if tr := m.observe(failoverRoutePrimary, false, 3*time.Second, nil, now.Add(2*time.Second)); tr == nil || tr.to != failoverRouteBackup {
		t.Fatalf("expected backup transition, got %+v", tr)
	}
}

func TestFailoverThresholdWatchTriggersBeforeRequestCompletes(t *testing.T) {
	m := newFailoverManager()
	m.configure(true, true, 20*time.Millisecond, 1, 60*time.Second, true)

	s := &ProxyServer{failover: m}
	watch := s.startFailoverWatch(failoverRoutePrimary, false)
	if watch == nil {
		t.Fatal("expected failover watch")
	}

	time.Sleep(60 * time.Millisecond)
	if got := m.activeRoute(); got != failoverRouteBackup {
		t.Fatalf("expected backup before request completion, got %s", got)
	}

	if !watch.finish() {
		// Timer already performed the observation. This is expected and confirms
		// the route transition happened while the request remained in flight.
		return
	}
	t.Fatal("request completed before threshold unexpectedly")
}

func TestFailoverManagerConnectionErrorCounts(t *testing.T) {
	m := newFailoverManager()
	m.configure(true, true, 0, 2, 60*time.Second, true)
	now := time.Unix(200, 0)
	err := errors.New("connect failed")

	if tr := m.observe(failoverRoutePrimary, false, 50*time.Millisecond, err, now); tr != nil {
		t.Fatalf("unexpected early transition: %+v", tr)
	}
	if tr := m.observe(failoverRoutePrimary, false, 50*time.Millisecond, err, now.Add(time.Second)); tr == nil || tr.reason != "connection_error" {
		t.Fatalf("expected connection error, got %+v", tr)
	}
}

func TestFailoverManagerRecoveryTrial(t *testing.T) {
	m := newFailoverManager()
	m.configure(true, true, 2*time.Second, 1, 10*time.Second, true)
	now := time.Unix(300, 0)

	if tr := m.observe(failoverRoutePrimary, false, 3*time.Second, nil, now); tr == nil {
		t.Fatal("expected switch")
	}
	if route, trial := m.routeForRequest(true, now.Add(5*time.Second)); route != failoverRouteBackup || trial {
		t.Fatalf("expected cooldown, got %s/%v", route, trial)
	}
	if route, trial := m.routeForRequest(true, now.Add(11*time.Second)); route != failoverRoutePrimary || !trial {
		t.Fatalf("expected recovery trial, got %s/%v", route, trial)
	}
	if route, trial := m.routeForRequest(true, now.Add(12*time.Second)); route != failoverRouteBackup || trial {
		t.Fatalf("expected backup while trial runs, got %s/%v", route, trial)
	}
	if tr := m.observe(failoverRoutePrimary, true, 50*time.Millisecond, nil, now.Add(11*time.Second)); tr == nil || tr.reason != "primary_recovered" {
		t.Fatalf("expected recovery transition, got %+v", tr)
	}
	if route, trial := m.routeForRequest(true, now.Add(12*time.Second)); route != failoverRoutePrimary || trial {
		t.Fatalf("expected primary after recovery, got %s/%v", route, trial)
	}
}

func TestFailoverManagerFailedRecoveryTrialKeepsBackup(t *testing.T) {
	m := newFailoverManager()
	m.configure(true, true, 2*time.Second, 1, 10*time.Second, true)
	now := time.Unix(400, 0)

	if tr := m.observe(failoverRoutePrimary, false, 3*time.Second, nil, now); tr == nil {
		t.Fatal("expected switch")
	}
	_, trial := m.routeForRequest(true, now.Add(11*time.Second))
	if !trial {
		t.Fatal("expected recovery trial")
	}
	if tr := m.observe(failoverRoutePrimary, true, 3*time.Second, nil, now.Add(11*time.Second)); tr != nil {
		t.Fatalf("unexpected recovery transition: %+v", tr)
	}
	if route, trial := m.routeForRequest(true, now.Add(12*time.Second)); route != failoverRouteBackup || trial {
		t.Fatalf("expected backup after failed trial, got %s/%v", route, trial)
	}
}

func TestFailoverManagerIgnoresBackupObservations(t *testing.T) {
	m := newFailoverManager()
	m.configure(true, true, 2*time.Second, 1, 60*time.Second, true)
	now := time.Unix(500, 0)

	if tr := m.observe(failoverRoutePrimary, false, 3*time.Second, nil, now); tr == nil {
		t.Fatal("expected switch")
	}
	if tr := m.observe(failoverRouteBackup, false, 10*time.Second, errors.New("backup failed"), now.Add(time.Second)); tr != nil {
		t.Fatalf("unexpected backup transition: %+v", tr)
	}
}

func TestFailoverManagerDisabledNeverSwitches(t *testing.T) {
	m := newFailoverManager()
	m.configure(false, true, 2*time.Second, 1, 60*time.Second, true)
	if tr := m.observe(failoverRoutePrimary, false, 10*time.Second, errors.New("fail"), time.Now()); tr != nil {
		t.Fatalf("disabled switched: %+v", tr)
	}
	if got := m.activeRoute(); got != failoverRoutePrimary {
		t.Fatalf("expected primary, got %s", got)
	}
}

func TestFailoverProxySchemeValidation(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:7890", "https://127.0.0.1:8443", "socks5://127.0.0.1:10808", "direct"} {
		if !isSupportedFailoverProxy(raw) { t.Errorf("unsupported: %q", raw) }
	}
	for _, raw := range []string{"", "ftp://127.0.0.1:21", "not-a-url"} {
		if isSupportedFailoverProxy(raw) { t.Errorf("unexpected support: %q", raw) }
	}
}
