#!/bin/sh
# Updates ./nightshift to the latest release and pulls the latest agent image (docker mode).
# Run it in the folder that holds nightshift and env.json.  Config, tokens and data/ are left untouched.
# Usage: curl -fsSL https://raw.githubusercontent.com/NPetersenDK/nightshift/main/update.sh | sh
set -e
[ -f ./nightshift ] || { echo "no ./nightshift here, use install.sh first" >&2; exit 1; }
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
esac
case "$os" in linux|darwin) ;; *) echo "unsupported OS: $os" >&2; exit 1 ;; esac
old=$(./nightshift version 2>/dev/null || echo unknown)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
file="nightshift_${os}_${arch}.tar.gz"
curl -fsSL "https://github.com/NPetersenDK/nightshift/releases/latest/download/$file" -o "$tmp/$file"
tar -xzf "$tmp/$file" -C "$tmp" nightshift
chmod +x "$tmp/nightshift"
# rename, not overwrite: a run in progress keeps its old binary and the next run gets the new one
mv -f "$tmp/nightshift" ./nightshift
echo "nightshift $old -> $(./nightshift version)"
if command -v docker >/dev/null 2>&1; then
  docker pull -q ghcr.io/npetersendk/nightshift-agent:latest
fi
