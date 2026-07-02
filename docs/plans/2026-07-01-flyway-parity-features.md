# Flyway-parity features: target, toggles, repair realignment, locking

## Context

Rian covers the core Flyway commands but misses several settings and behaviors
that are everyday Flyway usage. Each item below is implemented as its own
commit; all follow Flyway's config keys and defaults so existing setups keep
working unchanged.

## Chosen features

- **`target`** (`flyway.target` / `FLYWAY_TARGET` / `-target`): migrate only up
  to the given version; `info` marks newer migrations `Above Target`. Empty or
  `latest` means no limit. Our own e2e workflow drives Flyway with `-target=2`,
  so Rian should understand the same setting.
- **`outOfOrder`** (default `false`): opt-in to applying a pending version
  below the latest applied one, instead of the (Flyway-default) refusal.
- **`validateOnMigrate`** (default `true`): allow disabling the pre-migrate
  validation, matching Flyway's toggle.
- **`installedBy`**: override the `installed_by` history value (falls back to
  the connection user, as before).
- **`repair` realigns checksums**: in addition to removing failed rows, update
  the stored checksum of applied versioned migrations to match the local file
  (Flyway's repair does this; it is the standard fix after an applied migration
  is edited). Repeatables are intentionally untouched — their checksum change
  is what triggers re-application.
- **Migration lock**: serialize `migrate`/`baseline`/`repair` across processes
  the way Flyway does — `pg_advisory_lock` on Postgres, `GET_LOCK` on MySQL —
  so concurrently starting app replicas cannot race.

## Deferred (worth doing, not in this change)

- `baselineOnMigrate` — needs schema-emptiness introspection per dialect.
- `connectRetries` — connect retry loop for container start-up ordering.
- `schemas`/`defaultSchema` — requires search_path/qualified-table handling.
- Additional dialects (MariaDB, SQLite, SQL Server) and `clean`.

## Verification

Unit tests per feature against the in-memory fake connection; the lock and
repair SQL paths are exercised against real Postgres/MySQL by the existing
e2e round-trip job. `gofmt`/`go vet`/`go test ./...` stay clean.
