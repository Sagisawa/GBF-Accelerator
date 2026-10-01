package patcher

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

type ManifestInfo struct {
	PackageName string
	VersionName string
	SplitName   string
	IsSplit     bool
	Strings     []string
}

const (
	MaxManifestSize    = 10 * 1024 * 1024 // 10 MB maximum allowed AndroidManifest.xml
	MaxStringPoolCount = 100000           // 100k maximum strings in AXML string pool
)

// ParseManifest parses an AndroidManifest.xml binary XML (AXML) buffer.
// It extracts the string pool and precisely inspects the <manifest> element attributes
// (package, versionName, split) according to the AXML binary specification.
func ParseManifest(data []byte) (*ManifestInfo, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("data too short for AXML header")
	}
	if len(data) > MaxManifestSize {
		return nil, fmt.Errorf("AXML data exceeds maximum size limit (%d bytes)", MaxManifestSize)
	}
	magic := binary.LittleEndian.Uint32(data[0:4])
	if magic != 0x00080003 {
		return nil, fmt.Errorf("invalid AXML magic: 0x%08X (expected 0x00080003)", magic)
	}

	var stringPool []string
	offset := 8

	// 1. Locate and parse StringPool
	for offset+8 <= len(data) {
		chunkType := binary.LittleEndian.Uint32(data[offset : offset+4])
		chunkSize := binary.LittleEndian.Uint32(data[offset+4 : offset+8])
		if chunkSize < 8 || uint64(offset)+uint64(chunkSize) > uint64(len(data)) {
			break
		}

		if chunkType == 0x001C0001 { // RES_STRING_POOL_TYPE
			spData := data[offset : offset+int(chunkSize)]
			sp, err := parseStringPool(spData)
			if err != nil {
				return nil, err
			}
			stringPool = sp
			offset += int(chunkSize)
			break
		}
		offset += int(chunkSize)
	}

	if len(stringPool) == 0 {
		return nil, fmt.Errorf("AXML StringPool not found or empty")
	}

	info := &ManifestInfo{
		Strings: stringPool,
	}

	// 2. Scan remaining chunks for START_TAG (0x00100102)
	for offset+8 <= len(data) {
		chunkType := binary.LittleEndian.Uint32(data[offset : offset+4])
		chunkSize := binary.LittleEndian.Uint32(data[offset+4 : offset+8])
		if chunkSize < 8 || uint64(offset)+uint64(chunkSize) > uint64(len(data)) {
			break
		}

		if chunkType == 0x00100102 { // RES_XML_START_ELEMENT_TYPE
			chunkData := data[offset : offset+int(chunkSize)]
			if len(chunkData) >= 36 {
				nameIdx := binary.LittleEndian.Uint32(chunkData[20:24])
				attrCount := binary.LittleEndian.Uint16(chunkData[28:30])

				var tagName string
				if int(nameIdx) < len(stringPool) {
					tagName = stringPool[nameIdx]
				}

				if tagName == "manifest" {
					// Parse attributes (each attribute is 20 bytes)
					attrOffset := 36
					for i := 0; i < int(attrCount) && attrOffset+20 <= len(chunkData); i++ {
						attrNameIdx := binary.LittleEndian.Uint32(chunkData[attrOffset+4 : attrOffset+8])
						attrValIdx := binary.LittleEndian.Uint32(chunkData[attrOffset+8 : attrOffset+12])

						var attrName, attrVal string
						if int(attrNameIdx) < len(stringPool) {
							attrName = stringPool[attrNameIdx]
						}
						if int(attrValIdx) < len(stringPool) {
							attrVal = stringPool[attrValIdx]
						}

						switch attrName {
						case "package":
							if attrVal != "" {
								info.PackageName = attrVal
							}
						case "versionName":
							if attrVal != "" {
								info.VersionName = attrVal
							}
						case "split":
							if attrVal != "" {
								info.SplitName = attrVal
								info.IsSplit = true
							}
						}
						attrOffset += 20
					}
					// Found manifest tag, done parsing top-level attributes
					break
				}
			}
		}

		offset += int(chunkSize)
	}

	// Fallback heuristic if manifest tag attributes were not matched directly
	if info.PackageName == "" {
		for _, s := range stringPool {
			if s == "com.dena.skyleap" {
				info.PackageName = s
				break
			}
		}
	}

	return info, nil
}

func parseStringPool(data []byte) ([]string, error) {
	if len(data) < 28 {
		return nil, fmt.Errorf("invalid StringPool header length")
	}
	stringCount := binary.LittleEndian.Uint32(data[8:12])
	flags := binary.LittleEndian.Uint32(data[16:20])
	stringsStart := binary.LittleEndian.Uint32(data[20:24])

	if stringCount > MaxStringPoolCount {
		return nil, fmt.Errorf("StringPool string count %d exceeds maximum limit %d", stringCount, MaxStringPoolCount)
	}

	// Offsets start at byte 28; each offset is 4 bytes. Ensure offsets fit within data.
	if 28+uint64(stringCount)*4 > uint64(len(data)) {
		return nil, fmt.Errorf("StringPool string count %d exceeds available data length", stringCount)
	}
	if stringCount > 0 {
		if uint64(stringsStart) < 28+uint64(stringCount)*4 {
			return nil, fmt.Errorf("StringPool string count %d overlaps strings start offset %d", stringCount, stringsStart)
		}
		if uint64(stringsStart) > uint64(len(data)) {
			return nil, fmt.Errorf("StringPool strings start offset %d exceeds data length %d", stringsStart, len(data))
		}
	}

	isUTF8 := (flags & (1 << 8)) != 0

	offsets := make([]uint32, stringCount)
	for i := uint32(0); i < stringCount; i++ {
		offPos := 28 + i*4
		if int(offPos+4) > len(data) {
			break
		}
		offsets[i] = binary.LittleEndian.Uint32(data[offPos : offPos+4])
	}

	result := make([]string, 0, stringCount)
	strBlock := data[stringsStart:]

	for _, off := range offsets {
		if int(off) >= len(strBlock) {
			result = append(result, "")
			continue
		}
		sub := strBlock[off:]
		if isUTF8 {
			idx := 0
			// UTF-16 len prefix (skip)
			if idx < len(sub) && (sub[idx]&0x80) != 0 {
				idx += 2
			} else {
				idx += 1
			}
			// UTF-8 len prefix
			var u8len int
			if idx < len(sub) && (sub[idx]&0x80) != 0 {
				if idx+1 < len(sub) {
					u8len = int(sub[idx]&0x7F)<<8 | int(sub[idx+1])
					idx += 2
				}
			} else if idx < len(sub) {
				u8len = int(sub[idx])
				idx += 1
			}
			if idx+u8len <= len(sub) {
				result = append(result, string(sub[idx:idx+u8len]))
			} else {
				result = append(result, "")
			}
		} else {
			// UTF-16LE
			if len(sub) < 2 {
				result = append(result, "")
				continue
			}
			idx := 0
			val := binary.LittleEndian.Uint16(sub[idx : idx+2])
			idx += 2
			var charCount int
			if (val & 0x8000) != 0 {
				if idx+2 <= len(sub) {
					val2 := binary.LittleEndian.Uint16(sub[idx : idx+2])
					idx += 2
					charCount = int(val&0x7FFF)<<16 | int(val2)
				}
			} else {
				charCount = int(val)
			}

			// Strictly cap charCount to remaining buffer bytes to prevent out-of-memory panics
			maxChars := (len(sub) - idx) / 2
			if charCount > maxChars {
				charCount = maxChars
			}
			if charCount < 0 {
				charCount = 0
			}

			u16 := make([]uint16, 0, charCount)
			for j := 0; j < charCount && idx+2 <= len(sub); j++ {
				u16 = append(u16, binary.LittleEndian.Uint16(sub[idx:idx+2]))
				idx += 2
			}
			result = append(result, string(utf16.Decode(u16)))
		}
	}
	return result, nil
}
