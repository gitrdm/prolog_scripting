# vm.procedures occurrences

Generated: 2025-10-29

This file lists discovered occurrences of `vm.procedures` reads or writes. Many are correct and protected by `vm.mu` (RLock/Lock). Test files may access `vm.procedures` directly to set up fixtures; those are OK but mean the background compaction worker must remain disabled during `go test`.

Files with notable occurrences (file : approximate line):

- engine/vm.go : 104-170  (Register* implementations) — these are writes under `vm.mu.Lock()` (OK).
- engine/vm.go : Clone() — reads `vm.procedures` under vm.mu.RLock() (OK).
- engine/retract_bench_test.go : ~18 — test sets `vm.procedures` under `vm.mu.Lock()` (OK).
- engine/stress_test.go : lines ~41,86 — test reads `vm.procedures` directly (in test harness) — allowed.
- engine/builtin_concurrency_test.go : lines ~40,68,131,157 — tests read/write `vm.procedures` for fixtures (test-only).
- engine/vm_test.go : various lines reading `vm.procedures` after Register* to validate registration (fine; test-local)
- engine/text_test.go : lines ~412-429 — tests mutate `vm.procedures` and expect `proceduresEqual` (test-only)
- engine/builtin.go : many lines where `vm.procedures` is read/written — these are mostly within functions that acquire vm.mu appropriately (Arrive, assertMerge, CurrentPredicate, Abolish, etc.).

Actionable checking steps
-------------------------
1. For every non-test file that reads `vm.procedures`, confirm it does so while holding `vm.mu.RLock()`; if not, add RLock/Unlock or introduce an accessor function `vm.getProcedure(pi) (procedure, bool)` that RLocks internally.
2. For production writes, ensure `vm.mu.Lock()` is held. If multiple locks are needed, follow the lock-order `vm.mu -> vm.streams.mu -> s.mu`.
3. For test files that mutate `vm.procedures` directly (to set fixtures), leave them as-is but document in `THREAD-SAFETY.md` and keep the test-only behavior of disabling background worker when binary ends with `.test` (current behavior).

If you want, I can now auto-generate a focused patch that replaces any non-test direct read of `vm.procedures[...]` without `vm.mu` with `vm.getProcedure(pi)` accessors (small, mechanical change). Say "patch procedures accessors" and I'll implement it and run `go test -race ./...`.
