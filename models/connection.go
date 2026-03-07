package models

// SSHConnectionConfig holds SSH connection configuration
type SSHConnectionConfig struct {
	Host              string `json:"host"`
	Port              int    `json:"port"`
	Username          string `json:"username"`
	Password          string `json:"password,omitempty"`
	EncryptedPassword string `json:"encryptedPassword,omitempty"`
	PrivateKey        string `json:"privateKey,omitempty"`
	Passphrase        string `json:"passphrase,omitempty"`
}

// TerminalMessage represents a WebSocket message for terminal
type TerminalMessage struct {
	Type  string `json:"type"`
	Data  string `json:"data,omitempty"`
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
}

// SFTPFile represents a file in SFTP listing
type SFTPFile struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime int64  `json:"modTime"`
	IsDir   bool   `json:"isDir"`
}

// SFTPResponse represents SFTP operation response
type SFTPResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// LocalBashConfig holds local bash configuration
type LocalBashConfig struct {
	Shell string `json:"shell"`
}
