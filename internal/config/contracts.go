package config

const ContractOwnerRuntime = "runtime"

type ContractGap struct {
	Field     string
	Owner     string
	Milestone string
	Reason    string
}

func DefaultContractGaps() []ContractGap {
	return []ContractGap{
		{
			Field:     "env.OPENAI_API_KEY",
			Owner:     ContractOwnerRuntime,
			Milestone: "M0",
			Reason:    "OpenAI-compatible provider startup needs a stable environment key contract.",
		},
		{
			Field:     "env.ZERO_PROVIDER",
			Owner:     ContractOwnerRuntime,
			Milestone: "M0",
			Reason:    "Provider selection needs an explicit environment override before provider resolution is wired.",
		},
		{
			Field:     "providers[].timeout",
			Owner:     ContractOwnerRuntime,
			Milestone: "M1",
			Reason:    "Provider adapters need bounded request and stream waits.",
		},
		{
			Field:     "providers[].retryPolicy",
			Owner:     ContractOwnerRuntime,
			Milestone: "M1",
			Reason:    "Provider runtime needs consistent retry behavior across adapters.",
		},
		{
			Field:     "providers[].maxOutputTokens",
			Owner:     ContractOwnerRuntime,
			Milestone: "M0",
			Reason:    "The provider contract must bound generated output for chat and tool loops.",
		},
		{
			Field:     "providers[].reasoningEffort",
			Owner:     ContractOwnerRuntime,
			Milestone: "M1",
			Reason:    "Reasoning-capable models need a normalized config surface.",
		},
		{
			Field:     "permissions.mode",
			Owner:     ContractOwnerRuntime,
			Milestone: "M0",
			Reason:    "Runtime permission behavior must be explicit before tools execute mutating actions.",
		},
		{
			Field:     "sandbox.policy",
			Owner:     ContractOwnerRuntime,
			Milestone: "M0",
			Reason:    "Sandbox policy needs a shared backend contract before platform adapters land.",
		},
	}
}

func FindContractGapsByMilestone(gaps []ContractGap, milestone string) []ContractGap {
	var matches []ContractGap
	for _, gap := range gaps {
		if gap.Milestone == milestone {
			matches = append(matches, gap)
		}
	}
	return matches
}
