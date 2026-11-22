package cache

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gbf-proxy/config"
)

// MetaIndexFileName is the compact binary metadata snapshot stored at the cache root
// to accelerate cold-start metadata lookups and tiered RAM warmup without reading
// tens of thousands of individual .ext JSON files.
const MetaIndexFileName = ".gbf_meta_index.v1"

const (
	MetaIndexVersion1     = 1
	MetaIndexVersion2     = 2
	FlagMetaIndexComplete = 1 << 0
)

var metaIndexMagic = [8]byte{'G', 'B', 'F', 'I', 'D', 'X', '0', '1'}

const metaIndexMaxFileBytes = 128 * 1024 * 1024

func writeStringLP(buf *bytes.Buffer, s string) {
	if len(s) > 65535 {
		s = s[:65535]
	}
	var lenBuf [2]byte
	binary.LittleEndian.PutUint16(lenBuf[:], uint16(len(s)))
	buf.Write(lenBuf[:])
	buf.WriteString(s)
}

func readStringLP(r *bytes.Reader) (string, error) {
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return "", err
	}
	n := int(binary.LittleEndian.Uint16(lenBuf[:]))
	if n == 0 {
		return "", nil
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", err
	}
	return string(b), nil
}

func (m *Manager) loadMetaIndexFromBase(base string, expectedGen uint64) int {
	if base == "" {
		return 0
	}
	indexPath := filepath.Join(base, MetaIndexFileName)
	fi, err := os.Stat(indexPath)
	if err != nil || fi.IsDir() || fi.Size() < 20 || fi.Size() > metaIndexMaxFileBytes {
		return 0
	}
	raw, err := os.ReadFile(indexPath)
	if err != nil || len(raw) < 20 {
		return 0
	}
	if !bytes.Equal(raw[:8], metaIndexMagic[:]) {
		return 0
	}

	payloadEnd := len(raw) - 4
	expectedCRC := binary.LittleEndian.Uint32(raw[payloadEnd:])
	if crc32.ChecksumIEEE(raw[:payloadEnd]) != expectedCRC {
		return 0
	}

	version := binary.LittleEndian.Uint32(raw[8:12])
	var count uint32
	var flags uint32
	var payloadOffset int

	switch version {
	case MetaIndexVersion1:
		count = binary.LittleEndian.Uint32(raw[12:16])
		payloadOffset = 16
		flags = 0
	case MetaIndexVersion2:
		if payloadEnd < 20 {
			return 0
		}
		flags = binary.LittleEndian.Uint32(raw[12:16])
		count = binary.LittleEndian.Uint32(raw[16:20])
		payloadOffset = 20
	default:
		return 0
	}

	m.metaIndexComplete.Store((flags & FlagMetaIndexComplete) != 0)
	r := bytes.NewReader(raw[payloadOffset:payloadEnd])

	loaded := 0
	for i := uint32(0); i < count; i++ {
		ramKey, err := readStringLP(r)
		if err != nil {
			break
		}
		ns, err := readStringLP(r)
		if err != nil {
			break
		}
		var nums [24]byte
		if _, err := io.ReadFull(r, nums[:]); err != nil {
			break
		}
		mtimeNano := int64(binary.LittleEndian.Uint64(nums[0:8]))
		size := int64(binary.LittleEndian.Uint64(nums[8:16]))
		accessTime := int64(binary.LittleEndian.Uint64(nums[16:24]))

		ct, err := readStringLP(r)
		if err != nil {
			break
		}
		ce, err := readStringLP(r)
		if err != nil {
			break
		}
		etag, err := readStringLP(r)
		if err != nil {
			break
		}
		lm, err := readStringLP(r)
		if err != nil {
			break
		}
		flags, err := r.ReadByte()
		if err != nil {
			break
		}

		m.persistMu.RLock()
		genMismatch := m.generation != expectedGen
		m.persistMu.RUnlock()
		if genMismatch {
			return loaded
		}

		shard := m.diskMetaShard(ramKey)
		shard.mu.Lock()
		if _, exists := shard.items[ramKey]; !exists && len(shard.items) < diskMetaShardMaxItems {
			shard.items[ramKey] = diskMetaEntry{
				namespace:       ns,
				mtimeNano:       mtimeNano,
				size:            size,
				accessTime:      accessTime,
				contentType:     ct,
				contentEncoding: ce,
				etag:            etag,
				lastModified:    lm,
				exists:          flags&1 != 0,
				hasMeta:         flags&2 != 0,
				verified:        flags&4 != 0,
			}
			loaded++
		}
		shard.mu.Unlock()
	}
	if loaded > 0 {
		m.metaIndexLoaded.Store(true)
	}
	return loaded
}

func (m *Manager) startAsyncMetaIndexLoad() {
	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()
	m.persistMu.RLock()
	gen := m.generation
	m.persistMu.RUnlock()
	m.startAsyncMetaIndexLoadWithBase(base, gen)
}

func (m *Manager) startAsyncMetaIndexLoadWithBase(base string, gen uint64) {
	done := make(chan struct{})
	m.metaIndexLoadMu.Lock()
	m.metaIndexLoadDone = done
	m.metaIndexLoadMu.Unlock()

	go func() {
		defer close(done)
		m.loadMetaIndexFromBase(base, gen)
	}()
}

func (m *Manager) waitMetaIndexLoad(timeout time.Duration) {
	m.metaIndexLoadMu.Lock()
	done := m.metaIndexLoadDone
	m.metaIndexLoadMu.Unlock()
	if done == nil {
		return
	}
	if timeout <= 0 {
		<-done
		return
	}
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// SaveMetaIndex persists an atomic binary snapshot of in-memory disk metadata
// shards to MetaIndexFileName inside cacheBase.
func (m *Manager) SaveMetaIndex() error {
	return m.saveMetaIndex(true)
}

func (m *Manager) saveMetaIndex(blocking bool) error {
	if blocking {
		m.metaIndexSaveMu.Lock()
	} else if !m.metaIndexSaveMu.TryLock() {
		return nil
	}
	defer m.metaIndexSaveMu.Unlock()

	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()
	if base == "" {
		return nil
	}
	if _, err := os.Stat(base); err != nil {
		return nil
	}

	// Clear dirty flag before snapshotting shards so any concurrent write or delete
	// occurring after our snapshot loop re-marks metaIndexDirty = true.
	m.metaIndexDirty.Store(false)

	type kvEntry struct {
		key string
		val diskMetaEntry
	}
	var all []kvEntry
	for i := range m.diskMetaShards {
		shard := &m.diskMetaShards[i]
		shard.mu.RLock()
		if len(shard.items) > 0 {
			for k, v := range shard.items {
				if v.exists && v.hasMeta && v.size > 0 {
					all = append(all, kvEntry{key: k, val: v})
				}
			}
		}
		shard.mu.RUnlock()
	}

	if len(all) == 0 {
		return nil
	}

	var buf bytes.Buffer
	buf.Grow(20 + len(all)*96 + 4)
	buf.Write(metaIndexMagic[:])
	var hdr [12]byte
	binary.LittleEndian.PutUint32(hdr[0:4], MetaIndexVersion2)
	var flags uint32
	if m.metaIndexComplete.Load() {
		flags |= FlagMetaIndexComplete
	}
	binary.LittleEndian.PutUint32(hdr[4:8], flags)
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(all)))
	buf.Write(hdr[:])

	for _, item := range all {
		writeStringLP(&buf, item.key)
		writeStringLP(&buf, item.val.namespace)
		var nums [24]byte
		binary.LittleEndian.PutUint64(nums[0:8], uint64(item.val.mtimeNano))
		binary.LittleEndian.PutUint64(nums[8:16], uint64(item.val.size))
		binary.LittleEndian.PutUint64(nums[16:24], uint64(item.val.accessTime))
		buf.Write(nums[:])
		writeStringLP(&buf, item.val.contentType)
		writeStringLP(&buf, item.val.contentEncoding)
		writeStringLP(&buf, item.val.etag)
		writeStringLP(&buf, item.val.lastModified)
		var flags byte
		if item.val.exists {
			flags |= 1
		}
		if item.val.hasMeta {
			flags |= 2
		}
		if item.val.verified {
			flags |= 4
		}
		_ = buf.WriteByte(flags)
	}

	crc := crc32.ChecksumIEEE(buf.Bytes())
	var crcBuf [4]byte
	binary.LittleEndian.PutUint32(crcBuf[:], crc)
	buf.Write(crcBuf[:])

	indexPath := filepath.Join(base, MetaIndexFileName)
	tmpPath := fmt.Sprintf("%s.tmp.%d.%d.%d", indexPath, os.Getpid(), time.Now().UnixNano(), tmpFileSeq.Add(1))

	m.persistMu.RLock()
	defer m.persistMu.RUnlock()
	m.mu.RLock()
	curBase := m.cacheBase
	m.mu.RUnlock()
	if curBase != base {
		return nil
	}

	tok := opToken{gen: m.generation}
	registered := m.registerTempFileTokenLocked(tmpPath, tok)

	if err := os.WriteFile(tmpPath, buf.Bytes(), 0644); err != nil {
		m.metaIndexDirty.Store(true)
		_ = os.Remove(tmpPath)
		if registered {
			m.popTempFileToken(tmpPath)
		}
		return err
	}
	if err := renameWithRetry(tmpPath, indexPath, 3); err != nil {
		m.metaIndexDirty.Store(true)
		_ = os.Remove(tmpPath)
		if registered {
			m.popTempFileToken(tmpPath)
		}
		return err
	}
	if registered {
		m.popTempFileToken(tmpPath)
	}
	return nil
}

func (m *Manager) flushMetaIndexOnClose() {
	m.metaIndexSaveMu.Lock()
	dirty := m.metaIndexDirty.Load()
	m.metaIndexSaveMu.Unlock()
	if dirty {
		_ = m.SaveMetaIndex()
	}
}

func (m *Manager) markMetaIndexDirty() {
	m.metaIndexDirty.Store(true)
	if m.metaWritesSinceFlush.Add(1)%512 == 0 {
		select {
		case <-m.stopPersist:
			return
		default:
		}
		if m.metaPeriodicFlushInFlight.CompareAndSwap(false, true) {
			go func() {
				defer m.metaPeriodicFlushInFlight.Store(false)
				_ = m.saveMetaIndex(false)
			}()
		}
	}
}

func (m *Manager) diskMetaCount() int {
	total := 0
	for i := range m.diskMetaShards {
		shard := &m.diskMetaShards[i]
		shard.mu.RLock()
		total += len(shard.items)
		shard.mu.RUnlock()
	}
	return total
}

// StartBackgroundMetaIndexBackfill schedules a low-priority, foreground-yielding
// background scan to build .gbf_meta_index.v1 for pre-existing disk caches (e.g.
// imported ACGPower cache directories) when no metadata snapshot is present yet.
// Safe to call multiple times; at most one backfill runs per cache directory generation.
func (m *Manager) StartBackgroundMetaIndexBackfill(fg ForegroundWaiter, initialDelay time.Duration) {
	if m == nil {
		return
	}
	select {
	case <-m.stopPersist:
		return
	default:
	}
	if !m.metaBackfillStarted.CompareAndSwap(false, true) {
		return
	}

	m.metaBackfillWg.Add(1)
	go func() {
		defer m.metaBackfillWg.Done()

		if initialDelay > 0 {
			timer := time.NewTimer(initialDelay)
			select {
			case <-m.stopPersist:
				timer.Stop()
				return
			case <-timer.C:
			}
		}

		m.waitMetaIndexLoad(5 * time.Second)
		if m.isMetaIndexComplete() {
			return
		}
		m.BackfillMetaIndex(fg, m.stopPersist)
	}()
}

// BackfillMetaIndex walks the active cache directory reading only .ext metadata files
// (never asset bodies) to populate in-memory diskMetaShards and persist .gbf_meta_index.v1.
// It yields immediately when foreground traffic is active and aborts cleanly on cancelCh
// or cache generation changes. Returns the number of newly indexed entries.
func (m *Manager) BackfillMetaIndex(fg ForegroundWaiter, cancelCh <-chan struct{}) int {
	if m == nil {
		return 0
	}
	m.persistMu.RLock()
	startGen := m.generation
	m.persistMu.RUnlock()

	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()
	if base == "" {
		return 0
	}
	if fi, err := os.Stat(base); err != nil || !fi.IsDir() {
		return 0
	}

	added := 0
	aborted := false
	_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if cancelCh != nil {
			select {
			case <-cancelCh:
				aborted = true
				return filepath.SkipAll
			default:
			}
		}
		m.persistMu.RLock()
		genChanged := m.generation != startGen
		m.persistMu.RUnlock()
		if genChanged {
			aborted = true
			return filepath.SkipAll
		}
		if fg != nil && !fg.IsForegroundIdle() {
			if !fg.WaitForegroundIdle(cancelCh) {
				aborted = true
				return filepath.SkipAll
			}
		}

		if err != nil || d.IsDir() {
			return nil
		}
		if d.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return nil
		}
		baseName := d.Name()
		if baseName == RootMarkerFileName || baseName == MetaIndexFileName || config.IsSystemJunk(baseName) {
			return nil
		}
		if strings.HasSuffix(p, ".ext") || strings.Contains(p, ".tmp.") || IsAcceleratorQuarantineFile(p) {
			return nil
		}

		info, infoErr := d.Info()
		if infoErr != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			return nil
		}
		ns, cleanKey, ramKey, ok := resolveDiskFileOwner(base, p)
		if !ok {
			return nil
		}

		mtimeNano := info.ModTime().UnixNano()
		size := info.Size()
		if existing, exists := m.getDiskMeta(ramKey); exists && existing.hasMeta && existing.verified && existing.mtimeNano == mtimeNano && existing.size == size {
			return nil
		}

		metaBytes, rErr := os.ReadFile(p + ".ext")
		if rErr != nil {
			return nil
		}
		meta, parseOK := parseExtFile(metaBytes)
		if !parseOK || !meta.Verified || meta.Size != size || meta.MTimeNano != mtimeNano || !isDiskFileOwnedByNamespace(ns, cleanKey, meta, true) {
			return nil
		}

		ct := resolveContentType(meta.ContentType, p)
		etag := meta.ETag
		if etag == "" {
			etag = fmt.Sprintf("\"%x-%x\"", mtimeNano/1e9, size)
		}
		at := meta.AccessTime
		if at == 0 {
			at = info.ModTime().Unix()
		}

		m.persistMu.RLock()
		if m.generation != startGen {
			m.persistMu.RUnlock()
			aborted = true
			return filepath.SkipAll
		}
		shard := m.diskMetaShard(ramKey)
		shard.mu.Lock()
		if cur, exists := shard.items[ramKey]; (!exists || !cur.hasMeta) && len(shard.items) < diskMetaShardMaxItems {
			shard.items[ramKey] = diskMetaEntry{
				namespace:       ns,
				mtimeNano:       mtimeNano,
				size:            size,
				accessTime:      at,
				contentType:     ct,
				contentEncoding: meta.ContentEncoding,
				etag:            etag,
				lastModified:    meta.LastModified,
				exists:          true,
				hasMeta:         true,
				verified:        true,
			}
			added++
		}
		shard.mu.Unlock()
		m.persistMu.RUnlock()
		return nil
	})

	if added > 0 {
		m.metaIndexDirty.Store(true)
	}
	if !aborted {
		m.metaIndexLoaded.Store(true)
		m.metaIndexComplete.Store(true)
		_ = m.saveMetaIndex(false)
	}
	return added
}
