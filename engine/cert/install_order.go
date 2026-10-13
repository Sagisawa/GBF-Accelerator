package cert

import "fmt"

// caInstallSteps holds the side-effecting trust-store operations of a Root CA install. Each
// field maps to a distinct `security` CLI command on macOS:
//
//	importCert    -> security add-trusted-cert
//	trustVerified -> security find-certificate / verify-cert / dump-trust-settings
//	removeStale   -> security delete-certificate
//
// The steps are injected so the install ordering can be unit-tested on every platform without
// touching a real keychain.
type caInstallSteps struct {
	// importCert installs the CA certificate into the trust store. The returned error already
	// carries any command diagnostics.
	importCert func() error
	// trustVerified reports whether the freshly imported certificate is actually trusted.
	trustVerified func() bool
	// removeStale removes stale or duplicate certificates that share the Root CA name, keeping
	// the certificate identified by keepSHA1.
	removeStale func(keepSHA1 string)
}

// installCAInOrder runs the CA install steps so that an existing, working trust chain survives
// until its replacement is in place.
//
// An existing CA must stay usable until the new certificate is both imported and trusted:
// otherwise a cancelled authorization prompt, a locked keychain or a failed import would leave
// the machine without any working trust chain. Cleanup is therefore the last step, it only runs
// after verification succeeded, and it always keeps the verified fingerprint.
//
// When the fingerprint of the new certificate cannot be determined, cleanup is skipped entirely
// rather than removing certificates that cannot be identified as stale.
//
// This file deliberately carries no build tag: the ordering guarantee is the part worth testing,
// and CI runs on Linux. It is currently only wired up by the macOS installer (install_darwin.go).
func installCAInOrder(currentSHA1 string, steps caInstallSteps) error {
	// Import first: the previously trusted CA must remain untouched until the replacement is in.
	if err := steps.importCert(); err != nil {
		return err
	}

	if currentSHA1 == "" {
		return nil
	}

	// Confirm the new certificate is actually trusted before removing anything.
	if !steps.trustVerified() {
		return fmt.Errorf("certificate was imported into keychain but trust verification failed; please verify trust settings in Keychain Access")
	}

	// CA Hygiene: only now remove stale/duplicate or 0-trust certificates sharing the CA name,
	// always keeping the certificate that was just imported and verified.
	steps.removeStale(currentSHA1)

	return nil
}
