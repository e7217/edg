package sdk

import (
	"errors"
	"testing"
)

func TestDeviceErrorChain(t *testing.T) {
	if !errors.Is(ErrDeviceConnection, ErrDevice) {
		t.Errorf("ErrDeviceConnection should wrap ErrDevice")
	}
	if !errors.Is(ErrDeviceTimeout, ErrDevice) {
		t.Errorf("ErrDeviceTimeout should wrap ErrDevice")
	}
}

func TestCoreErrorUnwrap(t *testing.T) {
	ce := &CoreError{Subject: "platform.meta.asset.create", Message: "name is required"}
	if !errors.Is(ce, ErrCore) {
		t.Errorf("CoreError should unwrap to ErrCore")
	}
	if ce.Error() == "" {
		t.Errorf("CoreError.Error() empty")
	}
}
