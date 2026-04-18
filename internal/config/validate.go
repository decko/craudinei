// Package config provides configuration loading, validation, and hot-reload
// for the Craudinei bot.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Validate checks that the configuration values are valid.
func Validate(cfg *Config) error {
	if cfg.Telegram.Token == "" {
		return fmt.Errorf("telegram.token is required")
	}
	if len(cfg.Telegram.AllowedUsers) == 0 {
		return fmt.Errorf("telegram.allowed_users is required")
	}
	if cfg.Telegram.AuthPassphrase == "" {
		return fmt.Errorf("telegram.auth_passphrase is required")
	}
	if cfg.Telegram.AuthIdleTimeout > 4*time.Hour {
		return fmt.Errorf("telegram.auth_idle_timeout must be at most 4 hours")
	}

	if !filepath.IsAbs(cfg.Claude.Binary) {
		return fmt.Errorf("claude.binary must be an absolute path")
	}

	if len(cfg.Claude.AllowedPaths) == 0 {
		return fmt.Errorf("claude.allowed_paths must contain at least one directory")
	}

	for _, path := range cfg.Claude.AllowedPaths {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("claude.allowed_paths entry does not exist: %s", path)
			}
			return fmt.Errorf("checking claude.allowed_paths: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("claude.allowed_paths entry is not a directory: %s", path)
		}
	}

	return nil
}

// ValidateWorkDir validates that a working directory is within one of the
// allowed paths. It uses defense-in-depth: filepath.Clean, filepath.EvalSymlinks,
// os.Stat to verify directory existence, and strings.HasPrefix with a trailing
// slash to prevent prefix attacks.
func ValidateWorkDir(dir string, allowedPaths []string) error {
	cleaned := filepath.Clean(dir)

	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return fmt.Errorf("resolving symlinks for work directory: %w", err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("work directory does not exist: %s", dir)
		}
		return fmt.Errorf("checking work directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("work directory is not a directory: %s", dir)
	}

	for _, allowed := range allowedPaths {
		allowedClean := filepath.Clean(allowed)
		allowedResolved, err := filepath.EvalSymlinks(allowedClean)
		if err != nil {
			continue
		}
		if strings.HasPrefix(resolved, allowedResolved+string(filepath.Separator)) {
			return nil
		}
		if resolved == allowedResolved {
			return nil
		}
	}

	return fmt.Errorf("work directory %s is not within allowed_paths", dir)
}

// ValidateFilePermissions checks that the config file has 0600 permissions
// (owner read/write only). Refuses to start if group- or world-readable.
func ValidateFilePermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("statting config file: %w", err)
	}
	if info.Mode().Perm() != 0600 {
		return fmt.Errorf("config file %s has permissions %o; must be 0600", path, info.Mode().Perm())
	}
	return nil
}

// Reload re-reads the configuration file and returns a new config with
// non-sensitive fields updated. Token and AuthPassphrase changes require
// a restart. If reload validation fails, the previous config remains
// active and an error is returned.
func Reload(cfg *Config) (*Config, error) {
	if cfg.filePath == "" {
		return nil, fmt.Errorf("config file path is unknown")
	}

	newCfg, err := Load(cfg.filePath)
	if err != nil {
		return nil, fmt.Errorf("reloading config: %w", err)
	}

	// Preserve sensitive fields from the original config
	newCfg.Telegram.Token = cfg.Telegram.Token
	newCfg.Telegram.AuthPassphrase = cfg.Telegram.AuthPassphrase

	// Validate the merged config
	if err := Validate(newCfg); err != nil {
		return nil, fmt.Errorf("reload validation failed: %w", err)
	}

	return newCfg, nil
}
