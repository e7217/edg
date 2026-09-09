package natsauth

import (
	"fmt"
	"strings"

	"github.com/nats-io/nats-server/v2/server"
)

// Config is everything Apply needs to build the user table.
type Config struct {
	// Mode is one of ModeCompat, ModeStrict, ModeOff.
	Mode string
	// Stream is the JetStream stream the fanout role may consume. Empty or
	// invalid falls back to DefaultStreamName. Callers pass
	// CoreConfig.JetStream.Stream.Name so that renaming the stream does not
	// silently strip the fanout role of its permissions.
	Stream string
	// Creds holds the operator/adapter/fanout secrets.
	Creds Credentials
	// Core is the ephemeral identity edg-core uses for its own connection.
	Core Ephemeral
}

// Apply installs the role table onto a server.Options.
//
// It sets opts.Users (and opts.NoAuthUser in compat mode) and leaves every
// other field untouched — binding, JetStream and store configuration remain the
// caller's responsibility.
//
// In ModeOff nothing is installed and the caller is responsible for having
// already refused a non-loopback binding; see internal/core/config.go.
func Apply(opts *server.Options, cfg Config) error {
	if opts == nil {
		return fmt.Errorf("natsauth: nil server options")
	}

	switch cfg.Mode {
	case ModeOff:
		return nil
	case ModeCompat, ModeStrict:
	default:
		return fmt.Errorf("invalid nats.auth.mode: %q (allowed: %s)", cfg.Mode, strings.Join(Modes(), ", "))
	}

	if cfg.Core.Username == "" || cfg.Core.Secret == "" {
		return fmt.Errorf("natsauth: core identity is empty")
	}
	if !cfg.Creds.Complete() {
		return fmt.Errorf("natsauth: credentials incomplete (operator/adapter/fanout required)")
	}

	stream := cfg.Stream
	users := []*server.User{
		{Username: cfg.Core.Username, Password: cfg.Core.Secret, Permissions: Permissions(RoleCore, stream)},
		{Username: RoleOperator, Password: cfg.Creds.Operator, Permissions: Permissions(RoleOperator, stream)},
		{Username: RoleAdapter, Password: cfg.Creds.Adapter, Permissions: Permissions(RoleAdapter, stream)},
		{Username: RoleFanout, Password: cfg.Creds.Fanout, Permissions: Permissions(RoleFanout, stream)},
	}

	if cfg.Mode == ModeCompat {
		// The legacy user carries an unguessable password nobody is told: it
		// exists only as the target of NoAuthUser. nats-server validates that
		// NoAuthUser names a user that actually exists
		// (server/auth.go validateNoAuthUser).
		secret, err := newSecret()
		if err != nil {
			return fmt.Errorf("natsauth: generate legacy credential: %w", err)
		}
		users = append(users, &server.User{
			Username:    RoleLegacy,
			Password:    secret,
			Permissions: Permissions(RoleLegacy, stream),
		})
		opts.NoAuthUser = RoleLegacy
	} else {
		opts.NoAuthUser = ""
	}

	opts.Users = users
	return nil
}
