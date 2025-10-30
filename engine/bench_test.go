package engine

import (
	"sync"
	"testing"
)

// BenchmarkRetractStress reproduces the concurrent assert/retract workload used
// by the stress tests but as a benchmark so we can gather profiles and
// measure allocations/CPU.
func BenchmarkRetractStress(b *testing.B) {
	// Keep the benchmark deterministic and small-ish while still exercising
	// the hot path.
	const (
		live = 512
		iters = 200
	)
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		vm := &VM{}
		env := &Env{}

		// Pre-populate some clauses for a predicate p/1
		pi := procedureIndicator{name: NewAtom("p"), arity: 1}
		u := &userDefined{public: true, dynamic: true}
		// Build initial clauses
		var cs clauses
		for i := 0; i < live; i++ {
			c := clause{}
			// simple head p(i)
			h := NewAtom("p").Apply(NewAtom("x"))
			c.compileHead(h, env)
			c.bytecode = append(c.bytecode, instruction{opcode: opExit})
			cs = append(cs, &c)
		}
		u.setClauses(cs)
		vm.InstallProcedure(pi, u)

		// Run concurrent asserts and retracts
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				Assertz(vm, NewAtom("p").Apply(NewAtom("a")), Success, env)
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				Retract(vm, NewAtom("p").Apply(NewAtom("a")), Success, env)
			}
		}()
		wg.Wait()
	}
}
