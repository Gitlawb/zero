package cron

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	StatusActive = "active"
	StatusPaused = "paused"
)

// ErrJobNotFound is returned (wrapped) by Get when a job's metadata file is
// absent — a genuinely removed job. Callers use errors.Is to distinguish this
// from a transient read failure (IO error, permission), which must NOT be
// treated as "job removed".
var ErrJobNotFound = errors.New("cron job not found")

// Job is a stored scheduled job.
type Job struct {
	ID        string    `json:"id"`
	Expr      string    `json:"expr"`
	Prompt    string    `json:"prompt"`
	Cwd       string    `json:"cwd,omitempty"`
	Model     string    `json:"model,omitempty"`
	Status    string    `json:"status"`
	FireCount int       `json:"fireCount"`
	NextRunAt time.Time `json:"nextRunAt"`
	CreatedAt time.Time `json:"createdAt"`
}

// RunRecord is one fire's outcome, appended to the job's runs.jsonl.
type RunRecord struct {
	JobID        string    `json:"jobId"`
	At           time.Time `json:"at"`
	ExitCode     int       `json:"exitCode"`
	SessionTitle string    `json:"sessionTitle,omitempty"`
	Error        string    `json:"error,omitempty"`
}

// StoreOptions configures a Store. RootDir defaults to DefaultRoot(os env); Now
// defaults to time.Now. Both injectable for tests.
type StoreOptions struct {
	RootDir string
	Now     func() time.Time
}

type Store struct {
	root string
	now  func() time.Time
}

func NewStore(opts StoreOptions) *Store {
	root := opts.RootDir
	if root == "" {
		root = DefaultRoot(envMap())
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Store{root: root, now: now}
}

// DefaultRoot mirrors sessions.DefaultRoot: <XDG_DATA_HOME|~/.local/share>/zero/cron.
func DefaultRoot(env map[string]string) string {
	dataHome := strings.TrimSpace(env["XDG_DATA_HOME"])
	home := strings.TrimSpace(env["HOME"])
	if home == "" {
		// Mirror sessions.DefaultRoot: fall back to the OS user home so an unset
		// HOME (Windows, restricted shells) doesn't yield a RELATIVE ".local/share"
		// under the caller's cwd, which would scatter cron data per working dir.
		if userHome, err := os.UserHomeDir(); err == nil {
			home = userHome
		}
	}
	base := dataHome
	if base == "" {
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "zero", "cron")
}

func envMap() map[string]string {
	return map[string]string{"XDG_DATA_HOME": os.Getenv("XDG_DATA_HOME"), "HOME": os.Getenv("HOME")}
}

// Add assigns an ID + CreatedAt and writes the job's metadata.json.
func (s *Store) Add(job Job) (Job, error) {
	if job.Status == "" {
		job.Status = StatusActive
	}
	job.CreatedAt = s.now().UTC()
	id, err := s.allocID()
	if err != nil {
		return Job{}, err
	}
	job.ID = id
	if err := s.writeJob(job); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *Store) allocID() (string, error) {
	base := s.now().UTC().Format("20060102-150405")
	for n := 0; n < 100; n++ {
		id := base
		if n > 0 {
			id = fmt.Sprintf("%s-%d", base, n)
		}
		if _, err := os.Stat(filepath.Join(s.root, id)); errors.Is(err, os.ErrNotExist) {
			return id, nil
		}
	}
	return "", errors.New("could not allocate a unique cron job id")
}

func (s *Store) jobDir(id string) string { return filepath.Join(s.root, id) }

// validID rejects ids that could escape the store root (path separators or
// traversal). allocID-generated timestamp ids always pass; this guards
// externally-supplied ids (get/update/remove/append).
func validID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`) && filepath.Base(id) == id
}

func (s *Store) writeJob(job Job) error {
	dir := s.jobDir(job.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "metadata.json.tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "metadata.json"))
}

func (s *Store) Get(id string) (Job, error) {
	if !validID(id) {
		return Job{}, fmt.Errorf("invalid cron job id %q", id)
	}
	data, err := os.ReadFile(filepath.Join(s.jobDir(id), "metadata.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Job{}, fmt.Errorf("cron job %q: %w", id, ErrJobNotFound)
		}
		// A transient read failure (IO error, permission) is NOT a missing job;
		// surface it as a distinct error so callers don't mistake it for removal.
		return Job{}, fmt.Errorf("read cron job %q: %w", id, err)
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return Job{}, fmt.Errorf("cron job %q is corrupt: %w", id, err)
	}
	return job, nil
}

func (s *Store) Update(job Job) error {
	if !validID(job.ID) {
		return fmt.Errorf("invalid cron job id %q", job.ID)
	}
	if _, err := os.Stat(s.jobDir(job.ID)); err != nil {
		return fmt.Errorf("cron job %q not found", job.ID)
	}
	return s.writeJob(job)
}

func (s *Store) Remove(id string) error {
	if !validID(id) {
		return fmt.Errorf("invalid cron job id %q", id)
	}
	if _, err := os.Stat(s.jobDir(id)); err != nil {
		return fmt.Errorf("cron job %q not found", id)
	}
	return os.RemoveAll(s.jobDir(id))
}

func (s *Store) List() ([]Job, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var jobs []Job
	var corrupt []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, statErr := os.Stat(filepath.Join(s.jobDir(e.Name()), "metadata.json")); statErr != nil {
			continue // a directory without metadata.json is not a job
		}
		job, gerr := s.Get(e.Name())
		if gerr != nil {
			corrupt = append(corrupt, e.Name())
			continue
		}
		jobs = append(jobs, job)
	}
	// Surface corrupt jobs (jobs slice is still authoritative; callers treat this
	// as a warning, not a fatal error, so one bad job never hides the rest).
	if len(corrupt) > 0 {
		return jobs, fmt.Errorf("skipped %d unreadable cron job(s): %s", len(corrupt), strings.Join(corrupt, ", "))
	}
	return jobs, nil
}

func (s *Store) AppendRun(id string, rec RunRecord) error {
	if !validID(id) {
		return fmt.Errorf("invalid cron job id %q", id)
	}
	dir := s.jobDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "runs.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	line, err := json.Marshal(rec)
	if err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	// Surface a buffered-write failure that only materializes on Close.
	return f.Close()
}

func (s *Store) Runs(id string) ([]RunRecord, error) {
	if !validID(id) {
		return nil, fmt.Errorf("invalid cron job id %q", id)
	}
	f, err := os.Open(filepath.Join(s.jobDir(id), "runs.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var runs []RunRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var rec RunRecord
		if json.Unmarshal(scanner.Bytes(), &rec) == nil {
			runs = append(runs, rec)
		}
	}
	return runs, scanner.Err()
}
