#!/bin/sh
# Installs the latest nightshift release for this OS/arch.  Usage: curl -fsSL https://raw.githubusercontent.com/NPetersenDK/nightshift/main/install.sh | sh
set -e
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
esac
case "$os" in linux|darwin) ;; *) echo "unsupported OS: $os" >&2; exit 1 ;; esac
dir=${NIGHTSHIFT_INSTALL_DIR:-/usr/local/bin}
[ -w "$dir" ] || { dir="$HOME/.local/bin"; mkdir -p "$dir"; }
tmp=$(mktemp -d)
curl -fsSL "https://github.com/NPetersenDK/nightshift/releases/latest/download/nightshift_${os}_${arch}.tar.gz" | tar -xz -C "$tmp"
install -m 755 "$tmp/nightshift" "$dir/nightshift"
rm -rf "$tmp"
echo "installed $dir/nightshift"
case ":$PATH:" in *":$dir:"*) ;; *) echo "add $dir to your PATH" ;; esac
echo "next: mkdir ~/nightshift && cd ~/nightshift && nightshift init"
