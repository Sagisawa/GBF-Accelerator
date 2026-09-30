package cache

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkDiskReadStrategies(b *testing.B) {
	// Cover requested sizes: 16KB, 64KB, 256KB, 1MB, and 4MB (large static asset e.g. BGM/artwork)
	sizes := []int{
		16 * 1024,
		64 * 1024,
		256 * 1024,
		1024 * 1024,
		4096 * 1024,
	}

	for _, size := range sizes {
		size := size
		b.Run(fmt.Sprintf("Size_%dKB", size/1024), func(b *testing.B) {
			tempDir := b.TempDir()
			testFile := filepath.Join(tempDir, "test.dat")
			payload := make([]byte, size)
			for i := range payload {
				payload[i] = byte(i % 251)
			}
			_ = os.WriteFile(testFile, payload, 0644)

			// Pre-flight byte-for-byte exactness verification
			fVer, err := os.Open(testFile)
			if err != nil {
				b.Fatal(err)
			}
			dataVer, errVer := readDiskFile(fVer, int64(size))
			_ = fVer.Close()
			if errVer != nil || len(dataVer) != size || !bytes.Equal(dataVer, payload) {
				b.Fatal("byte exactness verification failed for readDiskFile")
			}

			// Strategy 1: Previous baseline (os.Open + f.Stat + io.ReadAll)
			b.Run("Current_OpenStatReadAll", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(size))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					f, err := os.Open(testFile)
					if err != nil {
						b.Fatal(err)
					}
					fi, err := f.Stat()
					if err != nil {
						_ = f.Close()
						b.Fatal(err)
					}
					_ = fi.Size()
					data, err := io.ReadAll(f)
					_ = f.Close()
					if err != nil || len(data) != size {
						b.Fatal("read mismatch")
					}
				}
			})

			// Strategy 2: Production safe preallocated read (readDiskFile with capacity size+1)
			b.Run("Preallocated_readDiskFile", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(size))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					f, err := os.Open(testFile)
					if err != nil {
						b.Fatal(err)
					}
					fi, err := f.Stat()
					if err != nil {
						_ = f.Close()
						b.Fatal(err)
					}
					sz := fi.Size()
					data, err := readDiskFile(f, sz)
					_ = f.Close()
					if err != nil || len(data) != size {
						b.Fatal("read mismatch")
					}
				}
			})

			// Strategy 3: Unsafe baseline using io.ReadFull (does not handle concurrent file growth or truncation)
			b.Run("Unsafe_ReadFull_Baseline", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(size))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					f, err := os.Open(testFile)
					if err != nil {
						b.Fatal(err)
					}
					fi, err := f.Stat()
					if err != nil {
						_ = f.Close()
						b.Fatal(err)
					}
					sz := fi.Size()
					data := make([]byte, sz)
					_, err = io.ReadFull(f, data)
					_ = f.Close()
					if err != nil || len(data) != size {
						b.Fatal("read mismatch")
					}
				}
			})

			// Strategy 4: Standard library os.ReadFile (re-opens file)
			b.Run("StdLib_ReadFile", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(size))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					data, err := os.ReadFile(testFile)
					if err != nil || len(data) != size {
						b.Fatal("read mismatch")
					}
				}
			})
		})
	}
}
