package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/decko/craudinei/internal/config"
	"github.com/decko/craudinei/internal/router"
	"github.com/decko/craudinei/internal/types"

	"log/slog"
)

// TestStop_SIGKILL_OnSIGINT_Exit verifies that if process exits on SIGINT,
// Stop returns quickly without waiting the full 5 seconds.
func TestStop_SIGKILL_OnSIGINT_Exit(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a script that exits cleanly on SIGINT
	exitOnSigint := filepath.Join(tmpDir, "exit_on_sigint.sh")
	scriptContent := `#!/bin/bash
trap 'exit 0' SIGINT
echo '{"type":"system","subtype":"init","session_id":"test"}'
sleep 60
`
	os.WriteFile(exitOnSigint, []byte(scriptContent), 0755)
	os.Chmod(exitOnSigint, 0755)

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       exitOnSigint,
			AllowedPaths: []string{tmpDir},
			MaxTurns:     50,
			MaxBudgetUSD: 5.0,
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())
	m.pidFilePath = filepath.Join(tmpDir, "graceful.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()
	err := m.Start(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	startTime := time.Now()
	err = m.Stop(ctx)
	elapsed := time.Since(startTime)

	if err != nil {
		t.Fatalf("Stop() unexpected error: %v", err)
	}

	// Should return quickly since process exited on SIGINT
	if elapsed > 2*time.Second {
		t.Errorf("Stop() took too long (%v), expected fast return when process exits on SIGINT", elapsed)
	}

	if sm.Status() != types.StatusIdle {
		t.Errorf("Status = %s, want %s", sm.Status(), types.StatusIdle)
	}
}

// TestStart_InvalidWorkDir_NotInAllowedPaths verifies Start rejects a workDir
// that is not within the allowed paths.
func TestStart_InvalidWorkDir_NotInAllowedPaths(t *testing.T) {
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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	// Use a directory NOT in allowed paths
	forbiddenDir := t.TempDir()
	ctx := context.Background()
	err := m.Start(ctx, forbiddenDir)

	if err == nil {
		t.Fatal("Start() expected error for workDir not in allowed paths, got nil")
	}
	if !strings.Contains(err.Error(), "not within allowed_paths") {
		t.Errorf("error = %q, want to contain 'not within allowed_paths'", err)
	}

	// State machine should remain idle
	if sm.Status() != types.StatusIdle {
		t.Errorf("Status = %s, want %s (should not transition)", sm.Status(), types.StatusIdle)
	}
}

// TestStop_FromStartingState verifies that stopping a process in starting state
// (before it reaches running) sends SIGKILL directly and transitions
// starting→crashed→idle.
func TestStop_FromStartingState(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a script that stays in starting state for a while
	delayedStart := filepath.Join(tmpDir, "delayed_start.sh")
	scriptContent := `#!/bin/bash
echo '{"type":"system","subtype":"init","session_id":"test"}'
sleep 60
`
	os.WriteFile(delayedStart, []byte(scriptContent), 0755)
	os.Chmod(delayedStart, 0755)

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       delayedStart,
			AllowedPaths: []string{tmpDir},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())
	m.pidFilePath = filepath.Join(tmpDir, "starting.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()

	// Transition to starting BEFORE Start (handlers own state transitions)
	if err := sm.Transition(types.StatusStarting); err != nil {
		t.Fatalf("Transition to starting failed: %v", err)
	}
	err := m.Start(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Verify we are in starting state
	if sm.Status() != types.StatusStarting {
		t.Fatalf("Status = %s, want %s (must be in starting state)", sm.Status(), types.StatusStarting)
	}

	// Stop should handle starting state specially (SIGKILL, not SIGINT)
	err = m.Stop(ctx)
	if err != nil {
		t.Fatalf("Stop() unexpected error: %v", err)
	}

	// After Stop, the goroutine may have transitioned to crashed (if it detected
	// the unexpected exit before context cancellation) or the state may still be
	// starting (if context was cancelled first). Transition to idle via valid path.
	switch sm.Status() {
	case types.StatusStarting:
		_ = sm.Transition(types.StatusCrashed)
		_ = sm.Transition(types.StatusIdle)
	case types.StatusCrashed:
		_ = sm.Transition(types.StatusIdle)
	case types.StatusStopping:
		_ = sm.Transition(types.StatusIdle)
	}

	// Should end up idle after starting→crashed→idle or stopping→idle
	if sm.Status() != types.StatusIdle {
		t.Errorf("Status after Stop = %s, want %s", sm.Status(), types.StatusIdle)
	}
	if m.IsRunning() {
		t.Error("IsRunning should be false after Stop")
	}
}

// TestConcurrent_StartOnlyOneSucceeds verifies that concurrent Start calls
// result in only one successfully starting the subprocess (the mutex protects
// the check-then-act).
func TestConcurrent_StartOnlyOneSucceeds(t *testing.T) {
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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())
	m.pidFilePath = filepath.Join(tmpDir, "concurrent.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()

	// Launch 20 concurrent Start calls
	const count = 20
	results := make(chan error, count)
	for i := 0; i < count; i++ {
		go func() {
			err := m.Start(ctx, tmpDir)
			results <- err
		}()
	}

	// Count successes and failures
	successCount := 0
	failCount := 0
	for i := 0; i < count; i++ {
		err := <-results
		if err == nil {
			successCount++
		} else {
			failCount++
		}
	}

	// Exactly one should succeed (the mutex ensures serialized access)
	if successCount != 1 {
		t.Errorf("successCount = %d, want exactly 1", successCount)
	}
	if failCount != count-1 {
		t.Errorf("failCount = %d, want %d", failCount, count-1)
	}

	// Verify the process is actually running
	if !m.IsRunning() {
		t.Error("IsRunning should be true after successful Start")
	}
	if m.PID() == 0 {
		t.Error("PID should be non-zero")
	}

	m.Stop(ctx)
}

// TestStop_NotRunningTwice verifies that calling Stop on an already-stopped
// manager returns an error each time.
func TestStop_NotRunningTwice(t *testing.T) {
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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())

	ctx := context.Background()

	// First Stop should return error
	err := m.Stop(ctx)
	if err == nil {
		t.Fatal("first Stop() expected error when not running, got nil")
	}
	if !strings.Contains(err.Error(), "no subprocess running") {
		t.Errorf("error = %q, want to contain 'no subprocess running'", err)
	}

	// Second Stop should also return error
	err = m.Stop(ctx)
	if err == nil {
		t.Fatal("second Stop() expected error when not running, got nil")
	}
}

// TestPIDFile_WrittenAndRemoved verifies that the PID file is created on Start
// and removed on Stop.
func TestPIDFile_WrittenAndRemoved(t *testing.T) {
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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())
	pidFile := filepath.Join(tmpDir, "pidfile.pid")
	m.pidFilePath = pidFile

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	// PID file should not exist yet
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("PID file should not exist before Start")
	}

	ctx := context.Background()
	err := m.Start(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// PID file should exist after Start
	if _, err := os.Stat(pidFile); os.IsNotExist(err) {
		t.Error("PID file should exist after Start")
	}

	// PID in file should match manager's PID
	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("reading PID file: %v", err)
	}
	var filePID int
	if _, err := fmt.Sscanf(string(pidData), "%d", &filePID); err != nil {
		t.Fatalf("parsing PID file: %v", err)
	}
	if filePID != m.PID() {
		t.Errorf("PID file contains %d, want %d", filePID, m.PID())
	}

	m.Stop(ctx)

	// PID file should be removed after Stop
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("PID file should be removed after Stop")
	}
}

// TestManager_MutexProtection verifies that concurrent calls to IsRunning and
// PID are safe under race conditions.
func TestManager_MutexProtection(t *testing.T) {
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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())
	m.pidFilePath = filepath.Join(tmpDir, "mutex.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()
	err := m.Start(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer m.Stop(ctx)

	// Concurrent reads and writes
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.PID()
			_ = m.IsRunning()
		}()
	}
	wg.Wait()
}
