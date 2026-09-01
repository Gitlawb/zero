// Package refactor is the starting workspace for the multi-file refactor
// benchmark tasks. It is intentionally a little messy (duplicated logic, a
// bare map type, a thin wrapper) so the refactor prompts have real work to do.
// It builds cleanly at all times; the tasks keep it building.
package refactor

import "fmt"

// Config holds demo settings.
type Config struct {
	Name string
	Port int
}

// GreetFromConfig returns a greeting using the config's name.
func GreetFromConfig(c Config) string {
	return fmt.Sprintf("hello, %s", c.Name)
}

// GreetByName returns a greeting using a bare name. BUG-note: this duplicates
// GreetFromConfig's formatting — refactor-01 extracts a shared helper.
func GreetByName(name string) string {
	return fmt.Sprintf("hello, %s", name)
}

// Wrapper is a thin wrapper around GreetByName — refactor-05 inlines it.
func Wrapper(name string) string {
	return GreetByName(name)
}

// greetWrapped is Wrapper's single caller — refactor-05 inlines Wrapper here
// and removes the Wrapper function.
func greetWrapped(name string) string {
	return Wrapper(name)
}

// stats is a bare map used across the file — refactor-04 introduces a named type.
var stats = map[string]int{}

// Record bumps a stat by name.
func Record(name string) {
	stats[name]++
}

// Lookup returns a stat by name.
func Lookup(name string) int {
	return stats[name]
}

// load failed wrapped error — refactor-06 consolidates these.
func firstError() error {
	return fmt.Errorf("load failed: missing input")
}

func secondError() error {
	return fmt.Errorf("load failed: bad format")
}

func buildError(ctx string) error {
	return fmt.Errorf("load failed: %s", ctx)
}
