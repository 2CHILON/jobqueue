package queue

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Status is the lifecycle state of a job.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed" // failed, but will be retried
	StatusDead      Status = "dead"   // failed too many times, gave up
)

// DefaultMaxAttempts is how many times a job may run before it is marked dead.
const DefaultMaxAttempts = 3

// Job is a unit of work submitted by a client and executed by a worker.
type Job struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	Priority    int             `json:"priority"`
	Status      Status          `json:"status"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	Result      string          `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// New creates a queued job with a fresh ID and sensible defaults.
func New(jobType string, payload json.RawMessage, priority int) *Job {
	now := time.Now().UTC()
	return &Job{
		ID:          newID(),
		Type:        jobType,
		Payload:     payload,
		Priority:    priority,
		Status:      StatusQueued,
		MaxAttempts: DefaultMaxAttempts,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// newID returns a random 16-character hex string.
func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing means the system is badly broken
	}
	return hex.EncodeToString(b)
}
