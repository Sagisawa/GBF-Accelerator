package cache

import (
	"bytes"
	"fmt"
	"testing"
)

func TestBoost_PrioritizesLatestVersionCoreAssetsUnderPartialBudget(t *testing.T) {
	base := t.TempDir()
	m1 := NewManager(base, 16)

	// Equal-sized 100-byte payloads across old and latest versions so budget fits exactly 2 items (220 bytes).
	pad100 := func(prefix []byte) []byte {
		if len(prefix) >= 100 {
			return prefix[:100]
		}
		return append(prefix, bytes.Repeat([]byte("a"), 100-len(prefix))...)
	}

	oldAudio := "/assets/1700000001/sound/old_bgm.mp3"
	oldImg := "/assets/1700000001/img/old_bg.png"
	midImg := "/assets/1750000001/img/mid_bg.png"
	newJS := "/assets/1795000001/js/core.js"
	newCSS := "/assets/1795000001/css/app.css"

	if !m1.Save(oldAudio, map[string]string{"content-type": "audio/mpeg"}, pad100([]byte("ID3"))) {
		t.Fatal("save oldAudio failed")
	}
	if !m1.Save(oldImg, map[string]string{"content-type": "image/png"}, makeFakePNG(100)) {
		t.Fatal("save oldImg failed")
	}
	if !m1.Save(midImg, map[string]string{"content-type": "image/png"}, makeFakePNG(100)) {
		t.Fatal("save midImg failed")
	}
	if !m1.Save(newJS, map[string]string{"content-type": "application/javascript"}, pad100([]byte("console.log('new');"))) {
		t.Fatal("save newJS failed")
	}
	if !m1.Save(newCSS, map[string]string{"content-type": "text/css"}, pad100([]byte("body{color:#fff}"))) {
		t.Fatal("save newCSS failed")
	}
	m1.Close() // persists .gbf_meta_index.v1

	// Reopen manager with tight budget (250 bytes -> holds 2 x 100-byte items)
	m2 := NewManager(base, 1)
	defer m2.Close()
	m2.RAMBudget().SetTotalBudget(250, m2.ramCache)
	m2.waitMetaIndexLoad(0)

	if !m2.metaIndexLoaded.Load() {
		t.Fatal("expected meta.idx loaded on restart")
	}

	progress := m2.PrewarmBoostPoolWithProgress(nil, nil, nil)
	if progress.State != BoostStatePartial {
		t.Fatalf("expected BoostStatePartial under tight budget, got %s", progress.State)
	}
	if progress.LoadedFiles != 2 {
		t.Fatalf("expected 2 loaded files under 250B budget, got %d", progress.LoadedFiles)
	}

	if _, ok := m2.ResidentPool().Get("assets/1795000001/js/core.js"); !ok {
		t.Fatal("expected latest-version core.js to be prioritized into ResidentPool")
	}
	if _, ok := m2.ResidentPool().Get("assets/1795000001/css/app.css"); !ok {
		t.Fatal("expected latest-version app.css to be prioritized into ResidentPool")
	}
	if _, ok := m2.ResidentPool().Get("assets/1700000001/sound/old_bgm.mp3"); ok {
		t.Fatal("expected obsolete-version old_bgm.mp3 NOT to crowd out core JS/CSS")
	}
}

func TestBoost_ConcurrentBatchLoadCompletesAllItemsViaMetaIndex(t *testing.T) {
	base := t.TempDir()
	m1 := NewManager(base, 32)

	const total = 80
	png := makeFakePNG(512)
	for i := 0; i < total; i++ {
		p := fmt.Sprintf("/assets/1795000001/img/sp/ui/icon_%03d.png", i)
		if !m1.Save(p, map[string]string{"content-type": "image/png"}, png) {
			t.Fatalf("save %s failed", p)
		}
	}
	m1.Close()

	m2 := NewManager(base, 32)
	defer m2.Close()
	m2.waitMetaIndexLoad(0)

	progress := m2.PrewarmBoostPoolWithProgress(nil, nil, nil)
	if progress.State != BoostStateCompleted {
		t.Fatalf("expected BoostStateCompleted, got %s (msg=%q)", progress.State, progress.Message)
	}
	if progress.LoadedFiles != total {
		t.Fatalf("expected %d loaded files, got %d", total, progress.LoadedFiles)
	}
	rItems, rBytes := m2.ResidentPool().Stats()
	if rItems != total || rBytes != int64(total*len(png)) {
		t.Fatalf("unexpected resident stats: items=%d bytes=%d", rItems, rBytes)
	}
}
