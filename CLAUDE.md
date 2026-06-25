# Rian

Rian is a tiny, Flyway-compatible database migration runner built for simple
deployments. It runs as a lightweight binary, fits easily into Docker images,
and applies SQL migrations without pulling in a heavy runtime or framework.

Designed for teams that want predictable schema changes with minimal
operational overhead, Rian keeps a clear record of every migration it applies,
so your database state stays traceable, repeatable, and easy to reason about.

## Scope (initial)

- Drop-in compatible with the open-source Flyway engine against an existing
  Flyway-managed DB
- PostgreSQL and MySQL only (initially)
- Single, lightweight, statically-linked Go binary (`CGO_ENABLED=0`) — no
  JVM/runtime

## Conventions

### Git

- Branch names use commitizen/conventional-commit type prefixes:
  `feat/branch-name`, `fix/branch-name`, `chore/branch-name`,
  `docs/branch-name`, `refactor/branch-name`, `test/branch-name`,
  `ci/branch-name`
- Keep branch names short and descriptive
- Do NOT reference Claude/AI in commit messages, PR titles, or PR bodies

### Commit messages

- Conventional Commits: `type(scope): short summary`
  (types: feat, fix, chore, docs, refactor, test, build, ci, perf, style)
- Optional body uses concise bullets, e.g.:

  ```
  feat(checksum): match Flyway CRC32 computation

  - read line-by-line, strip line terminators (line-ending independent)
  - strip leading UTF-8 BOM; return signed 32-bit int
  ```

## Planning docs

- Every plan is committed to `docs/plans/` in the repo.
- Name plan files `YYYY-MM-DD-short-kebab-description.md`
  (e.g. `2026-01-01-plan-to-do-something.md`), dated when the plan is written.
- Keep plans concise: context/why, the chosen approach, and how to verify.

## Architecture doc

- `docs/architecture.md` is a **living document** describing the system's
  architecture (components, data flow, key invariants).
- Keep it current: whenever an architectural change is made (new package
  responsibility, a changed boundary/interface, a new dialect, etc.), update
  `docs/architecture.md` in the same change.

## Build

- `CGO_ENABLED=0 go build` — CI must assert the binary is statically linked.
- `go test ./...` for unit and golden tests.
- `gofmt`/`go vet` must be clean.

## Compatibility notes (for contributors)

- The migration **checksum must match Flyway's `ChecksumCalculator` exactly** or
  validation against an existing Flyway-managed database will fail. It is a
  CRC32 computed line-by-line with line terminators stripped (line-ending
  independent), a leading UTF-8 BOM removed, and the result stored as a signed
  32-bit int. Do not hash raw file bytes.
- MySQL implicitly commits on DDL, so a failed multi-statement DDL migration
  cannot be rolled back. Replicate Flyway's behavior: mark the migration failed
  and require `repair`. Do not promise rollback on MySQL.
- Reimplement the Flyway schema-history format and checksum from observed
  behavior/spec. Do not copy Flyway's Java source.
