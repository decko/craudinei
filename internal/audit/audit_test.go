package audit

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestAuditLog_AuthAttempt(t *testing.T) {
	t.Parallel()

	buf := new(bytes.Buffer)
	handler := slog.NewJSONHandler(buf, nil)
	logger := New(slog.New(handler))

	tests := []struct {
		name    string
		userID  int64
		outcome string
		target  string
	}{
		{"success", 12345, "success", "chat:12345"},
		{"failure", 99999, "failure", "chat:99999"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			logger.AuthAttempt(tc.userID, tc.outcome, tc.target)

			var entry map[string]any
			if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
				t.Fatalf("failed to unmarshal JSON: %v", err)
			}

			if entry["action"] != "auth_attempt" {
				t.Errorf("expected action=%q, got %q", "auth_attempt", entry["action"])
			}
			if entry["user_id"] != float64(tc.userID) {
				t.Errorf("expected user_id=%v, got %v", tc.userID, entry["user_id"])
			}
			if entry["outcome"] != tc.outcome {
				t.Errorf("expected outcome=%q, got %q", tc.outcome, entry["outcome"])
			}
			if entry["target"] != tc.target {
				t.Errorf("expected target=%q, got %q", tc.target, entry["target"])
			}
		})
	}
}

func TestAuditLog_Command(t *testing.T) {
	t.Parallel()

	buf := new(bytes.Buffer)
	handler := slog.NewJSONHandler(buf, nil)
	logger := New(slog.New(handler))

	buf.Reset()
	logger.Command(12345, "help", "--verbose", "success", "session:sess-abc")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if entry["action"] != "command" {
		t.Errorf("expected action=%q, got %q", "command", entry["action"])
	}
	if entry["user_id"] != float64(12345) {
		t.Errorf("expected user_id=%v, got %v", 12345, entry["user_id"])
	}
	if entry["command"] != "help" {
		t.Errorf("expected command=%q, got %q", "help", entry["command"])
	}
	if entry["args"] != "--verbose" {
		t.Errorf("expected args=%q, got %q", "--verbose", entry["args"])
	}
	if entry["outcome"] != "success" {
		t.Errorf("expected outcome=%q, got %q", "success", entry["outcome"])
	}
	if entry["target"] != "session:sess-abc" {
		t.Errorf("expected target=%q, got %q", "session:sess-abc", entry["target"])
	}
}

func TestAuditLog_ToolDecision(t *testing.T) {
	t.Parallel()

	buf := new(bytes.Buffer)
	handler := slog.NewJSONHandler(buf, nil)
	logger := New(slog.New(handler))

	tests := []struct {
		name         string
		userID       int64
		tool         string
		inputSummary string
		outcome      string
		target       string
	}{
		{"approved", 12345, "Bash", "ls -la /tmp", "approved", "/tmp"},
		{"denied", 12345, "Bash", "rm -rf /", "denied", "/"},
		{"timeout", 12345, "Bash", "sleep 3600", "timeout", "/tmp"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			logger.ToolDecision(tc.userID, tc.tool, tc.inputSummary, tc.outcome, tc.target)

			var entry map[string]any
			if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
				t.Fatalf("failed to unmarshal JSON: %v", err)
			}

			if entry["action"] != "tool_decision" {
				t.Errorf("expected action=%q, got %q", "tool_decision", entry["action"])
			}
			if entry["tool"] != tc.tool {
				t.Errorf("expected tool=%q, got %q", tc.tool, entry["tool"])
			}
			if entry["input"] != tc.inputSummary {
				t.Errorf("expected input=%q, got %q", tc.inputSummary, entry["input"])
			}
			if entry["outcome"] != tc.outcome {
				t.Errorf("expected outcome=%q, got %q", tc.outcome, entry["outcome"])
			}
			if entry["target"] != tc.target {
				t.Errorf("expected target=%q, got %q", tc.target, entry["target"])
			}
		})
	}
}

func TestAuditLog_SessionEvent(t *testing.T) {
	t.Parallel()

	buf := new(bytes.Buffer)
	handler := slog.NewJSONHandler(buf, nil)
	logger := New(slog.New(handler))

	tests := []struct {
		name      string
		userID    int64
		event     string
		sessionID string
		workDir   string
		outcome   string
		target    string
	}{
		{"started", 12345, "started", "sess-abc123", "/home/decko/dev", "success", "sess-abc123"},
		{"stopped", 12345, "stopped", "sess-abc123", "/home/decko/dev", "success", "sess-abc123"},
		{"crashed", 12345, "crashed", "sess-abc123", "/home/decko/dev", "failure", "sess-abc123"},
		{"resumed", 12345, "resumed", "sess-abc123", "/home/decko/dev", "success", "sess-abc123"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			logger.SessionEvent(tc.userID, tc.event, tc.sessionID, tc.workDir, tc.outcome, tc.target)

			var entry map[string]any
			if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
				t.Fatalf("failed to unmarshal JSON: %v", err)
			}

			if entry["action"] != "session" {
				t.Errorf("expected action=%q, got %q", "session", entry["action"])
			}
			if entry["user_id"] != float64(tc.userID) {
				t.Errorf("expected user_id=%v, got %v", tc.userID, entry["user_id"])
			}
			if entry["event"] != tc.event {
				t.Errorf("expected event=%q, got %q", tc.event, entry["event"])
			}
			if entry["session_id"] != tc.sessionID {
				t.Errorf("expected session_id=%q, got %q", tc.sessionID, entry["session_id"])
			}
			if entry["work_dir"] != tc.workDir {
				t.Errorf("expected work_dir=%q, got %q", tc.workDir, entry["work_dir"])
			}
			if entry["outcome"] != tc.outcome {
				t.Errorf("expected outcome=%q, got %q", tc.outcome, entry["outcome"])
			}
			if entry["target"] != tc.target {
				t.Errorf("expected target=%q, got %q", tc.target, entry["target"])
			}
		})
	}
}

func TestAuditLog_UnauthorizedAccess(t *testing.T) {
	t.Parallel()

	buf := new(bytes.Buffer)
	handler := slog.NewJSONHandler(buf, nil)
	logger := New(slog.New(handler))

	buf.Reset()
	logger.UnauthorizedAccess(99999, "chat:99999")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if entry["action"] != "unauthorized_access" {
		t.Errorf("expected action=%q, got %q", "unauthorized_access", entry["action"])
	}
	if entry["user_id"] != float64(99999) {
		t.Errorf("expected user_id=%v, got %v", 99999, entry["user_id"])
	}
	if entry["target"] != "chat:99999" {
		t.Errorf("expected target=%q, got %q", "chat:99999", entry["target"])
	}
}

func TestAuditLog_New_NilLogger(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil logger")
		}
	}()
	New(nil)
}
