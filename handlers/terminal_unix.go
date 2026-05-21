//go:build !windows

package handlers

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/creack/pty"
)

type unixPTY struct {
	*os.File
}

func (p *unixPTY) Resize(rows, cols uint16) error {
	return pty.Setsize(p.File, &pty.Winsize{Rows: rows, Cols: cols})
}

func startLocalShell(shell string) (PTY, error) {
	if shell == "" {
		shell = "bash"
	}
	cmd := exec.Command(shell, "--login")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	return &unixPTY{ptmx}, nil
}

func GetAvailableShells(w http.ResponseWriter, r *http.Request) {
	currentShell := os.Getenv("SHELL")
	if currentShell == "" {
		currentShell = "/bin/bash"
	}

	file, err := os.Open("/etc/shells")
	if err != nil {
		shells := []string{"/bin/bash", "/bin/zsh", "/bin/sh"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"shells":        shells,
			"current_shell": currentShell,
		})
		return
	}
	defer file.Close()

	var shells []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		shells = append(shells, line)
	}
	if len(shells) == 0 {
		shells = []string{"/bin/bash", "/bin/zsh", "/bin/sh"}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"shells":        shells,
		"current_shell": currentShell,
	})
}

func GetSystemUsers(w http.ResponseWriter, r *http.Request) {
	file, err := os.Open("/etc/passwd")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}
	defer file.Close()

	var users []map[string]string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, ":")
		if len(parts) < 7 {
			continue
		}

		username := parts[0]
		shell := parts[6]

		// Only return users with valid login shells
		if shell != "/usr/sbin/nologin" && shell != "/bin/false" && shell != "/sbin/nologin" && shell != "" {
			users = append(users, map[string]string{
				"username": username,
				"shell":    shell,
				"home":     parts[5],
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"os":    runtime.GOOS,
		"arch":  runtime.GOARCH,
		"users": users,
	})
}
