package proxy

import (
	"strings"
	"testing"
)

func TestGetPACDoesNotUseOverBroadCDNWildcards(t *testing.T) {
	pac := GetPAC("127.0.0.1", 8124)

	for _, pattern := range []string{
		`shExpMatch(host, "*granbluefantasy.akamaized.net")`,
		`shExpMatch(host, "*granbluefantasy-steam.akamaized.net")`,
		`shExpMatch(host, "*gbf.akamaized.net")`,
	} {
		if strings.Contains(pac, pattern) {
			t.Fatalf("PAC contains over-broad CDN wildcard: %s", pattern)
		}
	}

	for _, pattern := range []string{
		`shExpMatch(host, "granbluefantasy.akamaized.net")`,
		`shExpMatch(host, "*.granbluefantasy.akamaized.net")`,
		`shExpMatch(host, "gbf.akamaized.net")`,
		`shExpMatch(host, "*.game.mbga.jp")`,
	} {
		if !strings.Contains(pac, pattern) {
			t.Fatalf("PAC lost expected explicit domain rule: %s", pattern)
		}
	}
}
