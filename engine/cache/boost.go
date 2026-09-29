package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
}

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

	var candidates []boostCandidate
	var totalPayloadBytes int64
	cancelledDuringScan := false

	// Pass 1: Disk Cache Discovery & Filtration
	_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		select {
		case <-cancelCh:
			cancelledDuringScan = true
			return filepath.SkipAll
		default:
		}
		if err != nil || d.IsDir() {
			return nil
		}
		// Rule VII: Strictly exclude .ext, .tmp.*, .quarantine
		if strings.HasSuffix(p, ".ext") || strings.Contains(p, ".tmp.") || strings.Contains(p, ".quarantine") {
			return nil
		}

		ns, cleanKey, ramKey, ok := extractNamespaceAndKey(base, p)
		if !ok {
			return nil
		}

		info, err := d.Info()
		if err != nil || info.Size() == 0 {
			return nil
		}

		candidates = append(candidates, boostCandidate{
			filePath: p,
			ns:       ns,
			cleanKey: cleanKey,
			ramKey:   ramKey,
			fileSize: info.Size(),
		})
		totalPayloadBytes += info.Size()
		return nil
	})

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

	workerGen := m.residentPool.NextGeneration()
	m.residentPool.ResetForGen(workerGen)

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

		// f. Process single sequential batch
		batchEnd := candidateIdx + batchSize
		if batchEnd > totalCandidates {
			batchEnd = totalCandidates
		}

		stoppedByBudget = false
		for i := candidateIdx; i < batchEnd; i++ {
			// Check if generation invalidated or boost disabled externally before reading
			if m.residentPool.Generation() != workerGen || !m.residentPool.Enabled() {
				state = BoostStateDisabled
				break
			}

			// Check cancel at single-file boundary
			select {
			case <-cancelCh:
				state = BoostStateCancelled
				break
			default:
			}
			if state == BoostStateCancelled {
				break
			}

			// Check fg at single-file boundary
			if fg != nil && !fg.IsForegroundIdle() {
				break // yield to outer loop to trigger WaitForegroundIdle
			}

			c := candidates[i]
			candidateIdx = i + 1

			shard := m.residentPool.getShard(c.ramKey)
			existing, exists := shard.get(c.ramKey)
			if exists && existing != nil && existing.Size == c.fileSize {
				// Already resident in snapshot with identical size: count as loaded without re-reading or double-acquiring budget
				loadedFiles++
				loadedBytes += existing.Size
				continue
			}

			var acquireBytes int64 = c.fileSize
			if exists && existing != nil {
				acquireBytes = c.fileSize - existing.Size
			}

			// Check and reserve budget before reading file
			if acquireBytes > 0 && m.ramBudget != nil {
				if !m.residentPool.TryAcquireBudget(acquireBytes, workerGen) {
					state = BoostStatePartial
					stoppedByBudget = true
					break
				}
			}

			// Unified validation and loading path (Section IV)
			item, err := m.loadAndValidateDiskItem(c.ns, c.cleanKey, c.filePath)

			// Re-verify generation/enabled immediately after file read
			if m.residentPool.Generation() != workerGen || !m.residentPool.Enabled() {
				if acquireBytes > 0 {
					m.residentPool.ReleaseBudget(acquireBytes, workerGen)
				}
				state = BoostStateDisabled
				break
			}

			if err != nil || item == nil {
				if acquireBytes > 0 {
					m.residentPool.ReleaseBudget(acquireBytes, workerGen)
				}
				invalidFiles++
				continue
			}

			// Adjust difference if file size on disk differs from actual bytes loaded
			if item.Size != c.fileSize && m.ramBudget != nil {
				actualDelta := item.Size - c.fileSize
				if actualDelta > 0 {
					if !m.residentPool.TryAcquireBudget(actualDelta, workerGen) {
						if acquireBytes > 0 {
							m.residentPool.ReleaseBudget(acquireBytes, workerGen)
						}
						state = BoostStatePartial
						stoppedByBudget = true
						break
					}
				} else if actualDelta < 0 {
					m.residentPool.ReleaseBudget(-actualDelta, workerGen)
				}
			}

			// Commit to resident pool using workerGen. If generation changed or disabled, SetWithGen returns false.
			if !m.residentPool.SetWithGen(c.ramKey, item, workerGen) {
				m.residentPool.ReleaseBudget(item.Size, workerGen)
				state = BoostStateDisabled
				break
			}

			loadedFiles++
			loadedBytes += item.Size
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
