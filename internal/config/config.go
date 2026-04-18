// Package config provides configuration loading, validation, and hot-reload
// for the Craudinei bot.
package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

// TelegramConfig holds Telegram bot configuration.
type TelegramConfig struct {
	Token             string        `yaml:"token"`
	AllowedUsers      []int64       `yaml:"allowed_users"`
	AuthPassphrase    string        `yaml:"auth_passphrase"`
	AuthIdleTimeout   time.Duration `yaml:"auth_idle_timeout"`
	ApprovalTimeout   time.Duration `yaml:"approval_timeout"`
	ProgressInterval  time.Duration `yaml:"progress_interval"`
	InactivityTimeout time.Duration `yaml:"inactivity_timeout"`
}

// ClaudeConfig holds Claude Code subprocess configuration.
type ClaudeConfig struct {
	Binary         string   `yaml:"binary"`
	DefaultWorkDir string   `yaml:"default_workdir"`
	SystemPrompt   string   `yaml:"system_prompt"`
	AllowedTools   []string `yaml:"allowed_tools"`
	MaxTurns       int      `yaml:"max_turns"`
	MaxBudgetUSD   float64  `yaml:"max_budget_usd"`
	AllowedPaths   []string `yaml:"allowed_paths"`
}

// ApprovalConfig holds approval server configuration.
type ApprovalConfig struct {
	Port         int           `yaml:"port"`
	TimeoutBash  time.Duration `yaml:"timeout_bash"`
	TimeoutEdit  time.Duration `yaml:"timeout_edit"`
	TimeoutWrite time.Duration `yaml:"timeout_write"`
}

// LoggingConfig holds logging configuration.
type LoggingConfig struct {
	Level     string `yaml:"level"`
	File      string `yaml:"file"`
	AuditFile string `yaml:"audit_file"`
}

// Config holds all configuration sections.
type Config struct {
	filePath string

	Telegram TelegramConfig `yaml:"telegram"`
	Claude   ClaudeConfig   `yaml:"claude"`
	Approval ApprovalConfig `yaml:"approval"`
	Logging  LoggingConfig  `yaml:"logging"`
}

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// Load reads a YAML configuration file, unmarshals it, expands secrets, and
// applies defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}

	cfg.filePath = path
	expandSecrets(&cfg)
	applyDefaults(&cfg)

	return &cfg, nil
}

// FilePath returns the path to the loaded configuration file.
func (c *Config) FilePath() string {
	return c.filePath
}

// expandSecrets expands ${VAR} patterns in sensitive fields (Token and
// AuthPassphrase) by looking up environment variables. Other fields are
// left unchanged, preserving any literal $ characters.
func expandSecrets(cfg *Config) {
	cfg.Telegram.Token = expandEnvVar(cfg.Telegram.Token)
	cfg.Telegram.AuthPassphrase = expandEnvVar(cfg.Telegram.AuthPassphrase)
}

// expandEnvVar expands ${VAR} patterns in a string by looking up the
// corresponding environment variable. If the string does not contain a
// ${VAR} pattern, it is returned unchanged. This preserves literal $
// characters in non-secret fields.
func expandEnvVar(s string) string {
	if !envVarPattern.MatchString(s) {
		return s
	}
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		// match is ${VAR}; capture group 1 is VAR
		parts := envVarPattern.FindStringSubmatch(match)
		if parts == nil {
			return match
		}
		return os.Getenv(parts[1])
	})
}

// applyDefaults sets default values for zero-value fields.
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

	if cfg.Claude.MaxTurns == 0 {
		cfg.Claude.MaxTurns = 50
	}
	if cfg.Claude.MaxBudgetUSD == 0 {
		cfg.Claude.MaxBudgetUSD = 5.00
	}
	if cfg.Claude.SystemPrompt == "" {
		cfg.Claude.SystemPrompt = "Keep each response under 4000 characters. Wrap lines at 80 characters."
	}
	if len(cfg.Claude.AllowedTools) == 0 {
		cfg.Claude.AllowedTools = []string{"Read", "Grep", "Glob"}
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
