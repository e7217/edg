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
)

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
