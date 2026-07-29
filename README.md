# go-selfupdate-template

A reusable Go application template with verified self-updates from GitHub Releases and Azure, six-platform builds, rollout policy, local binary cache, rollback, CLI menu, and CI release gating.

## Targets

`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, `windows/arm64`.

## Quick start

```bash
make test
make build
./bin/app version --json
./bin/app menu
```

Copy `configs/config.example.json` to the path printed by your OS config directory, or point `APP_CONFIG` at it. CLI flags override environment, environment overrides the file, and signed manifest policy controls mandatory updates and blocked versions.

## Commands

```bash
app version [--json]
app update [--check] [--silent] [--version 1.YY.MMDD.HHmm]
app menu
```

Silent update exit codes are stable: `0` updated, `10` current, `20` not found, `30` verification failure, `40` apply/rollback failure, `50` all sources unavailable, `60` lock held, `64` bad invocation.

## Versioning

Users see `1.YY.MMDD.HHmm`. Git tags use valid semver `v1.YY.MMDDHHmm`. Both are generated in UTC. `tools/genversion` advances the minute when a tag collision exists.

## Signing setup

Generate a pair once:

```bash
make keygen
```

Store `PRIVATE_KEY` in the GitHub Actions secret `UPDATE_PRIVATE_KEY`, and `PUBLIC_KEY` in `UPDATE_PUBLIC_KEY`. Never commit the private key. Two public-key slots are embedded so keys can be rotated before the old key is retired.

## Release safety gate

The Release workflow builds and signs all six binaries, creates a draft GitHub release, builds an old-version probe, makes that probe update itself from the authenticated draft, verifies the installed version, and only then publishes the release. Public clients never receive a GitHub token and cannot inspect drafts.

Every push runs formatting, vet, race tests, an end-to-end update test, and the six-target build matrix. Azure Pipelines mirrors the cached test/build path.

## Add app functionality

```bash
make new-feature NAME=myfeature
```

Features register themselves through `internal/features`; core updater and `main.go` stay untouched. Keep headless builds as the default and put desktop-only integrations behind build tags.

## Update sources

GitHub and Azure are queried concurrently. A requested version may come from either source. Latest selection merges all healthy sources and chooses the newest version. One failed mirror does not fail the update when another mirror has the release.

Azure Blob index mode is the portable default. Azure Universal Packages mode is represented in configuration, but publishing/download credentials must be supplied by the consuming organization.

## Security rules

Checksums are signed with Ed25519. Downloads are cached by SHA-256, installed with an atomic backup/replace sequence, and rolled back when replacement fails. Internal draft checks require `APP_UPDATE_TOKEN` at runtime; tokens are never linked into shipped binaries.
