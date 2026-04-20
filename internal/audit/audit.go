// Package audit provides structured audit logging for the Craudinei bot.
package audit

import (
	"fmt"
	"log/slog"
	"os"
)

// Logger is a structured audit logger that wraps slog.Logger with
// domain-specific logging methods for auth, commands, tools, and sessions.
type Logger struct {
	log       *slog.Logger
	file      *os.File
	closeable bool
}

// New creates a new audit Logger that writes to the given slog.Logger.
func New(log *slog.Logger) *Logger {
	if log == nil {
		panic("audit: logger must not be nil")
	}
	return &Logger{log: log}
}

// NewWithFile creates a new audit Logger that writes to the specified file path.
// If auditFilePath is empty, writes to stderr.
// The returned Logger must be closed when done if a file path was provided.
func NewWithFile(auditFilePath string) (*Logger, error) {
	if auditFilePath == "" {
		handler := slog.NewJSONHandler(os.Stderr, nil)
		return &Logger{log: slog.New(handler)}, nil
	}

	file, err := os.OpenFile(auditFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("audit: opening log file: %w", err)
	}

	handler := slog.NewJSONHandler(file, nil)
	return &Logger{
		log:       slog.New(handler),
		file:      file,
		closeable: true,
	}, nil
}

// Close closes the underlying file handle if one was opened.
// It is safe to call Close on a Logger that writes to stderr.
func (l *Logger) Close() error {
	if l.closeable && l.file != nil {
		err := l.file.Close()
		l.closeable = false
		return err
	}
	return nil
}

// AuthAttempt logs an authentication attempt.
func (l *Logger) AuthAttempt(userID int64, outcome string, target string) {
	l.log.Info("auth attempt",
		"action", "auth_attempt",
		"user_id", userID,
		"outcome", outcome,
		"target", target,
	)
}

// Command logs a command execution.
func (l *Logger) Command(userID int64, command string, args string, outcome string, target string) {
	l.log.Info("command",
		"action", "command",
		"user_id", userID,
		"command", command,
		"args", args,
		"outcome", outcome,
		"target", target,
	)
}

// ToolDecision logs a tool decision.
func (l *Logger) ToolDecision(userID int64, tool string, inputSummary string, outcome string, target string) {
	l.log.Info("tool decision",
		"action", "tool_decision",
		"user_id", userID,
		"tool", tool,
		"input", inputSummary,
		"outcome", outcome,
		"target", target,
	)
}

// SessionEvent logs a session event.
func (l *Logger) SessionEvent(userID int64, event string, sessionID string, workDir string, outcome string, target string) {
	l.log.Info("session event",
		"action", "session",
		"user_id", userID,
		"event", event,
		"session_id", sessionID,
		"work_dir", workDir,
		"outcome", outcome,
		"target", target,
	)
}

// UnauthorizedAccess logs an unauthorized access attempt at Warn level.
func (l *Logger) UnauthorizedAccess(userID int64, target string) {
	l.log.Warn("unauthorized access",
		"action", "unauthorized_access",
		"user_id", userID,
		"target", target,
	)
}
