# Implementation checklist

Last updated: 2026-07-29 UTC

## Core

- [x] UTC display and semver mapping (`1.YY.MMDD.HHmm` / `v1.YY.MMDDHHmm`)
- [x] Collision-aware version generator
- [x] GitHub release source, including authenticated internal drafts
- [x] Azure Blob index and Universal Packages source adapters
- [x] Concurrent multi-source latest/specific-version resolver
- [x] Signed manifest with channels, rollout, force/minimum, pause and block list
- [x] Ed25519 verification with primary and next key slots
- [x] SHA-256 binary cache, verified blobs and LRU pruning
- [x] Atomic replacement, backup and rollback
- [x] Cross-process update lock
- [x] Silent update exit-code contract
- [x] Interactive terminal menu
- [x] Pluggable feature registry and feature scaffolder
- [ ] Desktop system-tray frontend (headless core is complete)
- [ ] Background periodic auto-update loop

## Build and delivery

- [x] Build six OS/architecture targets
- [x] GitHub Actions module/build caches
- [x] Azure Pipelines module/build caches
- [x] Build and test on every push
- [x] Weekly Dependabot updates for Go and Actions
- [x] Draft release before promotion
- [x] Real old-binary silent-update gate before publishing
- [x] Release signing tools
- [ ] Configure `UPDATE_PRIVATE_KEY` and `UPDATE_PUBLIC_KEY` repository secrets
- [ ] Run first Release workflow and verify published version
- [ ] Configure Azure project/feed or Blob index destination
- [ ] Add macOS Developer ID signing and notarization secrets
- [ ] Add Windows Authenticode certificate

## Validation

- [x] Version parser/comparator unit tests
- [x] Config and manifest tests
- [x] End-to-end verified download/cache/apply test
- [ ] Latest `main` CI run is green
- [ ] Release probe reports the exact promoted version
- [ ] Manual tray smoke test on Windows, macOS and Linux desktop
