package longproc

import "testing"

// benchSink keeps Process(100) live across the benchmark loop so the compiler
// can't optimize the call away and report an unrealistically fast benchmark.
var benchSink int

func TestProcess(t *testing.T) {
	if got := Process(100); got != 4950 {
		t.Fatalf("Process(100) = %d, want 4950", got)
	}
}

func BenchmarkProcess(b *testing.B) {
	var sink int
	for i := 0; i < b.N; i++ {
		sink = Process(100)
	}
	benchSink = sink
}
