# Thread-safety Report — thread-safety-refactor

Summary
-------
This short report summarizes the concrete changes made in branch `thread-safety-refactor` to make the codebase safer for concurrent use and documents verification steps performed during the change.

Key changes applied
-------------------
1. Stream locking and tests
   - Added per-Stream mutex `s.mu` and protected internal Stream operations where appropriate.
   - Added `streams` registry locking (`sync.RWMutex`) for `add`, `remove`, `lookup`.
   - Fixed a deadlock caused by a double-lock in `WriteByte`/`WriteRune` by moving locking into writer implementations.
   - Test added: `engine/stream_concurrency_test.go` (concurrent writers + closer).

2. VM-level protections and clone
   - Added `vm.mu sync.RWMutex` to protect `procedures`, `loaded`, `charConversions`, `operators`, and flag fields.
   - Added `VM.Clone()` to create per-request VMs that copy configuration but do not share mutable runtime state.
   - Tests added: `engine/vm_concurrency_test.go` covering shared-VM Arrive and clone-based concurrency.

3. Memory limit behavior
   - Replaced calls to `debug.SetMemoryLimit(-1)` with a package-local, atomic `memoryLimit` and `SetMemoryLimit(limit int64) int64` to avoid mutating global runtime state.

4. Added guidance comments
   - Added lock-ordering and concurrency guidance comments to `engine/vm.go` and `engine/stream.go`.

Verification performed
----------------------
- Unit tests: ran `go test ./...` locally; all packages passed.
- Race detection: ran `go test ./... -race`; no data races reported.
- Targeted tests: ran `TestStream_WriteByte`, `TestStream_ConcurrentWriteClose`, `TestVM_SharedConcurrentArrive`, and `TestVM_CloneConcurrent` to stress the recent changes.

Next recommended steps
----------------------
1. Expand concurrent test coverage:
   - Concurrent assert/retract/abolish scenarios against a shared VM.
   - Concurrent open/close and stream alias mutation while queries run.

2. Decide and document official support model:
   - If the project will support shared-VM mode, continue the Phase 2 roadmap to lock-protect all VM mutable structures and harden compile/install paths.
   - If per-request clones are the recommended mode, document best practices and provide a small interpreter pool helper.

3. CI integration:
   - Add `go test ./... -race` to CI for the `thread-safety-refactor` branch and require it for pull requests.

4. Performance & profiling:
   - If contention appears, profile under realistic loads and consider read-optimized snapshots, sharded maps, or a write-serialized worker for modifications.

Files changed (summary)
----------------------
- Modified:
  - `engine/stream.go` — per-stream lock guidance and earlier locking changes.
  - `engine/vm.go` — top-level locking guidance (comment) and vm.mu usages.
  - `engine/malloc.go` — replaced `debug.SetMemoryLimit` usage with `SetMemoryLimit` and package-local `memoryLimit`.
- Added:
  - `engine/stream_concurrency_test.go`
  - `engine/vm_concurrency_test.go`
  - `THREAD-SAFETY-REPORT.md` (this file)

How I validated
----------------
1. Ran the full test suite under Go 1.19 locally.
2. Ran the race detector on the full test suite.
3. Targeted concurrency stress tests executed and passed.

Contact & follow-ups
--------------------
If you'd like, I can now:
- Continue with Phase 2 (shared-VM sync) and apply `vm.mu` protections incrementally across all code paths that mutate VM-level maps/flags (I can do small batches and re-run tests between batches).
- Or, finalize documentation and add example code demonstrating the per-request-VM pattern and an interpreter pool helper.

Pick which path and I'll proceed automatically.
