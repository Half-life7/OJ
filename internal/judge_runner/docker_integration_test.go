package judge_runner

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"biue/internal/judge"
)

// Opt-in: requires Linux Docker, seccomp and the four locally built images.
// It creates/removes only containers labeled with its fresh journal owner.
func TestDockerIntegration(t *testing.T) {
	if os.Getenv("BIUE_DOCKER_TEST") != "1" {
		t.Skip("set BIUE_DOCKER_TEST=1 for real Docker acceptance")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("real sandbox acceptance requires Linux")
	}
	j, err := NewJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	socket := os.Getenv("DOCKER_SOCKET")
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	d, err := NewDocker(socket, j.Owner)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if d.Ready(ctx) != nil {
		t.Fatal("Docker/seccomp/four language images unavailable")
	}
	defer func() {
		if d.Cleanup(context.Background(), true) != nil {
			t.Error("acceptance sandbox cleanup failed")
		}
	}()
	sources := map[string]string{
		"cpp17":   "#include <iostream>\nint main(){long long a,b;std::cin>>a>>b;std::cout<<a+b<<'\\n';}",
		"go":      "package main\nimport(\"fmt\";\"os\")\nfunc main(){var a,b int64;fmt.Fscan(os.Stdin,&a,&b);fmt.Println(a+b)}",
		"python3": "a,b=map(int,input().split());print(a+b)",
		"java":    "import java.util.Scanner; public class Main{public static void main(String[] args){Scanner s=new Scanner(System.in);long a=s.nextLong(),b=s.nextLong();System.out.println(a+b);}}",
	}
	id := uint64(100)
	for language, source := range sources {
		t.Run(language, func(t *testing.T) {
			id++
			r := testRequest()
			r.Task.TaskID = id
			r.Kind = judge.Submit
			r.Language = language
			r.SourceCode = source
			r.TimeLimitMS = 3000
			r.MemoryLimitMB = 256
			r.Cases = []judge.Case{{ID: "1", Input: []byte("1 2\n"), Expected: []byte("3\n"), Score: 40}, {ID: "2", Input: []byte("-4 9\n"), Expected: []byte("5\n"), Score: 60}}
			result, err := d.Judge(ctx, r, func(judge.Status) error { return nil })
			if err != nil || result.Status != judge.Accepted || result.Score != 100 || judge.ValidateResult(r, result) != nil {
				t.Fatalf("language=%s status=%s score=%d error=%v diagnostic=%s", language, result.Status, result.Score, err, result.ErrorMessage)
			}
		})
	}
	for _, test := range []struct {
		name, language, source, input string
		limit                         int64
		want                          judge.Status
	}{
		{"compile error", "cpp17", "not valid C++", "", 1000, judge.CompileError},
		{"time limit", "python3", "while True: pass", "", 100, judge.TimeLimitExceeded},
		{"memory limit", "python3", "x=bytearray(512*1024*1024);print(len(x))", "", 2000, judge.MemoryLimitExceeded},
		{"runtime error", "python3", "raise RuntimeError('expected')", "", 1000, judge.RuntimeError},
		{"output limit", "python3", "print('x'*200000)", "", 1000, judge.RuntimeError},
		{"file size limit", "python3", `import errno
try:
 with open('/work/large','wb') as f:
  for _ in range(40): f.write(b'x'*(1024*1024))
 raise AssertionError('file limit absent')
except OSError as e:
 assert e.errno==errno.EFBIG
print('bounded')`, "", 3000, judge.Accepted},
		{"descriptor limit", "python3", `import errno
files=[]
try:
 for _ in range(128): files.append(open('/work/fds','ab'))
 raise AssertionError('descriptor limit absent')
except OSError as e:
 assert e.errno==errno.EMFILE and len(files)<64
finally:
 for f in files: f.close()
print('bounded')`, "", 2000, judge.Accepted},
		{"PID limit", "python3", `import os,time,signal,errno
children=[]
try:
 for _ in range(256):
  p=os.fork()
  if p==0:
   time.sleep(10);os._exit(0)
  children.append(p)
 raise AssertionError('PID limit absent')
except OSError as e:
 assert e.errno==errno.EAGAIN and len(children)<128
finally:
 for p in children:
  try: os.kill(p,signal.SIGKILL)
  except ProcessLookupError: pass
 for p in children: os.waitpid(p,0)
print('bounded')`, "", 3000, judge.Accepted},
		{"isolation", "python3", `import os,socket
assert os.getuid()!=0
try:
 open('/etc/biue-write-probe','w')
 raise AssertionError('rootfs writable')
except OSError: pass
try:
 open('/proc/1/environ','rb').read()
 raise AssertionError('supervisor secret readable')
except PermissionError: pass
s=socket.socket();s.settimeout(0.5)
try:
 s.connect(('1.1.1.1',80))
 raise AssertionError('network available')
except OSError: pass
print('isolated')`, "", 2000, judge.Accepted},
	} {
		t.Run(test.name, func(t *testing.T) {
			id++
			r := testRequest()
			r.Task.TaskID = id
			r.Language = test.language
			r.SourceCode = test.source
			r.Stdin = []byte(test.input)
			r.TimeLimitMS = test.limit
			r.MemoryLimitMB = 64
			if test.name == "PID limit" {
				r.MemoryLimitMB = 256
			}
			result, err := d.Judge(ctx, r, func(judge.Status) error { return nil })
			if err != nil || result.Status != test.want || judge.ValidateResult(r, result) != nil {
				t.Fatalf("status=%s want=%s error=%v diagnostic=%s", result.Status, test.want, err, result.ErrorMessage)
			}
		})
	}
	r := testRequest()
	id++
	r.Task.TaskID = id
	r.Kind = judge.Submit
	r.SourceCode = "print(0)"
	r.Cases = []judge.Case{{ID: "1", Expected: []byte("1"), Score: 100}}
	result, err := d.Judge(ctx, r, func(judge.Status) error { return nil })
	if err != nil || result.Status != judge.WrongAnswer || result.Score != 0 {
		t.Fatalf("WA acceptance: %v %s", err, result.Status)
	}
}
