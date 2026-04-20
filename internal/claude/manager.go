// Package claude provides core engine components for the Craudinei session runner.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/decko/craudinei/internal/config"
	"github.com/decko/craudinei/internal/router"
	"github.com/decko/craudinei/internal/types"

	"log/slog"
)

// SessionData represents the persisted session state.
type SessionData struct {
	SessionID string    `json:"session_id"`
	WorkDir   string    `json:"work_dir"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

// Manager manages the Claude Code subprocess lifecycle.
type Manager struct {
	mu              sync.Mutex
	cfg             *config.Config
	sm              *StateMachine
	queue           *InputQueue
	router          *router.Router
	logger          *slog.Logger
	cmd             *exec.Cmd
	pid             int
	pgid            int
	pidFilePath     string
	sessionFilePath string
	stdin           io.WriteCloser
	stdout          io.Reader
	wg              sync.WaitGroup
	cancel          context.CancelFunc
	readyCh         chan struct{}
	stopping        atomic.Bool
}

// NewManager creates a new Manager with the given configuration, state machine,
// input queue, router, and logger.
func NewManager(cfg *config.Config, sm *StateMachine, queue *InputQueue, router *router.Router, logger *slog.Logger) *Manager {
	homeDir, _ := os.UserHomeDir()
	sessionFilePath := filepath.Join(homeDir, ".craudinei", "session.json")
	return &Manager{
		cfg:             cfg,
		sm:              sm,
		queue:           queue,
		router:          router,
		logger:          logger,
		pidFilePath:     "/tmp/craudinei.pid",
		sessionFilePath: sessionFilePath,
		readyCh:         make(chan struct{}),
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

	// Create derived context for goroutines and start pipe management
	// Re-initialize readyCh in case Start is called again after Stop
	m.readyCh = make(chan struct{})
	m.stopping.Store(false)
	goroutineCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.startStdinWriter(goroutineCtx)
	m.startStdoutReader(goroutineCtx)

	// SaveSession() is called by the event handler after the init event sets
	// the session ID. The session ID is set asynchronously when the subprocess
	// sends the init event, so we can't save it synchronously here.

	return nil
}

// Stop gracefully shuts down the subprocess by sending SIGINT to the process
// group, waiting up to 5 seconds, then sending SIGKILL if necessary. It then
// reaps the process and transitions the state machine to idle.
func (m *Manager) Stop(ctx context.Context) error {
	if !m.stopping.CompareAndSwap(false, true) {
		return fmt.Errorf("manager: stop already in progress")
	}
	m.mu.Lock()
	if m.cmd == nil || m.cmd.Process == nil {
		m.mu.Unlock()
		return fmt.Errorf("manager: no subprocess running")
	}
	pgid := m.pgid
	cmd := m.cmd
	m.mu.Unlock()

	// Signal goroutines to stop before sending signals to the process
	if m.cancel != nil {
		m.cancel()
	}

	// Close stdin to signal EOF to subprocess and unblock stdin writer
	m.mu.Lock()
	if m.stdin != nil {
		_ = m.stdin.Close()
		m.stdin = nil
	}
	m.mu.Unlock()

	// Determine the appropriate transition based on current state.
	currentStatus := m.sm.Status()

	// Note: The signal sent depends on the current state machine status.
	// When status is StatusStarting, SIGKILL is sent immediately because
	// the process has not yet completed initialization. The transition from
	// StatusStarting to StatusRunning is performed by the event handler
	// upon receiving the init event from the subprocess. If this transition
	// is delayed, Stop() will use SIGKILL instead of the graceful SIGINT.
	switch currentStatus {
	case types.StatusStarting:
		// Process in startup phase - kill directly
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	case types.StatusRunning, types.StatusWaitingApproval:
		// Normal case: graceful shutdown via SIGINT, then SIGKILL if needed
		_ = syscall.Kill(-pgid, syscall.SIGINT)
	default:
		// For any other state, just ensure process is dead
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}

	// Wait for our goroutines to finish (they should finish now that pipes are closed)
	m.wg.Wait()

	// Reap the process - for running/waiting_approval we wait with timeout
	if currentStatus == types.StatusRunning || currentStatus == types.StatusWaitingApproval {
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
	} else {
		// For starting and default, just reap the process directly
		_ = cmd.Wait()
		if currentStatus == types.StatusStarting {
			if err := m.sm.Transition(types.StatusCrashed); err != nil {
				return fmt.Errorf("manager: transitioning to crashed: %w", err)
			}
		}
	}

	// Clean up PID file
	_ = m.removePIDFile()

	m.mu.Lock()
	m.stdout = nil
	m.mu.Unlock()

	// Transition to idle (from crashed or stopping)
	if err := m.sm.Transition(types.StatusIdle); err != nil {
		return fmt.Errorf("manager: transitioning to idle: %w", err)
	}

	m.mu.Lock()
	m.cmd = nil
	m.pid = 0
	m.pgid = 0
	m.mu.Unlock()

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

	// Remove the stale session file
	_ = m.RemoveSession()

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

// Status returns the current session status from the state machine.
func (m *Manager) Status() types.SessionStatus {
	return m.sm.Status()
}

// SessionID returns the current session ID from the state machine.
func (m *Manager) SessionID() string {
	return m.sm.state.SessionID()
}

// WorkDir returns the current working directory from the state machine.
func (m *Manager) WorkDir() string {
	return m.sm.state.WorkDir()
}

// AllowedCommands returns the list of allowed commands for the current state.
func (m *Manager) AllowedCommands() []string {
	return m.sm.AllowedCommands()
}

// CommandGuard validates whether the given command is allowed in the current state.
func (m *Manager) CommandGuard(command string) error {
	return m.sm.CommandGuard(command)
}

// Transition moves the state machine to the target status if valid.
func (m *Manager) Transition(target types.SessionStatus) error {
	return m.sm.Transition(target)
}

// Stdin returns the stdin pipe writer for direct writes (e.g., approval responses).
// Returns nil if no subprocess is running.
func (m *Manager) Stdin() io.WriteCloser {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stdin
}

// WaitReady blocks until the stdout reader goroutine has started or the context is cancelled.
func (m *Manager) WaitReady(ctx context.Context) error {
	select {
	case <-m.readyCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// startStdinWriter starts a goroutine that drains the input queue and writes
// messages to the subprocess stdin.
func (m *Manager) startStdinWriter(ctx context.Context) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for {
			msg, err := m.queue.Dequeue(ctx)
			if err != nil {
				// Context cancelled or queue closed
				if ctx.Err() != nil {
					return
				}
				// Queue was closed
				m.mu.Lock()
				if m.stdin != nil {
					_ = m.stdin.Close()
				}
				m.mu.Unlock()
				return
			}

			data, err := json.Marshal(msg)
			if err != nil {
				m.logger.Error("manager: marshalling message for stdin", "error", err)
				continue
			}

			m.mu.Lock()
			stdin := m.stdin
			m.mu.Unlock()

			if stdin == nil {
				return
			}

			if _, err := stdin.Write(data); err != nil {
				if ctx.Err() != nil {
					return
				}
				m.logger.Error("manager: writing to stdin", "error", err)
				m.mu.Lock()
				if m.sm.Status() != types.StatusCrashed {
					if transitionErr := m.sm.Transition(types.StatusCrashed); transitionErr != nil {
						m.logger.Error("manager: failed to transition to crashed", "error", transitionErr)
					}
				}
				m.mu.Unlock()
				return
			}

			if _, err := stdin.Write([]byte("\n")); err != nil {
				if ctx.Err() != nil {
					return
				}
				m.logger.Error("manager: writing newline to stdin", "error", err)
				m.mu.Lock()
				if m.sm.Status() != types.StatusCrashed {
					if transitionErr := m.sm.Transition(types.StatusCrashed); transitionErr != nil {
						m.logger.Error("manager: failed to transition to crashed", "error", transitionErr)
					}
				}
				m.mu.Unlock()
				return
			}
		}
	}()
}

// startStdoutReader starts a goroutine that reads from stdout and feeds
// lines to the router.
func (m *Manager) startStdoutReader(ctx context.Context) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()

		scanner := bufio.NewScanner(m.stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		// Close readyCh immediately after scanner is created so WaitReady() returns
		// as soon as the goroutine is running and ready to scan.
		close(m.readyCh)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			if err := m.router.Feed(line); err != nil {
				m.logger.Error("manager: feeding line to router", "error", err)
			}
		}

		if err := scanner.Err(); err != nil {
			m.logger.Error("manager: stdout scanner error", "error", err)
		}

		// Check if this was unexpected EOF (process crashed)
		select {
		case <-ctx.Done():
			// Context was cancelled - graceful shutdown, expected
		default:
			// Context not cancelled - process exited unexpectedly
			m.logger.Error("manager: subprocess exited unexpectedly")
			m.mu.Lock()
			if m.sm.Status() != types.StatusCrashed {
				if transitionErr := m.sm.Transition(types.StatusCrashed); transitionErr != nil {
					m.logger.Error("manager: failed to transition to crashed", "error", transitionErr)
				}
			}
			// Close the input queue so Enqueue returns ErrQueueClosed
			m.queue.Close()
			m.mu.Unlock()
		}
	}()
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

// SaveSession persists the current session state to the session file.
// Called after Start() succeeds and after the init event sets the session ID.
func (m *Manager) SaveSession() error {
	sessionID := m.sm.state.SessionID()
	workDir := m.sm.state.WorkDir()
	startedAt := m.sm.state.StartedAt()

	data := SessionData{
		SessionID: sessionID,
		WorkDir:   workDir,
		PID:       m.pid,
		StartedAt: startedAt,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("manager: marshalling session data: %w", err)
	}

	// Create the directory if it doesn't exist
	dir := filepath.Dir(m.sessionFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("manager: creating session directory: %w", err)
	}

	// Write to temp file then rename for atomicity
	tmpFile := m.sessionFilePath + ".tmp"
	if err := os.WriteFile(tmpFile, jsonData, 0600); err != nil {
		return fmt.Errorf("manager: writing session file: %w", err)
	}
	if err := os.Rename(tmpFile, m.sessionFilePath); err != nil {
		return fmt.Errorf("manager: renaming session file: %w", err)
	}

	return nil
}

// UpdateSessionID updates the session ID in the state machine and persists
// the updated session. Called by the event handler after receiving the init
// event from the subprocess.
func (m *Manager) UpdateSessionID(sessionID string) error {
	m.sm.state.SetSessionID(sessionID)
	return m.SaveSession()
}

// LoadSession reads the session file and returns the persisted session data.
// Returns os.ErrNotExist if no session file exists.
func (m *Manager) LoadSession() (*SessionData, error) {
	file, err := os.Open(m.sessionFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("manager: opening session file: %w", err)
	}
	defer file.Close()

	var data SessionData
	if err := json.NewDecoder(file).Decode(&data); err != nil {
		return nil, fmt.Errorf("manager: decoding session file: %w", err)
	}

	if data.SessionID == "" {
		return nil, fmt.Errorf("manager: session file missing session_id")
	}
	if data.WorkDir == "" {
		return nil, fmt.Errorf("manager: session file missing work_dir")
	}

	return &data, nil
}

// RemoveSession removes the session file. Called during Stop() cleanup.
func (m *Manager) RemoveSession() error {
	err := os.Remove(m.sessionFilePath)
	if err != nil && !os.IsNotExist(err) {
		m.logger.Warn("manager: failed to remove session file", "error", err)
	}
	return nil
}

// Resume spawns a new subprocess with --resume <session_id> to continue
// a previous session. It validates the session file exists, loads the
// session ID and work directory, and starts the subprocess with the
// --resume flag.
func (m *Manager) Resume(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil && m.cmd.Process != nil {
		return fmt.Errorf("manager: subprocess already running with PID %d", m.pid)
	}

	sessionData, err := m.LoadSession()
	if err != nil {
		return fmt.Errorf("manager: loading session %q: %w", sessionID, err)
	}

	if sessionData.SessionID != sessionID {
		return fmt.Errorf("manager: session ID mismatch: got %q, want %q", sessionData.SessionID, sessionID)
	}

	workDir := sessionData.WorkDir
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
		"--resume", sessionID,
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

	// Create derived context for goroutines and start pipe management
	m.readyCh = make(chan struct{})
	m.stopping.Store(false)
	goroutineCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.startStdinWriter(goroutineCtx)
	m.startStdoutReader(goroutineCtx)

	// SaveSession() is called by the event handler after the init event sets
	// the session ID. For Resume(), the session ID is already set (loaded from
	// the session file), but we still defer saving to ensure atomicity.

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
