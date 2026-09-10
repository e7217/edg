package main

import (
	"os/exec"
	"strings"
	"testing"
)

// packageDeps returns the transitive import list of a package.
func packageDeps(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", pkg).Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	return strings.Fields(string(out))
}
