// sandbox runs only inside the controlled language images. It never runs user
// programs on the host; the Runner starts and destroys its container.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"

	"biue/internal/judge"
	"biue/internal/sandbox"
)

type boundedOutput struct {
	mu       sync.Mutex
	data     []byte
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	keep := min(n, max(0, b.limit-len(b.data)))
	b.data = append(b.data, p[:keep]...)
	if keep < n {
		b.exceeded = true
		b.cancel()
	}
	return n, nil
}

func main() {
	key, err := hex.DecodeString(os.Getenv("OJ_SANDBOX_SECRET"))
	_ = os.Unsetenv("OJ_SANDBOX_SECRET")
	if err != nil || len(key) != 32 {
		os.Exit(1)
	}
	out := execute()
	_ = json.NewEncoder(os.Stdout).Encode(sandbox.Sign(out, key))
}

func execute() sandbox.Outcome {
	out := sandbox.Outcome{ExitCode: -1}
	if protectSupervisor() != nil {
		out.InternalError = true
		return out
	}
	lang, ok := judge.LookupLanguage(os.Getenv("OJ_SANDBOX_LANGUAGE"))
	if !ok {
		out.InternalError = true
		return out
	}
	compile := os.Getenv("OJ_SANDBOX_PHASE") == "compile"
	command := lang.Execute
	if compile {
		command = lang.Compile
	}
	limit, err := strconv.Atoi(os.Getenv("OJ_SANDBOX_TIME_MS"))
	if err != nil || limit < 1 || limit > 30000 && compile || limit > 60000 {
		out.InternalError = true
		return out
	}
	maxOutput, err := strconv.Atoi(os.Getenv("OJ_SANDBOX_OUTPUT_BYTES"))
	if err != nil || maxOutput < 1 || maxOutput > judge.MaxCaseBytes || len(command) == 0 {
		out.InternalError = true
		return out
	}
	var packet sandbox.Input
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 64<<20))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&packet) != nil || decoder.Decode(&extra) != io.EOF || writeInput(packet, lang, compile) != nil {
		out.InternalError = true
		return out
	}
	packet = sandbox.Input{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(limit)*time.Millisecond)
	defer cancel()
	stdout := &boundedOutput{limit: maxOutput, cancel: cancel}
	stderr := &boundedOutput{limit: judge.MaxRunBytes, cancel: cancel}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = "/work"
	cmd.Env = []string{"PATH=/usr/local/go/bin:/opt/java/openjdk/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/tmp", "TMPDIR=/tmp", "LANG=C.UTF-8", "GOCACHE=/tmp/gocache", "GOPATH=/tmp/go", "GOPROXY=off", "GOTOOLCHAIN=local", "GOMAXPROCS=1", "CGO_ENABLED=0"}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if !compile {
		input, err := os.Open("/work/.oj-input")
		if err != nil {
			out.InternalError = true
			return out
		}
		defer input.Close()
		cmd.Stdin = input
	} else {
		cmd.Stdin = io.LimitReader(os.Stdin, 0)
	}
	prepareProcess(cmd)
	cmd.WaitDelay = 250 * time.Millisecond
	start := time.Now()
	err = cmd.Run()
	out.TimeMS = time.Since(start).Milliseconds()
	if cmd.ProcessState != nil {
		out.ExitCode = processCode(cmd.ProcessState)
		out.MemoryKB, out.MemoryMeasured = peakMemory(cmd.ProcessState)
	} else if err != nil {
		out.InternalError = true
	}
	out.TimedOut = ctx.Err() == context.DeadlineExceeded
	out.Stdout, out.Stderr = stdout.data, stderr.data
	out.OutputExceeded = stdout.exceeded || stderr.exceeded
	if compile && out.ExitCode == 0 && !out.TimedOut && !out.OutputExceeded {
		artifacts, err := collectArtifacts(lang.ID)
		if err != nil {
			out.ExitCode = 1
			out.Stderr = []byte("Compiler artifacts are missing, unsafe, or exceed limits.")
		} else {
			out.Artifacts = artifacts
		}
	}
	return out
}

var compiledClass = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*\.class$`)

func writeInput(packet sandbox.Input, lang judge.Language, compile bool) error {
	if len(packet.Stdin) > judge.MaxCaseBytes || len(packet.Files) == 0 || len(packet.Files) > judge.MaxArtifactEntries {
		return os.ErrInvalid
	}
	root, err := os.OpenRoot("/work")
	if err != nil {
		return err
	}
	defer root.Close()
	var total int64
	for name, data := range packet.Files {
		allowed := name == lang.Filename && (compile || lang.ID == "python3") || !compile && (name == "main" && (lang.ID == "cpp17" || lang.ID == "go") || lang.ID == "java" && compiledClass.MatchString(name))
		if !allowed {
			return os.ErrInvalid
		}
		total += int64(len(data))
		if total > judge.MaxArtifactBytes {
			return os.ErrInvalid
		}
		mode := os.FileMode(0600)
		if name == "main" {
			mode = 0700
		}
		f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			return os.ErrInvalid
		}
	}
	if compile || lang.ID == "python3" {
		if len(packet.Files) != 1 || len(packet.Files[lang.Filename]) > judge.MaxSourceBytes {
			return os.ErrInvalid
		}
	} else if lang.ID == "java" {
		if _, ok := packet.Files["Main.class"]; !ok {
			return os.ErrInvalid
		}
	} else {
		if _, ok := packet.Files["main"]; !ok {
			return os.ErrInvalid
		}
	}
	if !compile {
		f, err := root.OpenFile(".oj-input", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(packet.Stdin)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			return os.ErrInvalid
		}
	}
	return nil
}

func collectArtifacts(language string) (map[string][]byte, error) {
	root, err := os.OpenRoot("/work")
	if err != nil {
		return nil, err
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(judge.MaxArtifactEntries + 5)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > judge.MaxArtifactEntries+4 {
		return nil, os.ErrInvalid
	}
	files := map[string][]byte{}
	var total int64
	for _, entry := range entries {
		name := entry.Name()
		allowed := name == "main" && (language == "cpp17" || language == "go") || language == "java" && compiledClass.MatchString(name)
		if !allowed {
			continue
		}
		info, err := root.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || info.Size() > judge.MaxArtifactBytes {
			return nil, os.ErrInvalid
		}
		total += info.Size()
		if total > judge.MaxArtifactBytes || len(files) >= judge.MaxArtifactEntries {
			return nil, os.ErrInvalid
		}
		f, err := root.Open(name)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(f, judge.MaxArtifactBytes+1))
		f.Close()
		if err != nil || int64(len(data)) != info.Size() {
			return nil, os.ErrInvalid
		}
		files[name] = data
	}
	needed := "main"
	if language == "java" {
		needed = "Main.class"
	}
	if _, ok := files[needed]; !ok {
		return nil, os.ErrInvalid
	}
	return files, nil
}
