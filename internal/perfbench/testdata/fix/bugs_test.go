package fix

import (
	"sync"
	"testing"
)

func TestSumIncludesLastElement(t *testing.T) {
	if got := Sum([]int{1, 2, 3, 4}); got != 10 {
		t.Fatalf("Sum = %d, want 10", got)
	}
}

func TestPortReturnsDefault(t *testing.T) {
	if got := Port(); got != 8080 {
		t.Fatalf("Port() = %d, want 8080 (DefaultConfig returned nil)", got)
	}
}

func TestMaxReturnsLarger(t *testing.T) {
	if got := Max(3, 7); got != 7 {
		t.Fatalf("Max(3,7) = %d, want 7", got)
	}
}

func TestReadFirstReturnsFullContent(t *testing.T) {
	if got := ReadFirst("hello"); got != "hello" {
		t.Fatalf("ReadFirst = %q, want %q", got, "hello")
	}
}

func TestDivideOrder(t *testing.T) {
	if got := Divide(10, 2); got != 5 {
		t.Fatalf("Divide(10,2) = %d, want 5", got)
	}
}

func TestClassifyHigh(t *testing.T) {
	if got := Classify(500); got != "high" {
		t.Fatalf("Classify(500) = %q, want %q", got, "high")
	}
}

func TestLabelUsesString(t *testing.T) {
	if got := Label("alice"); got != "user-alice" {
		t.Fatalf("Label = %q, want %q", got, "user-alice")
	}
}

// TestCounterRace exercises Inc concurrently. Run with `go test -race`:
// the unsynchronized counter trips the race detector until a mutex is added.
func TestCounterRace(t *testing.T) {
	counter = 0
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				Inc()
			}
		}()
	}
	wg.Wait()
	if got := Count(); got != 20000 {
		t.Fatalf("Count = %d, want 20000", got)
	}
}
