// Package fix holds deliberately buggy functions for the bug-fix benchmark
// tasks. Each function is covered by its own test (see bugs_test.go) and each
// task verifies with `go test -run <TestName>`, so the tasks are independent:
// the initial package compiles and the targeted test fails until the named bug
// is fixed. Every bug is fixable without changing a function signature, so the
// test files compile in both the buggy and the fixed state.
package fix

import "fmt"

// Sum returns the sum of all elements. BUG: off-by-one — skips the last element.
func Sum(xs []int) int {
	total := 0
	for i := 0; i < len(xs)-1; i++ {
		total += xs[i]
	}
	return total
}

// DefaultConfig returns the default configuration. BUG: returns nil, which
// Port() then dereferences.
func DefaultConfig() *Config {
	return nil
}

// Config holds demo settings.
type Config struct {
	Port int
}

// Port returns the configured port. BUG: panics when DefaultConfig() is nil.
func Port() int {
	return DefaultConfig().Port
}

// Max returns the larger of a and b. BUG: uses the wrong inequality and returns
// the smaller value.
func Max(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ReadFirst returns the first byte of the given file's content as a string. BUG:
// returns only a single character instead of the full content.
func ReadFirst(content string) string {
	if len(content) == 0 {
		return ""
	}
	return string(content[0])
}

// Divide returns a divided by b. BUG: the operands are swapped.
func Divide(a, b int) int {
	return b / a
}

// Classify returns "low", "mid", or "high". BUG: the "high" branch is missing a
// return, so high values fall through to the default "".
func Classify(n int) string {
	if n < 10 {
		return "low"
	}
	if n < 100 {
		return "mid"
	}
	// BUG: missing return for the high case
	return ""
}

// Label returns a display label for the given name. BUG: uses the wrong prefix
// ("admin-" instead of "user-"), producing the wrong text.
func Label(name string) string {
	return fmt.Sprintf("admin-%s", name)
}

// counter is a shared, unsynchronized counter. BUG: Inc is not goroutine-safe.
var counter int

// Inc increments the shared counter.
func Inc() {
	counter++
}

// Count returns the current counter value.
func Count() int {
	return counter
}
