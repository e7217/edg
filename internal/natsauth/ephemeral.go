package natsauth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// secretBytes is the entropy per generated credential. 32 bytes of crypto/rand
// rendered as base64url is 43 characters with no padding and no characters that
// require escaping inside a nats://user:pass@host URL.
const secretBytes = 32

// Ephemeral is the identity edg-core uses to talk to its own embedded server.
// It is generated fresh on every boot and never written to disk: nothing
// outside this process needs it, and the connection does not traverse TCP
// (see nats.InProcessServer in cmd/core/main.go).
type Ephemeral struct {
	Username string
	Secret   string
}

// NewEphemeralCore mints the per-process core identity.
func NewEphemeralCore() (Ephemeral, error) {
	secret, err := newSecret()
	if err != nil {
		return Ephemeral{}, fmt.Errorf("generate core credential: %w", err)
	}
	return Ephemeral{Username: RoleCore, Secret: secret}, nil
}

// newSecret returns a URL-safe random secret.
func newSecret() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
