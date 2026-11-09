package judge_runner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"biue/internal/judge"
)

type Backend interface {
	Ready(context.Context) error
	Cleanup(context.Context, bool) error
	Judge(context.Context, judge.Request, func(judge.Status) error) (judge.Result, error)
}
type journalRecord struct {
	Digest string         `json:"digest"`
	State  string         `json:"state"`
	Phases []judge.Status `json:"phases,omitempty"`
	Result *judge.Result  `json:"result,omitempty"`
}
type Journal struct {
	root  *os.Root
	lock  *os.File
	Owner string
}

func NewJournal(directory string) (*Journal, error) {
	abs, err := filepath.Abs(directory)
	if err != nil || directory == "" || filepath.Dir(abs) == abs {
		return nil, ErrSandbox
	}
	if os.MkdirAll(abs, 0700) != nil {
		return nil, ErrSandbox
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrSandbox
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, ErrSandbox
	}
	fail := func() (*Journal, error) { root.Close(); return nil, ErrSandbox }
	lock, err := root.OpenFile(".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fail()
	}
	if lockJournal(lock) != nil {
		lock.Close()
		return fail()
	}
	j := &Journal{root: root, lock: lock}
	owner, err := root.ReadFile("owner")
	if errors.Is(err, os.ErrNotExist) {
		var random [16]byte
		if _, err = rand.Read(random[:]); err == nil {
			owner = []byte(hex.EncodeToString(random[:]))
			err = j.writeFile("owner", owner)
		}
	}
	if err != nil || len(owner) != 32 {
		j.Close()
		return nil, ErrSandbox
	}
	if raw, e := hex.DecodeString(string(owner)); e != nil || len(raw) != 16 {
		j.Close()
		return nil, ErrSandbox
	}
	j.Owner = string(owner)
	return j, nil
}
func (j *Journal) Close() error { j.lock.Close(); return j.root.Close() }
func (j *Journal) writeFile(name string, data []byte) error {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ErrSandbox
	}
	temporary := ".pending-" + hex.EncodeToString(random[:])
	f, err := j.root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return ErrSandbox
	}
	defer j.root.Remove(temporary)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil || j.root.Rename(temporary, name) != nil {
		return ErrSandbox
	}
	return syncJournal(j.root)
}
func (j *Journal) read(task judge.TaskEnvelope) (journalRecord, error) {
	var record journalRecord
	f, err := j.root.Open(taskKey(task) + ".json")
	if err != nil {
		return record, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 2<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil {
		return record, ErrSandbox
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return record, ErrSandbox
	}
	return record, nil
}
func (j *Journal) write(task judge.TaskEnvelope, record journalRecord) error {
	data, err := json.Marshal(record)
	if err != nil || len(data) > 2<<20 {
		return ErrSandbox
	}
	return j.writeFile(taskKey(task)+".json", data)
}

var ErrBusy = errors.New("runner capacity unavailable")
var ErrConflict = errors.New("runner task fingerprint conflict")

type activeJob struct {
	digest string
	done   chan struct{}
	token  string
	cancel context.CancelFunc
}
type Manager struct {
	journal     *Journal
	backend     Backend
	mu          sync.Mutex
	active      map[string]*activeJob
	slots       chan struct{}
	initialized atomic.Bool
}

func NewManager(j *Journal, b Backend, concurrency int) (*Manager, error) {
	if j == nil || b == nil || concurrency < 1 || concurrency > 8 {
		return nil, ErrSandbox
	}
	return &Manager{journal: j, backend: b, active: map[string]*activeJob{}, slots: make(chan struct{}, concurrency)}, nil
}
func (m *Manager) Initialize(ctx context.Context) error {
	if m.backend.Ready(ctx) != nil || m.backend.Cleanup(ctx, true) != nil {
		return ErrSandbox
	}
	m.initialized.Store(true)
	return nil
}
func (m *Manager) Ready(ctx context.Context) error {
	if !m.initialized.Load() {
		return ErrSandbox
	}
	if len(m.slots) == cap(m.slots) {
		return ErrBusy
	}
	return m.BackendReady(ctx)
}
func (m *Manager) BackendReady(ctx context.Context) error {
	if !m.initialized.Load() {
		return ErrSandbox
	}
	return m.backend.Ready(ctx)
}
func (m *Manager) Sweep(ctx context.Context) error { return m.backend.Cleanup(ctx, false) }

func (m *Manager) Judge(ctx context.Context, r judge.Request, token string, emit func(judge.Status) error) (judge.Result, error) {
	if r.Validate() != nil || !m.initialized.Load() {
		return judge.Result{}, ErrSandbox
	}
	raw, _ := json.Marshal(r)
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	key := taskKey(r.Task)
	m.mu.Lock()
	if active := m.active[key]; active != nil {
		if active.digest != digest {
			m.mu.Unlock()
			return judge.Result{}, ErrConflict
		}
		if active.token != token {
			active.cancel()
		}
		done := active.done
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return judge.Result{}, ctx.Err()
		case <-done:
			return m.Judge(ctx, r, token, emit)
		}
	}
	record, err := m.journal.read(r.Task)
	if err == nil {
		if record.Digest != digest {
			m.mu.Unlock()
			return judge.Result{}, ErrConflict
		}
		if record.State == "started" {
			record.State = "complete"
			result := systemResult(r)
			record.Result = &result
			if m.journal.write(r.Task, record) != nil {
				m.mu.Unlock()
				return judge.Result{}, ErrSandbox
			}
		}
		m.mu.Unlock()
		if record.State != "complete" || record.Result == nil || judge.ValidateResult(r, *record.Result) != nil {
			return judge.Result{}, ErrSandbox
		}
		for _, phase := range record.Phases {
			if err := emit(phase); err != nil {
				return judge.Result{}, err
			}
		}
		return *record.Result, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		m.mu.Unlock()
		return judge.Result{}, ErrSandbox
	}
	select {
	case m.slots <- struct{}{}:
	default:
		m.mu.Unlock()
		return judge.Result{}, ErrBusy
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	active := &activeJob{digest: digest, done: make(chan struct{}), token: token, cancel: cancel}
	m.active[key] = active
	defer func() { m.mu.Lock(); delete(m.active, key); close(active.done); <-m.slots; m.mu.Unlock() }()
	record = journalRecord{Digest: digest, State: "started"}
	if m.journal.write(r.Task, record) != nil {
		m.mu.Unlock()
		return judge.Result{}, ErrSandbox
	}
	m.mu.Unlock()
	result, err := m.backend.Judge(work, r, func(phase judge.Status) error { record.Phases = append(record.Phases, phase); return emit(phase) })
	if err != nil || judge.ValidateResult(r, result) != nil {
		result = systemResult(r)
	}
	record.State = "complete"
	record.Result = &result
	if m.journal.write(r.Task, record) != nil {
		return judge.Result{}, ErrSandbox
	}
	return result, nil
}
func systemResult(r judge.Request) judge.Result {
	n := len(r.Cases)
	if r.Kind == judge.Run {
		n = 1
	}
	return judge.Result{Task: r.Task, Status: judge.SystemError, TotalCases: n}
}
