# Craudinei Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go binary that bridges Claude Code CLI sessions with Telegram, enabling remote coding assistance and session monitoring.

**Architecture:** Single binary with four components — Telegram Bot, Event Router, Session Manager, and Approval Server — communicating via Go channels. Claude Code runs as a subprocess with `--input-format stream-json --output-format stream-json`, reading prompts from stdin and emitting NDJSON events on stdout.

**Tech Stack:** Go 1.22+, `github.com/go-telegram/bot` (Telegram), `gopkg.in/yaml.v3` (config), stdlib for everything else (os/exec, bufio, encoding/json, log/slog, net/http, sync, syscall).

**Spec:** `docs/superpowers/specs/2026-04-17-craudinei-design.md`

---

## Phase 1: Foundation

### Task 1: Project Scaffolding

**Files:**
- Create: `go.mod`
- Create: `Makefile`
- Create: `cmd/craudinei/main.go`
- Create: `.gitignore`

- [ ] **Step 1: Initialize Go module**

```bash
cd /home/ddebrito/dev/craudinei
go mod init github.com/ddebrito/craudinei
```

- [ ] **Step 2: Create Makefile**

Create `Makefile`:

```makefile
.PHONY: build run test clean

BINARY=craudinei
BUILD_DIR=./cmd/craudinei

build:
	go build -o $(BINARY) $(BUILD_DIR)

run: build
	./$(BINARY)

test:
	go test -race -v ./...

clean:
	rm -f $(BINARY)
```

- [ ] **Step 3: Create main.go skeleton**

Create `cmd/craudinei/main.go`:

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Println("craudinei starting...")
	return nil
}
```

- [ ] **Step 4: Create .gitignore**

Create `.gitignore`:

```
craudinei
*.pid
*.log
craudinei.yaml
.craudinei/
```

- [ ] **Step 5: Verify build**

```bash
make build
./craudinei
```

Expected: prints "craudinei starting..." and exits 0.

- [ ] **Step 6: Initialize git and commit**

```bash
git init
git add go.mod Makefile cmd/craudinei/main.go .gitignore
git commit -m "feat: project scaffolding"
```

---

### Task 2: Shared Types

**Files:**
- Create: `internal/types/types.go`
- Create: `internal/types/types_test.go`

- [ ] **Step 1: Write tests for SessionStatus constants and Event types**

Create `internal/types/types_test.go`:

```go
package types

import "testing"

func TestSessionStatusValues(t *testing.T) {
	statuses := []SessionStatus{
		StatusIdle,
		StatusStarting,
		StatusRunning,
		StatusWaitingApproval,
		StatusStopping,
		StatusCrashed,
	}
	seen := make(map[SessionStatus]bool)
	for _, s := range statuses {
		if seen[s] {
			t.Errorf("duplicate status: %s", s)
		}
		seen[s] = true
		if s.String() == "" {
			t.Errorf("empty string for status")
		}
	}
}

func TestEventTypeValues(t *testing.T) {
	types := []EventType{
		EventSystem,
		EventAssistant,
		EventUser,
		EventResult,
		EventRateLimit,
	}
	seen := make(map[EventType]bool)
	for _, et := range types {
		if seen[et] {
			t.Errorf("duplicate event type: %s", et)
		}
		seen[et] = true
	}
}

func TestContentBlockFiltering(t *testing.T) {
	blocks := []ContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "thinking", Thinking: "hmm"},
		{Type: "tool_use", ToolName: "Bash"},
		{Type: "text", Text: "world"},
	}

	textBlocks := FilterContentBlocks(blocks, "text")
	if len(textBlocks) != 2 {
		t.Errorf("expected 2 text blocks, got %d", len(textBlocks))
	}

	thinkingBlocks := FilterContentBlocks(blocks, "thinking")
	if len(thinkingBlocks) != 1 {
		t.Errorf("expected 1 thinking block, got %d", len(thinkingBlocks))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/types/...
```

Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Implement shared types**

Create `internal/types/types.go`:

```go
package types

import (
	"encoding/json"
	"sync"
	"time"
)

type SessionStatus string

const (
	StatusIdle            SessionStatus = "idle"
	StatusStarting        SessionStatus = "starting"
	StatusRunning         SessionStatus = "running"
	StatusWaitingApproval SessionStatus = "waiting_approval"
	StatusStopping        SessionStatus = "stopping"
	StatusCrashed         SessionStatus = "crashed"
)

func (s SessionStatus) String() string { return string(s) }

type EventType string

const (
	EventSystem    EventType = "system"
	EventAssistant EventType = "assistant"
	EventUser      EventType = "user"
	EventResult    EventType = "result"
	EventRateLimit EventType = "rate_limit"
)

type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
	ToolName string `json:"name,omitempty"`
	ToolID   string `json:"id,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

type Message struct {
	ID         string         `json:"id,omitempty"`
	Model      string         `json:"model,omitempty"`
	StopReason string         `json:"stop_reason,omitempty"`
	Content    []ContentBlock `json:"content,omitempty"`
	Role       string         `json:"role,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type Event struct {
	Type      EventType `json:"type"`
	Subtype   string    `json:"subtype,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Message   *Message  `json:"message,omitempty"`
	Result    string    `json:"result,omitempty"`
	IsError   bool      `json:"is_error,omitempty"`
	Usage     *Usage    `json:"usage,omitempty"`
	TotalCost float64   `json:"total_cost_usd,omitempty"`
	NumTurns  int       `json:"num_turns,omitempty"`

	// rate_limit fields
	RetryAfterSeconds int `json:"retry_after_seconds,omitempty"`

	// system/api_retry fields
	Attempt    int `json:"attempt,omitempty"`
	MaxRetries int `json:"max_retries,omitempty"`
}

type ToolCall struct {
	ID       string
	Name     string
	Input    json.RawMessage
	Handled  bool
	Decision string // "approved" or "denied"
}

type SessionState struct {
	mu              sync.Mutex
	status          SessionStatus
	sessionID       string
	workDir         string
	startedAt       time.Time
	lastActivity    time.Time
	pendingApproval *ToolCall
	totalCost       float64
	totalTokensIn   int
	totalTokensOut  int
	numTurns        int
}

func NewSessionState() *SessionState {
	return &SessionState{status: StatusIdle}
}

func (s *SessionState) Status() SessionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *SessionState) SetStatus(status SessionStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
	s.lastActivity = time.Now()
}

func (s *SessionState) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

func (s *SessionState) SetSessionID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = id
}

func (s *SessionState) WorkDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workDir
}

func (s *SessionState) StartSession(workDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = StatusStarting
	s.workDir = workDir
	s.startedAt = time.Now()
	s.lastActivity = time.Now()
	s.totalCost = 0
	s.totalTokensIn = 0
	s.totalTokensOut = 0
	s.numTurns = 0
	s.pendingApproval = nil
}

func (s *SessionState) LastActivity() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastActivity
}

func (s *SessionState) TouchActivity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastActivity = time.Now()
}

func (s *SessionState) StartedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startedAt
}

func (s *SessionState) SetPendingApproval(tc *ToolCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingApproval = tc
	if tc != nil {
		s.status = StatusWaitingApproval
	} else {
		s.status = StatusRunning
	}
	s.lastActivity = time.Now()
}

func (s *SessionState) PendingApproval() *ToolCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingApproval
}

func (s *SessionState) UpdateUsage(cost float64, tokensIn, tokensOut, turns int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalCost = cost
	s.totalTokensIn = tokensIn
	s.totalTokensOut = tokensOut
	s.numTurns = turns
	s.lastActivity = time.Now()
}

func (s *SessionState) Stats() (cost float64, tokensIn, tokensOut, turns int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.totalCost, s.totalTokensIn, s.totalTokensOut, s.numTurns
}

func FilterContentBlocks(blocks []ContentBlock, blockType string) []ContentBlock {
	var result []ContentBlock
	for _, b := range blocks {
		if b.Type == blockType {
			result = append(result, b)
		}
	}
	return result
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -race ./internal/types/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/types/
git commit -m "feat: shared types — Event, SessionState, ContentBlock"
```

---

### Task 3: Configuration

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/validate.go`
- Create: `internal/config/config_test.go`
- Create: `craudinei.yaml.example`

- [ ] **Step 1: Write config tests**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfig_Defaults(t *testing.T) {
	yaml := `
telegram:
  token: "test-token"
  allowed_users:
    - 123456789
  auth_passphrase: "test-pass"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telegram.Token != "test-token" {
		t.Errorf("expected token 'test-token', got %q", cfg.Telegram.Token)
	}
	if cfg.Telegram.AuthIdleTimeout != 1*time.Hour {
		t.Errorf("expected 1h default, got %v", cfg.Telegram.AuthIdleTimeout)
	}
	if cfg.Telegram.ApprovalTimeout != 5*time.Minute {
		t.Errorf("expected 5m default, got %v", cfg.Telegram.ApprovalTimeout)
	}
	if cfg.Telegram.ProgressInterval != 2*time.Minute {
		t.Errorf("expected 2m default, got %v", cfg.Telegram.ProgressInterval)
	}
	if cfg.Claude.MaxTurns != 50 {
		t.Errorf("expected 50 max turns, got %d", cfg.Claude.MaxTurns)
	}
	if cfg.Claude.MaxBudgetUSD != 5.0 {
		t.Errorf("expected 5.0 budget, got %f", cfg.Claude.MaxBudgetUSD)
	}
}

func TestLoadConfig_EnvVarExpansion(t *testing.T) {
	t.Setenv("TEST_TOKEN_VAR", "expanded-token")
	t.Setenv("TEST_PASS_VAR", "expanded-pass")

	yaml := `
telegram:
  token: "${TEST_TOKEN_VAR}"
  allowed_users:
    - 123456789
  auth_passphrase: "${TEST_PASS_VAR}"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telegram.Token != "expanded-token" {
		t.Errorf("expected 'expanded-token', got %q", cfg.Telegram.Token)
	}
	if cfg.Telegram.AuthPassphrase != "expanded-pass" {
		t.Errorf("expected 'expanded-pass', got %q", cfg.Telegram.AuthPassphrase)
	}
}

func TestLoadConfig_LiteralDollarPreserved(t *testing.T) {
	yaml := `
telegram:
  token: "my-token"
  allowed_users:
    - 123456789
  auth_passphrase: "pass"
claude:
  system_prompt: "Use $HOME in examples"
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Claude.SystemPrompt != "Use $HOME in examples" {
		t.Errorf("literal $ was expanded: got %q", cfg.Claude.SystemPrompt)
	}
}

func TestValidate_MissingToken(t *testing.T) {
	cfg := &Config{}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestValidate_MissingAllowedUsers(t *testing.T) {
	cfg := &Config{
		Telegram: TelegramConfig{Token: "tok", AuthPassphrase: "pass"},
	}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for missing allowed_users")
	}
}

func TestValidate_RelativeBinaryPath(t *testing.T) {
	cfg := &Config{
		Telegram: TelegramConfig{
			Token:          "tok",
			AllowedUsers:   []int64{123},
			AuthPassphrase: "pass",
		},
		Claude: ClaudeConfig{
			Binary: "claude",
		},
	}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for relative binary path")
	}
}

func TestValidate_AllowedPathsValidation(t *testing.T) {
	cfg := &Config{
		Telegram: TelegramConfig{
			Token:          "tok",
			AllowedUsers:   []int64{123},
			AuthPassphrase: "pass",
		},
		Claude: ClaudeConfig{
			Binary:       "/usr/bin/claude",
			AllowedPaths: []string{"/nonexistent/path/xyz"},
		},
	}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for nonexistent allowed path")
	}
}

func TestValidateWorkDir_TraversalAttack(t *testing.T) {
	tmpDir := t.TempDir()
	allowedPaths := []string{tmpDir}

	err := ValidateWorkDir(tmpDir+"/../../etc", allowedPaths)
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestValidateWorkDir_PrefixAttack(t *testing.T) {
	tmpDir := t.TempDir()
	secretsDir := tmpDir + "-secrets"
	os.MkdirAll(secretsDir, 0o755)
	defer os.RemoveAll(secretsDir)
	allowedPaths := []string{tmpDir}

	err := ValidateWorkDir(secretsDir, allowedPaths)
	if err == nil {
		t.Fatal("expected error for prefix attack")
	}
}

func TestValidateWorkDir_Valid(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "project")
	os.MkdirAll(subDir, 0o755)
	allowedPaths := []string{tmpDir}

	err := ValidateWorkDir(subDir, allowedPaths)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateWorkDir_ExactMatch(t *testing.T) {
	tmpDir := t.TempDir()
	allowedPaths := []string{tmpDir}

	err := ValidateWorkDir(tmpDir, allowedPaths)
	if err != nil {
		t.Fatalf("unexpected error for exact match: %v", err)
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "craudinei.yaml")
	err := os.WriteFile(path, []byte(content), 0o600)
	if err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}
	return path
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/config/...
```

Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement config loading**

Create `internal/config/config.go`:

```go
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type TelegramConfig struct {
	Token             string        `yaml:"token"`
	AllowedUsers      []int64       `yaml:"allowed_users"`
	AuthPassphrase    string        `yaml:"auth_passphrase"`
	AuthIdleTimeout   time.Duration `yaml:"auth_idle_timeout"`
	ApprovalTimeout   time.Duration `yaml:"approval_timeout"`
	ProgressInterval  time.Duration `yaml:"progress_interval"`
	InactivityTimeout time.Duration `yaml:"inactivity_timeout"`
}

type ClaudeConfig struct {
	Binary       string   `yaml:"binary"`
	DefaultWorkDir string `yaml:"default_workdir"`
	SystemPrompt string   `yaml:"system_prompt"`
	AllowedTools []string `yaml:"allowed_tools"`
	MaxTurns     int      `yaml:"max_turns"`
	MaxBudgetUSD float64  `yaml:"max_budget_usd"`
	AllowedPaths []string `yaml:"allowed_paths"`
}

type ApprovalConfig struct {
	Port         int           `yaml:"port"`
	TimeoutBash  time.Duration `yaml:"timeout_bash"`
	TimeoutEdit  time.Duration `yaml:"timeout_edit"`
	TimeoutWrite time.Duration `yaml:"timeout_write"`
}

type LoggingConfig struct {
	Level     string `yaml:"level"`
	File      string `yaml:"file"`
	AuditFile string `yaml:"audit_file"`
}

type Config struct {
	Telegram TelegramConfig `yaml:"telegram"`
	Claude   ClaudeConfig   `yaml:"claude"`
	Approval ApprovalConfig `yaml:"approval"`
	Logging  LoggingConfig  `yaml:"logging"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	expandSecrets(cfg)
	applyDefaults(cfg)

	return cfg, nil
}

func expandSecrets(cfg *Config) {
	cfg.Telegram.Token = expandEnvVar(cfg.Telegram.Token)
	cfg.Telegram.AuthPassphrase = expandEnvVar(cfg.Telegram.AuthPassphrase)
}

func expandEnvVar(s string) string {
	if strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") {
		varName := s[2 : len(s)-1]
		return os.Getenv(varName)
	}
	return s
}

func applyDefaults(cfg *Config) {
	if cfg.Telegram.AuthIdleTimeout == 0 {
		cfg.Telegram.AuthIdleTimeout = 1 * time.Hour
	}
	if cfg.Telegram.ApprovalTimeout == 0 {
		cfg.Telegram.ApprovalTimeout = 5 * time.Minute
	}
	if cfg.Telegram.ProgressInterval == 0 {
		cfg.Telegram.ProgressInterval = 2 * time.Minute
	}
	if cfg.Telegram.InactivityTimeout == 0 {
		cfg.Telegram.InactivityTimeout = 15 * time.Minute
	}
	if cfg.Claude.Binary == "" {
		cfg.Claude.Binary = "/usr/local/bin/claude"
	}
	if cfg.Claude.SystemPrompt == "" {
		cfg.Claude.SystemPrompt = "Keep each response under 4000 characters. Wrap lines at 80 characters."
	}
	if len(cfg.Claude.AllowedTools) == 0 {
		cfg.Claude.AllowedTools = []string{"Read", "Grep", "Glob"}
	}
	if cfg.Claude.MaxTurns == 0 {
		cfg.Claude.MaxTurns = 50
	}
	if cfg.Claude.MaxBudgetUSD == 0 {
		cfg.Claude.MaxBudgetUSD = 5.0
	}
	if cfg.Approval.TimeoutBash == 0 {
		cfg.Approval.TimeoutBash = 5 * time.Minute
	}
	if cfg.Approval.TimeoutEdit == 0 {
		cfg.Approval.TimeoutEdit = 5 * time.Minute
	}
	if cfg.Approval.TimeoutWrite == 0 {
		cfg.Approval.TimeoutWrite = 5 * time.Minute
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
}
```

- [ ] **Step 4: Implement validation**

Create `internal/config/validate.go`:

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Validate(cfg *Config) error {
	if cfg.Telegram.Token == "" {
		return fmt.Errorf("telegram.token is required")
	}
	if len(cfg.Telegram.AllowedUsers) == 0 {
		return fmt.Errorf("telegram.allowed_users is required (at least one user ID)")
	}
	if cfg.Telegram.AuthPassphrase == "" {
		return fmt.Errorf("telegram.auth_passphrase is required")
	}
	if cfg.Telegram.AuthIdleTimeout > 4*time.Hour {
		return fmt.Errorf("telegram.auth_idle_timeout cannot exceed 4 hours")
	}
	if !filepath.IsAbs(cfg.Claude.Binary) {
		return fmt.Errorf("claude.binary must be an absolute path (got %q)", cfg.Claude.Binary)
	}
	for _, p := range cfg.Claude.AllowedPaths {
		info, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("claude.allowed_paths: %q does not exist: %w", p, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("claude.allowed_paths: %q is not a directory", p)
		}
	}
	return nil
}

func ValidateWorkDir(dir string, allowedPaths []string) error {
	cleaned := filepath.Clean(dir)

	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("path does not exist: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", resolved)
	}

	for _, allowed := range allowedPaths {
		allowedResolved, err := filepath.EvalSymlinks(filepath.Clean(allowed))
		if err != nil {
			continue
		}
		if resolved == allowedResolved || strings.HasPrefix(resolved, allowedResolved+"/") {
			return nil
		}
	}

	return fmt.Errorf("path %q is not within allowed paths", dir)
}
```

- [ ] **Step 5: Install YAML dependency and run tests**

```bash
cd /home/ddebrito/dev/craudinei && go get gopkg.in/yaml.v3
go test -race ./internal/config/...
```

Expected: PASS.

- [ ] **Step 6: Create example config**

Create `craudinei.yaml.example`:

```yaml
# Craudinei Configuration
# Copy to craudinei.yaml and customize.
# File permissions must be 0600 (owner read/write only).

telegram:
  # Bot token from BotFather (required). Use env var reference.
  token: "${TELEGRAM_BOT_TOKEN}"

  # Telegram user IDs allowed to use this bot (required).
  # Find your ID via @userinfobot on Telegram.
  allowed_users:
    - 123456789

  # Passphrase for authentication (required). Use env var reference.
  auth_passphrase: "${CRAUDINEI_PASSPHRASE}"

  # Auth session idle timeout. Max 4 hours. Default: 1h.
  auth_idle_timeout: 1h

  # Timeout for tool approval buttons. Default: 5m.
  approval_timeout: 5m

  # Interval for "still working" progress notifications. Default: 2m.
  progress_interval: 2m

  # Inactivity watchdog timeout. Default: 15m.
  inactivity_timeout: 15m

claude:
  # Absolute path to the claude binary (required to be absolute).
  binary: "/usr/local/bin/claude"

  # Default working directory when /begin is used without arguments.
  default_workdir: "/home/user/dev"

  # System prompt appended to Claude Code's default prompt.
  system_prompt: "Keep each response under 4000 characters. Wrap lines at 80 characters."

  # Tools that are auto-approved (no Telegram confirmation needed).
  allowed_tools:
    - Read
    - Grep
    - Glob

  # Maximum turns per session. Default: 50.
  max_turns: 50

  # Maximum budget per session in USD (soft limit). Default: 5.00.
  max_budget_usd: 5.00

  # Directories where sessions can be started. Path traversal is blocked.
  allowed_paths:
    - "/home/user/dev"

approval:
  # Port for the localhost approval server. 0 = auto-assign. Default: 0.
  port: 0

  # Per-tool approval timeouts (override telegram.approval_timeout).
  timeout_bash: 5m
  timeout_edit: 5m
  timeout_write: 5m

logging:
  # Log level: debug, info, warn, error. Default: info.
  level: info

  # Operational log file path.
  file: "/var/log/craudinei.log"

  # Audit log file path (auth, commands, approvals). Cannot be disabled.
  audit_file: "/var/log/craudinei-audit.log"
```

- [ ] **Step 7: Commit**

```bash
git add internal/config/ craudinei.yaml.example go.mod go.sum
git commit -m "feat: config loading with env var expansion, validation, path traversal protection"
```

---

### Task 4: Audit Logging

**Files:**
- Create: `internal/audit/audit.go`
- Create: `internal/audit/audit_test.go`

- [ ] **Step 1: Write audit tests**

Create `internal/audit/audit_test.go`:

```go
package audit

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestAuditLog_AuthSuccess(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.New(slog.NewJSONHandler(&buf, nil)))

	logger.AuthAttempt(123456, true)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry["action"] != "auth_attempt" {
		t.Errorf("expected action 'auth_attempt', got %v", entry["action"])
	}
	if entry["user_id"].(float64) != 123456 {
		t.Errorf("expected user_id 123456, got %v", entry["user_id"])
	}
	if entry["success"] != true {
		t.Errorf("expected success true")
	}
}

func TestAuditLog_ToolApproval(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.New(slog.NewJSONHandler(&buf, nil)))

	logger.ToolDecision(123456, "Bash", "ls -la", "approved")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry["action"] != "tool_decision" {
		t.Errorf("expected action 'tool_decision', got %v", entry["action"])
	}
	if entry["tool"] != "Bash" {
		t.Errorf("expected tool 'Bash', got %v", entry["tool"])
	}
	if entry["outcome"] != "approved" {
		t.Errorf("expected outcome 'approved', got %v", entry["outcome"])
	}
}

func TestAuditLog_Command(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.New(slog.NewJSONHandler(&buf, nil)))

	logger.Command(123456, "/begin", "/home/user/dev")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry["action"] != "command" {
		t.Errorf("expected action 'command', got %v", entry["action"])
	}
	if entry["command"] != "/begin" {
		t.Errorf("expected command '/begin', got %v", entry["command"])
	}
}

func TestAuditLog_SessionEvent(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.New(slog.NewJSONHandler(&buf, nil)))

	logger.SessionEvent("started", "abc123", "/home/user/dev")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry["action"] != "session" {
		t.Errorf("expected action 'session', got %v", entry["action"])
	}
	if entry["event"] != "started" {
		t.Errorf("expected event 'started', got %v", entry["event"])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/audit/...
```

Expected: FAIL.

- [ ] **Step 3: Implement audit logger**

Create `internal/audit/audit.go`:

```go
package audit

import "log/slog"

type Logger struct {
	log *slog.Logger
}

func New(log *slog.Logger) *Logger {
	return &Logger{log: log}
}

func (l *Logger) AuthAttempt(userID int64, success bool) {
	l.log.Info("audit",
		"action", "auth_attempt",
		"user_id", userID,
		"success", success,
	)
}

func (l *Logger) Command(userID int64, command string, args string) {
	l.log.Info("audit",
		"action", "command",
		"user_id", userID,
		"command", command,
		"args", args,
	)
}

func (l *Logger) ToolDecision(userID int64, tool string, inputSummary string, outcome string) {
	l.log.Info("audit",
		"action", "tool_decision",
		"user_id", userID,
		"tool", tool,
		"input", inputSummary,
		"outcome", outcome,
	)
}

func (l *Logger) SessionEvent(event string, sessionID string, workDir string) {
	l.log.Info("audit",
		"action", "session",
		"event", event,
		"session_id", sessionID,
		"work_dir", workDir,
	)
}

func (l *Logger) UnauthorizedAccess(userID int64) {
	l.log.Warn("audit",
		"action", "unauthorized_access",
		"user_id", userID,
	)
}
```

- [ ] **Step 4: Run tests**

```bash
go test -race ./internal/audit/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/audit/
git commit -m "feat: structured audit logging for auth, commands, tool decisions, sessions"
```

---

### Task 5: Authentication

**Files:**
- Create: `internal/bot/auth.go`
- Create: `internal/bot/auth_test.go`

- [ ] **Step 1: Write auth tests**

Create `internal/bot/auth_test.go`:

```go
package bot

import (
	"testing"
	"time"
)

func TestAuth_WhitelistedUser(t *testing.T) {
	a := NewAuth([]int64{111, 222}, "secret", 1*time.Hour)

	if !a.IsWhitelisted(111) {
		t.Error("user 111 should be whitelisted")
	}
	if a.IsWhitelisted(999) {
		t.Error("user 999 should not be whitelisted")
	}
}

func TestAuth_CorrectPassphrase(t *testing.T) {
	a := NewAuth([]int64{111}, "secret", 1*time.Hour)

	ok, err := a.Authenticate(111, "secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("correct passphrase should authenticate")
	}
	if !a.IsAuthenticated(111) {
		t.Error("user should be authenticated after correct passphrase")
	}
}

func TestAuth_WrongPassphrase(t *testing.T) {
	a := NewAuth([]int64{111}, "secret", 1*time.Hour)

	ok, err := a.Authenticate(111, "wrong")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("wrong passphrase should not authenticate")
	}
	if a.IsAuthenticated(111) {
		t.Error("user should not be authenticated after wrong passphrase")
	}
}

func TestAuth_RateLimiting(t *testing.T) {
	a := NewAuth([]int64{111}, "secret", 1*time.Hour)

	for i := 0; i < 3; i++ {
		a.Authenticate(111, "wrong")
	}

	_, err := a.Authenticate(111, "secret")
	if err == nil {
		t.Error("expected rate limit error after 3 failures")
	}
}

func TestAuth_SessionExpiry(t *testing.T) {
	a := NewAuth([]int64{111}, "secret", 50*time.Millisecond)

	a.Authenticate(111, "secret")
	if !a.IsAuthenticated(111) {
		t.Fatal("user should be authenticated")
	}

	time.Sleep(100 * time.Millisecond)
	if a.IsAuthenticated(111) {
		t.Error("session should have expired")
	}
}

func TestAuth_SuccessResetsFailures(t *testing.T) {
	a := NewAuth([]int64{111}, "secret", 1*time.Hour)

	a.Authenticate(111, "wrong")
	a.Authenticate(111, "wrong")
	a.Authenticate(111, "secret") // success resets counter

	// should be able to fail again without hitting rate limit
	ok, err := a.Authenticate(111, "wrong")
	if err != nil {
		t.Error("should not be rate limited after success reset")
	}
	if ok {
		t.Error("wrong passphrase should fail")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/bot/...
```

Expected: FAIL.

- [ ] **Step 3: Implement auth**

Create `internal/bot/auth.go`:

```go
package bot

import (
	"fmt"
	"sync"
	"time"
)

type authSession struct {
	authenticatedAt time.Time
	lastActivity    time.Time
}

type authFailures struct {
	count    int
	firstAt  time.Time
	lockedAt time.Time
}

type Auth struct {
	mu             sync.Mutex
	whitelist      map[int64]bool
	passphrase     string
	idleTimeout    time.Duration
	sessions       map[int64]*authSession
	failures       map[int64]*authFailures
}

func NewAuth(allowedUsers []int64, passphrase string, idleTimeout time.Duration) *Auth {
	wl := make(map[int64]bool, len(allowedUsers))
	for _, id := range allowedUsers {
		wl[id] = true
	}
	return &Auth{
		whitelist:   wl,
		passphrase:  passphrase,
		idleTimeout: idleTimeout,
		sessions:    make(map[int64]*authSession),
		failures:    make(map[int64]*authFailures),
	}
}

func (a *Auth) IsWhitelisted(userID int64) bool {
	return a.whitelist[userID]
}

func (a *Auth) IsAuthenticated(userID int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	sess, ok := a.sessions[userID]
	if !ok {
		return false
	}
	if time.Since(sess.lastActivity) > a.idleTimeout {
		delete(a.sessions, userID)
		return false
	}
	return true
}

func (a *Auth) TouchSession(userID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if sess, ok := a.sessions[userID]; ok {
		sess.lastActivity = time.Now()
	}
}

func (a *Auth) Authenticate(userID int64, passphrase string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if f, ok := a.failures[userID]; ok {
		if !f.lockedAt.IsZero() {
			lockDuration := a.lockDuration(f.count)
			if time.Since(f.lockedAt) < lockDuration {
				remaining := lockDuration - time.Since(f.lockedAt)
				return false, fmt.Errorf("too many failed attempts, try again in %s", remaining.Round(time.Minute))
			}
			// lockout expired, reset
			delete(a.failures, userID)
		}
	}

	if passphrase != a.passphrase {
		f, ok := a.failures[userID]
		if !ok {
			f = &authFailures{firstAt: time.Now()}
			a.failures[userID] = f
		}
		f.count++

		if f.count >= 3 && time.Since(f.firstAt) <= 5*time.Minute {
			f.lockedAt = time.Now()
		}
		return false, nil
	}

	delete(a.failures, userID)
	now := time.Now()
	a.sessions[userID] = &authSession{
		authenticatedAt: now,
		lastActivity:    now,
	}
	return true, nil
}

func (a *Auth) lockDuration(failCount int) time.Duration {
	switch {
	case failCount < 6:
		return 30 * time.Minute
	case failCount < 9:
		return 1 * time.Hour
	default:
		return 4 * time.Hour
	}
}

func (a *Auth) RevokeAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions = make(map[int64]*authSession)
}
```

- [ ] **Step 4: Run tests**

```bash
go test -race ./internal/bot/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bot/auth.go internal/bot/auth_test.go
git commit -m "feat: auth with user ID whitelist, passphrase, rate limiting, session expiry"
```

---

## Phase 2: Core Engine

### Task 6: State Machine

**Files:**
- Create: `internal/claude/state.go`
- Create: `internal/claude/state_test.go`

- [ ] **Step 1: Write state machine tests**

Create `internal/claude/state_test.go`:

```go
package claude

import (
	"testing"

	"github.com/ddebrito/craudinei/internal/types"
)

func TestTransition_IdleToStarting(t *testing.T) {
	sm := NewStateMachine()
	if err := sm.Transition(types.StatusStarting); err != nil {
		t.Fatalf("idle->starting should succeed: %v", err)
	}
}

func TestTransition_IdleToRunning_Invalid(t *testing.T) {
	sm := NewStateMachine()
	if err := sm.Transition(types.StatusRunning); err == nil {
		t.Fatal("idle->running should fail (must go through starting)")
	}
}

func TestTransition_RunningToWaitingApproval(t *testing.T) {
	sm := NewStateMachine()
	sm.Transition(types.StatusStarting)
	sm.Transition(types.StatusRunning)
	if err := sm.Transition(types.StatusWaitingApproval); err != nil {
		t.Fatalf("running->waiting_approval should succeed: %v", err)
	}
}

func TestTransition_WaitingApprovalToRunning(t *testing.T) {
	sm := NewStateMachine()
	sm.Transition(types.StatusStarting)
	sm.Transition(types.StatusRunning)
	sm.Transition(types.StatusWaitingApproval)
	if err := sm.Transition(types.StatusRunning); err != nil {
		t.Fatalf("waiting_approval->running should succeed: %v", err)
	}
}

func TestTransition_RunningToStopping(t *testing.T) {
	sm := NewStateMachine()
	sm.Transition(types.StatusStarting)
	sm.Transition(types.StatusRunning)
	if err := sm.Transition(types.StatusStopping); err != nil {
		t.Fatalf("running->stopping should succeed: %v", err)
	}
}

func TestTransition_StoppingToIdle(t *testing.T) {
	sm := NewStateMachine()
	sm.Transition(types.StatusStarting)
	sm.Transition(types.StatusRunning)
	sm.Transition(types.StatusStopping)
	if err := sm.Transition(types.StatusIdle); err != nil {
		t.Fatalf("stopping->idle should succeed: %v", err)
	}
}

func TestTransition_RunningToCrashed(t *testing.T) {
	sm := NewStateMachine()
	sm.Transition(types.StatusStarting)
	sm.Transition(types.StatusRunning)
	if err := sm.Transition(types.StatusCrashed); err != nil {
		t.Fatalf("running->crashed should succeed: %v", err)
	}
}

func TestTransition_CrashedToStarting(t *testing.T) {
	sm := NewStateMachine()
	sm.Transition(types.StatusStarting)
	sm.Transition(types.StatusRunning)
	sm.Transition(types.StatusCrashed)
	if err := sm.Transition(types.StatusStarting); err != nil {
		t.Fatalf("crashed->starting should succeed: %v", err)
	}
}

func TestTransition_StoppingToRunning_Invalid(t *testing.T) {
	sm := NewStateMachine()
	sm.Transition(types.StatusStarting)
	sm.Transition(types.StatusRunning)
	sm.Transition(types.StatusStopping)
	if err := sm.Transition(types.StatusRunning); err == nil {
		t.Fatal("stopping->running should fail")
	}
}

func TestCanExecuteCommand(t *testing.T) {
	sm := NewStateMachine()

	tests := []struct {
		status  types.SessionStatus
		command string
		valid   bool
	}{
		{types.StatusIdle, "/begin", true},
		{types.StatusIdle, "/stop", false},
		{types.StatusIdle, "/resume", true},
		{types.StatusIdle, "/status", true},
		{types.StatusIdle, "/help", true},
		{types.StatusRunning, "/begin", false},
		{types.StatusRunning, "/stop", true},
		{types.StatusRunning, "/cancel", true},
		{types.StatusRunning, "/status", true},
		{types.StatusStarting, "/begin", false},
		{types.StatusStarting, "/status", true},
		{types.StatusStopping, "/begin", false},
		{types.StatusStopping, "/status", true},
		{types.StatusCrashed, "/begin", true},
		{types.StatusCrashed, "/resume", true},
		{types.StatusCrashed, "/stop", false},
	}

	for _, tt := range tests {
		sm.forceStatus(tt.status)
		err := sm.CanExecuteCommand(tt.command)
		if tt.valid && err != nil {
			t.Errorf("%s in %s: expected valid, got error: %v", tt.command, tt.status, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("%s in %s: expected error, got nil", tt.command, tt.status)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/claude/...
```

Expected: FAIL.

- [ ] **Step 3: Implement state machine**

Create `internal/claude/state.go`:

```go
package claude

import (
	"fmt"
	"sync"

	"github.com/ddebrito/craudinei/internal/types"
)

var validTransitions = map[types.SessionStatus][]types.SessionStatus{
	types.StatusIdle:            {types.StatusStarting},
	types.StatusStarting:        {types.StatusRunning, types.StatusCrashed, types.StatusIdle},
	types.StatusRunning:         {types.StatusWaitingApproval, types.StatusStopping, types.StatusCrashed},
	types.StatusWaitingApproval: {types.StatusRunning, types.StatusStopping, types.StatusCrashed},
	types.StatusStopping:        {types.StatusIdle, types.StatusCrashed},
	types.StatusCrashed:         {types.StatusStarting},
}

var commandsByStatus = map[types.SessionStatus]map[string]bool{
	types.StatusIdle:            {"/begin": true, "/resume": true, "/status": true, "/help": true, "/sessions": true, "/reload": true},
	types.StatusStarting:        {"/status": true, "/help": true},
	types.StatusRunning:         {"/stop": true, "/cancel": true, "/status": true, "/help": true},
	types.StatusWaitingApproval: {"/stop": true, "/cancel": true, "/status": true, "/help": true},
	types.StatusStopping:        {"/status": true, "/help": true},
	types.StatusCrashed:         {"/begin": true, "/resume": true, "/status": true, "/help": true, "/sessions": true},
}

var commandErrors = map[string]map[types.SessionStatus]string{
	"/begin": {
		types.StatusStarting:        "Session is starting, please wait",
		types.StatusRunning:         "Session already active. /stop first",
		types.StatusWaitingApproval: "Session active, waiting for approval",
		types.StatusStopping:        "Session is stopping, please wait",
	},
	"/stop": {
		types.StatusIdle:    "No active session",
		types.StatusCrashed: "Session already ended",
	},
}

type StateMachine struct {
	mu     sync.Mutex
	status types.SessionStatus
}

func NewStateMachine() *StateMachine {
	return &StateMachine{status: types.StatusIdle}
}

func (sm *StateMachine) Status() types.SessionStatus {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.status
}

func (sm *StateMachine) Transition(to types.SessionStatus) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	allowed := validTransitions[sm.status]
	for _, valid := range allowed {
		if valid == to {
			sm.status = to
			return nil
		}
	}
	return fmt.Errorf("invalid transition: %s -> %s", sm.status, to)
}

func (sm *StateMachine) CanExecuteCommand(cmd string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if commands, ok := commandsByStatus[sm.status]; ok {
		if commands[cmd] {
			return nil
		}
	}

	if errors, ok := commandErrors[cmd]; ok {
		if msg, ok := errors[sm.status]; ok {
			return fmt.Errorf("%s", msg)
		}
	}

	return fmt.Errorf("command %s not available in state %s", cmd, sm.status)
}

func (sm *StateMachine) forceStatus(s types.SessionStatus) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.status = s
}
```

- [ ] **Step 4: Run tests**

```bash
go test -race ./internal/claude/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/claude/
git commit -m "feat: session state machine with transition guards and command validation"
```

---

### Task 7: Input Queue

**Files:**
- Create: `internal/claude/inputqueue.go`
- Create: `internal/claude/inputqueue_test.go`

- [ ] **Step 1: Write input queue tests**

Create `internal/claude/inputqueue_test.go`:

```go
package claude

import (
	"context"
	"testing"
	"time"
)

func TestInputQueue_EnqueueDequeue(t *testing.T) {
	q := NewInputQueue(5)

	if err := q.Enqueue("hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	msg, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "hello" {
		t.Errorf("expected 'hello', got %q", msg)
	}
}

func TestInputQueue_Full(t *testing.T) {
	q := NewInputQueue(2)

	q.Enqueue("one")
	q.Enqueue("two")

	err := q.Enqueue("three")
	if err == nil {
		t.Fatal("expected error when queue is full")
	}
}

func TestInputQueue_Order(t *testing.T) {
	q := NewInputQueue(5)

	q.Enqueue("first")
	q.Enqueue("second")
	q.Enqueue("third")

	ctx := context.Background()
	msg, _ := q.Dequeue(ctx)
	if msg != "first" {
		t.Errorf("expected 'first', got %q", msg)
	}
	msg, _ = q.Dequeue(ctx)
	if msg != "second" {
		t.Errorf("expected 'second', got %q", msg)
	}
}

func TestInputQueue_DequeueBlocksUntilAvailable(t *testing.T) {
	q := NewInputQueue(5)

	go func() {
		time.Sleep(50 * time.Millisecond)
		q.Enqueue("delayed")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	msg, err := q.Dequeue(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "delayed" {
		t.Errorf("expected 'delayed', got %q", msg)
	}
}

func TestInputQueue_DequeueContextCancelled(t *testing.T) {
	q := NewInputQueue(5)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := q.Dequeue(ctx)
	if err == nil {
		t.Fatal("expected error when context is cancelled")
	}
}

func TestInputQueue_Len(t *testing.T) {
	q := NewInputQueue(5)

	if q.Len() != 0 {
		t.Errorf("expected len 0, got %d", q.Len())
	}

	q.Enqueue("one")
	q.Enqueue("two")
	if q.Len() != 2 {
		t.Errorf("expected len 2, got %d", q.Len())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/claude/...
```

Expected: FAIL (new tests fail, existing state tests still pass).

- [ ] **Step 3: Implement input queue**

Create `internal/claude/inputqueue.go`:

```go
package claude

import (
	"context"
	"fmt"
)

type InputQueue struct {
	ch       chan string
	capacity int
}

func NewInputQueue(capacity int) *InputQueue {
	return &InputQueue{
		ch:       make(chan string, capacity),
		capacity: capacity,
	}
}

func (q *InputQueue) Enqueue(msg string) error {
	select {
	case q.ch <- msg:
		return nil
	default:
		return fmt.Errorf("queue full (%d/%d)", len(q.ch), q.capacity)
	}
}

func (q *InputQueue) Dequeue(ctx context.Context) (string, error) {
	select {
	case msg := <-q.ch:
		return msg, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (q *InputQueue) Len() int {
	return len(q.ch)
}
```

- [ ] **Step 4: Run tests**

```bash
go test -race ./internal/claude/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/claude/inputqueue.go internal/claude/inputqueue_test.go
git commit -m "feat: bounded input queue with blocking dequeue and context cancellation"
```

---

### Task 8: Event Router

**Files:**
- Create: `internal/router/events.go`
- Create: `internal/router/router.go`
- Create: `internal/router/router_test.go`

- [ ] **Step 1: Write router tests**

Create `internal/router/router_test.go`:

```go
package router

import (
	"strings"
	"testing"

	"github.com/ddebrito/craudinei/internal/types"
)

func TestParseEvent_SystemInit(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"sess-123","tools":["Bash","Read"]}`
	ev, err := ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Type != types.EventSystem {
		t.Errorf("expected system, got %s", ev.Type)
	}
	if ev.Subtype != "init" {
		t.Errorf("expected init, got %s", ev.Subtype)
	}
	if ev.SessionID != "sess-123" {
		t.Errorf("expected sess-123, got %s", ev.SessionID)
	}
}

func TestParseEvent_AssistantText(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"hello world"}]}}`
	ev, err := ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Type != types.EventAssistant {
		t.Errorf("expected assistant, got %s", ev.Type)
	}
	texts := types.FilterContentBlocks(ev.Message.Content, "text")
	if len(texts) != 1 || texts[0].Text != "hello world" {
		t.Errorf("unexpected text content: %v", texts)
	}
}

func TestParseEvent_AssistantThinking(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"let me think"},{"type":"text","text":"answer"}]}}`
	ev, err := ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	thinking := types.FilterContentBlocks(ev.Message.Content, "thinking")
	if len(thinking) != 1 {
		t.Errorf("expected 1 thinking block, got %d", len(thinking))
	}
}

func TestParseEvent_ToolUse(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","id":"tool-1","input":{"command":"ls -la"}}]}}`
	ev, err := ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools := types.FilterContentBlocks(ev.Message.Content, "tool_use")
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool_use, got %d", len(tools))
	}
	if tools[0].ToolName != "Bash" {
		t.Errorf("expected Bash, got %s", tools[0].ToolName)
	}
}

func TestParseEvent_ResultSuccess(t *testing.T) {
	line := `{"type":"result","subtype":"success","result":"done","total_cost_usd":0.05,"usage":{"input_tokens":500,"output_tokens":200},"num_turns":3}`
	ev, err := ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Type != types.EventResult {
		t.Errorf("expected result, got %s", ev.Type)
	}
	if ev.TotalCost != 0.05 {
		t.Errorf("expected cost 0.05, got %f", ev.TotalCost)
	}
}

func TestParseEvent_RateLimit(t *testing.T) {
	line := `{"type":"rate_limit","retry_after_seconds":30}`
	ev, err := ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Type != types.EventRateLimit {
		t.Errorf("expected rate_limit, got %s", ev.Type)
	}
	if ev.RetryAfterSeconds != 30 {
		t.Errorf("expected 30s retry, got %d", ev.RetryAfterSeconds)
	}
}

func TestParseEvent_InvalidJSON(t *testing.T) {
	_, err := ParseEvent([]byte(`{invalid json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestClassifyEvent(t *testing.T) {
	allowedTools := map[string]bool{"Read": true, "Grep": true, "Glob": true}

	tests := []struct {
		name     string
		json     string
		expected Action
	}{
		{
			"system_init",
			`{"type":"system","subtype":"init","session_id":"s1"}`,
			ActionSessionStarted,
		},
		{
			"assistant_text",
			`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`,
			ActionSendText,
		},
		{
			"assistant_thinking_only",
			`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hmm"}]}}`,
			ActionIgnore,
		},
		{
			"tool_use_allowed",
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","id":"t1"}]}}`,
			ActionToolAutoApproved,
		},
		{
			"tool_use_needs_approval",
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","id":"t1","input":{"command":"rm -rf /"}}]}}`,
			ActionToolNeedsApproval,
		},
		{
			"result_success",
			`{"type":"result","subtype":"success","total_cost_usd":0.1}`,
			ActionSessionCompleted,
		},
		{
			"result_error",
			`{"type":"result","subtype":"error_max_turns"}`,
			ActionSessionError,
		},
		{
			"api_retry_low",
			`{"type":"system","subtype":"api_retry","attempt":1,"max_retries":10}`,
			ActionIgnore,
		},
		{
			"api_retry_high",
			`{"type":"system","subtype":"api_retry","attempt":3,"max_retries":10}`,
			ActionNotifyRetry,
		},
		{
			"rate_limit",
			`{"type":"rate_limit","retry_after_seconds":30}`,
			ActionNotifyRateLimit,
		},
		{
			"compact_boundary",
			`{"type":"system","subtype":"compact_boundary"}`,
			ActionIgnore,
		},
		{
			"user_tool_result",
			`{"type":"user","message":{"role":"user"}}`,
			ActionIgnore,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := ParseEvent([]byte(tt.json))
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			action := ClassifyEvent(ev, allowedTools)
			if action != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, action)
			}
		})
	}
}

func TestClassifyEvent_MixedTextAndToolUse(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"I will edit"},{"type":"tool_use","name":"Edit","id":"t1","input":{"file":"a.go"}}]}}`
	ev, err := ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	allowedTools := map[string]bool{"Read": true}
	action := ClassifyEvent(ev, allowedTools)
	if action != ActionToolNeedsApproval {
		t.Errorf("mixed text+tool_use with unapproved tool should need approval, got %s", action)
	}
}

func TestExtractText(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"hello"},{"type":"text","text":" world"}]}}`
	ev, err := ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	text := ExtractText(ev)
	if !strings.Contains(text, "hello") || !strings.Contains(text, "world") {
		t.Errorf("expected text blocks joined, got %q", text)
	}
	if strings.Contains(text, "hmm") {
		t.Error("thinking blocks should not be in extracted text")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/router/...
```

Expected: FAIL.

- [ ] **Step 3: Implement event types**

Create `internal/router/events.go`:

```go
package router

type Action string

const (
	ActionSessionStarted    Action = "session_started"
	ActionSendText          Action = "send_text"
	ActionToolAutoApproved  Action = "tool_auto_approved"
	ActionToolNeedsApproval Action = "tool_needs_approval"
	ActionSessionCompleted  Action = "session_completed"
	ActionSessionError      Action = "session_error"
	ActionNotifyRetry       Action = "notify_retry"
	ActionNotifyRateLimit   Action = "notify_rate_limit"
	ActionIgnore            Action = "ignore"
)
```

- [ ] **Step 4: Implement router**

Create `internal/router/router.go`:

```go
package router

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ddebrito/craudinei/internal/types"
)

func ParseEvent(data []byte) (*types.Event, error) {
	var ev types.Event
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, fmt.Errorf("parsing event: %w", err)
	}
	return &ev, nil
}

func ClassifyEvent(ev *types.Event, allowedTools map[string]bool) Action {
	switch ev.Type {
	case types.EventSystem:
		return classifySystem(ev)
	case types.EventAssistant:
		return classifyAssistant(ev, allowedTools)
	case types.EventResult:
		return classifyResult(ev)
	case types.EventRateLimit:
		return ActionNotifyRateLimit
	case types.EventUser:
		return ActionIgnore
	default:
		return ActionIgnore
	}
}

func classifySystem(ev *types.Event) Action {
	switch ev.Subtype {
	case "init":
		return ActionSessionStarted
	case "api_retry":
		if ev.Attempt > 2 {
			return ActionNotifyRetry
		}
		return ActionIgnore
	case "compact_boundary":
		return ActionIgnore
	default:
		return ActionIgnore
	}
}

func classifyAssistant(ev *types.Event, allowedTools map[string]bool) Action {
	if ev.Message == nil {
		return ActionIgnore
	}

	toolBlocks := types.FilterContentBlocks(ev.Message.Content, "tool_use")
	if len(toolBlocks) > 0 {
		for _, tb := range toolBlocks {
			if !allowedTools[tb.ToolName] {
				return ActionToolNeedsApproval
			}
		}
		return ActionToolAutoApproved
	}

	textBlocks := types.FilterContentBlocks(ev.Message.Content, "text")
	if len(textBlocks) > 0 {
		return ActionSendText
	}

	return ActionIgnore
}

func classifyResult(ev *types.Event) Action {
	if ev.Subtype == "success" {
		return ActionSessionCompleted
	}
	return ActionSessionError
}

func ExtractText(ev *types.Event) string {
	if ev.Message == nil {
		return ""
	}
	textBlocks := types.FilterContentBlocks(ev.Message.Content, "text")
	var parts []string
	for _, b := range textBlocks {
		parts = append(parts, b.Text)
	}
	return strings.Join(parts, "")
}
```

- [ ] **Step 5: Run tests**

```bash
go test -race ./internal/router/...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/router/
git commit -m "feat: event router with NDJSON parsing, classification, and text extraction"
```

---

### Task 9: HTML Renderer

**Files:**
- Create: `internal/bot/renderer.go`
- Create: `internal/bot/renderer_test.go`

- [ ] **Step 1: Write renderer tests**

Create `internal/bot/renderer_test.go`:

```go
package bot

import (
	"strings"
	"testing"
)

func TestEscapeHTML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"<script>", "&lt;script&gt;"},
		{"a & b", "a &amp; b"},
		{"1 < 2 > 0", "1 &lt; 2 &gt; 0"},
		{"", ""},
	}
	for _, tt := range tests {
		result := EscapeHTML(tt.input)
		if result != tt.expected {
			t.Errorf("EscapeHTML(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestChunkMessage_Short(t *testing.T) {
	msg := "Hello, world!"
	chunks := ChunkMessage(msg, 4096)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != msg {
		t.Errorf("chunk mismatch")
	}
}

func TestChunkMessage_SplitsAtParagraph(t *testing.T) {
	part1 := strings.Repeat("a", 2000)
	part2 := strings.Repeat("b", 2000)
	msg := part1 + "\n\n" + part2
	chunks := ChunkMessage(msg, 2500)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
}

func TestChunkMessage_PreservesCodeBlock(t *testing.T) {
	code := "<pre><code>" + strings.Repeat("x", 100) + "</code></pre>"
	msg := "Before\n\n" + code + "\n\nAfter"
	chunks := ChunkMessage(msg, 4096)
	for _, chunk := range chunks {
		opens := strings.Count(chunk, "<pre>")
		closes := strings.Count(chunk, "</pre>")
		if opens != closes {
			t.Errorf("unbalanced pre tags in chunk: opens=%d closes=%d", opens, closes)
		}
	}
}

func TestChunkMessage_VeryLongSingleLine(t *testing.T) {
	msg := strings.Repeat("x", 5000)
	chunks := ChunkMessage(msg, 4096)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if len(chunk) > 4096 {
			t.Errorf("chunk exceeds limit: %d", len(chunk))
		}
	}
}

func TestFormatCodeBlock(t *testing.T) {
	code := "func main() {\n\tfmt.Println(\"hello\")\n}"
	result := FormatCodeBlock(code, "go")
	if !strings.Contains(result, "<pre>") {
		t.Error("missing <pre> tag")
	}
	if !strings.Contains(result, `class="language-go"`) {
		t.Error("missing language class")
	}
	if !strings.Contains(result, "&quot;") {
		t.Error("quotes should be escaped inside code")
	}
}

func TestShouldSendAsFile(t *testing.T) {
	short := strings.Repeat("x", 10000)
	if ShouldSendAsFile(short) {
		t.Error("10K chars should not be sent as file")
	}

	long := strings.Repeat("x", 20000)
	if !ShouldSendAsFile(long) {
		t.Error("20K chars should be sent as file")
	}
}

func TestSanitizeInput(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"hello\x00world", "helloworld"},
		{"tab\there", "tab\there"},
		{"newline\nhere", "newline\nhere"},
		{"\x01\x02\x03clean", "clean"},
	}
	for _, tt := range tests {
		result := SanitizeInput(tt.input)
		if result != tt.expected {
			t.Errorf("SanitizeInput(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestSanitizeInput_MaxLength(t *testing.T) {
	long := strings.Repeat("a", 5000)
	result := SanitizeInput(long)
	if len(result) > 4096 {
		t.Errorf("expected max 4096 chars, got %d", len(result))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/bot/... -run TestEscape\|TestChunk\|TestFormat\|TestShould\|TestSanitize
```

Expected: FAIL.

- [ ] **Step 3: Implement renderer**

Create `internal/bot/renderer.go`:

```go
package bot

import (
	"fmt"
	"strings"
)

const (
	maxMessageLen = 4096
	fileThreshold = 16384
)

func EscapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

func FormatCodeBlock(code, language string) string {
	escaped := EscapeHTML(code)
	if language != "" {
		return fmt.Sprintf("<pre><code class=\"language-%s\">%s</code></pre>", language, escaped)
	}
	return fmt.Sprintf("<pre><code>%s</code></pre>", escaped)
}

func ChunkMessage(msg string, limit int) []string {
	if len(msg) <= limit {
		return []string{msg}
	}

	var chunks []string
	remaining := msg

	for len(remaining) > 0 {
		if len(remaining) <= limit {
			chunks = append(chunks, remaining)
			break
		}

		cutPoint := findCutPoint(remaining, limit)
		chunk := remaining[:cutPoint]
		chunk = balancePreTags(chunk)
		chunks = append(chunks, chunk)
		remaining = reopenPreTags(remaining[cutPoint:], chunk)
	}

	return chunks
}

func findCutPoint(s string, limit int) int {
	// Try paragraph boundary
	if idx := strings.LastIndex(s[:limit], "\n\n"); idx > 0 {
		return idx + 2
	}
	// Try line boundary
	if idx := strings.LastIndex(s[:limit], "\n"); idx > 0 {
		return idx + 1
	}
	// Hard cut
	return limit
}

func balancePreTags(chunk string) string {
	opens := strings.Count(chunk, "<pre>")
	closes := strings.Count(chunk, "</pre>")
	for opens > closes {
		chunk += "</pre>"
		closes++
	}
	return chunk
}

func reopenPreTags(remaining string, prevChunk string) string {
	opens := strings.Count(prevChunk, "<pre>")
	closes := strings.Count(prevChunk, "</pre>")
	unclosed := opens - closes
	if unclosed > 0 {
		// The prevChunk had unclosed <pre> tags that we force-closed,
		// so reopen in the next chunk
		remaining = "<pre>" + remaining
	}
	return remaining
}

func ShouldSendAsFile(content string) bool {
	return len(content) > fileThreshold
}

func SanitizeInput(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' || r >= 0x20 {
			b.WriteRune(r)
		}
	}
	result := b.String()
	if len(result) > 4096 {
		result = result[:4096]
	}
	return result
}
```

- [ ] **Step 4: Run tests**

```bash
go test -race ./internal/bot/... -run TestEscape\|TestChunk\|TestFormat\|TestShould\|TestSanitize
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bot/renderer.go internal/bot/renderer_test.go
git commit -m "feat: HTML renderer with chunking, code blocks, input sanitization"
```

---

### Task 10: Session Manager

**Files:**
- Create: `internal/claude/manager.go`
- Create: `internal/claude/manager_test.go`
- Create: `internal/claude/testdata/mock_claude.sh`

- [ ] **Step 1: Create mock Claude script for testing**

Create `internal/claude/testdata/mock_claude.sh`:

```bash
#!/bin/bash
# Mock Claude Code subprocess for testing.
# Emits canned NDJSON events and reads stdin.

echo '{"type":"system","subtype":"init","session_id":"test-session-001"}'

while IFS= read -r line; do
  echo '{"type":"assistant","message":{"content":[{"type":"text","text":"Mock response to your prompt."}]}}'
  echo '{"type":"result","subtype":"success","result":"done","total_cost_usd":0.01,"usage":{"input_tokens":100,"output_tokens":50},"num_turns":1}'
done
```

```bash
chmod +x internal/claude/testdata/mock_claude.sh
```

- [ ] **Step 2: Write session manager tests**

Create `internal/claude/manager_test.go`:

```go
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ddebrito/craudinei/internal/types"
)

func TestManager_StartAndReceiveInit(t *testing.T) {
	mockPath, err := filepath.Abs("testdata/mock_claude.sh")
	if err != nil {
		t.Fatalf("resolving mock path: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan *types.Event, 10)
	m := NewManager(mockPath, eventCh)

	if err := m.Start(ctx, t.TempDir()); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer m.Stop()

	select {
	case ev := <-eventCh:
		if ev.Type != types.EventSystem || ev.Subtype != "init" {
			t.Errorf("expected system/init, got %s/%s", ev.Type, ev.Subtype)
		}
		if ev.SessionID != "test-session-001" {
			t.Errorf("expected session test-session-001, got %s", ev.SessionID)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for init event")
	}
}

func TestManager_SendPromptAndReceiveResponse(t *testing.T) {
	mockPath, err := filepath.Abs("testdata/mock_claude.sh")
	if err != nil {
		t.Fatalf("resolving mock path: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan *types.Event, 10)
	m := NewManager(mockPath, eventCh)

	if err := m.Start(ctx, t.TempDir()); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer m.Stop()

	// Drain init event
	<-eventCh

	// Send a prompt
	if err := m.SendPrompt("hello"); err != nil {
		t.Fatalf("send prompt failed: %v", err)
	}

	// Should receive assistant response then result
	var gotAssistant, gotResult bool
	for i := 0; i < 2; i++ {
		select {
		case ev := <-eventCh:
			if ev.Type == types.EventAssistant {
				gotAssistant = true
			}
			if ev.Type == types.EventResult {
				gotResult = true
			}
		case <-ctx.Done():
			t.Fatal("timed out")
		}
	}
	if !gotAssistant {
		t.Error("did not receive assistant event")
	}
	if !gotResult {
		t.Error("did not receive result event")
	}
}

func TestManager_Stop(t *testing.T) {
	mockPath, err := filepath.Abs("testdata/mock_claude.sh")
	if err != nil {
		t.Fatalf("resolving mock path: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan *types.Event, 10)
	m := NewManager(mockPath, eventCh)

	if err := m.Start(ctx, t.TempDir()); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	// Drain init
	<-eventCh

	m.Stop()

	if m.IsRunning() {
		t.Error("manager should not be running after stop")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test -race ./internal/claude/... -run TestManager
```

Expected: FAIL.

- [ ] **Step 4: Implement session manager**

Create `internal/claude/manager.go`:

```go
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"syscall"

	"github.com/ddebrito/craudinei/internal/types"
)

type Manager struct {
	binary  string
	eventCh chan<- *types.Event

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	running bool
	pgid    int
}

func NewManager(binary string, eventCh chan<- *types.Event) *Manager {
	return &Manager{
		binary:  binary,
		eventCh: eventCh,
	}
}

func (m *Manager) Start(ctx context.Context, workDir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("session already running")
	}

	childCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel

	cmd := exec.CommandContext(childCtx, m.binary)
	cmd.Dir = workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("creating stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("creating stdout pipe: %w", err)
	}

	cmd.Stderr = slogWriter{}

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("starting claude: %w", err)
	}

	m.cmd = cmd
	m.stdin = stdinPipe
	m.running = true

	if cmd.Process != nil {
		m.pgid = cmd.Process.Pid
	}

	m.wg.Add(1)
	go m.readStdout(childCtx, stdoutPipe)

	go m.waitForExit()

	return nil
}

func (m *Manager) readStdout(ctx context.Context, r io.Reader) {
	defer m.wg.Done()

	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var ev types.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			slog.Warn("failed to parse NDJSON line", "error", err)
			continue
		}

		select {
		case m.eventCh <- &ev:
		case <-ctx.Done():
			return
		}
	}
}

func (m *Manager) waitForExit() {
	m.wg.Wait()

	m.mu.Lock()
	cmd := m.cmd
	m.mu.Unlock()

	if cmd != nil {
		cmd.Wait()
	}

	m.mu.Lock()
	m.running = false
	m.mu.Unlock()
}

func (m *Manager) SendPrompt(prompt string) error {
	m.mu.Lock()
	stdin := m.stdin
	m.mu.Unlock()

	if stdin == nil {
		return fmt.Errorf("no active session")
	}

	msg := struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}{
		Type:    "user_message",
		Content: prompt,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encoding prompt: %w", err)
	}

	_, err = fmt.Fprintf(stdin, "%s\n", data)
	if err != nil {
		return fmt.Errorf("writing to stdin: %w", err)
	}

	return nil
}

func (m *Manager) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	pgid := m.pgid
	running := m.running
	m.mu.Unlock()

	if !running {
		return
	}

	if pgid > 0 {
		syscall.Kill(-pgid, syscall.SIGINT)
	}

	if cancel != nil {
		cancel()
	}

	m.wg.Wait()

	m.mu.Lock()
	if m.cmd != nil {
		m.cmd.Wait()
	}
	if m.stdin != nil {
		m.stdin.Close()
	}
	m.running = false
	m.mu.Unlock()
}

func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

type slogWriter struct{}

func (slogWriter) Write(p []byte) (n int, err error) {
	slog.Debug("claude stderr", "output", string(p))
	return len(p), nil
}
```

- [ ] **Step 5: Run tests**

```bash
go test -race ./internal/claude/... -run TestManager
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/claude/manager.go internal/claude/manager_test.go internal/claude/testdata/
git commit -m "feat: session manager with subprocess lifecycle, NDJSON reading, prompt sending"
```

---

## Phase 3: Telegram Bot

### Task 11: Bot Core with Send Queue

**Files:**
- Create: `internal/bot/bot.go`

- [ ] **Step 1: Install Telegram bot dependency**

```bash
cd /home/ddebrito/dev/craudinei && go get github.com/go-telegram/bot
```

- [ ] **Step 2: Implement bot core**

Create `internal/bot/bot.go`:

```go
package bot

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type Bot struct {
	api         *bot.Bot
	auth        *Auth
	sendQueue   chan outboundMessage
	chatID      int64
}

type outboundMessage struct {
	chatID          int64
	text            string
	parseMode       string
	replyTo         int
	disableNotify   bool
	replyMarkup     models.ReplyMarkup
}

func New(token string, auth *Auth) (*Bot, error) {
	b := &Bot{
		auth:      auth,
		sendQueue: make(chan outboundMessage, 50),
	}

	opts := []bot.Option{
		bot.WithDefaultHandler(b.handleUpdate),
	}

	api, err := bot.New(token, opts...)
	if err != nil {
		return nil, fmt.Errorf("creating bot: %w", err)
	}
	b.api = api

	return b, nil
}

func (b *Bot) Start(ctx context.Context) {
	go b.processSendQueue(ctx)
	b.api.Start(ctx)
}

func (b *Bot) handleUpdate(ctx context.Context, api *bot.Bot, update *models.Update) {
	if update.Message != nil {
		b.handleMessage(ctx, api, update.Message)
	}
	if update.CallbackQuery != nil {
		b.handleCallback(ctx, api, update.CallbackQuery)
	}
}

func (b *Bot) handleMessage(ctx context.Context, api *bot.Bot, msg *models.Message) {
	userID := msg.From.ID
	b.chatID = msg.Chat.ID

	if !b.auth.IsWhitelisted(userID) {
		slog.Warn("unauthorized access attempt", "user_id", userID)
		return
	}

	slog.Info("received message", "user_id", userID, "text", msg.Text)
}

func (b *Bot) handleCallback(ctx context.Context, api *bot.Bot, cb *models.CallbackQuery) {
	api.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: cb.ID,
	})
}

func (b *Bot) processSendQueue(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case msg := <-b.sendQueue:
			b.doSend(ctx, msg)
			// Rate limit: wait for next tick
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (b *Bot) doSend(ctx context.Context, msg outboundMessage) {
	params := &bot.SendMessageParams{
		ChatID:              msg.chatID,
		Text:                msg.text,
		ParseMode:           models.ParseMode(msg.parseMode),
		DisableNotification: msg.disableNotify,
	}
	if msg.replyTo > 0 {
		params.ReplyParameters = &models.ReplyParameters{
			MessageID: msg.replyTo,
		}
	}
	if msg.replyMarkup != nil {
		params.ReplyMarkup = msg.replyMarkup
	}

	_, err := b.api.SendMessage(ctx, params)
	if err != nil {
		slog.Error("failed to send message", "error", err, "chat_id", msg.chatID)
	}
}

func (b *Bot) Send(chatID int64, text string) {
	b.sendQueue <- outboundMessage{
		chatID:    chatID,
		text:      text,
		parseMode: "HTML",
	}
}

func (b *Bot) SendSilent(chatID int64, text string) {
	b.sendQueue <- outboundMessage{
		chatID:        chatID,
		text:          text,
		parseMode:     "HTML",
		disableNotify: true,
	}
}

func (b *Bot) SendWithReply(chatID int64, text string, replyTo int) {
	b.sendQueue <- outboundMessage{
		chatID:    chatID,
		text:      text,
		parseMode: "HTML",
		replyTo:   replyTo,
	}
}

func (b *Bot) SendWithKeyboard(chatID int64, text string, keyboard models.ReplyMarkup) {
	b.sendQueue <- outboundMessage{
		chatID:      chatID,
		text:        text,
		parseMode:   "HTML",
		replyMarkup: keyboard,
	}
}

func (b *Bot) RegisterCommands(ctx context.Context) {
	commands := []models.BotCommand{
		{Command: "begin", Description: "Start a Claude Code session"},
		{Command: "stop", Description: "Stop the current session"},
		{Command: "cancel", Description: "Interrupt the current task"},
		{Command: "status", Description: "Show session state"},
		{Command: "auth", Description: "Authenticate with passphrase"},
		{Command: "resume", Description: "Resume a previous session"},
		{Command: "help", Description: "Show available commands"},
		{Command: "reload", Description: "Reload configuration"},
		{Command: "sessions", Description: "List recent sessions"},
	}
	b.api.SetMyCommands(ctx, &bot.SetMyCommandsParams{
		Commands: commands,
	})
}

func (b *Bot) DeleteWebhook(ctx context.Context) {
	b.api.DeleteWebhook(ctx, &bot.DeleteWebhookParams{})
}
```

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

Expected: compiles without errors.

- [ ] **Step 4: Commit**

```bash
git add internal/bot/bot.go go.mod go.sum
git commit -m "feat: Telegram bot core with rate-limited send queue, command registration"
```

---

### Task 12: Command Handlers

**Files:**
- Create: `internal/bot/handlers.go`

- [ ] **Step 1: Implement command handlers**

Create `internal/bot/handlers.go`:

```go
package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/ddebrito/craudinei/internal/audit"
	"github.com/ddebrito/craudinei/internal/claude"
	"github.com/ddebrito/craudinei/internal/config"
	"github.com/ddebrito/craudinei/internal/types"
)

type Handlers struct {
	bot      *Bot
	cfg      *config.Config
	state    *types.SessionState
	sm       *claude.StateMachine
	manager  *claude.Manager
	queue    *claude.InputQueue
	audit    *audit.Logger
}

func NewHandlers(b *Bot, cfg *config.Config, state *types.SessionState, sm *claude.StateMachine, manager *claude.Manager, queue *claude.InputQueue, auditLog *audit.Logger) *Handlers {
	return &Handlers{
		bot:     b,
		cfg:     cfg,
		state:   state,
		sm:      sm,
		manager: manager,
		queue:   queue,
		audit:   auditLog,
	}
}

func (h *Handlers) HandleMessage(ctx context.Context, api *bot.Bot, msg *models.Message) {
	userID := msg.From.ID
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)

	if !h.bot.auth.IsWhitelisted(userID) {
		h.audit.UnauthorizedAccess(userID)
		h.bot.Send(chatID, "You are not authorized to use this bot.")
		return
	}

	if strings.HasPrefix(text, "/") {
		h.handleCommand(ctx, api, msg, userID, chatID, text)
		return
	}

	if !h.bot.auth.IsAuthenticated(userID) {
		h.bot.Send(chatID, "Please authenticate with /auth &lt;passphrase&gt;")
		return
	}
	h.bot.auth.TouchSession(userID)

	status := h.state.Status()
	switch status {
	case types.StatusRunning, types.StatusWaitingApproval:
		sanitized := SanitizeInput(text)
		if err := h.queue.Enqueue(sanitized); err != nil {
			h.bot.Send(chatID, "Queue full. Please wait for Claude to finish.")
			return
		}
		if h.queue.Len() > 1 {
			h.bot.SendSilent(chatID, "Your message is queued and will be sent after the current task completes.")
		}
	default:
		h.bot.Send(chatID, "No active session. Use /begin &lt;directory&gt; to start one.")
	}
}

func (h *Handlers) handleCommand(ctx context.Context, api *bot.Bot, msg *models.Message, userID int64, chatID int64, text string) {
	parts := strings.SplitN(text, " ", 2)
	cmd := parts[0]
	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}

	// /start is Telegram's reserved command
	if cmd == "/start" {
		h.handleStart(chatID)
		return
	}

	// /auth and /help don't require prior auth
	if cmd == "/auth" {
		h.handleAuth(ctx, api, msg, userID, chatID, args)
		return
	}
	if cmd == "/help" {
		h.handleHelp(chatID)
		return
	}

	if !h.bot.auth.IsAuthenticated(userID) {
		h.bot.Send(chatID, "Please authenticate with /auth &lt;passphrase&gt;")
		return
	}
	h.bot.auth.TouchSession(userID)

	if err := h.sm.CanExecuteCommand(cmd); err != nil {
		h.bot.Send(chatID, EscapeHTML(err.Error()))
		return
	}

	h.audit.Command(userID, cmd, args)

	switch cmd {
	case "/begin":
		h.handleBegin(ctx, chatID, args)
	case "/stop":
		h.handleStop(chatID)
	case "/cancel":
		h.handleCancel(chatID)
	case "/status":
		h.handleStatus(chatID)
	case "/resume":
		h.handleResume(ctx, chatID, args)
	case "/sessions":
		h.handleSessions(chatID)
	case "/reload":
		h.handleReload(chatID)
	default:
		h.bot.Send(chatID, fmt.Sprintf("Unknown command: %s. Use /help for available commands.", EscapeHTML(cmd)))
	}
}

func (h *Handlers) handleStart(chatID int64) {
	h.bot.Send(chatID, "Welcome to Craudinei! Authenticate with /auth &lt;passphrase&gt; to get started.")
}

func (h *Handlers) handleAuth(ctx context.Context, api *bot.Bot, msg *models.Message, userID int64, chatID int64, passphrase string) {
	// Delete the auth message to protect the passphrase
	api.DeleteMessage(ctx, &bot.DeleteMessageParams{
		ChatID:    chatID,
		MessageID: msg.ID,
	})

	if passphrase == "" {
		h.bot.Send(chatID, "Usage: /auth &lt;passphrase&gt;")
		return
	}

	ok, err := h.bot.auth.Authenticate(userID, passphrase)
	if err != nil {
		h.audit.AuthAttempt(userID, false)
		h.bot.Send(chatID, EscapeHTML(err.Error()))
		return
	}

	h.audit.AuthAttempt(userID, ok)

	if !ok {
		h.bot.Send(chatID, "Authentication failed. Please try again.")
		return
	}

	h.bot.Send(chatID, "Authenticated successfully. Send /begin &lt;directory&gt; to start a session.")
}

func (h *Handlers) handleBegin(ctx context.Context, chatID int64, dir string) {
	if dir == "" {
		dir = h.cfg.Claude.DefaultWorkDir
	}
	if dir == "" {
		h.bot.Send(chatID, "Please specify a directory: /begin &lt;directory&gt;")
		return
	}

	if err := config.ValidateWorkDir(dir, h.cfg.Claude.AllowedPaths); err != nil {
		h.bot.Send(chatID, "Invalid directory: "+EscapeHTML(err.Error()))
		return
	}

	h.state.StartSession(dir)
	if err := h.sm.Transition(types.StatusStarting); err != nil {
		h.bot.Send(chatID, "Failed to start: "+EscapeHTML(err.Error()))
		return
	}

	if err := h.manager.Start(ctx, dir); err != nil {
		h.sm.Transition(types.StatusCrashed)
		h.state.SetStatus(types.StatusCrashed)
		h.bot.Send(chatID, "Failed to start Claude Code: "+EscapeHTML(err.Error()))
		return
	}

	h.audit.SessionEvent("started", "", dir)
}

func (h *Handlers) handleStop(chatID int64) {
	keyboard := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{Text: "Yes, stop", CallbackData: "c:stop_yes"},
				{Text: "Cancel", CallbackData: "c:stop_no"},
			},
		},
	}
	h.bot.SendWithKeyboard(chatID, "Stop the current session?", keyboard)
}

func (h *Handlers) handleCancel(chatID int64) {
	h.manager.Interrupt()
	h.bot.Send(chatID, "Interrupting current task...")
}

func (h *Handlers) handleStatus(chatID int64) {
	status := h.state.Status()
	workDir := h.state.WorkDir()
	sessionID := h.state.SessionID()
	startedAt := h.state.StartedAt()
	cost, tokensIn, tokensOut, turns := h.state.Stats()

	var uptime string
	if !startedAt.IsZero() {
		uptime = time.Since(startedAt).Round(time.Second).String()
	} else {
		uptime = "n/a"
	}

	text := fmt.Sprintf(
		"<b>Status:</b> %s\n<b>Project:</b> %s\n<b>Session:</b> %s\n<b>Uptime:</b> %s\n<b>Cost:</b> $%.2f / $%.2f\n<b>Tokens:</b> %d in / %d out\n<b>Turns:</b> %d / %d",
		EscapeHTML(string(status)),
		EscapeHTML(workDir),
		EscapeHTML(sessionID),
		uptime,
		cost, h.cfg.Claude.MaxBudgetUSD,
		tokensIn, tokensOut,
		turns, h.cfg.Claude.MaxTurns,
	)
	h.bot.Send(chatID, text)
}

func (h *Handlers) handleHelp(chatID int64) {
	help := `<b>Craudinei Commands</b>

/begin &lt;dir&gt; — Start a session in a directory
/stop — Stop the current session
/cancel — Interrupt the current task
/status — Show session state and usage
/auth &lt;pass&gt; — Authenticate
/resume [id] — Resume a previous session
/sessions — List recent sessions
/reload — Reload configuration
/help — Show this message`
	h.bot.Send(chatID, help)
}

func (h *Handlers) handleResume(ctx context.Context, chatID int64, sessionID string) {
	h.bot.Send(chatID, "Resume is not yet implemented.")
}

func (h *Handlers) handleSessions(chatID int64) {
	h.bot.Send(chatID, "No saved sessions yet.")
}

func (h *Handlers) handleReload(chatID int64) {
	h.bot.Send(chatID, "Config reload is not yet implemented.")
}
```

- [ ] **Step 2: Add Interrupt method to Manager**

Add to `internal/claude/manager.go`:

```go
func (m *Manager) Interrupt() {
	m.mu.Lock()
	pgid := m.pgid
	m.mu.Unlock()

	if pgid > 0 {
		syscall.Kill(-pgid, syscall.SIGINT)
	}
}
```

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

Expected: compiles.

- [ ] **Step 4: Commit**

```bash
git add internal/bot/handlers.go internal/claude/manager.go
git commit -m "feat: command handlers for /begin, /stop, /cancel, /status, /auth, /help"
```

---

### Task 13: Main Wiring

**Files:**
- Modify: `cmd/craudinei/main.go`

- [ ] **Step 1: Wire everything together in main.go**

Replace `cmd/craudinei/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ddebrito/craudinei/internal/audit"
	internalbot "github.com/ddebrito/craudinei/internal/bot"
	"github.com/ddebrito/craudinei/internal/claude"
	"github.com/ddebrito/craudinei/internal/config"
	"github.com/ddebrito/craudinei/internal/types"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := "craudinei.yaml"
	if p := os.Getenv("CRAUDINEI_CONFIG"); p != "" {
		cfgPath = p
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if err := config.Validate(cfg); err != nil {
		return fmt.Errorf("validating config: %w", err)
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY environment variable is required")
	}

	level := slog.LevelInfo
	switch cfg.Logging.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	auditWriter := os.Stderr
	if cfg.Logging.AuditFile != "" {
		f, err := os.OpenFile(cfg.Logging.AuditFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			slog.Warn("failed to open audit log file, using stderr", "error", err)
		} else {
			auditWriter = f
			defer f.Close()
		}
	}
	auditLog := audit.New(slog.New(slog.NewJSONHandler(auditWriter, nil)))

	auth := internalbot.NewAuth(cfg.Telegram.AllowedUsers, cfg.Telegram.AuthPassphrase, cfg.Telegram.AuthIdleTimeout)

	state := types.NewSessionState()
	sm := claude.NewStateMachine()
	eventCh := make(chan *types.Event, 100)
	manager := claude.NewManager(cfg.Claude.Binary, eventCh)
	queue := claude.NewInputQueue(5)

	b, err := internalbot.New(cfg.Telegram.Token, auth)
	if err != nil {
		return fmt.Errorf("creating bot: %w", err)
	}

	handlers := internalbot.NewHandlers(b, cfg, state, sm, manager, queue, auditLog)
	_ = handlers

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		slog.Info("received signal, shutting down", "signal", sig)
		manager.Stop()
		cancel()
	}()

	slog.Info("craudinei starting", "allowed_users", cfg.Telegram.AllowedUsers)

	b.DeleteWebhook(ctx)
	b.RegisterCommands(ctx)
	b.Start(ctx)

	return nil
}
```

- [ ] **Step 2: Verify build and run**

```bash
make build
```

Expected: compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add cmd/craudinei/main.go
git commit -m "feat: main wiring — config, auth, session manager, bot startup with signal handling"
```

---

## Phase 4: Integration

### Task 14: Event Loop Integration

**Files:**
- Create: `internal/bot/eventloop.go`

This task connects the event channel from the session manager to the Telegram bot, processing NDJSON events and routing them to the appropriate Telegram actions.

- [ ] **Step 1: Implement event loop**

Create `internal/bot/eventloop.go`:

```go
package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ddebrito/craudinei/internal/claude"
	"github.com/ddebrito/craudinei/internal/config"
	"github.com/ddebrito/craudinei/internal/router"
	"github.com/ddebrito/craudinei/internal/types"
)

type EventLoop struct {
	bot     *Bot
	cfg     *config.Config
	state   *types.SessionState
	sm      *claude.StateMachine
	manager *claude.Manager
	queue   *claude.InputQueue
	eventCh <-chan *types.Event
	chatID  int64

	allowedTools map[string]bool
}

func NewEventLoop(b *Bot, cfg *config.Config, state *types.SessionState, sm *claude.StateMachine, manager *claude.Manager, queue *claude.InputQueue, eventCh <-chan *types.Event) *EventLoop {
	allowed := make(map[string]bool)
	for _, t := range cfg.Claude.AllowedTools {
		allowed[t] = true
	}
	return &EventLoop{
		bot:          b,
		cfg:          cfg,
		state:        state,
		sm:           sm,
		manager:      manager,
		queue:        queue,
		eventCh:      eventCh,
		allowedTools: allowed,
	}
}

func (el *EventLoop) SetChatID(id int64) {
	el.chatID = id
}

func (el *EventLoop) Run(ctx context.Context) {
	stdinDone := make(chan struct{})
	go el.runStdinWriter(ctx, stdinDone)
	go el.runProgressTicker(ctx)

	for {
		select {
		case ev := <-el.eventCh:
			el.handleEvent(ev)
		case <-ctx.Done():
			return
		}
	}
}

func (el *EventLoop) handleEvent(ev *types.Event) {
	el.state.TouchActivity()
	action := router.ClassifyEvent(ev, el.allowedTools)

	switch action {
	case router.ActionSessionStarted:
		el.state.SetSessionID(ev.SessionID)
		el.sm.Transition(types.StatusRunning)
		el.state.SetStatus(types.StatusRunning)

		tools := strings.Join(el.cfg.Claude.AllowedTools, ", ")
		text := fmt.Sprintf("Session started in <code>%s</code>\nAuto-approved tools: %s\nBudget: $%.2f",
			EscapeHTML(el.state.WorkDir()), EscapeHTML(tools), el.cfg.Claude.MaxBudgetUSD)
		el.bot.Send(el.chatID, text)

	case router.ActionSendText:
		text := router.ExtractText(ev)
		if text == "" {
			return
		}
		if ShouldSendAsFile(text) {
			el.bot.Send(el.chatID, "(Response too large — sending as file is not yet implemented)")
			return
		}
		chunks := ChunkMessage(text, maxMessageLen)
		for _, chunk := range chunks {
			el.bot.Send(el.chatID, chunk)
		}

	case router.ActionToolAutoApproved:
		slog.Debug("tool auto-approved")

	case router.ActionToolNeedsApproval:
		slog.Info("tool needs approval (approval server not yet implemented)")
		el.bot.Send(el.chatID, "A tool requires approval but the approval server is not yet implemented.")

	case router.ActionSessionCompleted:
		el.sm.Transition(types.StatusIdle)
		el.state.SetStatus(types.StatusIdle)
		if ev.Usage != nil {
			el.state.UpdateUsage(ev.TotalCost, ev.Usage.InputTokens, ev.Usage.OutputTokens, ev.NumTurns)
		}
		cost, tokensIn, tokensOut, turns := el.state.Stats()
		text := fmt.Sprintf("Task completed. Cost: $%.4f | Tokens: %d in / %d out | Turns: %d",
			cost, tokensIn, tokensOut, turns)
		el.bot.Send(el.chatID, text)

	case router.ActionSessionError:
		el.sm.Transition(types.StatusCrashed)
		el.state.SetStatus(types.StatusCrashed)
		errMsg := formatErrorMessage(ev)
		el.bot.Send(el.chatID, errMsg)

	case router.ActionNotifyRetry:
		text := fmt.Sprintf("API retry %d/%d — temporarily unavailable", ev.Attempt, ev.MaxRetries)
		el.bot.SendSilent(el.chatID, text)

	case router.ActionNotifyRateLimit:
		text := fmt.Sprintf("Rate limited by Claude API. Retrying in %ds. No action needed.", ev.RetryAfterSeconds)
		el.bot.SendSilent(el.chatID, text)

	case router.ActionIgnore:
		// no-op
	}
}

func (el *EventLoop) runStdinWriter(ctx context.Context, done chan struct{}) {
	defer close(done)
	for {
		msg, err := el.queue.Dequeue(ctx)
		if err != nil {
			return
		}
		if err := el.manager.SendPrompt(msg); err != nil {
			slog.Error("failed to send prompt", "error", err)
			el.bot.Send(el.chatID, "Failed to send prompt: "+EscapeHTML(err.Error()))
		}
	}
}

func (el *EventLoop) runProgressTicker(ctx context.Context) {
	interval := el.cfg.Telegram.ProgressInterval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	consecutiveIdle := 0

	for {
		select {
		case <-ticker.C:
			status := el.state.Status()
			if status != types.StatusRunning && status != types.StatusWaitingApproval {
				consecutiveIdle = 0
				continue
			}

			lastActivity := el.state.LastActivity()
			elapsed := time.Since(el.state.StartedAt()).Round(time.Second)
			cost, tokensIn, _, _ := el.state.Stats()

			inactivity := time.Since(lastActivity)
			if inactivity > el.cfg.Telegram.InactivityTimeout {
				el.bot.Send(el.chatID, fmt.Sprintf(
					"Session appears stalled — no activity for %s. Use /stop to end or /cancel to interrupt.",
					inactivity.Round(time.Minute)))
				continue
			}

			consecutiveIdle++
			if consecutiveIdle > 3 {
				ticker.Reset(interval * 2)
			}

			text := fmt.Sprintf("Still working (%s) — %d tokens used, $%.4f spent",
				elapsed, tokensIn, cost)
			el.bot.SendSilent(el.chatID, text)

		case <-ctx.Done():
			return
		}
	}
}

func formatErrorMessage(ev *types.Event) string {
	switch ev.Subtype {
	case "error_max_turns":
		return "Reached the maximum number of turns. Start a new session to continue."
	case "error_max_budget_usd":
		return "Budget limit reached. Session ended to prevent overspending."
	case "error_during_execution":
		return "Claude Code encountered an error during execution. Use /begin to start a new session or /resume to attempt recovery."
	default:
		return fmt.Sprintf("Session ended with error: %s", EscapeHTML(ev.Subtype))
	}
}
```

- [ ] **Step 2: Update main.go to wire the event loop**

Add to `cmd/craudinei/main.go`, after creating handlers and before `b.Start(ctx)`:

```go
	eventLoop := internalbot.NewEventLoop(b, cfg, state, sm, manager, queue, eventCh)
	go eventLoop.Run(ctx)
```

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

Expected: compiles.

- [ ] **Step 4: Commit**

```bash
git add internal/bot/eventloop.go cmd/craudinei/main.go
git commit -m "feat: event loop — routes NDJSON events to Telegram, progress ticker, stdin writer"
```

---

### Task 15: Rich UI Screens

**Files:**
- Create: `internal/bot/screens.go`
- Create: `internal/bot/callbacks.go`

- [ ] **Step 1: Implement rich UI screens**

Create `internal/bot/screens.go`:

```go
package bot

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/go-telegram/bot/models"
)

func WelcomeKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{Text: "Authenticate", CallbackData: "n:auth"},
				{Text: "Help", CallbackData: "n:help"},
			},
		},
	}
}

func HomeKeyboard(allowedPaths []string, maxDirs int) *models.InlineKeyboardMarkup {
	var rows [][]models.InlineKeyboardButton

	dirs := listProjectDirs(allowedPaths, maxDirs)
	row := make([]models.InlineKeyboardButton, 0, 3)
	for i, d := range dirs {
		row = append(row, models.InlineKeyboardButton{
			Text:         filepath.Base(d),
			CallbackData: fmt.Sprintf("d:%d", i),
		})
		if len(row) == 3 || i == len(dirs)-1 {
			rows = append(rows, row)
			row = make([]models.InlineKeyboardButton, 0, 3)
		}
	}

	rows = append(rows, []models.InlineKeyboardButton{
		{Text: "Other directory...", CallbackData: "n:other_dir"},
	})

	rows = append(rows, []models.InlineKeyboardButton{
		{Text: "Resume last", CallbackData: "n:resume_last"},
		{Text: "Sessions", CallbackData: "n:sessions"},
	})

	rows = append(rows, []models.InlineKeyboardButton{
		{Text: "Status", CallbackData: "n:status"},
		{Text: "Help", CallbackData: "n:help"},
	})

	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func ApprovalKeyboard(index int, toolName string) *models.InlineKeyboardMarkup {
	label := "Run this"
	switch toolName {
	case "Edit":
		label = "Apply edit"
	case "Write":
		label = "Write file"
	}

	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{Text: label, CallbackData: fmt.Sprintf("a:%d:y", index)},
				{Text: "Skip", CallbackData: fmt.Sprintf("a:%d:n", index)},
				{Text: "+5 min", CallbackData: fmt.Sprintf("a:%d:s", index)},
			},
			{
				{Text: "Show full", CallbackData: fmt.Sprintf("a:%d:f", index)},
			},
		},
	}
}

func StopConfirmKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{Text: "Yes, stop", CallbackData: "c:stop_yes"},
				{Text: "Cancel", CallbackData: "c:stop_no"},
			},
		},
	}
}

func SessionStatusKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{Text: "Cancel task", CallbackData: "n:cancel"},
				{Text: "Stop", CallbackData: "n:stop"},
			},
		},
	}
}

func listProjectDirs(allowedPaths []string, max int) []string {
	var dirs []string
	for _, root := range allowedPaths {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() && !isHidden(e.Name()) {
				dirs = append(dirs, filepath.Join(root, e.Name()))
			}
		}
	}

	sort.Slice(dirs, func(i, j int) bool {
		infoI, _ := os.Stat(dirs[i])
		infoJ, _ := os.Stat(dirs[j])
		if infoI == nil || infoJ == nil {
			return dirs[i] < dirs[j]
		}
		return infoI.ModTime().After(infoJ.ModTime())
	})

	if len(dirs) > max {
		dirs = dirs[:max]
	}
	return dirs
}

func isHidden(name string) bool {
	return len(name) > 0 && name[0] == '.'
}
```

- [ ] **Step 2: Implement callback dispatcher**

Create `internal/bot/callbacks.go`:

```go
package bot

import (
	"context"
	"log/slog"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type CallbackDispatcher struct {
	b        *Bot
	handlers *Handlers
	dirs     []string
}

func NewCallbackDispatcher(b *Bot, h *Handlers, allowedPaths []string) *CallbackDispatcher {
	return &CallbackDispatcher{
		b:        b,
		handlers: h,
		dirs:     listProjectDirs(allowedPaths, 8),
	}
}

func (cd *CallbackDispatcher) Handle(ctx context.Context, api *bot.Bot, cb *models.CallbackQuery) {
	data := cb.Data
	chatID := cb.Message.Message.Chat.ID

	// Always answer the callback query
	api.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: cb.ID,
	})

	parts := strings.SplitN(data, ":", 2)
	if len(parts) < 2 {
		slog.Warn("invalid callback data", "data", data)
		return
	}

	prefix := parts[0]
	payload := parts[1]

	switch prefix {
	case "n":
		cd.handleNavigation(ctx, api, chatID, payload)
	case "d":
		cd.handleDirectory(ctx, chatID, payload)
	case "c":
		cd.handleConfirm(ctx, chatID, payload)
	case "a":
		cd.handleApproval(ctx, chatID, payload)
	case "s":
		cd.handleSession(ctx, chatID, payload)
	default:
		slog.Warn("unknown callback prefix", "prefix", prefix)
	}
}

func (cd *CallbackDispatcher) handleNavigation(ctx context.Context, api *bot.Bot, chatID int64, action string) {
	switch action {
	case "auth":
		cd.b.Send(chatID, "Send your passphrase now.")
	case "help":
		cd.handlers.handleHelp(chatID)
	case "status":
		cd.handlers.handleStatus(chatID)
	case "other_dir":
		cd.b.Send(chatID, "Send the full directory path.")
	case "resume_last":
		cd.handlers.handleResume(ctx, chatID, "")
	case "sessions":
		cd.handlers.handleSessions(chatID)
	case "cancel":
		cd.handlers.handleCancel(chatID)
	case "stop":
		cd.handlers.handleStop(chatID)
	}
}

func (cd *CallbackDispatcher) handleDirectory(ctx context.Context, chatID int64, indexStr string) {
	var idx int
	fmt.Sscanf(indexStr, "%d", &idx)
	if idx < 0 || idx >= len(cd.dirs) {
		cd.b.Send(chatID, "Invalid directory selection.")
		return
	}
	cd.handlers.handleBegin(ctx, chatID, cd.dirs[idx])
}

func (cd *CallbackDispatcher) handleConfirm(ctx context.Context, chatID int64, action string) {
	switch action {
	case "stop_yes":
		cd.handlers.manager.Stop()
		cd.b.Send(chatID, "Session stopped.")
	case "stop_no":
		cd.b.Send(chatID, "Stop cancelled.")
	}
}

func (cd *CallbackDispatcher) handleApproval(ctx context.Context, chatID int64, payload string) {
	slog.Info("approval callback received (approval server not yet implemented)", "payload", payload)
	cd.b.Send(chatID, "Approval handling is not yet implemented.")
}

func (cd *CallbackDispatcher) handleSession(ctx context.Context, chatID int64, sessionID string) {
	cd.handlers.handleResume(ctx, chatID, sessionID)
}
```

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

Expected: compiles.

- [ ] **Step 4: Commit**

```bash
git add internal/bot/screens.go internal/bot/callbacks.go
git commit -m "feat: rich UI screens — welcome, home, directory picker, approval cards, callback dispatcher"
```

---

### Task 16: Documentation

**Files:**
- Create: `README.md`
- Create: `docs/config.md`
- Create: `docs/deployment.md`
- Create: `docs/botfather-setup.md`

- [ ] **Step 1: Create README.md**

Create `README.md`:

```markdown
# Craudinei

Remote Claude Code sessions from your Telegram. Drive a Claude Code CLI session from your phone, approve tool calls with inline buttons, and monitor long-running sessions with notifications.

## Prerequisites

- Go 1.22+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed
- Telegram account
- `ANTHROPIC_API_KEY` environment variable set

## Quick Start

1. Create a Telegram bot via [@BotFather](https://t.me/BotFather) (see [docs/botfather-setup.md](docs/botfather-setup.md))
2. Copy `craudinei.yaml.example` to `craudinei.yaml` and fill in your bot token and user ID
3. Set environment variables:
   ```bash
   export TELEGRAM_BOT_TOKEN="your-bot-token"
   export CRAUDINEI_PASSPHRASE="your-passphrase"
   export ANTHROPIC_API_KEY="your-api-key"
   ```
4. Build and run:
   ```bash
   make build && ./craudinei
   ```
5. Open the bot in Telegram, authenticate, and start a session

### Finding your Telegram user ID

Message [@userinfobot](https://t.me/userinfobot) on Telegram. It will reply with your numeric user ID.

## Commands

| Command | Description |
|---|---|
| `/begin <dir>` | Start a session in a directory |
| `/stop` | Stop the current session |
| `/cancel` | Interrupt the current task |
| `/status` | Show session state and usage |
| `/auth <pass>` | Authenticate |
| `/resume [id]` | Resume a previous session |
| `/sessions` | List recent sessions |
| `/help` | Show available commands |

## Configuration

See [docs/config.md](docs/config.md) for the full configuration reference.

## Deployment

See [docs/deployment.md](docs/deployment.md) for systemd setup and production deployment.

## Security

- Bot chats are **not end-to-end encrypted**. All content flows through Telegram's servers.
- Auto-approved tools (Read, Grep, Glob) can access any file the process user can read.
- Users may develop approval fatigue. Use resource limits and review tool calls carefully.
- See the [design spec](docs/superpowers/specs/2026-04-17-craudinei-design.md) for full security analysis.

## License

TBD
```

- [ ] **Step 2: Create docs/config.md**

Create `docs/config.md` with the full field reference (content derived from the annotated `craudinei.yaml.example` — every field with name, type, default, description).

- [ ] **Step 3: Create docs/deployment.md**

Create `docs/deployment.md` with the systemd unit file, environment setup, and log management instructions as specified in the design spec's Documentation section.

- [ ] **Step 4: Create docs/botfather-setup.md**

Create `docs/botfather-setup.md` with the step-by-step bot creation guide as specified in the design spec's Documentation section.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/
git commit -m "docs: README, config reference, deployment guide, BotFather setup"
```

---

### Task 17: End-to-End Smoke Test

**Files:**
- Create: `internal/claude/testdata/mock_claude_full.sh`

- [ ] **Step 1: Create a comprehensive mock Claude script**

Create `internal/claude/testdata/mock_claude_full.sh`:

```bash
#!/bin/bash
# Full mock Claude Code for end-to-end smoke testing.
# Emits realistic NDJSON events including tool_use scenarios.

echo '{"type":"system","subtype":"init","session_id":"smoke-test-001"}'

while IFS= read -r line; do
  # Parse the user message (simplified)
  echo '{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Let me think about this..."},{"type":"text","text":"Here is my response to your prompt."}]}}'
  echo '{"type":"result","subtype":"success","result":"done","total_cost_usd":0.02,"usage":{"input_tokens":200,"output_tokens":100},"num_turns":1}'
done
```

```bash
chmod +x internal/claude/testdata/mock_claude_full.sh
```

- [ ] **Step 2: Create a test config**

Create `internal/claude/testdata/test_config.yaml`:

```yaml
telegram:
  token: "test-token-not-real"
  allowed_users:
    - 999999
  auth_passphrase: "test-pass"
claude:
  binary: "./testdata/mock_claude_full.sh"
  default_workdir: "/tmp"
  allowed_paths:
    - "/tmp"
```

- [ ] **Step 3: Verify the full binary builds and the mock script works**

```bash
make build
make test
```

Expected: all tests pass with `-race`.

- [ ] **Step 4: Commit**

```bash
git add internal/claude/testdata/
git commit -m "test: end-to-end smoke test fixtures with comprehensive mock Claude script"
```

---

## Summary

| Phase | Tasks | Components |
|-------|-------|------------|
| 1: Foundation | 1-5 | Scaffolding, Types, Config, Audit, Auth |
| 2: Core Engine | 6-10 | State machine, Input queue, Router, Renderer, Session manager |
| 3: Telegram Bot | 11-13 | Bot core, Handlers, Main wiring |
| 4: Integration | 14-17 | Event loop, Rich UI, Docs, Smoke test |

**Not yet implemented (future tasks):**
- Approval server (MCP/HTTP bridge for tool approval flow) — requires validation of `--permission-prompt-tool` behavior
- Session persistence and `/resume` functionality
- `/reload` config hot-reload
- Edit-based streaming (`editMessageText` for live response updates)
- Banner image asset and `sendPhoto` integration
- Pinned status bar message
- `sendDocument` for very large outputs
- `sendChatAction("typing")` integration
