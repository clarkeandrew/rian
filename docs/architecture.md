# Rian Architecture

> Living document. Update this whenever an architectural change is made —
> a new package responsibility, a changed boundary/interface, a new database
> dialect, or a new command. Keep it accurate, not exhaustive.

## Overview

Rian is a single static Go binary that applies SQL migrations to a database and
records what it applied in a Flyway-compatible `flyway_schema_history` table.
It is designed to be a drop-in replacement for the open-source Flyway engine
against an existing Flyway-managed database, so its on-disk and in-database
formats must match Flyway exactly.

Execution is a simple serial pipeline — no async/concurrency:

```
discover migrations  ->  resolve config + placeholders  ->  compute checksums
        |                                                          |
        v                                                          v
   read schema history  ->  diff (pending/applied/failed)  ->  apply in order
        |                                                          |
        v                                                          v
   per-migration transaction (where supported)  ->  write history row
```

## Components (package responsibilities)

| Package | Responsibility |
|---|---|
| `cmd/rian` | CLI entrypoint; cobra root + subcommands; wires config → engine. |
| `internal/config` | Merge configuration from `flyway.conf` files, `FLYWAY_*` env vars, and CLI flags (precedence: flags > env > file). Exposes resolved settings + placeholders. |
| `internal/scan` | Discover migration files in configured locations; parse filenames into `(type, version, description)`; order versions with Flyway's numeric-segment ordering. |
| `internal/checksum` | Compute the **Flyway-exact CRC32** of a migration (see Invariants). The keystone of drop-in compatibility. |
| `internal/sql` | Split a migration into statements (handle function bodies, MySQL `DELIMITER`); apply `${placeholder}` substitution. |
| `internal/history` | Read/write `flyway_schema_history`; model rows; compute applied/pending/failed state and `installed_rank`. |
| `internal/db` | `Dialect` abstraction: connect, identifier quoting, transactional-DDL capability flag, schema-history DDL. |
| `internal/db/postgres` | pgx-backed dialect. Transactional DDL = true. |
| `internal/db/mysql` | go-sql-driver/mysql dialect. Transactional DDL = false (implicit commit on DDL). |
| `internal/engine` | Orchestrates `migrate`, `info`, `validate`, `baseline`, `repair` over scan + checksum + history + dialect. |

## The `Dialect` boundary

The only database-specific surface is the `Dialect` interface in `internal/db`.
Adding a new database = implementing this interface. It abstracts:

- connection establishment (DSN/URL parsing, TLS),
- identifier quoting,
- whether DDL is transactional (drives rollback strategy),
- creating and reading the schema-history table.

The `engine` and `history` packages depend only on `Dialect`, never on a
concrete driver.

## Key invariants

- **Checksum must match Flyway's `ChecksumCalculator` byte-for-byte.** CRC32
  computed line-by-line with line terminators stripped (line-ending
  independent), leading UTF-8 BOM removed, stored as a **signed 32-bit int**.
  Never hash raw file bytes. This is what makes Rian drop-in compatible; a
  regression here silently breaks `validate` against existing databases.
- **Schema-history format is fixed by Flyway.** Column names, order, types, and
  `installed_rank`/`success` semantics must match so Flyway and Rian can read
  each other's history.
- **Transaction strategy is dialect-driven.** Postgres runs each migration in a
  transaction and rolls back on failure. MySQL implicitly commits DDL and
  cannot roll back; on failure Rian marks the migration failed and requires
  `repair` (matching Flyway). The engine must not assume rollback is available.
- **No cgo.** The build is `CGO_ENABLED=0`; only pure-Go drivers are used so the
  binary stays static and cross-compilable. CI asserts this.

## Out of scope (current)

Undo migrations, Java/script/callback migrations, callbacks, `clean` (optional
and disabled by default), and databases beyond PostgreSQL and MySQL.

## Decision log

- **Go + pure-Go drivers (pgx, go-sql-driver/mysql):** chosen for the small
  static single-binary distribution goal. See
  `docs/plans/2026-06-25-initial-project-outline-and-tech-stack.md`.
- **MIT license:** permissive, compatible with all chosen drivers.
