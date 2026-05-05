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

## Install

The recommended first install is the bootstrap script. It downloads a prebuilt
Linux binary, so the target machine does **not** need Go installed.

```sh
curl -fsSL https://codeberg.org/gurg/bigkis/raw/branch/main/install.sh | sh
```

The installer detects `linux-amd64` or `linux-arm64`, downloads the matching
release asset, verifies `checksums.txt` when available, and installs to
`/usr/local/bin/bigkis`.

You can override defaults:

```sh
BIGKIS_VERSION=v0.1.0 sh install.sh
BIGKIS_INSTALL_DIR="$HOME/.local/bin" sh install.sh
BIGKIS_REPO=https://codeberg.org/gurg/bigkis sh install.sh
```

### Manual Binary Install

Download the matching asset from the
[Codeberg releases](https://codeberg.org/gurg/bigkis/releases):

- `bigkis-linux-amd64`
- `bigkis-linux-arm64`
- `checksums.txt`

Then install it:

```sh
chmod +x bigkis-linux-amd64
sudo install -m 755 bigkis-linux-amd64 /usr/local/bin/bigkis
```

### Declarative Long-Term Install

After the bootstrap, let `bigkis` manage itself with an AUR package:

```toml
[aur]
packages = ["bigkis-bin"]
```

That solves the chicken-and-egg problem: the first install uses a prebuilt
binary, and future installs/upgrades are declarative.

### Source Install

For development:

```sh
git clone https://codeberg.org/gurg/bigkis.git
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
4. `$XDG_CONFIG_HOME/bigkis/system.toml`
5. `~/.config/bigkis/system.toml`

Then:

```sh
bigkis status                   # show drift, no changes
bigkis apply --dry-run          # show what would change
sudo bigkis apply                # apply system plugins and write /var/lib/bigkis
sudo bigkis apply --only pacman  # only one plugin
sudo bigkis apply --skip flatpak # skip a plugin
sudo bigkis apply --yes          # skip the confirmation prompt
```

`pacman`, system-wide `flatpak`, and persisting state under `/var/lib/bigkis`
need root, so most users will run `sudo bigkis apply`. The `aur` plugin runs
the helper (`yay`/`paru`) without forcing root and lets the helper prompt for
elevation as needed.

## Configuration

```toml
[settings]
enabled      = ["pacman", "aur", "flatpak", "node"]  # plugins + execution order
aur_helper   = "yay"                                  # or "paru"
node_manager = "pnpm"                                 # default for [node].packages

[pacman]
packages = [
  "base",
  "linux",
  "linux-firmware",
  "networkmanager",
  "git",
  "base-devel",
  "go",
  "neovim",
]
ignored = ["opendoas"]                                # never install or remove

[aur]
packages = ["bigkis-bin", "fnm-bin", "visual-studio-code-bin"]
ignored  = ["yay"]

[flatpak]
packages = ["org.mozilla.firefox"]
ignored  = []
[flatpak.user_packages]
georgep = ["com.valvesoftware.Steam"]

[node]
packages = ["typescript", "eslint"]                   # uses settings.node_manager
[[node.package]]                                      # per-package override
name    = "@vue/cli"
manager = "yarn"
```

## Pacman Baseline

`[pacman].packages` is your native Arch baseline. It should contain every
explicit native package you want `bigkis` to keep installed.

For a workstation, that usually includes:

- Boot/system essentials: `base`, `linux`, `linux-firmware`.
- Network and access: `networkmanager`, `iwd`, `openssh`, `ufw`.
- Package/build tools: `git`, `base-devel`, `go`.
- Shell/editor/CLI tools: `fish`, `neovim`, `ripgrep`, `fd`, `fzf`.
- Desktop stack and native apps, if you install them through pacman:
  `gnome`, `plasma`, `sway`, `firefox`, `alacritty`, `kitty`, etc.

Think of this list as the answer to: “if I reinstall Arch, which native repo
packages should be explicitly present on this machine?”

`bigkis` tracks explicit packages, not dependency packages. If `pacman`
installed a library only as a dependency of something else, you normally do not
need to list that library yourself.

### Starting From an Existing Arch Install

On a current system, export the packages pacman already considers explicit:

```sh
pacman -Qqen > pacman-packages.txt
pacman -Qqem > aur-packages.txt
```

Then curate those lists into `system.toml`. Do not blindly paste everything.
Use the exported files as a starting point, remove experiments you no longer
want, and move foreign packages from `aur-packages.txt` into `[aur].packages`.

### Ignored Packages

`ignored` means “never install or remove this package.” Use it for packages
that are deliberately handled outside `bigkis`, such as:

- a manually managed replacement like `opendoas`;
- the AUR helper itself (`yay` / `paru`);
- vendor packages or temporary experiments;
- anything you want visible in your config but not controlled yet.

Ignored packages are omitted from both additions and removals.

## How Removals Work

`bigkis` only removes packages it previously recorded as declared. The first
time you run `apply` against an existing system, nothing is removed: `bigkis`
only records the declared set. On subsequent runs, anything in that recorded set
that is no longer declared can be removed.

Per-plugin mechanics:

- **pacman**: removed packages are demoted with `pacman -D --asdeps`, then
  `pacman -Rns $(pacman -Qdtq)` prunes orphans in a loop until none remain.
- **aur**: `yay -Rns` (or `paru -Rns`).
- **flatpak**: `flatpak uninstall --system` (or `--user`).
- **node**: removed via the manager that originally installed the package
  according to recorded state.

State lives at `/var/lib/bigkis/state.json` when running as root, otherwise at
`$XDG_STATE_HOME/bigkis/state.json` (defaults to
`~/.local/state/bigkis/state.json`).

## Release Assets

The bootstrap installer expects prebuilt release assets named:

- `bigkis-linux-amd64`
- `bigkis-linux-arm64`
- `checksums.txt`

`checksums.txt` should contain standard SHA256 lines:

```text
<sha256>  bigkis-linux-amd64
<sha256>  bigkis-linux-arm64
```

## License

MIT - see [LICENSE](LICENSE).

`bigkis` is a clean-room reimplementation in Go inspired by
[decman](https://github.com/kiviktnm/decman) (GPL-3.0). No decman source code
is included or derived; the projects share design ideas only.
