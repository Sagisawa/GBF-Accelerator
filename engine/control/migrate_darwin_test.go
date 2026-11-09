//go:build darwin

package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

const testAssetRel = "assets/img/sp/ui/icon.png"

var testValidPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRmigration")

func hasTestFile(dir, rel string) bool {
	_, err := os.Stat(filepath.Join(dir, rel))
	return err == nil
}

func evalTempDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if res, err := filepath.EvalSymlinks(d); err == nil {
		return res
	}
	return d
}

func setMigrationTargetForTest(t *testing.T, path string) {
	t.Helper()
	prev := standardCacheDirProvider
	standardCacheDirProvider = func() string { return path }
	t.Cleanup(func() { standardCacheDirProvider = prev })
}

func setupTestDir(t *testing.T, base, name string, validCache bool) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if validCache {
		asset := filepath.Join(dir, testAssetRel)
		_ = os.MkdirAll(filepath.Dir(asset), 0755); _ = cache.EnsureRootMarker(dir)
		_ = os.WriteFile(asset, testValidPNG, 0644); _ = os.WriteFile(asset+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)
	} else if name != "missing" {
		_ = os.MkdirAll(dir, 0755)
	}
	return dir
}

func setupDarwinMigrationTestServer(t *testing.T, srcDir, tgtDir string) (*ControlServer, *config.Manager, *cache.Manager, string) {
	t.Helper()
	cfgPath := filepath.Join(evalTempDir(t), "config.json")
	cfgMgr := config.NewManager(cfgPath)
	c := cfgMgr.Candidate()
	c.CacheDir = srcDir
	if err := cfgMgr.Commit(c); err != nil { t.Fatal(err) }
	cacheMgr := cache.NewManager(srcDir, 16)
	t.Cleanup(func() { cacheMgr.Close() })
	setMigrationTargetForTest(t, tgtDir)
	return NewControlServer(cfgMgr, nil, cacheMgr, nil, telemetry.NewStats()), cfgMgr, cacheMgr, cfgPath
}

func execMigrateRequest(ctrl *ControlServer, body []byte) (int, map[string]interface{}) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", r)
	rec := httptest.NewRecorder()
	ctrl.handleMigrateStandardCache(rec, req)
	var res map[string]interface{}
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
	}
	return rec.Code, res
}

func mockRename(corruptFile string, failRollback bool, failCalls ...int) func() {
	orig, calls := renameFunc, 0
	renameFunc = func(o, n string) error {
		calls++
		for _, fc := range failCalls {
			if calls == fc { return errors.New("simulated rename failure") }
		}
		if calls == 2 && failRollback { return errors.New("simulated rollback rename failure") }
		err := orig(o, n)
		if calls == 1 && corruptFile != "" && err == nil {
			_ = os.WriteFile(filepath.Join(n, corruptFile), []byte("unconfirmed"), 0644)
		}
		return err
	}
	return func() { renameFunc = orig }
}

func TestValidateCacheTargetAndTree_Structural(t *testing.T) {
	root := evalTempDir(t)
	src := setupTestDir(t, root, "src_tree", true)
	if err := validateCacheMigrationTree(src); err != nil {
		t.Fatalf("confirmed source tree should pass: %v", err)
	}
	for _, tc := range []struct {
		rel      string
		data     []byte
		symlink  bool
		wantFail bool
	}{
		{"assets/img/sp/ui/bare.png", testValidPNG, false, true}, {"personal_data/profile.png", testValidPNG, false, true},
		{"assets/img/sp/ui/orphan.png.ext", []byte(`{"v":1}`), false, false}, {"assets/img/sp/ui/linked.png", nil, true, true},
	} {
		p := filepath.Join(src, tc.rel)
		_ = os.MkdirAll(filepath.Dir(p), 0755)
		if tc.symlink {
			_ = os.Symlink(filepath.Join(src, testAssetRel), p)
		} else {
			_ = os.WriteFile(p, tc.data, 0644)
		}
		if err := validateCacheMigrationTree(src); (err != nil) != tc.wantFail {
			t.Fatalf("[%s] validateCacheMigrationTree err=%v, wantFail=%v", tc.rel, err, tc.wantFail)
		}
		_ = os.Remove(p)
		if strings.Contains(tc.rel, "personal_data") {
			_ = os.RemoveAll(filepath.Join(src, "personal_data"))
		}
	}

	emptyDir := setupTestDir(t, root, "empty_tgt", false)
	if err := validateEmptyMigrationTarget(emptyDir); err != nil {
		t.Fatalf("empty target should pass: %v", err)
	}
	_ = os.WriteFile(filepath.Join(emptyDir, ".DS_Store"), []byte("existing"), 0644)
	if err := validateEmptyMigrationTarget(emptyDir); err == nil {
		t.Fatal("target with .DS_Store must fail")
	}

	fileTarget, symDir := filepath.Join(root, "plain_file.txt"), filepath.Join(root, "sym_dir")
	_ = os.WriteFile(fileTarget, []byte("data"), 0644); _ = os.Symlink(emptyDir, symDir)
	cleanEmpty, validCache := setupTestDir(t, root, "clean_empty", false), setupTestDir(t, root, "valid_cache", true)
	_ = os.MkdirAll(filepath.Join(validCache, "hosts/game.granbluefantasy.jp"), 0755)
	_ = os.WriteFile(filepath.Join(validCache, ".DS_Store"), []byte("junk"), 0644)
	_ = os.WriteFile(filepath.Join(validCache, "favicon.ico"), []byte("ico"), 0644)
	_ = os.WriteFile(filepath.Join(validCache, "favicon.ico.ext"), []byte(`{"v":1,"ns":"gbf"}`), 0644)

	for _, tc := range []struct {
		path     string
		wantFail bool
	}{
		{filepath.Join(root, "non_existent"), false}, {fileTarget, true}, {symDir, true}, {cleanEmpty, false}, {validCache, false},
	} {
		if err := validateCacheOverwriteTarget(tc.path); (err != nil) != tc.wantFail {
			t.Fatalf("[%s] validateCacheOverwriteTarget err=%v, wantFail=%v", tc.path, err, tc.wantFail)
		}
	}
	for _, inv := range []string{"personal_docs/", "notes.txt", "sym_entry@link", "assets.tmp.123.456.789"} {
		p := filepath.Join(validCache, strings.TrimSuffix(inv, "/"))
		if strings.HasSuffix(inv, "/") {
			_ = os.MkdirAll(p, 0755)
		} else if strings.Contains(inv, "@link") {
			p = filepath.Join(validCache, "sym_entry"); _ = os.Symlink(fileTarget, p)
		} else {
			_ = os.WriteFile(p, []byte("temp"), 0644)
		}
		if err := validateCacheOverwriteTarget(validCache); err == nil {
			t.Fatalf("entry %s must fail", inv)
		}
		_ = os.RemoveAll(p)
	}
}

func TestHandleMigrateStandardCache_ModesAndPreflight(t *testing.T) {
	t.Run("RequestBodyAndIdentity", func(t *testing.T) {
		temp := evalTempDir(t)
		srcDir := setupTestDir(t, temp, "src", true)
		tgtDir := setupTestDir(t, temp, "tgt", false)
		_ = os.WriteFile(filepath.Join(tgtDir, "file.txt"), []byte("data"), 0644)
		ctrl, _, _, _ := setupDarwinMigrationTestServer(t, srcDir, tgtDir)

		for _, tc := range []struct {
			name, code string
			body       []byte
			status     int
		}{
			{"nil_body", ErrCodeMigrationTargetNotEmpty, nil, 409}, {"empty_json", ErrCodeMigrationTargetNotEmpty, []byte("{}"), 409},
			{"unknown_mode", ErrCodeMigrationInvalidMode, []byte(`{"mode":"teleport"}`), 400}, {"invalid_json", ErrCodeMigrationInvalidMode, []byte(`{not_json`), 400},
			{"oversized", ErrCodeMigrationInvalidMode, bytes.Repeat([]byte("a"), 4097), 400},
			{"trailing_tokens", ErrCodeMigrationInvalidMode, []byte(`{"mode":"migrate"} {"extra":1}`), 400},
			{"trailing_garbage", ErrCodeMigrationInvalidMode, []byte(`{"mode":"migrate"} trailing_garbage`), 400},
		} {
			if code, res := execMigrateRequest(ctrl, tc.body); code != tc.status || res["code"] != tc.code {
				t.Fatalf("[%s] expected status %d code %s, got %d (%v)", tc.name, tc.status, tc.code, code, res)
			}
		}
		ctrlSame, _, _, _ := setupDarwinMigrationTestServer(t, tgtDir, tgtDir)
		if codeSame, _ := execMigrateRequest(ctrlSame, nil); codeSame != 200 {
			t.Fatalf("expected 200 for identical paths, got %d", codeSame)
		}
	})

	t.Run("ModeMatrix", func(t *testing.T) {
		for _, tc := range []struct {
			name, mode, code, cDir                  string
			status                                  int
			srcValid, tgtValid, srcMissing          bool
			setup                                   func(srcDir, tgtDir string)
			hook                                    func(cfgMgr *config.Manager, cfgPath, otherDir string)
			checkBase, checkSrcAsset, checkTgtAsset bool
			checkFile, fileContent, checkAbsent     string
		}{
			{name: "SwitchOnly", mode: "switch_only", setup: func(_, tgt string) { _ = os.WriteFile(filepath.Join(tgt, "notes.txt"), []byte("hello user notes"), 0644) }, status: 200, cDir: "tgt", checkBase: true, checkFile: "notes.txt", fileContent: "hello user notes"},
			{name: "Overwrite_TargetUnconfirmed", mode: "overwrite", srcValid: true, tgtValid: true, setup: func(_, tgt string) { _ = os.WriteFile(filepath.Join(tgt, "user.pdf"), []byte("pdf data"), 0644) }, status: 409, code: ErrCodeMigrationTargetUnconfirmed, checkFile: "user.pdf"},
			{name: "Overwrite_ValidTarget_Success", mode: "overwrite", srcValid: true, tgtValid: true, status: 200, cDir: "tgt", checkTgtAsset: true, checkSrcAsset: true},
			{name: "Overwrite_MissingSource", mode: "overwrite", srcValid: true, tgtValid: true, setup: func(src, _ string) { _ = os.RemoveAll(src) }, status: 400, code: ErrCodeMigrationSourceUnconfirmed, cDir: "src", checkTgtAsset: true},
			{name: "Overwrite_EmptySource", mode: "overwrite", tgtValid: true, status: 409, code: ErrCodeMigrationSourceUnconfirmed, cDir: "src", checkTgtAsset: true},
			{name: "Migrate_MissingSourceAllowed", mode: "migrate", srcMissing: true, status: 200, cDir: "tgt", checkBase: true},
			{name: "Migrate_NonEmptyTargetConflict", mode: "migrate", srcValid: true, setup: func(_, tgt string) { _ = os.WriteFile(filepath.Join(tgt, "doc.txt"), []byte("data"), 0644) }, status: 409, code: ErrCodeMigrationTargetNotEmpty},
			{name: "ConcurrentSourceChange", mode: "switch_only", hook: func(cfgMgr *config.Manager, _, other string) { c := cfgMgr.Candidate(); c.CacheDir = other; _ = cfgMgr.Commit(c) }, status: 409, code: ErrCodeMigrationSourceChanged},
			{name: "CommitFailureRollsBack", mode: "overwrite", srcValid: true, tgtValid: true, hook: func(_ *config.Manager, cfgPath, _ string) { _ = os.Remove(cfgPath); _ = os.Mkdir(cfgPath, 0755) }, status: 500, code: ErrCodeMigrationConfigCommitFailed, checkTgtAsset: true, checkAbsent: "assets/img/sp/ui/new_marker.png"},
			{name: "SwitchOnly_CommitFailurePreservesTarget", mode: "switch_only", tgtValid: true, setup: func(_, tgt string) { _ = os.WriteFile(filepath.Join(tgt, "notes.txt"), []byte("valuable cache"), 0644) }, hook: func(_ *config.Manager, cfgPath, _ string) { _ = os.Remove(cfgPath); _ = os.Mkdir(cfgPath, 0755) }, status: 500, code: ErrCodeMigrationConfigCommitFailed, cDir: "src", checkFile: "notes.txt", fileContent: "valuable cache"},
			{name: "SwitchOnly_CommitFailureCleansCreatedTarget", mode: "switch_only", tgtValid: false, setup: func(_, tgt string) { _ = os.Remove(tgt) }, hook: func(_ *config.Manager, cfgPath, _ string) { _ = os.Remove(cfgPath); _ = os.Mkdir(cfgPath, 0755) }, status: 500, code: ErrCodeMigrationConfigCommitFailed, cDir: "src", checkAbsent: "."},
			{name: "Overwrite_TargetNestedUnconfirmed", mode: "overwrite", srcValid: true, tgtValid: true, setup: func(_, tgt string) { p := filepath.Join(tgt, "assets", "personal_docs"); _ = os.MkdirAll(p, 0755); _ = os.WriteFile(filepath.Join(p, "contract.pdf"), []byte("pdf"), 0644) }, status: 409, code: ErrCodeMigrationTargetUnconfirmed, checkFile: filepath.Join("assets", "personal_docs", "contract.pdf")},
		} {
			t.Run(tc.name, func(t *testing.T) {
				temp := evalTempDir(t)
				srcDir := filepath.Join(temp, "src")
				if !tc.srcMissing {
					srcDir = setupTestDir(t, temp, "src", tc.srcValid)
				}
				tgtDir := setupTestDir(t, temp, "tgt", tc.tgtValid)
				ctrl, cfgMgr, cacheMgr, cfgPath := setupDarwinMigrationTestServer(t, srcDir, tgtDir)
				if tc.setup != nil {
					tc.setup(srcDir, tgtDir)
				}
				if tc.hook != nil {
					beforeMigrationApplyHook = func() { tc.hook(cfgMgr, cfgPath, filepath.Join(temp, "other")) }
					defer func() { beforeMigrationApplyHook = nil }()
				}
				code, res := execMigrateRequest(ctrl, []byte(fmt.Sprintf(`{"mode":%q}`, tc.mode)))
				if code != tc.status || (tc.code != "" && res["code"] != tc.code) {
					t.Fatalf("expected status %d code %s, got %d (%v)", tc.status, tc.code, code, res)
				}
				if (tc.cDir == "tgt" && cfgMgr.Get().CacheDir != tgtDir) || (tc.cDir == "src" && cfgMgr.Get().CacheDir != srcDir) ||
					(tc.checkBase && cacheMgr.GetCacheBase() != tgtDir) || (tc.checkTgtAsset && !hasTestFile(tgtDir, testAssetRel)) ||
					(tc.checkSrcAsset && !hasTestFile(srcDir, testAssetRel)) || (tc.checkAbsent != "" && hasTestFile(tgtDir, tc.checkAbsent)) {
					t.Fatalf("assertion failed for %s", tc.name)
				}
				if tc.checkFile != "" {
					if data, err := os.ReadFile(filepath.Join(tgtDir, tc.checkFile)); err != nil || (tc.fileContent != "" && string(data) != tc.fileContent) {
						t.Fatalf("expected file %q content %q, got %q (err: %v)", tc.checkFile, tc.fileContent, string(data), err)
					}
				}
			})
		}
	})
}

func mockRemoveAllMatch(sub string) func() {
	orig := removeAllFunc
	removeAllFunc = func(p string) error {
		if strings.Contains(p, sub) {
			return errors.New("cleanup failure")
		}
		return orig(p)
	}
	return func() { removeAllFunc = orig }
}

func TestHandleMigrateStandardCache_FailuresAndRollback(t *testing.T) {
	for _, tc := range []struct {
		name, mode, code string
		withAssets       bool
		hook             func(srcDir, tgtDir, cfgPath string) (cleanup func())
		status           int
		wantBackup       bool
		wantWarn         bool
		restored, absent string
	}{
		{"SecondRenameFailsRollbackSucceeds", "overwrite", ErrCodeMigrationReplacementFailed, true, func(_, _, _ string) func() { return mockRename("", false, 2) }, 500, false, false, testAssetRel, ""},
		{"SecondRenameFailsRollbackFails", "overwrite", ErrCodeMigrationRollbackFailed, true, func(_, _, _ string) func() { return mockRename("", false, 2, 3) }, 500, true, false, "", ""},
		{"CommitFailsCleanupFailsRollbackSucceeds", "overwrite", "", true, func(_, tgt, cfg string) func() {
			rmCl := mockRemoveAllMatch(tgt)
			beforeMigrationApplyHook = func() { _ = os.Remove(cfg); _ = os.Mkdir(cfg, 0755) }
			return func() { rmCl(); beforeMigrationApplyHook = nil }
		}, 500, true, false, "", ""},
		{"BackupCleanupFailsWarningReported", "overwrite", "", true, func(_, _, _ string) func() { return mockRemoveAllMatch(".gbf-cache-old-") }, 200, true, true, "", ""},
		{"Overwrite_PostRenameCorruptRollbackSuccess", "overwrite", ErrCodeMigrationTargetUnconfirmed, true, func(_, _, _ string) func() { return mockRename("injected_user_doc.txt", false) }, 409, false, false, testAssetRel, ""},
		{"Overwrite_PostRenameCorruptRollbackFailure", "overwrite", ErrCodeMigrationRollbackFailed, true, func(_, _, _ string) func() { return mockRename("corrupt_note.txt", true) }, 500, true, false, "", testAssetRel},
		{"Migrate_PostRenameCorruptRollbackSuccess", "migrate", ErrCodeMigrationTargetNotEmpty, false, func(_, _, _ string) func() { return mockRename("unexpected.dat", false) }, 409, false, false, "unexpected.dat", ""},
		{"Migrate_PostRenameCorruptRollbackFailure", "migrate", ErrCodeMigrationRollbackFailed, false, func(_, _, _ string) func() { return mockRename("unexpected.dat", true) }, 500, true, false, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp := evalTempDir(t)
			srcDir, tgtDir := setupTestDir(t, temp, "src", tc.withAssets), setupTestDir(t, temp, "tgt", tc.withAssets)
			ctrl, _, _, cfgPath := setupDarwinMigrationTestServer(t, srcDir, tgtDir)
			if tc.hook != nil {
				defer tc.hook(srcDir, tgtDir, cfgPath)()
			}
			code, res := execMigrateRequest(ctrl, []byte(fmt.Sprintf(`{"mode":%q}`, tc.mode)))
			if code != tc.status || (tc.code != "" && res["code"] != tc.code) {
				t.Fatalf("expected status %d code %s, got %d (%v)", tc.status, tc.code, code, res)
			}
			if (tc.wantBackup && (res["backup_dir"] == nil || res["backup_dir"] == "")) ||
				(tc.wantWarn && (res["warning"] == nil || res["warning"] == "")) ||
				(tc.restored != "" && !hasTestFile(tgtDir, tc.restored)) ||
				(tc.absent != "" && hasTestFile(tgtDir, tc.absent)) {
				t.Fatalf("unexpected result in res: %v", res)
			}
		})
	}
}

func TestResidualMigrationBackups_FindAndRecover(t *testing.T) {
	p := evalTempDir(t)
	b1, b2 := filepath.Join(p, ".gbf-cache-old-1000"), filepath.Join(p, ".gbf-cache-old-2000")
	_ = os.MkdirAll(b1, 0755); _ = os.MkdirAll(b2, 0755); _ = os.MkdirAll(filepath.Join(p, "other"), 0755)
	_ = os.WriteFile(filepath.Join(b2, "saved.png"), []byte("png"), 0644)
	if backups, err := FindResidualMigrationBackups(p); err != nil || len(backups) != 2 || backups[0] != b1 || backups[1] != b2 {
		t.Fatalf("unexpected FindResidualMigrationBackups: %v (%v)", backups, err)
	}
	tgt, exist := filepath.Join(p, "target"), filepath.Join(p, "target", "exist.dat")
	_ = os.MkdirAll(tgt, 0755); _ = os.WriteFile(exist, []byte("data"), 0644)
	if RecoverResidualMigrationBackup(b2, tgt) == nil {
		t.Fatal("expected error recovering to non-empty target")
	}
	_ = os.Remove(exist)
	if err := RecoverResidualMigrationBackup(b2, tgt); err != nil || !hasTestFile(tgt, "saved.png") || hasTestFile(p, ".gbf-cache-old-2000") {
		t.Fatalf("RecoverResidualMigrationBackup failed: %v", err)
	}
}

func TestHandleMigrateStandardCache_ConcurrencyStress(t *testing.T) {
	temp := evalTempDir(t)
	srcDir, tgtDir := setupTestDir(t, temp, "src", true), setupTestDir(t, temp, "tgt", false)
	ctrl, _, _, _ := setupDarwinMigrationTestServer(t, srcDir, tgtDir)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", strings.NewReader(`{"mode":"migrate"}`))
			ctrl.handleMigrateStandardCache(rec, req)
		}()
	}
	wg.Wait()
}

