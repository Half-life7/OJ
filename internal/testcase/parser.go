// Package testcase validates small, root-only ZIP32 datasets in memory.
// It never extracts archive entries into a filesystem or allocates data versions.
package testcase

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"math"
	"sort"
	"strings"
)

var (
	ErrInvalidArchive = errors.New("invalid testcase archive")
	ErrInvalidPath    = errors.New("invalid testcase entry name or type")
	ErrLimitExceeded  = errors.New("testcase archive limit exceeded")
	ErrInvalidLimits  = errors.New("invalid testcase limits")
	ErrPairing        = errors.New("testcase input/output pairing invalid")
	ErrScore          = errors.New("testcase scores invalid")
)

// Limits are explicit budgets. Zero values are rejected; use DefaultLimits().
type Limits struct {
	MaxArchiveBytes   int64
	MaxFileBytes      int64
	MaxTotalBytes     int64
	MaxCases          int
	MaxDirectoryBytes int64
}

func DefaultLimits() Limits {
	return Limits{
		MaxArchiveBytes:   32 << 20,
		MaxFileBytes:      16 << 20,
		MaxTotalBytes:     64 << 20,
		MaxCases:          512,
		MaxDirectoryBytes: 1 << 20,
	}
}

type Case struct {
	ID     string
	Input  []byte
	Output []byte
	Score  int64
}

type Dataset struct {
	Cases      []Case
	Digest     [32]byte
	TotalBytes int64
	TotalScore int64
}

// Parse consumes a stable ReaderAt of exactly size bytes. Scores must contain
// exactly one nonnegative score for every original, case-sensitive case ID.
// A canceled context is returned unchanged. Other failures wrap a sentinel
// above and never include entry contents or arbitrary underlying I/O errors.
// No partial dataset is returned when validation fails.
func Parse(ctx context.Context, src io.ReaderAt, size int64, scores map[string]int64, limits Limits) (Dataset, error) {
	if err := validateLimits(limits); err != nil {
		return Dataset{}, err
	}
	if ctx == nil || src == nil || size < endHeaderLen {
		return Dataset{}, ErrInvalidArchive
	}
	if err := ctx.Err(); err != nil {
		return Dataset{}, err
	}
	if size > limits.MaxArchiveBytes {
		return Dataset{}, fmt.Errorf("%w: archive bytes", ErrLimitExceeded)
	}
	r := contextReaderAt{ctx: ctx, src: src}
	entries, err := inspectZIP32(r, size, limits)
	if err != nil {
		return Dataset{}, normalizeReadError(ctx, err)
	}
	if len(entries) == 0 {
		return Dataset{}, fmt.Errorf("%w: empty dataset", ErrPairing)
	}
	ids, totalScore, err := validatePairsAndScores(entries, scores)
	if err != nil {
		return Dataset{}, err
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return Dataset{}, normalizeReadError(ctx, ErrInvalidArchive)
	}
	if len(zr.File) != len(entries) {
		return Dataset{}, ErrInvalidArchive
	}
	// inspectZIP32 bounds the actual central directory before NewReader can
	// allocate its File slice. Do not replace it with a len(zr.File) check alone.
	files := make(map[string]*zip.File, len(entries))
	var declared int64
	for i, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return Dataset{}, err
		}
		e := entries[i]
		if f.Name != e.name || !f.Mode().IsRegular() {
			return Dataset{}, ErrInvalidPath
		}
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return Dataset{}, ErrInvalidArchive
		}
		if f.Flags&encryptionFlags != 0 {
			return Dataset{}, ErrInvalidArchive
		}
		if f.UncompressedSize64 > uint64(limits.MaxFileBytes) {
			return Dataset{}, fmt.Errorf("%w: file bytes", ErrLimitExceeded)
		}
		if f.UncompressedSize64 > uint64(limits.MaxTotalBytes-declared) {
			return Dataset{}, fmt.Errorf("%w: total bytes", ErrLimitExceeded)
		}
		declared += int64(f.UncompressedSize64)
		files[f.Name] = f
	}
	result := Dataset{Cases: make([]Case, 0, len(ids)), TotalScore: totalScore}
	for _, id := range ids {
		input, err := readFile(ctx, files[id+".in"], limits, result.TotalBytes)
		if err != nil {
			return Dataset{}, err
		}
		result.TotalBytes += int64(len(input))
		output, err := readFile(ctx, files[id+".out"], limits, result.TotalBytes)
		if err != nil {
			return Dataset{}, err
		}
		result.TotalBytes += int64(len(output))
		result.Cases = append(result.Cases, Case{ID: id, Input: input, Output: output, Score: scores[id]})
	}
	if err := ctx.Err(); err != nil {
		return Dataset{}, err
	}
	result.Digest = digest(result.Cases)
	if err := ctx.Err(); err != nil {
		return Dataset{}, err
	}
	return result, nil
}

func validateLimits(l Limits) error {
	if l.MaxArchiveBytes < endHeaderLen || l.MaxArchiveBytes > math.MaxUint32 ||
		l.MaxFileBytes <= 0 || l.MaxFileBytes >= math.MaxUint32 ||
		l.MaxTotalBytes <= 0 || l.MaxTotalBytes >= math.MaxInt64 ||
		l.MaxFileBytes > l.MaxTotalBytes || l.MaxCases <= 0 || l.MaxCases > 32767 ||
		l.MaxDirectoryBytes <= 0 || l.MaxDirectoryBytes > l.MaxArchiveBytes {
		return ErrInvalidLimits
	}
	return nil
}

func validateName(name string) (string, error) {
	var id string
	switch {
	case strings.HasSuffix(name, ".in"):
		id = strings.TrimSuffix(name, ".in")
	case strings.HasSuffix(name, ".out"):
		id = strings.TrimSuffix(name, ".out")
	default:
		return "", ErrInvalidPath
	}
	if len(id) == 0 || len(id) > 64 {
		return "", ErrInvalidPath
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		alphanumeric := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alphanumeric && (i == 0 || c != '_' && c != '-') {
			return "", ErrInvalidPath
		}
	}
	upper := strings.ToUpper(id)
	switch upper {
	case "CON", "PRN", "AUX", "NUL":
		return "", ErrInvalidPath
	}
	if len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9' {
		return "", ErrInvalidPath
	}
	return id, nil
}

func validatePairsAndScores(entries []entry, scores map[string]int64) ([]string, int64, error) {
	pairs := make(map[string]uint8, len(entries)/2)
	folded := make(map[string]string, len(entries)/2)
	for _, e := range entries {
		id, err := validateName(e.name)
		if err != nil {
			return nil, 0, err
		}
		fold := strings.ToLower(id)
		if prior, ok := folded[fold]; ok && prior != id {
			return nil, 0, fmt.Errorf("%w: case-folding conflict", ErrPairing)
		}
		folded[fold] = id
		bit := uint8(1)
		if strings.HasSuffix(e.name, ".out") {
			bit = 2
		}
		if pairs[id]&bit != 0 {
			return nil, 0, fmt.Errorf("%w: duplicate entry", ErrPairing)
		}
		pairs[id] |= bit
	}
	if len(scores) != len(pairs) {
		return nil, 0, ErrScore
	}
	ids := make([]string, 0, len(pairs))
	var total int64
	for id, pair := range pairs {
		if pair != 3 {
			return nil, 0, ErrPairing
		}
		score, ok := scores[id]
		if !ok || score < 0 || score > math.MaxInt64-total {
			return nil, 0, ErrScore
		}
		total += score
		ids = append(ids, id)
	}
	if total == 0 {
		return nil, 0, ErrScore
	}
	sort.Strings(ids)
	return ids, total, nil
}

func readFile(ctx context.Context, f *zip.File, limits Limits, total int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	budget := min(limits.MaxFileBytes, limits.MaxTotalBytes-total)
	if budget < 0 {
		return nil, ErrLimitExceeded
	}
	rc, err := f.Open()
	if err != nil {
		return nil, normalizeReadError(ctx, ErrInvalidArchive)
	}
	data, readErr := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, src: rc}, budget+1))
	closeErr := rc.Close()
	if int64(len(data)) > budget {
		return nil, ErrLimitExceeded
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// archive/zip may skip its CRC check when a non-descriptor entry declares
	// CRC32=0. Always verify it ourselves, including the legitimate zero case.
	if readErr != nil || closeErr != nil || uint64(len(data)) != f.UncompressedSize64 || crc32.ChecksumIEEE(data) != f.CRC32 {
		return nil, ErrInvalidArchive
	}
	return data, nil
}

func digest(cases []Case) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte("biue-testdata-v1\x00"))
	writeUint(h, uint64(len(cases)))
	for _, c := range cases {
		writeBytes(h, []byte(c.ID))
		writeBytes(h, c.Input)
		writeBytes(h, c.Output)
		writeUint(h, uint64(c.Score))
	}
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

func writeUint(h hash.Hash, n uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], n)
	_, _ = h.Write(buf[:])
}
func writeBytes(h hash.Hash, p []byte) { writeUint(h, uint64(len(p))); _, _ = h.Write(p) }

type contextReaderAt struct {
	ctx context.Context
	src io.ReaderAt
}

func (r contextReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.src.ReadAt(p, off)
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		return n, ctxErr
	}
	return n, err
}

type contextReader struct {
	ctx context.Context
	src io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	// Bound work between cancellation checks even if ReadAll grows its buffer.
	if len(p) > 32<<10 {
		p = p[:32<<10]
	}
	n, err := r.src.Read(p)
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		return n, ctxErr
	}
	return n, err
}
func normalizeReadError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
