#!/bin/sh
# SPDX-License-Identifier: MIT
# Fedora CI/Release builders may lag upstream Go security updates.
# Verify an exact upstream archive before allowing a build to use it.
set -eu
[ "$#" -eq 0 ] || { echo 'usage: ci-install-pinned-go-fedora.sh' >&2; exit 1; }
[ "$(uname -s)" = Linux ] && [ "$(uname -m)" = x86_64 ] || {
  echo 'unsupported build host: require Linux x86_64' >&2; exit 1;
}
[ -n "${GITHUB_PATH:-}" ] && [ -f "$GITHUB_PATH" ] || {
  echo 'GitHub Actions PATH file is required' >&2; exit 1;
}
for cmd in curl sha256sum tar mktemp; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "missing required command: $cmd" >&2; exit 1; }
done
archive=go1.26.10.linux-amd64.tar.gz
expected=ccfe7ae822d2c2f41f4fe7c956199967a4dac22d0ee0d2b2bfdb440aab3b20c5
install_dir=/opt/noderampart-verified-go-1.26.10
[ ! -e "$install_dir" ] && [ ! -L "$install_dir" ] || {
  echo 'verified toolchain destination must not exist' >&2; exit 1;
}
umask 077
tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT HUP INT TERM
curl --fail --location --silent --show-error --proto '=https' --proto-redir '=https' \
  --tlsv1.2 --connect-timeout 20 --max-time 240 --retry 2 \
  -o "$tmp/$archive" "https://go.dev/dl/$archive"
printf '%s  %s\n' "$expected" "$tmp/$archive" | sha256sum --check --status || {
  echo 'upstream Go toolchain SHA256 mismatch' >&2; exit 1;
}
mkdir -p "$install_dir"
tar -xzf "$tmp/$archive" -C "$install_dir" --no-same-owner
binary="$install_dir/go/bin/go"
[ -x "$binary" ] || { echo 'verified Go binary missing' >&2; exit 1; }
actual=$(GOTOOLCHAIN=local "$binary" version)
[ "$actual" = 'go version go1.26.10 linux/amd64' ] || {
  echo "unexpected pinned compiler identity: $actual" >&2; exit 1;
}
printf '%s\n' "$install_dir/go/bin" >> "$GITHUB_PATH"
printf 'Fedora verified upstream Go compiler PASS: %s\n' "$actual"
