package auth

// Provider is the interface for authentication
type Provider interface {
	// Login checks credentials and returns true if valid
	Login(username, password string) bool
}

// SimpleProvider implements Provider using config credentials
type SimpleProvider struct {
	ConfigUser string
	ConfigPass string
}

// NewSimpleProvider creates a new SimpleProvider
func NewSimpleProvider(user, pass string) *SimpleProvider {
	return &SimpleProvider{
		ConfigUser: user,
		ConfigPass: pass,
	}
}

// Login checks credentials
func (p *SimpleProvider) Login(username, password string) bool {
	// In production, use constant time comparison
	return username == p.ConfigUser && password == p.ConfigPass
}
