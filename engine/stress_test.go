package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// TestStressConcurrentAssertRetract runs a noisy concurrent workload of
// Assertz/Retract on the same predicate from many goroutines while a
// monitor goroutine snapshots the clause slice and checks simple
// invariants. Running this under `-race` helps detect data races caused by
// unsafe access to clause storage or compaction.
func TestStressConcurrentAssertRetract(t *testing.T) {
	// keep the test reasonably fast but noisy enough to exercise concurrency
	const (
		goroutines     = 32
		iterations     = 2000
		initialClauses = 256
	)

	vm := &VM{}
	name := NewAtom("stress")
	pi := makePredicate(vm, name, initialClauses)

	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(goroutines)

	// stop signal for monitor
	var done int32

	// monitor goroutine: periodically snapshot clauses and validate simple invariants
	var monWg sync.WaitGroup
	monWg.Add(1)
	go func() {
		defer monWg.Done()
		for atomic.LoadInt32(&done) == 0 {
			vm.mu.RLock()
			p, ok := vm.procedures[pi]
			if ok {
				if u, ok := p.(*userDefined); ok {
					// snapshot and scan without holding u.mu to simulate real readers
					snap := u.getClauses()
					live := 0
					for _, c := range snap {
						if atomic.LoadUint32(&c.deleted) == 0 {
							live++
						}
					}
					// invariants: live <= len(snap) and non-negative
					if live < 0 || live > len(snap) {
						t.Fatalf("invariant violated: live=%d len(snapshot)=%d", live, len(snap))
					}
				}
			}
			vm.mu.RUnlock()
		}
	}()

	// worker goroutines performing assert/retract
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// alternate assert and retract
				if (j+id)%2 == 0 {
					_, _ = Assertz(vm, name, Success, NewEnv()).Force(ctx)
				} else {
					_, _ = Retract(vm, name, Success, NewEnv()).Force(ctx)
				}
			}
		}(i)
	}

	// wait for workers
	wg.Wait()
	// signal monitor to stop and wait for it
	atomic.StoreInt32(&done, 1)
	monWg.Wait()

	// final consistency check
	vm.mu.RLock()
	p, ok := vm.procedures[pi]
	if !ok {
		vm.mu.RUnlock()
		t.Fatalf("procedure missing after stress run")
	}
	u, ok := p.(*userDefined)
	if !ok {
		vm.mu.RUnlock()
		t.Fatalf("procedure not userDefined")
	}
	snap := u.getClauses()
	live := 0
	deleted := 0
	for _, c := range snap {
		if atomic.LoadUint32(&c.deleted) == 0 {
			live++
		} else {
			deleted++
		}
	}
	vm.mu.RUnlock()

	// basic expectations: counts non-negative and total equals len(snapshot)
	if live < 0 || deleted < 0 || live+deleted != len(snap) {
		t.Fatalf("final counts inconsistent: live=%d deleted=%d len=%d", live, deleted, len(snap))
	}

	t.Logf("stress finished: live=%d deleted=%d total=%d", live, deleted, len(snap))
}
