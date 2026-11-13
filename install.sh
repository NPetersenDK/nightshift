#!/bin/sh
# Downloads the latest nightshift release for this OS/arch and unpacks it into the current folder.
# Nothing is installed system-wide.  Usage: curl -fsSL https://raw.githubusercontent.com/NPetersenDK/nightshift/main/install.sh | sh
set -e
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
esac
case "$os" in linux|darwin) ;; *) echo "unsupported OS: $os" >&2; exit 1 ;; esac
file="nightshift_${os}_${arch}.tar.gz"
curl -fsSL "https://github.com/NPetersenDK/nightshift/releases/latest/download/$file" -o "$file"
tar -xzf "$file" nightshift
rm -f "$file"
[ "$os" = darwin ] && xattr -d com.apple.quarantine nightshift 2>/dev/null || true
echo "unpacked ./nightshift"
echo "next: ./nightshift init"
