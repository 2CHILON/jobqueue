package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/2CHILON/jobqueue/internal/queue"
)

var baseTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// ---- helpers ----

// newJob builds a job with a fixed, predictable creation time so that
// "oldest first" ordering is deterministic. order=0 is the oldest.
func newJob(jobType string, priority, order int) *queue.Job {
	j := queue.New(jobType, json.RawMessage(`{"n":1}`), priority)
	j.CreatedAt = baseTime.Add(time.Duration(order) * time.Second)
	j.UpdatedAt = j.CreatedAt
	return j
}

func mustAdd(t *testing.T, s Store, j *queue.Job) {
	t.Helper()
	if err := s.Add(j); err != nil {
		t.Fatalf("Add: %v", err)
	}
}

func mustClaim(t *testing.T, s Store) *queue.Job {
	t.Helper()
	j, err := s.Claim()
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	return j
}

func expectErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("got error %v, want %v", got, want)
	}
}

type storeFactory struct {
	name string
	make func(t *testing.T) Store
}

var factories = []storeFactory{
	{"memory", func(t *testing.T) Store {
		return NewMemory()
	}},
	{"sqlite", func(t *testing.T) Store {
		path := filepath.ToSlash(filepath.Join(t.TempDir(), "test.db"))
		s, err := OpenSQLite(path)
		if err != nil {
			t.Fatalf("OpenSQLite: %v", err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}},
}

// forEachStore runs the same test against every Store implementation.
func forEachStore(t *testing.T, fn func(t *testing.T, s Store)) {
	for _, f := range factories {
		f := f
		t.Run(f.name, func(t *testing.T) {
			fn(t, f.make(t))
		})
	}
}

// ---- tests that run against BOTH stores ----

func TestAddAndGet(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		j := newJob("word_count", 2, 0)
		mustAdd(t, s, j)

		got, err := s.Get(j.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.ID != j.ID || got.Type != "word_count" || got.Priority != 2 {
			t.Errorf("unexpected job: %+v", got)
		}
		if got.Status != queue.StatusQueued {
			t.Errorf("status = %s, want queued", got.Status)
		}
		if got.MaxAttempts != queue.DefaultMaxAttempts {
			t.Errorf("max attempts = %d, want %d", got.MaxAttempts, queue.DefaultMaxAttempts)
		}
		if string(got.Payload) != `{"n":1}` {
			t.Errorf("payload = %s", got.Payload)
		}
		if !got.CreatedAt.Equal(j.CreatedAt) {
			t.Errorf("created_at = %v, want %v", got.CreatedAt, j.CreatedAt)
		}
	})
}

func TestGetNotFound(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		_, err := s.Get("does-not-exist")
		expectErr(t, err, ErrNotFound)
	})
}

func TestListOrderAndFilter(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		a := newJob("a", 0, 2)
		b := newJob("b", 0, 0)
		c := newJob("c", 0, 1)
		mustAdd(t, s, a)
		mustAdd(t, s, b)
		mustAdd(t, s, c)

		all, err := s.List("")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(all) != 3 || all[0].ID != b.ID || all[1].ID != c.ID || all[2].ID != a.ID {
			t.Fatalf("list should be oldest first (b, c, a), got %+v", all)
		}

		// Claiming takes the oldest job (b), moving it to running.
		mustClaim(t, s)

		running, _ := s.List(queue.StatusRunning)
		if len(running) != 1 || running[0].ID != b.ID {
			t.Errorf("running list wrong: %+v", running)
		}
		queued, _ := s.List(queue.StatusQueued)
		if len(queued) != 2 {
			t.Errorf("queued count = %d, want 2", len(queued))
		}
	})
}

func TestClaimEmptyQueue(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		_, err := s.Claim()
		expectErr(t, err, ErrNoJobs)
	})
}

func TestClaimOrder(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		low := newJob("low", 0, 0)
		high1 := newJob("high1", 5, 1)
		high2 := newJob("high2", 5, 2)
		mid := newJob("mid", 3, 3)
		for _, j := range []*queue.Job{low, high1, high2, mid} {
			mustAdd(t, s, j)
		}

		// Highest priority first; ties broken by oldest.
		want := []string{high1.ID, high2.ID, mid.ID, low.ID}
		for i, id := range want {
			got := mustClaim(t, s)
			if got.ID != id {
				t.Fatalf("claim %d: got %s (%s), want %s", i+1, got.ID, got.Type, id)
			}
		}
		_, err := s.Claim()
		expectErr(t, err, ErrNoJobs)
	})
}

func TestClaimMarksRunning(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		j := newJob("x", 0, 0)
		mustAdd(t, s, j)

		claimed := mustClaim(t, s)
		if claimed.Status != queue.StatusRunning || claimed.Attempts != 1 {
			t.Errorf("claimed job: status=%s attempts=%d", claimed.Status, claimed.Attempts)
		}

		stored, _ := s.Get(j.ID)
		if stored.Status != queue.StatusRunning || stored.Attempts != 1 {
			t.Errorf("stored job: status=%s attempts=%d", stored.Status, stored.Attempts)
		}
		if !stored.UpdatedAt.After(j.UpdatedAt) {
			t.Errorf("updated_at should move forward on claim")
		}
	})
}

func TestCompleteSucceeds(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		j := newJob("x", 0, 0)
		mustAdd(t, s, j)
		mustClaim(t, s)

		done, err := s.Complete(j.ID, "all good")
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if done.Status != queue.StatusSucceeded || done.Result != "all good" {
			t.Errorf("unexpected job: %+v", done)
		}

		// A finished job must never be handed out again.
		_, err = s.Claim()
		expectErr(t, err, ErrNoJobs)
	})
}

func TestFailRetriesThenDead(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		j := newJob("x", 0, 0)
		mustAdd(t, s, j)

		for attempt := 1; attempt <= queue.DefaultMaxAttempts; attempt++ {
			claimed := mustClaim(t, s)
			if claimed.Attempts != attempt {
				t.Fatalf("attempt %d: job reports %d attempts", attempt, claimed.Attempts)
			}

			failed, err := s.Fail(j.ID, "boom")
			if err != nil {
				t.Fatalf("Fail: %v", err)
			}
			if failed.Error != "boom" {
				t.Errorf("error = %q, want boom", failed.Error)
			}

			want := queue.StatusFailed
			if attempt == queue.DefaultMaxAttempts {
				want = queue.StatusDead
			}
			if failed.Status != want {
				t.Fatalf("after attempt %d: status = %s, want %s", attempt, failed.Status, want)
			}
		}

		// A dead job is never claimed again.
		_, err := s.Claim()
		expectErr(t, err, ErrNoJobs)
	})
}

func TestInvalidStateTransitions(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		j := newJob("x", 0, 0)
		mustAdd(t, s, j)

		// Not running yet: cannot be completed or failed.
		_, err := s.Complete(j.ID, "nope")
		expectErr(t, err, ErrInvalidState)
		_, err = s.Fail(j.ID, "nope")
		expectErr(t, err, ErrInvalidState)

		mustClaim(t, s)
		if _, err := s.Complete(j.ID, "first"); err != nil {
			t.Fatalf("Complete: %v", err)
		}

		// Already finished: a late or duplicate report must be rejected
		// and must not change the stored result.
		_, err = s.Complete(j.ID, "second")
		expectErr(t, err, ErrInvalidState)
		_, err = s.Fail(j.ID, "late failure")
		expectErr(t, err, ErrInvalidState)

		got, _ := s.Get(j.ID)
		if got.Status != queue.StatusSucceeded || got.Result != "first" {
			t.Errorf("job was modified by an invalid transition: %+v", got)
		}
	})
}

func TestUnknownIDTransitions(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		_, err := s.Complete("nope", "x")
		expectErr(t, err, ErrNotFound)
		_, err = s.Fail("nope", "x")
		expectErr(t, err, ErrNotFound)
	})
}

// Many workers claiming at once must never receive the same job twice.
func TestConcurrentClaimsNeverDuplicate(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		const jobs = 50
		const workers = 8

		for i := 0; i < jobs; i++ {
			mustAdd(t, s, newJob("x", 0, i))
		}

		var (
			mu      sync.Mutex
			claimed = make(map[string]int)
			wg      sync.WaitGroup
		)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					j, err := s.Claim()
					if errors.Is(err, ErrNoJobs) {
						return
					}
					if err != nil {
						t.Errorf("Claim: %v", err)
						return
					}
					mu.Lock()
					claimed[j.ID]++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()

		if len(claimed) != jobs {
			t.Errorf("claimed %d distinct jobs, want %d", len(claimed), jobs)
		}
		for id, n := range claimed {
			if n != 1 {
				t.Errorf("job %s was claimed %d times", id, n)
			}
		}
	})
}

// ---- SQLite-only test ----

// The whole point of the SQLite store: data survives a restart.
func TestSQLitePersistsAcrossReopen(t *testing.T) {
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "persist.db"))

	s1, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	j := newJob("word_count", 1, 0)
	mustAdd(t, s1, j)
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	got, err := s2.Get(j.ID)
	if err != nil {
		t.Fatalf("job missing after reopen: %v", err)
	}
	if got.Type != "word_count" || got.Status != queue.StatusQueued {
		t.Errorf("unexpected job after reopen: %+v", got)
	}
}