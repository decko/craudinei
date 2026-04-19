package claude

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/decko/craudinei/internal/config"
	"github.com/decko/craudinei/internal/types"

	"log/slog"
)

func TestNewManager(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:         "/usr/bin/claude",
			DefaultWorkDir: "/tmp",
			AllowedPaths:   []string{"/tmp"},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)
	logger := slog.Default()

	m := NewManager(cfg, sm, queue, logger)

	if m.cfg != cfg {
		t.Error("cfg not set correctly")
	}
	if m.sm != sm {
		t.Error("sm not set correctly")
	}
	if m.queue != queue {
		t.Error("queue not set correctly")
	}
	if m.logger != logger {
		t.Error("logger not set correctly")
	}
	if m.pidFilePath != "/tmp/craudinei.pid" {
		t.Errorf("pidFilePath = %q, want /tmp/craudinei.pid", m.pidFilePath)
	}
	if m.PID() != 0 {
		t.Error("PID should be 0 for new manager")
	}
	if m.IsRunning() {
		t.Error("IsRunning should be false for new manager")
	}
}

func TestStart_Success(t *testing.T) {
	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "mock_claude.sh")
	copyFile(t, "mock_claude.sh", mockBin)
	os.Chmod(mockBin, 0755)

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       mockBin,
			AllowedPaths: []string{tmpDir},
			MaxTurns:     50,
			MaxBudgetUSD: 5.0,
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)

	m := NewManager(cfg, sm, queue, slog.Default())
	m.pidFilePath = filepath.Join(tmpDir, "test.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()
	err := m.Start(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Start() unexpected error: %v", err)
	}

	if m.PID() == 0 {
		t.Error("PID should not be 0 after Start")
	}
	if !m.IsRunning() {
		t.Error("IsRunning should be true after Start")
	}
	if sm.Status() != types.StatusStarting {
		t.Errorf("Status = %s, want %s", sm.Status(), types.StatusStarting)
	}

	// Verify PID file was written
	pidData, err := os.ReadFile(m.pidFilePath)
	if err != nil {
		t.Fatalf("reading PID file: %v", err)
	}
	var pid int
	if _, err := fmt.Sscanf(string(pidData), "%d", &pid); err != nil {
		t.Fatalf("parsing PID file: %v", err)
	}
	if pid != m.PID() {
		t.Errorf("PID file contains %d, want %d", pid, m.PID())
	}

	m.Stop(ctx)
}

func TestStart_MissingAPIKey(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "mock_claude.sh")
	copyFile(t, "mock_claude.sh", mockBin)
	os.Chmod(mockBin, 0755)

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       mockBin,
			AllowedPaths: []string{tmpDir},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)

	m := NewManager(cfg, sm, queue, slog.Default())

	origKey := os.Getenv("ANTHROPIC_API_KEY")
	os.Unsetenv("ANTHROPIC_API_KEY")
	defer func() {
		if origKey != "" {
			os.Setenv("ANTHROPIC_API_KEY", origKey)
		}
	}()

	ctx := context.Background()
	err := m.Start(ctx, tmpDir)
	if err == nil {
		t.Fatal("Start() expected error for missing API key, got nil")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("error = %q, want to contain ANTHROPIC_API_KEY", err)
	}
}

func TestStart_InvalidWorkDir(t *testing.T) {
	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "mock_claude.sh")
	copyFile(t, "mock_claude.sh", mockBin)
	os.Chmod(mockBin, 0755)

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       mockBin,
			AllowedPaths: []string{tmpDir},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)

	m := NewManager(cfg, sm, queue, slog.Default())

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	otherDir := t.TempDir()
	ctx := context.Background()
	err := m.Start(ctx, otherDir)
	if err == nil {
		t.Fatal("Start() expected error for invalid workDir, got nil")
	}
}

func TestStart_AlreadyRunning(t *testing.T) {
	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "mock_claude.sh")
	copyFile(t, "mock_claude.sh", mockBin)
	os.Chmod(mockBin, 0755)

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       mockBin,
			AllowedPaths: []string{tmpDir},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)

	m := NewManager(cfg, sm, queue, slog.Default())

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()
	err := m.Start(ctx, tmpDir)
	if err != nil {
		t.Fatalf("first Start() failed: %v", err)
	}

	err = m.Start(ctx, tmpDir)
	if err == nil {
		t.Fatal("second Start() expected error for already running, got nil")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error = %q, want to contain 'already running'", err)
	}

	m.Stop(ctx)
}

func TestStop_Success(t *testing.T) {
	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "mock_claude.sh")
	copyFile(t, "mock_claude.sh", mockBin)
	os.Chmod(mockBin, 0755)

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       mockBin,
			AllowedPaths: []string{tmpDir},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)

	m := NewManager(cfg, sm, queue, slog.Default())
	m.pidFilePath = filepath.Join(tmpDir, "test.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()
	err := m.Start(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	err = m.Stop(ctx)
	if err != nil {
		t.Fatalf("Stop() unexpected error: %v", err)
	}

	if m.PID() != 0 {
		t.Error("PID should be 0 after Stop")
	}
	if m.IsRunning() {
		t.Error("IsRunning should be false after Stop")
	}
	if sm.Status() != types.StatusIdle {
		t.Errorf("Status = %s, want %s", sm.Status(), types.StatusIdle)
	}

	// Verify PID file was removed
	if _, err := os.Stat(m.pidFilePath); !os.IsNotExist(err) {
		t.Error("PID file should be removed after Stop")
	}
}

func TestStop_NotRunning(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       "/bin/ls",
			AllowedPaths: []string{tmpDir},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)

	m := NewManager(cfg, sm, queue, slog.Default())

	ctx := context.Background()
	err := m.Stop(ctx)
	if err == nil {
		t.Fatal("Stop() expected error when not running, got nil")
	}
	if !strings.Contains(err.Error(), "no subprocess running") {
		t.Errorf("error = %q, want to contain 'no subprocess running'", err)
	}
}

func TestKillOrphan_NoPIDFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "nonexistent.pid")

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       "/bin/ls",
			AllowedPaths: []string{tmpDir},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)

	m := NewManager(cfg, sm, queue, slog.Default())
	m.pidFilePath = pidFile

	err := m.KillOrphan()
	if err != nil {
		t.Errorf("KillOrphan() unexpected error: %v", err)
	}
}

func TestKillOrphan_DeadProcess(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "dead.pid")

	// Write a PID that is unlikely to exist (very high number)
	os.WriteFile(pidFile, []byte("999999\n"), 0644)

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       "/bin/ls",
			AllowedPaths: []string{tmpDir},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)

	m := NewManager(cfg, sm, queue, slog.Default())
	m.pidFilePath = pidFile

	err := m.KillOrphan()
	if err != nil {
		t.Errorf("KillOrphan() unexpected error: %v", err)
	}

	// PID file should be removed even if process is dead
	if _, statErr := os.Stat(pidFile); !os.IsNotExist(statErr) {
		t.Error("PID file should be removed")
	}
}

func TestKillOrphan_LiveOrphan(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "live.pid")

	// Start a blocking subprocess that ignores SIGINT
	ignoreScript := filepath.Join(tmpDir, "ignore_sigint.sh")
	scriptContent := `#!/bin/bash
trap '' SIGINT
sleep 60
`
	os.WriteFile(ignoreScript, []byte(scriptContent), 0755)

	cmd := exec.Command(ignoreScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting ignore script: %v", err)
	}
	defer func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
	}()

	// Write its PID to the file
	os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0644)

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       "/bin/ls",
			AllowedPaths: []string{tmpDir},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)

	m := NewManager(cfg, sm, queue, slog.Default())
	m.pidFilePath = pidFile

	err := m.KillOrphan()
	if err != nil {
		t.Errorf("KillOrphan() unexpected error: %v", err)
	}

	// PID file should be removed
	if _, statErr := os.Stat(pidFile); !os.IsNotExist(statErr) {
		t.Error("PID file should be removed")
	}
}

func TestStart_InvalidBinary(t *testing.T) {
	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "mock_claude.sh")
	copyFile(t, "mock_claude.sh", mockBin)
	os.Chmod(mockBin, 0755)

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       "/nonexistent/binary/path",
			AllowedPaths: []string{tmpDir},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)

	m := NewManager(cfg, sm, queue, slog.Default())

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()
	err := m.Start(ctx, tmpDir)
	if err == nil {
		t.Fatal("Start() expected error for invalid binary, got nil")
	}

	// State machine should be back to idle (via crashed), not stuck in starting
	if sm.Status() != types.StatusIdle {
		t.Errorf("Status = %s, want %s (not stuck in starting)", sm.Status(), types.StatusIdle)
	}

	// Process should not be considered running
	if m.IsRunning() {
		t.Error("IsRunning should be false after failed Start")
	}
	if m.PID() != 0 {
		t.Error("PID should be 0 after failed Start")
	}
}

func TestStop_ContextCancellation(t *testing.T) {
	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "mock_claude.sh")
	copyFile(t, "mock_claude.sh", mockBin)
	os.Chmod(mockBin, 0755)

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       mockBin,
			AllowedPaths: []string{tmpDir},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)

	m := NewManager(cfg, sm, queue, slog.Default())
	m.pidFilePath = filepath.Join(tmpDir, "cancel.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()
	err := m.Start(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Create a cancelled context
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// Stop with cancelled context should still clean up properly
	err = m.Stop(cancelledCtx)
	if err != nil {
		t.Fatalf("Stop() unexpected error with cancelled context: %v", err)
	}

	// State machine should be idle
	if sm.Status() != types.StatusIdle {
		t.Errorf("Status = %s, want %s after Stop with cancelled context", sm.Status(), types.StatusIdle)
	}

	// Process should not be running
	if m.IsRunning() {
		t.Error("IsRunning should be false after Stop with cancelled context")
	}
	if m.PID() != 0 {
		t.Error("PID should be 0 after Stop with cancelled context")
	}

	// PID file should be removed
	if _, statErr := os.Stat(m.pidFilePath); !os.IsNotExist(statErr) {
		t.Error("PID file should be removed after Stop with cancelled context")
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", src))
	if err != nil {
		t.Fatalf("reading testdata/%s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		t.Fatalf("writing %s: %v", dst, err)
	}
}
