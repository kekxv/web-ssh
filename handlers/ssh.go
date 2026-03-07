package handlers

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
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

// SSHSessionManager manages SSH sessions
type SSHSessionManager struct {
	sessions map[string]*SSHSession
	mu       sync.RWMutex
}

// SSHSession represents an active SSH session
type SSHSession struct {
	ID         string
	Config     *models.SSHConnectionConfig
	Client     *ssh.Client
	Session    *ssh.Session
	LastActive time.Time
	mu         sync.Mutex
}

// NewSSHSessionManager creates a new session manager
func NewSSHSessionManager() *SSHSessionManager {
	return &SSHSessionManager{
		sessions: make(map[string]*SSHSession),
	}
}

// CreateSSHClient creates a new SSH client from config
func CreateSSHClient(config *models.SSHConnectionConfig) (*ssh.Client, error) {
	var authMethods []ssh.AuthMethod

	if config.PrivateKey != "" {
		// Parse private key
		var signer ssh.Signer
		var err error

		if config.Passphrase != "" {
			// Private key with passphrase
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(config.PrivateKey), []byte(config.Passphrase))
			if err != nil {
				return nil, fmt.Errorf("failed to parse encrypted private key: %v", err)
			}
		} else {
			// Private key without passphrase
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

// Close closes the SSH session
func (s *SSHSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Session != nil {
		s.Session.Close()
	}
	if s.Client != nil {
		s.Client.Close()
	}
	return nil
}

// NewSFTPClient creates a new SFTP client from an SSH client
func NewSFTPClient(sshClient *ssh.Client) (io.Closer, error) {
	// Import github.com/pkg/sftp in the handler
	return sshClient.NewSession()
}
