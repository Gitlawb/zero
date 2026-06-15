package swarm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Mailbox is a per-agent, per-team message inbox persisted as a JSON array on
// disk. It mirrors reference-swarm-code-agent-js/communication/mailbox.js
// (inbox path <baseDir>/<team>/inboxes/<agent>.json, lock file, atomic write,
// {from,type,read} message shape) and hardens it for untrusted input:
//
//   - inbox files and their parent dirs are owner-only (0600/0700);
//   - every write takes an exclusive lock file (bounded retry, stale-break);
//   - message bodies and whole inboxes are size-capped (fail closed on oversize);
//   - team/agent names are sanitized so a name can never escape the base dir;
//   - malformed inbox JSON is rejected rather than silently reset (fail closed).
type Mailbox struct {
	// BaseDir is the root under which <team>/inboxes/<agent>.json live. It must
	// be set (NewMailbox enforces this).
	BaseDir string
	// MaxMessageBytes caps a single serialized message. 0 => defaultMaxMessageBytes.
	MaxMessageBytes int
	// MaxMessages caps the number of messages retained in one inbox. 0 =>
	// defaultMaxMessages. Sends past the cap fail closed.
	MaxMessages int
	// LockTimeout bounds how long Send/MarkRead wait for the inbox lock.
	LockTimeout time.Duration
}

const (
	defaultMaxMessageBytes = 64 * 1024 // 64 KiB per message
	defaultMaxMessages     = 1000      // per inbox
	defaultLockTimeout     = 5 * time.Second
	lockStaleAfter         = 30 * time.Second
	inboxFileMode          = 0o600
	inboxDirMode           = 0o700
)

// ErrMailboxFull is returned when an inbox is at MaxMessages.
var ErrMailboxFull = errors.New("swarm: mailbox is full")

// ErrMessageTooLarge is returned when a single message exceeds MaxMessageBytes.
var ErrMessageTooLarge = errors.New("swarm: message exceeds size cap")

// Message is one inbox entry. Mirrors the reference message shape
// ({from, type, read, ...}); Time is RFC3339 and set on Send if empty.
type Message struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body"`
	Type    string `json:"type"`
	Read    bool   `json:"read"`
	Time    string `json:"time,omitempty"`
}

// NewMailbox validates baseDir and returns a Mailbox with defaults applied.
func NewMailbox(baseDir string) (*Mailbox, error) {
	if strings.TrimSpace(baseDir) == "" {
		return nil, errors.New("swarm: mailbox base dir is required")
	}
	abs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("swarm: resolve mailbox base dir: %w", err)
	}
	return &Mailbox{
		BaseDir:         abs,
		MaxMessageBytes: defaultMaxMessageBytes,
		MaxMessages:     defaultMaxMessages,
		LockTimeout:     defaultLockTimeout,
	}, nil
}

var nameUnsafe = regexp.MustCompile(`[^a-zA-Z0-9\-_]`)

// sanitizeName mirrors mailbox.js sanitizeTeamName: replace any char outside
// [A-Za-z0-9-_] with '-', defaulting empty input to "default" and an all-unsafe
// result to "unknown". This guarantees the name is a single safe path segment
// (no '/', '\\', '.', '..').
func sanitizeName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		trimmed = "default"
	}
	cleaned := nameUnsafe.ReplaceAllString(trimmed, "-")
	if cleaned == "" {
		return "unknown"
	}
	return cleaned
}

func (m *Mailbox) maxMessageBytes() int {
	if m.MaxMessageBytes > 0 {
		return m.MaxMessageBytes
	}
	return defaultMaxMessageBytes
}

func (m *Mailbox) maxMessages() int {
	if m.MaxMessages > 0 {
		return m.MaxMessages
	}
	return defaultMaxMessages
}

func (m *Mailbox) lockTimeout() time.Duration {
	if m.LockTimeout > 0 {
		return m.LockTimeout
	}
	return defaultLockTimeout
}

// inboxPath returns the sanitized, base-confined inbox path for (team, agent)
// and verifies it cannot escape BaseDir (defense in depth on top of sanitize).
func (m *Mailbox) inboxPath(team, agent string) (string, error) {
	dir := filepath.Join(m.BaseDir, sanitizeName(team), "inboxes")
	path := filepath.Join(dir, sanitizeName(agent)+".json")
	// Confinement check: the resolved path must stay under BaseDir.
	rel, err := filepath.Rel(m.BaseDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("swarm: inbox path escapes base dir (team=%q agent=%q)", team, agent)
	}
	return path, nil
}

// Send appends msg to the recipient's inbox under the team, taking the inbox
// lock. It fails closed on oversize messages or a full inbox.
func (m *Mailbox) Send(team, recipient string, msg Message) error {
	path, err := m.inboxPath(team, recipient)
	if err != nil {
		return err
	}
	if msg.Type == "" {
		msg.Type = "message"
	}
	if msg.To == "" {
		msg.To = sanitizeName(recipient)
	}
	if msg.Time == "" {
		msg.Time = time.Now().UTC().Format(time.RFC3339)
	}
	encoded, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("swarm: encode message: %w", err)
	}
	if len(encoded) > m.maxMessageBytes() {
		return fmt.Errorf("%w: %d > %d bytes", ErrMessageTooLarge, len(encoded), m.maxMessageBytes())
	}
	if err := os.MkdirAll(filepath.Dir(path), inboxDirMode); err != nil {
		return fmt.Errorf("swarm: create inbox dir: %w", err)
	}
	release, err := acquireLock(path+".lock", m.lockTimeout())
	if err != nil {
		return err
	}
	defer release()

	messages, err := m.readLocked(path)
	if err != nil {
		return err
	}
	if len(messages) >= m.maxMessages() {
		return fmt.Errorf("%w: %d messages", ErrMailboxFull, len(messages))
	}
	messages = append(messages, msg)
	return atomicWriteJSON(path, messages)
}

// ReadAndConsume reads the recipient's inbox and marks every previously-unread
// message read under one lock, returning the messages with their read flags as
// they were BEFORE this read (so the caller can tell which were new). The inbox
// is empty (no error) if it does not yet exist; malformed inbox JSON is reported
// as an error (fail closed) and nothing is written.
func (m *Mailbox) ReadAndConsume(team, recipient string) ([]Message, error) {
	path, err := m.inboxPath(team, recipient)
	if err != nil {
		return nil, err
	}
	// The lock file lives beside the inbox; create the dir so it can be taken
	// even on first read of a not-yet-existing inbox.
	if err := os.MkdirAll(filepath.Dir(path), inboxDirMode); err != nil {
		return nil, fmt.Errorf("swarm: create inbox dir: %w", err)
	}
	release, err := acquireLock(path+".lock", m.lockTimeout())
	if err != nil {
		return nil, err
	}
	defer release()

	messages, err := m.readLocked(path)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return messages, nil
	}
	// Snapshot pre-consume state to return, then flip unread -> read on disk.
	snapshot := make([]Message, len(messages))
	copy(snapshot, messages)
	changed := false
	for i := range messages {
		if !messages[i].Read {
			messages[i].Read = true
			changed = true
		}
	}
	if changed {
		if err := atomicWriteJSON(path, messages); err != nil {
			return nil, err
		}
	}
	return snapshot, nil
}

// readLocked reads and decodes an inbox file. Callers that mutate must hold the
// lock; pure readers may call it directly (a torn read surfaces as a decode
// error and is retried by the caller, never silently treated as empty).
func (m *Mailbox) readLocked(path string) ([]Message, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("swarm: read inbox: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	// Reject an oversized inbox file outright rather than decoding untrusted,
	// unbounded JSON into memory.
	if maxFile := m.maxMessages() * m.maxMessageBytes() * 2; len(raw) > maxFile {
		return nil, fmt.Errorf("swarm: inbox file too large (%d bytes)", len(raw))
	}
	var messages []Message
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, fmt.Errorf("swarm: malformed inbox %s: %w", filepath.Base(path), err)
	}
	for i := range messages {
		if messages[i].Type == "" {
			messages[i].Type = "message"
		}
	}
	return messages, nil
}

// atomicWriteJSON writes data as pretty JSON to a sibling temp file (0600) then
// renames it over path, so a reader never observes a partial write.
func atomicWriteJSON(path string, data any) error {
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("swarm: encode inbox: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".inbox-*.tmp")
	if err != nil {
		return fmt.Errorf("swarm: create temp inbox: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := tmp.Chmod(inboxFileMode); err != nil {
		tmp.Close()
		return fmt.Errorf("swarm: chmod temp inbox: %w", err)
	}
	if _, err := tmp.Write(encoded); err != nil {
		tmp.Close()
		return fmt.Errorf("swarm: write temp inbox: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("swarm: close temp inbox: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("swarm: commit inbox: %w", err)
	}
	return nil
}

// acquireLock takes an exclusive lock by creating lockPath with O_EXCL. It
// retries with a short backoff until timeout, breaking a lock whose file is
// older than lockStaleAfter (so a crashed holder cannot deadlock the swarm).
func acquireLock(lockPath string, timeout time.Duration) (func(), error) {
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, inboxFileMode)
		if err == nil {
			// Record the holder for diagnostics; ignore write errors (the lock
			// itself is held by virtue of the exclusive create).
			fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
			f.Close()
			var released bool
			return func() {
				if released {
					return
				}
				released = true
				os.Remove(lockPath)
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("swarm: acquire lock: %w", err)
		}
		// Lock held: break it if stale, otherwise wait.
		if info, statErr := os.Stat(lockPath); statErr == nil {
			if time.Since(info.ModTime()) > lockStaleAfter {
				os.Remove(lockPath)
				continue
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("swarm: timed out acquiring lock %s", filepath.Base(lockPath))
		}
		time.Sleep(20 * time.Millisecond)
	}
}
