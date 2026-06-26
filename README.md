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

The first milestone is implemented:

- Versioned (`V<version>__<desc>.sql`) and repeatable (`R__<desc>.sql`) SQL
  migrations
- Commands: `migrate`, `info`, `validate`, `baseline`, `repair`
- `${placeholder}` substitution and `flyway.conf` / `FLYWAY_*` / flag config
- PostgreSQL and MySQL

## Usage

```sh
# Apply all pending migrations
rian migrate \
  --url jdbc:postgresql://localhost:5432/app \
  --user app --password secret \
  --locations filesystem:./sql

# See what is applied vs pending
rian info --url jdbc:postgresql://localhost:5432/app --user app --password secret

# Verify applied migrations still match the local files (checksums)
rian validate ...

# Baseline an existing database, or clear failed entries
rian baseline ...
rian repair ...
```

Configuration is merged from `flyway.conf` files, `FLYWAY_*` environment
variables, and CLI flags, with **flags > env > file** precedence — so existing
Flyway configuration works unchanged. MySQL URLs use `jdbc:mysql://…`.

Flags accept both Flyway's single-dash long form (`-url`, `-user`, `-locations`)
and the GNU double-dash form (`--url`), so existing Flyway command lines work
as-is.

### Container image

Rian is also published as a static multi-arch (amd64/arm64) image:

```sh
docker run --rm ghcr.io/clarkeandrew/rian:latest \
  migrate --url jdbc:postgresql://db:5432/app --user app --password secret \
  --locations filesystem:/sql
```

Mount your migrations into the container (e.g. `-v "$PWD/sql:/sql"`).

## GitHub Action

Run migrations from a workflow with the `clarkeandrew/rian` action. It downloads
the matching release binary for the runner (Linux, macOS, or Windows) and runs
the command you ask for:

```yaml
- uses: actions/checkout@v4
- uses: clarkeandrew/rian@v0.1.1
  with:
    command: migrate
    url: jdbc:postgresql://localhost:5432/app
    user: app
    password: ${{ secrets.DB_PASSWORD }}
    locations: filesystem:./sql
```

Inputs: `version` (default `latest`), `command` (default `migrate`), `url`,
`user`, `password`, `locations`, `table`, `args` (extra flags), and
`working-directory`. Pin `version` to a release tag for reproducible runs.

## Building

```sh
CGO_ENABLED=0 go build ./cmd/rian   # static binary
go test ./...                       # unit + golden tests
```

End-to-end Flyway-parity tests (real Flyway image hands off to Rian against live
Postgres and MySQL) live in `.github/workflows/e2e-tests.yml` and `test/e2e/`; run them
locally with `docker compose up -d` then
`go test -tags e2e ./test/e2e/...` with the `RIAN_E2E_*_URL` env vars set.

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
