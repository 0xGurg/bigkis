#!/bin/sh
set -eu

repo="${BIGKIS_REPO:-https://codeberg.org/gurg/bigkis}"
version="${BIGKIS_VERSION:-latest}"
install_dir="${BIGKIS_INSTALL_DIR:-/usr/local/bin}"
binary_name="${BIGKIS_BINARY:-bigkis}"
# When set to 1, skip checksum verification. We default to fail-closed so
# users get an actual error rather than a silently-unverified binary.
insecure="${BIGKIS_INSECURE:-0}"

say() {
  printf '%s\n' "$*"
}

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

detect_target() {
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"

  case "$os" in
    linux) os="linux" ;;
    *) die "unsupported OS: $os (bigkis currently ships Linux binaries only)" ;;
  esac

  case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) die "unsupported architecture: $arch" ;;
  esac

  printf '%s-%s' "$os" "$arch"
}

download() {
  url="$1"
  output="$2"

  if command -v curl >/dev/null 2>&1; then
    # -f fails on HTTP errors so an HTML 404 doesn't get written as the
    # binary; -L follows redirects.
    curl -fsSL "$url" -o "$output"
  elif command -v wget >/dev/null 2>&1; then
    # --tries=1 stops wget from retrying transient 5xx; --no-server-response
    # keeps output clean. wget exits non-zero on HTTP errors by default.
    wget -q --tries=1 -O "$output" "$url"
  else
    die "curl or wget is required to download bigkis"
  fi
}

sha256_file() {
  file="$1"

  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
  else
    return 1
  fi
}

install_binary() {
  src="$1"
  dest="$2"

  if [ -w "$install_dir" ]; then
    mkdir -p "$install_dir"
    install -m 755 "$src" "$dest"
  else
    command -v sudo >/dev/null 2>&1 || die "$install_dir is not writable and sudo is not available"
    sudo mkdir -p "$install_dir"
    sudo install -m 755 "$src" "$dest"
  fi
}

need_cmd uname
need_cmd tr
need_cmd awk
need_cmd grep
need_cmd sed
need_cmd install

target="$(detect_target)"
asset="bigkis-$target"

case "$version" in
  latest)
    # Codeberg's /releases/latest/download endpoint does not reliably
    # redirect to assets. Resolve the latest tag via the API first.
    # Derive the API URL from the repo URL:
    #   https://codeberg.org/gurg/bigkis → https://codeberg.org/api/v1/repos/gurg/bigkis
    api_url="$(echo "$repo" | sed 's|^\(https\?://[^/]*\)/\(.*\)|\1/api/v1/repos/\2|')/releases/latest"
    if command -v curl >/dev/null 2>&1; then
      resolved="$(curl -fsSL "$api_url" 2>/dev/null | grep -o '"tag_name":"[^"]*"' | head -1 | sed 's/"tag_name":"//;s/"//')" || true
    elif command -v wget >/dev/null 2>&1; then
      resolved="$(wget -qO- "$api_url" 2>/dev/null | grep -o '"tag_name":"[^"]*"' | head -1 | sed 's/"tag_name":"//;s/"//')" || true
    fi
    if [ -n "$resolved" ]; then
      version="$resolved"
      base_url="$repo/releases/download/$version"
    else
      die "could not resolve latest release tag from $api_url; set BIGKIS_VERSION=vX.Y.Z explicitly"
    fi
    ;;
  *)
    base_url="$repo/releases/download/$version"
    ;;
esac

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT INT TERM

binary_path="$tmp_dir/$asset"
checksums_path="$tmp_dir/checksums.txt"
binary_url="$base_url/$asset"
checksums_url="$base_url/checksums.txt"

say "Installing bigkis"
say "  repo:    $repo"
say "  version: $version"
say "  target:  $target"

download "$binary_url" "$binary_path" || die "failed to download $binary_url"
chmod +x "$binary_path"

if [ "$insecure" = "1" ]; then
  say "  checksum: SKIPPED (BIGKIS_INSECURE=1)"
else
  if ! download "$checksums_url" "$checksums_path"; then
    die "checksums.txt not found at $checksums_url; refusing to install without verification (set BIGKIS_INSECURE=1 to override)"
  fi
  expected="$(awk -v file="$asset" '$2 == file {print $1}' "$checksums_path")"
  if [ -z "$expected" ]; then
    die "checksums.txt has no entry for $asset"
  fi
  actual="$(sha256_file "$binary_path" || true)"
  if [ -z "$actual" ]; then
    die "no sha256 tool available; install sha256sum or shasum, or set BIGKIS_INSECURE=1"
  fi
  if [ "$actual" != "$expected" ]; then
    die "checksum mismatch for $asset (expected $expected, got $actual)"
  fi
  say "  checksum: ok"
fi

dest="$install_dir/$binary_name"
install_binary "$binary_path" "$dest"

say
say "Installed $dest"
say
say "Next steps:"
say "  bigkis --version"
say "  bigkis status --config ./system.toml"
say "  sudo bigkis apply --dry-run --config ./system.toml"
say "  sudo bigkis apply --config ./system.toml"
