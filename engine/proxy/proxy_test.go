package proxy

import (
	"testing"
)

func TestProxyRoutingRules(t *testing.T) {
	// 1. isGBFDomain
	gbfHosts := []string{
		"game.granbluefantasy.jp",
		"gbf.game.mbga.jp",
		"connect.mobage.jp",
		"prd-game-a-granbluefantasy.akamaized.net",
		"localhost",
		"127.0.0.1",
	}
	for _, h := range gbfHosts {
		if !isGBFDomain(h) {
			t.Errorf("host %s must be recognized as GBF domain", h)
		}
	}

	nonGBFHosts := []string{
		"example.com",
		"google.com",
		"twitter.com",
	}
	for _, h := range nonGBFHosts {
		if isGBFDomain(h) {
			t.Errorf("host %s must NOT be recognized as GBF domain", h)
		}
	}

	// 2. isStaticTarget
	staticTargets := []struct {
		host string
		path string
	}{
		{"prd-game-a-granbluefantasy.akamaized.net", "/assets/img/hero.png"},
		{"game.granbluefantasy.jp", "/assets/1772717316/js/app.js"},
		{"game.granbluefantasy.jp", "/assets_en/css/style.css"},
		{"game.granbluefantasy.jp", "/sound/bgm.mp3"},
	}
	for _, target := range staticTargets {
		if !isStaticTarget(target.host, target.path) {
			t.Errorf("expected static target for %s%s", target.host, target.path)
		}
	}

	dynamicTargets := []struct {
		host string
		path string
	}{
		{"game.granbluefantasy.jp", "/rest/multiraid/start.json"},
		{"game.granbluefantasy.jp", "/quest/stage_list"},
		{"game.granbluefantasy.jp", "/party/deck_info"},
		{"game.granbluefantasy.jp", "/user/status"},
		{"game.granbluefantasy.jp", "/ob/r"},
	}
	for _, target := range dynamicTargets {
		if isStaticTarget(target.host, target.path) {
			t.Errorf("dynamic API %s%s must NOT be treated as static", target.host, target.path)
		}
	}

	// 3. isRetryableAPI
	retryable := []string{
		"/rest/multiraid/condition.json",
		"/rest/quest/stage_list",
		"/rest/party/deck_info",
	}
	for _, p := range retryable {
		if !isRetryableAPI(p) {
			t.Errorf("path %s must be in retryable whitelist", p)
		}
	}

	nonRetryable := []string{
		"/rest/multiraid/start.json",
		"/rest/raid/ability_result.json",
		"/rest/user/profile",
		"/ob/r",
	}
	for _, p := range nonRetryable {
		if isRetryableAPI(p) {
			t.Errorf("path %s must NOT be in retryable whitelist", p)
		}
	}

	// 4. matchETag
	if !matchETag("\"abc\"", "\"abc\"") {
		t.Error("identical ETags must match")
	}
	if !matchETag("W/\"abc\"", "\"abc\"") {
		t.Error("weak ETag must match strong ETag with same value")
	}
	if !matchETag("\"abc\"", "W/\"abc\"") {
		t.Error("strong ETag must match weak ETag with same value")
	}
	if matchETag("\"abc\"", "\"def\"") {
		t.Error("different ETags must not match")
	}

	// 5. isHopByHop
	hopHeaders := []string{"connection", "keep-alive", "transfer-encoding", "upgrade", "te"}
	for _, h := range hopHeaders {
		if !isHopByHop(h) {
			t.Errorf("header %s must be recognized as hop-by-hop", h)
		}
	}
	if isHopByHop("content-type") || isHopByHop("etag") || isHopByHop("cookie") {
		t.Error("standard headers must not be marked as hop-by-hop")
	}
}
