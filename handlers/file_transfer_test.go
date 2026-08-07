package handlers

import (
	"bytes"
	"strings"
	"testing"
)

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
