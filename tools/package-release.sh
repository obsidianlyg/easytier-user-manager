#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
version_output="$(go run . --version)"
release_version="${version_output#easytier-access }"
if [[ ! "$release_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo 'Cannot determine application version.' >&2
  exit 1
fi
if [[ -n "${RELEASE_VERSION:-}" && "$RELEASE_VERSION" != "$release_version" ]]; then
  echo "Tag version $RELEASE_VERSION differs from application version $release_version." >&2
  exit 1
fi

artifact_dir="$repo_root/dist/releases"
mkdir -p "$artifact_dir"
package_dir="$(mktemp -d "$repo_root/dist/release-stage.XXXXXX")"
trap 'rm -rf "$package_dir"' EXIT
common_files=(
  README.md config.example.json THIRD_PARTY_LICENSES.txt
  deploy/config.production.example.json
  deploy/easytier-access.service deploy/core.example.toml
)
for entry in "${common_files[@]}"; do
  mkdir -p "$package_dir/$(dirname "$entry")"
  cp "$entry" "$package_dir/$entry"
done

archives=()
for platform in linux-amd64 linux-arm64 darwin-arm64; do
  archive="easytier-access-$release_version-$platform.tar.gz"
  cp "dist/$platform/easytier-access" "$package_dir/easytier-access"
  # Explicit files prevent runtime data or local keys from entering the archive.
  COPYFILE_DISABLE=1 tar -czf "$artifact_dir/$archive" -C "$package_dir" easytier-access "${common_files[@]}"
  archives+=("$archive")
done

cd "$artifact_dir"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "${archives[@]}" > "SHA256SUMS-$release_version"
else
  shasum -a 256 "${archives[@]}" > "SHA256SUMS-$release_version"
fi
printf 'Release packages: %s\n' "$artifact_dir"
