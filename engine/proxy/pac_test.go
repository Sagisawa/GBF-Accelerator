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

func TestGetLandingHTML(t *testing.T) {
	html := GetLandingHTML("192.168.1.50", 8124)

	expectedPhrases := []string{
		"第一步：安装并信任根证书",
		"第二步：配置手机 Wi-Fi 代理",
		"常见问题",
		"http://192.168.1.50:8124/ca.crt",
		"http://192.168.1.50:8124/proxy.pac",
		"game.granbluefantasy.jp",
		"SkyLeap",
		"GBF Local Accelerator Root CA",
		"8124",
	}

	for _, phrase := range expectedPhrases {
		if !strings.Contains(html, phrase) {
			t.Errorf("GetLandingHTML missing expected phrase: %q", phrase)
		}
	}
}
