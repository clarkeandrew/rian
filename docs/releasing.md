# Releasing Rian

Releases are automated from [Conventional Commits](https://www.conventionalcommits.org/)
on the `main` branch. You do not bump versions by hand.

## How it works

`.github/workflows/release.yml` runs on every push to `main`:

1. [`svu`](https://github.com/caarlos0/svu) computes the next version from the
   commit messages since the last tag:
   - `fix:` → patch (`0.3.1` → `0.3.2`)
   - `feat:` → minor (`0.3.1` → `0.4.0`)
   - breaking change (`feat!:` / `BREAKING CHANGE:`) → minor **while pre-1.0**
     (the workflow passes `--v0`, so it never auto-jumps to `1.0.0`)
   - only `docs:`/`chore:`/`test:`/etc. → no release (the run is a no-op)
2. If a bump is warranted, the workflow tags it and runs
   [GoReleaser](https://goreleaser.com/), which:
   - builds static (`CGO_ENABLED=0`) binaries for linux/macOS/windows on
     amd64/arm64, archived as `.tar.gz` (`.zip` on Windows) with `checksums.txt`;
   - builds and pushes a multi-arch image to
     `ghcr.io/clarkeandrew/rian:<version>` and `:latest`;
   - **publishes** the GitHub Release immediately (no draft step).

Tagging and building happen in one job because a tag pushed by `GITHUB_TOKEN`
does not retrigger workflows.

## Important: releases are immediate and tags are permanent

There is no review gate: every qualifying push to `main` ships a public Release,
a permanent git tag, and a container image on GHCR. To retract a version, delete
the Release, then the tag and image (the version number stays consumed):

```sh
git push --delete origin vX.Y.Z
# and delete the corresponding GHCR image version in the package settings
```

## Versions and SemVer

Rian is pre-1.0 (`0.x`): minor versions may contain breaking changes. The
first release is `v0.1.0`.

### Cutting v1.0.0

When the CLI surface and schema-history format are stable, drop the `--v0` flag
from the `svu next` step in `release.yml` (or push a `v1.0.0` tag manually). From
then on, breaking changes bump the major version per standard SemVer.

## Container image

The image is published to GitHub Container Registry:

```sh
docker pull ghcr.io/clarkeandrew/rian:latest
docker run --rm ghcr.io/clarkeandrew/rian:latest --version
```

The GHCR package is **private** by default; make it public in the package
settings if you want unauthenticated pulls.

## Manual / first release

`workflow_dispatch` runs the pipeline against the tag currently at `HEAD`
(used for the very first `v0.1.0` build, or to re-run a release). It fails fast
if `HEAD` is not tagged.

## Local dry-run

```sh
go run github.com/goreleaser/goreleaser/v2@v2.4.4 check         # validate config
go run github.com/goreleaser/goreleaser/v2@v2.4.4 release --snapshot --clean
svu next --v0                                                   # preview next version
```

`--snapshot` builds everything (including the Docker images) locally without
publishing. Building the arm64 image locally needs QEMU:
`docker run --privileged --rm tonistiigi/binfmt --install all`.
