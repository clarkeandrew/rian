# Release pipeline & versioning

## Context

Milestone 1 shipped a working binary, and `.goreleaser.yaml` already
cross-compiled all targets — but nothing published artifacts, there was no
version automation, and no `main` branch. This adds an automated,
Conventional-Commit-driven release pipeline.

## Approach (chosen)

- **Versioning:** [`caarlos0/svu`](https://github.com/caarlos0/svu) computes the
  next SemVer from Conventional Commits. Pre-1.0 is enforced with `svu next --v0`
  (breaking → minor, never auto-`1.0.0`). First release is `v0.1.0`.
- **Trigger:** single workflow `.github/workflows/release.yml` on push to `main`
  (plus `workflow_dispatch` for the seed build / manual re-runs). One job tags
  and builds, because a `GITHUB_TOKEN`-pushed tag does not retrigger workflows.
- **Artifacts:** GoReleaser builds the existing archives + `checksums.txt` and a
  multi-arch GHCR image (`ghcr.io/clarkeandrew/rian:<version>` + `:latest`) from
  a distroless `Dockerfile` that copies the prebuilt static binary.
- **Release:** created as a **draft** (`release.draft: true`) for manual publish.
- **GHCR image:** private by default.

## Key seam

The draft only gates the GitHub Release page. Each qualifying push to `main`
creates a permanent tag and pushes the GHCR image regardless — abandoning a draft
consumes that version number (see `docs/releasing.md`).

## Files

- `Dockerfile` (new) — distroless/static, `COPY rian`.
- `.goreleaser.yaml` — `dockers` (amd64/arm64) + `docker_manifests`; keep draft.
- `.github/workflows/release.yml` (new) — svu + GoReleaser, pinned to the same
  GoReleaser version as `build-and-test.yml` (v2.4.4).
- `docs/releasing.md`, README container note.

## Verification

- `goreleaser check` passes on v2.4.4.
- `goreleaser release --snapshot --clean` builds archives + both images locally.
- `svu next --v0` previews the next version (`v0.1.0` with no tags).
- First real release: a draft Release appears with archives + checksums;
  `docker buildx imagetools inspect ghcr.io/clarkeandrew/rian:0.1.0` lists two
  platforms; `docker run --rm …:0.1.0 --version` prints `0.1.0`.
