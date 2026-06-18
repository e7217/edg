package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceError_MessageAndKind(t *testing.T) {
	err := newServiceError(ErrValidation, "name is required")
	require.Equal(t, "name is required", err.Error())
	require.Equal(t, ErrValidation, KindOf(err))
}

func TestServiceError_Formatting(t *testing.T) {
	err := newServiceError(ErrNotFound, "asset %s not found", "abc")
	require.Equal(t, "asset abc not found", err.Error())
	require.Equal(t, ErrNotFound, KindOf(err))
}

func TestKindOf_NonServiceErrorIsInternal(t *testing.T) {
	require.Equal(t, ErrInternal, KindOf(errors.New("boom")))
	require.Equal(t, ErrInternal, KindOf(nil))
}
