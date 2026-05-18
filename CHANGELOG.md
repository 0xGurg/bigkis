# Changelog

This file is the source of truth for release notes. When you push a `v*`
tag, the release workflow extracts the matching section and uses it as the
body of the corresponding
[Codeberg release](https://codeberg.org/gurg/bigkis/releases), so the Codeberg
releases page is the canonical place to read what changed.

## v0.6.0 - "Interactive TUI"

`bigkis` now includes interactive terminal user interfaces that activate
automatically when your terminal supports them (TTY, no `--json`, no
`--quiet`, `NO_COLOR` unset, `BIGKIS_NO_TUI` unset). All non-TTY
fallbacks are preserved. The TUIs share a consistent lipgloss theme and
key bindings with the existing line-based output.

### New commands / flags

- **`bigkis import --interactive`** — Tabbed package picker with
  checkboxes, filter, and scan error display. Writes to `--output` or
  stdout on confirm.
- **`bigkis rollback`** — Split-pane browser showing rollback scripts
  (newest first) with body preview and confirm-before-execute flow.
- **`bigkis status`** — Dashboard with per-plugin badges (in sync /
  changes / unavailable), grouped operation detail, drift summary bar,
  and an `a` key that suggests the apply command.
- **`bigkis apply`** — Plan review TUI replacing the `proceed?` text
  prompt. Shows per-plugin change counts and operation detail.
- **`bigkis apply --select`** — Plan review with per-operation
  checkboxes for selective apply. Space toggles, `A` toggles all, Tab
  switches focus between panes, Enter proceeds with the checked subset.

### Plugin changes

- All four plugins (pacman, AUR, flatpak, node) now accept **subset
  reports** in `Apply()`. The report must be a subset of the cached plan
  (every operation must exist in the plan); extra planned operations the
  user deselected are allowed.

### Development

- 22 test files, 20 packages, 0 failures. Tests cover TUI model
  interaction (synthetic key messages), TUI gate logic (json/quiet/
  NO_COLOR/BIGKIS_NO_TUI/pipe), equivalence of `Run` vs `RunSelected`,
  subset-tolerant plugin assertions, and edge cases (empty states, scan
  errors, window resize, zero-checked-ops guard).
- Reviewed across 3 independent reviewers (oracle, zen, google).

Full PR: [#4](https://codeberg.org/gurg/bigkis/pulls/4)

## v0.5.1

Small follow-up to v0.5.0. No `bigkis` CLI behavior changes; this release is
packaging and release automation only.

### Release plumbing

- The release workflow can now update the `bigkis-bin` AUR package
  automatically after a successful tag-triggered release publish when the
  `AUR_SSH_PRIVATE_KEY` secret is configured.
- The AUR sync waits for the published `bigkis-linux-amd64` asset to become
  reachable before regenerating `PKGBUILD` / `.SRCINFO` and pushing to the AUR,
  so the package metadata tracks the actual published release asset checksums.

## v0.5.0 - "upgrade on apply"

Decman-style **system upgrades** are now part of the default `apply` flow.
`sudo bigkis apply` refreshes databases / upgrades packages for each enabled
plugin **before** install/remove reconciliation, in `settings.enabled` order.

### Behavior

- **pacman**: `sudo pacman -Syu --noconfirm`
- **aur**: `<aur_helper> -Sua --noconfirm` as `$SUDO_USER` when needed (pacman
  already handled repo sync; AUR-only upgrade path)
- **flatpak**: `flatpak update --system` and `flatpak update --user` for each
  username declared under `[flatpak.user_packages]`
- **node**: `npm update -g`, `pnpm update -g`, or `yarn global upgrade` for
  each manager that has declared or previously recorded packages

**`--no-upgrade`** on `apply` restores v0.4-style behavior (only bring the
system to the declared set, no broad upgrades).

**Dry-run** prints the same upgrade commands the real runner would execute.

**`apply --json`** is unchanged: it remains a plan-only view of add/remove
ops and does **not** list pending upgrades (expensive; out of scope).

### Orchestration

- Plugins skipped during planning (`Available` failure) are **not** passed to
  the upgrade phase, so the same unavailable-plugin warning is not repeated.

### Rollback

Rollback scripts still cover **install/remove only**; upgrades are not
recorded for automatic undo.

### Doctor / completion

- `bigkis doctor` includes an informational `apply:upgrade` check.
- Shell completions list `--no-upgrade`.

## v0.4.4

Small follow-up to v0.4.0. No code behavior changes; this is CI, release
plumbing, and docs only. (v0.4.1, v0.4.2, and v0.4.3 were burned during
release-engineering iteration on the workflow that attaches release notes;
the changes that would have shipped there are folded into v0.4.4.)

### CI

- Switch the `test` and `staticcheck` workflows from `codeberg-tiny` to
  `codeberg-small`. The tiny runner's podman socket has been timing out
  on the post-job cleanup step ("context deadline exceeded" on
  `.../archive?path=SUMMARY.md`), failing the workflows even when
  `go vet` / `go test -race` / `staticcheck` had already completed.
  The release workflow already runs on `codeberg-small`; align the rest.

### Release plumbing

- Release notes now live in this `CHANGELOG.md` at the repo root. The
  release workflow extracts the matching section and uploads it as
  `release-notes.md` on the Codeberg release, replacing the previous
  `wiki/Changelog.md` page.

### Docs

- The Installation wiki page now points at `bigkis import` for
  bootstrapping a `system.toml` from an existing Arch install, with the
  recommended `import` -> `check` -> `doctor` -> `status` flow.

## v0.4.0 - "ownership and recovery correctness"

This release fixes a handful of correctness and safety bugs found during a
post-v0.3 audit, polishes the CLI surface, and adds a `bigkis doctor`
preflight subcommand.

### Behavior changes worth knowing

- **`apply --json` exits 3 on drift** when changes would be applied.
  Previously it always exited 0. The wiki has documented this contract
  since v0.3; this is the implementation catching up.
- **`apply` now persists ownership state for plugins that are already in
  sync**. Without this, a clean first run on a fresh machine left
  `state.json` empty and the first-run-safety in `plan.Compute` quietly
  inhibited every future removal.
- **AUR helper runs as `$SUDO_USER` instead of root.** When you `sudo
  bigkis apply`, the AUR plugin drops privileges to the user that invoked
  sudo. Running bigkis as root with no `SUDO_USER` (e.g. `su` first, then
  `bigkis apply`) is rejected at `Available()` time, before any prompt.
- **Rollback scripts now reflect only what actually applied.** They are
  written at the end of `apply`, after each stage's success, so a partial
  failure no longer produces a script that tries to undo work that never
  happened.
- **Explicit `--config` no longer falls through.** A missing `--config`
  path is a hard error instead of silently picking
  `/etc/bigkis/system.toml` from the search path.
- **Status doesn't claim "system matches declaration" if a plugin was
  skipped** as unavailable. Human output prints which plugins were
  skipped; `--json` adds an `incomplete` flag.

### Bug fixes

- **`runner.Run` no longer panics** when `cmd.Run()` fails before exec
  (missing binary, exec permission denied). The synthesized exit code is
  -1.
- **`bigkis rollback --latest`** prints "no rollback scripts" instead of
  panicking when there are no scripts on disk.
- **Rollback scripts honor `cfg.Flatpak.Remote`** instead of hardcoding
  `flathub`. Custom remotes survive a rollback.
- **Rollback file names include nanoseconds** so two applies in the same
  second don't clobber each other.
- **Rollback scripts use POSIX single-quote escaping** for every target
  rather than Go's `%q`.
- **Lockfile records `[flatpak.user_packages]`** so per-user flatpaks
  aren't invisible in the lockfile.
- **`explain` recognises `[flatpak.user_packages]` declarations** and
  treats "declared in plugin A, installed via plugin B" as drift instead
  of "in sync".
- **Importer**: `--only flatpak` now writes
  `enabled = ["flatpak"]` so a subsequent `apply` doesn't queue removals
  for sections the import never populated. npm/pnpm probe errors now
  surface instead of being swallowed.
- **Host overlays carry `settings.prune_orphans`** alongside `aur_helper`
  / `node_manager`.
- **Node**: a manager that's referenced (in declared or previously
  declared packages) but missing on `PATH` now fails `status` /
  `apply --dry-run` instead of silently treating the live system as
  empty.

### New features

- **`bigkis doctor`**: preflight checks for the host (commands on PATH,
  root context, writable state/rollback dirs) and the loaded config (AUR
  helper, declared node managers, flatpak remote). Human output by
  default, `--json` for tooling. Exits 1 on any failing check.
- **`bigkis status --only` / `--skip`**: mirror the apply flags so you
  can scope status checks to one plugin.

### Polish

- `--quiet` is honored by the very first log line (the dim "config: ..."
  trace) instead of after the fact.
- Bash, zsh, and fish completions ship with all current flags
  (`--exit-on-drift`, `--json`, `--quiet`, the new `doctor` command,
  rollback flags, etc.).
- README install example pins `v0.4.0`.

## v0.3.0

See the [v0.3.0 release](https://codeberg.org/gurg/bigkis/releases/tag/v0.3.0).
