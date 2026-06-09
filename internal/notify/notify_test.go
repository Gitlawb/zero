package notify

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestShouldEmit(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		event   Event
		focused bool
		want    bool
	}{
		{"off never", Config{Mode: ModeOff, FocusMode: FocusAlways}, Completion, false, false},
		{"empty mode never", Config{}, Completion, false, false},
		{"always when focused", Config{Mode: ModeBell, FocusMode: FocusAlways}, Completion, true, true},
		{"always when unfocused", Config{Mode: ModeBell, FocusMode: FocusAlways}, Completion, false, true},
		{"unfocused emits when unfocused", Config{Mode: ModeBell, FocusMode: FocusUnfocused}, Completion, false, true},
		{"unfocused silent when focused", Config{Mode: ModeBell, FocusMode: FocusUnfocused}, Completion, true, false},
		{"empty focusmode == unfocused", Config{Mode: ModeBell}, Completion, true, false},
		{"focused emits when focused", Config{Mode: ModeBell, FocusMode: FocusFocused}, AwaitingInput, true, true},
		{"focused silent when unfocused", Config{Mode: ModeBell, FocusMode: FocusFocused}, AwaitingInput, false, false},
		{"awaiting-input also eligible", Config{Mode: ModeBoth, FocusMode: FocusAlways}, AwaitingInput, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldEmit(tc.cfg, tc.event, tc.focused); got != tc.want {
				t.Fatalf("shouldEmit=%v want %v", got, tc.want)
			}
		})
	}
}

func TestSequence(t *testing.T) {
	if got := sequence(ModeBell, "hi"); got != "\x07" {
		t.Fatalf("bell seq=%q", got)
	}
	if got := sequence(ModeNotify, "hi"); got != "\x1b]9;hi\x07" {
		t.Fatalf("notify seq=%q", got)
	}
	if got := sequence(ModeBoth, "hi"); got != "\x07\x1b]9;hi\x07" {
		t.Fatalf("both seq=%q", got)
	}
	if got := sequence(ModeOff, "hi"); got != "" {
		t.Fatalf("off seq=%q", got)
	}
}

func TestSanitizeMessage(t *testing.T) {
	if got := sanitizeMessage("ok\x1b]0;evil\x07more\nx"); got != "ok]0;evilmorex" {
		t.Fatalf("sanitize=%q", got)
	}
	long := strings.Repeat("a", 500)
	if got := sanitizeMessage(long); len([]rune(got)) != maxMessageLen {
		t.Fatalf("clamp len=%d want %d", len([]rune(got)), maxMessageLen)
	}
}

func TestNotifyWritesAndRespectsPolicy(t *testing.T) {
	var buf bytes.Buffer
	n := New(&buf, Config{Mode: ModeBoth, FocusMode: FocusAlways})
	n.Notify(Completion, "Zero: ready")
	if buf.String() != "\x07\x1b]9;Zero: ready\x07" {
		t.Fatalf("emitted=%q", buf.String())
	}

	buf.Reset()
	off := New(&buf, Config{Mode: ModeOff})
	off.Notify(Completion, "x")
	if buf.Len() != 0 {
		t.Fatalf("off should emit nothing, got %q", buf.String())
	}

	buf.Reset()
	uf := New(&buf, Config{Mode: ModeBell, FocusMode: FocusUnfocused})
	uf.SetFocused(true)
	uf.Notify(Completion, "x")
	if buf.Len() != 0 {
		t.Fatalf("focused+unfocused-mode should be silent, got %q", buf.String())
	}
	uf.SetFocused(false)
	uf.Notify(Completion, "x")
	if buf.String() != "\x07" {
		t.Fatalf("unfocused should bell, got %q", buf.String())
	}
}

func TestNotifierDefaultFocusFalse(t *testing.T) {
	var buf bytes.Buffer
	n := New(&buf, Config{Mode: ModeBell, FocusMode: FocusUnfocused})
	n.Notify(Completion, "x")
	if buf.String() != "\x07" {
		t.Fatalf("default-focus headless should bell, got %q", buf.String())
	}
}

func TestNotifyRaceSafe(t *testing.T) {
	n := New(&bytes.Buffer{}, Config{Mode: ModeBell, FocusMode: FocusAlways})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); n.SetFocused(true) }()
		go func() { defer wg.Done(); n.Notify(Completion, "x") }()
	}
	wg.Wait()
}

func TestDefaultMessage(t *testing.T) {
	if DefaultMessage(Completion) != "Zero: ready" {
		t.Fatal("completion message")
	}
	if DefaultMessage(AwaitingInput) != "Zero: needs input" {
		t.Fatal("awaiting message")
	}
}

func TestEnabled(t *testing.T) {
	if Enabled(ModeOff) || Enabled("") {
		t.Fatal("off/empty should be disabled")
	}
	if !Enabled(ModeBell) || !Enabled(ModeNotify) || !Enabled(ModeBoth) {
		t.Fatal("bell/notify/both should be enabled")
	}
}
