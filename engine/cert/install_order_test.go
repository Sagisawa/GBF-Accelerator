package cert

import (
	"errors"
	"strings"
	"testing"
)

// Command-level names recorded by the fake below. Each maps 1:1 to a real `security` CLI
// invocation, so the recorded sequence is the actual command order the installer would produce.
const (
	cmdImportCert = "security add-trusted-cert"
	cmdVerifyCert = "security verify-cert/dump-trust-settings"
	cmdDeleteCert = "security delete-certificate"
)

type fakeInstallSteps struct {
	calls         []string
	importErr     error
	trustVerified bool
}

func (f *fakeInstallSteps) steps() caInstallSteps {
	return caInstallSteps{
		importCert: func() error {
			f.calls = append(f.calls, cmdImportCert)
			return f.importErr
		},
		trustVerified: func() bool {
			f.calls = append(f.calls, cmdVerifyCert)
			return f.trustVerified
		},
		removeStale: func(keepSHA1 string) {
			f.calls = append(f.calls, cmdDeleteCert+":"+keepSHA1)
		},
	}
}

func indexOfCall(calls []string, prefix string) int {
	for i, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func (f *fakeInstallSteps) deleteCalls() []string {
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, cmdDeleteCert) {
			out = append(out, c)
		}
	}
	return out
}

// TestInstallCAInOrder_ImportsAndVerifiesBeforeDeleting verifies the required ordering: the new
// certificate is imported, its trust is confirmed, and only then are stale duplicates removed —
// while always keeping the fingerprint of the certificate that was just verified.
func TestInstallCAInOrder_ImportsAndVerifiesBeforeDeleting(t *testing.T) {
	const currentSHA1 = "BF49FCFF11223344556677889900AABBCCDDEEFF"
	fake := &fakeInstallSteps{trustVerified: true}

	if err := installCAInOrder(currentSHA1, fake.steps()); err != nil {
		t.Fatalf("expected install to succeed, got error: %v", err)
	}

	want := []string{
		cmdImportCert,
		cmdVerifyCert,
		cmdDeleteCert + ":" + currentSHA1,
	}
	if len(fake.calls) != len(want) {
		t.Fatalf("unexpected command sequence: got %v, want %v", fake.calls, want)
	}
	for i := range want {
		if fake.calls[i] != want[i] {
			t.Fatalf("unexpected command sequence: got %v, want %v", fake.calls, want)
		}
	}

	// The certificate that was just verified must be preserved by the cleanup step.
	if keep := fake.deleteCalls(); len(keep) != 1 || !strings.HasSuffix(keep[0], ":"+currentSHA1) {
		t.Fatalf("cleanup must keep the verified fingerprint %s, got %v", currentSHA1, keep)
	}
}

// TestInstallCAInOrder_ImportFailureKeepsExistingCertificates verifies that a failed import
// deletes nothing, so a previously trusted CA stays usable.
func TestInstallCAInOrder_ImportFailureKeepsExistingCertificates(t *testing.T) {
	const currentSHA1 = "BF49FCFF11223344556677889900AABBCCDDEEFF"
	importErr := errors.New("security add-trusted-cert failed: user canceled")
	fake := &fakeInstallSteps{importErr: importErr, trustVerified: true}

	err := installCAInOrder(currentSHA1, fake.steps())
	if err == nil {
		t.Fatal("expected install to fail when the import fails")
	}
	if !errors.Is(err, importErr) {
		t.Fatalf("expected the import error to be returned unchanged, got %v", err)
	}
	if deletes := fake.deleteCalls(); len(deletes) != 0 {
		t.Fatalf("import failure must not delete any certificate, got %v", deletes)
	}
	if indexOfCall(fake.calls, cmdVerifyCert) != -1 {
		t.Fatalf("trust verification must not run after a failed import, got %v", fake.calls)
	}
}

// TestInstallCAInOrder_VerificationFailureKeepsExistingCertificates verifies that when the import
// succeeds but trust cannot be confirmed, no certificate is deleted and the original valid CA
// remains available.
func TestInstallCAInOrder_VerificationFailureKeepsExistingCertificates(t *testing.T) {
	const currentSHA1 = "BF49FCFF11223344556677889900AABBCCDDEEFF"
	fake := &fakeInstallSteps{trustVerified: false}

	err := installCAInOrder(currentSHA1, fake.steps())
	if err == nil {
		t.Fatal("expected install to fail when trust verification fails")
	}
	if deletes := fake.deleteCalls(); len(deletes) != 0 {
		t.Fatalf("verification failure must not delete any certificate, got %v", deletes)
	}

	importIdx := indexOfCall(fake.calls, cmdImportCert)
	verifyIdx := indexOfCall(fake.calls, cmdVerifyCert)
	if importIdx == -1 || verifyIdx == -1 || verifyIdx < importIdx {
		t.Fatalf("trust must be verified after the import, got %v", fake.calls)
	}
}

// TestInstallCAInOrder_UnknownFingerprintSkipsCleanup verifies that when the fingerprint of the
// new certificate cannot be determined, cleanup is skipped entirely rather than deleting
// certificates that cannot be identified as stale.
func TestInstallCAInOrder_UnknownFingerprintSkipsCleanup(t *testing.T) {
	fake := &fakeInstallSteps{trustVerified: true}

	if err := installCAInOrder("", fake.steps()); err != nil {
		t.Fatalf("expected install to succeed, got error: %v", err)
	}
	if deletes := fake.deleteCalls(); len(deletes) != 0 {
		t.Fatalf("unknown fingerprint must skip cleanup entirely, got %v", deletes)
	}
	if indexOfCall(fake.calls, cmdImportCert) != 0 {
		t.Fatalf("import must still happen first, got %v", fake.calls)
	}
}
