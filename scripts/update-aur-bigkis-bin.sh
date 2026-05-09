#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
  printf 'usage: %s <version-tag> <aur-repo-dir>\n' "$0" >&2
  exit 1
fi

version_tag="$1"
target_dir="$2"

case "$version_tag" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *)
    printf 'error: version must look like vX.Y.Z (got %s)\n' "$version_tag" >&2
    exit 1
    ;;
esac

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "$script_dir/.." && pwd)
pkgver="${version_tag#v}"
pkgurl='https://codeberg.org/gurg/bigkis'
bin_path="$repo_dir/dist/bigkis-linux-amd64"
license_path="$repo_dir/LICENSE"

[ -f "$bin_path" ] || {
  printf 'error: release binary not found at %s\n' "$bin_path" >&2
  exit 1
}
[ -f "$license_path" ] || {
  printf 'error: upstream LICENSE not found at %s\n' "$license_path" >&2
  exit 1
}

bin_sha=$(sha256sum "$bin_path" | awk '{print $1}')
license_sha=$(sha256sum "$license_path" | awk '{print $1}')

mkdir -p "$target_dir"

cat > "$target_dir/PKGBUILD" <<'EOF'
# Maintainer: gurg <gurg@noreply.codeberg.org>

pkgname=bigkis-bin
pkgver=@@PKGVER@@
pkgrel=1
pkgdesc='Declarative package bundle manager for Arch Linux'
arch=('x86_64')
url='@@PKGURL@@'
license=('MIT')
depends=('sudo')
optdepends=(
  'yay: AUR helper support'
  'paru: AUR helper support'
  'flatpak: flatpak plugin support'
  'npm: node plugin support'
  'pnpm: node plugin support'
  'yarn: node plugin support'
)
provides=('bigkis')
conflicts=('bigkis')
options=('!strip' '!debug')
source=(
  "bigkis-linux-amd64::${url}/releases/download/v${pkgver}/bigkis-linux-amd64"
  "upstream-LICENSE::${url}/raw/tag/v${pkgver}/LICENSE"
)
sha256sums=(
  '@@BIN_SHA@@'
  '@@LICENSE_SHA@@'
)

package() {
  install -Dm755 "${srcdir}/bigkis-linux-amd64" "${pkgdir}/usr/bin/bigkis"
  install -Dm644 "${srcdir}/upstream-LICENSE" \
    "${pkgdir}/usr/share/licenses/${pkgname}/LICENSE"
}
EOF

sed -i \
  -e "s|@@PKGVER@@|$pkgver|g" \
  -e "s|@@PKGURL@@|$pkgurl|g" \
  -e "s|@@BIN_SHA@@|$bin_sha|g" \
  -e "s|@@LICENSE_SHA@@|$license_sha|g" \
  "$target_dir/PKGBUILD"

cat > "$target_dir/.SRCINFO" <<EOF
pkgbase = bigkis-bin
	pkgdesc = Declarative package bundle manager for Arch Linux
	pkgver = ${pkgver}
	pkgrel = 1
	url = ${pkgurl}
	arch = x86_64
	license = MIT
	depends = sudo
	optdepends = yay: AUR helper support
	optdepends = paru: AUR helper support
	optdepends = flatpak: flatpak plugin support
	optdepends = npm: node plugin support
	optdepends = pnpm: node plugin support
	optdepends = yarn: node plugin support
	provides = bigkis
	conflicts = bigkis
	options = !strip
	options = !debug
	source = bigkis-linux-amd64::${pkgurl}/releases/download/${version_tag}/bigkis-linux-amd64
	source = upstream-LICENSE::${pkgurl}/raw/tag/${version_tag}/LICENSE
	sha256sums = ${bin_sha}
	sha256sums = ${license_sha}

pkgname = bigkis-bin
EOF

cat > "$target_dir/.gitignore" <<'EOF'
pkg/
src/
*.log
*.pkg.tar.*
*.src.tar.*
bigkis-linux-amd64
upstream-LICENSE
EOF

cat > "$target_dir/LICENSE" <<'EOF'
Zero Clause BSD

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH
REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND
FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT,
INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM
LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR
OTHER TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR
PERFORMANCE OF THIS SOFTWARE.
EOF
