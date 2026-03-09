package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"web-ssh/models"
)

// SFTPHandler handles SFTP file operations
type SFTPHandler struct {
	sessionManager *SSHSessionManager
	sftpClients    map[string]*sftp.Client
	mu             sync.RWMutex
}

// NewSFTPHandler creates a new SFTP handler
func NewSFTPHandler(sm *SSHSessionManager) *SFTPHandler {
	return &SFTPHandler{
		sessionManager: sm,
		sftpClients:    make(map[string]*sftp.Client),
	}
}

// GetSFTPClient gets or creates an SFTP client for a session
func (h *SFTPHandler) GetSFTPClient(sessionID string) (*sftp.Client, error) {
	h.mu.RLock()
	client, ok := h.sftpClients[sessionID]
	h.mu.RUnlock()

	if ok {
		return client, nil
	}

	// Create new SFTP client
	sshSession, ok := h.sessionManager.GetSession(sessionID)
	if !ok {
		return nil, fmt.Errorf("session not found")
	}

	log.Printf("Creating SFTP client for session: %s", sessionID)

	// Use the standard sftp.NewClient approach
	sftpClient, err := sftp.NewClient(sshSession.Client)
	if err != nil {
		log.Printf("Failed to create SFTP client: %v", err)
		return nil, fmt.Errorf("failed to create SFTP client: %v", err)
	}

	h.mu.Lock()
	h.sftpClients[sessionID] = sftpClient
	h.mu.Unlock()

	log.Printf("SFTP client created successfully for session: %s", sessionID)
	return sftpClient, nil
}

// CloseSFTPClient closes an SFTP client
func (h *SFTPHandler) CloseSFTPClient(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if client, ok := h.sftpClients[sessionID]; ok {
		client.Close()
		delete(h.sftpClients, sessionID)
	}
}

// HandleListDir handles directory listing requests
func (h *SFTPHandler) HandleListDir(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	path := r.URL.Query().Get("path")

	if path == "" {
		path = "."
	}

	client, err := h.GetSFTPClient(sessionID)
	if err != nil {
		sendSFTPError(w, err.Error())
		return
	}

	files, err := client.ReadDir(path)
	if err != nil {
		sendSFTPError(w, fmt.Sprintf("failed to read directory: %v", err))
		return
	}

	fileList := make([]models.SFTPFile, 0, len(files))
	for _, f := range files {
		fileList = append(fileList, models.SFTPFile{
			Name:    f.Name(),
			Size:    f.Size(),
			Mode:    f.Mode().String(),
			ModTime: f.ModTime().Unix(),
			IsDir:   f.IsDir(),
		})
	}

	sendSFTPResponse(w, fileList)
}

// HandleDownload handles file download requests
func (h *SFTPHandler) HandleDownload(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	filePath := r.URL.Query().Get("path")

	if filePath == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	client, err := h.GetSFTPClient(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	file, err := client.Open(filePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to open file: %v", err), http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// Set download headers
	filename := filepath.Base(filePath)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.Header().Set("Content-Type", "application/octet-stream")

	io.Copy(w, file)
}

// HandleUpload handles file upload requests
func (h *SFTPHandler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	filePath := r.URL.Query().Get("path")

	if filePath == "" {
		sendSFTPError(w, "path is required")
		return
	}

	// Parse multipart form (max 100MB)
	err := r.ParseMultipartForm(100 << 20)
	if err != nil {
		sendSFTPError(w, fmt.Sprintf("failed to parse form: %v", err))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		sendSFTPError(w, fmt.Sprintf("failed to get file: %v", err))
		return
	}
	defer file.Close()

	client, err := h.GetSFTPClient(sessionID)
	if err != nil {
		sendSFTPError(w, err.Error())
		return
	}

	// Create remote file
	dstFile, err := client.Create(filePath)
	if err != nil {
		sendSFTPError(w, fmt.Sprintf("failed to create remote file: %v", err))
		return
	}
	defer dstFile.Close()

	// Copy file content
	_, err = io.Copy(dstFile, file)
	if err != nil {
		sendSFTPError(w, fmt.Sprintf("failed to upload file: %v", err))
		return
	}

	sendSFTPResponse(w, map[string]string{
		"filename": header.Filename,
		"path":     filePath,
	})
}

// HandleMkdir handles directory creation
func (h *SFTPHandler) HandleMkdir(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	path := r.URL.Query().Get("path")

	if path == "" {
		sendSFTPError(w, "path is required")
		return
	}

	client, err := h.GetSFTPClient(sessionID)
	if err != nil {
		sendSFTPError(w, err.Error())
		return
	}

	err = client.MkdirAll(path)
	if err != nil {
		sendSFTPError(w, fmt.Sprintf("failed to create directory: %v", err))
		return
	}

	sendSFTPResponse(w, map[string]string{"path": path})
}

// HandleRemove handles file/directory removal
func (h *SFTPHandler) HandleRemove(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	path := r.URL.Query().Get("path")

	if path == "" {
		sendSFTPError(w, "path is required")
		return
	}

	client, err := h.GetSFTPClient(sessionID)
	if err != nil {
		sendSFTPError(w, err.Error())
		return
	}

	// Check if it's a directory
	info, err := client.Stat(path)
	if err != nil {
		sendSFTPError(w, fmt.Sprintf("failed to stat: %v", err))
		return
	}

	if info.IsDir() {
		err = client.RemoveDirectory(path)
	} else {
		err = client.Remove(path)
	}

	if err != nil {
		sendSFTPError(w, fmt.Sprintf("failed to remove: %v", err))
		return
	}

	sendSFTPResponse(w, map[string]string{"path": path})
}

// HandlePwd handles current directory request
func (h *SFTPHandler) HandlePwd(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")

	client, err := h.GetSFTPClient(sessionID)
	if err != nil {
		sendSFTPError(w, err.Error())
		return
	}

	path, err := client.Getwd()
	if err != nil {
		sendSFTPError(w, fmt.Sprintf("failed to get current directory: %v", err))
		return
	}

	sendSFTPResponse(w, map[string]string{"path": path})
}

// HandleCd handles change directory request
func (h *SFTPHandler) HandleCd(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")

	if path == "" {
		path = "/"
	}

	// For SFTP, we just update the client's working directory reference
	// The actual path is managed client-side
	sendSFTPResponse(w, map[string]string{"path": path})
}

// Cleanup closes all SFTP clients
func (h *SFTPHandler) Cleanup() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for sessionID, client := range h.sftpClients {
		client.Close()
		delete(h.sftpClients, sessionID)
	}
}

func sendSFTPResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.SFTPResponse{
		Success: true,
		Data:    data,
	})
}

func sendSFTPError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(models.SFTPResponse{
		Success: false,
		Error:   msg,
	})
}

// CreateSSHSessionForSFTP creates an SSH session specifically for SFTP
func CreateSSHSessionForSFTP(w http.ResponseWriter, r *http.Request, sm *SSHSessionManager) string {
	var config models.SSHConnectionConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return ""
	}

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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return ""
	}

	targetClient := clients[len(clients)-1]
	jumpClients := clients[:len(clients)-1]

	sessionID := fmt.Sprintf("sftp_%d", time.Now().UnixNano())
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
