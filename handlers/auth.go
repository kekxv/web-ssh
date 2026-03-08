package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const usersFilePath = "users.json"

// User represents a system user
type User struct {
	Username  string    `json:"username"`
	Password  string    `json:"password"` // Hashed password
	CreatedAt time.Time `json:"created_at"`
}

// Session represents an authenticated user session
type Session struct {
	ID        string
	Username  string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// AuthManager manages user authentication and sessions
type AuthManager struct {
	users          map[string]*User
	sessions       map[string]*Session
	sshManager     *SSHSessionManager
	mu             sync.RWMutex
}

// Global auth manager
var (
	authManager *AuthManager
	authOnce    sync.Once
)

// getAuthManager returns the global auth manager
func GetAuthManager() *AuthManager {
	return authManager
}

// SetSSHSessionManager sets the SSH session manager and initializes AuthManager
func SetSSHSessionManager(sm *SSHSessionManager) {
	authOnce.Do(func() {
		authManager = &AuthManager{
			users:      make(map[string]*User),
			sessions:   make(map[string]*Session),
			sshManager: sm,
		}
		
		// Load users from file or create default
		if err := authManager.loadUsers(); err != nil {
			log.Printf("Failed to load users from file, creating default admin: %v", err)
			// Initialize with default admin user
			// Default password: admin123
			hashedPwd := hashPassword("admin123")
			authManager.users["admin"] = &User{
				Username:  "admin",
				Password:  hashedPwd,
				CreatedAt: time.Now(),
			}
			authManager.saveUsers()
		}
	})
}

// loadUsers loads users from the json file
func (m *AuthManager) loadUsers() error {
	data, err := os.ReadFile(usersFilePath)
	if err != nil {
		return err
	}
	
	m.mu.Lock()
	defer m.mu.Unlock()
	return json.Unmarshal(data, &m.users)
}

// saveUsers saves users to the json file
func (m *AuthManager) saveUsers() error {
	m.mu.RLock()
	data, err := json.MarshalIndent(m.users, "", "  ")
	m.mu.RUnlock()
	
	if err != nil {
		return err
	}
	
	return os.WriteFile(usersFilePath, data, 0600)
}

// hashPassword hashes a password using SHA256
// In production, use bcrypt or argon2 for better security
func hashPassword(password string) string {
	hash := sha256.Sum256([]byte(password))
	return base64.StdEncoding.EncodeToString(hash[:])
}

// verifyPassword checks if a password matches a hash
func verifyPassword(password, hash string) bool {
	return hashPassword(password) == hash
}

// generateSessionID creates a random session ID
func generateSessionID() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

// CreateSession creates a new session for a user
func (m *AuthManager) CreateSession(username string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessionID := generateSessionID()
	session := &Session{
		ID:        sessionID,
		Username:  username,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour), // 24 hour session
	}
	m.sessions[sessionID] = session
	return sessionID
}

// GetSession retrieves a session by ID
func (m *AuthManager) GetSession(sessionID string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return nil, false
	}

	// Check if session has expired
	if time.Now().After(session.ExpiresAt) {
		return nil, false
	}

	return session, true
}

// DeleteSession removes a session
func (m *AuthManager) DeleteSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
}

// ValidateUser checks if username and password are valid
func (m *AuthManager) ValidateUser(username, password string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, ok := m.users[username]
	if !ok {
		return false
	}

	return verifyPassword(password, user.Password)
}

// CleanupExpiredSessions removes expired sessions
func (m *AuthManager) CleanupExpiredSessions() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for id, session := range m.sessions {
		if now.After(session.ExpiresAt) {
			delete(m.sessions, id)
		}
	}
}

// AuthMiddleware creates a middleware that requires authentication
func AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get session from cookie
		cookie, err := r.Cookie("session_id")
		if err != nil {
			http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
			return
		}

		sessionID := cookie.Value
		auth := GetAuthManager()
		session, ok := auth.GetSession(sessionID)
		if !ok {
			http.Error(w, `{"error": "session expired"}`, http.StatusUnauthorized)
			return
		}

		// Add username to context for logging
		log.Printf("Request by user: %s", session.Username)
		next(w, r)
	}
}

// WebSocketAuthMiddleware authenticates WebSocket connections
func WebSocketAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.URL.Query().Get("session_id")
		if sessionID == "" {
			// Try cookie
			cookie, err := r.Cookie("session_id")
			if err != nil {
				log.Printf("WebSocket auth failed: no session")
				http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
				return
			}
			sessionID = cookie.Value
		}

		auth := GetAuthManager()
		_, ok := auth.GetSession(sessionID)
		if !ok {
			log.Printf("WebSocket auth failed: invalid session")
			http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}

// LoginRequest represents a login request
type LoginRequest struct {
	Username        string `json:"username"`
	Password        string `json:"password"`         // For future plain password support (not recommended)
	EncryptedPassword string `json:"encrypted_password"` // RSA-AES encrypted password
}

// HandleLogin handles user login
func HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	// Decrypt password if encrypted
	password := req.Password
	if req.EncryptedPassword != "" {
		var err error
		password, err = DecryptData(req.EncryptedPassword)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to decrypt password"})
			return
		}
	}

	auth := GetAuthManager()
	if !auth.ValidateUser(req.Username, password) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid credentials"})
		return
	}

	sessionID := auth.CreateSession(req.Username)

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400, // 24 hours
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"username": req.Username,
	})
}

// HandleLogout handles user logout
func HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get session from cookie
	cookie, err := r.Cookie("session_id")
	if err == nil {
		auth := GetAuthManager()
		if auth != nil {
			session, ok := auth.GetSession(cookie.Value)
			if ok {
				// Close all SSH sessions for this user
				if auth.sshManager != nil {
					auth.sshManager.CloseUserSessions(session.Username)
				}
				auth.DeleteSession(cookie.Value)
			}
		}

		// Clear cookie
		http.SetCookie(w, &http.Cookie{
			Name:     "session_id",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			MaxAge:   -1,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// HandleCheckAuth checks if user is authenticated
func HandleCheckAuth(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session_id")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]bool{"authenticated": false})
		return
	}

	auth := GetAuthManager()
	session, ok := auth.GetSession(cookie.Value)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]bool{"authenticated": false})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"authenticated": true,
		"username":      session.Username,
	})
}

// ChangePasswordRequest represents a password change request
type ChangePasswordRequest struct {
	Username           string `json:"username"`
	OldPassword        string `json:"old_password"`
	NewPassword        string `json:"new_password"`
	EncryptedOldPassword string `json:"encrypted_old_password"`
	EncryptedNewPassword string `json:"encrypted_new_password"`
}

// HandleChangePassword handles password change
func HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ChangePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	auth := GetAuthManager()

	// Decrypt passwords if encrypted
	oldPassword := req.OldPassword
	newPassword := req.NewPassword
	if req.EncryptedOldPassword != "" {
		var err error
		oldPassword, err = DecryptData(req.EncryptedOldPassword)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to decrypt old password"})
			return
		}
	}
	if req.EncryptedNewPassword != "" {
		var err error
		newPassword, err = DecryptData(req.EncryptedNewPassword)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to decrypt new password"})
			return
		}
	}

	// Verify old password
	if !auth.ValidateUser(req.Username, oldPassword) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid credentials"})
		return
	}

	// Update password
	auth.mu.Lock()
	user, ok := auth.users[req.Username]
	if !ok {
		auth.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "user not found"})
		return
	}
	user.Password = hashPassword(newPassword)
	auth.mu.Unlock()
	auth.saveUsers()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// AddUserRequest represents an add user request
type AddUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// HandleAddUser handles adding a new user (admin only)
func HandleAddUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if current user is admin
	cookie, err := r.Cookie("session_id")
	if err != nil {
		http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
		return
	}

	auth := GetAuthManager()
	session, ok := auth.GetSession(cookie.Value)
	if !ok || session.Username != "admin" {
		http.Error(w, `{"error": "admin access required"}`, http.StatusForbidden)
		return
	}

	var req AddUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	if strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Password) == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "username and password required"})
		return
	}

	auth.mu.Lock()
	if _, exists := auth.users[req.Username]; exists {
		auth.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "user already exists"})
		return
	}

	auth.users[req.Username] = &User{
		Username:  req.Username,
		Password:  hashPassword(req.Password),
		CreatedAt: time.Now(),
	}
	auth.mu.Unlock()
	auth.saveUsers()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// HandleListUsers handles listing users (admin only)
func HandleListUsers(w http.ResponseWriter, r *http.Request) {
	// Check if current user is admin
	cookie, err := r.Cookie("session_id")
	if err != nil {
		http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
		return
	}

	auth := GetAuthManager()
	session, ok := auth.GetSession(cookie.Value)
	if !ok || session.Username != "admin" {
		http.Error(w, `{"error": "admin access required"}`, http.StatusForbidden)
		return
	}

	auth.mu.RLock()
	users := make([]map[string]interface{}, 0, len(auth.users))
	for username, user := range auth.users {
		users = append(users, map[string]interface{}{
			"username":   username,
			"created_at": user.CreatedAt.Format(time.RFC3339),
		})
	}
	auth.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"users": users})
}

// HandleDeleteUser handles deleting a user (admin only)
func HandleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if current user is admin
	cookie, err := r.Cookie("session_id")
	if err != nil {
		http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
		return
	}

	auth := GetAuthManager()
	session, ok := auth.GetSession(cookie.Value)
	if !ok || session.Username != "admin" {
		http.Error(w, `{"error": "admin access required"}`, http.StatusForbidden)
		return
	}

	username := r.URL.Query().Get("username")
	if username == "" || username == "admin" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid username"})
		return
	}

	auth.mu.Lock()
	if _, exists := auth.users[username]; !exists {
		auth.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "user not found"})
		return
	}
	delete(auth.users, username)
	auth.mu.Unlock()
	auth.saveUsers()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
