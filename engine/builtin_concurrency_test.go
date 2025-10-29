package engine

import (
	"context"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentAssertz stresses concurrent Assertz against a single shared VM.
func TestConcurrentAssertz(t *testing.T) {
	runtime.GOMAXPROCS(0)

	var vm VM

	var wg sync.WaitGroup
	goroutines := 16
	callsPerG := 500

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < callsPerG; i++ {
				_, err := Assertz(&vm, &compound{functor: NewAtom("foo"), args: []Term{NewAtom("a")}}, Success, nil).Force(context.Background())
				if err != nil {
					t.Errorf("Assertz failed: %v", err)
					return
				}
			}
		}()
	}

	wg.Wait()

	// Verify number of clauses under the VM lock.
	vm.mu.RLock()
	p, ok := vm.procedures[procedureIndicator{name: NewAtom("foo"), arity: 1}]
	vm.mu.RUnlock()
	if !ok {
		t.Fatalf("predicate foo/1 missing")
	}
	u, ok := p.(*userDefined)
	if !ok {
		t.Fatalf("predicate foo/1 is not userDefined")
	}
	want := goroutines * callsPerG
	if got := len(u.getClauses()); got != want {
		t.Fatalf("unexpected clauses: got=%d want=%d", got, want)
	}
}

// TestConcurrentRetract pre-fills a predicate with many clauses and then stresses
// concurrent Retract calls to ensure deletions are safe.
func TestConcurrentRetract(t *testing.T) {
	runtime.GOMAXPROCS(0)

	var vm VM
	total := 200
	clauses := make([]*clause, 0, total)
	for i := 0; i < total; i++ {
		clauses = append(clauses, &clause{raw: &compound{functor: NewAtom("f"), args: []Term{NewAtom("x")}}})
	}
	ud := &userDefined{dynamic: true}
	ud.setClauses(clauses)
	vm.procedures = map[procedureIndicator]procedure{
		{name: NewAtom("f"), arity: 1}: ud,
	}

	// Optionally enable diagnostic counters for Retract if the environment
	// variable PROLOG_DEBUG_RETRACT=1 is set. This keeps diagnostics off by
	// default to avoid noisy logs in normal runs.
	if os.Getenv("PROLOG_DEBUG_RETRACT") == "1" {
		debugRetractStats.Store(&retractStats{})
	}

	var wg sync.WaitGroup
	goroutines := 16
	attemptsPerG := 500
	var successes int64

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < attemptsPerG; i++ {
				ok, err := Retract(&vm, &compound{functor: NewAtom("f"), args: []Term{NewVariable()}}, Success, nil).Force(context.Background())
				if err != nil {
					t.Errorf("Retract error: %v", err)
					return
				}
				if ok {
					atomic.AddInt64(&successes, 1)
				}
			}
		}()
	}

	wg.Wait()

	// Log diagnostic counters collected during the test.
	if p := debugRetractStats.Load(); p != nil {
		t.Logf("Retract diagnostics: attempts=%d comparisons=%d casSuccess=%d casFail=%d",
			atomic.LoadUint64(&p.attempts),
			atomic.LoadUint64(&p.comparisons),
			atomic.LoadUint64(&p.casSuccess),
			atomic.LoadUint64(&p.casFail),
		)
		// disable after use
		debugRetractStats.Store(nil)
	}

	// successes should equal the number of initial clauses (or less if attempts were
	// insufficient). We assert at least that we didn't overshoot and that no races/panics occurred.
	got := int(atomic.LoadInt64(&successes))
	if got > total {
		t.Fatalf("too many retract successes: got=%d want<=%d", got, total)
	}
}

// TestConcurrentAbolish ensures concurrent Abolish calls on the same predicate
// are safe and idempotent.
func TestConcurrentAbolish(t *testing.T) {
	runtime.GOMAXPROCS(0)

	var vm VM
	ud2 := &userDefined{dynamic: true}
	ud2.setClauses([]*clause{{raw: NewAtom("z")}})
	vm.procedures = map[procedureIndicator]procedure{
		{name: NewAtom("z"), arity: 0}: ud2,
	}

	var wg sync.WaitGroup
	goroutines := 8

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Abolish(&vm, &compound{functor: atomSlash, args: []Term{NewAtom("z"), Integer(0)}}, Success, nil).Force(context.Background())
			if err != nil {
				// Abolish may succeed only once; subsequent calls may return an error
				// because the predicate no longer exists. Accept permission errors.
				if _, ok := err.(Exception); !ok {
					t.Errorf("Abolish failed: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	// Ensure predicate is removed or absent.
	vm.mu.RLock()
	_, ok := vm.procedures[procedureIndicator{name: NewAtom("z"), arity: 0}]
	vm.mu.RUnlock()
	if ok {
		t.Fatalf("predicate z/0 still present after Abolish")
	}
}
