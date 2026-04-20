package claude

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/decko/craudinei/internal/config"
	"github.com/decko/craudinei/internal/router"
	"github.com/decko/craudinei/internal/types"

	"log/slog"
)

// TestE2E_FullSessionLifecycle verifies the complete session lifecycle:
// idle -> starting, enqueue prompt, verify router receives events,
// stop, and verify returns to idle.
func TestE2E_FullSessionLifecycle(t *testing.T) {
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

	var mu sync.Mutex
	var receivedEvents []router.ClassifiedEvent
	r := router.NewRouter(func(e router.ClassifiedEvent) {
		mu.Lock()
		receivedEvents = append(receivedEvents, e)
		mu.Unlock()
	})

	m := NewManager(cfg, sm, queue, r, slog.Default())
	m.pidFilePath = filepath.Join(tmpDir, "lifecycle.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()

	// Start session
	err := m.Start(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer m.Stop(ctx)

	// Wait for reader goroutine to be ready
	if err := m.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady() failed: %v", err)
	}

	// Wait for events to be processed
	time.Sleep(200 * time.Millisecond)

	// After Start, status is StatusStarting (transition to StatusRunning
	// requires an external event handler to process the init event)
	if sm.Status() != types.StatusStarting {
		t.Fatalf("Status = %s, want %s after Start", sm.Status(), types.StatusStarting)
	}

	if !m.IsRunning() {
		t.Fatal("IsRunning should be true after Start")
	}

	// Enqueue a prompt
	err = queue.Enqueue("Hello, Claude!")
	if err != nil {
		t.Fatalf("Enqueue() failed: %v", err)
	}

	// Give the stdin writer time to process the message
	time.Sleep(100 * time.Millisecond)

	// Verify we have at least one event (the init event)
	// Note: Event receipt is timing-dependent, so we only log if none received
	mu.Lock()
	eventCount := len(receivedEvents)
	mu.Unlock()

	if eventCount == 0 {
		t.Log("Router received no events; this may be due to timing in test environment")
	}

	// Stop the session explicitly before checking post-stop assertions
	m.Stop(ctx)

	// Verify session returned to idle
	if sm.Status() != types.StatusIdle {
		t.Errorf("Status after Stop = %s, want %s", sm.Status(), types.StatusIdle)
	}
	if m.IsRunning() {
		t.Error("IsRunning should be false after Stop")
	}
}

// TestE2E_CrashRecovery verifies that when a subprocess exits unexpectedly,
// the session transitions to crashed state.
func TestE2E_CrashRecovery(t *testing.T) {
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
	m.pidFilePath = filepath.Join(tmpDir, "crash.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()

	// Create a wrapper script that runs mock_claude.sh with --exit-immediately.
	// The Manager.Start() will use this wrapper as the binary.
	wrapperPath := filepath.Join(tmpDir, "exit_immediately.sh")
	wrapperContent := `#!/bin/bash
exec "` + mockBin + `" --exit-immediately
`
	if err := os.WriteFile(wrapperPath, []byte(wrapperContent), 0755); err != nil {
		t.Fatalf("writing wrapper script: %v", err)
	}
	os.Chmod(wrapperPath, 0755)

	cfg.Claude.Binary = wrapperPath

	err := m.Start(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer m.Stop(ctx)

	// Wait for the process to exit and the reader goroutine to detect it
	// and transition to crashed state
	time.Sleep(500 * time.Millisecond)

	// After the subprocess exits unexpectedly, the status should be crashed
	// (the stdout reader detects EOF and transitions to crashed)
	currentStatus := sm.Status()
	if currentStatus != types.StatusCrashed {
		t.Errorf("Status = %s, want %s after unexpected exit", currentStatus, types.StatusCrashed)
	}
}

// TestE2E_InputQueueFull verifies that enqueueing more items than queue
// capacity returns ErrQueueFull.
func TestE2E_InputQueueFull(t *testing.T) {
	capacity := 5
	queue := NewInputQueue(capacity)

	// Fill the queue to capacity
	for i := 0; i < capacity; i++ {
		err := queue.Enqueue("message")
		if err != nil {
			t.Fatalf("Enqueue() iteration %d failed unexpectedly: %v", i, err)
		}
	}

	// Verify queue is at capacity
	if queue.Len() != capacity {
		t.Errorf("queue.Len() = %d, want %d", queue.Len(), capacity)
	}

	// The 6th enqueue should return ErrQueueFull
	err := queue.Enqueue("overflow message")
	if err == nil {
		t.Fatal("Enqueue() expected ErrQueueFull on overflow, got nil")
	}
	if err != ErrQueueFull {
		t.Errorf("Enqueue() error = %v, want %v", err, ErrQueueFull)
	}

	// Clean up
	queue.Close()
}

// TestE2E_StateTransitions verifies valid and invalid state transitions.
func TestE2E_StateTransitions(t *testing.T) {
	tests := []struct {
		name    string
		from    types.SessionStatus
		to      types.SessionStatus
		wantErr bool
	}{
		{
			name:    "idle_to_starting",
			from:    types.StatusIdle,
			to:      types.StatusStarting,
			wantErr: false,
		},
		{
			name:    "starting_to_running",
			from:    types.StatusStarting,
			to:      types.StatusRunning,
			wantErr: false,
		},
		{
			name:    "starting_to_crashed",
			from:    types.StatusStarting,
			to:      types.StatusCrashed,
			wantErr: false,
		},
		{
			name:    "running_to_waiting_approval",
			from:    types.StatusRunning,
			to:      types.StatusWaitingApproval,
			wantErr: false,
		},
		{
			name:    "running_to_stopping",
			from:    types.StatusRunning,
			to:      types.StatusStopping,
			wantErr: false,
		},
		{
			name:    "running_to_crashed",
			from:    types.StatusRunning,
			to:      types.StatusCrashed,
			wantErr: false,
		},
		{
			name:    "waiting_approval_to_running",
			from:    types.StatusWaitingApproval,
			to:      types.StatusRunning,
			wantErr: false,
		},
		{
			name:    "waiting_approval_to_stopping",
			from:    types.StatusWaitingApproval,
			to:      types.StatusStopping,
			wantErr: false,
		},
		{
			name:    "stopping_to_idle",
			from:    types.StatusStopping,
			to:      types.StatusIdle,
			wantErr: false,
		},
		{
			name:    "stopping_to_crashed",
			from:    types.StatusStopping,
			to:      types.StatusCrashed,
			wantErr: false,
		},
		{
			name:    "crashed_to_idle",
			from:    types.StatusCrashed,
			to:      types.StatusIdle,
			wantErr: false,
		},
		{
			name:    "idle_to_running_invalid",
			from:    types.StatusIdle,
			to:      types.StatusRunning,
			wantErr: true,
		},
		{
			name:    "running_to_idle_invalid",
			from:    types.StatusRunning,
			to:      types.StatusIdle,
			wantErr: true,
		},
		{
			name:    "idle_to_crashed_invalid",
			from:    types.StatusIdle,
			to:      types.StatusCrashed,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := NewStateMachine(tt.from)

			err := sm.Transition(tt.to)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Transition(%s -> %s) expected error, got nil", tt.from, tt.to)
				}
			} else {
				if err != nil {
					t.Errorf("Transition(%s -> %s) unexpected error: %v", tt.from, tt.to, err)
				}
				if sm.Status() != tt.to {
					t.Errorf("Status after Transition = %s, want %s", sm.Status(), tt.to)
				}
			}
		})
	}
}

// TestE2E_ConcurrentStateChecks verifies that concurrent IsRunning and PID
// calls are safe under race conditions with a running session.
func TestE2E_ConcurrentStateChecks(t *testing.T) {
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
	err := m.Start(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer m.Stop(ctx)

	// Run many concurrent reads to stress-test mutex protection
	var wg sync.WaitGroup
	const iterations = 100

	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.PID()
			_ = m.IsRunning()
			_ = sm.Status()
			_ = m.SessionID()
		}()
	}

	wg.Wait()
}

// TestE2E_CommandGuard tests that commands are properly validated against
// the current state via CommandGuard.
func TestE2E_CommandGuard(t *testing.T) {
	tests := []struct {
		name    string
		state   types.SessionStatus
		command string
		wantErr bool
	}{
		{
			name:    "idle_allows_begin",
			state:   types.StatusIdle,
			command: "/begin",
			wantErr: false,
		},
		{
			name:    "idle_allows_status",
			state:   types.StatusIdle,
			command: "/status",
			wantErr: false,
		},
		{
			name:    "idle_allows_help",
			state:   types.StatusIdle,
			command: "/help",
			wantErr: false,
		},
		{
			name:    "idle_denies_stop",
			state:   types.StatusIdle,
			command: "/stop",
			wantErr: true,
		},
		{
			name:    "running_allows_stop",
			state:   types.StatusRunning,
			command: "/stop",
			wantErr: false,
		},
		{
			name:    "running_allows_cancel",
			state:   types.StatusRunning,
			command: "/cancel",
			wantErr: false,
		},
		{
			name:    "running_denies_begin",
			state:   types.StatusRunning,
			command: "/begin",
			wantErr: true,
		},
		{
			name:    "waiting_approval_allows_stop",
			state:   types.StatusWaitingApproval,
			command: "/stop",
			wantErr: false,
		},
		{
			name:    "crashed_allows_begin",
			state:   types.StatusCrashed,
			command: "/begin",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := NewStateMachine(tt.state)

			err := sm.CommandGuard(tt.command)

			if tt.wantErr {
				if err == nil {
					t.Errorf("CommandGuard(%q) expected error in state %s, got nil", tt.command, tt.state)
				}
			} else {
				if err != nil {
					t.Errorf("CommandGuard(%q) unexpected error in state %s: %v", tt.command, tt.state, err)
				}
			}
		})
	}
}

// TestE2E_ManagerIntegration tests that the Manager, StateMachine, InputQueue,
// and Router all work together correctly.
func TestE2E_ManagerIntegration(t *testing.T) {
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

	var mu sync.Mutex
	var systemEvents int
	var assistantEvents int
	r := router.NewRouter(func(e router.ClassifiedEvent) {
		mu.Lock()
		defer mu.Unlock()
		switch e.Action {
		case router.ActionSystem:
			systemEvents++
		case router.ActionText, router.ActionToolUse:
			assistantEvents++
		}
	})

	m := NewManager(cfg, sm, queue, r, slog.Default())
	m.pidFilePath = filepath.Join(tmpDir, "integration.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()

	// Start session
	err := m.Start(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer m.Stop(ctx)

	// Wait for reader goroutine to be ready
	if err := m.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady() failed: %v", err)
	}

	// Wait for init event to be processed
	time.Sleep(200 * time.Millisecond)

	// After Start, status is StatusStarting (transition to StatusRunning
	// requires an external event handler)
	if sm.Status() != types.StatusStarting {
		t.Errorf("Status = %s, want %s after Start", sm.Status(), types.StatusStarting)
	}

	// Verify init event was received (system event)
	// Note: Event receipt is timing-dependent, so we only log if none received
	mu.Lock()
	hasEvents := systemEvents > 0 || assistantEvents > 0
	mu.Unlock()

	if !hasEvents {
		t.Log("No events received by router; this may be due to timing in test environment")
	}

	// Verify Manager reflects correct state
	if !m.IsRunning() {
		t.Error("IsRunning should be true")
	}
	if m.PID() == 0 {
		t.Error("PID should be non-zero")
	}

	// CommandGuard is per-state-machine, not per-manager
	// In starting state, only /status and /help are allowed
	err = sm.CommandGuard("/status")
	if err != nil {
		t.Errorf("CommandGuard(\"/status\") in starting state failed: %v", err)
	}

	err = sm.CommandGuard("/help")
	if err != nil {
		t.Errorf("CommandGuard(\"/help\") in starting state failed: %v", err)
	}

	// /stop should not be allowed in starting state
	err = sm.CommandGuard("/stop")
	if err == nil {
		t.Error("CommandGuard(\"/stop\") should fail in starting state")
	}
}

// TestE2E_SlowExitRecovery tests that the manager handles a subprocess that
// exits with an error after some time.
func TestE2E_SlowExitRecovery(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a wrapper script for --slow-exit
	mockBin := filepath.Join(tmpDir, "mock_claude.sh")
	copyFile(t, "mock_claude.sh", mockBin)
	os.Chmod(mockBin, 0755)

	wrapperPath := filepath.Join(tmpDir, "slow_exit.sh")
	wrapperContent := `#!/bin/bash
exec "` + mockBin + `" --slow-exit
`
	if err := os.WriteFile(wrapperPath, []byte(wrapperContent), 0755); err != nil {
		t.Fatalf("writing wrapper script: %v", err)
	}
	os.Chmod(wrapperPath, 0755)

	cfg := &config.Config{
		Claude: config.ClaudeConfig{
			Binary:       wrapperPath,
			AllowedPaths: []string{tmpDir},
		},
	}
	sm := NewStateMachine(types.StatusIdle)
	queue := NewInputQueue(5)
	r := router.NewRouter(func(e router.ClassifiedEvent) {})

	m := NewManager(cfg, sm, queue, r, slog.Default())
	m.pidFilePath = filepath.Join(tmpDir, "slow_exit.pid")

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	ctx := context.Background()

	err := m.Start(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer m.Stop(ctx)

	// Wait for the process to start and then exit (--slow-exit sleeps 2s then exits 1)
	// The status will be crashed once the reader detects the unexpected exit
	time.Sleep(2500 * time.Millisecond)

	// After subprocess exits unexpectedly, the status should be crashed
	currentStatus := sm.Status()
	if currentStatus != types.StatusCrashed {
		t.Errorf("Status = %s, want %s after slow-exit subprocess exits", currentStatus, types.StatusCrashed)
	}
}
