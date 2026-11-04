//go:build darwin

package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func setMigrationTargetForTest(t *testing.T, path string) {
	t.Helper()
	previous := standardCacheDirProvider
	standardCacheDirProvider = func() string { return path }
	t.Cleanup(func() { standardCacheDirProvider = previous })
}

func TestValidateCacheMigrationTreeRejectsUnconfirmedFiles(t *testing.T) {
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := cache.EnsureRootMarker(root); err != nil {
		t.Fatal(err)
	}

	asset := filepath.Join(root, "assets", "img", "sp", "ui", "known.png")
	if err := os.MkdirAll(filepath.Dir(asset), 0755); err != nil {
		t.Fatal(err)
	}
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRmigration")
	if err := os.WriteFile(asset, png, 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheMigrationTree(root); err != nil {
		t.Fatalf("confirmed Tier-2 source tree should pass: %v", err)
	}

	userFile := filepath.Join(root, "personal_data", "profile.png")
	if err := os.MkdirAll(filepath.Dir(userFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userFile, png, 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheMigrationTree(root); err == nil {
		t.Fatal("migration must reject a user file in an unrecognized directory")
	}
	if err := os.RemoveAll(filepath.Join(root, "personal_data")); err != nil {
		t.Fatal(err)
	}

	orphan := filepath.Join(root, "assets", "img", "sp", "ui", "orphan.png.ext")
	if err := os.WriteFile(orphan, []byte(`{"v":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheMigrationTree(root); err != nil {
		t.Fatalf("migration must allow orphan .ext sidecar without blocking: %v", err)
	}
	if err := os.Remove(orphan); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(root, "assets", "img", "sp", "ui", "linked.png")
	if err := os.Symlink(asset, link); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheMigrationTree(root); err == nil {
		t.Fatal("migration must reject symbolic links")
	}
}

func TestValidateEmptyMigrationTargetRejectsAnyExistingEntry(t *testing.T) {
	target := t.TempDir()
	if err := validateEmptyMigrationTarget(target); err != nil {
		t.Fatalf("empty target should pass: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, ".DS_Store"), []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateEmptyMigrationTarget(target); err == nil {
		t.Fatal("migration must not replace an existing target containing even system metadata")
	}
}

func TestValidateCacheMigrationTree_Over50FilesBoundary(t *testing.T) {
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := cache.EnsureRootMarker(root); err != nil {
		t.Fatal(err)
	}

	pngMagic := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, 0x01}
	uiDir := filepath.Join(root, "assets", "img", "sp", "ui")
	if err := os.MkdirAll(uiDir, 0755); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 60; i++ {
		p := filepath.Join(uiDir, fmt.Sprintf("item_%02d.png", i))
		if err := os.WriteFile(p, pngMagic, 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := validateCacheMigrationTree(root); err != nil {
		t.Fatalf("expected 60 valid legacy files to pass migration validation: %v", err)
	}

	tempFile := filepath.Join(uiDir, "item_temp.png.tmp.1234.1700000000.1")
	quarFile := filepath.Join(uiDir, "item_quar.png.quarantine.1700000000")
	orphanExt := filepath.Join(uiDir, "item_orphan.png.ext")
	for _, f := range []string{tempFile, quarFile, orphanExt} {
		if err := os.WriteFile(f, []byte("junk"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateCacheMigrationTree(root); err != nil {
		t.Fatalf("expected residual temp, quarantine, and orphan .ext not to block migration: %v", err)
	}
	for _, f := range []string{tempFile, quarFile, orphanExt} {
		_ = os.Remove(f)
	}

	p55 := filepath.Join(uiDir, "item_55.png")
	p55Docx := filepath.Join(uiDir, "item_55.docx")
	if err := os.Rename(p55, p55Docx); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheMigrationTree(root); err == nil {
		t.Fatal("expected migration validation to fail when 55th file has an invalid asset extension")
	}
	if err := os.Rename(p55Docx, p55); err != nil {
		t.Fatal(err)
	}

	personalDir := filepath.Join(root, "personal_notes")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatal(err)
	}
	p55Personal := filepath.Join(personalDir, "item_55.png")
	if err := os.Rename(p55, p55Personal); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheMigrationTree(root); err == nil {
		t.Fatal("expected migration validation to fail when 55th file is in an unconfirmed directory")
	}
	if err := os.Rename(p55Personal, p55); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(personalDir)

	if err := os.WriteFile(p55, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheMigrationTree(root); err == nil {
		t.Fatal("expected migration validation to fail when 55th file is empty")
	}
	if err := os.WriteFile(p55, pngMagic, 0644); err != nil {
		t.Fatal(err)
	}

	if err := validateCacheMigrationTree(root); err != nil {
		t.Fatalf("expected restored 60 files to pass: %v", err)
	}
}

func TestValidateCacheOverwriteTarget_StrictAllFilesNoSampling(t *testing.T) {
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := cache.EnsureRootMarker(root); err != nil {
		t.Fatal(err)
	}

	pngMagic := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, 0x01}
	uiDir := filepath.Join(root, "assets", "img", "sp", "ui")
	if err := os.MkdirAll(uiDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create 60 valid legacy files
	for i := 1; i <= 60; i++ {
		p := filepath.Join(uiDir, fmt.Sprintf("item_%02d.png", i))
		if err := os.WriteFile(p, pngMagic, 0644); err != nil {
			t.Fatal(err)
		}
	}

	// 1. All 60 valid legacy files must pass
	if err := validateCacheOverwriteTarget(root); err != nil {
		t.Fatalf("expected valid 60 files to pass overwrite validation: %v", err)
	}

	// 2. Corrupt the 55th file magic bytes: MUST FAIL (proving no 50-file cutoff!)
	p55 := filepath.Join(uiDir, "item_55.png")
	if err := os.WriteFile(p55, []byte("NOT_A_PNG_FILE_DATA_HERE"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheOverwriteTarget(root); err == nil {
		t.Fatal("expected overwrite target validation to FAIL on 55th file invalid magic (no sampling cutoff allowed)")
	}
	if err := os.WriteFile(p55, pngMagic, 0644); err != nil {
		t.Fatal(err)
	}

	// 3. System junk (.DS_Store): MUST FAIL in overwrite target
	dsStore := filepath.Join(root, ".DS_Store")
	if err := os.WriteFile(dsStore, []byte("junk"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheOverwriteTarget(root); err == nil {
		t.Fatal("expected overwrite target validation to FAIL when .DS_Store is present")
	}
	_ = os.Remove(dsStore)

	// 4. Orphan .ext: MUST FAIL in overwrite target
	orphanExt := filepath.Join(uiDir, "orphan.png.ext")
	if err := os.WriteFile(orphanExt, []byte(`{"v":1,"ns":"gbf"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheOverwriteTarget(root); err == nil {
		t.Fatal("expected overwrite target validation to FAIL when orphan .ext is present")
	}
	_ = os.Remove(orphanExt)

	// 5. Streaming temp file: MUST FAIL in overwrite target
	tempFile := filepath.Join(uiDir, "item_temp.png.tmp.123.456.789")
	if err := os.WriteFile(tempFile, []byte("temp"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheOverwriteTarget(root); err == nil {
		t.Fatal("expected overwrite target validation to FAIL when temp file is present")
	}
	_ = os.Remove(tempFile)

	// 6. Unknown personal file (notes.txt): MUST FAIL in overwrite target
	notesFile := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(notesFile, []byte("personal notes"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheOverwriteTarget(root); err == nil {
		t.Fatal("expected overwrite target validation to FAIL on notes.txt")
	}
	_ = os.Remove(notesFile)

	// 7. Paired Tier-1 file with mismatched namespace: MUST FAIL
	t1Asset := filepath.Join(root, "hosts", "prd-game-a-granbluefantasy.akamaized.net", "assets", "img", "t1.png")
	if err := os.MkdirAll(filepath.Dir(t1Asset), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(t1Asset, pngMagic, 0644); err != nil {
		t.Fatal(err)
	}
	t1Ext := t1Asset + ".ext"
	if err := os.WriteFile(t1Ext, []byte(`{"v":1,"ns":"malicious_other"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheOverwriteTarget(root); err == nil {
		t.Fatal("expected overwrite target validation to FAIL when namespace mismatches")
	}
	// Correct namespace to "gbf": MUST PASS
	if err := os.WriteFile(t1Ext, []byte(`{"v":1,"ns":"gbf"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheOverwriteTarget(root); err != nil {
		t.Fatalf("expected valid Tier-1 asset and .ext to pass overwrite validation: %v", err)
	}
}

func setupDarwinMigrationTestServer(t *testing.T, sourceDir, targetDir string) (*ControlServer, *config.Manager, *cache.Manager, string) {
	t.Helper()
	tempBase := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(tempBase); err == nil {
		tempBase = resolved
	}
	cfgPath := filepath.Join(tempBase, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	candidate := cfgMgr.Candidate()
	candidate.CacheDir = sourceDir
	if err := cfgMgr.Commit(candidate); err != nil {
		t.Fatal(err)
	}

	cacheMgr := cache.NewManager(sourceDir, 16)
	t.Cleanup(func() { cacheMgr.Close() })
	stats := telemetry.NewStats()

	setMigrationTargetForTest(t, targetDir)
	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)
	return ctrl, cfgMgr, cacheMgr, cfgPath
}

func TestHandleMigrateStandardCache_ModesAndCompat(t *testing.T) {
	sourceDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(sourceDir); err == nil {
		sourceDir = resolved
	}
	targetDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(targetDir); err == nil {
		targetDir = resolved
	}
	// Place a file in target so it is non-empty
	if err := os.WriteFile(filepath.Join(targetDir, "file.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	ctrl, _, _, _ := setupDarwinMigrationTestServer(t, sourceDir, targetDir)

	// 1. Nil body -> defaults to migrate, target is non-empty -> 409 CACHE_MIGRATION_TARGET_NOT_EMPTY
	reqNil := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", nil)
	recNil := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(recNil, reqNil)
	if recNil.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d", recNil.Code)
	}
	var resNil map[string]interface{}
	if err := json.Unmarshal(recNil.Body.Bytes(), &resNil); err != nil {
		t.Fatal(err)
	}
	if resNil["code"] != ErrCodeMigrationTargetNotEmpty {
		t.Fatalf("expected code %s, got %v", ErrCodeMigrationTargetNotEmpty, resNil["code"])
	}

	// 2. Empty JSON "{}" -> defaults to migrate, returns 409 CACHE_MIGRATION_TARGET_NOT_EMPTY
	reqEmpty := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader([]byte("{}")))
	recEmpty := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(recEmpty, reqEmpty)
	if recEmpty.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d", recEmpty.Code)
	}

	// 3. Unknown mode "teleport" -> 400 CACHE_MIGRATION_INVALID_MODE
	reqUnknown := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader([]byte(`{"mode":"teleport"}`)))
	recUnknown := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(recUnknown, reqUnknown)
	if recUnknown.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recUnknown.Code)
	}
	var resUnknown map[string]interface{}
	_ = json.Unmarshal(recUnknown.Body.Bytes(), &resUnknown)
	if resUnknown["code"] != ErrCodeMigrationInvalidMode {
		t.Fatalf("expected code %s, got %v", ErrCodeMigrationInvalidMode, resUnknown["code"])
	}

	// 4. Invalid JSON syntax -> 400 CACHE_MIGRATION_INVALID_MODE
	reqBadJSON := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader([]byte(`{not_json`)))
	recBadJSON := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(recBadJSON, reqBadJSON)
	if recBadJSON.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recBadJSON.Code)
	}

	// 5. Source already pointing to target -> 200 OK immediately
	ctrlSame, _, _, _ := setupDarwinMigrationTestServer(t, targetDir, targetDir)
	reqSame := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", nil)
	recSame := httptest.NewRecorder()
	ctrlSame.handleMigrateStandardCache(recSame, reqSame)
	if recSame.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recSame.Code)
	}
}

func TestHandleMigrateStandardCache_SwitchOnly(t *testing.T) {
	sourceDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(sourceDir); err == nil {
		sourceDir = resolved
	}
	targetDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(targetDir); err == nil {
		targetDir = resolved
	}

	// Target contains personal file notes.txt
	notesPath := filepath.Join(targetDir, "notes.txt")
	notesContent := []byte("hello user notes")
	if err := os.WriteFile(notesPath, notesContent, 0644); err != nil {
		t.Fatal(err)
	}

	ctrl, cfgMgr, cacheMgr, _ := setupDarwinMigrationTestServer(t, sourceDir, targetDir)

	// switch_only should succeed without touching or deleting notes.txt
	body := []byte(`{"mode":"switch_only"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var res map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res["mode"] != "switch_only" || res["ok"] != true {
		t.Fatalf("unexpected response: %v", res)
	}

	// Verify configuration committed
	if cfgMgr.Get().CacheDir != targetDir {
		t.Fatalf("expected config.CacheDir to be %s, got %s", targetDir, cfgMgr.Get().CacheDir)
	}

	// Verify cache runtime base updated
	if cacheMgr.GetCacheBase() != targetDir {
		t.Fatalf("expected runtime cacheBase to be %s, got %s", targetDir, cacheMgr.GetCacheBase())
	}

	// Verify notes.txt was preserved intact
	readBack, err := os.ReadFile(notesPath)
	if err != nil || !bytes.Equal(readBack, notesContent) {
		t.Fatalf("expected notes.txt to be preserved, err: %v, content: %s", err, string(readBack))
	}
}

func TestHandleMigrateStandardCache_OverwriteSafety(t *testing.T) {
	sourceBase := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(sourceBase); err == nil {
		sourceBase = resolved
	}
	sourceDir := filepath.Join(sourceBase, "source_cache")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := cache.EnsureRootMarker(sourceDir); err != nil {
		t.Fatal(err)
	}

	pngMagic := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, 0x01}
	srcAsset := filepath.Join(sourceDir, "assets", "img", "sp", "ui", "src_icon.png")
	if err := os.MkdirAll(filepath.Dir(srcAsset), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcAsset, pngMagic, 0644); err != nil {
		t.Fatal(err)
	}

	targetBase := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(targetBase); err == nil {
		targetBase = resolved
	}
	targetDir := filepath.Join(targetBase, "target_cache")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := cache.EnsureRootMarker(targetDir); err != nil {
		t.Fatal(err)
	}

	// 1. Target contains an unconfirmed user file (user.pdf): OVERWRITE MUST BE REJECTED
	userPdf := filepath.Join(targetDir, "user.pdf")
	if err := os.WriteFile(userPdf, []byte("pdf data"), 0644); err != nil {
		t.Fatal(err)
	}

	ctrl, cfgMgr, _, _ := setupDarwinMigrationTestServer(t, sourceDir, targetDir)

	body := []byte(`{"mode":"overwrite"}`)
	reqReject := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader(body))
	recReject := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(recReject, reqReject)

	if recReject.Code != http.StatusConflict {
		t.Fatalf("expected status 409 for unconfirmed target, got %d: %s", recReject.Code, recReject.Body.String())
	}
	var resReject map[string]interface{}
	_ = json.Unmarshal(recReject.Body.Bytes(), &resReject)
	if resReject["code"] != ErrCodeMigrationTargetUnconfirmed {
		t.Fatalf("expected code %s, got %v", ErrCodeMigrationTargetUnconfirmed, resReject["code"])
	}
	// user.pdf must remain untouched
	if _, err := os.Stat(userPdf); err != nil {
		t.Fatal("user.pdf must not be deleted on rejection")
	}
	_ = os.Remove(userPdf)

	// 2. Target contains ONLY verified GBF cache: OVERWRITE MUST SUCCEED
	oldTargetAsset := filepath.Join(targetDir, "assets", "img", "sp", "ui", "old_icon.png")
	if err := os.MkdirAll(filepath.Dir(oldTargetAsset), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldTargetAsset, pngMagic, 0644); err != nil {
		t.Fatal(err)
	}

	reqSuccess := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader(body))
	recSuccess := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(recSuccess, reqSuccess)

	if recSuccess.Code != http.StatusOK {
		t.Fatalf("expected status 200 for valid overwrite, got %d: %s", recSuccess.Code, recSuccess.Body.String())
	}

	// Verify target now contains src_icon.png from sourceDir
	migratedAsset := filepath.Join(targetDir, "assets", "img", "sp", "ui", "src_icon.png")
	if _, err := os.Stat(migratedAsset); err != nil {
		t.Fatalf("expected migrated asset %s to exist: %v", migratedAsset, err)
	}
	// Verify config updated
	if cfgMgr.Get().CacheDir != targetDir {
		t.Fatalf("expected config cache_dir to be %s, got %s", targetDir, cfgMgr.Get().CacheDir)
	}
	// Verify sourceDir was preserved and NOT deleted
	if _, err := os.Stat(srcAsset); err != nil {
		t.Fatalf("source asset must not be deleted: %v", err)
	}
}

func TestHandleMigrateStandardCache_ConcurrentSourceChange(t *testing.T) {
	sourceDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(sourceDir); err == nil {
		sourceDir = resolved
	}
	targetDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(targetDir); err == nil {
		targetDir = resolved
	}

	ctrl, cfgMgr, _, _ := setupDarwinMigrationTestServer(t, sourceDir, targetDir)

	otherDir := filepath.Join(sourceDir, "other")
	_ = os.MkdirAll(otherDir, 0755)

	// Simulate concurrent modification right before the apply transaction locks
	beforeMigrationApplyHook = func() {
		candidate := cfgMgr.Candidate()
		candidate.CacheDir = otherDir
		if err := cfgMgr.Commit(candidate); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { beforeMigrationApplyHook = nil })

	body := []byte(`{"mode":"switch_only"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res["code"] != ErrCodeMigrationSourceChanged {
		t.Fatalf("expected code %s, got %v", ErrCodeMigrationSourceChanged, res["code"])
	}
}

func TestHandleMigrateStandardCache_CommitFailureRollsBackDirectory(t *testing.T) {
	sourceBase := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(sourceBase); err == nil {
		sourceBase = resolved
	}
	sourceDir := filepath.Join(sourceBase, "source_cache")
	_ = os.MkdirAll(sourceDir, 0755)
	_ = cache.EnsureRootMarker(sourceDir)

	targetBase := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(targetBase); err == nil {
		targetBase = resolved
	}
	targetDir := filepath.Join(targetBase, "target_cache")
	_ = os.MkdirAll(targetDir, 0755)
	_ = cache.EnsureRootMarker(targetDir)

	pngMagic := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, 0x01}
	oldTargetAsset := filepath.Join(targetDir, "assets", "img", "sp", "ui", "old_marker.png")
	_ = os.MkdirAll(filepath.Dir(oldTargetAsset), 0755)
	_ = os.WriteFile(oldTargetAsset, pngMagic, 0644)

	srcAsset := filepath.Join(sourceDir, "assets", "img", "sp", "ui", "new_marker.png")
	_ = os.MkdirAll(filepath.Dir(srcAsset), 0755)
	_ = os.WriteFile(srcAsset, pngMagic, 0644)

	ctrl, _, _, cfgPath := setupDarwinMigrationTestServer(t, sourceDir, targetDir)

	// Simulate config commit failure right before commit
	beforeMigrationApplyHook = func() {
		// Break config file path by making it a directory
		_ = os.Remove(cfgPath)
		_ = os.Mkdir(cfgPath, 0755)
	}
	t.Cleanup(func() { beforeMigrationApplyHook = nil })

	body := []byte(`{"mode":"overwrite"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 on commit failure, got %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res["code"] != ErrCodeMigrationConfigCommitFailed {
		t.Fatalf("expected code %s, got %v", ErrCodeMigrationConfigCommitFailed, res["code"])
	}

	// Verify oldTargetAsset was restored!
	if _, err := os.Stat(oldTargetAsset); err != nil {
		t.Fatalf("expected oldTargetAsset to be restored after commit rollback: %v", err)
	}
	// Verify new_marker was NOT kept in targetDir
	if _, err := os.Stat(filepath.Join(targetDir, "assets", "img", "sp", "ui", "new_marker.png")); err == nil {
		t.Fatal("expected new marker to not be in target after rollback")
	}
}

func TestValidateCacheOverwriteTarget_Tier1PersonalNoteRegression(t *testing.T) {
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := cache.EnsureRootMarker(root); err != nil {
		t.Fatal(err)
	}

	notePath := filepath.Join(root, "assets", "img", "personal-note.txt")
	noteMeta := filepath.Join(root, "assets", "img", "personal-note.txt.ext")
	_ = os.MkdirAll(filepath.Dir(notePath), 0755)
	_ = os.WriteFile(notePath, []byte("This is my private note"), 0644)
	_ = os.WriteFile(noteMeta, []byte(`{"v":1,"ns":"gbf"}`), 0644)

	// personal-note.txt with valid .ext metadata MUST be rejected!
	if err := validateCacheOverwriteTarget(root); err == nil {
		t.Fatal("validateCacheOverwriteTarget must reject personal-note.txt even with valid Tier-1 .ext metadata")
	}

	// Clean up personal note
	_ = os.Remove(notePath)
	_ = os.Remove(noteMeta)

	// Legitimate GBF assets should pass
	assets := []struct {
		rel     string
		content []byte
	}{
		{"assets/img/sp/ui/icon.png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRok")},
		{"assets/js/bundle.js", []byte("console.log('game ready');")},
		{"assets/css/main.css", []byte("body { background: #000; }")},
		{"assets/sound/bgm.mp3", []byte("ID3\x03\x00\x00\x00\x00\x00\x0aTIT2\x00\x00\x01")},
		{"assets/font/font.woff2", []byte("wOF2\x00\x01\x00\x00\x00\x00\x00\x20")},
		{"assets/wasm/engine.wasm", []byte("\x00asm\x01\x00\x00\x00")},
	}

	for _, a := range assets {
		p := filepath.Join(root, filepath.FromSlash(a.rel))
		_ = os.MkdirAll(filepath.Dir(p), 0755)
		if err := os.WriteFile(p, a.content, 0644); err != nil {
			t.Fatal(err)
		}
		extP := p + ".ext"
		if err := os.WriteFile(extP, []byte(`{"v":1,"ns":"gbf"}`), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := validateCacheOverwriteTarget(root); err != nil {
		t.Fatalf("expected genuine GBF static assets to pass overwrite validation: %v", err)
	}

	// Test rejection of fake JS with binary nulls
	badJS := filepath.Join(root, "assets", "js", "bad.js")
	_ = os.WriteFile(badJS, []byte("\x7fELF\x00\x00bin"), 0644)
	_ = os.WriteFile(badJS+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)
	if err := validateCacheOverwriteTarget(root); err == nil {
		t.Fatal("expected validateCacheOverwriteTarget to reject JS containing binary null bytes")
	}
	_ = os.Remove(badJS)
	_ = os.Remove(badJS + ".ext")

	// Test rejection of error HTML page disguised as JS
	htmlJS := filepath.Join(root, "assets", "js", "html_error.js")
	_ = os.WriteFile(htmlJS, []byte("<!doctype html><html><body>502 Bad Gateway</body></html>"), 0644)
	_ = os.WriteFile(htmlJS+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)
	if err := validateCacheOverwriteTarget(root); err == nil {
		t.Fatal("expected validateCacheOverwriteTarget to reject HTML error content")
	}
	_ = os.Remove(htmlJS)
	_ = os.Remove(htmlJS + ".ext")

	// Test full-stream scanning of JS: first 512 bytes clean, NUL byte at offset 600
	streamJS := filepath.Join(root, "assets", "js", "stream_bad.js")
	streamData := bytes.Repeat([]byte("/* valid javascript comment padding */\n"), 20) // > 512 bytes
	streamData = append(streamData, 0x00)                                              // NUL byte beyond first 512 bytes
	streamData = append(streamData, []byte("console.log('end');")...)
	_ = os.WriteFile(streamJS, streamData, 0644)
	_ = os.WriteFile(streamJS+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)
	if err := validateCacheOverwriteTarget(root); err == nil {
		t.Fatal("expected validateCacheOverwriteTarget to reject JS with NUL byte beyond first 512 bytes")
	}
	_ = os.Remove(streamJS)
	_ = os.Remove(streamJS + ".ext")
}

func TestValidateCacheOverwriteTarget_SVGAndJSON(t *testing.T) {
	tempBase := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(tempBase); err == nil {
		tempBase = resolved
	}
	root := filepath.Join(tempBase, "cache_svg_json")
	_ = os.MkdirAll(root, 0755)
	_ = cache.EnsureRootMarker(root)

	// Subtest 1: Valid SVG formats
	validDirectSVG := filepath.Join(root, "assets", "img", "icon.svg")
	_ = os.MkdirAll(filepath.Dir(validDirectSVG), 0755)
	_ = os.WriteFile(validDirectSVG, []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="36" height="36"><circle cx="18" cy="18" r="10"/></svg>`), 0644)
	_ = os.WriteFile(validDirectSVG+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)

	validXmlSVG := filepath.Join(root, "assets", "img", "logo.svg")
	_ = os.WriteFile(validXmlSVG, []byte(`<?xml version="1.0" encoding="UTF-8"?><svg viewBox="0 0 100 100"><rect width="100" height="100"/></svg>`), 0644)
	_ = os.WriteFile(validXmlSVG+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)

	// Subtest 2: Valid JSON atlas format
	validJSON := filepath.Join(root, "assets", "img", "sp", "atlas", "raid_ui_6.json")
	_ = os.MkdirAll(filepath.Dir(validJSON), 0755)
	_ = os.WriteFile(validJSON, []byte(`{"images":["sp/atlas/raid_ui_6.png?v=0"],"frames":[[5,5,516,92],[527,5,440,40]]}`), 0644)
	_ = os.WriteFile(validJSON+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)

	if err := validateCacheOverwriteTarget(root); err != nil {
		t.Fatalf("expected valid SVG and JSON assets to pass validateCacheOverwriteTarget: %v", err)
	}

	// Subtest 3: Negative SVG - plain text disguised as SVG
	badTextSVG := filepath.Join(root, "assets", "img", "fake.svg")
	_ = os.WriteFile(badTextSVG, []byte("this is plain text not svg"), 0644)
	_ = os.WriteFile(badTextSVG+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)
	if err := validateCacheOverwriteTarget(root); err == nil {
		t.Fatal("expected validateCacheOverwriteTarget to reject plain text disguised as SVG")
	}
	_ = os.Remove(badTextSVG)
	_ = os.Remove(badTextSVG + ".ext")

	// Subtest 4: Negative SVG - non-SVG XML (e.g. plist)
	plistSVG := filepath.Join(root, "assets", "img", "plist.svg")
	_ = os.WriteFile(plistSVG, []byte(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist><plist version="1.0"><dict></dict></plist>`), 0644)
	_ = os.WriteFile(plistSVG+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)
	if err := validateCacheOverwriteTarget(root); err == nil {
		t.Fatal("expected validateCacheOverwriteTarget to reject non-SVG XML documents")
	}
	_ = os.Remove(plistSVG)
	_ = os.Remove(plistSVG + ".ext")

	// Subtest 5: Negative SVG - HTML error page
	htmlSVG := filepath.Join(root, "assets", "img", "html_error.svg")
	_ = os.WriteFile(htmlSVG, []byte("<!doctype html><html><body>502 Bad Gateway</body></html>"), 0644)
	_ = os.WriteFile(htmlSVG+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)
	if err := validateCacheOverwriteTarget(root); err == nil {
		t.Fatal("expected validateCacheOverwriteTarget to reject HTML error disguised as SVG")
	}
	_ = os.Remove(htmlSVG)
	_ = os.Remove(htmlSVG + ".ext")

	// Subtest 6: Negative JSON - malformed JSON syntax
	malformedJSON := filepath.Join(root, "assets", "img", "sp", "atlas", "broken.json")
	_ = os.WriteFile(malformedJSON, []byte(`{"images":["broken",`), 0644)
	_ = os.WriteFile(malformedJSON+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)
	if err := validateCacheOverwriteTarget(root); err == nil {
		t.Fatal("expected validateCacheOverwriteTarget to reject malformed JSON syntax")
	}
	_ = os.Remove(malformedJSON)
	_ = os.Remove(malformedJSON + ".ext")

	// Subtest 7: Negative JSON - plain text disguised as JSON
	textJSON := filepath.Join(root, "assets", "img", "sp", "atlas", "note.json")
	_ = os.WriteFile(textJSON, []byte("this is a text note with no json tokens"), 0644)
	_ = os.WriteFile(textJSON+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)
	if err := validateCacheOverwriteTarget(root); err == nil {
		t.Fatal("expected validateCacheOverwriteTarget to reject plain text disguised as JSON")
	}
	_ = os.Remove(textJSON)
	_ = os.Remove(textJSON + ".ext")

	// Subtest 8: Negative JSON - HTML error disguised as JSON
	htmlJSON := filepath.Join(root, "assets", "img", "sp", "atlas", "html_err.json")
	_ = os.WriteFile(htmlJSON, []byte("<html><body>503 Service Unavailable</body></html>"), 0644)
	_ = os.WriteFile(htmlJSON+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)
	if err := validateCacheOverwriteTarget(root); err == nil {
		t.Fatal("expected validateCacheOverwriteTarget to reject HTML error disguised as JSON")
	}
	_ = os.Remove(htmlJSON)
	_ = os.Remove(htmlJSON + ".ext")
}

func TestHandleMigrateStandardCache_MissingOrEmptySourceInOverwrite(t *testing.T) {
	// Subtest 1: Missing source directory
	t.Run("MissingSourceDirectory", func(t *testing.T) {
		tempBase := t.TempDir()
		if resolved, err := filepath.EvalSymlinks(tempBase); err == nil {
			tempBase = resolved
		}
		sourceDir := filepath.Join(tempBase, "non_existent_source")
		targetDir := filepath.Join(tempBase, "target_cache")
		_ = os.MkdirAll(targetDir, 0755)
		_ = cache.EnsureRootMarker(targetDir)
		targetFile := filepath.Join(targetDir, "assets", "img", "icon.png")
		_ = os.MkdirAll(filepath.Dir(targetFile), 0755)
		_ = os.WriteFile(targetFile, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRtarget"), 0644)

		ctrl, cfgMgr, _, _ := setupDarwinMigrationTestServer(t, sourceDir, targetDir)
		_ = os.RemoveAll(sourceDir)

		body := []byte(`{"mode":"overwrite"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		ctrl.handleMigrateStandardCache(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400 for missing source, got %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["code"] != ErrCodeMigrationSourceUnconfirmed {
			t.Fatalf("expected code %s, got %v", ErrCodeMigrationSourceUnconfirmed, res["code"])
		}

		// Ensure target file is completely preserved
		if _, err := os.Stat(targetFile); err != nil {
			t.Fatalf("target file must not be modified or deleted: %v", err)
		}
		if cfgMgr.Get().CacheDir != sourceDir {
			t.Fatalf("config CacheDir must remain untouched, got %s", cfgMgr.Get().CacheDir)
		}
	})

	// Subtest 2: Empty source directory (contains only .gbf_cache and system junk)
	t.Run("EmptySourceDirectory", func(t *testing.T) {
		tempBase := t.TempDir()
		if resolved, err := filepath.EvalSymlinks(tempBase); err == nil {
			tempBase = resolved
		}
		sourceDir := filepath.Join(tempBase, "empty_source")
		_ = os.MkdirAll(sourceDir, 0755)
		_ = cache.EnsureRootMarker(sourceDir)
		_ = os.WriteFile(filepath.Join(sourceDir, ".DS_Store"), []byte("junk"), 0644)

		targetDir := filepath.Join(tempBase, "target_cache")
		_ = os.MkdirAll(targetDir, 0755)
		_ = cache.EnsureRootMarker(targetDir)
		targetFile := filepath.Join(targetDir, "assets", "img", "icon.png")
		_ = os.MkdirAll(filepath.Dir(targetFile), 0755)
		_ = os.WriteFile(targetFile, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRtarget"), 0644)

		ctrl, cfgMgr, _, _ := setupDarwinMigrationTestServer(t, sourceDir, targetDir)

		body := []byte(`{"mode":"overwrite"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		ctrl.handleMigrateStandardCache(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("expected status 409 for empty source in overwrite, got %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["code"] != ErrCodeMigrationSourceUnconfirmed {
			t.Fatalf("expected code %s, got %v", ErrCodeMigrationSourceUnconfirmed, res["code"])
		}

		// Ensure target file is completely preserved
		if _, err := os.Stat(targetFile); err != nil {
			t.Fatalf("target file must not be modified or deleted: %v", err)
		}
		if cfgMgr.Get().CacheDir != sourceDir {
			t.Fatalf("config CacheDir must remain untouched, got %s", cfgMgr.Get().CacheDir)
		}
	})
}

func TestHandleMigrateStandardCache_RequestBodyBoundaries(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()
	ctrl, _, _, _ := setupDarwinMigrationTestServer(t, sourceDir, targetDir)

	// Subtest 1: Body > 4096 bytes
	t.Run("OversizedBody", func(t *testing.T) {
		oversized := bytes.Repeat([]byte("a"), 4097)
		req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader(oversized))
		rec := httptest.NewRecorder()
		ctrl.handleMigrateStandardCache(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400 for oversized body, got %d", rec.Code)
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["code"] != ErrCodeMigrationInvalidMode {
			t.Fatalf("expected code %s, got %v", ErrCodeMigrationInvalidMode, res["code"])
		}
		if !strings.Contains(fmt.Sprint(res["error"]), "超过限制") {
			t.Fatalf("expected error message to mention size limit, got %v", res["error"])
		}
	})

	// Subtest 2: Body with extra trailing JSON token
	t.Run("TrailingJSONTokens", func(t *testing.T) {
		body := []byte(`{"mode":"migrate"} {"extra":1}`)
		req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		ctrl.handleMigrateStandardCache(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400 for trailing JSON, got %d", rec.Code)
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["code"] != ErrCodeMigrationInvalidMode {
			t.Fatalf("expected code %s, got %v", ErrCodeMigrationInvalidMode, res["code"])
		}
		if !strings.Contains(fmt.Sprint(res["error"]), "多余") {
			t.Fatalf("expected error message to mention extra data, got %v", res["error"])
		}
	})

	// Subtest 3: Body with trailing non-JSON garbage
	t.Run("TrailingNonJSONGarbage", func(t *testing.T) {
		body := []byte(`{"mode":"migrate"} trailing_garbage`)
		req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		ctrl.handleMigrateStandardCache(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400 for trailing garbage, got %d", rec.Code)
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["code"] != ErrCodeMigrationInvalidMode {
			t.Fatalf("expected code %s, got %v", ErrCodeMigrationInvalidMode, res["code"])
		}
	})
}

func TestHandleMigrateStandardCache_TransactionFailures(t *testing.T) {
	pngMagic := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, 0x01}

	setupAssets := func(sourceDir, targetDir string) (string, string) {
		_ = os.MkdirAll(sourceDir, 0755)
		_ = cache.EnsureRootMarker(sourceDir)
		srcFile := filepath.Join(sourceDir, "assets", "img", "sp", "ui", "icon.png")
		_ = os.MkdirAll(filepath.Dir(srcFile), 0755)
		_ = os.WriteFile(srcFile, pngMagic, 0644)
		_ = os.WriteFile(srcFile+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)

		_ = os.MkdirAll(targetDir, 0755)
		_ = cache.EnsureRootMarker(targetDir)
		targetFile := filepath.Join(targetDir, "assets", "img", "sp", "ui", "old.png")
		_ = os.MkdirAll(filepath.Dir(targetFile), 0755)
		_ = os.WriteFile(targetFile, pngMagic, 0644)
		_ = os.WriteFile(targetFile+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)
		return srcFile, targetFile
	}

	// Subtest 1: Second rename fails, rollback succeeds
	t.Run("SecondRenameFailsRollbackSucceeds", func(t *testing.T) {
		tempBase := t.TempDir()
		if resolved, err := filepath.EvalSymlinks(tempBase); err == nil {
			tempBase = resolved
		}
		sourceDir := filepath.Join(tempBase, "src")
		targetDir := filepath.Join(tempBase, "dst")
		_, targetFile := setupAssets(sourceDir, targetDir)

		ctrl, _, _, _ := setupDarwinMigrationTestServer(t, sourceDir, targetDir)

		origRename := renameFunc
		renameCalls := 0
		renameFunc = func(oldpath, newpath string) error {
			renameCalls++
			if renameCalls == 2 {
				// Second rename (stageDir -> targetDir) fails
				return errors.New("simulated rename failure")
			}
			return origRename(oldpath, newpath)
		}
		t.Cleanup(func() { renameFunc = origRename })

		body := []byte(`{"mode":"overwrite"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		ctrl.handleMigrateStandardCache(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected status 500, got %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["code"] != ErrCodeMigrationReplacementFailed {
			t.Fatalf("expected code %s, got %v", ErrCodeMigrationReplacementFailed, res["code"])
		}

		// Verify target was restored by rollback
		if _, err := os.Stat(targetFile); err != nil {
			t.Fatalf("expected targetFile %s to be restored: %v", targetFile, err)
		}
	})

	// Subtest 2: Second rename fails, rollback ALSO fails
	t.Run("SecondRenameFailsRollbackFails", func(t *testing.T) {
		tempBase := t.TempDir()
		if resolved, err := filepath.EvalSymlinks(tempBase); err == nil {
			tempBase = resolved
		}
		sourceDir := filepath.Join(tempBase, "src")
		targetDir := filepath.Join(tempBase, "dst")
		setupAssets(sourceDir, targetDir)

		ctrl, _, _, _ := setupDarwinMigrationTestServer(t, sourceDir, targetDir)

		origRename := renameFunc
		renameCalls := 0
		renameFunc = func(oldpath, newpath string) error {
			renameCalls++
			if renameCalls == 2 {
				// Second rename fails
				return errors.New("simulated second rename failure")
			}
			if renameCalls == 3 {
				// Rollback rename also fails!
				return errors.New("simulated rollback rename failure")
			}
			return origRename(oldpath, newpath)
		}
		t.Cleanup(func() { renameFunc = origRename })

		body := []byte(`{"mode":"overwrite"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		ctrl.handleMigrateStandardCache(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected status 500, got %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["code"] != ErrCodeMigrationRollbackFailed {
			t.Fatalf("expected code %s, got %v", ErrCodeMigrationRollbackFailed, res["code"])
		}
		if res["backup_dir"] == nil || res["backup_dir"] == "" {
			t.Fatalf("expected backup_dir to be present in response when rollback fails")
		}
	})

	// Subtest 3: Commit fails, new directory cleanup fails, rollback succeeds
	t.Run("CommitFailsCleanupFailsRollbackSucceeds", func(t *testing.T) {
		tempBase := t.TempDir()
		if resolved, err := filepath.EvalSymlinks(tempBase); err == nil {
			tempBase = resolved
		}
		sourceDir := filepath.Join(tempBase, "src")
		targetDir := filepath.Join(tempBase, "dst")
		setupAssets(sourceDir, targetDir)

		ctrl, _, _, cfgPath := setupDarwinMigrationTestServer(t, sourceDir, targetDir)

		origRemoveAll := removeAllFunc
		removeAllFunc = func(path string) error {
			if path == targetDir {
				return errors.New("simulated new dir cleanup failure")
			}
			return origRemoveAll(path)
		}
		t.Cleanup(func() { removeAllFunc = origRemoveAll })

		// Break commit by making cfgPath a directory
		beforeMigrationApplyHook = func() {
			_ = os.Remove(cfgPath)
			_ = os.Mkdir(cfgPath, 0755)
		}
		t.Cleanup(func() { beforeMigrationApplyHook = nil })

		body := []byte(`{"mode":"overwrite"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		ctrl.handleMigrateStandardCache(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected status 500, got %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["backup_dir"] == nil || res["backup_dir"] == "" {
			t.Fatalf("expected backup_dir to be present in error response")
		}
	})

	// Subtest 4: Backup cleanup fails, returns 200 with warning
	t.Run("BackupCleanupFailsWarningReported", func(t *testing.T) {
		tempBase := t.TempDir()
		if resolved, err := filepath.EvalSymlinks(tempBase); err == nil {
			tempBase = resolved
		}
		sourceDir := filepath.Join(tempBase, "src")
		targetDir := filepath.Join(tempBase, "dst")
		setupAssets(sourceDir, targetDir)

		ctrl, _, _, _ := setupDarwinMigrationTestServer(t, sourceDir, targetDir)

		origRemoveAll := removeAllFunc
		removeAllFunc = func(path string) error {
			if strings.Contains(path, ".gbf-cache-old-") {
				return errors.New("simulated backup cleanup failure")
			}
			return origRemoveAll(path)
		}
		t.Cleanup(func() { removeAllFunc = origRemoveAll })

		body := []byte(`{"mode":"overwrite"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		ctrl.handleMigrateStandardCache(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200 with warning, got %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["warning"] == nil || res["warning"] == "" {
			t.Fatalf("expected warning to be returned in response")
		}
		if res["backup_dir"] == nil || res["backup_dir"] == "" {
			t.Fatalf("expected backup_dir to be returned in response")
		}
	})
}

func TestResidualMigrationBackups_FindAndRecover(t *testing.T) {
	parentDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(parentDir); err == nil {
		parentDir = resolved
	}

	backup1 := filepath.Join(parentDir, ".gbf-cache-old-1000")
	backup2 := filepath.Join(parentDir, ".gbf-cache-old-2000")
	otherDir := filepath.Join(parentDir, "other_folder")
	_ = os.MkdirAll(backup1, 0755)
	_ = os.MkdirAll(backup2, 0755)
	_ = os.MkdirAll(otherDir, 0755)

	cacheFile := filepath.Join(backup2, "saved.png")
	_ = os.WriteFile(cacheFile, []byte("pngdata"), 0644)

	// Test FindResidualMigrationBackups
	backups, err := FindResidualMigrationBackups(parentDir)
	if err != nil {
		t.Fatalf("FindResidualMigrationBackups failed: %v", err)
	}
	if len(backups) != 2 {
		t.Fatalf("expected 2 residual backups, got %d: %v", len(backups), backups)
	}
	if backups[0] != backup1 || backups[1] != backup2 {
		t.Fatalf("unexpected backup list: %v", backups)
	}

	// Test RecoverResidualMigrationBackup with non-empty target directory (must reject)
	targetDir := filepath.Join(parentDir, "target")
	_ = os.MkdirAll(targetDir, 0755)
	_ = os.WriteFile(filepath.Join(targetDir, "existing.dat"), []byte("data"), 0644)

	if err := RecoverResidualMigrationBackup(backup2, targetDir); err == nil {
		t.Fatal("expected RecoverResidualMigrationBackup to fail when target is non-empty")
	}

	// Clean target directory and recover
	_ = os.Remove(filepath.Join(targetDir, "existing.dat"))
	if err := RecoverResidualMigrationBackup(backup2, targetDir); err != nil {
		t.Fatalf("RecoverResidualMigrationBackup failed on empty target: %v", err)
	}

	// Verify recovered file exists in targetDir
	if _, err := os.Stat(filepath.Join(targetDir, "saved.png")); err != nil {
		t.Fatalf("expected recovered file to exist in targetDir: %v", err)
	}
	// Verify backup2 is moved (no longer exists)
	if _, err := os.Stat(backup2); !os.IsNotExist(err) {
		t.Fatalf("expected backup2 to be moved, but still exists")
	}
}

func TestHandleMigrateStandardCache_MigrateMissingSourceAllowed(t *testing.T) {
	tempBase := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(tempBase); err == nil {
		tempBase = resolved
	}
	sourceDir := filepath.Join(tempBase, "non_existent_source")
	targetDir := filepath.Join(tempBase, "target_cache")

	ctrl, cfgMgr, cacheMgr, _ := setupDarwinMigrationTestServer(t, sourceDir, targetDir)
	_ = os.RemoveAll(sourceDir) // Ensure source directory does not exist

	req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader([]byte(`{"mode":"migrate"}`)))
	rec := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 for migrate with missing source, got %d: %s", rec.Code, rec.Body.String())
	}
	if cfgMgr.Get().CacheDir != targetDir {
		t.Fatalf("expected config CacheDir to be %s, got %s", targetDir, cfgMgr.Get().CacheDir)
	}
	if cacheMgr.GetCacheBase() != targetDir {
		t.Fatalf("expected runtime cacheBase to be %s, got %s", targetDir, cacheMgr.GetCacheBase())
	}
}

func TestHandleMigrateStandardCache_SourceWithOnlyPersonalNoteRejected(t *testing.T) {
	tempBase := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(tempBase); err == nil {
		tempBase = resolved
	}
	sourceDir := filepath.Join(tempBase, "source_with_notes")
	_ = os.MkdirAll(sourceDir, 0755)
	_ = cache.EnsureRootMarker(sourceDir)

	noteFile := filepath.Join(sourceDir, "assets", "img", "personal-note.txt")
	_ = os.MkdirAll(filepath.Dir(noteFile), 0755)
	_ = os.WriteFile(noteFile, []byte("Private user note"), 0644)
	_ = os.WriteFile(noteFile+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)

	targetDir := filepath.Join(tempBase, "target_cache")
	_ = os.MkdirAll(targetDir, 0755)
	_ = cache.EnsureRootMarker(targetDir)
	targetFile := filepath.Join(targetDir, "assets", "img", "sp", "ui", "icon.png")
	_ = os.MkdirAll(filepath.Dir(targetFile), 0755)
	_ = os.WriteFile(targetFile, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRtarget"), 0644)
	_ = os.WriteFile(targetFile+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)

	ctrl, cfgMgr, _, _ := setupDarwinMigrationTestServer(t, sourceDir, targetDir)

	req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader([]byte(`{"mode":"overwrite"}`)))
	rec := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(rec, req)

	// Since sourceDir only contains personal-note.txt, hasConfirmedCacheAssets must return false
	// and reject with 409 ErrCodeMigrationSourceUnconfirmed
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409 for source containing only personal-note.txt, got %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res["code"] != ErrCodeMigrationSourceUnconfirmed {
		t.Fatalf("expected code %s, got %v", ErrCodeMigrationSourceUnconfirmed, res["code"])
	}

	// Verify target assets and config were not modified
	if _, err := os.Stat(targetFile); err != nil {
		t.Fatalf("target file must be preserved: %v", err)
	}
	if cfgMgr.Get().CacheDir != sourceDir {
		t.Fatalf("config CacheDir must not be changed, got %s", cfgMgr.Get().CacheDir)
	}
}

func TestHandleMigrateStandardCache_PostRenameBackupReverificationFailure(t *testing.T) {
	tempBase := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(tempBase); err == nil {
		tempBase = resolved
	}
	pngMagic := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, 0x01}

	sourceDir := filepath.Join(tempBase, "source_cache")
	_ = os.MkdirAll(sourceDir, 0755)
	_ = cache.EnsureRootMarker(sourceDir)
	srcFile := filepath.Join(sourceDir, "assets", "img", "sp", "ui", "icon.png")
	_ = os.MkdirAll(filepath.Dir(srcFile), 0755)
	_ = os.WriteFile(srcFile, pngMagic, 0644)
	_ = os.WriteFile(srcFile+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)

	targetDir := filepath.Join(tempBase, "target_cache")
	_ = os.MkdirAll(targetDir, 0755)
	_ = cache.EnsureRootMarker(targetDir)
	targetFile := filepath.Join(targetDir, "assets", "img", "sp", "ui", "old.png")
	_ = os.MkdirAll(filepath.Dir(targetFile), 0755)
	_ = os.WriteFile(targetFile, pngMagic, 0644)
	_ = os.WriteFile(targetFile+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)

	ctrl, _, _, _ := setupDarwinMigrationTestServer(t, sourceDir, targetDir)

	origRename := renameFunc
	renameCalls := 0
	renameFunc = func(oldpath, newpath string) error {
		renameCalls++
		if renameCalls == 1 {
			// First rename: targetDir -> oldBackupDir
			err := origRename(oldpath, newpath)
			if err == nil {
				// Inject an unconfirmed file into the frozen backup directory
				// simulating a concurrent change right before the rename completed
				corruptFile := filepath.Join(newpath, "assets", "img", "injected_user_doc.txt")
				_ = os.WriteFile(corruptFile, []byte("unconfirmed content"), 0644)
			}
			return err
		}
		return origRename(oldpath, newpath)
	}
	t.Cleanup(func() { renameFunc = origRename })

	req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader([]byte(`{"mode":"overwrite"}`)))
	rec := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409 when backup re-verification fails, got %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res["code"] != ErrCodeMigrationTargetUnconfirmed {
		t.Fatalf("expected code %s, got %v", ErrCodeMigrationTargetUnconfirmed, res["code"])
	}

	// Target directory must have been rolled back to targetDir
	if _, err := os.Stat(targetFile); err != nil {
		t.Fatalf("expected targetFile %s to exist after rollback: %v", targetFile, err)
	}
}

func TestHandleMigrateStandardCache_PostRenameBackupReverificationRollbackFailure_Overwrite(t *testing.T) {
	tempBase := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(tempBase); err == nil {
		tempBase = resolved
	}
	pngMagic := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, 0x01}

	sourceDir := filepath.Join(tempBase, "source_cache")
	_ = os.MkdirAll(sourceDir, 0755)
	_ = cache.EnsureRootMarker(sourceDir)
	srcFile := filepath.Join(sourceDir, "assets", "img", "sp", "ui", "icon.png")
	_ = os.MkdirAll(filepath.Dir(srcFile), 0755)
	_ = os.WriteFile(srcFile, pngMagic, 0644)
	_ = os.WriteFile(srcFile+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)

	targetDir := filepath.Join(tempBase, "target_cache")
	_ = os.MkdirAll(targetDir, 0755)
	_ = cache.EnsureRootMarker(targetDir)
	targetFile := filepath.Join(targetDir, "assets", "img", "sp", "ui", "old.png")
	_ = os.MkdirAll(filepath.Dir(targetFile), 0755)
	_ = os.WriteFile(targetFile, pngMagic, 0644)
	_ = os.WriteFile(targetFile+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)

	ctrl, _, _, _ := setupDarwinMigrationTestServer(t, sourceDir, targetDir)

	origRename := renameFunc
	renameCalls := 0
	var capturedBackupDir string
	renameFunc = func(oldpath, newpath string) error {
		renameCalls++
		if renameCalls == 1 {
			// Step 1: First rename moves targetDir -> oldBackupDir
			capturedBackupDir = newpath
			err := origRename(oldpath, newpath)
			if err == nil {
				// Step 2: Inject an unconfirmed file into the frozen backup directory
				corruptFile := filepath.Join(newpath, "assets", "img", "corrupt_note.txt")
				_ = os.WriteFile(corruptFile, []byte("unconfirmed content"), 0644)
			}
			return err
		}
		if renameCalls == 2 {
			// Step 3: Second rename deliberately fails to simulate rollback failure
			return fmt.Errorf("injected disk failure during rollback rename")
		}
		return origRename(oldpath, newpath)
	}
	t.Cleanup(func() { renameFunc = origRename })

	req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader([]byte(`{"mode":"overwrite"}`)))
	rec := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(rec, req)

	// Step 4: Verify API returns HTTP 500 and ErrCodeMigrationRollbackFailed
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 on rollback failure, got %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if res["code"] != ErrCodeMigrationRollbackFailed {
		t.Fatalf("expected code %s, got %v", ErrCodeMigrationRollbackFailed, res["code"])
	}

	// Step 5: Verify response contains the correct backup_dir
	if res["backup_dir"] != capturedBackupDir {
		t.Fatalf("expected backup_dir %s, got %v", capturedBackupDir, res["backup_dir"])
	}

	// Step 6: Verify original cache remains preserved in the backup directory, not deleted
	backedUpFile := filepath.Join(capturedBackupDir, "assets", "img", "sp", "ui", "old.png")
	if _, err := os.Stat(backedUpFile); err != nil {
		t.Fatalf("expected original cache file to be preserved in backup directory %s: %v", backedUpFile, err)
	}

	// Target directory should not have been restored
	if _, err := os.Stat(targetFile); !os.IsNotExist(err) {
		t.Fatalf("expected targetFile to not exist at original path after failed rollback, but got: %v", err)
	}
}

func TestHandleMigrateStandardCache_PostRenameBackupReverificationRollbackFailure_Migrate(t *testing.T) {
	tempBase := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(tempBase); err == nil {
		tempBase = resolved
	}

	sourceDir := filepath.Join(tempBase, "source_cache")
	_ = os.MkdirAll(sourceDir, 0755)

	targetDir := filepath.Join(tempBase, "target_cache")
	_ = os.MkdirAll(targetDir, 0755) // initially empty, passes validateEmptyMigrationTarget

	ctrl, _, _, _ := setupDarwinMigrationTestServer(t, sourceDir, targetDir)

	origRename := renameFunc
	renameCalls := 0
	var capturedBackupDir string
	renameFunc = func(oldpath, newpath string) error {
		renameCalls++
		if renameCalls == 1 {
			capturedBackupDir = newpath
			err := origRename(oldpath, newpath)
			if err == nil {
				// Inject an unexpected non-empty file into the backup directory
				injectedFile := filepath.Join(newpath, "unexpected.dat")
				_ = os.WriteFile(injectedFile, []byte("data"), 0644)
			}
			return err
		}
		if renameCalls == 2 {
			// Second rename simulates rollback failure
			return fmt.Errorf("injected disk failure during migrate rollback rename")
		}
		return origRename(oldpath, newpath)
	}
	t.Cleanup(func() { renameFunc = origRename })

	req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader([]byte(`{"mode":"migrate"}`)))
	rec := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 on migrate rollback failure, got %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if res["code"] != ErrCodeMigrationRollbackFailed {
		t.Fatalf("expected code %s, got %v", ErrCodeMigrationRollbackFailed, res["code"])
	}
	if res["backup_dir"] != capturedBackupDir {
		t.Fatalf("expected backup_dir %s, got %v", capturedBackupDir, res["backup_dir"])
	}

	// Verify backup dir remains preserved
	injectedFile := filepath.Join(capturedBackupDir, "unexpected.dat")
	if _, err := os.Stat(injectedFile); err != nil {
		t.Fatalf("expected injected file to be preserved in backup directory %s: %v", injectedFile, err)
	}
}

func TestHandleMigrateStandardCache_PostRenameBackupReverificationRollbackSuccess_Migrate(t *testing.T) {
	tempBase := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(tempBase); err == nil {
		tempBase = resolved
	}

	sourceDir := filepath.Join(tempBase, "source_cache")
	_ = os.MkdirAll(sourceDir, 0755)

	targetDir := filepath.Join(tempBase, "target_cache")
	_ = os.MkdirAll(targetDir, 0755) // initially empty, passes validateEmptyMigrationTarget

	ctrl, _, _, _ := setupDarwinMigrationTestServer(t, sourceDir, targetDir)

	origRename := renameFunc
	renameCalls := 0
	renameFunc = func(oldpath, newpath string) error {
		renameCalls++
		if renameCalls == 1 {
			err := origRename(oldpath, newpath)
			if err == nil {
				injectedFile := filepath.Join(newpath, "unexpected.dat")
				_ = os.WriteFile(injectedFile, []byte("data"), 0644)
			}
			return err
		}
		return origRename(oldpath, newpath)
	}
	t.Cleanup(func() { renameFunc = origRename })

	req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader([]byte(`{"mode":"migrate"}`)))
	rec := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409 on successful rollback, got %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if res["code"] != ErrCodeMigrationTargetNotEmpty {
		t.Fatalf("expected code %s, got %v", ErrCodeMigrationTargetNotEmpty, res["code"])
	}

	// Original directory must have been restored
	restoredFile := filepath.Join(targetDir, "unexpected.dat")
	if _, err := os.Stat(restoredFile); err != nil {
		t.Fatalf("expected restored file to exist at targetDir: %v", err)
	}
}
