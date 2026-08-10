#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
	echo "usage: $0 VERSION OUTPUT_DIRECTORY" >&2
	exit 2
fi

version=$1
output=$2
if [[ ! $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
	echo "release version must be a semantic version" >&2
	exit 1
fi

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
tag="v$version"
if [[ $(git cat-file -t "refs/tags/$tag") != commit ]]; then
	echo "release tag $tag must be a lightweight tag" >&2
	exit 1
fi
if [[ $(git rev-parse "refs/tags/$tag^{commit}") != "$(git rev-parse HEAD)" ]]; then
	echo "release tag $tag must point to HEAD" >&2
	exit 1
fi

if [[ -e $output && -n $(find "$output" -mindepth 1 -maxdepth 1 -print -quit) ]]; then
	echo "release output directory must be empty: $output" >&2
	exit 1
fi
mkdir -p "$output"
output=$(cd "$output" && pwd)

goreleaser_bin=${RELEASE_GORELEASER_PATH:-goreleaser}
"$goreleaser_bin" release --clean --skip=publish >&2

archives=(
	"dots_${version}_darwin_arm64.tar.gz"
	"dots_${version}_linux_amd64.tar.gz"
	"dots_${version}_linux_arm64.tar.gz"
)
for archive in "${archives[@]}"; do
	if [[ ! -f "dist/$archive" ]]; then
		echo "missing Go release archive: $archive" >&2
		exit 1
	fi
	cp "dist/$archive" "$output/$archive"
done
if [[ ! -f dist/checksums.txt ]]; then
	echo "missing GoReleaser checksums.txt" >&2
	exit 1
fi

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
(
	cd dist
	shasum -a 256 "${archives[@]}"
) >"$tmpdir/expected-checksums.txt"
if ! cmp "$tmpdir/expected-checksums.txt" dist/checksums.txt; then
	echo "GoReleaser checksums.txt does not match the release archives" >&2
	exit 1
fi
cp dist/checksums.txt "$output/checksums.txt"

expected_archive_files=$(printf '%s\n' \
	"LICENSE" \
	"README.md" \
	"docs/man/dots-add.1" \
	"docs/man/dots-apply.1" \
	"docs/man/dots-completion.1" \
	"docs/man/dots-diff.1" \
	"docs/man/dots-doctor.1" \
	"docs/man/dots-forget.1" \
	"docs/man/dots-init.1" \
	"docs/man/dots-list.1" \
	"docs/man/dots-man.1" \
	"docs/man/dots-reindex.1" \
	"docs/man/dots-status.1" \
	"docs/man/dots-sync.1" \
	"docs/man/dots.1" \
	"dots" \
	"examples/config.toml")
for archive in "${archives[@]}"; do
	actual_archive_files=$(tar -tzf "$output/$archive" | LC_ALL=C sort)
	if [[ $actual_archive_files != "$expected_archive_files" ]]; then
		echo "$archive contains unexpected files" >&2
		exit 1
	fi
done

(
	cd "$output"
	shasum -a 256 checksums.txt "${archives[@]}" >release-manifest.sha256
	shasum -a 256 -c release-manifest.sha256 >&2
)

shasum -a 256 "$output/release-manifest.sha256" | awk '{print $1}'
