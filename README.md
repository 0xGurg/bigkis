# bigkis

`bigkis` (Tagalog: *bundle / sheaf*) is a declarative package manager for Arch
Linux. It is a Go reimagining of [decman](https://github.com/kiviktnm/decman)
that drops the dotfile / config / systemd manager and keeps only what its
name suggests: package bundles.

## What it manages

| Plugin   | Source                           | Notes                                             |
| -------- | -------------------------------- | ------------------------------------------------- |
| pacman   | Native Arch repositories         | install, demote-to-dep + orphan prune on removal  |
| aur      | AUR via `yay` or `paru`          | wraps an installed helper; no native makepkg      |
| flatpak  | flathub, system-wide and per-user | `--system` and per-user `--user` installs         |
| node     | global `npm` / `pnpm` / `yarn`   | per-package manager overrides                     |

What it deliberately does **not** manage: dotfiles, directories, symlinks,
systemd units, users, or PGP keys. Use a separate dotfile tool for those.

## Install

```sh
go install github.com/georgepagarigan/bigkis/cmd/bigkis@latest
```

Or build from source:

```sh
git clone https://github.com/georgepagarigan/bigkis.git
cd bigkis
go build -o bigkis ./cmd/bigkis
```

Cross-compile from macOS for an Arch host:

```sh
GOOS=linux GOARCH=amd64 go build -o bigkis ./cmd/bigkis
```

## Usage

Write a `system.toml` (see [`examples/system.toml`](examples/system.toml)) and
place it at one of the following paths (first match wins):

1. `--config <path>` (CLI flag)
2. `$BIGKIS_CONFIG`
3. `/etc/bigkis/system.toml`
4. `$XDG_CONFIG_HOME/bigkis/system.toml` or `~/.config/bigkis/system.toml`

Then:

```sh
bigkis status                   # show drift, no changes
bigkis apply --dry-run          # show what would change
sudo bigkis apply                # actually apply (sudo for system plugins)
sudo bigkis apply --only pacman  # only one plugin
sudo bigkis apply --skip flatpak # skip a plugin
sudo bigkis apply --yes          # skip the confirmation prompt
```

`pacman`, system-wide `flatpak`, and persisting state under `/var/lib/bigkis`
all need root, so most users will run `sudo bigkis apply`. The `aur` plugin
intentionally runs without root and lets the helper (`yay`/`paru`) prompt for
elevation as needed.

## Configuration

```toml
[settings]
enabled      = ["pacman", "aur", "flatpak", "node"]  # plugins + execution order
aur_helper   = "yay"                                  # or "paru"
node_manager = "pnpm"                                 # default for [node].packages

[pacman]
packages = ["base", "linux", "neovim"]
ignored  = ["opendoas"]                              # never install or remove

[aur]
packages = ["fnm-bin", "visual-studio-code-bin"]
ignored  = ["yay"]

[flatpak]
packages = ["org.mozilla.firefox"]
ignored  = []
[flatpak.user_packages]
georgep = ["com.valvesoftware.Steam"]

[node]
packages = ["typescript", "eslint"]                  # uses settings.node_manager
[[node.package]]                                     # per-package override
name    = "@vue/cli"
manager = "yarn"
```

## How removals work

`bigkis` only removes packages it previously installed. The first time you run
`apply` against an existing system, nothing is removed: bigkis only records
the declared set. On subsequent runs, anything in the recorded set that's no
longer declared is removed.

Per-plugin mechanics:

- **pacman**: removed packages are demoted with `-D --asdeps`, then
  `pacman -Rns $(pacman -Qdtq)` prunes orphans in a loop until none remain.
- **aur**: `yay -Rns` (or `paru -Rns`).
- **flatpak**: `flatpak uninstall --system` (or `--user`).
- **node**: removed via the manager that originally installed the package
  according to recorded state.

State lives at `/var/lib/bigkis/state.json` when running as root, otherwise
at `$XDG_STATE_HOME/bigkis/state.json` (defaults to
`~/.local/state/bigkis/state.json`).

## License

MIT — see [LICENSE](LICENSE) (TBD).
