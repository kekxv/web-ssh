# Download Snapshot Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every file-download endpoint return exactly the byte count advertised at the start of the download, even if the source file grows while it is being read.

**Architecture:** Download handlers retain their initial `Stat` result as the snapshot size. A package-local helper copies at most that many bytes from any `io.Reader`; the local filesystem, SFTP, and remote-proxy handlers delegate to it instead of unbounded `io.Copy`. A focused unit test supplies bytes beyond the snapshot boundary and asserts that the response payload stops exactly at that boundary.

**Tech Stack:** Go 1.25, `net/http`, `io`, Go standard testing package, `github.com/pkg/sftp`.

## Global Constraints

- Keep the initial `Content-Length` as the download snapshot size.
- Do not buffer download bodies in memory.
- Preserve existing response headers and error logging behavior.
- Apply the same bounded-copy behavior to local, SFTP, and remote-proxy downloads.

---

### Task 1: Bound download body streaming to the initial snapshot

**Files:**
- Create: `handlers/file_transfer_test.go`
- Modify: `handlers/file_transfer.go`
- Modify: `handlers/terminal.go:647`
- Modify: `handlers/sftp.go:151`
- Modify: `handlers/remote.go:415`

**Interfaces:**
- Consumes: an `io.Reader` positioned at the beginning of a file and its initial `int64` size from `Stat`.
- Produces: `copyDownloadSnapshot(dst io.Writer, src io.Reader, size int64) error`, which writes no more than `size` bytes and returns an error when the first `size` bytes cannot be read.

- [x] **Step 1: Write the failing test**

```go
func TestCopyDownloadSnapshotStreamsOnlyAdvertisedBytes(t *testing.T) {
    var body bytes.Buffer

    err := copyDownloadSnapshot(&body, strings.NewReader("snapshot-appended-after-start"), int64(len("snapshot")))

    if err != nil {
        t.Fatalf("copyDownloadSnapshot returned an error: %v", err)
    }
    if got, want := body.String(), "snapshot"; got != want {
        t.Fatalf("download body = %q, want %q", got, want)
    }
}
```

- [x] **Step 2: Run the focused test to verify it fails**

Run: `/usr/local/go/bin/go test ./handlers -run '^TestCopyDownloadSnapshotStreamsOnlyAdvertisedBytes$' -count=1`

Expected: FAIL because `copyDownloadSnapshot` is undefined.

- [x] **Step 3: Implement the bounded streaming helper and call it from all download paths**

```go
func copyDownloadSnapshot(dst io.Writer, src io.Reader, size int64) error {
    _, err := io.CopyN(dst, src, size)
    return err
}
```

Replace each unbounded `io.Copy(w, body)` call in `LocalFileDownload`, `SFTPHandler.HandleDownload`, and `HandleRemoteFileDownload` with this helper and the corresponding initial size: `info.Size()` locally/SFTP, and parsed `Content-Length` for the remote proxy.

- [x] **Step 4: Run focused and package tests to verify they pass**

Run: `/usr/local/go/bin/go test ./handlers -count=1`

Expected: PASS with `TestCopyDownloadSnapshotStreamsOnlyAdvertisedBytes` passing.

- [x] **Step 5: Run the repository test suite and inspect the diff**

Run: `/usr/local/go/bin/go test ./... -count=1 && git diff --check && git diff --check main...HEAD`

Expected: all tests pass and both diff checks produce no output.

- [x] **Step 6: Commit the implementation**

```bash
git add handlers/file_transfer.go handlers/file_transfer_test.go handlers/terminal.go handlers/sftp.go handlers/remote.go docs/superpowers/plans/2026-08-07-download-snapshot.md
git commit -m "fix: bound downloads to initial file size"
```

## Self-Review

- Spec coverage: Task 1 adds an automated regression test and bounds all three download paths to their initial advertised size.
- Placeholder scan: no deferred work or unspecified implementation remains.
- Type consistency: every call uses `copyDownloadSnapshot(io.Writer, io.Reader, int64) error`.
