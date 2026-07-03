package tui

import (
	"strings"
	"testing"
)

func TestHandleOnlyBashCommand(t *testing.T) {
	t.Run("on restricts the tool surface to bash + skill with tool_search disabled", func(t *testing.T) {
		got, out := model{}.handleOnlyBashCommand("on")
		if !got.onlyBashActive {
			t.Fatal("onlyBashActive should be true after /onlybash on")
		}
		if want := []string{"bash", "skill"}; !stringSlicesEqual(got.agentOptions.EnabledTools, want) {
			t.Fatalf("EnabledTools = %v, want %v", got.agentOptions.EnabledTools, want)
		}
		if want := []string{"tool_search"}; !stringSlicesEqual(got.agentOptions.DisabledTools, want) {
			t.Fatalf("DisabledTools = %v, want %v", got.agentOptions.DisabledTools, want)
		}
		if !strings.Contains(out, "onlybash: on") {
			t.Fatalf("status output should report onlybash: on, got %q", out)
		}
	})

	t.Run("off with no prior filters restores an unrestricted tool surface", func(t *testing.T) {
		m := model{}
		m, _ = m.handleOnlyBashCommand("on")
		got, out := m.handleOnlyBashCommand("off")
		if got.onlyBashActive {
			t.Fatal("onlyBashActive should be false after /onlybash off")
		}
		if len(got.agentOptions.EnabledTools) != 0 {
			t.Fatalf("EnabledTools = %v, want empty (restored to pre-onlybash state)", got.agentOptions.EnabledTools)
		}
		if len(got.agentOptions.DisabledTools) != 0 {
			t.Fatalf("DisabledTools = %v, want empty (restored to pre-onlybash state)", got.agentOptions.DisabledTools)
		}
		if !strings.Contains(out, "onlybash: off") {
			t.Fatalf("status output should report onlybash: off, got %q", out)
		}
	})

	t.Run("off restores operator-configured filters that predate onlybash", func(t *testing.T) {
		m := model{}
		m.agentOptions.EnabledTools = []string{"read_file", "grep"}
		m.agentOptions.DisabledTools = []string{"bash"}

		m, _ = m.handleOnlyBashCommand("on")
		if want := []string{"bash", "skill"}; !stringSlicesEqual(m.agentOptions.EnabledTools, want) {
			t.Fatalf("EnabledTools while on = %v, want %v", m.agentOptions.EnabledTools, want)
		}

		got, _ := m.handleOnlyBashCommand("off")
		if want := []string{"read_file", "grep"}; !stringSlicesEqual(got.agentOptions.EnabledTools, want) {
			t.Fatalf("EnabledTools after off = %v, want restored operator filter %v", got.agentOptions.EnabledTools, want)
		}
		if want := []string{"bash"}; !stringSlicesEqual(got.agentOptions.DisabledTools, want) {
			t.Fatalf("DisabledTools after off = %v, want restored operator filter %v", got.agentOptions.DisabledTools, want)
		}
	})

	t.Run("off while never turned on does not clobber a pre-existing operator filter", func(t *testing.T) {
		m := model{}
		m.agentOptions.EnabledTools = []string{"read_file", "grep"}
		m.agentOptions.DisabledTools = []string{"bash"}

		got, out := m.handleOnlyBashCommand("off")
		if got.onlyBashActive {
			t.Fatal("onlyBashActive should stay false")
		}
		if want := []string{"read_file", "grep"}; !stringSlicesEqual(got.agentOptions.EnabledTools, want) {
			t.Fatalf("EnabledTools = %v, want unchanged operator filter %v (onlybash was never on, so off must be a no-op)", got.agentOptions.EnabledTools, want)
		}
		if want := []string{"bash"}; !stringSlicesEqual(got.agentOptions.DisabledTools, want) {
			t.Fatalf("DisabledTools = %v, want unchanged operator filter %v", got.agentOptions.DisabledTools, want)
		}
		if !strings.Contains(out, "onlybash: off") {
			t.Fatalf("status output should still report onlybash: off, got %q", out)
		}
	})

	t.Run("a redundant on while already active does not re-stash onlybash's own filter", func(t *testing.T) {
		m := model{}
		m.agentOptions.EnabledTools = []string{"read_file"}
		m.agentOptions.DisabledTools = []string{"write_file"}

		m, _ = m.handleOnlyBashCommand("on")
		// Redundant "on": must be a no-op on the stash, not re-capture onlybash's
		// own [bash,skill]/[tool_search] as if it were the operator's filter.
		m, _ = m.handleOnlyBashCommand("on")

		got, _ := m.handleOnlyBashCommand("off")
		if want := []string{"read_file"}; !stringSlicesEqual(got.agentOptions.EnabledTools, want) {
			t.Fatalf("EnabledTools after off = %v, want original operator filter %v (stash must not have been clobbered by the redundant /onlybash on)", got.agentOptions.EnabledTools, want)
		}
		if want := []string{"write_file"}; !stringSlicesEqual(got.agentOptions.DisabledTools, want) {
			t.Fatalf("DisabledTools after off = %v, want original operator filter %v", got.agentOptions.DisabledTools, want)
		}
	})

	t.Run("bare form toggles", func(t *testing.T) {
		m := model{}
		m, _ = m.handleOnlyBashCommand("")
		if !m.onlyBashActive {
			t.Fatal("bare /onlybash should turn onlybash on from off")
		}
		m, _ = m.handleOnlyBashCommand("")
		if m.onlyBashActive {
			t.Fatal("bare /onlybash should turn onlybash off from on")
		}
	})

	t.Run("status reports current state without mutating it", func(t *testing.T) {
		m := model{}
		m, _ = m.handleOnlyBashCommand("on")
		got, out := m.handleOnlyBashCommand("status")
		if !got.onlyBashActive {
			t.Fatal("status must not change onlyBashActive")
		}
		if !strings.Contains(out, "onlybash: on") {
			t.Fatalf("status output should report onlybash: on, got %q", out)
		}
	})

	t.Run("unknown argument shows usage without changing state", func(t *testing.T) {
		m := model{}
		got, out := m.handleOnlyBashCommand("bogus")
		if got.onlyBashActive {
			t.Fatal("unknown argument must not enable onlybash")
		}
		if !strings.Contains(out, "Usage") {
			t.Fatalf("unknown argument should show usage, got %q", out)
		}
	})
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
