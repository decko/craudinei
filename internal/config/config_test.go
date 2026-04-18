package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfig_Defaults(t *testing.T) {
	t.Parallel()

	content := `
telegram:
  token: "test-token"
  allowed_users:
    - 123456789
  auth_passphrase: "test-passphrase"

claude:
  binary: "/usr/bin/claude"

approval:
  port: 0
`
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "craudinei.yaml")
	if err := os.WriteFile(configFile, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	cfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Telegram.AuthIdleTimeout != 1*time.Hour {
		t.Errorf("AuthIdleTimeout = %v, want 1h", cfg.Telegram.AuthIdleTimeout)
	}
	if cfg.Telegram.ApprovalTimeout != 5*time.Minute {
		t.Errorf("ApprovalTimeout = %v, want 5m", cfg.Telegram.ApprovalTimeout)
	}
	if cfg.Telegram.ProgressInterval != 2*time.Minute {
		t.Errorf("ProgressInterval = %v, want 2m", cfg.Telegram.ProgressInterval)
	}
	if cfg.Telegram.InactivityTimeout != 15*time.Minute {
		t.Errorf("InactivityTimeout = %v, want 15m", cfg.Telegram.InactivityTimeout)
	}
	if cfg.Claude.MaxTurns != 50 {
		t.Errorf("MaxTurns = %v, want 50", cfg.Claude.MaxTurns)
	}
	if cfg.Claude.MaxBudgetUSD != 5.00 {
		t.Errorf("MaxBudgetUSD = %v, want 5.00", cfg.Claude.MaxBudgetUSD)
	}
	if cfg.Claude.SystemPrompt != "Keep each response under 4000 characters. Wrap lines at 80 characters." {
		t.Errorf("SystemPrompt = %q, want default", cfg.Claude.SystemPrompt)
	}
	if cfg.Approval.TimeoutBash != 5*time.Minute {
		t.Errorf("TimeoutBash = %v, want 5m", cfg.Approval.TimeoutBash)
	}
	if cfg.Approval.TimeoutEdit != 5*time.Minute {
		t.Errorf("TimeoutEdit = %v, want 5m", cfg.Approval.TimeoutEdit)
	}
	if cfg.Approval.TimeoutWrite != 5*time.Minute {
		t.Errorf("TimeoutWrite = %v, want 5m", cfg.Approval.TimeoutWrite)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("Level = %q, want 'info'", cfg.Logging.Level)
	}
	if len(cfg.Claude.AllowedTools) != 3 {
		t.Errorf("AllowedTools len = %d, want 3", len(cfg.Claude.AllowedTools))
	}
	if cfg.Claude.AllowedTools[0] != "Read" || cfg.Claude.AllowedTools[1] != "Grep" || cfg.Claude.AllowedTools[2] != "Glob" {
		t.Errorf("AllowedTools = %v, want [Read, Grep, Glob]", cfg.Claude.AllowedTools)
	}
}

func TestLoadConfig_EnvVarExpansion(t *testing.T) {
	t.Parallel()

	content := `
telegram:
  token: "${TEST_TOKEN_VAR}"
  allowed_users:
    - 123456789
  auth_passphrase: "${TEST_PASSPHRASE_VAR}"

claude:
  binary: "/usr/bin/claude"
`
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "craudinei.yaml")
	if err := os.WriteFile(configFile, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	wantToken := "expanded-bot-token"
	wantPassphrase := "expanded-secret-passphrase"
	os.Setenv("TEST_TOKEN_VAR", wantToken)
	os.Setenv("TEST_PASSPHRASE_VAR", wantPassphrase)
	defer func() {
		os.Unsetenv("TEST_TOKEN_VAR")
		os.Unsetenv("TEST_PASSPHRASE_VAR")
	}()

	cfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Telegram.Token != wantToken {
		t.Errorf("Token = %q, want %q", cfg.Telegram.Token, wantToken)
	}
	if cfg.Telegram.AuthPassphrase != wantPassphrase {
		t.Errorf("AuthPassphrase = %q, want %q", cfg.Telegram.AuthPassphrase, wantPassphrase)
	}
}

func TestLoadConfig_LiteralDollarPreserved(t *testing.T) {
	t.Parallel()

	content := `
telegram:
  token: "static-token"
  allowed_users:
    - 123456789
  auth_passphrase: "static-passphrase"

claude:
  binary: "/usr/bin/claude"
  system_prompt: "Use $HOME in examples and $USER for username"
`
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "craudinei.yaml")
	if err := os.WriteFile(configFile, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	os.Unsetenv("HOME")
	os.Unsetenv("USER")

	cfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Claude.SystemPrompt != "Use $HOME in examples and $USER for username" {
		t.Errorf("SystemPrompt = %q, want literal $ preserved", cfg.Claude.SystemPrompt)
	}
}

func TestExpandEnvVar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		env   map[string]string
		want  string
	}{
		{
			name:  "simple expansion",
			input: "${VAR}",
			env:   map[string]string{"VAR": "value"},
			want:  "value",
		},
		{
			name:  "no match returns original",
			input: "plain string",
			env:   map[string]string{},
			want:  "plain string",
		},
		{
			name:  "literal dollar preserved",
			input: "Use $HOME here",
			env:   map[string]string{},
			want:  "Use $HOME here",
		},
		{
			name:  "partial expansion only matches whole pattern",
			input: "${VAR} and $OTHER",
			env:   map[string]string{"VAR": "replaced"},
			want:  "replaced and $OTHER",
		},
		{
			name:  "undefined var expands to empty",
			input: "${UNDEFINED}",
			env:   map[string]string{},
			want:  "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				os.Setenv(k, v)
				defer os.Unsetenv(k)
			}
			got := expandEnvVar(tc.input)
			if got != tc.want {
				t.Errorf("expandEnvVar(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidate_MissingToken(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Telegram: TelegramConfig{
			AllowedUsers:   []int64{123456789},
			AuthPassphrase: "passphrase",
		},
		Claude: ClaudeConfig{
			Binary: "/usr/bin/claude",
		},
	}

	err := Validate(cfg)
	if err == nil {
		t.Error("Validate() expected error for missing token")
	}
}

func TestValidate_MissingAllowedUsers(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Telegram: TelegramConfig{
			Token:          "token",
			AllowedUsers:   []int64{},
			AuthPassphrase: "passphrase",
		},
		Claude: ClaudeConfig{
			Binary: "/usr/bin/claude",
		},
	}

	err := Validate(cfg)
	if err == nil {
		t.Error("Validate() expected error for missing allowed_users")
	}
}

func TestValidate_RelativeBinaryPath(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Telegram: TelegramConfig{
			Token:          "token",
			AllowedUsers:   []int64{123456789},
			AuthPassphrase: "passphrase",
		},
		Claude: ClaudeConfig{
			Binary: "relative/path/claude",
		},
	}

	err := Validate(cfg)
	if err == nil {
		t.Error("Validate() expected error for relative binary path")
	}
}

func TestValidate_AllowedPathsValidation(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	nonExistentPath := filepath.Join(tmpDir, "does-not-exist")

	cfg := &Config{
		Telegram: TelegramConfig{
			Token:          "token",
			AllowedUsers:   []int64{123456789},
			AuthPassphrase: "passphrase",
		},
		Claude: ClaudeConfig{
			Binary:       "/usr/bin/claude",
			AllowedPaths: []string{nonExistentPath},
		},
	}

	err := Validate(cfg)
	if err == nil {
		t.Error("Validate() expected error for non-existent allowed_paths entry")
	}
}

func TestValidate_EmptyAllowedPaths(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Telegram: TelegramConfig{
			Token:          "token",
			AllowedUsers:   []int64{123456789},
			AuthPassphrase: "passphrase",
		},
		Claude: ClaudeConfig{
			Binary:       "/usr/bin/claude",
			AllowedPaths: []string{},
		},
	}

	err := Validate(cfg)
	if err == nil {
		t.Error("Validate() expected error for empty allowed_paths")
	}
}

func TestValidateFilePermissions_Valid(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "craudinei.yaml")
	if err := os.WriteFile(configFile, []byte("test: data\n"), 0600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	err := ValidateFilePermissions(configFile)
	if err != nil {
		t.Errorf("ValidateFilePermissions() unexpected error: %v", err)
	}
}

func TestValidateFilePermissions_WorldReadable(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "craudinei.yaml")
	if err := os.WriteFile(configFile, []byte("test: data\n"), 0644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	err := ValidateFilePermissions(configFile)
	if err == nil {
		t.Error("ValidateFilePermissions() expected error for 0644 permissions")
	}
}

func TestValidateFilePermissions_GroupReadable(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "craudinei.yaml")
	if err := os.WriteFile(configFile, []byte("test: data\n"), 0660); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	err := ValidateFilePermissions(configFile)
	if err == nil {
		t.Error("ValidateFilePermissions() expected error for 0660 permissions")
	}
}

func TestValidate_AuthIdleTimeout(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Telegram: TelegramConfig{
			Token:           "token",
			AllowedUsers:    []int64{123456789},
			AuthPassphrase:  "passphrase",
			AuthIdleTimeout: 5 * time.Hour,
		},
		Claude: ClaudeConfig{
			Binary: "/usr/bin/claude",
		},
	}

	err := Validate(cfg)
	if err == nil {
		t.Error("Validate() expected error for AuthIdleTimeout > 4h")
	}
}

func TestValidateWorkDir_TraversalAttack(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	allowedDir := filepath.Join(tmpDir, "allowed")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	attackPath := filepath.Join(tmpDir, "allowed", "..", "..", "etc", "passwd")

	err := ValidateWorkDir(attackPath, []string{allowedDir})
	if err == nil {
		t.Error("ValidateWorkDir() expected error for traversal attack")
	}
}

func TestValidateWorkDir_PrefixAttack(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	allowedDir := filepath.Join(tmpDir, "dev")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	// This should NOT match /home/user/dev because of trailing slash check
	attackDir := filepath.Join(tmpDir, "dev-secrets")
	if err := os.MkdirAll(attackDir, 0755); err != nil {
		t.Fatalf("failed to create attack dir: %v", err)
	}

	err := ValidateWorkDir(attackDir, []string{allowedDir})
	if err == nil {
		t.Error("ValidateWorkDir() expected error for prefix attack (dev-secrets vs dev)")
	}
}

func TestValidateWorkDir_Valid(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	allowedDir := filepath.Join(tmpDir, "allowed", "project")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	subDir := filepath.Join(allowedDir, "subproject")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create sub dir: %v", err)
	}

	err := ValidateWorkDir(subDir, []string{allowedDir})
	if err != nil {
		t.Errorf("ValidateWorkDir() unexpected error: %v", err)
	}
}

func TestValidateWorkDir_ExactMatch(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	allowedDir := filepath.Join(tmpDir, "allowed")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	err := ValidateWorkDir(allowedDir, []string{allowedDir})
	if err != nil {
		t.Errorf("ValidateWorkDir() unexpected error for exact match: %v", err)
	}
}

func TestValidateWorkDir_NonExistentWorkDir(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	allowedDir := filepath.Join(tmpDir, "allowed")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	nonExistent := filepath.Join(tmpDir, "nonexistent")

	err := ValidateWorkDir(nonExistent, []string{allowedDir})
	if err == nil {
		t.Error("ValidateWorkDir() expected error for non-existent work dir")
	}
}

func TestReload_NonSensitiveFields(t *testing.T) {
	t.Parallel()

	content := `
telegram:
  token: "original-token"
  allowed_users:
    - 123456789
  auth_passphrase: "original-passphrase"
  progress_interval: 1m

claude:
  binary: "/usr/bin/claude"
  system_prompt: "Original prompt"
  max_turns: 50
  allowed_paths:
    - "/tmp"
`
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "craudinei.yaml")
	if err := os.WriteFile(configFile, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	originalCfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Update the file with new non-sensitive values
	newContent := `
telegram:
  token: "NEW-SHOULD-NOT-APPEAR"
  allowed_users:
    - 123456789
  auth_passphrase: "NEW-SHOULD-NOT-APPEAR"
  progress_interval: 3m

claude:
  binary: "/usr/bin/claude"
  system_prompt: "Updated prompt"
  max_turns: 100
  allowed_paths:
    - "/tmp"
`
	if err := os.WriteFile(configFile, []byte(newContent), 0600); err != nil {
		t.Fatalf("failed to write updated config: %v", err)
	}

	reloadedCfg, err := Reload(originalCfg)
	if err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	// Sensitive fields should be preserved
	if reloadedCfg.Telegram.Token != "original-token" {
		t.Errorf("Token was updated on reload, got %q, want original", reloadedCfg.Telegram.Token)
	}
	if reloadedCfg.Telegram.AuthPassphrase != "original-passphrase" {
		t.Errorf("AuthPassphrase was updated on reload, got %q, want original", reloadedCfg.Telegram.AuthPassphrase)
	}

	// Non-sensitive fields should be updated
	if reloadedCfg.Telegram.ProgressInterval != 3*time.Minute {
		t.Errorf("ProgressInterval = %v, want 3m", reloadedCfg.Telegram.ProgressInterval)
	}
	if reloadedCfg.Claude.SystemPrompt != "Updated prompt" {
		t.Errorf("SystemPrompt = %q, want updated", reloadedCfg.Claude.SystemPrompt)
	}
	if reloadedCfg.Claude.MaxTurns != 100 {
		t.Errorf("MaxTurns = %v, want 100", reloadedCfg.Claude.MaxTurns)
	}
}

func TestReload_ValidationError(t *testing.T) {
	t.Parallel()

	content := `
telegram:
  token: "test-token"
  allowed_users:
    - 123456789
  auth_passphrase: "test-passphrase"

claude:
  binary: "/usr/bin/claude"
  allowed_paths:
    - "/tmp"
`
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "craudinei.yaml")
	if err := os.WriteFile(configFile, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	originalCfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Write a config that will fail validation (allowed_paths entry does not exist)
	invalidContent := `
telegram:
  token: "test-token"
  allowed_users:
    - 123456789
  auth_passphrase: "test-passphrase"

claude:
  binary: "/usr/bin/claude"
  allowed_paths:
    - "/this/path/does/not/exist"
`
	if err := os.WriteFile(configFile, []byte(invalidContent), 0600); err != nil {
		t.Fatalf("failed to write invalid config: %v", err)
	}

	_, err = Reload(originalCfg)
	if err == nil {
		t.Error("Reload() expected error for invalid config")
	}

	// Original config should be unchanged
	if originalCfg.Telegram.Token != "test-token" {
		t.Errorf("Original config was modified, Token = %q", originalCfg.Telegram.Token)
	}
}

func TestReload_FilePathPreserved(t *testing.T) {
	t.Parallel()

	content := `
telegram:
  token: "test-token"
  allowed_users:
    - 123456789
  auth_passphrase: "test-passphrase"

claude:
  binary: "/usr/bin/claude"
`
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "craudinei.yaml")
	if err := os.WriteFile(configFile, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	cfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.FilePath() != configFile {
		t.Errorf("FilePath() = %q, want %q", cfg.FilePath(), configFile)
	}
}
