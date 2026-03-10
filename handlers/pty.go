package handlers

import "io"

// PTY is a platform-independent interface for a PTY
type PTY interface {
	io.ReadWriteCloser
	Resize(rows, cols uint16) error
}
