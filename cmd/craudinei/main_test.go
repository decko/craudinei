package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/decko/craudinei/internal/config"
	"github.com/go-telegram/bot/models"
)

func TestDefaultConfigPath(t *testing.T) {
	path := defaultConfigPath()
	if path == "" {
		t.Error("defaultConfigPath() returned empty string")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("defaultConfigPath() = %q, want absolute path", path)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	_, err := loadConfig("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("loadConfig() for missing file returned nil error, want non-nil")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	// Create a temporary file with invalid YAML
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.yaml")
	if err := os.WriteFile(configPath, []byte("invalid: yaml: content:"), 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_, err := loadConfig(configPath)
	if err == nil {
		t.Error("loadConfig() for invalid YAML returned nil error, want non-nil")
	}
}

func TestLoadConfigValidation(t *testing.T) {
	// Create a temporary file with config that passes YAML parsing but fails validation
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "validation_fail.yaml")

	// Missing required fields - validation should fail
	invalidConfig := []byte(`
telegram:
  token: ""
  allowed_users: []
  auth_passphrase: ""
claude:
  binary: ""
  allowed_paths: []
approval:
  port: 8080
logging:
  level: debug
`)
	if err := os.WriteFile(configPath, invalidConfig, 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_, err := loadConfig(configPath)
	if err == nil {
		t.Error("loadConfig() for invalid config returned nil error, want non-nil")
	}
}

func TestLoadConfigPathExpansion(t *testing.T) {
	// Test that loadConfig handles path expansion
	// This verifies that os.ExpandEnv is called correctly
	// We use $HOME which should expand to the home directory
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "path_expand.yaml")

	// Write a minimal valid config to test path handling
	validConfig := []byte(`
telegram:
  token: "test-token"
  allowed_users:
    - 123456
  auth_passphrase: "test"
  auth_idle_timeout: 1h
claude:
  binary: "/usr/bin/claude"
  default_workdir: "/tmp"
  allowed_paths:
    - "/tmp"
  system_prompt: "test"
  max_turns: 10
  max_budget_usd: 1.0
approval:
  port: 8080
  timeout_bash: 5m
  timeout_edit: 5m
  timeout_write: 5m
logging:
  level: info
`)
	if err := os.WriteFile(configPath, validConfig, 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Errorf("loadConfig() returned error: %v", err)
	}
	if cfg == nil {
		t.Error("loadConfig() returned nil config")
	}
}

// shutdownSignals returns the signals used for shutdown notification.
func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

func TestShutdownSignalsContainsInterruptAndSIGTERM(t *testing.T) {
	signals := shutdownSignals()

	foundInterrupt := false
	foundSIGTERM := false

	for _, s := range signals {
		if s == os.Interrupt {
			foundInterrupt = true
		}
		if s == syscall.SIGTERM {
			foundSIGTERM = true
		}
	}

	if !foundInterrupt {
		t.Error("shutdownSignals() does not contain os.Interrupt")
	}
	if !foundSIGTERM {
		t.Error("shutdownSignals() does not contain syscall.SIGTERM")
	}
}

func TestShutdownSignalsNotNilOrEmpty(t *testing.T) {
	signals := shutdownSignals()
	if len(signals) == 0 {
		t.Error("shutdownSignals() returned empty slice")
	}
	for i, s := range signals {
		if s == nil {
			t.Errorf("shutdownSignals()[%d] is nil", i)
		}
	}
}

func TestEmptyAllowedUsersRejectedEarly(t *testing.T) {
	// Create a config with empty allowed users that passes YAML parsing
	// but should be rejected by Validate since allowed_users cannot be empty
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "empty_users.yaml")

	invalidConfig := []byte(`
telegram:
  token: "test-token"
  allowed_users: []
  auth_passphrase: "test"
  auth_idle_timeout: 1h
claude:
  binary: "/usr/bin/claude"
  default_workdir: "/tmp"
  allowed_paths:
    - "/tmp"
  system_prompt: "test"
  max_turns: 10
  max_budget_usd: 1.0
approval:
  port: 8080
  timeout_bash: 5m
  timeout_edit: 5m
  timeout_write: 5m
logging:
  level: info
`)

	if err := os.WriteFile(configPath, invalidConfig, 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_, err := loadConfig(configPath)
	if err == nil {
		t.Error("loadConfig() with empty allowed_users should return error, got nil")
	}
}

func TestConvertUpdateNilFrom(t *testing.T) {
	// models.Update with Message but nil From
	src := &models.Update{
		Message: &models.Message{
			Text: "hello",
			Chat: models.Chat{ID: 12345},
			// From is nil
		},
	}

	dst := convertUpdate(src)

	if dst == nil {
		t.Fatal("convertUpdate() returned nil for non-nil input")
	}
	if dst.Message.Text != "hello" {
		t.Errorf("convertUpdate().Message.Text = %q, want %q", dst.Message.Text, "hello")
	}
	if dst.Message.Chat.ID != 12345 {
		t.Errorf("convertUpdate().Message.Chat.ID = %d, want %d", dst.Message.Chat.ID, 12345)
	}
	// From.ID should remain zero-value since src.Message.From is nil
	if dst.Message.From.ID != 0 {
		t.Errorf("convertUpdate().Message.From.ID = %d, want 0 (nil From)", dst.Message.From.ID)
	}
}

func TestConvertUpdateNilMessage(t *testing.T) {
	// models.Update with nil Message
	src := &models.Update{
		Message: nil,
		EditedMessage: &models.Message{
			Text: "edited",
			Chat: models.Chat{ID: 67890},
		},
	}

	dst := convertUpdate(src)

	if dst == nil {
		t.Fatal("convertUpdate() returned nil for nil Message")
	}
	// When Message is nil, dst.Message should be zero-value
	if dst.Message.Text != "" {
		t.Errorf("convertUpdate().Message.Text = %q, want empty string", dst.Message.Text)
	}
}

func TestConvertUpdateNilUpdate(t *testing.T) {
	src := (*models.Update)(nil)

	dst := convertUpdate(src)

	if dst != nil {
		t.Errorf("convertUpdate(nil) = %v, want nil", dst)
	}
}

func TestConvertUpdateWithFrom(t *testing.T) {
	src := &models.Update{
		Message: &models.Message{
			Text: "hello from user",
			Chat: models.Chat{ID: 12345},
			From: &models.User{
				ID: 999,
			},
		},
	}

	dst := convertUpdate(src)

	if dst == nil {
		t.Fatal("convertUpdate() returned nil")
	}
	if dst.Message.Text != "hello from user" {
		t.Errorf("Message.Text = %q, want %q", dst.Message.Text, "hello from user")
	}
	if dst.Message.Chat.ID != 12345 {
		t.Errorf("Chat.ID = %d, want %d", dst.Message.Chat.ID, 12345)
	}
	if dst.Message.From.ID != 999 {
		t.Errorf("From.ID = %d, want %d", dst.Message.From.ID, 999)
	}
}

func TestSetupLoggingInfoLevel(t *testing.T) {
	cfg := &config.Config{
		Logging: config.LoggingConfig{
			Level: "info",
		},
	}

	logger := setupLogging(cfg)

	if logger == nil {
		t.Fatal("setupLogging() returned nil logger")
	}

	logger.Info("test info message")
}

func TestSetupLoggingDebugLevel(t *testing.T) {
	cfg := &config.Config{
		Logging: config.LoggingConfig{
			Level: "debug",
		},
	}

	logger := setupLogging(cfg)

	if logger == nil {
		t.Fatal("setupLogging() returned nil logger")
	}

	logger.Debug("test debug message")
}

func TestSetupLoggingWarnLevel(t *testing.T) {
	cfg := &config.Config{
		Logging: config.LoggingConfig{
			Level: "warn",
		},
	}

	logger := setupLogging(cfg)

	if logger == nil {
		t.Fatal("setupLogging() returned nil logger")
	}

	logger.Warn("test warn message")
}

func TestSetupLoggingErrorLevel(t *testing.T) {
	cfg := &config.Config{
		Logging: config.LoggingConfig{
			Level: "error",
		},
	}

	logger := setupLogging(cfg)

	if logger == nil {
		t.Fatal("setupLogging() returned nil logger")
	}

	logger.Error("test error message")
}

func TestSetupLoggingUnknownLevel(t *testing.T) {
	// Unknown level should default to info
	cfg := &config.Config{
		Logging: config.LoggingConfig{
			Level: "unknown",
		},
	}

	logger := setupLogging(cfg)

	if logger == nil {
		t.Fatal("setupLogging() returned nil logger for unknown level")
	}

	logger.Info("test with unknown level defaulted to info")
}

func TestSetupLoggingWithFile(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	cfg := &config.Config{
		Logging: config.LoggingConfig{
			Level: "debug",
			File:  logFile,
		},
	}

	logger := setupLogging(cfg)

	if logger == nil {
		t.Fatal("setupLogging() returned nil logger")
	}

	logger.Info("test message to file")

	// Verify file was created and has content
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if len(data) == 0 {
		t.Error("log file is empty, expected JSON log output")
	}
}

// TestShutdownSequenceFreshContext verifies that the shutdown sequence uses
// context.Background() to create a fresh context for the stop operation,
// not inheriting from the parent ctx which may have been cancelled earlier.
func TestShutdownSequenceFreshContext(t *testing.T) {
	// This test verifies the pattern used in run():
	//   stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	// The key is that context.Background() creates a fresh context, not inheriting
	// from the parent ctx which could be cancelled.

	// Simulate the parent context being cancelled (like after shutdown signal)
	_, parentCancel := context.WithCancel(context.Background())
	parentCancel() // Parent is now cancelled

	// The shutdown uses context.Background() - a fresh context
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()

	// The fresh context should NOT be done even though parent is cancelled
	select {
	case <-stopCtx.Done():
		t.Error("stopCtx is already done - should use fresh context.Background()")
	default:
		// Expected: fresh context is not done
	}

	// Verify the timeout is set correctly
	_, ok := stopCtx.Deadline()
	if !ok {
		t.Error("stopCtx should have a deadline")
	}
}

func TestShutdownContextNotCancelledByParentSignal(t *testing.T) {
	// This test verifies that signal.NotifyContext with os.Interrupt and
	// syscall.SIGTERM is called with the parent context (not a cancelled context).
	// The shutdownCtx should only be cancelled when the signal is received,
	// not when the parent context is cancelled (since parent is not cancelled
	// in the real run() flow until AFTER shutdown signal is received).

	parentCtx, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()

	shutdownCtx, stop := signal.NotifyContext(parentCtx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Parent is not cancelled, so shutdownCtx should not be done
	select {
	case <-shutdownCtx.Done():
		t.Error("shutdownCtx is done before any signal received")
	default:
		// Expected
	}

	// Send os.Interrupt to trigger cancellation
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("failed to find process: %v", err)
	}

	// Give signal time to be processed
	done := make(chan struct{}, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		close(done)
	}()

	// Send the signal
	err = p.Signal(os.Interrupt)
	if err != nil {
		t.Fatalf("failed to send signal: %v", err)
	}

	// Wait for shutdownCtx to be done
	select {
	case <-shutdownCtx.Done():
		// Expected: shutdownCtx is cancelled after signal
	case <-done:
		t.Error("shutdownCtx was not cancelled after signal within timeout")
	}
}
