#!/bin/bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$script_dir/.." && pwd)"
out="${NEKPO_ARTIFACTS:-$root/nekpo/nekpo_artifacts}"
changelog_file="$root/changelog.md"
conf_file="$script_dir/pkg_meta.conf"
pkg_script="$script_dir/package.sh"

mkdir -p "$out"
cd "$root/src"

go build -o "$out/nekpo" ./cmd/nekpo

[[ -f "$changelog_file" ]] || { echo "Error: Changelog not found: $changelog_file" >&2; exit 1; }
[[ -x "$pkg_script" ]] || { echo "Error: Package script not executable: $pkg_script" >&2; exit 1; }
[[ -f "$conf_file" ]] || { echo "Error: Config file not found: $conf_file" >&2; exit 1; }

top_version="$(grep -m 1 -E '^#[[:space:]]*v?[0-9]+' "$changelog_file" | sed -E 's/^#[[:space:]]*v?//; s/[[:space:]].*$//')"
[[ -n "$top_version" ]] || { echo "Error: Could not parse version from $changelog_file" >&2; exit 1; }

highest_version="$(
  {
    printf '%s\n' "$top_version"
    grep -E '^#[[:space:]]*v?[0-9]+' "$changelog_file" | sed -E 's/^#[[:space:]]*v?//; s/[[:space:]].*$//'
  } | sort -V | tail -n 1
)"

if [[ "$top_version" != "$highest_version" ]]; then
  echo "Error: Top changelog version ($top_version) is not the highest version ($highest_version)." >&2
  exit 1
fi

"$pkg_script" "$out/nekpo" "$out" "$conf_file" "$top_version"
rm "$out/nekpo"
