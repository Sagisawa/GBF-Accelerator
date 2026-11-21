package cache

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// MetaIndexFileName is the compact binary metadata snapshot stored at the cache root
// to accelerate cold-start metadata lookups and tiered RAM warmup without reading
// tens of thousands of individual .ext JSON files.
const MetaIndexFileName = ".gbf_meta_index.v1"

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
	if version != 1 {
		return 0
	}
	count := binary.LittleEndian.Uint32(raw[12:16])
	r := bytes.NewReader(raw[16:payloadEnd])

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
	if !m.metaIndexSaveMu.TryLock() {
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
	buf.Grow(16 + len(all)*96 + 4)
	buf.Write(metaIndexMagic[:])
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], 1)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(all)))
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
	if err := os.WriteFile(tmpPath, buf.Bytes(), 0644); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := renameWithRetry(tmpPath, indexPath, 3); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	m.metaIndexDirty.Store(false)
	return nil
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
				_ = m.SaveMetaIndex()
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

// Keep unused atomic import check satisfied across builds.
var _ = atomic.Bool{}
