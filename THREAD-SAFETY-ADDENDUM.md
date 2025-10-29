VM procedure accessor API and CI addendum

This short addendum documents the recent accessor API added to `*VM` and a
recommended minimal CI configuration to run tests and the race detector.

VM procedure accessors (implemented)

- `vm.LookupProcedure(pi procedureIndicator) (procedure, bool)` — read-locked lookup.
- `vm.InstallProcedure(pi procedureIndicator, p procedure)` — write-locked install (allocates map if needed).
- `vm.RemoveProcedure(pi procedureIndicator)` — write-locked delete.
- `vm.ProceduresCopy() map[procedureIndicator]procedure` — returns a shallow copy of the procedures map for safe iteration.

Guidance

- Prefer these accessors in production code unless the caller already holds `vm.mu`.
- Do not call `InstallProcedure`/`RemoveProcedure` while holding `vm.mu` (they lock internally).
- Tests that intentionally mutate internals may continue to use direct map mutation but should either hold `vm.mu` or run in deterministic modes (e.g., sync compaction).

CI recommendation

- Add a GitHub Actions workflow to run `go test ./...` and `go test -race ./...` on PRs and pushes to main. This repository already passes these locally; CI will help catch regressions.

Next actions

- I can proceed to automatically replace remaining production usages of `vm.procedures[...]` with these accessors (skipping test files). Say "auto-replace now" and I'll continue the mechanical replacements in small batches, running `go test -race ./...` between batches and reporting progress.
