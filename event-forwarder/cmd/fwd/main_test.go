package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMainVersionFlag(t *testing.T) {
	// Build the binary first
	cmd := exec.Command("go", "build", "-o", "test_fwd.exe", ".")
	err := cmd.Run()
	if err != nil {
		t.Fatalf("failed to build binary: %v", err)
	}
	defer os.Remove("test_fwd.exe")

	// Test version flag
	cmd = exec.Command("./test_fwd.exe", "--version")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to run version command: %v", err)
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, "fwd version") {
		t.Errorf("expected version output to contain 'fwd version', got: %s", outputStr)
	}
}

func TestMainMissingConfig(t *testing.T) {
	// Build the binary first
	cmd := exec.Command("go", "build", "-o", "test_fwd.exe", ".")
	err := cmd.Run()
	if err != nil {
		t.Fatalf("failed to build binary: %v", err)
	}
	defer os.Remove("test_fwd.exe")

	// Clear environment variables
	os.Unsetenv("SOURCE_RELAY_URL")
	os.Unsetenv("DEEPFRY_RELAY_URL")
	os.Unsetenv("NOSTR_SYNC_SECKEY")

	// Test missing config
	cmd = exec.Command("./test_fwd.exe")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected error for missing config, but command succeeded")
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, "Error loading configuration") {
		t.Errorf("expected error message about configuration, got: %s", outputStr)
	}
}

func TestMainHelp(t *testing.T) {
	// Build the binary first
	cmd := exec.Command("go", "build", "-o", "test_fwd.exe", ".")
	err := cmd.Run()
	if err != nil {
		t.Fatalf("failed to build binary: %v", err)
	}
	defer os.Remove("test_fwd.exe")

	// Test help flag
	cmd = exec.Command("./test_fwd.exe", "--help")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to run help command: %v", err)
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, "Event Forwarder - Forward events between Nostr relays") {
		t.Errorf("expected help output to contain header, got: %s", outputStr)
	}
	if !strings.Contains(outputStr, "Usage:") {
		t.Errorf("expected help output to contain 'Usage:', got: %s", outputStr)
	}
	if !strings.Contains(outputStr, "Options:") {
		t.Errorf("expected help output to contain 'Options:', got: %s", outputStr)
	}
	if !strings.Contains(outputStr, "Environment Variables:") {
		t.Errorf("expected help output to contain 'Environment Variables:', got: %s", outputStr)
	}
}
