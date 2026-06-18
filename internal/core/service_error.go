package core

import (
	"errors"
	"fmt"
)

// ErrorKind classifies a service-layer error so transports (HTTP status codes,
// MCP error types) can react consistently. The Error() string is unchanged from
// the legacy NATS responses, so the NATS contract is preserved.
type ErrorKind int

const (
	ErrInternal ErrorKind = iota
	ErrValidation
	ErrNotFound
	ErrConflict
	ErrConstraint
)

// ServiceError is a categorized, transport-agnostic error.
type ServiceError struct {
	Kind ErrorKind
	Msg  string
}

func (e *ServiceError) Error() string { return e.Msg }

func newServiceError(kind ErrorKind, format string, args ...any) *ServiceError {
	return &ServiceError{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// KindOf returns the ErrorKind of err if it is (or wraps) a *ServiceError,
// otherwise ErrInternal. KindOf(nil) is ErrInternal.
func KindOf(err error) ErrorKind {
	var se *ServiceError
	if errors.As(err, &se) {
		return se.Kind
	}
	return ErrInternal
}
