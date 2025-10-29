# Package-level var audit

Generated: 2025-10-29

This file lists package-level `var` declarations discovered during the audit and a short recommendation for each.

- engine/builtin.go
  - `var debugRetractStats *retractStats` (line ~29)
    - Recommendation: already converted to `atomic.Pointer[retractStats]` — keep atomic and access via Load/Store.
  - `var syncCompactOnRetract bool` (line ~36)
    - Recommendation: converted to `uint32` + atomic ops. Keep atomic.
  - `var operatorSpecifiers = map[Atom]operatorSpecifier{...}` (line ~604)
    - Recommendation: initialized at program start and not mutated at runtime. If tests mutate it, guard with `vm.mu` or make test-local copies.
  - `var openFile = os.OpenFile` (line ~1479)
    - Recommendation: test-injection hook. Document that tests must not mutate concurrently; consider guarding with package-level mutex or provide a test helper that sets/restores under a mutex.
  - `var osExit = func(code int) { ... }` (line ~2209)
    - Recommendation: similar to `openFile` — test hook; guard or document.

- engine/malloc.go
  - `var memoryLimit int64` (line ~16)
  - `var memFree = func() int64 { ... }` (line ~24)
    - Recommendation: `SetMemoryLimit` uses atomics; `memFree` reads atomically. These are OK. Avoid global runtime setters.

- engine/stream.go
  - several `var (...)` groups for error constants (errWrongIOMode, errWrongStreamType, etc.)
    - Recommendation: immutable error vars are fine.

- engine/variable.go
  - `var varCounter int64` (line ~9)
    - Recommendation: `NewVariable()` uses atomic increment — OK.

- engine/number.go, engine/term.go, engine/promise.go, solutions.go, interpreter.go, etc.
  - several package-level immutable constants and error vars (OK to keep as-is).

General recommendations
---------------------
- Prefer atomic or mutex-guarded access for any package-level var that tests or production code may mutate at runtime.
- Mark as `const` or move to initialization-time-only variables anything that does not need to change at runtime.
- For test-injection hooks (like `openFile`/`osExit`), provide a `tests.SetOpenFile(...)` helper that uses a package-level mutex to set/restore the hook safely.

Next actions
------------
1. Convert any remaining mutable package-level vars used by tests to atomic types or guard them with a `testHooksMu sync.Mutex` and helper functions. (low risk)
2. Run a targeted pass to ensure no production code mutates these globals without synchronization. (medium risk)
