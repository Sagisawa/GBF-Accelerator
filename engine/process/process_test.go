package process

import (
	"testing"
)

func TestIsPortProtected(t *testing.T) {
	protected := []int{80, 443, 6152, 7890, 7891, 7897, 8099, 10808, 10809}
	for _, p := range protected {
		if !IsPortProtected(p) {
			t.Errorf("expected port %d to be protected", p)
		}
		killed, err := KillProcessOnPort(p)
		if err != nil {
			t.Errorf("KillProcessOnPort(%d) returned error: %v", p, err)
		}
		if killed {
			t.Errorf("KillProcessOnPort(%d) on protected port must return false", p)
		}
	}

	unprotected := []int{8124, 8125, 8080, 9999}
	for _, p := range unprotected {
		if IsPortProtected(p) {
			t.Errorf("port %d should not be marked protected", p)
		}
	}
}
