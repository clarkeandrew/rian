# Rian

Rian is a tiny, Flyway-compatible database migration runner built for simple
deployments. It runs as a lightweight binary, fits easily into Docker images,
and applies SQL migrations without pulling in a heavy runtime or framework.

Designed for teams that want predictable schema changes with minimal
operational overhead, Rian keeps a clear record of every migration it applies,
so your database state stays traceable, repeatable, and easy to reason about.

## Why Rian

- **Lightweight single binary** — statically linked Go (`CGO_ENABLED=0`), no JVM
  or runtime to install. Drops cleanly into a minimal/`scratch` Docker image.
- **Drop-in for the open-source Flyway engine** — reads the same
  `flyway_schema_history` table, the same `V__`/`R__` SQL migration files, and
  computes byte-identical checksums, so it can run against a database an
  existing Flyway already manages.
- **PostgreSQL and MySQL** supported initially.

## Status

Early development. The first milestone targets:

- Versioned (`V<version>__<desc>.sql`) and repeatable (`R__<desc>.sql`) SQL
  migrations
- Commands: `migrate`, `info`, `validate`, `baseline`, `repair`
- `${placeholder}` substitution and `flyway.conf` / `FLYWAY_*` / flag config
- PostgreSQL and MySQL

## Building

```sh
CGO_ENABLED=0 go build ./cmd/rian
go test ./...
```

## Compatibility

Rian aims to be a drop-in replacement for the **open-source Flyway engine**
(target version range to be declared as the engine stabilizes). Some Flyway
features — undo migrations, Java/script migrations, callbacks — are intentionally
out of scope.

> **Note on MySQL:** MySQL implicitly commits DDL statements, so a migration
> that fails partway cannot be rolled back. Like Flyway, Rian marks the failed
> migration in the history table and requires `repair`; manual cleanup of any
> partially-applied DDL may be needed.

## License

MIT — see [LICENSE](LICENSE).

## Trademark / affiliation

Rian is **not affiliated with or endorsed by Redgate or Boxfuse**. *Flyway* is a
registered trademark of Boxfuse GmbH. Rian is an independent, clean-room tool
designed to be compatible with the open-source Flyway engine; it reimplements
interoperable formats (the schema-history table layout and checksum algorithm)
from their observed behavior and does not include Flyway source code.
