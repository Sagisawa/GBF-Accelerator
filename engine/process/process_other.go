//go:build !windows && !darwin
// +build !windows,!darwin

package process

func killProcessOnPort(port int) (bool, error) {
	return false, nil
}
