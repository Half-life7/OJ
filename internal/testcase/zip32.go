package testcase

import (
	"archive/zip"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	endHeaderLen     = 22
	centralHeaderLen = 46
	localHeaderLen   = 30
	endSignature     = 0x06054b50
	centralSignature = 0x02014b50
	localSignature   = 0x04034b50
	encryptionFlags  = 1 | 1<<6 | 1<<13
)

type entry struct {
	name         string
	offset       int64
	compressed   uint32
	uncompressed uint32
	flags        uint16
	method       uint16
	crc          uint32
}

// inspectZIP32 validates the actual bounded directory before archive/zip can
// allocate a File per entry. It does not trust EOCD's declared entry count.
func inspectZIP32(r io.ReaderAt, size int64, limits Limits) ([]entry, error) {
	tail := make([]byte, min(size, endHeaderLen+65535))
	if !readExact(r, tail, size-int64(len(tail))) {
		return nil, ErrInvalidArchive
	}
	endIndex := -1
	for i := len(tail) - endHeaderLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:i+4]) == endSignature && i+endHeaderLen+int(binary.LittleEndian.Uint16(tail[i+20:i+22])) == len(tail) {
			endIndex = i
			break
		}
	}
	if endIndex < 0 {
		return nil, ErrInvalidArchive
	}
	end := tail[endIndex:]
	if binary.LittleEndian.Uint16(end[4:6]) != 0 || binary.LittleEndian.Uint16(end[6:8]) != 0 {
		return nil, ErrInvalidArchive
	}
	count := binary.LittleEndian.Uint16(end[10:12])
	if count == 0xffff || binary.LittleEndian.Uint16(end[8:10]) != count {
		return nil, ErrInvalidArchive
	}
	if int(count) > limits.MaxCases*2 {
		return nil, fmt.Errorf("%w: entry count", ErrLimitExceeded)
	}
	directoryBytes := binary.LittleEndian.Uint32(end[12:16])
	directoryOffset := binary.LittleEndian.Uint32(end[16:20])
	if directoryBytes == 0xffffffff || directoryOffset == 0xffffffff {
		return nil, ErrInvalidArchive
	}
	if int64(directoryBytes) > limits.MaxDirectoryBytes {
		return nil, fmt.Errorf("%w: central directory bytes", ErrLimitExceeded)
	}
	endOffset := size - int64(len(tail)) + int64(endIndex)
	directoryEnd := int64(directoryOffset) + int64(directoryBytes)
	if directoryEnd != endOffset {
		return nil, ErrInvalidArchive
	}
	entries := make([]entry, 0, int(count))
	pos := int64(directoryOffset)
	var declared int64
	for pos < directoryEnd {
		if len(entries) >= limits.MaxCases*2 {
			return nil, fmt.Errorf("%w: actual entry count", ErrLimitExceeded)
		}
		var header [centralHeaderLen]byte
		if directoryEnd-pos < centralHeaderLen || !readExact(r, header[:], pos) || binary.LittleEndian.Uint32(header[:4]) != centralSignature {
			return nil, ErrInvalidArchive
		}
		if binary.LittleEndian.Uint16(header[6:8]) >= 45 || binary.LittleEndian.Uint16(header[34:36]) != 0 {
			return nil, ErrInvalidArchive
		}
		e := entry{flags: binary.LittleEndian.Uint16(header[8:10]), method: binary.LittleEndian.Uint16(header[10:12]), crc: binary.LittleEndian.Uint32(header[16:20]), compressed: binary.LittleEndian.Uint32(header[20:24]), uncompressed: binary.LittleEndian.Uint32(header[24:28]), offset: int64(binary.LittleEndian.Uint32(header[42:46]))}
		if e.flags&encryptionFlags != 0 || e.method != zip.Store && e.method != zip.Deflate || e.compressed == 0xffffffff || e.uncompressed == 0xffffffff || e.offset == 0xffffffff {
			return nil, ErrInvalidArchive
		}
		if int64(e.uncompressed) > limits.MaxFileBytes {
			return nil, fmt.Errorf("%w: declared file bytes", ErrLimitExceeded)
		}
		if int64(e.uncompressed) > limits.MaxTotalBytes-declared {
			return nil, fmt.Errorf("%w: declared total bytes", ErrLimitExceeded)
		}
		declared += int64(e.uncompressed)
		nameLen := int64(binary.LittleEndian.Uint16(header[28:30]))
		extraLen := int64(binary.LittleEndian.Uint16(header[30:32]))
		commentLen := int64(binary.LittleEndian.Uint16(header[32:34]))
		if nameLen < 4 || nameLen > 68 {
			return nil, ErrInvalidPath
		}
		next := pos + centralHeaderLen + nameLen + extraLen + commentLen
		if next > directoryEnd {
			return nil, ErrInvalidArchive
		}
		name := make([]byte, nameLen)
		if !readExact(r, name, pos+centralHeaderLen) {
			return nil, ErrInvalidArchive
		}
		e.name = string(name)
		if _, err := validateName(e.name); err != nil {
			return nil, err
		}
		extra := make([]byte, extraLen)
		if !readExact(r, extra, pos+centralHeaderLen+nameLen) || !validExtra(extra) {
			return nil, ErrInvalidArchive
		}
		if err := inspectLocal(r, e, int64(directoryOffset)); err != nil {
			return nil, err
		}
		entries = append(entries, e)
		pos = next
	}
	if len(entries) != int(count) {
		return nil, ErrInvalidArchive
	}
	if len(entries) == 0 {
		if directoryOffset != 0 {
			return nil, ErrInvalidArchive
		}
		return entries, nil
	}
	first := entries[0].offset
	for _, e := range entries {
		first = min(first, e.offset)
	}
	if first != 0 {
		return nil, ErrInvalidArchive
	}
	return entries, nil
}

func inspectLocal(r io.ReaderAt, e entry, directoryOffset int64) error {
	if e.offset < 0 || directoryOffset-e.offset < localHeaderLen {
		return ErrInvalidArchive
	}
	var header [localHeaderLen]byte
	if !readExact(r, header[:], e.offset) || binary.LittleEndian.Uint32(header[:4]) != localSignature {
		return ErrInvalidArchive
	}
	if binary.LittleEndian.Uint16(header[4:6]) >= 45 || binary.LittleEndian.Uint16(header[6:8]) != e.flags || binary.LittleEndian.Uint16(header[8:10]) != e.method || binary.LittleEndian.Uint32(header[18:22]) == 0xffffffff || binary.LittleEndian.Uint32(header[22:26]) == 0xffffffff {
		return ErrInvalidArchive
	}
	nameLen := int64(binary.LittleEndian.Uint16(header[26:28]))
	extraLen := int64(binary.LittleEndian.Uint16(header[28:30]))
	dataOffset := e.offset + localHeaderLen + nameLen + extraLen
	if nameLen != int64(len(e.name)) || dataOffset > directoryOffset || int64(e.compressed) > directoryOffset-dataOffset {
		return ErrInvalidArchive
	}
	name := make([]byte, nameLen)
	if !readExact(r, name, e.offset+localHeaderLen) || string(name) != e.name {
		return ErrInvalidArchive
	}
	extra := make([]byte, extraLen)
	if !readExact(r, extra, e.offset+localHeaderLen+nameLen) || !validExtra(extra) {
		return ErrInvalidArchive
	}
	if e.flags&8 == 0 && (binary.LittleEndian.Uint32(header[14:18]) != e.crc || binary.LittleEndian.Uint32(header[18:22]) != e.compressed || binary.LittleEndian.Uint32(header[22:26]) != e.uncompressed) {
		return ErrInvalidArchive
	}
	if e.flags&8 != 0 && directoryOffset-(dataOffset+int64(e.compressed)) < 12 {
		return ErrInvalidArchive
	}
	return nil
}

func validExtra(extra []byte) bool {
	for len(extra) > 0 {
		if len(extra) < 4 {
			return false
		}
		id := binary.LittleEndian.Uint16(extra[:2])
		size := int(binary.LittleEndian.Uint16(extra[2:4]))
		if id == 1 || size > len(extra)-4 {
			return false
		}
		extra = extra[4+size:]
	}
	return true
}

func readExact(r io.ReaderAt, p []byte, off int64) bool {
	if len(p) == 0 {
		return true
	}
	n, err := r.ReadAt(p, off)
	return n == len(p) && err == nil
}
