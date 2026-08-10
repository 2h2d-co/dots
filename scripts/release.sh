#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
	echo "usage: $0 VERSION" >&2
	exit 2
fi

version=$1
if [[ ! $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
	echo "release version must be a semantic version" >&2
	exit 1
fi

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
tag="v$version"
prerelease=false
if [[ $version == *-* ]]; then
	prerelease=true
fi

if [[ $(git branch --show-current) != main ]]; then
	echo "releases must be created from main" >&2
	exit 1
fi
if [[ -n $(git status --porcelain=v1) ]]; then
	echo "working tree must be clean before creating a release" >&2
	exit 1
fi

git fetch --quiet --tags origin main
if [[ $(git rev-parse HEAD) != "$(git rev-parse origin/main)" ]]; then
	echo "HEAD must match origin/main before creating a release" >&2
	exit 1
fi
if git rev-parse --quiet --verify "refs/tags/$tag" >/dev/null; then
	echo "tag $tag already exists" >&2
	exit 1
fi
if git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
	echo "remote tag $tag already exists" >&2
	exit 1
fi

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

if [[ $prerelease == false ]]; then
	unreleased=$(awk '
		/^## Unreleased$/ { found = 1; next }
		found && /^## / { exit }
		found { print }
	' CHANGELOG.md)
	if [[ -z ${unreleased//[[:space:]]/} ]]; then
		echo "CHANGELOG.md Unreleased section is empty" >&2
		exit 1
	fi
	release_date=$(date -u +%F)
	awk -v heading="## [$version] - $release_date" '
		!inserted && /^## Unreleased$/ {
			print
			print ""
			print heading
			inserted = 1
			next
		}
		{ print }
		END {
			if (!inserted) {
				exit 1
			}
		}
	' CHANGELOG.md >"$tmpdir/CHANGELOG.md"
	mv "$tmpdir/CHANGELOG.md" CHANGELOG.md
	git add CHANGELOG.md
fi

staged_files=$(git diff --cached --name-only)
if [[ $prerelease == true && -n $staged_files ]]; then
	echo "prerelease metadata changed unexpected files: $staged_files" >&2
	exit 1
fi
if [[ $prerelease == false && $staged_files != CHANGELOG.md ]]; then
	echo "stable release metadata changed unexpected files: $staged_files" >&2
	exit 1
fi

go_bin=$(mise which go)
goreleaser_bin=$(mise which goreleaser)

build_from_index() {
	local destination=$1
	local source="$tmpdir/source"
	rm -rf "$source"
	mkdir -p "$source"
	git checkout-index --all --force --prefix="$source/"
	(
		cd "$source"
		git init --quiet
		git config user.name "Release build"
		git config user.email "release-build@invalid"
		git add --all
		git -c commit.gpgsign=false commit --quiet -m "release build source"
		git remote add origin https://github.com/2h2d-co/dots.git
		git -c tag.gpgSign=false tag "$tag"
		PATH="$(dirname "$go_bin"):$PATH" \
			RELEASE_GORELEASER_PATH="$goreleaser_bin" \
			scripts/package-release.sh "$version" "$destination"
	)
}

local_output="$tmpdir/local"
local_digest=$(build_from_index "$local_output")

git commit --allow-empty -S \
	-m "release: $tag" \
	-m "Release-Manifest-SHA256: $local_digest"

release_commit=$(git rev-parse HEAD)
git -c gpg.format=ssh \
	-c gpg.ssh.allowedSignersFile=.github/release-signers \
	verify-commit "$release_commit"
signed_digest=$(git log -1 \
	--format='%(trailers:key=Release-Manifest-SHA256,valueonly)' \
	"$release_commit")
if [[ $signed_digest != "$local_digest" ]]; then
	echo "signed release manifest digest does not match the local artifacts" >&2
	exit 1
fi

committed_output="$tmpdir/committed"
committed_digest=$(build_from_index "$committed_output")
if [[ $committed_digest != "$local_digest" ]]; then
	echo "Go release artifacts are not reproducible" >&2
	exit 1
fi

git tag "$tag"
if [[ $(git cat-file -t "refs/tags/$tag") != commit ]]; then
	echo "release tag $tag must be a lightweight tag" >&2
	exit 1
fi

echo "created signed release commit $release_commit"
echo "created lightweight tag $tag"
echo "locally attested release manifest SHA-256: $local_digest"
echo "push with: git push --atomic origin main $tag"
