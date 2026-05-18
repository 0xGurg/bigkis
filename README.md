# bigkis

`bigkis` (Tagalog: *bundle / sheaf*) is a declarative package manager for Arch
Linux. It is a Go reimagining of [decman](https://github.com/kiviktnm/decman)
that drops the dotfile / config / systemd manager and keeps only what its name
suggests: package bundles.

## What It Manages

| Plugin  | Source                            | Notes                                            |
| ------- | --------------------------------- | ------------------------------------------------ |
| pacman  | Native Arch repositories          | `apply` syncs/repos (`-Syu`) before changes; install; demote-to-dep + orphan prune on removal |
| aur     | AUR via `yay` or `paru`           | `apply` runs `-Sua` after pacman (AUR-only upgrades); wraps helper; no native makepkg     |
| flatpak | flathub, system-wide and per-user | `apply` runs `flatpak update` for system + declared users; `--system` / `--user` installs        |
| node    | global `npm` / `pnpm` / `yarn`    | `apply` upgrades globals per manager in use; per-package manager overrides                    |

What it deliberately does **not** manage: dotfiles, directories, symlinks,
systemd units, users, or PGP keys. Use a separate dotfile tool for those.

## Quick install

```sh
curl -fsSL https://codeberg.org/gurg/bigkis/raw/branch/main/install.sh | sh
```

To pin a version or change install location:

```sh
BIGKIS_VERSION=v0.5.0 sh install.sh
BIGKIS_INSTALL_DIR="$HOME/.local/bin" sh install.sh
```

Other install variants live in the
[Installation guide](https://codeberg.org/gurg/bigkis/wiki/Installation).

## Quick usage

Write a `system.toml` (see [`examples/system.toml`](examples/system.toml)),
then:

```sh
bigkis doctor                    # preflight checks (PATH, helper, remote, perms)
bigkis status                    # show drift, no changes
bigkis status --exit-on-drift    # exit 3 when drift detected (CI-friendly)
bigkis apply --dry-run           # show upgrades + install/remove preview (no changes)
bigkis apply --json              # emit machine-readable plan; exit 3 on drift (no upgrade list)
sudo bigkis apply                # upgrade packages, then converge; write state
sudo bigkis apply --no-upgrade   # skip upgrades (presence-only, like v0.4)
sudo bigkis apply --yes --quiet  # skip the prompt, suppress info logs

# interactive mode
bigkis import --interactive                    # tabbed package picker before writing system.toml
bigkis rollback                                # split-pane browser to preview and run rollback scripts
bigkis status                                  # dashboard with per-plugin change counts and operation detail
bigkis apply                                   # plan review screen replacing the text confirmation prompt
bigkis apply --select                          # plan review with per-operation checkboxes for selective apply
```

> **Interactive mode** activates automatically when your terminal supports it
> (TTY, no `--json`, no `--quiet`, no `NO_COLOR`, not piped). Set
> `BIGKIS_NO_TUI=1` to disable it globally. All TUIs support `q` to quit and
> share the same visual theme with the line-based output.

`bigkis` returns distinct exit codes (`0` ok, `1` error, `2` user-cancelled,
`3` drift) so wrappers can branch precisely. See
[Exit Codes](https://codeberg.org/gurg/bigkis/wiki/Exit-Codes).

## Documentation

Full docs live on the
[Codeberg wiki](https://codeberg.org/gurg/bigkis/wiki):

- [Installation](https://codeberg.org/gurg/bigkis/wiki/Installation) -
  bootstrap, manual binary, declarative AUR, source build and cross-compile.
- [Configuration](https://codeberg.org/gurg/bigkis/wiki/Configuration) -
  `system.toml` schema and config search paths.
- [Doctor](https://codeberg.org/gurg/bigkis/wiki/Doctor) - preflight checks
  before the first apply.
- [Pacman Baseline](https://codeberg.org/gurg/bigkis/wiki/Pacman-Baseline) -
  choosing your explicit baseline, bootstrapping from an existing Arch install,
  and the `ignored` list.
- [Removals and State](https://codeberg.org/gurg/bigkis/wiki/Removals-and-State) -
  how `apply` removes packages per plugin and where state is stored.
- [Rollback](https://codeberg.org/gurg/bigkis/wiki/Rollback) - rollback
  scripts written before each apply, and how to run them.
- [Explain](https://codeberg.org/gurg/bigkis/wiki/Explain) and
  [Import](https://codeberg.org/gurg/bigkis/wiki/Import) - per-package
  debugging and bootstrapping a config from an existing system.
- [Completion](https://codeberg.org/gurg/bigkis/wiki/Completion) - bash /
  zsh / fish completion.
- [Exit Codes](https://codeberg.org/gurg/bigkis/wiki/Exit-Codes) - the
  contract for `0` / `1` / `2` / `3` exits.
- [Release Assets](https://codeberg.org/gurg/bigkis/wiki/Release-Assets) -
  asset naming, `checksums.txt`, and optional minisign signature.

## License

MIT - see [LICENSE](LICENSE).

`bigkis` is a clean-room reimplementation in Go inspired by
[decman](https://github.com/kiviktnm/decman) (GPL-3.0). No decman source code
is included or derived; the projects share design ideas only.
