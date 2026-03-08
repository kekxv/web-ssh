package handlers

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"web-ssh/models"
)

// KeyPair holds RSA key pair
type KeyPair struct {
	PrivateKey *rsa.PrivateKey
	PublicKey  *rsa.PublicKey
}

// Global key pair for password encryption
var (
	globalKeyPair *KeyPair
	keyOnce       sync.Once
)

// initKeyPair initializes the global RSA key pair
func initKeyPair() {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(fmt.Sprintf("Failed to generate RSA key: %v", err))
	}
	globalKeyPair = &KeyPair{
		PrivateKey: privKey,
		PublicKey:  &privKey.PublicKey,
	}
}

// GetKeyPair returns the global key pair, initializing if needed
func GetKeyPair() *KeyPair {
	keyOnce.Do(initKeyPair)
	return globalKeyPair
}

// GetPublicKeyPEM returns the public key in PEM format
func GetPublicKeyPEM() ([]byte, error) {
	keyPair := GetKeyPair()
	pubDER, err := x509.MarshalPKIXPublicKey(keyPair.PublicKey)
	if err != nil {
		return nil, err
	}
	return pubDER, nil
}

// DecryptPassword decrypts a base64-encoded RSA-encrypted password
func DecryptPassword(encryptedBase64 string) (string, error) {
	keyPair := GetKeyPair()

	encryptedData, err := base64.StdEncoding.DecodeString(encryptedBase64)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %v", err)
	}

	// Use OAEP with SHA256
	decrypted, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, keyPair.PrivateKey, encryptedData, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %v", err)
	}

	return string(decrypted), nil
}

// DecryptData decrypts hybrid AES-RSA encrypted data
// Format: keyLength(4) + encryptedAESKey + iv(12) + encryptedData
func DecryptData(encryptedBase64 string) (string, error) {
	keyPair := GetKeyPair()

	encryptedData, err := base64.StdEncoding.DecodeString(encryptedBase64)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %v", err)
	}

	// Read key length
	if len(encryptedData) < 4 {
		return "", fmt.Errorf("data too short")
	}
	keyLength := int(binary.LittleEndian.Uint32(encryptedData[0:4]))

	// Extract encrypted AES key
	if len(encryptedData) < 4+keyLength {
		return "", fmt.Errorf("data too short for key")
	}
	encryptedAESKey := encryptedData[4 : 4+keyLength]

	// RSA decrypt AES key
	aesKeyBytes, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, keyPair.PrivateKey, encryptedAESKey, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt AES key: %v", err)
	}

	// Extract IV
	ivStart := 4 + keyLength
	if len(encryptedData) < ivStart+12 {
		return "", fmt.Errorf("data too short for IV")
	}
	iv := encryptedData[ivStart : ivStart+12]

	// Extract encrypted content
	contentStart := ivStart + 12
	encryptedContent := encryptedData[contentStart:]

	// AES decrypt
	block, err := aes.NewCipher(aesKeyBytes)
	if err != nil {
		return "", fmt.Errorf("failed to create AES cipher: %v", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %v", err)
	}

	decrypted, err := aesGCM.Open(nil, iv, encryptedContent, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt content: %v", err)
	}

	return string(decrypted), nil
}

// SSHSessionManager manages SSH sessions
type SSHSessionManager struct {
	sessions map[string]*SSHSession
	mu       sync.RWMutex
}

// SSHSession represents an active SSH session
type SSHSession struct {
	ID          string
	Username    string // The web user who owns this session
	Config      *models.SSHConnectionConfig
	Client      *ssh.Client
	JumpClients []*ssh.Client
	Session     *ssh.Session
	LastActive  time.Time
	mu          sync.Mutex
}

// NewSSHSessionManager creates a new session manager
func NewSSHSessionManager() *SSHSessionManager {
	return &SSHSessionManager{
		sessions: make(map[string]*SSHSession),
	}
}

// CloseUserSessions closes all SSH sessions belonging to a specific web user
func (m *SSHSessionManager) CloseUserSessions(username string) {
	m.mu.Lock()
	var sessionsToClose []*SSHSession
	for id, session := range m.sessions {
		if session.Username == username {
			sessionsToClose = append(sessionsToClose, session)
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()

	for _, session := range sessionsToClose {
		session.Close()
	}
}

// CreateSSHClient creates a new SSH client from config with jump host support.
// Returns a slice of clients where the last one is the target client.
func CreateSSHClient(config *models.SSHConnectionConfig) ([]*ssh.Client, error) {
	// If there are jump hosts, create SSH tunnel through them
	if len(config.JumpHosts) > 0 {
		return CreateSSHClientWithJump(config)
	}

	// Direct connection (no jump hosts)
	client, err := createDirectConnection(config)
	if err != nil {
		return nil, err
	}
	return []*ssh.Client{client}, nil
}

// createDirectConnection creates a direct SSH connection
func createDirectConnection(config *models.SSHConnectionConfig) (*ssh.Client, error) {
	authMethods, err := getAuthMethods(config)
	if err != nil {
		return nil, err
	}

	clientConfig := ssh.ClientConfig{
		User:            config.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", config.Host, config.Port)
	client, err := ssh.Dial("tcp", addr, &clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %v", err)
	}

	return client, nil
}

// getAuthMethods returns authentication methods based on config
func getAuthMethods(config *models.SSHConnectionConfig) ([]ssh.AuthMethod, error) {
	var authMethods []ssh.AuthMethod

	if config.PrivateKey != "" {
		var signer ssh.Signer
		var err error

		if config.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(config.PrivateKey), []byte(config.Passphrase))
			if err != nil {
				return nil, fmt.Errorf("failed to parse encrypted private key: %v", err)
			}
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(config.PrivateKey))
			if err != nil {
				return nil, fmt.Errorf("failed to parse private key: %v", err)
			}
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	} else if config.Password != "" {
		authMethods = append(authMethods, ssh.Password(config.Password))
	} else {
		return nil, fmt.Errorf("no authentication method provided")
	}

	return authMethods, nil
}

// CreateSSHClientWithJump creates SSH connection through jump hosts (supports up to 4 levels)
func CreateSSHClientWithJump(config *models.SSHConnectionConfig) ([]*ssh.Client, error) {
	// Limit jump hosts to 4 levels
	jumpHosts := config.JumpHosts
	if len(jumpHosts) > 4 {
		jumpHosts = jumpHosts[:4]
	}

	var allClients []*ssh.Client
	var currentClient *ssh.Client
	var err error

	// Connect through each jump host
	for i, jumpHost := range jumpHosts {
		// Decrypt jump host credentials if needed
		if jumpHost.EncryptedPassword != "" {
			jumpHost.Password, err = DecryptData(jumpHost.EncryptedPassword)
			if err != nil {
				closeAllClients(allClients)
				return nil, fmt.Errorf("failed to decrypt jump host %d password: %v", i+1, err)
			}
		}
		if jumpHost.EncryptedPrivateKey != "" {
			jumpHost.PrivateKey, err = DecryptData(jumpHost.EncryptedPrivateKey)
			if err != nil {
				closeAllClients(allClients)
				return nil, fmt.Errorf("failed to decrypt jump host %d private key: %v", i+1, err)
			}
		}
		if jumpHost.EncryptedPassphrase != "" {
			jumpHost.Passphrase, err = DecryptData(jumpHost.EncryptedPassphrase)
			if err != nil {
				closeAllClients(allClients)
				return nil, fmt.Errorf("failed to decrypt jump host %d passphrase: %v", i+1, err)
			}
		}

		jumpConfig := &models.SSHConnectionConfig{
			Host:       jumpHost.Host,
			Port:       jumpHost.Port,
			Username:   jumpHost.Username,
			Password:   jumpHost.Password,
			PrivateKey: jumpHost.PrivateKey,
			Passphrase: jumpHost.Passphrase,
		}

		// Create connection to jump host
		if currentClient == nil {
			// First jump host - direct connection
			currentClient, err = createDirectConnection(jumpConfig)
			if err != nil {
				closeAllClients(allClients)
				return nil, fmt.Errorf("failed to connect to jump host %d (%s:%d): %v", i+1, jumpHost.Host, jumpHost.Port, err)
			}
		} else {
			// Subsequent jump hosts - tunnel through previous connection
			jumpAddr := fmt.Sprintf("%s:%d", jumpHost.Host, jumpHost.Port)
			conn, err := currentClient.Dial("tcp", jumpAddr)
			if err != nil {
				closeAllClients(allClients)
				return nil, fmt.Errorf("failed to tunnel to jump host %d (%s): %v", i+1, jumpAddr, err)
			}

			// Create SSH config for jump host
			authMethods, err := getAuthMethods(jumpConfig)
			if err != nil {
				closeAllClients(allClients)
				return nil, fmt.Errorf("failed to get auth methods for jump host %d: %v", i+1, err)
			}

			sshConfig := ssh.ClientConfig{
				User:            jumpConfig.Username,
				Auth:            authMethods,
				HostKeyCallback: ssh.InsecureIgnoreHostKey(),
				Timeout:         30 * time.Second,
			}

			// Create SSH client connection over the tunnel
			ncc, chans, reqs, err := ssh.NewClientConn(conn, jumpAddr, &sshConfig)
			if err != nil {
				closeAllClients(allClients)
				return nil, fmt.Errorf("failed to create SSH client for jump host %d: %v", i+1, err)
			}

			currentClient = ssh.NewClient(ncc, chans, reqs)
		}
		allClients = append(allClients, currentClient)
	}

	// Decrypt target host credentials if needed
	if config.EncryptedPassword != "" {
		config.Password, err = DecryptData(config.EncryptedPassword)
		if err != nil {
			closeAllClients(allClients)
			return nil, fmt.Errorf("failed to decrypt target password: %v", err)
		}
	}
	if config.EncryptedPrivateKey != "" {
		config.PrivateKey, err = DecryptData(config.EncryptedPrivateKey)
		if err != nil {
			closeAllClients(allClients)
			return nil, fmt.Errorf("failed to decrypt target private key: %v", err)
		}
	}
	if config.EncryptedPassphrase != "" {
		config.Passphrase, err = DecryptData(config.EncryptedPassphrase)
		if err != nil {
			closeAllClients(allClients)
			return nil, fmt.Errorf("failed to decrypt target passphrase: %v", err)
		}
	}

	// Connect to final target through the last jump host
	targetAddr := fmt.Sprintf("%s:%d", config.Host, config.Port)
	conn, err := currentClient.Dial("tcp", targetAddr)
	if err != nil {
		closeAllClients(allClients)
		return nil, fmt.Errorf("failed to tunnel to target host (%s): %v", targetAddr, err)
	}

	// Create SSH config for target host
	authMethods, err := getAuthMethods(config)
	if err != nil {
		closeAllClients(allClients)
		return nil, fmt.Errorf("failed to get auth methods for target host: %v", err)
	}

	sshConfig := ssh.ClientConfig{
		User:            config.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}

	// Create final SSH client connection
	ncc, chans, reqs, err := ssh.NewClientConn(conn, targetAddr, &sshConfig)
	if err != nil {
		closeAllClients(allClients)
		return nil, fmt.Errorf("failed to create SSH client for target host: %v", err)
	}

	targetClient := ssh.NewClient(ncc, chans, reqs)
	allClients = append(allClients, targetClient)

	return allClients, nil
}

// closeAllClients closes all clients in the slice
func closeAllClients(clients []*ssh.Client) {
	for i := len(clients) - 1; i >= 0; i-- {
		if clients[i] != nil {
			clients[i].Close()
		}
	}
}

// AddSession adds a session to the manager
func (m *SSHSessionManager) AddSession(id string, session *SSHSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[id] = session
}

// GetSession gets a session by ID
func (m *SSHSessionManager) GetSession(id string) (*SSHSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[id]
	return session, ok
}

// RemoveSession removes a session by ID
func (m *SSHSessionManager) RemoveSession(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if session, ok := m.sessions[id]; ok {
		session.Close()
		delete(m.sessions, id)
	}
}

// ListSessions lists all session IDs
func (m *SSHSessionManager) ListSessions() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	return ids
}

// Close closes the SSH session and all its clients
func (s *SSHSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Session != nil {
		s.Session.Close()
	}
	if s.Client != nil {
		s.Client.Close()
	}
	// Close jump host clients in reverse order
	for i := len(s.JumpClients) - 1; i >= 0; i-- {
		if s.JumpClients[i] != nil {
			s.JumpClients[i].Close()
		}
	}
	return nil
}

// NewSFTPClient creates a new SFTP client from an SSH client
func NewSFTPClient(sshClient *ssh.Client) (io.Closer, error) {
	// Import github.com/pkg/sftp in the handler
	return sshClient.NewSession()
}
