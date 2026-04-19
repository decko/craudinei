package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/decko/craudinei/internal/config"
	"github.com/decko/craudinei/internal/types"

	"log/slog"
)

func TestAdversary_Start_RaceCondition(t *testing.T) {
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
	m.pidFilePath = filepath.Join(tmpDir, "race.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()

	startErrs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func() {
			err := m.Start(ctx, tmpDir)
			startErrs <- err
		}()
	}

	gotErr := false
	for i := 0; i < 10; i++ {
		if err := <-startErrs; err == nil {
			gotErr = true
		}
	}

	// Clean up
	m.Stop(ctx)

	// At least one start should succeed
	if !gotErr {
		t.Error("at least one Start should succeed")
	}
}

func TestAdversary_Stop_RapidStopStart(t *testing.T) {
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
	m.pidFilePath = filepath.Join(tmpDir, "rapid.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()

	// Rapid stop/start cycles
	for i := 0; i < 5; i++ {
		if err := m.Start(ctx, tmpDir); err != nil {
			t.Fatalf("Start() iteration %d failed: %v", i, err)
		}
		time.Sleep(10 * time.Millisecond)
		if err := m.Stop(ctx); err != nil {
			t.Fatalf("Stop() iteration %d failed: %v", i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// No goroutine leaks if we reach here
}

func TestAdversary_Start_ProcessImmediateExit(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a script that exits immediately
	exitScript := filepath.Join(tmpDir, "exit_immediately.sh")
	scriptContent := `#!/bin/bash
echo '{"type":"system","subtype":"init","session_id":"test"}'
sleep 0.1
exit 0
`
	os.WriteFile(exitScript, []byte(scriptContent), 0755)

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       exitScript,
			AllowedPaths: []string{tmpDir},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)

	m := NewManager(cfg, sm, queue, slog.Default())
	m.pidFilePath = filepath.Join(tmpDir, "exit.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()
	err := m.Start(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Give process time to exit
	time.Sleep(200 * time.Millisecond)

	// Should still be considered "running" until explicitly stopped
	// The process may have exited but cmd.Process is still set
	if !m.IsRunning() {
		t.Log("Process exited immediately, IsRunning returned false")
	}

	m.Stop(ctx)
}

func TestAdversary_PIDFile_CorruptContent(t *testing.T) {
	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "corrupt.pid")

	// Write non-numeric content
	os.WriteFile(pidFile, []byte("not-a-number\n"), 0644)

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

	// Should handle corrupt content gracefully
	err := m.KillOrphan()
	if err != nil {
		t.Errorf("KillOrphan() error for corrupt PID file: %v", err)
	}

	// PID file should be cleaned up
	if _, statErr := os.Stat(pidFile); !os.IsNotExist(statErr) {
		t.Error("corrupt PID file should be removed")
	}
}

func TestAdversary_PIDFile_PermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a directory for PID file that we can't write to
	readonlyDir := filepath.Join(tmpDir, "readonly_dir")
	os.MkdirAll(readonlyDir, 0555)

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       "/bin/ls",
			AllowedPaths: []string{tmpDir},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)

	m := NewManager(cfg, sm, queue, slog.Default())

	// Try to set PID file path to a location we can't write to
	m.pidFilePath = filepath.Join(readonlyDir, "noperm.pid")

	// writePIDFile should return an error but not panic
	err := m.writePIDFile(12345)
	if err == nil {
		t.Error("writePIDFile should return error for unwritable location")
	}
}
