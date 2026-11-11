package problem

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestParseBuiltInProblemPackage(t *testing.T) {
	pkg, err := ParsePackageDirectory(context.Background(), filepath.Join("..", "..", "导入样例"))
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Preview.Title != "A+B 输入输出练习I" || pkg.Preview.TotalScore != 100 || len(pkg.Preview.Cases) != 1 ||
		pkg.Dataset.Cases[0].ID != "1" || pkg.Dataset.TotalScore != 100 || pkg.Preview.SampleInput == "" || pkg.Preview.SampleOutput == "" {
		t.Fatalf("unexpected package: %+v", pkg.Preview)
	}
	if len(pkg.Preview.Digest) != 64 || pkg.Input.Status != "draft" || pkg.Input.Visibility != "hidden" {
		t.Fatalf("unsafe defaults: %+v", pkg.Input)
	}
}

func TestParseProblemPackageZIPAndRejectUnknownFiles(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, entry := range []struct{ name, body string }{
		{"problem.yaml", "title: ZIP problem\ntag:\n  - test\n"},
		{"problem_zh.md", "# 题目描述\n\nZIP body\n\n## 输入\n\ninput\n\n## 输出\n\noutput\n"},
		{"testdata/config.yaml", "subtasks:\n  - score: 100\n    cases:\n      - input: 1.in\n        output: 1.out\n"},
		{"testdata/1.in", "1\n"},
		{"testdata/1.out", "1\n"},
	} {
		file, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte(entry.body))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	pkg, err := ParsePackageZIP(context.Background(), archive.Bytes())
	if err != nil || pkg.Preview.Title != "ZIP problem" || pkg.Preview.TotalScore != 100 {
		t.Fatalf("package=%+v err=%v", pkg.Preview, err)
	}
	bad := append([]byte("MZ"), archive.Bytes()...)
	if _, err := ParsePackageZIP(context.Background(), bad); !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("prefix accepted: %v", err)
	}
}

func TestParseWrappedSampleProblemPackageZIP(t *testing.T) {
	path := filepath.Join("..", "..", "导入样例", "菲波那契数.zip")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := ParsePackageZIP(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Preview.Title != "菲波那契数" || pkg.Input.TimeLimitMS != 1000 || pkg.Input.MemoryLimitMB != 128 ||
		len(pkg.Preview.Cases) != 10 || pkg.Preview.TotalScore != 100 || pkg.Dataset.TotalScore != 100 {
		t.Fatalf("unexpected wrapped package: preview=%+v input=%+v", pkg.Preview, pkg.Input)
	}
	for index, item := range pkg.Preview.Cases {
		want := "problem" + strconv.Itoa(index+1)
		if item.ID != want || item.Score != 10 {
			t.Fatalf("case %d=%+v, want id=%s score=10", index, item, want)
		}
	}
}

func TestPackageDirectoryRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	for _, source := range []string{"problem.yaml", "problem_zh.md", filepath.Join("testdata", "config.yaml"), filepath.Join("testdata", "1.in"), filepath.Join("testdata", "1.out")} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, source)), 0750)
		_ = os.WriteFile(filepath.Join(dir, source), []byte("title: x\n"), 0640)
	}
	if err := os.Symlink(filepath.Join(dir, "problem.yaml"), filepath.Join(dir, "problem_link.md")); err == nil {
		if _, err := ParsePackageDirectory(context.Background(), dir); !errors.Is(err, ErrInvalidPackage) {
			t.Fatalf("symlink accepted: %v", err)
		}
	}
}
