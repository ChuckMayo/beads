#!/usr/bin/env bash

set -euo pipefail

repo="ChuckMayo/beads"
version="${BD_FORK_VERSION:-v1.2.2-chuck.1}"
install_dir="${BD_INSTALL_DIR:-${HOME}/.local/bin}"

case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *) echo "Unsupported operating system: $(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  arm64|aarch64) arch="arm64" ;;
  x86_64|amd64) arch="amd64" ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

release_version="${version#v}"
archive="beads_${release_version}_${os}_${arch}.tar.gz"
base_url="https://github.com/${repo}/releases/download/${version}"
temp_dir="$(mktemp -d)"
trap 'rm -rf "$temp_dir"' EXIT

curl -fsSL "${base_url}/${archive}" -o "${temp_dir}/${archive}"
curl -fsSL "${base_url}/checksums.txt" -o "${temp_dir}/checksums.txt"

expected="$(awk -v file="$archive" '$2 == file { print $1 }' "${temp_dir}/checksums.txt")"
if [ -z "$expected" ]; then
  echo "No checksum found for ${archive}" >&2
  exit 1
fi

if command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "${temp_dir}/${archive}" | awk '{ print $1 }')"
else
  actual="$(sha256sum "${temp_dir}/${archive}" | awk '{ print $1 }')"
fi

if [ "$actual" != "$expected" ]; then
  echo "Checksum mismatch for ${archive}" >&2
  exit 1
fi

mkdir -p "$install_dir"
tar -xzf "${temp_dir}/${archive}" -C "$temp_dir"
install -m 0755 "${temp_dir}/bd" "${install_dir}/bd"

echo "Installed ${repo} ${version} to ${install_dir}/bd"
"${install_dir}/bd" version
