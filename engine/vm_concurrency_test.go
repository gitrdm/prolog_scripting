package engine

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// TestVM_SharedConcurrentArrive runs many concurrent Arrive calls against a
// single shared VM to ensure lookups and predicate calls are safe.
func TestVM_SharedConcurrentArrive(t *testing.T) {
	runtime.GOMAXPROCS(0)

	var successes int64

	var vm VM
	// ensure Unknown is non-nil so Arrive doesn't mutate it concurrently
	vm.Unknown = func(Atom, []Term, *Env) {}
	vm.procedures = map[procedureIndicator]procedure{
		{name: NewAtom("foo"), arity: 1}: Predicate1(func(vm *VM, _ Term, k Cont, env *Env) *Promise {
			// do a tiny bit of work then succeed
			atomic.AddInt64(&successes, 1)
			return k(env)
		}),
	}

	var wg sync.WaitGroup
	goroutines := 16
	callsPerG := 1000

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < callsPerG; i++ {
				p := vm.Arrive(NewAtom("foo"), []Term{NewAtom("a")}, Success, nil)
				ok, err := p.Force(context.Background())
				if err != nil || !ok {
					t.Errorf("Arrive failed: ok=%v err=%v", ok, err)
					return
				}
			}
		}()
	}

	wg.Wait()

	want := int64(goroutines * callsPerG)
	if got := atomic.LoadInt64(&successes); got != want {
		t.Fatalf("unexpected successes: got=%d want=%d", got, want)
	}
}

// TestVM_CloneConcurrent verifies that creating many per-request clones and
// using them concurrently is safe.
func TestVM_CloneConcurrent(t *testing.T) {
	var base VM
	var calls int64
	base.procedures = map[procedureIndicator]procedure{
		{name: NewAtom("bar"), arity: 0}: Predicate0(func(vm *VM, k Cont, env *Env) *Promise {
			atomic.AddInt64(&calls, 1)
			return k(env)
		}),
	}

	var wg sync.WaitGroup
	goroutines := 32
	callsPerG := 200

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// each goroutine makes its own clone and calls into it repeatedly
			local := base.Clone()
			for i := 0; i < callsPerG; i++ {
				p := local.Arrive(NewAtom("bar"), nil, Success, nil)
				ok, err := p.Force(context.Background())
				if err != nil || !ok {
					t.Errorf("clone Arrive failed: ok=%v err=%v", ok, err)
					return
				}
			}
		}()
	}

	wg.Wait()

	want := int64(goroutines * callsPerG)
	if got := atomic.LoadInt64(&calls); got != want {
		t.Fatalf("unexpected calls: got=%d want=%d", got, want)
	}
}
