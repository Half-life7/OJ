package migrations

import (
	"io"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// Validate the embedded migration chain and its version gate without claiming
// that SQL constraints or MySQL DDL have been exercised.
func TestEmbeddedMigrationChain(t *testing.T) {
	source, err := iofs.New(Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	version, err := source.First()
	if err != nil || version != 1 {
		t.Fatalf("first=%d error=%v", version, err)
	}
	for expected := uint(1); expected <= ExpectedVersion; expected++ {
		if version != expected {
			t.Fatalf("version=%d expected=%d", version, expected)
		}
		for _, read := range []func(uint) (io.ReadCloser, string, error){source.ReadUp, source.ReadDown} {
			r, _, err := read(version)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(r)
			r.Close()
			if err != nil || len(data) == 0 {
				t.Fatalf("empty/unreadable migration %d: %v", version, err)
			}
		}
		if expected < ExpectedVersion {
			version, err = source.Next(version)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if next, err := source.Next(version); err == nil {
		t.Fatalf("embedded migration %d exceeds ExpectedVersion", next)
	}
}
