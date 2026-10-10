package store

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/2CHILON/jobqueue/internal/queue"
)

var (
	ErrNotFound     = errors.New("job not found")
	ErrNoJobs       = errors.New("no jobs available")
	ErrInvalidState = errors.New("job is not in a valid state for this operation")
)

// Store is what the API depends on. MemoryStore implements it now;
// a SQLite store will implement the same interface later.
type Store interface {
	Add(job *queue.Job) error
	Get(id string) (*queue.Job, error)
	List(status queue.Status) ([]*queue.Job, error)
	Claim() (*queue.Job, error)
	Complete(id, result string) (*queue.Job, error)
	Fail(id, errMsg string) (*queue.Job, error)
}

// Compile-time check that MemoryStore satisfies Store.
var _ Store = (*MemoryStore)(nil)

// MemoryStore keeps jobs in a map protected by a mutex.
type MemoryStore struct {
	mu   sync.Mutex
	jobs map[string]*queue.Job
}

func NewMemory() *MemoryStore {
	return &MemoryStore{jobs: make(map[string]*queue.Job)}
}

// clone returns a copy so callers can never modify stored data
// without going through the store (which would be a data race).
func clone(j *queue.Job) *queue.Job {
	c := *j
	return &c
}

func (s *MemoryStore) Add(job *queue.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = clone(job)
	return nil
}

func (s *MemoryStore) Get(id string) (*queue.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return clone(j), nil
}

// List returns jobs oldest first. An empty status returns all jobs.
func (s *MemoryStore) List(status queue.Status) ([]*queue.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*queue.Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		if status == "" || j.Status == status {
			out = append(out, clone(j))
		}
	}
	sort.Slice(out, func(a, b int) bool {
		return out[a].CreatedAt.Before(out[b].CreatedAt)
	})
	return out, nil
}

// Claim hands the best available job to exactly one caller.
// "Best" = highest priority, then oldest. Because the whole method
// runs under the lock, two workers can never claim the same job.
func (s *MemoryStore) Claim() (*queue.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var best *queue.Job
	for _, j := range s.jobs {
		if j.Status != queue.StatusQueued && j.Status != queue.StatusFailed {
			continue
		}
		if best == nil ||
			j.Priority > best.Priority ||
			(j.Priority == best.Priority && j.CreatedAt.Before(best.CreatedAt)) {
			best = j
		}
	}
	if best == nil {
		return nil, ErrNoJobs
	}

	best.Status = queue.StatusRunning
	best.Attempts++
	best.UpdatedAt = time.Now().UTC()
	return clone(best), nil
}

// Complete marks a running job as succeeded.
func (s *MemoryStore) Complete(id, result string) (*queue.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	if j.Status != queue.StatusRunning {
		return nil, ErrInvalidState
	}
	j.Status = queue.StatusSucceeded
	j.Result = result
	j.Error = ""
	j.UpdatedAt = time.Now().UTC()
	return clone(j), nil
}

// Fail records an error. If attempts remain the job becomes "failed"
// (eligible to be claimed again); otherwise it becomes "dead".
func (s *MemoryStore) Fail(id, errMsg string) (*queue.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	if j.Status != queue.StatusRunning {
		return nil, ErrInvalidState
	}
	j.Error = errMsg
	if j.Attempts >= j.MaxAttempts {
		j.Status = queue.StatusDead
	} else {
		j.Status = queue.StatusFailed
	}
	j.UpdatedAt = time.Now().UTC()
	return clone(j), nil
}