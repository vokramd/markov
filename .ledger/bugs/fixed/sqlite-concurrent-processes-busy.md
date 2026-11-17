# Concurrent Markov CLI processes fail with SQLITE_BUSY

Observed 2026-11-17 in the production operations VM, released engine v0.2.0.
markovd's shell runner inherits one working directory, so scheduled consumers
and jobs they dispatch share /var/lib/markovd/markov-state.db by default.

Consumer markov-run-e9b8304b1cb5dcfa31e62c57 successfully launched discovery
markov-run-0d732a44, but then failed to persist dispatch_job:

```
persisting completed step "dispatch_job": saving step: database is locked (5) (SQLITE_BUSY)
```

The child completed. A later simultaneous hourly/consumer launch exposed the
existing markovd failure-propagation issue (a CLI can exit before callbacks
and leave the run shown as running).

Local pkg/state/sqlite.go NewSQLiteStore limits connections to one per
process but does not configure busy_timeout or WAL. That does not serialize
independent CLI processes. Need a focused multi-process regression test and
review of connection-level pragmas, bounded busy waits/retries and migration
startup concurrency. Do not change checkpoint semantics or retry arbitrary
workflow effects. This is a state-persistence bug, not an instruction to rerun
already dispatched jobs.

Production mitigation under evaluation: existing PostgreSQL state backend in
a dedicated markov_engine database, separate from markovd's API database.
Keep SQLite data/history intact; no release binary changed or published.

## Confirmed startup failure and mitigation evidence

Narrowly filtered markovd logs at 2026-11-17 01:00:00 UTC confirm:

```
Error: migrating schema: database is locked (5) (SQLITE_BUSY)
process exited with error: exit status 1
```

API run markov-run-32e4af7bbc18e63b7506f17c had no steps and stayed running;
it was cancelled through the API before maintenance. Separate PostgreSQL
checkpoint database was initialized using the unchanged released engine.
Three simultaneous API runs (consumer + two discovery) completed, including
both consumer child dispatch steps; all seven resulting engine runs were
completed in the new database. Its dump restored successfully into a fresh
validation database. This is an operational mitigation only; this core bug
remains open for a real SQLite concurrency regression/fix and future release.

## Source resolution — 2026-11-17

Fixed on `fix/sqlite-concurrent-state`: per-connection default busy_timeout=5000
(with explicit URI overrides) and an immediate transaction encompassing schema
inspection, creation and legacy ALTERs. No external operation retries and no
journal-mode change. Lock timeout exhaustion still returns an error.

Verification: independent-process migration and completed-step checkpoint
regressions fail against original source and pass with the fix (five repeats).
Connection replacement and bounded explicit timeout are covered. Full `make
test`, `make lint`, race-checked state tests and build passed. See
[tasks/done/sqlite-concurrent-state.md](../../tasks/done/sqlite-concurrent-state.md).
This is a local source fix, not a release or deployment; production PostgreSQL
mitigation remains in place.
