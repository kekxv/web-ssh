//go:build !windows

package handlers

import (
	"os"
	"testing"
)

func TestLocalShellCommandDoesNotPassLoginFlagToSh(t *testing.T) {
	command, err := localShellCommand("/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := command.Args, []string{"/bin/sh"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestLocalShellCommandUsesLoginFlagForBash(t *testing.T) {
	command, err := localShellCommand("/bin/bash")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := command.Args, []string{"/bin/bash", "--login"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestInstalledUnixShellsExcludesMissingPaths(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	shells := installedUnixShells([]string{"/does/not/exist", executable})

	if got, want := shells, []string{executable}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("shells = %#v, want %#v", got, want)
	}
}
