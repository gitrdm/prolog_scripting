package engine

import (
	"context"
	"fmt"
	"testing"
)

// helper to populate a predicate with n simple clauses (raw = Atom)
func makePredicate(vm *VM, name Atom, n int) procedureIndicator {
	pi := procedureIndicator{name: name, arity: 0}
	clauses := make([]*clause, 0, n)
	for i := 0; i < n; i++ {
		clauses = append(clauses, &clause{raw: name})
	}
	u := &userDefined{dynamic: true, public: true}
	u.setClauses(clauses)
	vm.mu.Lock()
	if vm.procedures == nil {
		vm.procedures = map[procedureIndicator]procedure{}
	}
	vm.procedures[pi] = u
	vm.mu.Unlock()
	return pi
}

func BenchmarkRetractConcurrent(b *testing.B) {
	cases := []int{16, 64, 256, 1024}
	ctx := context.Background()
	for _, n := range cases {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			vm := &VM{}
			makePredicate(vm, NewAtom("p"), n)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					Retract(vm, NewAtom("p"), Success, NewEnv()).Force(ctx)
				}
			})
		})
	}
}

// Simple concurrency test exercising concurrent assert/retract to detect races
func TestConcurrentAssertRetract(t *testing.T) {
	vm := &VM{}
	name := NewAtom("q")
	// start with some clauses
	makePredicate(vm, name, 128)

	ctx := context.Background()

	done := make(chan struct{})

	// spawn several goroutines that concurrently assert and retract
	for i := 0; i < 8; i++ {
		go func(id int) {
			for j := 0; j < 1000; j++ {
				// alternate assert and retract
				if j%2 == 0 {
					_, _ = Assertz(vm, name, Success, NewEnv()).Force(ctx)
				} else {
					_, _ = Retract(vm, name, Success, NewEnv()).Force(ctx)
				}
			}
			done <- struct{}{}
		}(i)
	}

	// wait for goroutines
	for i := 0; i < 8; i++ {
		<-done
	}
}
