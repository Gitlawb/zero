// Test seams: helpers only test code uses, kept out of the production binary.
package openai

import (
	"errors"
	"strings"
)

// ValidateAccount is a convenience for tests/callers that want to confirm the
// account id is the right shape (non-empty, trimmed). It is a no-op helper
// rather than a constructor check so a Codex provider can be built before the
// first login completes.
func ValidateAccount(account string) error {
	if strings.TrimSpace(account) == "" {
		return errors.New("openai codex: account id is empty")
	}
	return nil
}
