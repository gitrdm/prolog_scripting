package engine

import (
	"sync"
	"testing"
)

// TestRetractNoPanicConcurrent is a focused stress/regression test that
// reproduces the concurrent assert/retract workload in a deterministic,
// test-friendly way. It ensures the common hot path doesn't panic and
// runs to completion (this previously exposed closure-capture regressions).
func TestRetractNoPanicConcurrent(t *testing.T) {
	const (
		live  = 64
		iters = 200
	)

	vm := &VM{}
	env := &Env{}

	// Create a dynamic predicate p/1 pre-populated with some live clauses.
	pi := procedureIndicator{name: NewAtom("p"), arity: 1}
	u := &userDefined{public: true, dynamic: true}
	var cs clauses
	for i := 0; i < live; i++ {
		c := clause{}
		h := NewAtom("p").Apply(NewAtom("x"))
		c.compileHead(h, env)
		c.bytecode = append(c.bytecode, instruction{opcode: opExit})
		cs = append(cs, &c)
	}
	u.setClauses(cs)
	vm.InstallProcedure(pi, u)

	var wg sync.WaitGroup
	wg.Add(2)

	// Many concurrent asserts of the exact same atom.
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			Assertz(vm, NewAtom("p").Apply(NewAtom("a")), Success, env)
		}
	}()

	// Many concurrent retracts of that same atom.
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			Retract(vm, NewAtom("p").Apply(NewAtom("a")), Success, env)
		}
	}()

	wg.Wait()

	// Basic post-condition: ensure the procedure entry still exists and is
	// accessible; no semantic assertion here other than: we didn't crash and
	// the predicate data is still reachable.
	if p, ok := vm.LookupProcedure(pi); !ok || p == nil {
		t.Fatalf("procedure p/1 missing after concurrent assert/retract")
	}
}
