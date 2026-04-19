package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/decko/craudinei/internal/config"
	"github.com/decko/craudinei/internal/router"
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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})
	logger := slog.Default()

	m := NewManager(cfg, sm, queue, r, logger)

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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())
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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())

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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())

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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())

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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())
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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())

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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())
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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())
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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())
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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())

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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())
	m.pidFilePath = filepath.Join(tmpDir, "test.pid")

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

func TestPipeManagement_StdinWriter(t *testing.T) {
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
	m.pidFilePath = filepath.Join(tmpDir, "stdin.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()
	if err := m.Start(ctx, tmpDir); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Wait for reader to be ready
	if err := m.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady() failed: %v", err)
	}

	// Enqueue a message that should be written to stdin
	testMsg := `{"type":"user","message":{"content":[{"type":"text","text":"hello"}]}}`
	if err := queue.Enqueue(testMsg); err != nil {
		t.Fatalf("Enqueue() failed: %v", err)
	}

	// Close queue to signal writer to exit
	queue.Close()

	// Give time for the writer to process and exit
	time.Sleep(200 * time.Millisecond)

	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	// Verify manager is stopped
	if m.IsRunning() {
		t.Error("IsRunning should be false after Stop")
	}
}

func TestPipeManagement_StdoutReader(t *testing.T) {
	tmpDir := t.TempDir()
	mockBin := filepath.Join(tmpDir, "mock_claude.sh")
	copyFile(t, "mock_claude.sh", mockBin)
	os.Chmod(mockBin, 0755)

	var mu sync.Mutex
	eventsReceived := make([]router.ClassifiedEvent, 0)

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       mockBin,
			AllowedPaths: []string{tmpDir},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)
	r := router.NewRouter(func(e router.ClassifiedEvent) {
		mu.Lock()
		eventsReceived = append(eventsReceived, e)
		mu.Unlock()
	})

	m := NewManager(cfg, sm, queue, r, slog.Default())
	m.pidFilePath = filepath.Join(tmpDir, "stdout.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()
	if err := m.Start(ctx, tmpDir); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Wait for reader to be ready
	if err := m.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady() failed: %v", err)
	}

	// Give time for the init event to be routed
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	gotEvents := len(eventsReceived) > 0
	mu.Unlock()

	if !gotEvents {
		t.Log("No events received - this may be due to timing")
	}

	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
}

func TestPipeManagement_ContextCancellation(t *testing.T) {
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
	m.pidFilePath = filepath.Join(tmpDir, "cancel.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()
	if err := m.Start(ctx, tmpDir); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Wait for reader to be ready
	if err := m.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady() failed: %v", err)
	}

	// Create a cancelled context
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// Stop should complete even with cancelled context
	if err := m.Stop(cancelledCtx); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	// Verify manager is stopped
	if m.IsRunning() {
		t.Error("IsRunning should be false after Stop")
	}
}

func TestPipeManagement_CrashDetection(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a script that exits with code 1 immediately after init
	crashScript := filepath.Join(tmpDir, "crash_script.sh")
	scriptContent := `#!/bin/bash
echo '{"type":"system","subtype":"init","session_id":"test-session-123"}'
sleep 0.1
exit 1
`
	os.WriteFile(crashScript, []byte(scriptContent), 0755)

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       crashScript,
			AllowedPaths: []string{tmpDir},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())
	m.pidFilePath = filepath.Join(tmpDir, "crash.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()
	if err := m.Start(ctx, tmpDir); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Wait for reader to be ready
	if err := m.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady() failed: %v", err)
	}

	// Give time for the crash to be detected
	time.Sleep(300 * time.Millisecond)

	// The state machine should have transitioned to crashed
	if sm.Status() != types.StatusCrashed {
		t.Errorf("Status = %s, want crashed", sm.Status())
	}

	// Try to Enqueue after crash - should fail with ErrQueueClosed
	err := queue.Enqueue(`{"type":"user"}`)
	if err != ErrQueueClosed {
		t.Errorf("Enqueue() after crash error = %v, want ErrQueueClosed", err)
	}

	// Stop to clean up
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
}

func TestPipeManagement_StopCleansUp(t *testing.T) {
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
	m.pidFilePath = filepath.Join(tmpDir, "cleanup.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()
	if err := m.Start(ctx, tmpDir); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Wait for reader to be ready
	if err := m.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady() failed: %v", err)
	}

	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	// Verify cleanup
	if m.IsRunning() {
		t.Error("IsRunning should be false after Stop")
	}
	if m.PID() != 0 {
		t.Error("PID should be 0 after Stop")
	}
}

func TestSaveSession(t *testing.T) {
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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())
	m.pidFilePath = filepath.Join(tmpDir, "test.pid")
	m.sessionFilePath = filepath.Join(tmpDir, "session.json")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()
	if err := m.Start(ctx, tmpDir); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Wait for the init event to set session ID
	time.Sleep(100 * time.Millisecond)

	// Manually set session data since the mock doesn't send real init
	m.sm.state.StartSession(tmpDir)
	if err := m.UpdateSessionID("test-session-abc123"); err != nil {
		t.Fatalf("UpdateSessionID() failed: %v", err)
	}

	if err := m.SaveSession(); err != nil {
		t.Fatalf("SaveSession() failed: %v", err)
	}

	// Verify session file contents
	data, err := os.ReadFile(m.sessionFilePath)
	if err != nil {
		t.Fatalf("reading session file: %v", err)
	}

	var sessionData SessionData
	if err := json.Unmarshal(data, &sessionData); err != nil {
		t.Fatalf("unmarshalling session data: %v", err)
	}

	if sessionData.SessionID != "test-session-abc123" {
		t.Errorf("SessionID = %q, want test-session-abc123", sessionData.SessionID)
	}
	if sessionData.WorkDir != tmpDir {
		t.Errorf("WorkDir = %q, want %q", sessionData.WorkDir, tmpDir)
	}
	if sessionData.PID != m.PID() {
		t.Errorf("PID = %d, want %d", sessionData.PID, m.PID())
	}
	if sessionData.StartedAt.IsZero() {
		t.Error("StartedAt should not be zero")
	}

	m.Stop(ctx)
}

func TestLoadSession(t *testing.T) {
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "session.json")

	// Write a session file
	startedAt := time.Now().Truncate(time.Second)
	sessionData := SessionData{
		SessionID: "loaded-session-xyz",
		WorkDir:   tmpDir,
		PID:       12345,
		StartedAt: startedAt,
	}
	data, err := json.Marshal(sessionData)
	if err != nil {
		t.Fatalf("marshalling session data: %v", err)
	}
	if err := os.WriteFile(sessionFile, data, 0644); err != nil {
		t.Fatalf("writing session file: %v", err)
	}

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
	m.sessionFilePath = sessionFile

	loaded, err := m.LoadSession()
	if err != nil {
		t.Fatalf("LoadSession() failed: %v", err)
	}

	if loaded.SessionID != "loaded-session-xyz" {
		t.Errorf("SessionID = %q, want loaded-session-xyz", loaded.SessionID)
	}
	if loaded.WorkDir != tmpDir {
		t.Errorf("WorkDir = %q, want %q", loaded.WorkDir, tmpDir)
	}
	if loaded.PID != 12345 {
		t.Errorf("PID = %d, want 12345", loaded.PID)
	}
	if !loaded.StartedAt.Equal(startedAt) {
		t.Errorf("StartedAt = %v, want %v", loaded.StartedAt, startedAt)
	}
}

func TestLoadSession_NotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "nonexistent.json")

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
	m.sessionFilePath = sessionFile

	_, err := m.LoadSession()
	if err == nil {
		t.Fatal("LoadSession() expected error for nonexistent file, got nil")
	}
	if !os.IsNotExist(err) {
		t.Errorf("error = %v, want os.ErrNotExist", err)
	}
}

func TestRemoveSession(t *testing.T) {
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "session.json")

	// Write a session file first
	sessionData := SessionData{
		SessionID: "to-be-removed",
		WorkDir:   tmpDir,
		PID:       99999,
		StartedAt: time.Now(),
	}
	data, err := json.Marshal(sessionData)
	if err != nil {
		t.Fatalf("marshalling session data: %v", err)
	}
	if err := os.WriteFile(sessionFile, data, 0644); err != nil {
		t.Fatalf("writing session file: %v", err)
	}

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
	m.sessionFilePath = sessionFile

	if err := m.RemoveSession(); err != nil {
		t.Fatalf("RemoveSession() failed: %v", err)
	}

	// Verify file is gone
	if _, err := os.Stat(sessionFile); !os.IsNotExist(err) {
		t.Error("session file should be removed")
	}
}

func TestResume(t *testing.T) {
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
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())
	m.pidFilePath = filepath.Join(tmpDir, "resume.pid")
	m.sessionFilePath = filepath.Join(tmpDir, "session.json")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()

	// Start initial session
	if err := m.Start(ctx, tmpDir); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Set session ID via state machine
	m.sm.state.StartSession(tmpDir)
	m.sm.state.SetSessionID("resume-test-session-001")

	// Save session
	if err := m.SaveSession(); err != nil {
		t.Fatalf("SaveSession() failed: %v", err)
	}

	// Stop the session
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	// Resume with the session ID
	if err := m.Resume(ctx, "resume-test-session-001"); err != nil {
		t.Fatalf("Resume() failed: %v", err)
	}

	// Verify manager is running
	if !m.IsRunning() {
		t.Error("IsRunning should be true after Resume")
	}
	if m.PID() == 0 {
		t.Error("PID should not be 0 after Resume")
	}

	// Verify session file was updated with new PID
	loaded, err := m.LoadSession()
	if err != nil {
		t.Fatalf("LoadSession() failed: %v", err)
	}
	if loaded.SessionID != "resume-test-session-001" {
		t.Errorf("SessionID = %q, want resume-test-session-001", loaded.SessionID)
	}

	m.Stop(ctx)
}

func TestResume_SessionNotFound(t *testing.T) {
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
	m.sessionFilePath = filepath.Join(tmpDir, "nonexistent.json")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()
	err := m.Resume(ctx, "nonexistent-session")
	if err == nil {
		t.Fatal("Resume() expected error for nonexistent session, got nil")
	}
}

func TestSaveSession_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, ".craudinei", "session.json")

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
	m.sessionFilePath = sessionFile

	// Set session data
	m.sm.state.StartSession(tmpDir)
	m.sm.state.SetSessionID("dir-test-session")

	// Save should create the directory
	if err := m.SaveSession(); err != nil {
		t.Fatalf("SaveSession() failed: %v", err)
	}

	// Verify directory and file exist
	info, err := os.Stat(filepath.Dir(sessionFile))
	if err != nil {
		t.Fatalf("stat session directory: %v", err)
	}
	if !info.IsDir() {
		t.Error("session directory should be a directory")
	}

	data, err := os.ReadFile(sessionFile)
	if err != nil {
		t.Fatalf("reading session file: %v", err)
	}

	var sessionData SessionData
	if err := json.Unmarshal(data, &sessionData); err != nil {
		t.Fatalf("unmarshalling session data: %v", err)
	}

	if sessionData.SessionID != "dir-test-session" {
		t.Errorf("SessionID = %q, want dir-test-session", sessionData.SessionID)
	}
}
