package swarm

import (
	"errors"
	"testing"
)

func TestRegistryBuiltins(t *testing.T) {
	r := NewRegistry()
	for _, want := range []string{"teammate", "subagent"} {
		def, err := r.Lookup(want)
		if err != nil {
			t.Fatalf("builtin %q missing: %v", want, err)
		}
		if def.Model != modelInherit {
			t.Fatalf("builtin %q model = %q, want inherit", want, def.Model)
		}
		if def.SystemPrompt == nil {
			t.Fatalf("builtin %q must have a SystemPrompt", want)
		}
		if got := def.SystemPrompt(PromptContext{Team: "t", Task: "do x"}); got == "" {
			t.Fatalf("builtin %q SystemPrompt returned empty", want)
		}
	}
}

func TestRegistryLookupUnknown(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Lookup("nope"); !errors.Is(err, ErrUnknownAgentType) {
		t.Fatalf("Lookup(nope) err = %v, want ErrUnknownAgentType", err)
	}
}

func TestRegistryRegisterOverrideAndDefaultModel(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Definition{AgentType: "  custom  "}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	def, err := r.Lookup("custom")
	if err != nil {
		t.Fatalf("Lookup(custom): %v", err)
	}
	if def.AgentType != "custom" {
		t.Fatalf("agentType not trimmed: %q", def.AgentType)
	}
	if def.Model != modelInherit {
		t.Fatalf("default model = %q, want inherit", def.Model)
	}
	// Override a builtin.
	if err := r.Register(Definition{AgentType: "teammate", Model: "fixed-model"}); err != nil {
		t.Fatalf("override Register: %v", err)
	}
	if got, _ := r.Lookup("teammate"); got.Model != "fixed-model" {
		t.Fatalf("override model = %q, want fixed-model", got.Model)
	}
}

func TestRegistryRegisterRejectsEmpty(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Definition{AgentType: "   "}); err == nil {
		t.Fatal("Register with blank agentType must error")
	}
}

func TestRegistryAgentTypesSorted(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(Definition{AgentType: "alpha"})
	types := r.AgentTypes()
	for i := 1; i < len(types); i++ {
		if types[i-1] > types[i] {
			t.Fatalf("AgentTypes not sorted: %v", types)
		}
	}
}
