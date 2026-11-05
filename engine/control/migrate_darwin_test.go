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

func TestValidateCacheOverwriteTarget_Structural(t *testing.T) {
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}

	// 1. Non-existent root: MUST PASS (nothing to overwrite)
	nonExistent := filepath.Join(root, "does_not_exist")
	if err := validateCacheOverwriteTarget(nonExistent); err != nil {
		t.Fatalf("expected non-existent path to pass: %v", err)
	}

	// 2. Regular file instead of directory: MUST FAIL
	filePath := filepath.Join(root, "plain_file.txt")
	if err := os.WriteFile(filePath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheOverwriteTarget(filePath); err == nil {
		t.Fatal("expected regular file to fail validateCacheOverwriteTarget")
	}

	// 3. Symlink to directory: MUST FAIL
	symlinkPath := filepath.Join(root, "dir_symlink")
	targetSubdir := filepath.Join(root, "target_subdir")
	_ = os.MkdirAll(targetSubdir, 0755)
	if err := os.Symlink(targetSubdir, symlinkPath); err == nil {
		if err := validateCacheOverwriteTarget(symlinkPath); err == nil {
			t.Fatal("expected symlink directory to fail validateCacheOverwriteTarget")
		}
	}

	// 4. Empty directory: MUST PASS
	emptyDir := filepath.Join(root, "empty_dir")
	_ = os.MkdirAll(emptyDir, 0755)
	if err := validateCacheOverwriteTarget(emptyDir); err != nil {
		t.Fatalf("expected empty directory to pass: %v", err)
	}

	// 5. Valid cache structure: .gbf_cache, .DS_Store, assets/, hosts/, favicon.ico: MUST PASS
	cacheRoot := filepath.Join(root, "valid_cache")
	_ = os.MkdirAll(cacheRoot, 0755)
	if err := cache.EnsureRootMarker(cacheRoot); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(cacheRoot, ".DS_Store"), []byte("junk"), 0644)
	_ = os.WriteFile(filepath.Join(cacheRoot, "favicon.ico"), []byte("ico"), 0644)
	_ = os.WriteFile(filepath.Join(cacheRoot, "favicon.ico.ext"), []byte(`{"v":1,"ns":"gbf"}`), 0644)
	_ = os.MkdirAll(filepath.Join(cacheRoot, "assets", "img"), 0755)
	_ = os.MkdirAll(filepath.Join(cacheRoot, "hosts", "game.granbluefantasy.jp"), 0755)

	if err := validateCacheOverwriteTarget(cacheRoot); err != nil {
		t.Fatalf("expected valid cache structure to pass: %v", err)
	}

	// 6. Unknown top-level directory (e.g. personal_docs): MUST FAIL
	badDir := filepath.Join(cacheRoot, "personal_docs")
	_ = os.MkdirAll(badDir, 0755)
	if err := validateCacheOverwriteTarget(cacheRoot); err == nil {
		t.Fatal("expected unknown top-level directory to FAIL")
	}
	_ = os.RemoveAll(badDir)

	// 7. Unknown root file (e.g. notes.txt): MUST FAIL
	unknownFile := filepath.Join(cacheRoot, "notes.txt")
	_ = os.WriteFile(unknownFile, []byte("my notes"), 0644)
	if err := validateCacheOverwriteTarget(cacheRoot); err == nil {
		t.Fatal("expected unknown root file to FAIL")
	}
	_ = os.Remove(unknownFile)

	// 8. Root symlink entry: MUST FAIL
	symlinkEntry := filepath.Join(cacheRoot, "sym_entry")
	if err := os.Symlink(filePath, symlinkEntry); err == nil {
		if err := validateCacheOverwriteTarget(cacheRoot); err == nil {
			t.Fatal("expected symlink inside root to FAIL")
		}
		_ = os.Remove(symlinkEntry)
	}

	// 9. Root temp streaming file: MUST FAIL
	tempFile := filepath.Join(cacheRoot, "assets.tmp.123.456.789")
	_ = os.WriteFile(tempFile, []byte("temp"), 0644)
	if err := validateCacheOverwriteTarget(cacheRoot); err == nil {
		t.Fatal("expected root temp file to FAIL")
	}
	_ = os.Remove(tempFile)
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

func setupStandardMigrationDirs(t *testing.T, withAssets bool) (sourceDir, targetDir, srcFile, targetFile string, ctrl *ControlServer, cfgMgr *config.Manager, cfgPath string) {
	t.Helper()
	tempBase := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(tempBase); err == nil {
		tempBase = resolved
	}
	sourceDir = filepath.Join(tempBase, "source_cache")
	_ = os.MkdirAll(sourceDir, 0755)
	targetDir = filepath.Join(tempBase, "target_cache")
	_ = os.MkdirAll(targetDir, 0755)

	if withAssets {
		_ = cache.EnsureRootMarker(sourceDir)
		_ = cache.EnsureRootMarker(targetDir)
		pngMagic := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, 0x01}
		srcFile = filepath.Join(sourceDir, "assets", "img", "sp", "ui", "icon.png")
		_ = os.MkdirAll(filepath.Dir(srcFile), 0755)
		_ = os.WriteFile(srcFile, pngMagic, 0644)
		_ = os.WriteFile(srcFile+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)

		targetFile = filepath.Join(targetDir, "assets", "img", "sp", "ui", "old.png")
		_ = os.MkdirAll(filepath.Dir(targetFile), 0755)
		_ = os.WriteFile(targetFile, pngMagic, 0644)
		_ = os.WriteFile(targetFile+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)
	}

	ctrl, cfgMgr, _, cfgPath = setupDarwinMigrationTestServer(t, sourceDir, targetDir)
	return
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
	_, targetDir, srcAsset, _, ctrl, cfgMgr, _ := setupStandardMigrationDirs(t, true)

	// 1. Target contains an unconfirmed user file (user.pdf): OVERWRITE MUST BE REJECTED
	userPdf := filepath.Join(targetDir, "user.pdf")
	if err := os.WriteFile(userPdf, []byte("pdf data"), 0644); err != nil {
		t.Fatal(err)
	}

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
	reqSuccess := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader(body))
	recSuccess := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(recSuccess, reqSuccess)

	if recSuccess.Code != http.StatusOK {
		t.Fatalf("expected status 200 for valid overwrite, got %d: %s", recSuccess.Code, recSuccess.Body.String())
	}

	// Verify target now contains src icon from sourceDir
	migratedAsset := filepath.Join(targetDir, "assets", "img", "sp", "ui", "icon.png")
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
	_, targetDir, _, oldTargetAsset, ctrl, _, cfgPath := setupStandardMigrationDirs(t, true)

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

func TestHandleMigrateStandardCache_MissingOrEmptySourceInOverwrite(t *testing.T) {
	// Subtest 1: Missing source directory
	t.Run("MissingSourceDirectory", func(t *testing.T) {
		sourceDir, _, _, targetFile, ctrl, cfgMgr, _ := setupStandardMigrationDirs(t, true)
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
		sourceDir, _, srcFile, targetFile, ctrl, cfgMgr, _ := setupStandardMigrationDirs(t, true)
		_ = os.Remove(srcFile)
		_ = os.Remove(srcFile + ".ext")
		_ = os.WriteFile(filepath.Join(sourceDir, ".DS_Store"), []byte("junk"), 0644)

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
	// Subtest 1: Second rename fails, rollback succeeds
	t.Run("SecondRenameFailsRollbackSucceeds", func(t *testing.T) {
		_, _, _, targetFile, ctrl, _, _ := setupStandardMigrationDirs(t, true)

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
		_, _, _, _, ctrl, _, _ := setupStandardMigrationDirs(t, true)

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
		_, targetDir, _, _, ctrl, _, cfgPath := setupStandardMigrationDirs(t, true)

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
		_, _, _, _, ctrl, _, _ := setupStandardMigrationDirs(t, true)

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

func TestHandleMigrateStandardCache_PostRenameBackupReverification(t *testing.T) {
	t.Run("Overwrite_RollbackSuccess", func(t *testing.T) {
		_, _, _, targetFile, ctrl, _, _ := setupStandardMigrationDirs(t, true)
		origRename := renameFunc
		renameCalls := 0
		renameFunc = func(oldpath, newpath string) error {
			renameCalls++
			if renameCalls == 1 {
				err := origRename(oldpath, newpath)
				if err == nil {
					_ = os.WriteFile(filepath.Join(newpath, "injected_user_doc.txt"), []byte("unconfirmed"), 0644)
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
			t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["code"] != ErrCodeMigrationTargetUnconfirmed {
			t.Fatalf("expected %s, got %v", ErrCodeMigrationTargetUnconfirmed, res["code"])
		}
		if _, err := os.Stat(targetFile); err != nil {
			t.Fatalf("expected targetFile to exist after rollback: %v", err)
		}
	})

	t.Run("Overwrite_RollbackFailure", func(t *testing.T) {
		_, _, _, targetFile, ctrl, _, _ := setupStandardMigrationDirs(t, true)
		origRename := renameFunc
		renameCalls := 0
		var capturedBackupDir string
		renameFunc = func(oldpath, newpath string) error {
			renameCalls++
			if renameCalls == 1 {
				capturedBackupDir = newpath
				err := origRename(oldpath, newpath)
				if err == nil {
					_ = os.WriteFile(filepath.Join(newpath, "corrupt_note.txt"), []byte("unconfirmed"), 0644)
				}
				return err
			}
			if renameCalls == 2 {
				return errors.New("simulated rollback rename failure")
			}
			return origRename(oldpath, newpath)
		}
		t.Cleanup(func() { renameFunc = origRename })

		req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader([]byte(`{"mode":"overwrite"}`)))
		rec := httptest.NewRecorder()
		ctrl.handleMigrateStandardCache(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["code"] != ErrCodeMigrationRollbackFailed || res["backup_dir"] != capturedBackupDir {
			t.Fatalf("expected rollback failure response, got %v", res)
		}
		if _, err := os.Stat(targetFile); !os.IsNotExist(err) {
			t.Fatalf("targetFile must not exist at original path after failed rollback")
		}
	})

	t.Run("Migrate_RollbackSuccess", func(t *testing.T) {
		_, targetDir, _, _, ctrl, _, _ := setupStandardMigrationDirs(t, false)
		origRename := renameFunc
		renameCalls := 0
		renameFunc = func(oldpath, newpath string) error {
			renameCalls++
			if renameCalls == 1 {
				err := origRename(oldpath, newpath)
				if err == nil {
					_ = os.WriteFile(filepath.Join(newpath, "unexpected.dat"), []byte("data"), 0644)
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
			t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["code"] != ErrCodeMigrationTargetNotEmpty {
			t.Fatalf("expected %s, got %v", ErrCodeMigrationTargetNotEmpty, res["code"])
		}
		if _, err := os.Stat(filepath.Join(targetDir, "unexpected.dat")); err != nil {
			t.Fatalf("expected restored file to exist at targetDir: %v", err)
		}
	})

	t.Run("Migrate_RollbackFailure", func(t *testing.T) {
		_, _, _, _, ctrl, _, _ := setupStandardMigrationDirs(t, false)
		origRename := renameFunc
		renameCalls := 0
		var capturedBackupDir string
		renameFunc = func(oldpath, newpath string) error {
			renameCalls++
			if renameCalls == 1 {
				capturedBackupDir = newpath
				err := origRename(oldpath, newpath)
				if err == nil {
					_ = os.WriteFile(filepath.Join(newpath, "unexpected.dat"), []byte("data"), 0644)
				}
				return err
			}
			if renameCalls == 2 {
				return errors.New("simulated rollback rename failure")
			}
			return origRename(oldpath, newpath)
		}
		t.Cleanup(func() { renameFunc = origRename })

		req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", bytes.NewReader([]byte(`{"mode":"migrate"}`)))
		rec := httptest.NewRecorder()
		ctrl.handleMigrateStandardCache(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["code"] != ErrCodeMigrationRollbackFailed || res["backup_dir"] != capturedBackupDir {
			t.Fatalf("expected rollback failure response, got %v", res)
		}
	})
}
