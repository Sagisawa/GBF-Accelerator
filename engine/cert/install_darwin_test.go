//go:build darwin
// +build darwin

package cert

import (
	"testing"
)

func TestParseSHA1sFromOutput(t *testing.T) {
	sampleOutput := `
keychain: "/Users/test/Library/Keychains/login.keychain-db"
version: 256
class: 0x80001000
attributes:
    "labl"<blob>="GBF Local Accelerator Root CA"
SHA-1 hash: BF49FCFF11223344556677889900AABBCCDDEEFF
keychain: "/Users/test/Library/Keychains/login.keychain-db"
version: 256
class: 0x80001000
attributes:
    "labl"<blob>="GBF Local Accelerator Root CA"
SHA-1 hash: 65BF4F3A11223344556677889900AABBCCDDEEFF
`
	shas := parseSHA1sFromOutput(sampleOutput)
	if len(shas) != 2 {
		t.Fatalf("expected 2 SHAs, got %d", len(shas))
	}
	if shas[0] != "BF49FCFF11223344556677889900AABBCCDDEEFF" {
		t.Errorf("unexpected sha[0]: %s", shas[0])
	}
	if shas[1] != "65BF4F3A11223344556677889900AABBCCDDEEFF" {
		t.Errorf("unexpected sha[1]: %s", shas[1])
	}
}

func TestParseTrustSettingsOutput(t *testing.T) {
	// Case 1: 0 trust settings (the false positive bug report case)
	zeroTrust := `
Cert 0: GBF Local Accelerator Root CA
   Number of trust settings : 0
`
	if parseTrustSettingsOutput(zeroTrust, rootCAName) {
		t.Errorf("expected 0 trust settings to return false, got true")
	}

	// Case 2: TrustRoot configured (valid trusted case)
	trusted := `
Cert 0: GBF Local Accelerator Root CA
   Number of trust settings : 1
   Trust Setting 0:
      Policy OID : 1.2.840.113635.100.1.7 (Code Signing)
      Allowed Error : None
      Result : kSecTrustSettingsResultTrustRoot
`
	if !parseTrustSettingsOutput(trusted, rootCAName) {
		t.Errorf("expected trusted cert to return true, got false")
	}

	// Case 3: Other cert trusted, target cert has 0 trust settings
	mixed := `
Cert 0: Apple Root CA
   Number of trust settings : 2
Cert 1: GBF Local Accelerator Root CA
   Number of trust settings : 0
`
	if parseTrustSettingsOutput(mixed, rootCAName) {
		t.Errorf("expected mixed output to return false for target, got true")
	}

	// Case 4: Target cert trusted among others
	mixedTrusted := `
Cert 0: Apple Root CA
   Number of trust settings : 2
Cert 1: GBF Local Accelerator Root CA
   Number of trust settings : 1
   Trust Setting 0:
      Result : kSecTrustSettingsResultTrustAsRoot
`
	if !parseTrustSettingsOutput(mixedTrusted, rootCAName) {
		t.Errorf("expected mixedTrusted output to return true for target, got false")
	}
}
