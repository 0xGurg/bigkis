# bigkis

`bigkis` (Tagalog: *bundle / sheaf*) is a declarative package manager for Arch
Linux. It is a Go reimagining of [decman](https://github.com/kiviktnm/decman)
that drops the dotfile / config / systemd manager and keeps only what its name
suggests: package bundles.

## What It Manages

| Plugin  | Source                            | Notes                                            |
| ------- | --------------------------------- | ------------------------------------------------ |
| pacman  | Native Arch repositories          | install, demote-to-dep + orphan prune on removal |
| aur     | AUR via `yay` or `paru`           | wraps an installed helper; no native makepkg     |
| flatpak | flathub, system-wide and per-user | `--system` and per-user `--user` installs        |
| node    | global `npm` / `pnpm` / `yarn`    | per-package manager overrides                    |

What it deliberately does **not** manage: dotfiles, directories, symlinks,
systemd units, users, or PGP keys. Use a separate dotfile tool for those.

## Quick install

```sh
curl -fsSL https://codeberg.org/gurg/bigkis/raw/branch/main/install.sh | sh
```

To pin a version or change install location:

```sh
BIGKIS_VERSION=v0.2.0 sh install.sh
BIGKIS_INSTALL_DIR="$HOME/.local/bin" sh install.sh
```

Other install variants live in the
[Installation guide](https://codeberg.org/gurg/bigkis/wiki/Installation).

## Quick usage

Write a `system.toml` (see [`examples/system.toml`](examples/system.toml)),
then:

```sh
bigkis status                    # show drift, no changes
bigkis apply --dry-run           # show what would change
sudo bigkis apply                # apply system plugins and write state
```

## Documentation

Full docs live on the
[Codeberg wiki](https://codeberg.org/gurg/bigkis/wiki):

- [Installation](https://codeberg.org/gurg/bigkis/wiki/Installation) -
  bootstrap, manual binary, declarative AUR, source build and cross-compile.
- [Configuration](https://codeberg.org/gurg/bigkis/wiki/Configuration) -
  `system.toml` schema and config search paths.
- [Pacman Baseline](https://codeberg.org/gurg/bigkis/wiki/Pacman-Baseline) -
  choosing your explicit baseline, bootstrapping from an existing Arch install,
  and the `ignored` list.
- [Removals and State](https://codeberg.org/gurg/bigkis/wiki/Removals-and-State) -
  how `apply` removes packages per plugin and where state is stored.
- [Release Assets](https://codeberg.org/gurg/bigkis/wiki/Release-Assets) -
  asset naming and `checksums.txt` format.

## License

MIT - see [LICENSE](LICENSE).

`bigkis` is a clean-room reimplementation in Go inspired by
[decman](https://github.com/kiviktnm/decman) (GPL-3.0). No decman source code
is included or derived; the projects share design ideas only.
