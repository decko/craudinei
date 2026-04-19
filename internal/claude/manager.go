// Package claude provides core engine components for the Craudinei session runner.
package claude

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/decko/craudinei/internal/config"
	"github.com/decko/craudinei/internal/types"

	"log/slog"
)

// Manager manages the Claude Code subprocess lifecycle.
type Manager struct {
	mu          sync.Mutex
	cfg         *config.Config
	sm          *StateMachine
	queue       *InputQueue
	logger      *slog.Logger
	cmd         *exec.Cmd
	pid         int
	pgid        int
	pidFilePath string
	stdin       io.WriteCloser
	stdout      io.Reader
}

// NewManager creates a new Manager with the given configuration, state machine,
// input queue, and logger.
func NewManager(cfg *config.Config, sm *StateMachine, queue *InputQueue, logger *slog.Logger) *Manager {
	return &Manager{
		cfg:         cfg,
		sm:          sm,
		queue:       queue,
		logger:      logger,
		pidFilePath: "/tmp/craudinei.pid",
	}
}

// Start spawns the Claude Code subprocess with the given working directory.
// It validates the work directory, checks for the API key, transitions the
// state machine, and starts the subprocess with process group management.
func (m *Manager) Start(ctx context.Context, workDir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil && m.cmd.Process != nil {
		return fmt.Errorf("manager: subprocess already running with PID %d", m.pid)
	}

	if err := config.ValidateWorkDir(workDir, m.cfg.Claude.AllowedPaths); err != nil {
		return fmt.Errorf("manager: validating work directory: %w", err)
	}

	if _, ok := os.LookupEnv("ANTHROPIC_API_KEY"); !ok {
		return fmt.Errorf("manager: ANTHROPIC_API_KEY environment variable is not set")
	}

	if err := m.sm.Transition(types.StatusStarting); err != nil {
		return fmt.Errorf("manager: transitioning to starting: %w", err)
	}

	allowedTools := ""
	if len(m.cfg.Claude.AllowedTools) > 0 {
		allowedTools = strings.Join(m.cfg.Claude.AllowedTools, " ")
	}

	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--append-system-prompt", m.cfg.Claude.SystemPrompt,
	}
	if allowedTools != "" {
		args = append(args, "--allowedTools", allowedTools)
	}
	args = append(args,
		"--permission-prompt-tool", "craudinei_approval",
		"--mcp-config", "/tmp/craudinei-mcp.json",
		"--max-turns", fmt.Sprintf("%d", m.cfg.Claude.MaxTurns),
		"--max-budget-usd", fmt.Sprintf("%f", m.cfg.Claude.MaxBudgetUSD),
		"--add-dir", workDir,
	)

	cmd := exec.Command(m.cfg.Claude.Binary, args...)
	cmd.Dir = workDir
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		if transitionErr := m.sm.Transition(types.StatusCrashed); transitionErr != nil {
			m.logger.Error("manager: failed to transition to crashed after stdin pipe failure", "error", transitionErr)
		}
		if transitionErr := m.sm.Transition(types.StatusIdle); transitionErr != nil {
			m.logger.Error("manager: failed to transition to idle after stdin pipe failure", "error", transitionErr)
		}
		return fmt.Errorf("manager: creating stdin pipe: %w", err)
	}
	m.stdin = stdinPipe

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		if transitionErr := m.sm.Transition(types.StatusCrashed); transitionErr != nil {
			m.logger.Error("manager: failed to transition to crashed after stdout pipe failure", "error", transitionErr)
		}
		if transitionErr := m.sm.Transition(types.StatusIdle); transitionErr != nil {
			m.logger.Error("manager: failed to transition to idle after stdout pipe failure", "error", transitionErr)
		}
		return fmt.Errorf("manager: creating stdout pipe: %w", err)
	}
	m.stdout = stdoutPipe

	cmd.Stderr = &slogWriter{m.logger}

	if err := cmd.Start(); err != nil {
		if transitionErr := m.sm.Transition(types.StatusCrashed); transitionErr != nil {
			m.logger.Error("manager: failed to transition to crashed after start failure", "error", transitionErr)
		}
		if transitionErr := m.sm.Transition(types.StatusIdle); transitionErr != nil {
			m.logger.Error("manager: failed to transition to idle after start failure", "error", transitionErr)
		}
		return fmt.Errorf("manager: starting subprocess: %w", err)
	}

	m.cmd = cmd
	m.pid = cmd.Process.Pid
	m.pgid = cmd.Process.Pid

	if err := m.writePIDFile(m.pid); err != nil {
		m.logger.Error("manager: failed to write PID file", "error", err)
	}

	return nil
}

// Stop gracefully shuts down the subprocess by sending SIGINT to the process
// group, waiting up to 5 seconds, then sending SIGKILL if necessary. It then
// reaps the process and transitions the state machine to idle.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	if m.cmd == nil || m.cmd.Process == nil {
		m.mu.Unlock()
		return fmt.Errorf("manager: no subprocess running")
	}
	pgid := m.pgid
	cmd := m.cmd
	m.mu.Unlock()

	// Determine the appropriate transition based on current state.
	currentStatus := m.sm.Status()

	switch currentStatus {
	case types.StatusStarting:
		// Process in startup phase - kill directly
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		cmd.Wait()
		// Must transition starting → crashed → idle
		if err := m.sm.Transition(types.StatusCrashed); err != nil {
			return fmt.Errorf("manager: transitioning to crashed: %w", err)
		}
	case types.StatusRunning, types.StatusWaitingApproval:
		// Normal case: graceful shutdown via SIGINT, then SIGKILL if needed
		_ = syscall.Kill(-pgid, syscall.SIGINT)
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			<-done
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			<-done
		}
		if err := m.sm.Transition(types.StatusStopping); err != nil {
			return fmt.Errorf("manager: transitioning to stopping: %w", err)
		}
	default:
		// For any other state, just ensure process is dead
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		cmd.Wait()
	}

	// Clean up PID file
	_ = m.removePIDFile()

	m.mu.Lock()
	m.cmd = nil
	m.pid = 0
	m.pgid = 0
	m.mu.Unlock()

	// Transition to idle (from crashed or stopping)
	if err := m.sm.Transition(types.StatusIdle); err != nil {
		return fmt.Errorf("manager: transitioning to idle: %w", err)
	}

	return nil
}

// KillOrphan detects and kills any orphan process from a previous session.
// It reads the PID file, checks if the process is alive, and kills the
// process group if the process exists.
func (m *Manager) KillOrphan() error {
	file, err := os.Open(m.pidFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("manager: opening PID file: %w", err)
	}
	defer file.Close()

	var pid int
	if _, err := fmt.Fscanf(file, "%d", &pid); err != nil {
		// Corrupt PID file - remove it
		_ = os.Remove(m.pidFilePath)
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("manager: finding process %d: %w", pid, err)
	}

	// Check if process is alive by sending signal 0
	if err := proc.Signal(syscall.Signal(0)); err == nil {
		// Process is alive - kill the process group
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
			return fmt.Errorf("manager: killing orphan process group: %w", err)
		}
	}

	// Remove the stale PID file
	_ = os.Remove(m.pidFilePath)
	return nil
}

// PID returns the PID of the managed subprocess, or 0 if not running.
func (m *Manager) PID() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pid
}

// IsRunning returns true if a subprocess is currently managed.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil && m.cmd.Process != nil
}

// writePIDFile writes the given PID to the PID file.
func (m *Manager) writePIDFile(pid int) error {
	file, err := os.OpenFile(m.pidFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("manager: opening PID file: %w", err)
	}
	defer file.Close()

	_, err = fmt.Fprintf(file, "%d\n", pid)
	if err != nil {
		return fmt.Errorf("manager: writing PID: %w", err)
	}
	return nil
}

// removePIDFile removes the PID file, logging a warning on failure.
func (m *Manager) removePIDFile() error {
	err := os.Remove(m.pidFilePath)
	if err != nil && !os.IsNotExist(err) {
		m.logger.Warn("manager: failed to remove PID file", "error", err)
	}
	return nil
}

// slogWriter is an io.Writer that writes to slog.Error.
type slogWriter struct {
	logger *slog.Logger
}

// Write implements io.Writer by logging the given bytes to slog.Error.
func (w *slogWriter) Write(p []byte) (n int, err error) {
	w.logger.Error("subprocess stderr", "output", string(p))
	return len(p), nil
}
