# Lightweight binary and image: review and actions

## Context

Rian's pitch is a tiny static binary in a tiny image, so size regressions are
product regressions. This records a size review of the linux/amd64 release
binary (`-s -w -trimpath`, as GoReleaser builds it) and what was done about it.

## Measurements

- Release-equivalent binary before: **9.93 MB** (unstripped dev build: 14.6 MB).
- The bulk is irreducible for a two-driver migration tool: the Go runtime plus
  `crypto/tls`, pgx (Postgres), and go-sql-driver (MySQL).
- The only pure-convenience dependency was the CLI framework (cobra + pflag):
  an equivalent stdlib `flag` CLI measured ~0.7 MB smaller in isolation and
  **270 KB** smaller in the full binary (overlap with already-linked stdlib).

## Actions

- **Replace cobra/pflag with a stdlib `flag` CLI** (−270 KB, −2 dependencies).
  `flag` treats `-url` and `--url` identically, which is exactly the Flyway
  compatibility Rian wants — the argv-rewriting shim is gone too.
- **CI size budget**: the GoReleaser snapshot job now fails if the linux/amd64
  binary exceeds 11 MiB, so a size regression (e.g. a heavy new dependency)
  is caught in review, not in a release.

## Considered and rejected

- **`scratch` base instead of `distroless/static`** (~2 MB smaller image):
  distroless provides maintained CA certificates, `/etc/passwd`, and a nonroot
  user; replicating that from an alpine build stage adds moving parts for a
  one-time 2 MB win. Revisit if image size becomes critical.
- **UPX compression** (~50% smaller file): antivirus false positives, no
  memory-mapped page sharing, slower start. Wrong trade for a deploy tool.
- **pgconn-only Postgres driver** (est. ~1 MB): means hand-rolling parameter
  encoding/decoding on the most compatibility-critical I/O path.
- **`.tar.xz` archives** (smaller downloads): would break the GitHub Action's
  asset-name contract for existing pinned versions.

## Verification

`go test ./...` including rewritten CLI tests; binary diff measured with
`-s -w -trimpath` builds before/after; CI asserts the budget from now on.
