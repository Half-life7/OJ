package problemdata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"biue/internal/testcase"
)

func dataset(cases ...testcase.Case) testcase.Dataset {
	d := testcase.Dataset{Cases: cases, Digest: testcase.Fingerprint(cases)}
	for _, c := range cases {
		d.TotalBytes += int64(len(c.Input) + len(c.Output))
		d.TotalScore += c.Score
	}
	return d
}

func example() testcase.Dataset {
	return dataset(
		testcase.Case{ID: "1", Input: []byte{0, 255, '\r', '\n', 128}, Score: 0},
		testcase.Case{ID: "10", Input: []byte(" "), Output: []byte("x\r\n"), Score: 100},
	)
}

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "versions")
	s, err := New(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, directory
}

func expectDataset(t *testing.T, got, want testcase.Dataset) {
	t.Helper()
	if got.Digest != want.Digest || got.TotalBytes != want.TotalBytes || got.TotalScore != want.TotalScore || len(got.Cases) != len(want.Cases) {
		t.Fatal("dataset metadata changed")
	}
	for i := range want.Cases {
		g, w := got.Cases[i], want.Cases[i]
		if g.ID != w.ID || g.Score != w.Score || !bytes.Equal(g.Input, w.Input) || !bytes.Equal(g.Output, w.Output) {
			t.Fatalf("case %d changed", i)
		}
	}
}

func assertZero(t *testing.T, d testcase.Dataset) {
	t.Helper()
	if d.Cases != nil || d.Digest != ([32]byte{}) || d.TotalBytes != 0 || d.TotalScore != 0 {
		t.Fatal("failure returned partial data")
	}
}

func TestRoundTripPreservesBinaryEmptyScoresAndSurvivesReopen(t *testing.T) {
	s, directory := newStore(t)
	want := example()
	key, err := s.Write(context.Background(), want)
	if err != nil || !validHex(key, 32) {
		t.Fatalf("write returned invalid key or error: %q %v", key, err)
	}
	got, err := s.Read(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	expectDataset(t, got, want)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err = reopened.Read(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	expectDataset(t, got, want)

	files, err := os.ReadDir(filepath.Join(directory, key, "data"))
	if err != nil || len(files) != 5 {
		t.Fatal("unexpected persisted file set", err)
	}
	for _, file := range files {
		info, err := file.Info()
		if err != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0007 != 0 {
			t.Fatal("dataset files must be regular and not world-accessible")
		}
	}
}

func TestRepeatedImportsUseIndependentImmutableVersions(t *testing.T) {
	s, _ := newStore(t)
	d := example()
	first, err := s.Write(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Write(context.Background(), d)
	if err != nil || first == second {
		t.Fatal("reimport reused or overwrote the existing version", err)
	}
	want := example()
	d.Cases[0].Input[0] = 50
	for _, key := range []string{first, second} {
		got, err := s.Read(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		expectDataset(t, got, want)
	}
}

func TestClearRemovesOnlyOwnedVersionDirectories(t *testing.T) {
	s, directory := newStore(t)
	first, err := s.Write(context.Background(), example())
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Write(context.Background(), example())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(directory, "operator-notes"), 0750); err != nil {
		t.Fatal(err)
	}
	removed, err := s.Clear(context.Background())
	if err != nil || removed != 2 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	for _, key := range []string{first, second} {
		if _, err := s.Read(context.Background(), key); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cleared key %s remained: %v", key, err)
		}
	}
	if info, err := os.Stat(filepath.Join(directory, "operator-notes")); err != nil || !info.IsDir() {
		t.Fatal("Clear removed an unrelated directory")
	}
}

func TestKeyReservationCollisionCannotOverwriteExistingDirectory(t *testing.T) {
	s, directory := newStore(t)
	key := strings.Repeat("00", 16)
	if err := os.Mkdir(filepath.Join(directory, key), 0750); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(directory, key, "keep")
	if err := os.WriteFile(marker, []byte("existing"), 0640); err != nil {
		t.Fatal(err)
	}
	s.random = bytes.NewReader(make([]byte, 16*8))
	got, err := s.Write(context.Background(), example())
	if !errors.Is(err, ErrUnavailable) || got != "" {
		t.Fatal("collision must fail without publishing", got, err)
	}
	content, err := os.ReadFile(marker)
	if err != nil || string(content) != "existing" {
		t.Fatal("collision changed existing data", err)
	}
}

func TestInvalidDatasetsNeverCreateFiles(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*testcase.Dataset)
	}{
		{"no cases", func(d *testcase.Dataset) { *d = testcase.Dataset{} }},
		{"digest", func(d *testcase.Dataset) { d.Digest[0] ^= 1 }},
		{"bytes", func(d *testcase.Dataset) { d.TotalBytes++ }},
		{"total score", func(d *testcase.Dataset) { d.TotalScore++ }},
		{"negative score", func(d *testcase.Dataset) { *d = dataset(testcase.Case{ID: "a", Score: -1}) }},
		{"zero total", func(d *testcase.Dataset) { *d = dataset(testcase.Case{ID: "a"}) }},
		{"score overflow", func(d *testcase.Dataset) {
			*d = dataset(testcase.Case{ID: "a", Score: math.MaxInt64}, testcase.Case{ID: "b", Score: 1})
		}},
		{"traversal", func(d *testcase.Dataset) { *d = dataset(testcase.Case{ID: "../password-secret", Score: 1}) }},
		{"device", func(d *testcase.Dataset) { *d = dataset(testcase.Case{ID: "COM1", Score: 1}) }},
		{"unsorted", func(d *testcase.Dataset) {
			*d = dataset(testcase.Case{ID: "b", Score: 1}, testcase.Case{ID: "a", Score: 1})
		}},
		{"duplicate", func(d *testcase.Dataset) {
			*d = dataset(testcase.Case{ID: "a", Score: 1}, testcase.Case{ID: "a", Score: 1})
		}},
		{"case folding", func(d *testcase.Dataset) {
			*d = dataset(testcase.Case{ID: "A", Score: 1}, testcase.Case{ID: "a", Score: 1})
		}},
		{"too many cases", func(d *testcase.Dataset) {
			d.Cases = make([]testcase.Case, testcase.DefaultLimits().MaxCases+1)
		}},
		{"too large member", func(d *testcase.Dataset) {
			*d = dataset(testcase.Case{ID: "a", Score: 1, Input: make([]byte, testcase.DefaultLimits().MaxFileBytes+1)})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, directory := newStore(t)
			d := example()
			tc.edit(&d)
			key, err := s.Write(context.Background(), d)
			if !errors.Is(err, ErrInvalid) || key != "" || strings.Contains(err.Error(), "password-secret") {
				t.Fatalf("invalid dataset not rejected safely: %q %v", key, err)
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid dataset created files", err)
			}
		})
	}
}

func TestPublishFailureCleansStagingAndPreservesExistingVersion(t *testing.T) {
	s, directory := newStore(t)
	want := example()
	existing, err := s.Write(context.Background(), want)
	if err != nil {
		t.Fatal(err)
	}
	s.rename = func(string, string) error {
		return &os.PathError{Op: "rename", Path: "password-secret", Err: errors.New("answer-secret")}
	}
	key, err := s.Write(context.Background(), want)
	if !errors.Is(err, ErrUnavailable) || key != "" || strings.Contains(err.Error(), "secret") {
		t.Fatal("publication failure leaked filesystem details or returned a key", key, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != existing {
		t.Fatal("failed import left staging or removed another version", err)
	}
	got, err := s.Read(context.Background(), existing)
	if err != nil {
		t.Fatal(err)
	}
	expectDataset(t, got, want)
}

func TestReadRejectsInvalidAndMissingKeys(t *testing.T) {
	s, _ := newStore(t)
	for _, key := range []string{"", "../outside", "/absolute", `C:\data`, strings.Repeat("A", 32), strings.Repeat("a", 33)} {
		got, err := s.Read(context.Background(), key)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("unsafe key accepted: %q %v", key, err)
		}
		assertZero(t, got)
	}
	got, err := s.Read(context.Background(), strings.Repeat("0", 32))
	if !errors.Is(err, ErrNotFound) {
		t.Fatal("missing key was not classified", err)
	}
	assertZero(t, got)
}

func TestErrorAfterPublicationNeverDeletesPublishedFiles(t *testing.T) {
	s, directory := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rename := s.rename
	s.rename = func(old, new string) error {
		if err := rename(old, new); err != nil {
			return err
		}
		cancel()
		return nil
	}
	key, err := s.Write(ctx, example())
	if key != "" || !errors.Is(err, context.Canceled) {
		t.Fatal("post-publication cancellation was not reported", key, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || !validHex(entries[0].Name(), 32) {
		t.Fatal("published data was deleted after an uncertain result", err)
	}
	got, err := s.Read(context.Background(), entries[0].Name())
	if err != nil {
		t.Fatal(err)
	}
	expectDataset(t, got, example())
}

func TestReadDetectsFileAndManifestTampering(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(t *testing.T, directory string)
	}{
		{"changed bytes", func(t *testing.T, dir string) {
			mustWrite(t, filepath.Join(dir, "000001.in"), []byte("WRONG"))
		}},
		{"truncated bytes", func(t *testing.T, dir string) {
			mustWrite(t, filepath.Join(dir, "000001.in"), nil)
		}},
		{"extra bytes", func(t *testing.T, dir string) {
			mustWrite(t, filepath.Join(dir, "000001.in"), []byte("TOO-LONG"))
		}},
		{"missing member", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "000001.out")); err != nil {
				t.Fatal(err)
			}
		}},
		{"extra file", func(t *testing.T, dir string) { mustWrite(t, filepath.Join(dir, "extra"), nil) }},
		{"directory member", func(t *testing.T, dir string) {
			if err := os.Mkdir(filepath.Join(dir, "unexpected"), 0750); err != nil {
				t.Fatal(err)
			}
		}},
		{"oversized manifest", func(t *testing.T, dir string) {
			mustWrite(t, filepath.Join(dir, "manifest.json"), bytes.Repeat([]byte(" "), maxManifestBytes+1))
		}},
		{"unknown manifest paths", func(t *testing.T, dir string) {
			editManifest(t, dir, func(m map[string]any) { m["input_path"] = "../secret" })
		}},
		{"format version", func(t *testing.T, dir string) {
			editManifest(t, dir, func(m map[string]any) { m["format_version"] = 2 })
		}},
		{"case count", func(t *testing.T, dir string) {
			editManifest(t, dir, func(m map[string]any) { m["case_count"] = 50000 })
		}},
		{"manifest total bytes", func(t *testing.T, dir string) {
			editManifest(t, dir, func(m map[string]any) { m["total_bytes"] = 0 })
		}},
		{"score binding to digest", func(t *testing.T, dir string) {
			editManifest(t, dir, func(m map[string]any) {
				m["cases"].([]any)[0].(map[string]any)["score"] = 1
				m["total_score"] = 101
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, directory := newStore(t)
			key, err := s.Write(context.Background(), example())
			if err != nil {
				t.Fatal(err)
			}
			tc.edit(t, filepath.Join(directory, key, "data"))
			got, err := s.Read(context.Background(), key)
			if !errors.Is(err, ErrCorrupt) {
				t.Fatal("corrupted dataset accepted", err)
			}
			assertZero(t, got)
		})
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0640); err != nil {
		t.Fatal(err)
	}
}

func editManifest(t *testing.T, directory string, edit func(map[string]any)) {
	t.Helper()
	path := filepath.Join(directory, "manifest.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	raw, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, raw)
}

func TestSymlinkCannotReadOutsideVersion(t *testing.T) {
	s, directory := newStore(t)
	key, err := s.Write(context.Background(), example())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, key, "data", "000001.in")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(directory), "outside-answer")
	mustWrite(t, outside, []byte("outside-secret"))
	if err := os.Symlink(outside, path); err != nil {
		t.Skip("symlink creation unavailable on this host")
	}
	got, err := s.Read(context.Background(), key)
	if !errors.Is(err, ErrCorrupt) || strings.Contains(err.Error(), "outside-secret") {
		t.Fatal("symlink read was not rejected safely", err)
	}
	assertZero(t, got)
}

func TestCanceledWritesAndReadsReturnNoPartialDataset(t *testing.T) {
	s, directory := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	key, err := s.Write(ctx, example())
	if key != "" || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled write accepted", key, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatal("canceled write created files", err)
	}
	// Cancel after staging begins, after at least one member has been written.
	key, err = s.Write(&cancelAfterChecks{Context: context.Background(), limit: 8}, example())
	if key != "" || !errors.Is(err, context.Canceled) {
		t.Fatal("mid-write cancellation accepted", key, err)
	}
	entries, err = os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatal("mid-write cancellation left partial files", err)
	}
	key, err = s.Write(context.Background(), example())
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Read(ctx, key)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled read accepted", err)
	}
	assertZero(t, got)
	got, err = s.Read(&cancelAfterChecks{Context: context.Background(), limit: 8}, key)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("mid-read cancellation accepted", err)
	}
	assertZero(t, got)
}

type cancelAfterChecks struct {
	context.Context
	checks int
	limit  int
}

func (c *cancelAfterChecks) Err() error {
	c.checks++
	if c.checks >= c.limit {
		return context.Canceled
	}
	return nil
}

func TestClosePreventsOperationsWithoutDeletingData(t *testing.T) {
	s, directory := newStore(t)
	key, err := s.Write(context.Background(), example())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal("Close should be idempotent", err)
	}
	if _, err := s.Write(context.Background(), example()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("closed store accepted write", err)
	}
	got, err := s.Read(context.Background(), key)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("closed store accepted read", err)
	}
	assertZero(t, got)
	if _, err := os.Stat(filepath.Join(directory, key, "data", "manifest.json")); err != nil {
		t.Fatal("Close removed published data", err)
	}
}

func TestConcurrentReadersAndImports(t *testing.T) {
	s, _ := newStore(t)
	want := example()
	var wg sync.WaitGroup
	type result struct {
		dataset testcase.Dataset
		err     error
	}
	results := make(chan result, 8)
	for range 8 {
		wg.Go(func() {
			key, err := s.Write(context.Background(), want)
			if err != nil {
				results <- result{err: err}
				return
			}
			got, err := s.Read(context.Background(), key)
			results <- result{dataset: got, err: err}
		})
	}
	wg.Wait()
	close(results)
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		expectDataset(t, result.dataset, want)
	}
}

func TestNewRejectsInvalidRootAndRandomFailureLeavesNoData(t *testing.T) {
	if _, err := New(""); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty root accepted", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	mustWrite(t, file, nil)
	if _, err := New(file); !errors.Is(err, ErrUnavailable) {
		t.Fatal("file accepted as storage root", err)
	}
	s, directory := newStore(t)
	s.random = io.LimitReader(strings.NewReader("short"), 5)
	key, err := s.Write(context.Background(), example())
	if key != "" || !errors.Is(err, ErrUnavailable) {
		t.Fatal("failed entropy source accepted", key, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatal("entropy failure created data", err)
	}
}
