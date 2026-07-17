// Package nav is a small read-only fixture for navigation tasks.
package nav

import "fmt"

// MaxRetries is the configured retry limit for the demo client.
const MaxRetries = 3

// Config holds the demo client's settings.
type Config struct {
	Port int
	Name string
}

// greet returns a greeting for the given name.
// receieves is a deliberate typo present in the fixture (see edit tasks).
func greet(name string) string {
	return fmt.Sprintf("hello, %s", name)
}

// main is the fixture entry point.
func main() {
	fmt.Println(greet("world"))
}

// TODO: replace the demo greet with the real client call before shipping.
// (Present so nav-05's find-the-markers task has a non-zero, inspection-
// required answer; an agent that always emits "count: 0" fails.)
