// Test seams: helpers only test code uses, kept out of the production binary.
package review

func HasBlockingChecks(checks []Check) bool {
	for _, check := range checks {
		if IsBlocking(check.Outcome) {
			return true
		}
	}
	return false
}
