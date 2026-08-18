#!/bin/sh
set -eu

if [ ! -f VERSION ]; then
    echo "VERSION file is required" >&2
    exit 1
fi

if [ -z "${GITHUB_OUTPUT:-}" ]; then
    echo "GITHUB_OUTPUT is required" >&2
    exit 1
fi

version=$(tr -d '[:space:]' < VERSION)
if ! printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$'; then
    echo "VERSION must be a semantic version, got: $version" >&2
    exit 1
fi

tag="v$version"
tag_created=false
should_release=false

if [ "${GITHUB_EVENT_NAME:-}" = "push" ] && { [ "${GITHUB_REF:-}" = "refs/heads/main" ] || [ "${GITHUB_REF:-}" = "refs/heads/master" ]; }; then
    if ! git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
        git config user.name "github-actions[bot]"
        git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
        git tag -a "$tag" -m "Release $tag"
        git push origin "refs/tags/$tag"
        tag_created=true
        should_release=true
    fi
elif [ "${GITHUB_EVENT_NAME:-}" = "push" ] && [ "${GITHUB_REF:-}" = "refs/tags/$tag" ]; then
    should_release=true
fi

{
    printf 'version=%s\n' "$version"
    printf 'tag=%s\n' "$tag"
    printf 'tag_created=%s\n' "$tag_created"
    printf 'should_release=%s\n' "$should_release"
} >> "$GITHUB_OUTPUT"
