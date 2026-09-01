// Test seams: helpers only test code uses, kept out of the production binary.
package agenteval

import "context"

func (fn AgentRunnerFunc) Run(ctx context.Context, input AgentRunInput) AgentRunResult {
	return fn(ctx, input)
}
