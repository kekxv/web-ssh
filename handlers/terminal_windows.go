//go:build windows

package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"

	"github.com/UserExistsError/conpty"
)

// windowsPTY handles both ConPTY and fallback pipe mode
type windowsPTY struct {
	// ConPTY mode
	cpty *conpty.ConPty

	// Fallback mode
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cmd    *exec.Cmd
}

func (p *windowsPTY) Read(b []byte) (int, error) {
	if p.cpty != nil {
		return p.cpty.Read(b)
	}
	return p.stdout.Read(b)
}

func (p *windowsPTY) Write(b []byte) (int, error) {
	if p.cpty != nil {
		return p.cpty.Write(b)
	}
	return p.stdin.Write(b)
}

func (p *windowsPTY) Close() error {
	if p.cpty != nil {
		return p.cpty.Close()
	}
	p.stdin.Close()
	p.stdout.Close()
	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
	return nil
}

func (p *windowsPTY) Resize(rows, cols uint16) error {
	if p.cpty != nil {
		return p.cpty.Resize(int(cols), int(rows))
	}
	// Fallback mode doesn't support resize
	return nil
}

func startLocalShell(shell string) (PTY, error) {
	if shell == "" {
		shell = "powershell.exe"
	}

	// 1. Try ConPTY first (Windows 10 1809+)
	cpty, err := conpty.Start(shell)
	if err == nil {
		return &windowsPTY{cpty: cpty}, nil
	}

	// 2. Fallback to Pipe mode for older Windows or if ConPTY fails
	cmd := exec.Command(shell)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stdout

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &windowsPTY{
		stdin:  stdin,
		stdout: stdout,
		cmd:    cmd,
	}, nil
}

func GetAvailableShells(w http.ResponseWriter, r *http.Request) {
	// PowerShell preferred; fall back to cmd
	shells := []string{"powershell.exe", "cmd.exe"}
	currentShell := "powershell.exe"
	// Check if powershell is actually available
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		currentShell = os.Getenv("COMSPEC")
		if currentShell == "" {
			currentShell = "cmd.exe"
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"shells":        shells,
		"current_shell": currentShell,
	})
}

func GetSystemUsers(w http.ResponseWriter, r *http.Request) {
	username := os.Getenv("USERNAME")
	if username == "" {
		username = "User"
	}
	
	users := []map[string]string{
		{
			"username": username,
			"shell":    "powershell.exe",
			"home":     os.Getenv("USERPROFILE"),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"os":    runtime.GOOS,
		"arch":  runtime.GOARCH,
		"users": users,
	})
}
