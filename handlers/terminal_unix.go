//go:build !windows

package handlers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
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
	cmd, err := localShellCommand(shell)
	if err != nil {
		return nil, err
	}

	// 获取当前用户信息，设置正确的 HOME 和工作目录
	currentUser, err := user.Current()
	if err == nil {
		cmd.Dir = currentUser.HomeDir
		// 继承当前环境变量，但确保 HOME 正确
		cmd.Env = append(os.Environ(), "HOME="+currentUser.HomeDir)
	} else {
		// 如果获取用户信息失败，尝试从环境变量获取 HOME
		homeDir := os.Getenv("HOME")
		if homeDir == "" {
			homeDir = "/root"
		}
		cmd.Dir = homeDir
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	return &unixPTY{ptmx}, nil
}

func localShellCommand(shell string) (*exec.Cmd, error) {
	if shell == "" {
		for _, candidate := range defaultUnixShells() {
			if _, err := exec.LookPath(candidate); err == nil {
				shell = candidate
				break
			}
		}
	}
	if shell == "" {
		return nil, fmt.Errorf("no supported shell is available")
	}

	path, err := exec.LookPath(shell)
	if err != nil {
		return nil, fmt.Errorf("shell %q is not available: %w", shell, err)
	}
	if base := filepath.Base(path); base == "bash" || base == "zsh" {
		return exec.Command(path, "--login"), nil
	}
	return exec.Command(path), nil
}

func GetAvailableShells(w http.ResponseWriter, r *http.Request) {
	currentShell := os.Getenv("SHELL")
	if currentShell == "" {
		currentShell = "/bin/zsh"
	}

	file, err := os.Open("/etc/shells")
	if err != nil {
		shells := installedUnixShells(defaultUnixShells())
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"shells":        shells,
			"current_shell": preferredUnixShell(shells, currentShell),
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
	shells = installedUnixShells(shells)
	if len(shells) == 0 {
		shells = installedUnixShells(defaultUnixShells())
	}
	sortUnixShells(shells)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"shells":        shells,
		"current_shell": preferredUnixShell(shells, currentShell),
	})
}

func installedUnixShells(shells []string) []string {
	installed := make([]string, 0, len(shells))
	seen := make(map[string]bool)
	for _, shell := range shells {
		if shell == "" || seen[shell] {
			continue
		}
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		seen[shell] = true
		installed = append(installed, shell)
	}
	return installed
}

func defaultUnixShells() []string {
	return []string{"/bin/zsh", "/bin/bash", "/bin/sh"}
}

func sortUnixShells(shells []string) {
	sort.SliceStable(shells, func(i, j int) bool {
		return shellPriority(shells[i]) < shellPriority(shells[j])
	})
}

func preferredUnixShell(shells []string, currentShell string) string {
	priorities := []string{"zsh", "bash", "sh"}
	for _, name := range priorities {
		for _, shell := range shells {
			if shellBaseName(shell) == name {
				return shell
			}
		}
	}
	for _, shell := range shells {
		if shell == currentShell {
			return shell
		}
	}
	if len(shells) > 0 {
		return shells[0]
	}
	return currentShell
}

func shellPriority(shell string) int {
	switch shellBaseName(shell) {
	case "zsh":
		return 0
	case "bash":
		return 1
	case "sh":
		return 2
	default:
		return 3
	}
}

func shellBaseName(shell string) string {
	parts := strings.Split(shell, "/")
	if len(parts) == 0 {
		return shell
	}
	return parts[len(parts)-1]
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
