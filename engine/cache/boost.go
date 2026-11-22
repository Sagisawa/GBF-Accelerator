package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gbf-proxy/config"
	"gbf-proxy/process"
)

const (
	BoostStateDisabled           = "disabled"
	BoostStateLoading            = "loading"
	BoostStateCompleted          = "completed"
	BoostStatePartial            = "partial"
	BoostStateCancelled          = "cancelled"
	BoostStateMemoryGuardStopped = "memory_guard_stopped"

	defaultMinSystemReserveBytes = uint64(2 * 1024 * 1024 * 1024) // 2 GB minimum reserve
)

var (
	boostMemMu      sync.RWMutex
	getSystemMemory = process.GetSystemMemory
)

func querySystemMemory() (process.MemoryInfo, error) {
	boostMemMu.RLock()
	fn := getSystemMemory
	boostMemMu.RUnlock()
	if fn != nil {
		return fn()
	}
	return process.GetSystemMemory()
}

// SetSystemMemoryProviderForTest overrides the memory provider function for unit testing.
// Returns a cleanup function that restores the previous provider.
func SetSystemMemoryProviderForTest(fn func() (process.MemoryInfo, error)) func() {
	boostMemMu.Lock()
	prev := getSystemMemory
	getSystemMemory = fn
	boostMemMu.Unlock()
	return func() {
		boostMemMu.Lock()
		getSystemMemory = prev
		boostMemMu.Unlock()
	}
}

// BoostProgress details the current state and progress metrics of the RAM Boost loader.
type BoostProgress struct {
	State         string `json:"state"`
	LoadedFiles   int    `json:"loaded_files"`
	TotalFiles    int    `json:"total_files"`
	SkippedFiles  int    `json:"skipped_files"`
	LoadedBytes   int64  `json:"loaded_bytes"`
	TotalBytes    int64  `json:"total_bytes"`
	SpeedBytesSec int64  `json:"speed_bytes_sec"`
	ResidentBytes int64  `json:"resident_bytes"`
	PendingBytes  int64  `json:"pending_bytes"`
	IsPausedByFG  bool   `json:"is_paused_by_fg"`
	Done          bool   `json:"done"`
	Message       string `json:"message,omitempty"`
}

// ForegroundWaiter is an interface implemented by telemetry.Stats for active battle QoS yielding.
type ForegroundWaiter interface {
	IsForegroundIdle() bool
	WaitForegroundIdle(stopChan <-chan struct{}) bool
}

type boostCandidate struct {
	filePath string
	ns       string
	cleanKey string
	ramKey   string
	fileSize int64
	recency  int64
	version  int64
	tier     int
}

const boostReadWorkers = 4

// DisableBoost deactivates RAM Boost, clears all items from the Resident Pool, releases occupied
// resident memory back to the SLRU cache, and restores the SLRU max capacity.
func (m *Manager) DisableBoost() {
	if m.residentPool != nil {
		m.residentPool.Disable(0)
	}
	if m.ramBudget != nil {
		m.ramBudget.ResetResident(m.ramCache)
	}
}

// PrewarmBoostPoolWithProgress performs a single-worker, sequential disk snapshot scan
// and preloads cache assets into the independent Resident Pool.
//
// It obeys the following invariants:
// 1. Filesystem scan excludes .ext, .tmp.*, and .quarantine files.
// 2. Yields immediately when foreground game requests or APIs are active (via fg).
// 3. Halts loading if available host physical memory drops to or below the safety threshold (2 GB).
// 4. Halts loading with state=partial if total configured RAM budget is exhausted.
// 5. Cancels promptly at single-file boundaries when cancelCh is triggered.
func (m *Manager) PrewarmBoostPoolWithProgress(fg ForegroundWaiter, progressCb func(p BoostProgress), cancelCh <-chan struct{}) BoostProgress {
	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()

	workerGen := m.residentPool.NextGeneration()
	m.residentPool.ResetForGen(workerGen)

	var candidates []boostCandidate
	var totalPayloadBytes int64
	cancelledDuringScan := false
	disabledDuringScan := false

	// Pass 1: Disk Cache Discovery & Filtration
	// Prefer in-memory metadata snapshot (.gbf_meta_index.v1) to skip costly full-disk
	// WalkDir when available; fall back to WalkDir for unindexed cache directories.
	m.waitMetaIndexLoad(1500 * time.Millisecond)

	versionSet := make(map[int64]struct{}, 32)
	usedMetaIndex := false
	if m.metaIndexLoaded.Load() && m.diskMetaCount() > 0 {
		usedMetaIndex = true
		for i := range m.diskMetaShards {
			if m.residentPool.Generation() != workerGen || !m.residentPool.Enabled() {
				disabledDuringScan = true
				break
			}
			select {
			case <-cancelCh:
				cancelledDuringScan = true
			default:
			}
			if cancelledDuringScan {
				break
			}
			shard := &m.diskMetaShards[i]
			shard.mu.RLock()
			for ramKey, meta := range shard.items {
				if !meta.exists || !meta.hasMeta || meta.size <= 0 {
					continue
				}
				ns := sanitizeNamespace(meta.namespace)
				cleanKey := ramKey
				if ns != "gbf" {
					prefix := ns + "\x00"
					if strings.HasPrefix(ramKey, prefix) {
						cleanKey = ramKey[len(prefix):]
					}
				}
				filePath, ok := m.resolvePathWithNamespace(ns, cleanKey)
				if !ok {
					continue
				}
				ver := extractWarmupVersion(strings.ToLower(cleanKey))
				if ver > 0 {
					versionSet[ver] = struct{}{}
				}
				recency := meta.mtimeNano
				if atNano := meta.accessTime * int64(time.Second); atNano > recency {
					recency = atNano
				}
				candidates = append(candidates, boostCandidate{
					filePath: filePath,
					ns:       ns,
					cleanKey: cleanKey,
					ramKey:   ramKey,
					fileSize: meta.size,
					recency:  recency,
					version:  ver,
				})
				totalPayloadBytes += meta.size
			}
			shard.mu.RUnlock()
		}
	}

	if !usedMetaIndex {
		_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
			if m.residentPool.Generation() != workerGen || !m.residentPool.Enabled() {
				disabledDuringScan = true
				return filepath.SkipAll
			}
			select {
			case <-cancelCh:
				cancelledDuringScan = true
				return filepath.SkipAll
			default:
			}
			if err != nil || d.IsDir() {
				return nil
			}
			baseName := d.Name()
			if baseName == RootMarkerFileName || baseName == MetaIndexFileName || config.IsSystemJunk(baseName) {
				return nil
			}
			// Rule VII: Strictly exclude .ext, .tmp.*, .quarantine
			if strings.HasSuffix(p, ".ext") || strings.Contains(p, ".tmp.") || IsAcceleratorQuarantineFile(p) {
				return nil
			}

			ns, cleanKey, ramKey, ok := resolveDiskFileOwner(base, p)
			if !ok {
				return nil
			}

			info, err := d.Info()
			if err != nil || info.Size() == 0 {
				return nil
			}

			ver := extractWarmupVersion(strings.ToLower(cleanKey))
			if ver > 0 {
				versionSet[ver] = struct{}{}
			}
			candidates = append(candidates, boostCandidate{
				filePath: p,
				ns:       ns,
				cleanKey: cleanKey,
				ramKey:   ramKey,
				fileSize: info.Size(),
				recency:  info.ModTime().UnixNano(),
				version:  ver,
			})
			totalPayloadBytes += info.Size()
			return nil
		})
	}

	if len(candidates) > 1 && !disabledDuringScan && !cancelledDuringScan {
		topVersions := make(map[int64]bool, 3)
		if len(versionSet) > 0 {
			vers := make([]int64, 0, len(versionSet))
			for v := range versionSet {
				vers = append(vers, v)
			}
			sort.Slice(vers, func(i, j int) bool { return vers[i] > vers[j] })
			for i := 0; i < len(vers) && i < 3; i++ {
				topVersions[vers[i]] = true
			}
		}
		for i := range candidates {
			candidates[i].tier = classifyWarmupTier(strings.ToLower(candidates[i].cleanKey), candidates[i].version, topVersions)
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].tier != candidates[j].tier {
				return candidates[i].tier > candidates[j].tier
			}
			if candidates[i].version != candidates[j].version {
				return candidates[i].version > candidates[j].version
			}
			if candidates[i].fileSize != candidates[j].fileSize {
				return candidates[i].fileSize < candidates[j].fileSize
			}
			return candidates[i].cleanKey < candidates[j].cleanKey
		})
	}

	if disabledDuringScan || m.residentPool.Generation() != workerGen || !m.residentPool.Enabled() {
		res := BoostProgress{
			State:   BoostStateDisabled,
			Done:    true,
			Message: "RAM Boost 已关闭",
		}
		if progressCb != nil {
			progressCb(res)
		}
		return res
	}

	if cancelledDuringScan {
		res := BoostProgress{
			State:   BoostStateCancelled,
			Done:    true,
			Message: "任务已取消",
		}
		if progressCb != nil {
			progressCb(res)
		}
		return res
	}

	totalCandidates := len(candidates)
	if totalCandidates == 0 {
		res := BoostProgress{
			State:        BoostStateCompleted,
			TotalFiles:   0,
			TotalBytes:   0,
			SkippedFiles: 0,
			Done:         true,
			Message:      "没有需要载入的缓存文件",
		}
		if progressCb != nil {
			progressCb(res)
		}
		return res
	}

	state := BoostStateLoading
	if progressCb != nil {
		progressCb(BoostProgress{
			State:        state,
			TotalFiles:   totalCandidates,
			TotalBytes:   totalPayloadBytes,
			PendingBytes: totalPayloadBytes,
			Done:         false,
		})
	}

	loadedFiles := 0
	loadedBytes := int64(0)
	invalidFiles := 0
	lastReportTime := time.Now()
	startTime := time.Now()
	batchSize := 48

	candidateIdx := 0
	stoppedByBudget := false

	for candidateIdx < totalCandidates {
		// a. Check if generation was invalidated or boost was disabled externally
		if m.residentPool.Generation() != workerGen || !m.residentPool.Enabled() {
			state = BoostStateDisabled
			break
		}

		// b. Check cancellation
		select {
		case <-cancelCh:
			state = BoostStateCancelled
			break
		default:
		}
		if state == BoostStateCancelled {
			break
		}

		// c. Foreground QoS check
		if fg != nil && !fg.IsForegroundIdle() {
			if progressCb != nil {
				_, curResBytes := m.residentPool.Stats()
				pending := totalPayloadBytes - loadedBytes
				if pending < 0 {
					pending = 0
				}
				progressCb(BoostProgress{
					State:         BoostStateLoading,
					LoadedFiles:   loadedFiles,
					TotalFiles:    totalCandidates,
					SkippedFiles:  invalidFiles,
					LoadedBytes:   loadedBytes,
					TotalBytes:    totalPayloadBytes,
					ResidentBytes: curResBytes,
					PendingBytes:  pending,
					IsPausedByFG:  true,
					Done:          false,
				})
			}
			if !fg.WaitForegroundIdle(cancelCh) {
				state = BoostStateCancelled
				break
			}
		}

		// d. Memory guard check (host physical RAM Available threshold)
		if memInfo, err := querySystemMemory(); err == nil && memInfo.AvailableBytes > 0 {
			if memInfo.AvailableBytes <= defaultMinSystemReserveBytes {
				state = BoostStateMemoryGuardStopped
				break
			}
		}

		// e. Total unified budget check before batch
		if m.ramBudget != nil && m.ramBudget.RemainingResidentBudget() <= 0 {
			state = BoostStatePartial
			stoppedByBudget = true
			break
		}

		// f. Process batch with bounded concurrent disk readers while keeping
		// budget accounting and ResidentPool commits strictly serialized.
		batchEnd := candidateIdx + batchSize
		if batchEnd > totalCandidates {
			batchEnd = totalCandidates
		}

		type boostJob struct {
			idx          int
			c            boostCandidate
			acquireBytes int64
			tok          opToken
		}
		type boostReadResult struct {
			item      *CacheItem
			err       error
			yieldedFG bool
			aborted   bool
		}

		jobs := make([]boostJob, 0, batchEnd-candidateIdx)
		stoppedByBudget = false
		for i := candidateIdx; i < batchEnd; i++ {
			if m.residentPool.Generation() != workerGen || !m.residentPool.Enabled() {
				state = BoostStateDisabled
				break
			}
			select {
			case <-cancelCh:
				state = BoostStateCancelled
			default:
			}
			if state == BoostStateCancelled {
				break
			}
			if fg != nil && !fg.IsForegroundIdle() {
				break
			}

			c := candidates[i]
			shard := m.residentPool.getShard(c.ramKey)
			existing, exists := shard.get(c.ramKey)
			if exists && existing != nil && existing.Size == c.fileSize {
				candidateIdx = i + 1
				loadedFiles++
				loadedBytes += existing.Size
				continue
			}

			var acquireBytes int64 = c.fileSize
			if exists && existing != nil {
				acquireBytes = c.fileSize - existing.Size
			}
			if acquireBytes > 0 && m.ramBudget != nil {
				if !m.residentPool.TryAcquireBudget(acquireBytes, workerGen) {
					state = BoostStatePartial
					stoppedByBudget = true
					break
				}
			}

			candidateIdx = i + 1
			jobs = append(jobs, boostJob{
				idx:          i,
				c:            c,
				acquireBytes: acquireBytes,
				tok:          m.acquireOpToken(c.ns, c.cleanKey),
			})
		}

		if len(jobs) > 0 {
			results := make([]boostReadResult, len(jobs))
			runJob := func(jIdx int) {
				job := jobs[jIdx]
				if m.residentPool.Generation() != workerGen || !m.residentPool.Enabled() {
					results[jIdx] = boostReadResult{aborted: true}
					return
				}
				select {
				case <-cancelCh:
					results[jIdx] = boostReadResult{aborted: true}
					return
				default:
				}
				if fg != nil && !fg.IsForegroundIdle() {
					results[jIdx] = boostReadResult{yieldedFG: true}
					return
				}

				item, err := m.loadAndValidateDiskItemWithToken(job.c.ns, job.c.cleanKey, job.c.filePath, job.tok)
				if err != nil || item == nil {
					results[jIdx] = boostReadResult{err: err}
					return
				}
				if len(item.Data) == 0 {
					data, rErr := os.ReadFile(job.c.filePath)
					if rErr != nil || int64(len(data)) != item.Size {
						if rErr == nil {
							rErr = fmt.Errorf("size mismatch")
						}
						results[jIdx] = boostReadResult{err: rErr}
						return
					}
					item.Data = data
				}
				results[jIdx] = boostReadResult{item: item}
			}

			workers := boostReadWorkers
			if len(jobs) < workers {
				workers = len(jobs)
			}
			if workers <= 1 {
				for jIdx := range jobs {
					runJob(jIdx)
				}
			} else {
				var nextJob atomic.Int64
				var wg sync.WaitGroup
				wg.Add(workers)
				for w := 0; w < workers; w++ {
					go func() {
						defer wg.Done()
						for {
							jIdx := int(nextJob.Add(1) - 1)
							if jIdx >= len(jobs) {
								return
							}
							runJob(jIdx)
						}
					}()
				}
				wg.Wait()
			}

			// Commit results sequentially in deterministic priority order.
			// Note: stoppedByBudget may already be true from the prepare loop when a
			// subsequent candidate exceeded budget; all jobs already appended to jobs
			// successfully reserved budget and must still be committed unless a file
			// grew past budget during actualDelta reconciliation.
			commitStoppedByBudget := false
			rewindIdx := -1
			for jIdx, job := range jobs {
				res := results[jIdx]
				c := job.c
				acquireBytes := job.acquireBytes
				tok := job.tok

				if m.residentPool.Generation() != workerGen || !m.residentPool.Enabled() {
					if acquireBytes > 0 {
						m.residentPool.ReleaseBudget(acquireBytes, workerGen)
					}
					state = BoostStateDisabled
					continue
				}
				if state == BoostStateDisabled || state == BoostStateCancelled || commitStoppedByBudget {
					if acquireBytes > 0 {
						m.residentPool.ReleaseBudget(acquireBytes, workerGen)
					}
					continue
				}
				select {
				case <-cancelCh:
					if acquireBytes > 0 {
						m.residentPool.ReleaseBudget(acquireBytes, workerGen)
					}
					state = BoostStateCancelled
					continue
				default:
				}
				if res.aborted {
					if acquireBytes > 0 {
						m.residentPool.ReleaseBudget(acquireBytes, workerGen)
					}
					continue
				}
				if res.yieldedFG {
					if acquireBytes > 0 {
						m.residentPool.ReleaseBudget(acquireBytes, workerGen)
					}
					if rewindIdx == -1 {
						rewindIdx = job.idx
					}
					continue
				}
				if res.err != nil || res.item == nil {
					if acquireBytes > 0 {
						m.residentPool.ReleaseBudget(acquireBytes, workerGen)
					}
					invalidFiles++
					continue
				}

				item := res.item
				if item.Size != c.fileSize && m.ramBudget != nil {
					actualDelta := item.Size - c.fileSize
					if actualDelta > 0 {
						if !m.residentPool.TryAcquireBudget(actualDelta, workerGen) {
							if acquireBytes > 0 {
								m.residentPool.ReleaseBudget(acquireBytes, workerGen)
							}
							state = BoostStatePartial
							stoppedByBudget = true
							commitStoppedByBudget = true
							continue
						}
					} else if actualDelta < 0 {
						m.residentPool.ReleaseBudget(-actualDelta, workerGen)
					}
				}

				m.persistMu.RLock()
				tokShard := m.tokenShardByCanon(tok.canonKey)
				tokShard.mu.Lock()
				if !isTokenValidLocked(tokShard, tok, m.generation) {
					tokShard.mu.Unlock()
					m.persistMu.RUnlock()
					m.residentPool.ReleaseBudget(item.Size, workerGen)
					invalidFiles++
					continue
				}
				committed := m.residentPool.SetWithGen(c.ramKey, item, workerGen)
				tokShard.mu.Unlock()
				m.persistMu.RUnlock()

				if !committed {
					m.residentPool.ReleaseBudget(item.Size, workerGen)
					state = BoostStateDisabled
					continue
				}

				loadedFiles++
				loadedBytes += item.Size
			}
			if rewindIdx >= 0 && state == BoostStateLoading && !stoppedByBudget {
				candidateIdx = rewindIdx
			}
		}

		if stoppedByBudget || state == BoostStateCancelled || state == BoostStateDisabled {
			break
		}

		// Sync SLRU capacity with updated budget after each batch
		if m.ramBudget != nil && m.residentPool.Enabled() && m.residentPool.Generation() == workerGen {
			m.ramBudget.SyncSLRU(m.ramCache)
			// Re-verify unified budget after batch commit
			if m.ramBudget.RemainingResidentBudget() <= 0 && candidateIdx < totalCandidates {
				state = BoostStatePartial
				stoppedByBudget = true
				break
			}
		}

		// Throttled progress report
		now := time.Now()
		if progressCb != nil && (now.Sub(lastReportTime) >= 150*time.Millisecond || candidateIdx >= totalCandidates) {
			lastReportTime = now
			elapsedSec := now.Sub(startTime).Seconds()
			speed := int64(0)
			if elapsedSec > 0 {
				speed = int64(float64(loadedBytes) / elapsedSec)
			}
			_, curResBytes := m.residentPool.Stats()
			pending := totalPayloadBytes - loadedBytes
			if pending < 0 {
				pending = 0
			}
			progressCb(BoostProgress{
				State:         BoostStateLoading,
				LoadedFiles:   loadedFiles,
				TotalFiles:    totalCandidates,
				SkippedFiles:  invalidFiles,
				LoadedBytes:   loadedBytes,
				TotalBytes:    totalPayloadBytes,
				SpeedBytesSec: speed,
				ResidentBytes: curResBytes,
				PendingBytes:  pending,
				IsPausedByFG:  false,
				Done:          false,
			})
		}
	}

	// If disabled or invalidated during prewarm, exit quietly without overriding disabled state
	if state == BoostStateDisabled || !m.residentPool.Enabled() || m.residentPool.Generation() != workerGen {
		return BoostProgress{
			State:   BoostStateDisabled,
			Done:    true,
			Message: "Boost已关闭",
		}
	}

	if state == BoostStateLoading {
		if stoppedByBudget {
			state = BoostStatePartial
		} else {
			state = BoostStateCompleted
		}
	}

	if m.ramBudget != nil && m.residentPool.Enabled() && m.residentPool.Generation() == workerGen {
		m.ramBudget.SyncSLRU(m.ramCache)
	}

	elapsedSec := time.Since(startTime).Seconds()
	speed := int64(0)
	if elapsedSec > 0 {
		speed = int64(float64(loadedBytes) / elapsedSec)
	}
	_, finalResBytes := m.residentPool.Stats()

	pending := totalPayloadBytes - loadedBytes
	if pending < 0 {
		pending = 0
	}

	msg := ""
	if invalidFiles > 0 {
		if state == BoostStateCompleted {
			msg = fmt.Sprintf("已载入 %d 项有效素材（跳过 %d 个损坏/无效文件）", loadedFiles, invalidFiles)
		} else {
			msg = fmt.Sprintf("已跳过 %d 个损坏/无效文件", invalidFiles)
		}
	} else if state == BoostStateCompleted {
		msg = fmt.Sprintf("已全量载入 %d 项有效素材", loadedFiles)
	}

	finalProgress := BoostProgress{
		State:         state,
		LoadedFiles:   loadedFiles,
		TotalFiles:    totalCandidates,
		SkippedFiles:  invalidFiles,
		LoadedBytes:   loadedBytes,
		TotalBytes:    totalPayloadBytes,
		SpeedBytesSec: speed,
		ResidentBytes: finalResBytes,
		PendingBytes:  pending,
		IsPausedByFG:  false,
		Done:          true,
		Message:       msg,
	}
	if progressCb != nil {
		progressCb(finalProgress)
	}
	return finalProgress
}
