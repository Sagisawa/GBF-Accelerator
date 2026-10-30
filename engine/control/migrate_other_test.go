//go:build !darwin

package control

import "testing"

// Non-Darwin builds use the stub migration endpoint; no destination override is needed.
func setMigrationTargetForTest(*testing.T, string) {}
