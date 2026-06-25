# Rian — Initial Project Outline & Tech-Stack Plan

## Context

Rian is a new, greenfield open-source project. The goal: a tiny,
**Flyway-compatible** database migration runner that ships as a **lightweight
static single binary**, drops into Docker images, and works as a **drop-in
replacement for open-source Flyway** against an existing Flyway-managed
database — no JVM, no heavy runtime. Initial DB support: **PostgreSQL and
MySQL**. It must be open source with **no legal issues**.

This plan is backed by a deep-research workflow (4 research dimensions + an
adversarial verification pass on the highest-stakes claims). Two naive
assumptions were corrected by verification (the Flyway checksum algorithm, and
the MySQL driver's license).

### Decisions (confirmed with user)
- **Language: Go** — simplest path to a small static binary (`CGO_ENABLED=0`),
  pure-Go drivers for both DBs, best release tooling. A serial migration runner
  needs no async.
- **License: MIT** — fully permissive, compatible with all chosen drivers.
- **M1 scope: lean drop-in core + placeholders + `flyway.conf` parsing.**

---

## Tech Stack

| Concern | Choice | License | Notes |
|---|---|---|---|
| Language | Go (1.23+) | — | `CGO_ENABLED=0` static binary |
| Postgres driver | `jackc/pgx` v5 | MIT | pure Go; use `database/sql` adapter or native pgx |
| MySQL driver | `go-sql-driver/mysql` | **MPL-2.0** | pure Go; use **unmodified** (see Legal) |
| CLI framework | `spf13/cobra` (or stdlib `flag`) | Apache-2.0 / BSD | cobra for command ergonomics |
| Release | GoReleaser | MIT | cross-compile linux(amd64/arm64, musl)/macOS/windows |

Build invariant: CI asserts `CGO_ENABLED=0` and runs `file`/`ldd` on the
artifact to confirm it is static.

---

## Architecture / Project Layout

```
cmd/rian/            # main(): cobra root + subcommands
internal/config/     # flyway.conf + FLYWAY_* env + CLI flags merge
internal/scan/       # locations discovery + filename parsing (V/R, version, desc)
internal/checksum/   # Flyway-exact CRC32 (the keystone — see below)
internal/sql/        # statement splitting, MySQL DELIMITER, placeholder substitution
internal/history/    # flyway_schema_history read/write + ordering/success semantics
internal/db/         # dialect abstraction: connect, tx behavior, quoting
internal/db/postgres # pgx-backed dialect (transactional DDL)
internal/db/mysql    # mysql-backed dialect (implicit-commit DDL handling)
internal/engine/     # migrate/info/validate/baseline/repair orchestration
testdata/            # golden checksum fixtures (LF/CRLF/BOM/multiline)
```

A `Dialect` interface (`internal/db`) isolates the two DBs: connection,
identifier quoting, transaction-per-migration support flag, and schema-history
DDL. Postgres reports transactional DDL = true; MySQL = false.

---

## Flyway Compatibility Requirements (the must-replicate list)

True "drop-in" = point Rian at a DB an existing Flyway populated, and it
validates/continues without re-running or erroring.

### 1. Checksum — KEYSTONE, replicate exactly (verified vs flyway-core 7.5.0)
The naive "hash the file bytes" approach is **wrong** and breaks drop-in. Match
Flyway's `ChecksumCalculator`:
- Algorithm: **CRC32** (`java.util.zip.CRC32` semantics), result cast to a
  **signed 32-bit int** (`(int) crc32.getValue()`), stored in `checksum` column.
- Read **line by line** (BufferedReader.readLine semantics): **strip all line
  terminators** (`\n`, `\r`, `\r\n`).
- Update CRC with **each line's UTF-8 bytes, concatenated with NO separator** —
  do not re-insert newlines.
- **Strip a leading UTF-8 BOM** on the first line.
- Result is **line-ending independent** (LF == CRLF == CR) and trailing-newline
  independent. Re-encode each decoded line as UTF-8 before hashing.
- Implement in `internal/checksum`; cover with golden tests cross-checked
  against real Flyway output (LF/CRLF/BOM/multiline/non-ASCII fixtures).

### 2. `flyway_schema_history` table (functional format — safe to replicate)
Columns in order: `installed_rank` (int, PK), `version` (varchar, NULL for
repeatable), `description` (varchar), `type` (varchar, e.g. SQL), `script`
(varchar), `checksum` (int, signed), `installed_by` (varchar), `installed_on`
(timestamp, DB default), `execution_time` (int ms), `success` (boolean).
Default name `flyway_schema_history` (configurable). `installed_rank` ordering
and `success` semantics must match so `info`/`validate`/`repair` align.

### 3. Migration discovery / naming
- Versioned: `V<version>__<description>.sql` (separators `.`/`_`, double
  underscore before description). Repeatable: `R__<description>.sql`.
- Configurable prefixes/separators/suffix (default `.sql`).
- Version sort must match Flyway numeric-segment ordering (`1.10` > `1.9`).
- Undo (`U…`) is OUT of scope (Flyway commercial feature).

### 4. SQL execution
- Statement splitting on `;`; handle `BEGIN…END`/function bodies; honor MySQL
  `DELIMITER`.
- **Placeholders (in M1):** `${placeholder}` substitution from config
  (`flyway.placeholders.*` / `FLYWAY_PLACEHOLDERS_*`), default prefix `${`
  suffix `}`.
- Per-migration transaction where supported (Postgres). On failure: rollback +
  mark `success=false`.
- **MySQL: DDL implicitly commits** (verified — even MySQL 8.0 atomic DDL does
  NOT make multi-statement DDL roll back). Replicate Flyway behavior: mark
  migration failed, require `repair`, document manual cleanup. Do not promise
  rollback.

### 5. Commands (M1)
`migrate`, `info`, `validate`, `baseline`, `repair`. `clean` optional and
**disabled by default** (like Flyway's `cleanDisabled`).

### 6. Config compatibility (M1)
Read `flyway.conf`-style files, `FLYWAY_*` env vars, and `-url -user -password
-locations` flags (merge precedence: flags > env > file). Document unsupported
keys explicitly.

---

## Roadblocks / Risks (ranked)

1. **Checksum exactness** — one deviation fails `validate` on every existing
   migration. Make-or-break. Mitigation: implement per spec above + golden tests
   validated against real Flyway output; declare a target Flyway version range.
2. **MySQL transactional-DDL limit** — cannot be engineered away; replicate
   Flyway's "mark failed + repair" behavior and set expectations in docs.
3. **Version-ordering/discovery edge cases** — subtle parse differences cause
   phantom re-runs. Mitigation: golden tests on filename → (version, desc, type).
4. **Config-surface drift** — must read `flyway.conf`/`FLYWAY_*`/same flags or
   it's "compatible-ish," not drop-in. Mitigation: map the common surface.
5. **Flyway version target ambiguity** — internals are stable across 6.x/7.x but
   the free "Community" build has drifted from OSS. Declare "compatible with
   Flyway OSS engine vX–Y" and test against those.
6. **Build regressions reintroducing cgo** — low severity; CI asserts static.

---

## Legal (verified)

- **Project license: MIT.** Compatible with all chosen drivers.
- **Driver licenses:** pgx = MIT (clean). go-sql-driver/mysql = **MPL-2.0**
  (weak, file-level copyleft). Fine for a single binary: only obligation is to
  publish source of any *modified MPL files*. **Use it unmodified** (vendor
  as-is, upstream fixes) → no burden.
- **GPL trap to avoid:** do NOT use Oracle MySQL Connector/* or anything
  wrapping `libmysqlclient`/`libpq` (GPL/native). The pure-Go drivers above are
  clean.
- **Trademark:** "Flyway" is a registered trademark (Boxfuse GmbH, owned via
  Redgate). Cannot name the product "Flyway" — name is **Rian**. Nominative
  "compatible with Flyway" / "drop-in replacement for Flyway" is permitted with
  a no-affiliation disclaimer (in README).
- **Interoperability:** replicating the schema-history format and checksum
  algorithm for interop does not infringe copyright (functional format / method
  of operation). Reimplement from observed behavior/spec — do not copy Flyway's
  Java source.

---

## M1 Build Order

1. Repo bootstrap (CLAUDE.md, LICENSE-MIT, README+disclaimer, go.mod, CI). *(commit 1)*
2. `internal/checksum` + golden tests vs real Flyway fixtures. *(de-risk first)*
3. `internal/scan` filename parsing + version ordering + tests.
4. `internal/config` (flyway.conf + FLYWAY_* + flags) + placeholders.
5. `internal/db` Dialect + pgx Postgres impl; `internal/history`.
6. `internal/engine`: `migrate` + `info` + `validate` on Postgres end-to-end.
7. MySQL dialect (go-sql-driver/mysql) + implicit-commit/repair handling.
8. `baseline`, `repair`; wire all commands via cobra in `cmd/rian`.
9. GoReleaser cross-compile + static-binary CI assertion.

### Explicitly NOT in M1
Undo migrations (`U`), Java/script/callback migrations, callbacks
(`beforeMigrate` etc.), `clean` (optional/disabled), dry-run, cherry-pick,
baseline-on-migrate edge cases, DBs beyond Postgres+MySQL.

---

## Verification

- **Checksum parity (most important):** stand up a Postgres + MySQL via Docker,
  run **real Flyway** against a set of migrations (LF/CRLF/BOM/multiline), then
  run Rian against the same DB — `rian validate` must pass with zero checksum
  mismatches. Add these as golden fixtures in `testdata/`.
- **Drop-in handoff:** Flyway migrates V1..V3; Rian runs V4 against the same
  `flyway_schema_history` and `info` shows a consistent history.
- **Reverse handoff:** Rian migrates; Flyway validates clean.
- **MySQL failure path:** a deliberately failing DDL migration leaves a
  `success=false` row and `rian repair` clears it (matches Flyway).
- **Unit/golden tests:** checksum vectors, filename parsing, version ordering,
  placeholder substitution, config precedence.
- **Build:** CI runs `CGO_ENABLED=0 go build` and asserts a static binary
  (`file`/`ldd`) across the GoReleaser target matrix.

> Note: 2 of 4 research dimensions (flyway-compat-surface, legal-licensing)
> returned stub payloads due to agent output failures during research; the
> content here is reconstructed from the **verified** adversarial verdicts
> (checksum, MySQL DDL, trademark, license, interop) which used primary sources.
> Exact `flyway.conf` key names and the precise schema-history column types
> should be confirmed against the target Flyway version during implementation.
