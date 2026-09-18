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

	// 6. isPassthroughHost: ws.game.granbluefantasy.jp must be passthrough and NOT MITM'd
	if !isPassthroughHost("ws.game.granbluefantasy.jp") {
		t.Error("ws.game.granbluefantasy.jp must be recognized as passthrough host")
	}
	if isGBFDomain("ws.game.granbluefantasy.jp") {
		t.Error("ws.game.granbluefantasy.jp must NOT be in MITM isGBFDomain")
	}

	// 7. isTelemetryHost: ad & tracking domains must be blocked
	telemetryHosts := []string{"smbeat.jp", "smrtbeat.com", "datadoghq.com", "rcv.a-i-ad.com"}
	for _, th := range telemetryHosts {
		if !isTelemetryHost(th) {
			t.Errorf("%s must be detected as telemetry domain", th)
		}
	}
	if isTelemetryHost("game.granbluefantasy.jp") {
		t.Error("game.granbluefantasy.jp must NOT be marked as telemetry domain")
	}

	// 8. isGBFAkamaiHost & isGBFDomain: only legitimate GBF domains are MITM'd
	if !isGBFAkamaiHost("prd-game-a-granbluefantasy.akamaized.net") {
		t.Error("prd-game-a-granbluefantasy.akamaized.net must be recognized as GBF Akamai host")
	}
	if !isGBFAkamaiHost("prd-game-a1-granbluefantasy-steam.akamaized.net") {
		t.Error("steam Akamai host must be recognized as GBF Akamai host")
	}
	if isGBFAkamaiHost("unrelated-tenant.akamaized.net") {
		t.Error("unrelated Akamai tenants must NOT be recognized as GBF Akamai host")
	}
	if isGBFDomain("fakegranbluefantasy.jp") {
		t.Error("fakegranbluefantasy.jp must NOT be recognized as GBF domain")
	}
	if !isGBFDomain("game.granbluefantasy.jp") {
		t.Error("game.granbluefantasy.jp must be recognized as GBF domain")
	}

	// 9. isStaticTarget vs dynamic APIs
	if !isStaticTarget("game.granbluefantasy.jp", "/sound/se/se_100.ogg") {
		t.Error("/sound/se/se_100.ogg must be recognized as static")
	}
	if !isStaticTarget("game.granbluefantasy.jp", "/img/sp/ui/btn.png") {
		t.Error("/img/sp/ui/btn.png must be recognized as static")
	}
	if isStaticTarget("game.granbluefantasy.jp", "/rest/multiraid/condition.json") {
		t.Error("/rest/... must NOT be recognized as static")
	}
	if isStaticTarget("game.granbluefantasy.jp", "/present/receive.json") {
		t.Error("/present/... must NOT be recognized as static")
	}
	if isStaticTarget("game.granbluefantasy.jp", "/guild/info.json") {
		t.Error("/guild/... must NOT be recognized as static")
	}
}

func TestPrefetchExtraction(t *testing.T) {
	pe := &PrefetchEngine{}
	sampleJS := `
		var img1 = "assets/img/sp/quest/scene/character/body/3040001000.png";
		var sound1 = '/sound/se/se_100.mp3';
		var cjs = "sp/cjs/npc_3040001000.png";
	`
	refs := pe.extractAssetRefs("/assets/js/bundle.js", sampleJS, "prd-game-a-granbluefantasy.akamaized.net")
	if len(refs) < 2 {
		t.Fatalf("expected at least 2 extracted refs, got %d", len(refs))
	}

	// Twin CreateJS deduction test
	twinManifestPath := "/assets/123456/js/model/manifest/npc_3040001000.js"
	twinRefs := pe.extractAssetRefs(twinManifestPath, "{}", "prd-game-a-granbluefantasy.akamaized.net")
	foundTwin := false
	for _, r := range twinRefs {
		if r[1] == "/assets/123456/js/cjs/npc_3040001000.js" {
			foundTwin = true
			break
		}
	}
	if !foundTwin {
		t.Error("expected twin CreateJS script to be deduced from model manifest")
	}

	// English asset prefix test
	enRefs := pe.extractAssetRefs("/assets_en/123456/js/manifest.js", `var s = "sp/cjs/tex.png";`, "prd-game-a-granbluefantasy.akamaized.net")
	foundEn := false
	for _, r := range enRefs {
		if r[1] == "/assets_en/img/sp/cjs/tex.png" {
			foundEn = true
			break
		}
	}
	if !foundEn {
		t.Errorf("expected /assets_en/img prefix for english assets, got: %v", enRefs)
	}

	prioJS := getPrefetchPriority("/assets/js/bundle.js")
	if prioJS != 1 {
		t.Errorf("expected prio 1 for JS, got %d", prioJS)
	}
	prioImg := getPrefetchPriority("/assets/img/hero.png")
	if prioImg != 2 {
		t.Errorf("expected prio 2 for PNG, got %d", prioImg)
	}
	prioSound := getPrefetchPriority("/sound/bgm.mp3")
	if prioSound != 3 {
		t.Errorf("expected prio 3 for MP3, got %d", prioSound)
	}
}
