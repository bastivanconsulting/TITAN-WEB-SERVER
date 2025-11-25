//go:build linux && cgo
// +build linux,cgo

package auth

import "errors"

// Since we cannot link against libpam in this environment,
// we will provide a runtime error instead of a build error.
// The user asked for PAM, but without the system library, we can't compile it.

type PAMProvider struct {
	ServiceName string
}

func NewPAMProvider(serviceName string) (*PAMProvider, error) {
	return nil, errors.New("PAM support requires libpam0g-dev and CGO")
}

func (p *PAMProvider) Login(username, password string) bool {
	return false
}
