//go:build darwin

package sandbox

import (
	"strings"
	"testing"
)

func TestParseSandboxDenyLine(t *testing.T) {
	tag := sandboxDenialLogTag
	cases := []struct {
		name   string
		line   string
		wantOK bool
	}{
		{"denial with tag", `2026-06-13 Sandbox: bash(123) deny(1) file-write-create /etc/x ` + tag, true},
		{"denial without tag", `2026-06-13 Sandbox: bash(123) deny(1) file-write-create /etc/x`, false},
		{"not a sandbox line", `2026-06-13 some unrelated log ` + tag, false},
		{"noisy daemon", `2026-06-13 Sandbox: mDNSResponder(9) deny(1) network-outbound ` + tag, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, ok := parseSandboxDenyLine(tc.line, tag)
			if ok != tc.wantOK {
				t.Fatalf("parseSandboxDenyLine(%q) ok=%v, want %v (msg=%q)", tc.line, ok, tc.wantOK, msg)
			}
			if ok {
				if strings.TrimSpace(msg) == "" {
					t.Fatalf("a matched denial must yield a non-empty message")
				}
				if strings.Contains(msg, tag) {
					t.Fatalf("the tag must be stripped from the reported message: %q", msg)
				}
				if !strings.Contains(msg, "Sandbox:") {
					t.Fatalf("the message should start at the Sandbox: marker: %q", msg)
				}
			}
		})
	}
}

func TestDenialMonitorEmptyTagIsNoop(t *testing.T) {
	monitor := StartDenialMonitor(t.Context(), "")
	if got := monitor.Stop(); got != nil {
		t.Fatalf("a no-op monitor must return no blocks, got %#v", got)
	}
}
