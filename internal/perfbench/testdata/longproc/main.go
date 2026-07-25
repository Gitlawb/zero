// Package longproc is a small buildable workspace for the long-running-process
// benchmark tasks, which run build/test/vet/bench commands against it.
package longproc

import "fmt"

// Version is the demo build's version string.
const Version = "1.0.0"

// Process simulates a bounded unit of work for the benchmark task.
func Process(n int) int {
	total := 0
	for i := 0; i < n; i++ {
		total += i
	}
	return total
}

func main() {
	fmt.Println("longproc fixture", Version, Process(100))
}
