package problem

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"biue/internal/testcase"

	"github.com/goccy/go-yaml"
)

const (
	maxPackageArchiveBytes = 32 << 20
	maxPackageMetadata     = 128 << 10
	maxPackageEntries      = 1028
)

var ErrInvalidPackage = errors.New("invalid problem package")

type PackageCase struct {
	ID          string `json:"id"`
	Score       int64  `json:"score"`
	InputBytes  int64  `json:"input_bytes"`
	OutputBytes int64  `json:"output_bytes"`
}

type PackagePreview struct {
	Title        string        `json:"title"`
	Description  string        `json:"description"`
	InputFormat  string        `json:"input_format"`
	OutputFormat string        `json:"output_format"`
	SampleInput  string        `json:"sample_input"`
	SampleOutput string        `json:"sample_output"`
	Tags         []string      `json:"tags"`
	Cases        []PackageCase `json:"cases"`
	TotalScore   int64         `json:"total_score"`
	Digest       string        `json:"digest"`
	Warnings     []string      `json:"warnings"`
}

type ProblemPackage struct {
	Input   Input
	Dataset testcase.Dataset
	Preview PackagePreview
}

type packageProblemYAML struct {
	Title         string   `yaml:"title"`
	Tags          []string `yaml:"tag"`
	TagsAlternate []string `yaml:"tags"`
	Difficulty    string   `yaml:"difficulty"`
	TimeLimitMS   int64    `yaml:"time_limit_ms"`
	MemoryLimitMB int64    `yaml:"memory_limit_mb"`
}

type packageTestConfig struct {
	Time          string `yaml:"time"`
	TimeLimitMS   int64  `yaml:"time_limit_ms"`
	Memory        string `yaml:"memory"`
	MemoryLimitMB int64  `yaml:"memory_limit_mb"`
	Subtasks      []struct {
		Score int64 `yaml:"score"`
		Cases []struct {
			Input  string `yaml:"input"`
			Output string `yaml:"output"`
			Score  *int64 `yaml:"score"`
		} `yaml:"cases"`
	} `yaml:"subtasks"`
}

func ParsePackageDirectory(ctx context.Context, directory string) (ProblemPackage, error) {
	if ctx == nil || strings.TrimSpace(directory) == "" {
		return ProblemPackage{}, ErrInvalidPackage
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return ProblemPackage{}, ErrInvalidPackage
	}
	files := map[string][]byte{}
	add := func(name string) error {
		info, err := os.Lstat(filepath.Join(directory, name))
		if err != nil || !info.Mode().IsRegular() || info.Size() > testcase.DefaultLimits().MaxFileBytes {
			return ErrInvalidPackage
		}
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return ErrInvalidPackage
		}
		files[name] = data
		return nil
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return ProblemPackage{}, err
		}
		switch {
		case entry.IsDir() && entry.Name() == "testdata":
			cases, err := os.ReadDir(filepath.Join(directory, "testdata"))
			if err != nil {
				return ProblemPackage{}, ErrInvalidPackage
			}
			for _, item := range cases {
				if item.IsDir() {
					continue
				}
				if err := add("testdata/" + item.Name()); err != nil {
					return ProblemPackage{}, err
				}
			}
		case entry.IsDir():
			continue
		case strings.EqualFold(filepath.Ext(entry.Name()), ".zip"):
			// The built-in sample directory also stores import examples.
			continue
		default:
			if err := add(entry.Name()); err != nil {
				return ProblemPackage{}, err
			}
		}
	}
	return parsePackageFiles(ctx, files)
}

func ParsePackageZIP(ctx context.Context, raw []byte) (ProblemPackage, error) {
	if ctx == nil || len(raw) < 22 || len(raw) > maxPackageArchiveBytes {
		return ProblemPackage{}, ErrInvalidPackage
	}
	if err := validatePackageZIP32(raw); err != nil {
		return ProblemPackage{}, err
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil || len(reader.File) == 0 || len(reader.File) > maxPackageEntries {
		return ProblemPackage{}, ErrInvalidPackage
	}
	files := map[string][]byte{}
	var total int64
	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return ProblemPackage{}, err
		}
		name := file.Name
		directory := file.FileInfo().IsDir()
		cleanName := name
		if directory && strings.HasSuffix(cleanName, "/") {
			cleanName = strings.TrimSuffix(cleanName, "/")
		}
		if cleanName == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") ||
			path.Clean(cleanName) != cleanName || strings.Contains(cleanName, "/../") {
			return ProblemPackage{}, ErrInvalidPackage
		}
		if directory {
			continue
		}
		if !file.Mode().IsRegular() || file.UncompressedSize64 > uint64(testcase.DefaultLimits().MaxFileBytes) || total+int64(file.UncompressedSize64) > testcase.DefaultLimits().MaxTotalBytes+maxPackageMetadata {
			return ProblemPackage{}, ErrInvalidPackage
		}
		if _, duplicate := files[name]; duplicate {
			return ProblemPackage{}, ErrInvalidPackage
		}
		rc, err := file.Open()
		if err != nil {
			return ProblemPackage{}, ErrInvalidPackage
		}
		data, readErr := io.ReadAll(io.LimitReader(rc, int64(file.UncompressedSize64)+1))
		closeErr := rc.Close()
		if readErr != nil || closeErr != nil || int64(len(data)) != int64(file.UncompressedSize64) {
			return ProblemPackage{}, ErrInvalidPackage
		}
		total += int64(len(data))
		files[name] = data
	}
	files = stripSinglePackageRoot(files)
	return parsePackageFiles(ctx, files)
}

func stripSinglePackageRoot(files map[string][]byte) map[string][]byte {
	if _, ok := files["problem.yaml"]; ok {
		return files
	}
	root := ""
	for name := range files {
		first, _, nested := strings.Cut(name, "/")
		if !nested || first == "" || strings.EqualFold(first, "testdata") {
			return files
		}
		if root == "" {
			root = first
		} else if root != first {
			return files
		}
	}
	if root == "" {
		return files
	}
	prefix := root + "/"
	normalized := make(map[string][]byte, len(files))
	for name, data := range files {
		if !strings.HasPrefix(name, prefix) {
			return files
		}
		normalized[strings.TrimPrefix(name, prefix)] = data
	}
	return normalized
}

func validatePackageZIP32(raw []byte) error {
	if len(raw) < 4 || !bytes.Equal(raw[:4], []byte{'P', 'K', 3, 4}) {
		return ErrInvalidPackage
	}
	signature := []byte{'P', 'K', 5, 6}
	index := bytes.LastIndex(raw, signature)
	if index < 0 || index+22 > len(raw) {
		return ErrInvalidPackage
	}
	comment := int(binary.LittleEndian.Uint16(raw[index+20 : index+22]))
	if index+22+comment != len(raw) || binary.LittleEndian.Uint16(raw[index+4:index+6]) != 0 || binary.LittleEndian.Uint16(raw[index+6:index+8]) != 0 ||
		binary.LittleEndian.Uint16(raw[index+8:index+10]) != binary.LittleEndian.Uint16(raw[index+10:index+12]) ||
		binary.LittleEndian.Uint16(raw[index+10:index+12]) == 0xffff || binary.LittleEndian.Uint32(raw[index+16:index+20]) == 0xffffffff {
		return ErrInvalidPackage
	}
	return nil
}

func parsePackageFiles(ctx context.Context, files map[string][]byte) (ProblemPackage, error) {
	var metadata []byte
	var markdownName string
	var markdown []byte
	var config []byte
	testFiles := make([]testcase.File, 0)
	hash := sha256.New()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		data := files[name]
		if strings.EqualFold(name, "README.md") {
			continue
		}
		_, _ = hash.Write([]byte(name))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
		switch {
		case name == "problem.yaml":
			metadata = data
		case strings.HasPrefix(name, "problem") && strings.HasSuffix(name, ".md") && !strings.Contains(name, "/"):
			if markdownName != "" {
				return ProblemPackage{}, ErrInvalidPackage
			}
			markdownName, markdown = name, data
		case name == "testdata/config.yaml":
			config = data
		case strings.HasPrefix(name, "testdata/") && (strings.HasSuffix(name, ".in") || strings.HasSuffix(name, ".out")):
			testFiles = append(testFiles, testcase.File{Name: strings.TrimPrefix(name, "testdata/"), Data: data})
		default:
			return ProblemPackage{}, ErrInvalidPackage
		}
	}
	if len(metadata) == 0 || len(markdown) == 0 || len(config) == 0 || len(metadata) > maxPackageMetadata || len(markdown) > 64<<10 || len(config) > maxPackageMetadata {
		return ProblemPackage{}, ErrInvalidPackage
	}
	var info packageProblemYAML
	if err := yaml.Unmarshal(metadata, &info); err != nil || strings.TrimSpace(info.Title) == "" {
		return ProblemPackage{}, ErrInvalidPackage
	}
	var testConfig packageTestConfig
	if err := yaml.Unmarshal(config, &testConfig); err != nil {
		return ProblemPackage{}, ErrInvalidPackage
	}
	available := map[string][]byte{}
	for _, file := range testFiles {
		available[file.Name] = file.Data
	}
	used := map[string]bool{}
	scores := map[string]int64{}
	warnings := []string{}
	if len(testConfig.Subtasks) == 0 {
		inputs := make([]string, 0)
		for name := range available {
			if strings.HasSuffix(name, ".in") {
				inputs = append(inputs, name)
			}
		}
		if len(inputs) == 0 || len(inputs)*2 != len(available) {
			return ProblemPackage{}, ErrInvalidPackage
		}
		sort.Slice(inputs, func(i, j int) bool { return naturalLess(inputs[i], inputs[j]) })
		for index, inputName := range inputs {
			base := strings.TrimSuffix(inputName, ".in")
			outputName := base + ".out"
			if _, ok := available[outputName]; !ok {
				return ProblemPackage{}, ErrInvalidPackage
			}
			score := int64(100 / len(inputs))
			if index == len(inputs)-1 {
				score += int64(100 % len(inputs))
			}
			scores[base] = score
			used[inputName], used[outputName] = true, true
		}
		warnings = append(warnings, "config.yaml 未提供 subtasks，已按文件名自然顺序把全部测试点平均分配到 100 分")
	} else {
		for _, subtask := range testConfig.Subtasks {
			if subtask.Score < 0 || len(subtask.Cases) == 0 {
				return ProblemPackage{}, ErrInvalidPackage
			}
			for index, item := range subtask.Cases {
				inputName := path.Base(strings.TrimSpace(item.Input))
				outputName := path.Base(strings.TrimSpace(item.Output))
				_, inputOK := available[inputName]
				_, outputOK := available[outputName]
				if inputName != item.Input || outputName != item.Output || !strings.HasSuffix(inputName, ".in") || !strings.HasSuffix(outputName, ".out") ||
					strings.TrimSuffix(inputName, ".in") != strings.TrimSuffix(outputName, ".out") || !inputOK || !outputOK {
					return ProblemPackage{}, ErrInvalidPackage
				}
				id := strings.TrimSuffix(inputName, ".in")
				if _, duplicate := scores[id]; duplicate {
					return ProblemPackage{}, ErrInvalidPackage
				}
				score := subtask.Score / int64(len(subtask.Cases))
				if index == len(subtask.Cases)-1 {
					score += subtask.Score % int64(len(subtask.Cases))
				}
				if item.Score != nil {
					score = *item.Score
				}
				if score < 0 {
					return ProblemPackage{}, ErrInvalidPackage
				}
				scores[id] = score
				used[inputName], used[outputName] = true, true
			}
			if len(subtask.Cases) > 1 && subtask.Score%int64(len(subtask.Cases)) != 0 {
				warnings = append(warnings, "同一子任务包含多个测试点，分值余数已分配给该子任务最后一个测试点")
			}
		}
	}
	for name := range available {
		if !used[name] {
			return ProblemPackage{}, ErrInvalidPackage
		}
	}
	sort.Slice(testFiles, func(i, j int) bool { return naturalLess(testFiles[i].Name, testFiles[j].Name) })
	dataset, err := testcase.ParseFiles(ctx, testFiles, scores, testcase.DefaultLimits())
	if err != nil {
		return ProblemPackage{}, ErrInvalidPackage
	}
	description, inputFormat, outputFormat, sampleInput, sampleOutput := splitPackageMarkdown(string(markdown))
	if strings.TrimSpace(description) == "" {
		description = string(markdown)
		warnings = append(warnings, "未识别标准 Markdown 标题，完整原文已放入题目描述")
	}
	tags := append([]string(nil), info.Tags...)
	tags = append(tags, info.TagsAlternate...)
	timeLimitMS := info.TimeLimitMS
	memoryLimitMB := info.MemoryLimitMB
	if testConfig.TimeLimitMS > 0 {
		timeLimitMS = testConfig.TimeLimitMS
	} else if strings.TrimSpace(testConfig.Time) != "" {
		parsed, parseErr := parsePackageTime(testConfig.Time)
		if parseErr != nil || parsed <= 0 {
			return ProblemPackage{}, ErrInvalidPackage
		}
		timeLimitMS = parsed
	}
	if testConfig.MemoryLimitMB > 0 {
		memoryLimitMB = testConfig.MemoryLimitMB
	} else if strings.TrimSpace(testConfig.Memory) != "" {
		parsed, parseErr := parsePackageMemory(testConfig.Memory)
		if parseErr != nil || parsed <= 0 {
			return ProblemPackage{}, ErrInvalidPackage
		}
		memoryLimitMB = parsed
	}
	input := Input{
		ProblemType: "programming", CheckerMode: "tokens", Title: strings.TrimSpace(info.Title),
		Description: description, InputFormat: inputFormat, OutputFormat: outputFormat,
		SampleInput: sampleInput, SampleOutput: sampleOutput,
		TimeLimitMS: timeLimitMS, MemoryLimitMB: memoryLimitMB,
		Difficulty: info.Difficulty, Tags: tags, Status: "draft", Visibility: "hidden",
	}
	if input.TimeLimitMS == 0 {
		input.TimeLimitMS = 1000
	}
	if input.MemoryLimitMB == 0 {
		input.MemoryLimitMB = 256
	}
	if input.Difficulty == "" {
		input.Difficulty = "medium"
	}
	normalized, err := normalizeInput(input)
	if err != nil {
		return ProblemPackage{}, ErrInvalidPackage
	}
	preview := PackagePreview{
		Title: normalized.Title, Description: normalized.Description, InputFormat: normalized.InputFormat,
		OutputFormat: normalized.OutputFormat, SampleInput: normalized.SampleInput, SampleOutput: normalized.SampleOutput,
		Tags: normalized.Tags, Cases: make([]PackageCase, 0, len(dataset.Cases)), TotalScore: dataset.TotalScore,
		Digest: hex.EncodeToString(hash.Sum(nil)), Warnings: warnings,
	}
	for _, item := range dataset.Cases {
		preview.Cases = append(preview.Cases, PackageCase{ID: item.ID, Score: item.Score, InputBytes: int64(len(item.Input)), OutputBytes: int64(len(item.Output))})
	}
	sort.Slice(preview.Cases, func(i, j int) bool {
		return naturalLess(preview.Cases[i].ID+".in", preview.Cases[j].ID+".in")
	})
	return ProblemPackage{Input: normalized, Dataset: dataset, Preview: preview}, nil
}

func naturalLess(a, b string) bool {
	ia, ib := 0, 0
	for ia < len(a) && ib < len(b) {
		da := a[ia] >= '0' && a[ia] <= '9'
		db := b[ib] >= '0' && b[ib] <= '9'
		if da && db {
			ja, jb := ia, ib
			for ja < len(a) && a[ja] >= '0' && a[ja] <= '9' {
				ja++
			}
			for jb < len(b) && b[jb] >= '0' && b[jb] <= '9' {
				jb++
			}
			na := strings.TrimLeft(a[ia:ja], "0")
			nb := strings.TrimLeft(b[ib:jb], "0")
			if na == "" {
				na = "0"
			}
			if nb == "" {
				nb = "0"
			}
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			ia, ib = ja, jb
			continue
		}
		if a[ia] != b[ib] {
			return a[ia] < b[ib]
		}
		ia++
		ib++
	}
	return len(a) < len(b)
}

func parsePackageTime(raw string) (int64, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if strings.HasSuffix(value, "ms") {
		return strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(value, "ms")), 10, 64)
	}
	if strings.HasSuffix(value, "s") {
		n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(value, "s")), 10, 64)
		return n * 1000, err
	}
	return strconv.ParseInt(value, 10, 64)
}

func parsePackageMemory(raw string) (int64, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	for _, suffix := range []string{"mib", "mb", "m"} {
		if strings.HasSuffix(value, suffix) {
			return strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(value, suffix)), 10, 64)
		}
	}
	return strconv.ParseInt(value, 10, 64)
}

var codeFenceInput = regexp.MustCompile("(?ms)^```input[^\\n]*\\n(.*?)^```\\s*$")
var codeFenceOutput = regexp.MustCompile("(?ms)^```output[^\\n]*\\n(.*?)^```\\s*$")

func splitPackageMarkdown(source string) (description, input, output, sampleInput, sampleOutput string) {
	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
	sections := map[string][]string{}
	current := "description"
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimLeft(line, "#"))
		switch {
		case strings.EqualFold(trimmed, "题目描述"):
			current = "description"
			continue
		case strings.EqualFold(trimmed, "输入") || strings.EqualFold(trimmed, "输入格式"):
			current = "input"
			continue
		case strings.EqualFold(trimmed, "输出") || strings.EqualFold(trimmed, "输出格式"):
			current = "output"
			continue
		case strings.EqualFold(trimmed, "样例"):
			current = "samples"
			continue
		}
		sections[current] = append(sections[current], line)
	}
	description = strings.TrimSpace(strings.Join(sections["description"], "\n"))
	input = strings.TrimSpace(strings.Join(sections["input"], "\n"))
	output = strings.TrimSpace(strings.Join(sections["output"], "\n"))
	if match := codeFenceInput.FindStringSubmatch(source); len(match) == 2 {
		sampleInput = strings.TrimSuffix(match[1], "\n")
	}
	if match := codeFenceOutput.FindStringSubmatch(source); len(match) == 2 {
		sampleOutput = strings.TrimSuffix(match[1], "\n")
	}
	return
}
