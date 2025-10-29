# Thread-safety and parallel execution plan (checkpointable)

This file lists concurrency observations, concrete issues, and a step-by-step checklist for refactoring the codebase to support safe parallel usage. It's intentionally written as short checklist items so an LLM or reviewer can continue from any point.

---

## Quick summary

- Current repo branch: `thread-safety-refactor` (created from `main`).
- Baseline test results (run on branch creation): `go test ./...` passed for package tests (see commit/test run output).

## Goals

- Make it safe to run multiple queries concurrently while either:
  - Using a single shared `*engine.VM` (concurrency enabled), or
  - Using per-request `*engine.VM` instances (documented, cheap creation), or both.

- Avoid global runtime state changes (e.g. avoiding frequent calls to `debug.SetMemoryLimit`) and keep any global changes well-documented and synchronized.

## Observable concurrency hotspots (concrete code locations)

- `engine/vm.go`
  - Fields: `procedures map[procedureIndicator]procedure`, `loaded map[string]struct{}`, `charConversions map[rune]rune`, `streams streams`, `input, output *Stream`, `operators` and toggles are mutable and accessed without synchronization.
  - Methods: `Register*` mutate `procedures`. `Arrive` reads `procedures`. `SetUserInput`/`SetUserOutput` update `streams` and `input`/`output`.

- `engine/stream.go`
  - `streams` type contains slice and map (`elems []*Stream`, `aliases map[Atom]*Stream`) and methods `add`, `remove`, `lookup` mutate/inspect without locks.
  - `Stream` methods mutate internal fields: `position`, `buf`, `lastRuneSize`, `endOfStream`, etc. `bufio.Reader` is not concurrency-safe.

- `engine/malloc.go`
  - `memFree()` calls `debug.SetMemoryLimit(-1)` on each invocation which interacts with global runtime state. This is unsafe to call concurrently and mutates process-level settings.

- `engine/promise.go`
  - `Promise` objects are stateful and mutated during `Force`; they are per-evaluation and not safe for concurrent reuse across goroutines.

- `engine/env.go`
  - `rootEnv`, `rootContext`, `varContext` are package-level values. The Env implementation is persistent/functional (creates new trees on bind), so Env instances are mostly safe; `rootEnv` is read-only after initialization.

- `engine/atom.go` and `engine/variable.go`
  - `atomTable` uses `sync.RWMutex` — concurrency-safe. `NewVariable()` uses `atomic.AddInt64` — concurrency-safe.

## Design choices (pick one or both)

Option A (Recommended, minimal invasive):
- Keep `VM` not concurrently safe for query execution; require callers to create a new `Interpreter` / `VM` per request. Document this clearly.
- Provide example helpers or a small pool to cheaply obtain pre-initialized `Interpreter` instances.
- Pros: minimal code changes; low risk.
- Cons: may allocate more frequently.

Option B (Shared VM, thread-safe):
- Add synchronization primitives to allow safely sharing a single `*engine.VM` across goroutines.
- Use fine-grained locking to avoid locking hot inner loops of the interpreter.
- Pros: efficient for many lightweight queries; central resource management.
- Cons: more invasive; must avoid introducing contention or deadlocks.

## Concrete checklist (survivable by LLM context window shifts)

Each item is formatted: [status] short description — file(s) — notes

Core work:

- [ ] Decide primary model: per-request VMs OR shared VM with locks — (repo) — pick and document.

If choosing shared VM (Option B), implement the following in order:

- [ ] Add `mu sync.RWMutex` to `engine.VM` — (`engine/vm.go`) — protect `procedures`, `loaded`, `charConversions`, `operators`, and `doubleQuotes` maps and toggles.
  - Read operations (e.g., lookup in `Arrive`) use `RLock`; write operations (Register*, modifications) use `Lock`.

- [ ] Protect `streams` structure — (`engine/stream.go`) — add `mu sync.RWMutex` to `streams` and use it in `add`, `remove`, and `lookup`.

- [ ] Make `Stream` methods safe (or document non-shareability) — (`engine/stream.go`) — add `mu sync.Mutex` to `Stream` and lock in methods mutating `position`, `buf`, `endOfStream`, and other fields. Ensure `bufReader` usage is only via this lock.

- [ ] Ensure `SetUserInput` / `SetUserOutput` use `VM.mu` and `vm.streams` locking consistently.

- [ ] Keep `exec` (hot inner loop) free of any global locks — it should use only per-query state (locals, `Env`, `Promise`). If `exec` needs to read immutable VM state (e.g., operator table), snapshot or read under `RLock` before the loop.

- [ ] Decide approach for `memFree()` / memory limit detection — (`engine/malloc.go`) — remove repeated calls to `debug.SetMemoryLimit(-1)`; instead either:
  - [ ] Keep an atomic-configured max memory limit and only read `runtime.ReadMemStats`; or
  - [ ] Synchronize calls to `debug.SetMemoryLimit` and limit changing this globally.

- [ ] Add tests that exercise concurrent access to VM: multiple goroutines calling `Interpreter.Query` concurrently against the same `*Interpreter` and verifying no data races (`go test -race`).

- [ ] Run race detector in CI and locally: `go test -race ./...`.

If choosing per-request VM (Option A):

- [ ] Document that `*Interpreter` is not safe for concurrent use in `README.md` and `THREAD-SAFETY.md`.

- [ ] Add helper `NewPooledInterpreter(poolSize int)` or an example of fast copy/seed using a stored `base Interpreter` (e.g., pre-register predicates/operators and then clone/copy lightweight state to a new VM) — (`prolog` package).

- [ ] Add tests that create many interpreters concurrently and run queries to show correctness and measure cost.

Low-level correctness items (both options):

- [ ] Mark `Promise` as per-query and avoid sharing across goroutines. If sharing needed, redesign `Promise` to be concurrency-safe.

- [ ] Ensure `streams` alias map updates and lookups are atomic and safe (adds/removes while queries are running should be safe).

- [ ] Avoid pointer identity misassumptions across goroutines (some Compare methods use pointer addresses for ordering). Document if necessary.

Verification & testing checklist:

- [ ] Unit tests for `streams` add/remove/lookup under concurrent operations.
- [ ] Unit tests for `Register*` while queries run concurrently (shared VM case).
- [ ] End-to-end test: real query that opens/closes streams concurrently.
- [ ] Run `go test -race ./...` and ensure no data race reports.

## Short-term, low-risk patch to apply first

- Add a mutex to `streams` and protect `add/remove/lookup` — (`engine/stream.go`) — this is small and covers a common concurrency hazard for I/O.

If you'd like, I can implement that small change now as the first code patch, run the tests, and iterate.

## Notes and rationale for specific items

- `debug.SetMemoryLimit(-1)` is a global runtime API and was being called in `memFree()`; calling or toggling process-level memory limits at runtime is error-prone and can race with other callers. Prefer an independently managed memory limit value for this package or a once-only initialization.

- `Env` implementation uses a persistent Red-Black tree style; this is good for concurrency as `bind` and `insert` return new `*Env` objects rather than mutating the shared tree in place. `rootEnv` is used as a base read-only sentinel.

- Atom interning already uses `sync.RWMutex` and `NewVariable()` uses atomics — these are safe and need no change.

## How to continue (next steps pick-one)

- [ ] I will implement the small `streams` mutex change and run the test-suite (fast, low-risk). — (recommended next action)
- [ ] Or: if you prefer the documentation-first route, I will mark `THREAD-SAFETY.md` as the design proposal and we can review it before coding.

## Test helpers and semantic comparators (test guidance)

After the `clauses` storage was refactored to an atomic-swap model (`clausesHolder` + `getClauses`/`setClauses` on `userDefined`), some tests that compared expected `*userDefined` values directly started failing intermittently. Those failures were not functional bugs but false negatives caused by comparing internal atomic pointer addresses and other implementation details that can change across allocations.

To make tests robust and implementation-agnostic, the test-suite now includes small helpers:

- `udWith(u *userDefined, cs clauses) *userDefined` (in `engine/text_test.go`) — constructs a `*userDefined` and publishes clauses using `u.setClauses(...)` so expected values don't capture atomic internals.
- `udWithClauses(u *userDefined, cs []*clause) *userDefined` (in `engine/test_helpers_test.go`) — same purpose, centralized for reuse.
- `proceduresEqual(exp, act map[procedureIndicator]procedure) bool` and `procedureEqual(exp, act procedure) bool` (in `engine/test_helpers_test.go`) — semantic comparators that compare the public fields and clause contents (pi, raw, bytecode, vars) while intentionally ignoring atomic internals (atomic.Pointer addresses, tombstone counters, etc.).

Guidelines for contributors:

- Do not write tests that use `assert.Equal(t, expected, vm.procedures)` or `assert.Equal(t, expectedUD, vm.procedures[pi])` when `expected` or `expectedUD` was constructed as a struct literal containing `clauses:` or direct `u.clauses` fields. Instead, use `proceduresEqual` / `procedureEqual` or construct expected values via `udWith` / `udWithClauses` so the expectations reflect semantic behavior only.
- Keep these helpers centralized in `engine/test_helpers_test.go` to avoid duplication and accidental pointer-sensitive comparisons.
- If a test needs to assert low-level atomic behavior (for example testing compaction internals), make that explicit and isolate it in a targeted test; prefer deterministic modes (e.g., `PROLOG_SYNC_COMPACT_ON_RETRACT`) so the test can observe and assert internal state deterministically.

This approach makes tests resilient to future internal changes (different allocation patterns, atomic implementation tweaks) while preserving full semantic verification of interpreter behavior.

---

Created on: 2025-10-29
Branch: thread-safety-refactor
Baseline tests: `go test ./...` passed for package tests.

## Threading model (proposal)

Contract (small):

- Inputs: a `*VM` configured with predicates, operators and char conversions; queries are performed by calling `Arrive`/`Interpreter.Query` which take per-query `Env` and `Promise` objects.
- Outputs: `Promise` results per query. Callers receive `bool,error` or streaming solutions via channels depending on API.
- Success criteria: running N concurrent queries should not cause data races, deadlocks, or corruption of `VM`'s configuration. Per-request clones should be independent for mutable runtime state (streams, loaded files, input/output).
- Error modes: concurrent attempts to mutate shared VM configuration (e.g., Register, assert/retract) may fail (return error) or be serialized; document behavior.

Locking rules (guideline):

- Use a global high-level lock ordering to avoid inversion: `vm.mu` -> `vm.streams.mu` -> `s.mu` (per-stream). Always acquire parent locks before child locks. Prefer not to hold `vm.mu` during long-running operations such as compilation; instead, snapshot or create new entries then acquire `vm.mu` briefly to install.
- `exec` (the interpreter hot loop) must not acquire `vm.mu`; snapshot necessary read-only VM tables (operators, procedures) before executing.
- Use `sync.RWMutex` on `VM` for read-mostly access: readers use `RLock` for Arrive/procedure lookup; writers (Register, assert/retract, SetPrologFlag) use `Lock`.

Edge cases to watch:

- Calls that add/remove streams while other goroutines are reading them — ensure rename/remove is atomic and lookups either see old or new state consistently.
- `Close` must remove the stream from `vm.streams` before closing the underlying `io.Closer` to avoid races with other threads trying to access the stream.
- Avoid holding `vm.mu` and then performing I/O while holding it; release `vm.mu` before I/O where possible.

## Minimal refactor roadmap (concrete steps)

These steps are ordered to reduce risk and allow incremental verification.

Phase 0 — Prepare and test harness

- Add short concurrency-focused tests (we added `engine/stream_concurrency_test.go` and `engine/vm_concurrency_test.go`). Ensure they run under `-race` in CI. (DONE)
- Add a `THREAD-SAFETY.md` checklist (this file). (DONE)

Phase 1 — Low-risk protective changes

1. Protect `streams` registry: add `mu sync.RWMutex` and guard `add/remove/lookup`. Add tests that concurrently add/lookup/remove streams. (LOW RISK) (DONE partially earlier)

2. Make `Stream` methods consistent: add `mu sync.Mutex` to `Stream` and ensure only writers acquire the lock; avoid nested double-locking. Add tests for concurrent write+close which we already added. (LOW RISK) (DONE)

3. Replace `debug.SetMemoryLimit` usage with a package-local limit setter and `memFree` using `runtime.ReadMemStats`. Expose `SetMemoryLimit` for embedding/tests. (DONE)

Phase 2 — VM sync for shared-VM mode (opt-in)

4. Add `vm.mu sync.RWMutex`. Protect `vm.procedures`, `vm.loaded`, `vm.charConversions`, `vm.operators`, and flag fields. Ensure `Arrive` uses `RLock` for lookup. (MEDIUM RISK) (MOST CHANGED)

5. Ensure `SetUserInput/Output` and open/close helpers use `vm.mu` + `vm.streams.mu` in the correct order. Avoid holding `vm.mu` while doing I/O. (MEDIUM RISK)

6. For operations that compile code (consult/compile/assert), avoid holding `vm.mu` during compilation; prepare the clauses, then `Lock` briefly to install. (MEDIUM/HIGH RISK)

Phase 3 — Tests, hardening and docs

7. Add integration tests that run many concurrent queries against: (a) a shared `*VM` and (b) per-request clones via `VM.Clone()`. Ensure both pass under `-race`. (DONE for basic cases; expand coverage.)

8. Document `VM.Clone()` semantics (what is copied, what is not). Provide example code for both modes in `examples/` (e.g., `examples/sandboxing` or `examples/embed_prolog_into_go`).

9. Add CI job or make `go test ./... -race` required for branch PRs. (OPS)

Phase 4 — Optional performance improvements

10. If contention is observed, profile and optimize: consider lock striping, sharded procedure maps, RCU-like snapshots for read-heavy tables, or a worker pool that serializes writes. (HIGH RISK, POST-TEST)

## Quick checklist to land this RFC

- [x] Add concurrency tests for streams (done)
- [x] Add concurrency tests for VM shared/clone (done)
- [x] Replace global memory-limit calls with package-local setter (done)
- [ ] Finalize lock ordering document and add as comment at top of `vm.go` and `stream.go`
- [ ] Expand tests to cover assert/retract/abolish concurrent scenarios
- [ ] Add example usage for per-request clones
- [ ] Add CI `-race` gating

## Closing notes

I implemented the low-risk changes and concurrency tests already in this branch. The next best step is to either finalize the refactor (if you want the shared-VM mode) or document the per-request pattern as the supported approach and add the pool helper. I can do either next — tell me which and I'll proceed.

## Stress test: standalone worker (background compaction)

To exercise the background compaction worker (which is intentionally disabled when running inside the `go test` binary), a small standalone stress worker binary has been added at `cmd/stress_worker`. Run this binary (or `go run`) to exercise concurrent Assertz/Retract workloads while the background compaction worker is active.

How to run locally:

```bash
# build the worker
go build ./cmd/stress_worker

# run with defaults
./stress_worker

# run with smaller / larger settings
./stress_worker -goroutines 8 -iterations 500 -initial 256

# or via go run
go run ./cmd/stress_worker -goroutines 16 -iterations 2000 -initial 512
```

Notes:
- Running the standalone worker starts the background compaction worker because the binary name does not end with `.test`. This forces compaction tasks to be processed by a background goroutine, which is the production behavior the unit tests avoid for determinism.
- The worker uses only exported APIs (Assertz/Retract). It intentionally does not inspect unexported internals; use `go test -race ./engine` and the in-repo `engine/stress_test.go` to run a test harness under the race detector.
- Use the standalone worker when you want to stress background compaction and observe background activity on your local machine. On CI you may prefer deterministic `go test -race ./...` runs which exercise the code paths without starting the background worker.

 
## Audit results (automatic scan) — 2025-10-29

I ran a repo-wide scan for package-level `var` declarations and direct `vm.` field accesses. Below are the high-confidence hotspots I found, with a short recommendation for each. This is intended as a prioritized audit so we can pick low-risk fixes first.

- Package-level mutable globals (review / protect / document):
  - `engine/builtin.go`
    - `var debugRetractStats *retractStats` — converted to `atomic.Pointer[retractStats]` in this branch. Recommendation: keep as atomic pointer (done).
    - `var syncCompactOnRetract bool` — converted to atomic `uint32` in this branch. Recommendation: keep atomic (done).
    - `var operatorSpecifiers = map[Atom]operatorSpecifier{...}` — constant initializer; OK if not mutated at runtime. If tests mutate it, protect with `vm.mu` or make copies.
    - `var openFile = os.OpenFile`, `var osExit = func(code int) { ... }` — test-injection hooks. Document that tests must set these only when single-threaded or guard them with a package-level mutex if concurrent mutation is needed.

  - `engine/malloc.go`
    - `var memoryLimit int64`, `var memFree = func() int64 { ... }` — `SetMemoryLimit` uses `atomic.SwapInt64`, `memFree` loads atomically. This is safe; avoid calling global runtime setters. (OK)

  - `engine/variable.go`
    - `var varCounter int64` — uses `atomic.AddInt64` for `NewVariable()` (OK).

  - Other package-level `var` occurrences (mostly immutable maps/constants or test-only):
    - `engine/number.go` constants map, `engine/term.go` defaultWriteOptions, `engine/promise.go` dummyCutParent, `solutions.go` ErrClosed, etc. Recommendation: mark immutable ones as const/var but do not mutate at runtime.

- VM field access hotspots (places to inspect and ensure proper locking):
  - `engine/vm.go`: `procedures`, `loaded`, `charConversions`, `operators`, `streams`, `input`, `output` — these are the primary mutable VM fields. Most production code already uses `vm.mu` (RLock/Lock) — good. Verify any direct reads/writes without `vm.mu` are test-only or guarded.
  - Tests and benches that set/inspect `vm.procedures` directly:
    - `engine/builtin_concurrency_test.go` and `engine/text_test.go` create or mutate `vm.procedures` directly for test fixtures. That's fine for in-package tests but means the background compaction worker must remain disabled for `go test` (we already do this).
    - `engine/retract_bench_test.go` and some bench tests explicitly lock `vm.mu` when mutating `procedures` — these are correct.

- Streams and I/O:
  - `engine/stream.go` contains the `streams` registry and a per-`Stream` struct. I added `streams.snapshot()` and there are per-stream `s.mu` locks. Recommendation: keep `streams.mu` as `sync.RWMutex` and always snapshot for iteration; ensure `Close()` removes from registry before acquiring `s.mu` (this pattern is already present).

- Compaction worker lifecycle:
  - `engine/builtin.go` holds `compactionQueue`, `compactionMu`, `compactionStopped`. These are protected by `compactionMu` for start/stop/enqueue operations. This is acceptable, but please review shutdown paths in long-running embedding scenarios; consider an atomic flag + close semantics for graceful shutdown.

- Remaining quick wins I recommend we land next:
  1. Convert any remaining test-only package-level mutation helpers to use atomic or a test-only mutex (low-risk).
 2. Produce an explicit list of files/tests which mutate `vm.procedures` without acquiring `vm.mu` (I found mostly tests). If any production code does this, wrap with `vm.mu.RLock()`/`Unlock()` or an accessor API.
 3. Add a CI job that runs `go test ./...` and a separate `go test -race ./...` job (budgeted). This will catch regressions early.

## Audit summary

- Current status: the branch has made significant progress (atomic clause publication, per-clause tombstones, streams snapshot, atomicized debug toggles). Unit tests and `go test -race ./...` pass locally.
- Remaining work: finish a repo-wide manual review of the package-level `var` list and any non-test direct accesses to `vm` fields; eliminate or guard them as appropriate.

If you want I'll now produce a precise file/line report for all package-level `var` declarations and all `vm.procedures` occurrences (including context lines) so we can triage them into small PRs. Say "full report" and I'll generate those files and update the todo list accordingly.

