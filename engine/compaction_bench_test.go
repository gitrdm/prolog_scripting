package engine

import (
	"context"
	"testing"
)

// BenchmarkForceCompaction repeatedly retracts clauses from a large predicate
// while synchronous compaction is enabled to measure the cost of compaction
// and help choose an optimal threshold. This benchmark forces synchronous
// compaction by toggling the package test helper.
func BenchmarkForceCompaction(b *testing.B) {
	// enable synchronous compaction for deterministic measurement
	old := syncCompactOnRetract
	setSyncCompactOnRetractForTest(true)
	defer setSyncCompactOnRetractForTest(old)

	vm := &VM{}
	// create a predicate with many clauses so compaction will be triggered
	makePredicate(vm, NewAtom("compaction_pred"), 20000)

	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// call Retract repeatedly; many calls will succeed and trigger compaction
		_, _ = Retract(vm, NewAtom("compaction_pred"), Success, NewEnv()).Force(ctx)
	}
}
