package daemon

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func drain(t *testing.T, buffered []string, live <-chan string) []string {
	t.Helper()
	got := append([]string(nil), buffered...)
	timeout := time.After(2 * time.Second)
	for {
		select {
		case line, ok := <-live:
			if !ok {
				return got
			}
			got = append(got, line)
		case <-timeout:
			t.Fatal("timed out draining session stream")
		}
	}
}

func TestSessionStartRoutesAndStreams(t *testing.T) {
	launcher, _ := seqLauncher(&fakeWorker{pid: 1, out: []string{"e1", "e2", "e3"}, exitCode: 0})
	pool, _ := NewPool(PoolOptions{Size: 2, Launcher: launcher})
	mgr, err := NewSessionManager(SessionManagerOptions{Pool: pool})
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}
	sess, err := mgr.Start(context.Background(), WorkerSpec{Session: "s1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	buffered, live, cancel := sess.Subscribe()
	defer cancel()
	got := drain(t, buffered, live)
	if len(got) != 3 || got[0] != "e1" || got[2] != "e3" {
		t.Fatalf("streamed lines = %v, want [e1 e2 e3]", got)
	}
	<-sess.Done()
	if sess.State() != SessionDone {
		t.Fatalf("state = %s, want done", sess.State())
	}
}

func TestSessionDuplicateIDRejected(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	launcher, _ := seqLauncher(&fakeWorker{pid: 1, waitCh: block})
	pool, _ := NewPool(PoolOptions{Size: 2, Launcher: launcher})
	mgr, _ := NewSessionManager(SessionManagerOptions{Pool: pool})
	if _, err := mgr.Start(context.Background(), WorkerSpec{Session: "dup"}); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if _, err := mgr.Start(context.Background(), WorkerSpec{Session: "dup"}); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("second Start err = %v, want ErrSessionExists", err)
	}
}

func TestSessionLeaseQueuesWhenPoolFull(t *testing.T) {
	block := make(chan struct{})
	a := &fakeWorker{pid: 1, waitCh: block}
	b := &fakeWorker{pid: 2, out: []string{"b1"}, exitCode: 0}
	launcher, calls := seqLauncher(a, b)
	pool, _ := NewPool(PoolOptions{Size: 1, Launcher: launcher})
	mgr, _ := NewSessionManager(SessionManagerOptions{Pool: pool})

	sa, _ := mgr.Start(context.Background(), WorkerSpec{Session: "a"})
	waitFor(t, func() bool { return sa.State() == SessionRunning })

	sb, _ := mgr.Start(context.Background(), WorkerSpec{Session: "b"})
	// b must stay queued (its worker not yet launched) while a holds the slot.
	time.Sleep(50 * time.Millisecond)
	if sb.State() != SessionQueued {
		t.Fatalf("session b state = %s, want queued while pool full", sb.State())
	}
	if n := atomic.LoadInt32(calls); n != 1 {
		t.Fatalf("launcher calls = %d, want 1 (b not launched yet)", n)
	}

	close(block) // a finishes, slot frees, b proceeds
	<-sb.Done()
	if sb.State() != SessionDone {
		t.Fatalf("session b state = %s, want done", sb.State())
	}
	if n := atomic.LoadInt32(calls); n != 2 {
		t.Fatalf("launcher calls = %d, want 2 after slot freed", n)
	}
}

func TestSessionAttachAfterFinishSeesBuffer(t *testing.T) {
	launcher, _ := seqLauncher(&fakeWorker{pid: 1, out: []string{"x", "y"}, exitCode: 0})
	pool, _ := NewPool(PoolOptions{Size: 1, Launcher: launcher})
	mgr, _ := NewSessionManager(SessionManagerOptions{Pool: pool})
	sess, _ := mgr.Start(context.Background(), WorkerSpec{Session: "s"})
	<-sess.Done()

	buffered, live, cancel, err := mgr.Attach("s")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer cancel()
	got := drain(t, buffered, live)
	if len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("attach buffered = %v, want [x y]", got)
	}
	if _, _, _, err := mgr.Attach("missing"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Attach(missing) err = %v, want ErrSessionNotFound", err)
	}
}

func TestSessionStatuses(t *testing.T) {
	launcher, _ := seqLauncher(&fakeWorker{pid: 1, out: []string{"l"}, exitCode: 0})
	pool, _ := NewPool(PoolOptions{Size: 1, Launcher: launcher})
	mgr, _ := NewSessionManager(SessionManagerOptions{Pool: pool})
	sess, _ := mgr.Start(context.Background(), WorkerSpec{Session: "only"})
	<-sess.Done()
	st := mgr.Statuses()
	if len(st) != 1 || st[0].ID != "only" || st[0].State != string(SessionDone) || st[0].Lines != 1 {
		t.Fatalf("statuses = %+v, want one done session with 1 line", st)
	}
}
