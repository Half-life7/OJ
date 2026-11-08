package testcase

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type zipEntry struct {
	name   string
	data   []byte
	method uint16
	mode   os.FileMode
	extra  []byte
}

func archive(t testing.TB, entries []zipEntry, modified time.Time) []byte {
	t.Helper()
	var buffer bytes.Buffer
	w := zip.NewWriter(&buffer)
	for _, e := range entries {
		h := zip.FileHeader{Name: e.name, Method: e.method, Extra: e.extra}
		if !modified.IsZero() {
			h.SetModTime(modified)
		}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		file, err := w.CreateHeader(&h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func validArchive(t testing.TB) []byte {
	return archive(t, []zipEntry{{name: "1.in", data: []byte("abc")}, {name: "1.out", data: []byte("ok")}}, time.Time{})
}

func parse(t testing.TB, raw []byte, scores map[string]int64, limits Limits) (Dataset, error) {
	t.Helper()
	return Parse(context.Background(), bytes.NewReader(raw), int64(len(raw)), scores, limits)
}

func expectError(t *testing.T, raw []byte, scores map[string]int64, limits Limits, want error) {
	t.Helper()
	result, err := parse(t, raw, scores, limits)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if !reflect.DeepEqual(result, Dataset{}) {
		t.Fatal("failure returned a partial dataset")
	}
}

func TestParseBinaryEmptyAndExactScores(t *testing.T) {
	input := []byte{0, 255, '\r', '\n', 128}
	raw := archive(t, []zipEntry{
		{name: "b.out", data: []byte("x\r\n")},
		{name: "a.in", data: input, method: zip.Deflate},
		{name: "b.in", data: []byte(" ")},
		{name: "a.out", data: nil},
	}, time.Time{})
	result, err := parse(t, raw, map[string]int64{"a": 0, "b": 25}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Cases) != 2 || result.Cases[0].ID != "a" || result.Cases[1].ID != "b" {
		t.Fatalf("incorrect order: %+v", result.Cases)
	}
	if !bytes.Equal(result.Cases[0].Input, input) || len(result.Cases[0].Output) != 0 || result.Cases[0].Score != 0 {
		t.Fatal("input/output bytes or scores changed")
	}
	if !bytes.Equal(result.Cases[1].Output, []byte("x\r\n")) || result.TotalBytes != 9 || result.TotalScore != 25 {
		t.Fatalf("incorrect totals: %+v", result)
	}
	if result.Digest == ([32]byte{}) {
		t.Fatal("digest missing")
	}
}

func TestDigestIgnoresZIPMetadataButIncludesDatasetSemantics(t *testing.T) {
	entries := []zipEntry{{name: "a.in", data: []byte("ab")}, {name: "a.out", data: []byte("c")}}
	a, err := parse(t, archive(t, entries, time.Time{}), map[string]int64{"a": 10}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	reversed := []zipEntry{{name: "a.out", data: []byte("c"), method: zip.Deflate}, {name: "a.in", data: []byte("ab"), method: zip.Deflate}}
	b, err := parse(t, archive(t, reversed, time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)), map[string]int64{"a": 10}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest != b.Digest {
		t.Fatal("zip order, timestamp or compression changed the dataset digest")
	}
	for _, tc := range []struct {
		name    string
		entries []zipEntry
		scores  map[string]int64
	}{
		{"boundaries", []zipEntry{{name: "a.in", data: []byte("a")}, {name: "a.out", data: []byte("bc")}}, map[string]int64{"a": 10}},
		{"id", []zipEntry{{name: "b.in", data: []byte("ab")}, {name: "b.out", data: []byte("c")}}, map[string]int64{"b": 10}},
		{"score", entries, map[string]int64{"a": 11}},
		{"newline", []zipEntry{{name: "a.in", data: []byte("ab\n")}, {name: "a.out", data: []byte("c")}}, map[string]int64{"a": 10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parse(t, archive(t, tc.entries, time.Time{}), tc.scores, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			if got.Digest == a.Digest {
				t.Fatal("semantic change did not alter digest")
			}
		})
	}
}

func TestPairingAndScoreValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []zipEntry
		scores  map[string]int64
		want    error
	}{
		{"empty", nil, nil, ErrPairing},
		{"missing-output", []zipEntry{{name: "a.in"}}, map[string]int64{"a": 1}, ErrPairing},
		{"missing-input", []zipEntry{{name: "a.out"}}, map[string]int64{"a": 1}, ErrPairing},
		{"duplicate", []zipEntry{{name: "a.in"}, {name: "a.in"}, {name: "a.out"}}, map[string]int64{"a": 1}, ErrPairing},
		{"mixed-id-case", []zipEntry{{name: "a.in"}, {name: "A.out"}}, map[string]int64{"a": 1}, ErrPairing},
		{"case-collision", []zipEntry{{name: "a.in"}, {name: "a.out"}, {name: "A.in"}, {name: "A.out"}}, map[string]int64{"a": 1, "A": 1}, ErrPairing},
		{"missing-score", []zipEntry{{name: "a.in"}, {name: "a.out"}}, nil, ErrScore},
		{"wrong-score-id", []zipEntry{{name: "a.in"}, {name: "a.out"}}, map[string]int64{"b": 1}, ErrScore},
		{"extra-score", []zipEntry{{name: "a.in"}, {name: "a.out"}}, map[string]int64{"a": 1, "b": 1}, ErrScore},
		{"negative", []zipEntry{{name: "a.in"}, {name: "a.out"}}, map[string]int64{"a": -1}, ErrScore},
		{"all-zero", []zipEntry{{name: "a.in"}, {name: "a.out"}}, map[string]int64{"a": 0}, ErrScore},
		{"overflow", []zipEntry{{name: "a.in"}, {name: "a.out"}, {name: "b.in"}, {name: "b.out"}}, map[string]int64{"a": math.MaxInt64, "b": 1}, ErrScore},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectError(t, archive(t, tc.entries, time.Time{}), tc.scores, DefaultLimits(), tc.want)
		})
	}
}

func TestRejectDangerousNamesAndTypes(t *testing.T) {
	for _, name := range []string{"../a.in", "/a.in", `a\a.in`, `C:\a.in`, `C:a.in`, `\\server\a.in`, "a/../a.in", "a//b.in", "a.in\x00", "a\n.in", "a.in ", ".in", "_a.in", "-a.in", "a.b.in", "a.IN", "a.zip", "a/", "CON.in", "nul.out", "Com1.in", "LPT9.out", "中.in", strings.Repeat("a", 65) + ".in"} {
		t.Run(name, func(t *testing.T) {
			expectError(t, archive(t, []zipEntry{{name: name}}, time.Time{}), map[string]int64{}, DefaultLimits(), ErrInvalidPath)
		})
	}
	for _, mode := range []os.FileMode{os.ModeSymlink | 0777, os.ModeNamedPipe | 0600, os.ModeDevice | 0600} {
		raw := archive(t, []zipEntry{{name: "a.in", mode: mode}, {name: "a.out"}}, time.Time{})
		expectError(t, raw, map[string]int64{"a": 1}, DefaultLimits(), ErrInvalidPath)
	}
	for _, id := range []string{"A", "01", "a-b_c", strings.Repeat("a", 64), "com0", "com10"} {
		raw := archive(t, []zipEntry{{name: id + ".in"}, {name: id + ".out"}}, time.Time{})
		if _, err := parse(t, raw, map[string]int64{id: 1}, DefaultLimits()); err != nil {
			t.Fatalf("valid ID rejected: %s: %v", id, err)
		}
	}
}

func TestLimitsAtAndOverBoundaries(t *testing.T) {
	raw := validArchive(t)
	eocd := len(raw) - endHeaderLen
	directoryBytes := int64(binary.LittleEndian.Uint32(raw[eocd+12 : eocd+16]))
	l := DefaultLimits()
	l.MaxArchiveBytes = int64(len(raw))
	l.MaxDirectoryBytes = directoryBytes
	l.MaxFileBytes = 3
	l.MaxTotalBytes = 5
	l.MaxCases = 1
	if _, err := parse(t, raw, map[string]int64{"1": 1}, l); err != nil {
		t.Fatalf("exact limits rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Limits)
	}{
		{"archive", func(l *Limits) { l.MaxArchiveBytes-- }},
		{"directory", func(l *Limits) { l.MaxDirectoryBytes-- }},
		{"file", func(l *Limits) { l.MaxFileBytes = 2 }},
		{"total", func(l *Limits) { l.MaxTotalBytes = 4 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limited := l
			tc.change(&limited)
			expectError(t, raw, map[string]int64{"1": 1}, limited, ErrLimitExceeded)
		})
	}
	more := archive(t, []zipEntry{{name: "a.in"}, {name: "a.out"}, {name: "b.in"}, {name: "b.out"}}, time.Time{})
	limited := DefaultLimits()
	limited.MaxCases = 1
	expectError(t, more, map[string]int64{"a": 1, "b": 1}, limited, ErrLimitExceeded)
	for _, change := range []func(*Limits){
		func(l *Limits) { *l = Limits{} }, func(l *Limits) { l.MaxCases = 0 }, func(l *Limits) { l.MaxCases = 32768 }, func(l *Limits) { l.MaxDirectoryBytes = l.MaxArchiveBytes + 1 }, func(l *Limits) { l.MaxFileBytes = l.MaxTotalBytes + 1 }, func(l *Limits) { l.MaxTotalBytes = math.MaxInt64 },
	} {
		invalid := DefaultLimits()
		change(&invalid)
		expectError(t, raw, map[string]int64{"1": 1}, invalid, ErrInvalidLimits)
	}
}

func centralOffsets(raw []byte) []int {
	end := len(raw) - endHeaderLen
	pos := int(binary.LittleEndian.Uint32(raw[end+16 : end+20]))
	var offsets []int
	for pos < end {
		offsets = append(offsets, pos)
		pos += centralHeaderLen + int(binary.LittleEndian.Uint16(raw[pos+28:pos+30])) + int(binary.LittleEndian.Uint16(raw[pos+30:pos+32])) + int(binary.LittleEndian.Uint16(raw[pos+32:pos+34]))
	}
	return offsets
}

func TestRejectMalformedZIP32AndMetadata(t *testing.T) {
	base := validArchive(t)
	for _, tc := range []struct {
		name   string
		change func([]byte) []byte
		want   error
	}{
		{"garbage", func(p []byte) []byte { return []byte("not a zip file") }, ErrInvalidArchive},
		{"truncated", func(p []byte) []byte { return p[:len(p)-1] }, ErrInvalidArchive},
		{"trailing-garbage", func(p []byte) []byte { return append(p, 0) }, ErrInvalidArchive},
		{"multi-disk", func(p []byte) []byte { binary.LittleEndian.PutUint16(p[len(p)-endHeaderLen+4:], 1); return p }, ErrInvalidArchive},
		{"zip64-count", func(p []byte) []byte { binary.LittleEndian.PutUint16(p[len(p)-endHeaderLen+10:], 0xffff); return p }, ErrInvalidArchive},
		{"zip64-offset", func(p []byte) []byte { binary.LittleEndian.PutUint32(p[len(p)-endHeaderLen+16:], 0xffffffff); return p }, ErrInvalidArchive},
		{"false-entry-count", func(p []byte) []byte {
			end := len(p) - endHeaderLen
			binary.LittleEndian.PutUint16(p[end+8:], 0)
			binary.LittleEndian.PutUint16(p[end+10:], 0)
			return p
		}, ErrInvalidArchive},
		{"central-entry-disk", func(p []byte) []byte { binary.LittleEndian.PutUint16(p[centralOffsets(p)[0]+34:], 1); return p }, ErrInvalidArchive},
		{"zip64-entry", func(p []byte) []byte { binary.LittleEndian.PutUint16(p[centralOffsets(p)[0]+6:], 45); return p }, ErrInvalidArchive},
		{"encryption", func(p []byte) []byte { binary.LittleEndian.PutUint16(p[centralOffsets(p)[0]+8:], 1); return p }, ErrInvalidArchive},
		{"unsupported-compression", func(p []byte) []byte { binary.LittleEndian.PutUint16(p[centralOffsets(p)[0]+10:], 99); return p }, ErrInvalidArchive},
		{"local-header-mismatch", func(p []byte) []byte { p[localHeaderLen] = 'X'; return p }, ErrInvalidArchive},
		{"forged-small-expanded-size", func(p []byte) []byte { binary.LittleEndian.PutUint32(p[centralOffsets(p)[0]+24:], 1); return p }, ErrInvalidArchive},
		{"forged-large-expanded-size", func(p []byte) []byte {
			binary.LittleEndian.PutUint32(p[centralOffsets(p)[0]+24:], uint32(DefaultLimits().MaxFileBytes+1))
			return p
		}, ErrLimitExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.change(bytes.Clone(base))
			expectError(t, raw, map[string]int64{"1": 1}, DefaultLimits(), tc.want)
		})
	}
	for _, extra := range [][]byte{{1, 0, 0, 0}, {2, 0, 5, 0, 0}} {
		raw := archive(t, []zipEntry{{name: "a.in", extra: extra}, {name: "a.out"}}, time.Time{})
		expectError(t, raw, map[string]int64{"a": 1}, DefaultLimits(), ErrInvalidArchive)
	}
}

func TestActualDirectoryCountCannotHideBehindEOCD(t *testing.T) {
	raw := archive(t, []zipEntry{{name: "a.in"}, {name: "a.out"}, {name: "b.in"}, {name: "b.out"}}, time.Time{})
	end := len(raw) - endHeaderLen
	binary.LittleEndian.PutUint16(raw[end+8:], 0)
	binary.LittleEndian.PutUint16(raw[end+10:], 0)
	l := DefaultLimits()
	l.MaxCases = 1
	expectError(t, raw, map[string]int64{"a": 1, "b": 1}, l, ErrLimitExceeded)
}

func TestRejectSelfExtractingPrefixWithOtherwiseValidOffsets(t *testing.T) {
	raw := validArchive(t)
	for _, offset := range centralOffsets(raw) {
		binary.LittleEndian.PutUint32(raw[offset+42:], binary.LittleEndian.Uint32(raw[offset+42:])+2)
	}
	end := len(raw) - endHeaderLen
	binary.LittleEndian.PutUint32(raw[end+16:], binary.LittleEndian.Uint32(raw[end+16:])+2)
	raw = append([]byte("MZ"), raw...)
	expectError(t, raw, map[string]int64{"1": 1}, DefaultLimits(), ErrInvalidArchive)
}

func dataOffset(t testing.TB, raw []byte, name string) int64 {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.File {
		if f.Name == name {
			offset, err := f.DataOffset()
			if err != nil {
				t.Fatal(err)
			}
			return offset
		}
	}
	t.Fatalf("entry %s missing", name)
	return 0
}

func TestCRCAndZIPBombBudgets(t *testing.T) {
	raw := validArchive(t)
	raw[dataOffset(t, raw, "1.in")] ^= 0xff
	expectError(t, raw, map[string]int64{"1": 1}, DefaultLimits(), ErrInvalidArchive)
	compressed := archive(t, []zipEntry{{name: "a.in", data: bytes.Repeat([]byte("x"), 1<<20), method: zip.Deflate}, {name: "a.out", method: zip.Deflate}}, time.Time{})
	l := DefaultLimits()
	l.MaxFileBytes = 128 << 10
	expectError(t, compressed, map[string]int64{"a": 1}, l, ErrLimitExceeded)
}

func TestZeroCRCWithoutDescriptorIsStillVerified(t *testing.T) {
	var buffer bytes.Buffer
	w := zip.NewWriter(&buffer)
	for _, e := range []zipEntry{{name: "1.in", data: []byte("abc")}, {name: "1.out", data: []byte("ok")}} {
		file, err := w.CreateRaw(&zip.FileHeader{Name: e.name, Method: zip.Store, CRC32: 0, CompressedSize64: uint64(len(e.data)), UncompressedSize64: uint64(len(e.data))})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	expectError(t, buffer.Bytes(), map[string]int64{"1": 1}, DefaultLimits(), ErrInvalidArchive)
}

type failingReaderAt struct {
	reader *bytes.Reader
	offset int64
	cancel context.CancelFunc
	reads  int
}

func (r *failingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	r.reads++
	if off == r.offset {
		if r.cancel != nil {
			r.cancel()
			return r.reader.ReadAt(p, off)
		}
		return 0, errors.New("secret source contents must not leak")
	}
	return r.reader.ReadAt(p, off)
}

func TestCancellationReadFailureAndNoPartialResult(t *testing.T) {
	raw := validArchive(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source := &failingReaderAt{reader: bytes.NewReader(raw), offset: -1}
	result, err := Parse(ctx, source, int64(len(raw)), map[string]int64{"1": 1}, DefaultLimits())
	if !errors.Is(err, context.Canceled) || source.reads != 0 || !reflect.DeepEqual(result, Dataset{}) {
		t.Fatal("pre-canceled context read data or returned a result")
	}
	for _, cancelRead := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		source := &failingReaderAt{reader: bytes.NewReader(raw), offset: dataOffset(t, raw, "1.in")}
		if cancelRead {
			source.cancel = cancel
		}
		result, err := Parse(ctx, source, int64(len(raw)), map[string]int64{"1": 1}, DefaultLimits())
		cancel()
		want := ErrInvalidArchive
		if cancelRead {
			want = context.Canceled
		}
		if !errors.Is(err, want) || !reflect.DeepEqual(result, Dataset{}) {
			t.Fatalf("read error = %v, want %v", err, want)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatal("source error leaked")
		}
	}
	_, err = Parse(context.Background(), io.NewSectionReader(bytes.NewReader(raw), 0, int64(len(raw))), int64(len(raw))+1, map[string]int64{"1": 1}, DefaultLimits())
	if !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("incorrect source size: %v", err)
	}
}

func FuzzParseZIPNeverPanics(f *testing.F) {
	f.Add(validArchive(f))
	f.Add([]byte("not a zip"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		l := DefaultLimits()
		l.MaxArchiveBytes = 1 << 20
		l.MaxDirectoryBytes = 64 << 10
		l.MaxFileBytes = 64 << 10
		l.MaxTotalBytes = 128 << 10
		l.MaxCases = 16
		result, err := Parse(context.Background(), bytes.NewReader(raw), int64(len(raw)), map[string]int64{"1": 1}, l)
		if err != nil && !reflect.DeepEqual(result, Dataset{}) {
			t.Fatal("error returned partial dataset")
		}
		if err == nil && (result.TotalBytes > l.MaxTotalBytes || len(result.Cases) > l.MaxCases) {
			t.Fatal("successful result exceeds budget")
		}
	})
}
