package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ─── Path Traversal: symlink escape via allowed paths ────────────────────────

func TestValidateWorkDir_SymlinkEscapeInAllowedPaths(t *testing.T) {
	t.Parallel()

	realDir := t.TempDir()
	realAllowed := filepath.Join(realDir, "allowed")
	if err := os.MkdirAll(realAllowed, 0755); err != nil {
		t.Fatal(err)
	}

	subDir := filepath.Join(realAllowed, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Symlink inside subDir → resolves to realDir (parent of realAllowed)
	escapeLink := filepath.Join(subDir, "escape_link")
	if err := os.Symlink(realDir, escapeLink); err != nil {
		t.Fatal(err)
	}

	// ValidateWorkDir resolves symlink and sees realDir is NOT under realAllowed
	err := ValidateWorkDir(escapeLink, []string{realAllowed})
	if err == nil {
		t.Error("expected error for symlink inside allowed that escapes via parent")
	}
}

func TestValidateWorkDir_SymlinkRaceOnWorkDir(t *testing.T) {
	t.Parallel()

	realDir := t.TempDir()
	realAllowed := filepath.Join(realDir, "allowed")
	if err := os.MkdirAll(realAllowed, 0755); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(realDir, "workdir_link")
	if err := os.Symlink(realAllowed, linkPath); err != nil {
		t.Fatal(err)
	}

	if err := ValidateWorkDir(linkPath, []string{realAllowed}); err != nil {
		t.Errorf("symlink to allowed should be valid initially: %v", err)
	}

	// Flip symlink to point outside
	escapeTarget := t.TempDir()
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escapeTarget, linkPath); err != nil {
		t.Fatal(err)
	}

	err := ValidateWorkDir(linkPath, []string{realAllowed})
	if err == nil {
		t.Error("expected error after symlink was flipped to escape target")
	}
}

// ─── File permissions: 0400 and 0700 ────────────────────────────────────────

func TestValidateFilePermissions_ReadOnlyOwner(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "cfg.yaml")
	if err := os.WriteFile(path, []byte("token: test\n"), 0400); err != nil {
		t.Fatal(err)
	}
	err := ValidateFilePermissions(path)
	// SECURITY NOTE: ValidateFilePermissions requires exactly 0600.
	// 0400 (read-only, owner) is more restrictive and arguably more secure,
	// but is currently rejected. This documents the conservatism gap.
	if err == nil {
		t.Error("expected error for 0400 (requires exactly 0600); SECURITY gap")
	}
}

func TestValidateFilePermissions_0700OwnerExec(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "cfg.yaml")
	if err := os.WriteFile(path, []byte("token: test\n"), 0700); err != nil {
		t.Fatal(err)
	}
	err := ValidateFilePermissions(path)
	if err == nil {
		t.Error("0700 should be rejected (requires exactly 0600)")
	}
}

// ─── Env var expansion edge cases ────────────────────────────────────────────

func TestExpandEnvVar_EmptyVarName(t *testing.T) {
	t.Parallel()

	// ${} with no variable name — pattern does not match so passes through
	result := expandEnvVar("${}")
	if result != "${}" {
		t.Errorf("expected ${} to pass through unchanged, got %q", result)
	}
}

// ─── Validate boundary cases ─────────────────────────────────────────────────

func TestValidate_AllowedPathsEntryIsFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "not_a_dir.txt")
	if err := os.WriteFile(tmpFile, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Telegram: TelegramConfig{
			Token:          "token",
			AllowedUsers:   []int64{123},
			AuthPassphrase: "passphrase",
		},
		Claude: ClaudeConfig{
			Binary:       "/bin/ls",
			AllowedPaths: []string{tmpFile},
		},
	}

	err := Validate(cfg)
	if err == nil {
		t.Error("expected error when allowed_paths entry is a file")
	}
}

func TestValidate_AuthIdleTimeoutAtBoundary(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfg := &Config{
		Telegram: TelegramConfig{
			Token:           "token",
			AllowedUsers:    []int64{123},
			AuthPassphrase:  "passphrase",
			AuthIdleTimeout: 4 * time.Hour,
		},
		Claude: ClaudeConfig{
			Binary:       "/bin/ls",
			AllowedPaths: []string{tmpDir},
		},
	}

	err := Validate(cfg)
	if err != nil {
		t.Errorf("exactly 4h should be valid, got: %v", err)
	}
}

func TestValidate_AuthIdleTimeoutJustOverLimit(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfg := &Config{
		Telegram: TelegramConfig{
			Token:           "token",
			AllowedUsers:    []int64{123},
			AuthPassphrase:  "passphrase",
			AuthIdleTimeout: 4*time.Hour + 1,
		},
		Claude: ClaudeConfig{
			Binary:       "/bin/ls",
			AllowedPaths: []string{tmpDir},
		},
	}

	err := Validate(cfg)
	if err == nil {
		t.Error("expected error for auth_idle_timeout just over 4h limit")
	}
}

func TestValidate_AuthIdleTimeoutNegative(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfg := &Config{
		Telegram: TelegramConfig{
			Token:           "token",
			AllowedUsers:    []int64{123},
			AuthPassphrase:  "passphrase",
			AuthIdleTimeout: -1 * time.Second,
		},
		Claude: ClaudeConfig{
			Binary:       "/bin/ls",
			AllowedPaths: []string{tmpDir},
		},
	}

	// Validate only checks > 4h, not < 0. This documents the gap.
	err := Validate(cfg)
	if err == nil {
		t.Log("WARNING: negative auth_idle_timeout is not rejected by Validate")
	}
}

// ─── Reload adversarial ───────────────────────────────────────────────────────

func TestReload_ConcurrentReloadFights(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "cfg.yaml")

	content := `
telegram:
  token: "test-token"
  allowed_users: [123]
  auth_passphrase: "test-passphrase"
claude:
  binary: "/bin/ls"
  allowed_paths: ["` + tmpDir + `"]
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, relErr := Reload(cfg)
			if relErr != nil {
				errs <- relErr
			}
		}()
	}
	wg.Wait()
	close(errs)

	var failCount int
	for e := range errs {
		t.Logf("concurrent reload error: %v", e)
		failCount++
	}
	if failCount > 0 {
		t.Errorf("%d concurrent reload errors occurred", failCount)
	}
}

func TestReload_MissingFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "vanished.yaml")
	if err := os.WriteFile(path, []byte("token: test\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	_, err = Reload(cfg)
	if err == nil {
		t.Error("expected error when backing file is missing at Reload time")
	}
}

func TestReload_InvalidYAML(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "cfg.yaml")

	content := `
telegram:
  token: "test-token"
  allowed_users: [123]
  auth_passphrase: "test-passphrase"
claude:
  binary: "/bin/ls"
  allowed_paths: ["` + tmpDir + `"]
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("invalid: [yaml: content\n"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err = Reload(cfg)
	if err == nil {
		t.Error("expected error when YAML is corrupted")
	}
}

func TestReload_InvalidatedConfigPreservesOriginal(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "cfg.yaml")

	content := `
telegram:
  token: "test-token"
  allowed_users: [123]
  auth_passphrase: "test-passphrase"
claude:
  binary: "/bin/ls"
  allowed_paths: ["` + tmpDir + `"]
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	originalCfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	invalidContent := `
telegram:
  token: "test-token"
  allowed_users: [123]
  auth_passphrase: "test-passphrase"
claude:
  binary: "/bin/ls"
  allowed_paths: ["/this/path/does/not/exist"]
`
	if err := os.WriteFile(path, []byte(invalidContent), 0600); err != nil {
		t.Fatal(err)
	}

	_, err = Reload(originalCfg)
	if err == nil {
		t.Error("expected Reload to fail with invalid allowed_paths")
	}

	if originalCfg.Telegram.Token != "test-token" {
		t.Errorf("original config was mutated, token is now %q", originalCfg.Telegram.Token)
	}
}

// ─── YAML injection / edge cases ─────────────────────────────────────────────

func TestLoad_YAMLMergeInjection(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "cfg.yaml")

	// YAML anchor/alias + merge key (<<) to inject or override fields
	yamlContent := `
telegram:
  token: &token "injected-token"
allowed_users: &users [999]
auth_passphrase: "injected-passphrase"
<<:
  *token
  *users
claude:
  binary: "/bin/ls"
  allowed_paths: ["` + tmpDir + `"]
`
	if err := os.WriteFile(path, []byte(yamlContent), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Logf("YAML merge injection parse error (may be acceptable): %v", err)
		return
	}
	_ = cfg
}

func TestLoad_ExtremelyLargeYAMLValue(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "cfg.yaml")

	// 1MB of repeated 'a' characters as token value
	largeVal := make([]byte, 1<<20)
	for i := range largeVal {
		largeVal[i] = 'a'
	}
	yamlContent := "token: \"" + string(largeVal) + "\"\n" +
		"allowed_users: [123]\n" +
		"auth_passphrase: \"test\"\n" +
		"claude:\n  binary: \"/bin/ls\"\n  allowed_paths: [\"" + tmpDir + "\"]\n"

	if err := os.WriteFile(path, []byte(yamlContent), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Errorf("1MB YAML value should parse without error, got: %v", err)
	}
	_ = cfg
}

func TestLoad_EmptyFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "empty.yaml")
	if err := os.WriteFile(path, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Errorf("empty file should parse (yaml.Unmarshal returns nil), got: %v", err)
	}
	if cfg != nil {
		if err := Validate(cfg); err == nil {
			t.Error("empty config should fail Validate (required fields missing)")
		}
	}
}

func TestLoad_OnlyYAMLComment(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "comment_only.yaml")
	if err := os.WriteFile(path, []byte("# only a comment\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Errorf("comment-only file should parse, got: %v", err)
	}
	if cfg != nil {
		if err := Validate(cfg); err == nil {
			t.Error("comment-only config should fail Validate")
		}
	}
}

func TestLoad_PermissionDenied(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "noperm.yaml")
	if err := os.WriteFile(path, []byte("token: test\n"), 0000); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("expected error when file has no read permission")
	}
}

func TestLoad_ThenValidate_MissingFields(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "cfg.yaml")

	yamlContent := `
telegram:
  token: ""
  allowed_users: []
  auth_passphrase: ""
claude:
  binary: "/bin/ls"
  allowed_paths: ["` + tmpDir + `"]
`
	if err := os.WriteFile(path, []byte(yamlContent), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load should succeed for empty-string fields, got: %v", err)
	}

	err = Validate(cfg)
	if err == nil {
		t.Error("expected Validate to fail for empty required fields")
	}
}

// ─── ValidateWorkDir trailing slash ───────────────────────────────────────────

func TestValidateWorkDir_TrailingSlashCoverage(t *testing.T) {
	t.Parallel()

	allowedDir := t.TempDir()
	subDir := filepath.Join(allowedDir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Subdirectory should be valid even without trailing slash on allowed
	err := ValidateWorkDir(subDir, []string{allowedDir})
	if err != nil {
		t.Errorf("subdirectory should be valid, got: %v", err)
	}
}
