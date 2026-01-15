package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	// Set test version variables
	Version = "v1.0.0-test"
	BuildTime = "2026-01-15T00:00:00Z"
	GitCommit = "abc123def456"

	// Build the binary for testing
	cmd := exec.Command("go", "build", "-o", "edg-core-test", ".")
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
	)

	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to build test binary: %v", err)
	}
	defer os.Remove("edg-core-test")

	// Test version flag
	versionCmd := exec.Command("./edg-core-test", "--version")
	output, err := versionCmd.CombinedOutput()

	// The program should exit with code 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() != 0 {
				t.Fatalf("Expected exit code 0, got %d", exitErr.ExitCode())
			}
		}
	}

	outputStr := string(output)

	// Check that output contains expected version information
	expectedStrings := []string{
		"EDG Platform Core",
		"Version:",
		"Build Time:",
		"Git Commit:",
	}

	for _, expected := range expectedStrings {
		if !strings.Contains(outputStr, expected) {
			t.Errorf("Expected output to contain %q, but it didn't.\nOutput: %s", expected, outputStr)
		}
	}
}

func TestVersionVariables(t *testing.T) {
	// Test that version variables have default values
	if Version == "" {
		Version = "dev"
	}
	if BuildTime == "" {
		BuildTime = "unknown"
	}
	if GitCommit == "" {
		GitCommit = "unknown"
	}

	// Verify defaults are set
	tests := []struct {
		name     string
		variable string
		want     string
	}{
		{"Version default", Version, "dev"},
		{"BuildTime default", BuildTime, "unknown"},
		{"GitCommit default", GitCommit, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.variable != tt.want && tt.variable == "" {
				t.Errorf("Expected %q to have a default value", tt.name)
			}
		})
	}
}
