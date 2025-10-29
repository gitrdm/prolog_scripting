package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ichiban/prolog/engine"
)

func main() {
	var goroutines int
	var iterations int
	var initial int
	flag.IntVar(&goroutines, "goroutines", 32, "number of worker goroutines")
	flag.IntVar(&iterations, "iterations", 20000, "iterations per goroutine")
	flag.IntVar(&initial, "initial", 1024, "initial number of clauses")
	flag.Parse()

	vm := &engine.VM{}
	name := engine.NewAtom("stress")

	// populate initial clauses via public API (Assertz)
	ctx := context.Background()
	for i := 0; i < initial; i++ {
		_, _ = engine.Assertz(vm, name, engine.Success, engine.NewEnv()).Force(ctx)
	}

	var wg sync.WaitGroup
	wg.Add(goroutines)

	start := time.Now()
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if (j+id)%2 == 0 {
					_, _ = engine.Assertz(vm, name, engine.Success, engine.NewEnv()).Force(ctx)
				} else {
					_, _ = engine.Retract(vm, name, engine.Success, engine.NewEnv()).Force(ctx)
				}
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	fmt.Fprintf(os.Stderr, "done: elapsed=%v goroutines=%d iterations=%d\n", elapsed, goroutines, iterations)
}
