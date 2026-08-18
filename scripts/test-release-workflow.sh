#!/bin/sh
set -eu

project_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
prepare_script="$project_dir/scripts/prepare-release.sh"
version=$(tr -d '[:space:]' < "$project_dir/VERSION")
tag="v$version"

if [ ! -f "$prepare_script" ]; then
    echo "missing release preparation script: $prepare_script" >&2
    exit 1
fi

temp_dir=$(mktemp -d)
trap 'rm -rf "$temp_dir"' EXIT HUP INT TERM

git init --bare "$temp_dir/origin.git" >/dev/null
git clone "$temp_dir/origin.git" "$temp_dir/work" >/dev/null
git -C "$temp_dir/work" config user.name "Workflow Test"
git -C "$temp_dir/work" config user.email "workflow-test@example.invalid"
cp "$project_dir/VERSION" "$temp_dir/work/VERSION"
cp "$prepare_script" "$temp_dir/work/prepare-release.sh"
git -C "$temp_dir/work" add VERSION prepare-release.sh
git -C "$temp_dir/work" commit -m "test release preparation" >/dev/null
git -C "$temp_dir/work" branch -M main
git -C "$temp_dir/work" push -u origin main >/dev/null

first_output="$temp_dir/first-output"
(
    cd "$temp_dir/work"
    GITHUB_EVENT_NAME=push GITHUB_REF=refs/heads/main GITHUB_OUTPUT="$first_output" sh ./prepare-release.sh
)
grep -qx "tag=$tag" "$first_output"
grep -qx 'tag_created=true' "$first_output"
grep -qx 'should_release=true' "$first_output"
git -C "$temp_dir/work" ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null

second_output="$temp_dir/second-output"
(
    cd "$temp_dir/work"
    GITHUB_EVENT_NAME=push GITHUB_REF=refs/heads/main GITHUB_OUTPUT="$second_output" sh ./prepare-release.sh
)
grep -qx 'tag_created=false' "$second_output"
grep -qx 'should_release=false' "$second_output"

tag_output="$temp_dir/tag-output"
(
    cd "$temp_dir/work"
    GITHUB_EVENT_NAME=push GITHUB_REF="refs/tags/$tag" GITHUB_OUTPUT="$tag_output" sh ./prepare-release.sh
)
grep -qx 'should_release=true' "$tag_output"

echo "release workflow preparation tests passed"
