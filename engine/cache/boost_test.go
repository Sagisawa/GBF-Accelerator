package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type mockFGWaiter struct {
	isIdle bool
	waited bool
}

func (m *mockFGWaiter) IsForegroundIdle() bool {
	return m.isIdle
}

func (m *mockFGWaiter) WaitForegroundIdle(stopChan <-chan struct{}) bool {
	m.waited = true
	m.isIdle = true
	return true
}

func TestBoost_FullPrewarmAndDisable(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_boost_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 16) // 16 MB budget
	defer mgr.Close()

	// Seed 5 valid PNG files on disk
	assetsDir := filepath.Join(tempDir, "assets", "lead")
	_ = os.MkdirAll(assetsDir, 0755)

	fileCount := 5
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRdata")
	for i := 0; i < fileCount; i++ {
		p := filepath.Join(assetsDir, fmt.Sprintf("hero_%d.png", i))
		_ = os.WriteFile(p, validPng, 0644)
		_ = os.WriteFile(p+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)
	}

	// 1. Run Prewarm
	progressEvents := 0
	finalProgress := mgr.PrewarmBoostPoolWithProgress(nil, func(p BoostProgress) {
		progressEvents++
	}, nil)

	if finalProgress.State != BoostStateCompleted {
		t.Fatalf("expected state %s, got %s", BoostStateCompleted, finalProgress.State)
	}
	if finalProgress.LoadedFiles != fileCount {
		t.Fatalf("expected %d loaded files, got %d", fileCount, finalProgress.LoadedFiles)
	}
	if !mgr.ResidentPool().Enabled() {
		t.Fatalf("expected resident pool to be enabled after boost")
	}

	// 2. Verify GetWithNamespace hits RAM-BOOST
	for i := 0; i < fileCount; i++ {
		urlPath := fmt.Sprintf("assets/lead/hero_%d.png", i)
		item, src := mgr.GetWithNamespace("gbf", urlPath)
		if item == nil || src != "RAM-BOOST" {
			t.Fatalf("expected item %s from RAM-BOOST, got item=%v, src=%s", urlPath, item, src)
		}
	}

	// 3. Disable Boost
	mgr.DisableBoost()

	if mgr.ResidentPool().Enabled() {
		t.Fatalf("expected resident pool to be disabled after DisableBoost")
	}
	rItems, rBytes := mgr.ResidentPool().Stats()
	if rItems != 0 || rBytes != 0 {
		t.Fatalf("expected 0 resident stats after DisableBoost, got (%d, %d)", rItems, rBytes)
	}

	// 4. Subsequent GetWithNamespace should hit normal SLRU (RAM) or DISK, not RAM-BOOST
	item, src := mgr.GetWithNamespace("gbf", "assets/lead/hero_0.png")
	if item == nil || src == "RAM-BOOST" {
		t.Fatalf("expected non-RAM-BOOST hit after disable, got src=%s", src)
	}
}

func TestBoost_Cancellation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_boost_cancel_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	assetsDir := filepath.Join(tempDir, "assets", "cancel")
	_ = os.MkdirAll(assetsDir, 0755)

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRdata")
	for i := 0; i < 20; i++ {
		p := filepath.Join(assetsDir, fmt.Sprintf("item_%d.png", i))
		_ = os.WriteFile(p, validPng, 0644)
		_ = os.WriteFile(p+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)
	}

	cancelCh := make(chan struct{})
	close(cancelCh) // Trigger immediate cancellation

	finalProgress := mgr.PrewarmBoostPoolWithProgress(nil, nil, cancelCh)
	if finalProgress.State != BoostStateCancelled {
		t.Fatalf("expected state %s, got %s", BoostStateCancelled, finalProgress.State)
	}
}

func TestBoost_ForegroundYielding(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_boost_fg_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	assetsDir := filepath.Join(tempDir, "assets", "fg")
	_ = os.MkdirAll(assetsDir, 0755)

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRdata")
	p := filepath.Join(assetsDir, "item_0.png")
	_ = os.WriteFile(p, validPng, 0644)
	_ = os.WriteFile(p+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)

	mock := &mockFGWaiter{isIdle: false}
	finalProgress := mgr.PrewarmBoostPoolWithProgress(mock, nil, nil)

	if !mock.waited {
		t.Fatalf("expected PrewarmBoostPoolWithProgress to wait for foreground idle")
	}
	if finalProgress.State != BoostStateCompleted {
		t.Fatalf("expected state %s, got %s", BoostStateCompleted, finalProgress.State)
	}
}

func TestBoost_BudgetPartial(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_boost_partial_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Construct manager with tiny budget of 50 bytes
	mgr := NewManager(tempDir, 1)
	// Force budget total to 50 bytes
	mgr.RAMBudget().SetTotalBudget(50, mgr.ramCache)
	defer mgr.Close()

	assetsDir := filepath.Join(tempDir, "assets", "partial")
	_ = os.MkdirAll(assetsDir, 0755)

	// Each item is 24 bytes
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRdata")
	for i := 0; i < 5; i++ {
		p := filepath.Join(assetsDir, fmt.Sprintf("item_%d.png", i))
		_ = os.WriteFile(p, validPng, 0644)
		_ = os.WriteFile(p+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)
	}

	finalProgress := mgr.PrewarmBoostPoolWithProgress(nil, nil, nil)
	if finalProgress.State != BoostStatePartial {
		t.Fatalf("expected state %s due to budget overflow, got %s", BoostStatePartial, finalProgress.State)
	}
	if finalProgress.LoadedFiles >= 5 {
		t.Fatalf("expected only partial loading, got all %d files", finalProgress.LoadedFiles)
	}
}

func TestBoost_ResumePrewarmNoDoubleBudget(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_boost_resume_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Manager with 500 bytes budget
	mgr := NewManager(tempDir, 1)
	mgr.RAMBudget().SetTotalBudget(500, mgr.ramCache)
	defer mgr.Close()

	assetsDir := filepath.Join(tempDir, "assets", "resume")
	_ = os.MkdirAll(assetsDir, 0755)

	// 4 files of 100 bytes each (total 400 bytes, fits in 500 bytes budget)
	validPngHeader := "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"
	for i := 0; i < 4; i++ {
		padding := string(make([]byte, 100-len(validPngHeader)))
		data := []byte(validPngHeader + padding)
		p := filepath.Join(assetsDir, fmt.Sprintf("item_%d.png", i))
		_ = os.WriteFile(p, data, 0644)
		_ = os.WriteFile(p+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)
	}

	// First run: cancel after 2 files
	cancelCh := make(chan struct{})
	loadedCount := 0
	_ = mgr.PrewarmBoostPoolWithProgress(nil, func(p BoostProgress) {
		if p.LoadedFiles >= 2 && loadedCount < 2 {
			loadedCount = p.LoadedFiles
			close(cancelCh)
		}
	}, cancelCh)

	// Verify partial state
	rItemsFirst, rBytesFirst := mgr.ResidentPool().Stats()
	if rItemsFirst == 0 || rBytesFirst == 0 {
		t.Fatalf("expected some resident items after cancel, got items=%d, bytes=%d", rItemsFirst, rBytesFirst)
	}
	if mgr.RAMBudget().ResidentBytes() != rBytesFirst {
		t.Fatalf("expected budget resident bytes %d, got %d", rBytesFirst, mgr.RAMBudget().ResidentBytes())
	}

	// Second run (Resume): should complete without double-charging budget
	finalProgress := mgr.PrewarmBoostPoolWithProgress(nil, nil, nil)
	if finalProgress.State != BoostStateCompleted {
		t.Fatalf("expected completed state on resume, got %s (loaded %d/4)", finalProgress.State, finalProgress.LoadedFiles)
	}
	if finalProgress.LoadedFiles != 4 {
		t.Fatalf("expected 4 loaded files, got %d", finalProgress.LoadedFiles)
	}

	rItemsFinal, rBytesFinal := mgr.ResidentPool().Stats()
	if rItemsFinal != 4 || rBytesFinal != 400 {
		t.Fatalf("expected 4 items and 400 bytes, got items=%d, bytes=%d", rItemsFinal, rBytesFinal)
	}

	// Budget must accurately match ResidentPool bytes without double-counting
	if mgr.RAMBudget().ResidentBytes() != 400 {
		t.Fatalf("expected budget resident bytes 400, got %d (double counting detected)", mgr.RAMBudget().ResidentBytes())
	}
}

func TestBoost_CorruptedFileSkipsAndCompletes(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_boost_corrupt_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	assetsDir := filepath.Join(tempDir, "assets", "corrupt")
	_ = os.MkdirAll(assetsDir, 0755)

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRgood")
	for i := 0; i < 3; i++ {
		p := filepath.Join(assetsDir, fmt.Sprintf("clean_%d.png", i))
		_ = os.WriteFile(p, validPng, 0644)
		_ = os.WriteFile(p+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)
	}

	// Add 1 corrupted file (HTML error page pretending to be png)
	badFile := filepath.Join(assetsDir, "corrupted.png")
	_ = os.WriteFile(badFile, []byte("<html><body>502 Bad Gateway</body></html>"), 0644)
	_ = os.WriteFile(badFile+".ext", []byte(`{"ContentType":"text/html","v":1}`), 0644)

	finalProgress := mgr.PrewarmBoostPoolWithProgress(nil, nil, nil)
	if finalProgress.State != BoostStateCompleted {
		t.Fatalf("expected state %s for corrupt file skip, got %s", BoostStateCompleted, finalProgress.State)
	}
	if finalProgress.LoadedFiles != 3 {
		t.Fatalf("expected 3 clean files loaded, got %d", finalProgress.LoadedFiles)
	}

	// Corrupt file must have been removed from disk by auto-repair
	if _, err := os.Stat(badFile); !os.IsNotExist(err) {
		t.Fatalf("expected corrupt file to be removed from disk by auto-repair")
	}
}

func TestBoost_DisableDuringPrewarmRace(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_boost_disablerace_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	assetsDir := filepath.Join(tempDir, "assets", "disablerace")
	_ = os.MkdirAll(assetsDir, 0755)

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRrace")
	for i := 0; i < 30; i++ {
		p := filepath.Join(assetsDir, fmt.Sprintf("item_%d.png", i))
		_ = os.WriteFile(p, validPng, 0644)
		_ = os.WriteFile(p+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)
	}

	cancelCh := make(chan struct{})
	doneCh := make(chan BoostProgress, 1)

	go func() {
		res := mgr.PrewarmBoostPoolWithProgress(nil, func(p BoostProgress) {
			if p.LoadedFiles >= 2 {
				mgr.DisableBoost()
			}
		}, cancelCh)
		doneCh <- res
	}()

	res := <-doneCh
	if res.State != BoostStateDisabled && res.State != BoostStateCancelled {
		t.Logf("final state: %s", res.State)
	}

	if mgr.ResidentPool().Enabled() {
		t.Fatalf("expected ResidentPool to be disabled")
	}
	rItems, rBytes := mgr.ResidentPool().Stats()
	if rItems != 0 || rBytes != 0 {
		t.Fatalf("expected 0 items/bytes in pool after disable, got (%d, %d)", rItems, rBytes)
	}
	if mgr.RAMBudget().ResidentBytes() != 0 {
		t.Fatalf("expected 0 resident bytes in budget coordinator, got %d", mgr.RAMBudget().ResidentBytes())
	}
}

