# Concurrent SQLite checkpoints

Source-only fix on `fix/sqlite-concurrent-state`: configure a bounded per-connection
busy timeout and serialize migrations before schema inspection. Independent
process regressions cover startup and completed-step writes. Preserve workflow
semantics; never retry external effects. Keep production on PostgreSQL.

## Resolution and verification — 2026-11-17

- Five-second default busy handler is applied to every connection; explicit
  URI timeout settings are honored. Writes remain serialized, without WAL or
  automatic replay of workflow effects.
- `BEGIN IMMEDIATE` serializes schema inspection and legacy ALTERs atomically.
- Coordinated subprocess tests cover migration and completed-step write lock
  contention, including a recycled connection. Both fail against original source.
- Five repeated contention runs passed; `make test`, `make lint`,
  `go test -race ./pkg/state`, and `make build` passed.
- Explicit 30 ms timeout test verifies a persistent lock still fails with
  SQLITE_BUSY. Production remains on its separate PostgreSQL engine database.

Source fixed locally, not released or deployed.
