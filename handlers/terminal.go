package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
	"web-ssh/models"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// TerminalHandler handles WebSocket terminal connections
type TerminalHandler struct {
	sessionManager *SSHSessionManager
	sessions       map[string]*TerminalSession
	mu             sync.RWMutex
	localSessions  map[string]*LocalPTYSession
}

// LocalPTYSession represents a local PTY session
type LocalPTYSession struct {
	ID       string
	PTY      PTY
	Cmd      *exec.Cmd
	mu       sync.Mutex
}

// TerminalSession represents a terminal WebSocket session
type TerminalSession struct {
	ID        string
	WS        *websocket.Conn
	SSHSession *ssh.Session
	SSHClient  *ssh.Client
	LocalProc  PTY
	mu         sync.Mutex
}

// NewTerminalHandler creates a new terminal handler
func NewTerminalHandler(sm *SSHSessionManager) *TerminalHandler {
	return &TerminalHandler{
		sessionManager: sm,
		sessions:       make(map[string]*TerminalSession),
		localSessions:  make(map[string]*LocalPTYSession),
	}
}

// HandleTerminal upgrades HTTP connection to WebSocket and handles terminal I/O
func (h *TerminalHandler) HandleTerminal(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	mode := r.URL.Query().Get("mode") // "ssh" or "local"
	username := r.URL.Query().Get("username") // optional username for local login

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	ts := &TerminalSession{
		ID: sessionID,
		WS: conn,
	}

	h.mu.Lock()
	h.sessions[sessionID] = ts
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.sessions, sessionID)
		h.mu.Unlock()
		ts.Close()
	}()

	log.Printf("Terminal connected: %s (mode: %s, user: %s)", sessionID, mode, username)

	if mode == "local" {
		h.handleLocalBash(ts, username)
	} else {
		h.handleSSHSession(ts, sessionID)
	}
}

func (h *TerminalHandler) handleSSHSession(ts *TerminalSession, sessionID string) {
	sshSession, ok := h.sessionManager.GetSession(sessionID)
	if !ok {
		ts.SendError("SSH session not found")
		return
	}

	sshClient := sshSession.Client

	// Create SSH session
	session, err := sshClient.NewSession()
	if err != nil {
		ts.SendError(fmt.Sprintf("Failed to create SSH session: %v", err))
		return
	}
	ts.SSHSession = session
	ts.SSHClient = sshClient

	// Set up terminal modes for full terminal support
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
		ssh.ICRNL:         0,
		ssh.INLCR:         0,
		ssh.IGNCR:         0,
	}

	err = session.RequestPty("xterm-256color", 80, 24, modes)
	if err != nil {
		ts.SendError(fmt.Sprintf("Failed to request PTY: %v", err))
		return
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		ts.SendError(fmt.Sprintf("Failed to get stdin: %v", err))
		return
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		ts.SendError(fmt.Sprintf("Failed to get stdout: %v", err))
		return
	}

	err = session.Shell()
	if err != nil {
		ts.SendError(fmt.Sprintf("Failed to start shell: %v", err))
		return
	}

	// Start goroutines for I/O
	go h.wsToSSH(stdin, ts)
	go h.sshToWS(stdout, ts)

	// Keep connection alive
	h.keepAlive(ts)
}

func (h *TerminalHandler) wsToSSH(writer io.Writer, ts *TerminalSession) {
	for {
		_, message, err := ts.WS.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket read error: %v", err)
			}
			return
		}

		var msg models.TerminalMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "input":
			writer.Write([]byte(msg.Data))
		case "resize":
			if ts.SSHSession != nil {
				ts.SSHSession.WindowChange(msg.Rows, msg.Cols)
			}
		}
	}
}

func (h *TerminalHandler) sshToWS(reader io.Reader, ts *TerminalSession) {
	buf := make([]byte, 8192)
	for {
		n, err := reader.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("SSH read error: %v", err)
			}
			return
		}
		if n > 0 {
			// Use BinaryMessage to support raw terminal output including vim
			ts.WS.WriteMessage(websocket.BinaryMessage, buf[:n])
		}
	}
}

func (h *TerminalHandler) handleLocalBash(ts *TerminalSession, username string) {
	// Use platform-specific startLocalShell
	proc, err := startLocalShell(username)
	if err != nil {
		ts.SendError(fmt.Sprintf("Failed to start local shell: %v", err))
		return
	}

	ts.LocalProc = proc

	// Start goroutines for I/O
	go h.wsToLocalProc(proc, ts)
	go h.localProcToWS(proc, ts)

	h.keepAlive(ts)
}

func (h *TerminalHandler) wsToLocalProc(writer PTY, ts *TerminalSession) {
	for {
		_, message, err := ts.WS.ReadMessage()
		if err != nil {
			return
		}

		var msg models.TerminalMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "input":
			writer.Write([]byte(msg.Data))
		case "resize":
			// Handle resize for local bash
			if writer != nil {
				writer.Resize(uint16(msg.Rows), uint16(msg.Cols))
			}
		}
	}
}

func (h *TerminalHandler) localProcToWS(reader io.Reader, ts *TerminalSession) {
	buf := make([]byte, 8192)
	for {
		n, err := reader.Read(buf)
		if err != nil {
			return
		}
		if n > 0 {
			// Use BinaryMessage to support raw terminal output
			ts.WS.WriteMessage(websocket.BinaryMessage, buf[:n])
		}
	}
}

func (h *TerminalHandler) keepAlive(ts *TerminalSession) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		err := ts.WS.WriteMessage(websocket.PingMessage, []byte{})
		if err != nil {
			return
		}
	}
}

func (ts *TerminalSession) SendError(msg string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	errMsg := map[string]string{"type": "error", "message": msg}
	data, _ := json.Marshal(errMsg)
	ts.WS.WriteMessage(websocket.TextMessage, data)
}

func (ts *TerminalSession) Close() error {
	if ts.SSHSession != nil {
		ts.SSHSession.Close()
	}
	if ts.SSHClient != nil {
		ts.SSHClient.Close()
	}
	if ts.LocalProc != nil {
		ts.LocalProc.Close()
	}
	if ts.WS != nil {
		ts.WS.Close()
	}
	return nil
}

// GetCurrentUser returns the current system user info
func GetCurrentUser(w http.ResponseWriter, r *http.Request) {
	user, err := user.Current()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	hostname, _ := os.Hostname()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"username": user.Username,
		"uid":      user.Uid,
		"gid":      user.Gid,
		"home":     user.HomeDir,
		"hostname": hostname,
	})
}

// GetPublicKey returns the RSA public key for password encryption
func GetPublicKey(w http.ResponseWriter, r *http.Request) {
	pubDER, err := GetPublicKeyPEM()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	// Return as base64 encoded DER
	pubBase64 := base64.StdEncoding.EncodeToString(pubDER)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"public_key": pubBase64,
	})
}

// ConnectSSH handles SSH connection requests
func ConnectSSH(w http.ResponseWriter, r *http.Request, sm *SSHSessionManager) string {
	var config models.SSHConnectionConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		log.Printf("Failed to decode request: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return ""
	}

	log.Printf("Received config: host=%s, port=%d, user=%s", config.Host, config.Port, config.Username)

	// Get web username from session
	var webUsername string
	cookie, err := r.Cookie("session_id")
	if err == nil {
		auth := GetAuthManager()
		if session, ok := auth.GetSession(cookie.Value); ok {
			webUsername = session.Username
		}
	}

	clients, err := CreateSSHClient(&config)
	if err != nil {
		log.Printf("Failed to create SSH client: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return ""
	}

	targetClient := clients[len(clients)-1]
	jumpClients := clients[:len(clients)-1]

	sessionID := generateSessionID()
	session := &SSHSession{
		ID:          sessionID,
		Username:    webUsername,
		Config:      &config,
		Client:      targetClient,
		JumpClients: jumpClients,
		LastActive:  time.Now(),
	}

	sm.AddSession(sessionID, session)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"session_id": sessionID})
	return sessionID
}

// LocalSessionRequest handles local bash session creation with login
func LocalSessionRequest(w http.ResponseWriter, r *http.Request, h *TerminalHandler) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := generateSessionID()

	// Use platform-specific startLocalShell
	ptmx, err := startLocalShell("")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to start shell: %v", err), http.StatusInternalServerError)
		return
	}

	session := &LocalPTYSession{
		ID:  sessionID,
		PTY: ptmx,
		Cmd: nil, // PTY started process
	}

	h.mu.Lock()
	h.localSessions[sessionID] = session
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"session_id": sessionID,
		"message":    "Local shell session created.",
	})
}

// LocalSessionRead handles reading output from local bash (HTTP long polling)
func LocalSessionRead(w http.ResponseWriter, r *http.Request, h *TerminalHandler) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "session_id required", http.StatusBadRequest)
		return
	}

	h.mu.RLock()
	session, ok := h.localSessions[sessionID]
	h.mu.RUnlock()

	if !ok {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Set timeout for long polling
	timeout := time.After(30 * time.Second)
	done := make(chan struct{})

	var buf [4096]byte
	var n int
	var readErr error

	go func() {
		n, readErr = session.PTY.Read(buf[:])
		close(done)
	}()

	select {
	case <-done:
		if n > 0 {
			// Encode as base64
			encoded := base64.StdEncoding.EncodeToString(buf[:n])
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"data": encoded,
				"type": "output",
			})
		}
		if readErr != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"type": "close",
			})
		}
	case <-timeout:
		// Send empty response to continue polling
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"type": "timeout",
		})
	}
}

// LocalSessionWrite handles writing input to local bash
func LocalSessionWrite(w http.ResponseWriter, r *http.Request, h *TerminalHandler) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "session_id required", http.StatusBadRequest)
		return
	}

	h.mu.RLock()
	session, ok := h.localSessions[sessionID]
	h.mu.RUnlock()

	if !ok {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	var msg models.TerminalMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch msg.Type {
	case "input":
		session.PTY.Write([]byte(msg.Data))
	case "resize":
		if session.PTY != nil {
			session.PTY.Resize(uint16(msg.Rows), uint16(msg.Cols))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// LocalSessionClose handles closing local bash session
func LocalSessionClose(w http.ResponseWriter, r *http.Request, h *TerminalHandler) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "session_id required", http.StatusBadRequest)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if session, ok := h.localSessions[sessionID]; ok {
		if session.PTY != nil {
			session.PTY.Close()
		}
		if session.Cmd != nil && session.Cmd.Process != nil {
			session.Cmd.Process.Kill()
		}
		delete(h.localSessions, sessionID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// LocalFileList handles local file listing
func LocalFileList(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" || path == "." {
		// 默认使用用户 home 目录
		homeDir, err := os.UserHomeDir()
		if err != nil {
			if runtime.GOOS == "windows" {
				homeDir = "C:\\"
			} else {
				homeDir = "/"
			}
		}
		path = homeDir
	} else if path == "~" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			if runtime.GOOS == "windows" {
				homeDir = "C:\\"
			} else {
				homeDir = "/"
			}
		}
		path = homeDir
	}

	// 转为绝对路径
	absPath, err := filepath.Abs(path)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	var files []models.SFTPFile
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, models.SFTPFile{
			Name:    entry.Name(),
			Size:    info.Size(),
			Mode:    info.Mode().String(),
			ModTime: info.ModTime().Unix(),
			IsDir:   entry.IsDir(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": files, "path": absPath})
}

// LocalFileDownload handles local file download
func LocalFileDownload(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	file, err := os.Open(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if info.IsDir() {
		http.Error(w, "cannot download directory", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filepath.Base(path)))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	io.Copy(w, file)
}

// LocalFileUpload handles local file upload
func LocalFileUpload(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	err := r.ParseMultipartForm(100 << 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	dst, err := os.Create(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	io.Copy(dst, file)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// LocalFileMkdir handles local directory creation
func LocalFileMkdir(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	err := os.MkdirAll(path, 0755)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// LocalFileRemove handles local file/directory removal
func LocalFileRemove(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	var removeErr error
	if info.IsDir() {
		removeErr = os.RemoveAll(path)
	} else {
		removeErr = os.Remove(path)
	}

	if removeErr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": removeErr.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// LocalFilePwd handles current directory request
func LocalFilePwd(w http.ResponseWriter, r *http.Request) {
	path, err := os.Getwd()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"path": path})
}

// LocalFileCd handles change directory (just returns the new path)
func LocalFileCd(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path, _ = os.UserHomeDir()
	}

	_, err := os.Stat(path)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"path": path})
}
