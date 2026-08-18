# Release From Version Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create and publish a GitHub release for the `v<VERSION>` tag when a push to `main` or `master` contains a version without a matching tag.

**Architecture:** The build workflow will have a release-preparation job that reads and validates `VERSION`, checks the repository tag list, and creates an annotated tag only when it is absent. The existing build job will depend on that preparation step, and the release job will use its version output so the release runs for both a new version push and a direct version-tag push without creating duplicates.

**Tech Stack:** GitHub Actions, bash, GitHub Actions expressions, git tags, Go/Node verification.

## Global Constraints

- `VERSION` is the canonical release version and is committed with the workflow.
- Tags use the exact format `v<VERSION>`.
- Existing tags must never be recreated or moved.
- The workflow must not use network-hosted runtime assets; release automation may use its existing GitHub Actions dependencies.

---

### Task 1: Add a red regression check for release workflow contracts

**Files:**
- Create: `scripts/test-release-workflow.sh`
- Test: `scripts/test-release-workflow.sh`

**Interfaces:**
- Consumes: `.github/workflows/build.yml` and `VERSION`.
- Produces: exit code 0 only when the workflow reads `VERSION`, checks `refs/tags/v<version>`, conditionally creates an annotated tag, and supplies a release version output.

- [x] **Step 1: Write the failing test**

```sh
grep -F 'version=$(tr -d' .github/workflows/build.yml
grep -F 'refs/tags/v${version}' .github/workflows/build.yml
grep -F 'git tag -a "v${version}"' .github/workflows/build.yml
```

- [x] **Step 2: Run test to verify it fails**

Run: `sh scripts/test-release-workflow.sh`

Expected: FAIL because the workflow has no release-preparation job.

- [x] **Step 3: Write minimal implementation**

Add the reusable version and tag checks to `build.yml`; use `github.token` with `contents: write` permissions and expose the normalized version through job outputs.

- [x] **Step 4: Run test to verify it passes**

Run: `sh scripts/test-release-workflow.sh`

Expected: PASS.

- [x] **Step 5: Commit**

```sh
git add .github/workflows/build.yml VERSION scripts/test-release-workflow.sh
git commit -m "ci: release from VERSION"
```

### Task 2: Verify repository-level integration

**Files:**
- Modify: `.github/workflows/build.yml`
- Modify: `VERSION`
- Test: `scripts/test-release-workflow.sh`

**Interfaces:**
- Consumes: the `release-prep` job output `version`.
- Produces: a release job that calls `softprops/action-gh-release` with `tag_name: v<version>`.

- [x] **Step 1: Verify YAML and shell syntax**

Run: `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/build.yml")'` and `sh -n scripts/test-release-workflow.sh`

Expected: both commands exit 0.

- [x] **Step 2: Verify existing application tests**

Run: `npm ci --offline && node --test static/js/app.test.js && go test ./... -count=1`

Expected: all tests pass.

- [x] **Step 3: Commit verification-ready changes**

```sh
git add docs/superpowers/plans/2026-08-18-release-from-version.md
git commit -m "docs: document version release workflow"
```
