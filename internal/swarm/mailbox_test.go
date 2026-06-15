package swarm

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func newTestMailbox(t *testing.T) *Mailbox {
	t.Helper()
	mb, err := NewMailbox(t.TempDir())
	if err != nil {
		t.Fatalf("NewMailbox: %v", err)
	}
	return mb
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"":           "default",
		"   ":        "default",
		"team-1_ok":  "team-1_ok",
		"../../etc":  "------etc",
		"a/b\\c":     "a-b-c",
		"team name!": "team-name-",
		"!!!":        "---",
		"plain":      "plain",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMailboxSendAndConsume(t *testing.T) {
	mb := newTestMailbox(t)
	if err := mb.Send("team", "bob", Message{From: "alice", Body: "hello"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := mb.Send("team", "bob", Message{From: "alice", Body: "again"}); err != nil {
		t.Fatalf("Send 2: %v", err)
	}
	msgs, err := mb.ReadAndConsume("team", "bob")
	if err != nil {
		t.Fatalf("ReadAndConsume: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	// First read returns them as unread (pre-consume snapshot), with defaults set.
	if msgs[0].Read || msgs[0].Type != "message" || msgs[0].To != "bob" || msgs[0].Time == "" {
		t.Fatalf("message defaults wrong: %+v", msgs[0])
	}
	// Second read shows them already consumed (read).
	msgs2, err := mb.ReadAndConsume("team", "bob")
	if err != nil {
		t.Fatalf("ReadAndConsume 2: %v", err)
	}
	for _, m := range msgs2 {
		if !m.Read {
			t.Fatalf("message should be read after consume: %+v", m)
		}
	}
}

func TestMailboxReadMissingIsEmpty(t *testing.T) {
	mb := newTestMailbox(t)
	msgs, err := mb.ReadAndConsume("team", "nobody")
	if err != nil {
		t.Fatalf("ReadAndConsume missing: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("missing inbox should be empty, got %d", len(msgs))
	}
}

func TestMailboxOversizeMessageRejected(t *testing.T) {
	mb := newTestMailbox(t)
	mb.MaxMessageBytes = 64
	err := mb.Send("team", "bob", Message{From: "a", Body: strings.Repeat("x", 1000)})
	if !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("oversize Send err = %v, want ErrMessageTooLarge", err)
	}
	// Nothing should have been written.
	msgs, _ := mb.ReadAndConsume("team", "bob")
	if len(msgs) != 0 {
		t.Fatalf("oversize Send must not persist, got %d messages", len(msgs))
	}
}

func TestMailboxFullRejected(t *testing.T) {
	mb := newTestMailbox(t)
	mb.MaxMessages = 2
	for i := 0; i < 2; i++ {
		if err := mb.Send("team", "bob", Message{From: "a", Body: "ok"}); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}
	if err := mb.Send("team", "bob", Message{From: "a", Body: "overflow"}); !errors.Is(err, ErrMailboxFull) {
		t.Fatalf("full Send err = %v, want ErrMailboxFull", err)
	}
}

func TestMailboxMalformedFailsClosed(t *testing.T) {
	mb := newTestMailbox(t)
	// Write a valid message so the inbox path exists, then corrupt the file.
	if err := mb.Send("team", "bob", Message{From: "a", Body: "ok"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	path, err := mb.inboxPath("team", "bob")
	if err != nil {
		t.Fatalf("inboxPath: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt inbox: %v", err)
	}
	if _, err := mb.ReadAndConsume("team", "bob"); err == nil {
		t.Fatal("malformed inbox must fail closed (error), not be treated as empty")
	}
	// A subsequent Send also fails closed rather than clobbering unknown data.
	if err := mb.Send("team", "bob", Message{From: "a", Body: "x"}); err == nil {
		t.Fatal("Send into a malformed inbox must fail closed")
	}
}

func TestMailboxPathConfinement(t *testing.T) {
	mb := newTestMailbox(t)
	// A traversal-style name is sanitized into a single safe segment; the inbox
	// stays under BaseDir.
	path, err := mb.inboxPath("../../evil", "../escape")
	if err != nil {
		t.Fatalf("inboxPath: %v", err)
	}
	rel, err := filepath.Rel(mb.BaseDir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("inbox path escaped base dir: base=%q path=%q rel=%q", mb.BaseDir, path, rel)
	}
}

func TestMailboxFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file modes")
	}
	mb := newTestMailbox(t)
	if err := mb.Send("team", "bob", Message{From: "a", Body: "ok"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	path, _ := mb.inboxPath("team", "bob")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("inbox file mode = %o, want 600", perm)
	}
}

func TestMailboxConcurrentSends(t *testing.T) {
	mb := newTestMailbox(t)
	const n = 30
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = mb.Send("team", "bob", Message{From: "a", Body: "concurrent"})
		}()
	}
	wg.Wait()
	msgs, err := mb.ReadAndConsume("team", "bob")
	if err != nil {
		t.Fatalf("ReadAndConsume: %v", err)
	}
	if len(msgs) != n {
		t.Fatalf("concurrent sends lost messages: got %d, want %d", len(msgs), n)
	}
}
