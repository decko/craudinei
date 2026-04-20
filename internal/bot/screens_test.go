package bot

import (
	"encoding/json"
	"testing"

	"github.com/decko/craudinei/internal/types"
)

func TestWelcomeScreen(t *testing.T) {
	t.Helper()

	text, keyboard := WelcomeScreen()

	if text == "" {
		t.Error("WelcomeScreen returned empty string")
	}

	// Verify it contains auth prompt
	if !contains(text, "/auth") {
		t.Error("WelcomeScreen missing auth prompt")
	}

	// Verify it contains welcome message
	if !contains(text, "Welcome") {
		t.Error("WelcomeScreen missing welcome message")
	}

	// Verify it returns a keyboard with at least 2 buttons
	if len(keyboard) == 0 {
		t.Error("WelcomeScreen missing keyboard")
	}
	if len(keyboard) > 0 && len(keyboard[0]) < 2 {
		t.Error("WelcomeScreen keyboard should have at least 2 buttons")
	}
}

func TestHomeScreen(t *testing.T) {
	t.Helper()

	tests := []struct {
		name    string
		status  types.SessionStatus
		workDir string
		check   func(t *testing.T, text string)
	}{
		{
			name:    "idle with no workdir",
			status:  types.StatusIdle,
			workDir: "",
			check: func(t *testing.T, text string) {
				if !contains(text, string(types.StatusIdle)) {
					t.Error("HomeScreen missing status")
				}
			},
		},
		{
			name:    "running with workdir",
			status:  types.StatusRunning,
			workDir: "/tmp/project",
			check: func(t *testing.T, text string) {
				if !contains(text, string(types.StatusRunning)) {
					t.Error("HomeScreen missing running status")
				}
				if !contains(text, "/tmp/project") {
					t.Error("HomeScreen missing workDir")
				}
			},
		},
		{
			name:    "waiting approval status",
			status:  types.StatusWaitingApproval,
			workDir: "",
			check: func(t *testing.T, text string) {
				if !contains(text, string(types.StatusWaitingApproval)) {
					t.Error("HomeScreen missing waiting_approval status")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, keyboard := HomeScreen(tt.status, tt.workDir)
			if text == "" {
				t.Error("HomeScreen returned empty string")
			}
			// Verify commands are listed
			if !contains(text, "/begin") {
				t.Error("HomeScreen missing /begin command")
			}
			if !contains(text, "/stop") {
				t.Error("HomeScreen missing /stop command")
			}
			if !contains(text, "/status") {
				t.Error("HomeScreen missing /status command")
			}
			tt.check(t, text)

			// Verify it returns a keyboard with at least 2 buttons
			if len(keyboard) == 0 {
				t.Error("HomeScreen missing keyboard")
			}
			if len(keyboard) > 0 {
				buttonCount := 0
				for _, row := range keyboard {
					buttonCount += len(row)
				}
				if buttonCount < 2 {
					t.Error("HomeScreen keyboard should have at least 2 buttons")
				}
			}
		})
	}
}

func TestDirectoryPickerScreen(t *testing.T) {
	t.Helper()

	tests := []struct {
		name     string
		dirs     []string
		wantRows int
		wantCols int
	}{
		{
			name:     "single directory",
			dirs:     []string{"/home/user"},
			wantRows: 1,
			wantCols: 1,
		},
		{
			name:     "three directories",
			dirs:     []string{"/home/user", "/tmp", "/var/data"},
			wantRows: 1,
			wantCols: 3,
		},
		{
			name:     "four directories",
			dirs:     []string{"/home/user", "/tmp", "/var/data", "/opt"},
			wantRows: 2,
			wantCols: 3, // last row has 1 button
		},
		{
			name:     "empty directories",
			dirs:     []string{},
			wantRows: 0,
			wantCols: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, keyboard, _ := DirectoryPickerScreen(tt.dirs)

			if text == "" {
				t.Error("DirectoryPickerScreen returned empty text")
			}
			if !contains(text, "Directory") {
				t.Error("DirectoryPickerScreen missing directory prompt")
			}

			if tt.wantRows == 0 && len(keyboard) != 0 {
				t.Errorf("expected 0 rows, got %d", len(keyboard))
			}

			if tt.wantRows > 0 && len(keyboard) != tt.wantRows {
				t.Errorf("expected %d rows, got %d", tt.wantRows, len(keyboard))
			}

			if tt.wantCols > 0 && len(keyboard) > 0 {
				// Check first row length
				if len(keyboard[0]) != tt.wantCols && len(tt.dirs) >= 3 {
					// If more than 3 dirs, first row should have 3
					if len(keyboard[0]) != 3 {
						t.Errorf("expected first row to have 3 buttons, got %d", len(keyboard[0]))
					}
				}
			}
		})
	}
}

func TestSessionsListScreen(t *testing.T) {
	t.Helper()

	tests := []struct {
		name     string
		sessions []SessionInfo
		wantText bool
		wantRows int
	}{
		{
			name:     "empty sessions",
			sessions: []SessionInfo{},
			wantText: true,
			wantRows: 1,
		},
		{
			name: "single session",
			sessions: []SessionInfo{
				{SessionID: "abc123", Status: types.StatusRunning, WorkDir: "/tmp"},
			},
			wantText: true,
			wantRows: 1,
		},
		{
			name: "multiple sessions",
			sessions: []SessionInfo{
				{SessionID: "abc123", Status: types.StatusRunning, WorkDir: "/tmp"},
				{SessionID: "def456", Status: types.StatusIdle, WorkDir: "/home"},
			},
			wantText: true,
			wantRows: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, keyboard := SessionsListScreen(tt.sessions)

			if tt.wantText && text == "" {
				t.Error("SessionsListScreen returned empty text")
			}
			if !contains(text, "Sessions") {
				t.Error("SessionsListScreen missing Sessions header")
			}
			if len(keyboard) != tt.wantRows {
				t.Errorf("expected %d keyboard rows, got %d", tt.wantRows, len(keyboard))
			}

			// Verify session info is present
			for _, s := range tt.sessions {
				if !contains(text, s.SessionID) {
					t.Errorf("SessionsListScreen missing session ID %s", s.SessionID)
				}
			}
		})
	}
}

func TestSessionStatusBar(t *testing.T) {
	t.Helper()

	tests := []struct {
		name      string
		status    types.SessionStatus
		sessionID string
		workDir   string
		cost      float64
		turns     int
		want      string
	}{
		{
			name:      "basic status bar",
			status:    types.StatusRunning,
			sessionID: "abc123",
			workDir:   "",
			cost:      0,
			turns:     0,
			want:      "abc123",
		},
		{
			name:      "full status bar",
			status:    types.StatusRunning,
			sessionID: "abc123",
			workDir:   "/tmp/project",
			cost:      0.05,
			turns:     10,
			want:      "abc123",
		},
		{
			name:      "with cost",
			status:    types.StatusRunning,
			sessionID: "xyz789",
			workDir:   "",
			cost:      1.23,
			turns:     0,
			want:      "$1.2300",
		},
		{
			name:      "with turns",
			status:    types.StatusRunning,
			sessionID: "xyz789",
			workDir:   "",
			cost:      0,
			turns:     42,
			want:      "Turns:</b> 42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := SessionStatusBar(tt.status, tt.sessionID, tt.workDir, tt.cost, tt.turns)

			if text == "" {
				t.Error("SessionStatusBar returned empty string")
			}
			if !contains(text, tt.want) {
				t.Errorf("SessionStatusBar missing expected content %q", tt.want)
			}
			if !contains(text, string(tt.status)) {
				t.Error("SessionStatusBar missing status")
			}
		})
	}
}

func TestApprovalCard(t *testing.T) {
	t.Helper()

	tests := []struct {
		name        string
		toolName    string
		input       json.RawMessage
		requestID   string
		wantText    bool
		wantButtons int
	}{
		{
			name:        "basic approval card",
			toolName:    "Bash",
			input:       json.RawMessage(`{"command": "ls -la"}`),
			requestID:   "req123",
			wantText:    true,
			wantButtons: 3,
		},
		{
			name:        "empty input",
			toolName:    "Read",
			input:       nil,
			requestID:   "req456",
			wantText:    true,
			wantButtons: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, keyboard := ApprovalCard(tt.toolName, tt.input, tt.requestID)

			if tt.wantText && text == "" {
				t.Error("ApprovalCard returned empty text")
			}
			if !contains(text, "Approval") {
				t.Error("ApprovalCard missing Approval header")
			}
			if !contains(text, tt.toolName) {
				t.Errorf("ApprovalCard missing tool name %s", tt.toolName)
			}

			if len(keyboard) != 1 {
				t.Errorf("expected 1 keyboard row, got %d", len(keyboard))
			}
			if len(keyboard) > 0 && len(keyboard[0]) != tt.wantButtons {
				t.Errorf("expected %d buttons, got %d", tt.wantButtons, len(keyboard[0]))
			}

			// Verify callback data prefixes
			if len(keyboard) > 0 {
				expectedPrefixes := []string{"a:" + tt.requestID, "d:" + tt.requestID, "s:" + tt.requestID}
				for i, prefix := range expectedPrefixes {
					if keyboard[0][i].CallbackData != prefix {
						t.Errorf("button %d has callback data %q, want %q", i, keyboard[0][i].CallbackData, prefix)
					}
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
