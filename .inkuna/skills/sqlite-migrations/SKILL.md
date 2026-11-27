---
name: sqlite-migrations
description: Changing Inkuna's SQLite schema, covering numbered forward-only migrations in internal/storage/migrations, the storage methods that use them, and what may never be stored.
---

# SQLite migrations

Inkuna keeps structured state in SQLite through `internal/storage`. Large content (traces, handoffs, diffs, outputs) belongs in the content-addressed artifact store (`internal/artifact`), not in a table.

## Migrations

- Migrations are `internal/storage/migrations/NNN_description.sql`, embedded with `go:embed`. `Store.Migrate` applies them in file-name order, once each, inside a transaction, and records the file name in `schema_migrations`.
- Add a new file with the next three-digit number. Never edit, rename, or renumber a migration that has been merged: the file name is its identity, so a renamed file runs again and an edited one never re-runs.
- Migrations are forward-only. Prefer additive changes (new tables, new nullable columns, new indexes). Dropping a column or table loses user history; do not do it without an explicit decision from the user.

## Storage code

- Add the methods that read and write the new schema to `internal/storage`, with tests in the package's `*_test.go` files that run against a migrated store.
- Domain types in `internal/domain` stay free of SQL. Storage maps between rows and domain types.
- Never store a secret value. Secrets live in the OS keychain or the environment; SQLite may hold a secret's declared name only.
