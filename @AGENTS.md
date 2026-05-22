# @AGENTS.md

This repository uses an agent-driven workflow for routine maintenance and release operations.

Acknowledged in this update:

- merged fix branch cleanup after release (`fix/staticcheck-upgrade-views` removed)
- Forgejo operations handled via the `fgj` CLI (PR/release flow)

Notes:

- `main` is protected; changes should go through PRs.
- Bugfix releases are tagged as `vX.Y.Z` and published on Codeberg releases.
