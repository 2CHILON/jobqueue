package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/2CHILON/jobqueue/internal/queue"
)

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
	id           TEXT PRIMARY KEY,
	type         TEXT NOT NULL,
	payload      TEXT NOT NULL,
	priority     INTEGER NOT NULL DEFAULT 0,
	status       TEXT NOT NULL,
	attempts     INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL,
	result       TEXT NOT NULL DEFAULT '',
	error        TEXT NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,
	updated_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_jobs_claim
	ON jobs (status, priority DESC, created_at ASC);
`

const jobColumns = `id, type, payload, priority, status, attempts, max_attempts, result, error, created_at, updated_at`

// SQLiteStore persists jobs in a single SQLite file.
type SQLiteStore struct {
	db *sql.DB
}

var _ Store = (*SQLiteStore)(nil)

// OpenSQLite opens (or creates) the database file and ensures the schema exists.
func OpenSQLite(path string) (*SQLiteStore, error) {
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One connection: SQLite allows a single writer anyway, and this
	// keeps every operation cleanly serialized.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// ---- helpers ----

type scanner interface {
	Scan(dest ...any) error
}

func scanJob(sc scanner) (*queue.Job, error) {
	var (
		j       queue.Job
		payload string
		status  string
		created int64
		updated int64
	)
	err := sc.Scan(&j.ID, &j.Type, &payload, &j.Priority, &status,
		&j.Attempts, &j.MaxAttempts, &j.Result, &j.Error, &created, &updated)
	if err != nil {
		return nil, err
	}
	j.Payload = []byte(payload)
	j.Status = queue.Status(status)
	j.CreatedAt = time.Unix(0, created).UTC()
	j.UpdatedAt = time.Unix(0, updated).UTC()
	return &j, nil
}

func nowNano() int64 {
	return time.Now().UTC().UnixNano()
}

// updateRunning runs an UPDATE ... WHERE id = ? AND status = 'running' ... RETURNING.
// If no row matched, it works out whether the job is missing or in the wrong state.
func (s *SQLiteStore) updateRunning(id, query string, args ...any) (*queue.Job, error) {
	job, err := scanJob(s.db.QueryRow(query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		var one int
		e := s.db.QueryRow(`SELECT 1 FROM jobs WHERE id = ?`, id).Scan(&one)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if e != nil {
			return nil, e
		}
		return nil, ErrInvalidState
	}
	return job, err
}

// ---- Store implementation ----

func (s *SQLiteStore) Add(job *queue.Job) error {
	_, err := s.db.Exec(
		`INSERT INTO jobs (`+jobColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		job.ID, job.Type, string(job.Payload), job.Priority, string(job.Status),
		job.Attempts, job.MaxAttempts, job.Result, job.Error,
		job.CreatedAt.UnixNano(), job.UpdatedAt.UnixNano(),
	)
	return err
}

func (s *SQLiteStore) Get(id string) (*queue.Job, error) {
	job, err := scanJob(s.db.QueryRow(
		`SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return job, err
}

func (s *SQLiteStore) List(status queue.Status) ([]*queue.Job, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, string(status))
	}
	query += ` ORDER BY created_at ASC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*queue.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Claim picks the best job and marks it running in ONE statement,
// so two workers can never claim the same job.
func (s *SQLiteStore) Claim() (*queue.Job, error) {
	job, err := scanJob(s.db.QueryRow(
		`UPDATE jobs
		 SET status = ?, attempts = attempts + 1, updated_at = ?
		 WHERE id = (
			SELECT id FROM jobs
			WHERE status IN (?, ?)
			ORDER BY priority DESC, created_at ASC
			LIMIT 1
		 )
		 RETURNING `+jobColumns,
		string(queue.StatusRunning), nowNano(),
		string(queue.StatusQueued), string(queue.StatusFailed),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoJobs
	}
	return job, err
}

func (s *SQLiteStore) Complete(id, result string) (*queue.Job, error) {
	return s.updateRunning(id,
		`UPDATE jobs
		 SET status = ?, result = ?, error = '', updated_at = ?
		 WHERE id = ? AND status = ?
		 RETURNING `+jobColumns,
		string(queue.StatusSucceeded), result, nowNano(),
		id, string(queue.StatusRunning),
	)
}

// Fail becomes "dead" when attempts are used up, otherwise "failed" (retryable).
func (s *SQLiteStore) Fail(id, errMsg string) (*queue.Job, error) {
	return s.updateRunning(id,
		`UPDATE jobs
		 SET status = CASE WHEN attempts >= max_attempts THEN ? ELSE ? END,
		     error = ?, updated_at = ?
		 WHERE id = ? AND status = ?
		 RETURNING `+jobColumns,
		string(queue.StatusDead), string(queue.StatusFailed),
		errMsg, nowNano(),
		id, string(queue.StatusRunning),
	)
}
