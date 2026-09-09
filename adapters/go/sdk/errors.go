package sdk

import (
	"errors"
	"fmt"
)

// Sentinel errors. Use errors.Is to classify failures.
var (
	// ErrConnection wraps NATS connection failures.
	ErrConnection = errors.New("nats connection")

	// ErrPublish wraps publish or request/reply failures.
	ErrPublish = errors.New("publish")

	// ErrCore is returned when EDG Core replies with success=false.
	ErrCore = errors.New("core reply error")

	// ErrNotFound is returned for Get* operations when the entity does not
	// exist. Equivalent to Python SDK returning None.
	ErrNotFound = errors.New("not found")

	// ErrDevice is the parent of device-related errors.
	ErrDevice = errors.New("device")

	// ErrDeviceConnection is a retryable device connection failure.
	ErrDeviceConnection = fmt.Errorf("%w: connection", ErrDevice)

	// ErrDeviceTimeout is a retryable device timeout.
	ErrDeviceTimeout = fmt.Errorf("%w: timeout", ErrDevice)

	// ErrForbidden means the NATS server refused the operation because the
	// connected credentials lack permission for that subject (ADR 0007).
	//
	// NATS reports publish violations only on the asynchronous error channel:
	// the publish is dropped server-side, no reply ever arrives, and a
	// request/reply would otherwise surface as a bare context deadline after
	// the full request timeout — indistinguishable from an unreachable core.
	// The Client correlates that async report back to the waiting call so the
	// failure names its own cause.
	ErrForbidden = errors.New("forbidden")
)

// ForbiddenError names the subject the server refused. Wraps ErrForbidden for
// errors.Is.
type ForbiddenError struct {
	Subject string
	Op      string // "publish" or "subscribe"
}

func (e *ForbiddenError) Error() string {
	return fmt.Sprintf("permission denied: not authorized to %s %s; "+
		"check the credentials in the NATS URL and the role's subject permissions", e.Op, e.Subject)
}

func (e *ForbiddenError) Unwrap() error { return ErrForbidden }

// CoreError is returned when EDG Core replies with success=false. The Message
// is the value of the reply's error field. Wraps ErrCore for errors.Is.
type CoreError struct {
	Subject string
	Message string
}

func (e *CoreError) Error() string {
	if e.Subject == "" {
		return fmt.Sprintf("core: %s", e.Message)
	}
	return fmt.Sprintf("core %s: %s", e.Subject, e.Message)
}

func (e *CoreError) Unwrap() error { return ErrCore }
