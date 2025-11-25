//go:build !linux || !cgo
// +build !linux !cgo

package auth

import "errors"

// PAMProvider implements Provider using PAM (Stub for non-Linux/non-CGO)
type PAMProvider struct {
	ServiceName string
}

// NewPAMProvider creates a new PAMProvider
func NewPAMProvider(serviceName string) (*PAMProvider, error) {
	return nil, errors.New("PAM authentication is only supported on Linux with CGO enabled")
}

// Login checks credentials
func (p *PAMProvider) Login(username, password string) bool {
	return false
}
