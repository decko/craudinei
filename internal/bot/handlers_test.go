package bot

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/decko/craudinei/internal/audit"
	"github.com/decko/craudinei/internal/types"
)

// FakeBot is a fake implementation of Bot for testing.
type FakeBot struct {
	sentMessages []string
	SentTo       map[int64][]string
}

func NewFakeBot() *FakeBot {
	return &FakeBot{
		sentMessages: []string{},
		SentTo:       make(map[int64][]string),
	}
}

func (f *FakeBot) Send(ctx context.Context, chatID int64, text, parseMode string) (*struct{}, error) {
	f.sentMessages = append(f.sentMessages, text)
	f.SentTo[chatID] = append(f.SentTo[chatID], text)
	return nil, nil
}

// FakeAuth is a fake implementation of Auth for testing.
type FakeAuth struct {
	whitelisted     map[int64]bool
	authenticated   map[int64]bool
	authCalled      []int64
	authPassphrases []string
	authenticateFn  func(userID int64, passphrase string) (bool, error)
}

func NewFakeAuth() *FakeAuth {
	return &FakeAuth{
		whitelisted:     make(map[int64]bool),
		authenticated:   make(map[int64]bool),
		authCalled:      []int64{},
		authPassphrases: []string{},
	}
}

func (f *FakeAuth) IsWhitelisted(userID int64) bool {
	return f.whitelisted[userID]
}

func (f *FakeAuth) IsAuthenticated(userID int64) bool {
	return f.authenticated[userID]
}

func (f *FakeAuth) Authenticate(userID int64, passphrase string) (bool, error) {
	f.authCalled = append(f.authCalled, userID)
	f.authPassphrases = append(f.authPassphrases, passphrase)
	if f.authenticateFn != nil {
		return f.authenticateFn(userID, passphrase)
	}
	return false, nil
}

func (f *FakeAuth) TouchSession(userID int64) {
	f.authenticated[userID] = true
}

// FakeSessionManager is a fake implementation of SessionManager for testing.
type FakeSessionManager struct {
	currentStatus  types.SessionStatus
	sessionID      string
	workDir        string
	allowedCmds    []string
	startCalled    bool
	stopCalled     bool
	resumeCalled   bool
	lastWorkDir    string
	lastSessionID  string
	transitionTo   []types.SessionStatus
	commandGuarded string
	guardError     error
	startError     error
	stopError      error
	resumeError    error
}

func NewFakeSessionManager() *FakeSessionManager {
	return &FakeSessionManager{
		currentStatus: types.StatusIdle,
		allowedCmds:   []string{"/begin", "/resume", "/status", "/help", "/sessions"},
	}
}

func (f *FakeSessionManager) Start(ctx context.Context, workDir string) error {
	f.startCalled = true
	f.lastWorkDir = workDir
	return f.startError
}

func (f *FakeSessionManager) Stop(ctx context.Context) error {
	f.stopCalled = true
	return f.stopError
}

func (f *FakeSessionManager) Resume(ctx context.Context, sessionID string) error {
	f.resumeCalled = true
	f.lastSessionID = sessionID
	return f.resumeError
}

func (f *FakeSessionManager) IsRunning() bool {
	return f.currentStatus == types.StatusRunning || f.currentStatus == types.StatusWaitingApproval
}

func (f *FakeSessionManager) Transition(target types.SessionStatus) error {
	f.transitionTo = append(f.transitionTo, target)
	f.currentStatus = target
	return nil
}

func (f *FakeSessionManager) CommandGuard(command string) error {
	f.commandGuarded = command
	return f.guardError
}

func (f *FakeSessionManager) Status() types.SessionStatus {
	return f.currentStatus
}

func (f *FakeSessionManager) SessionID() string {
	return f.sessionID
}

func (f *FakeSessionManager) WorkDir() string {
	return f.workDir
}

func (f *FakeSessionManager) AllowedCommands() []string {
	return f.allowedCmds
}

func (f *FakeSessionManager) SetStatus(status types.SessionStatus) {
	f.currentStatus = status
}

// FakePromptQueue is a fake implementation of PromptQueue for testing.
type FakePromptQueue struct {
	enqueued   []string
	enqueueErr error
}

func NewFakePromptQueue() *FakePromptQueue {
	return &FakePromptQueue{
		enqueued: []string{},
	}
}

func (f *FakePromptQueue) Enqueue(prompt string) error {
	if f.enqueueErr != nil {
		return f.enqueueErr
	}
	f.enqueued = append(f.enqueued, prompt)
	return nil
}

func makeUpdate(text string, chatID, userID int64) *Update {
	return &Update{
		Message: struct {
			Text string
			Chat struct {
				ID int64
			}
			From struct {
				ID int64
			}
		}{
			Text: text,
			Chat: struct {
				ID int64
			}{
				ID: chatID,
			},
			From: struct {
				ID int64
			}{
				ID: userID,
			},
		},
	}
}

func TestHandleAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		userID       int64
		setupAuth    func(*FakeAuth)
		update       *Update
		wantContains []string
	}{
		{
			name:   "whitelisted user authenticates successfully",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticateFn = func(userID int64, passphrase string) (bool, error) {
					return passphrase == "secret", nil
				}
			},
			update:       makeUpdate("/auth secret", 100, 123),
			wantContains: []string{"Authenticated"},
		},
		{
			name:   "wrong passphrase",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticateFn = func(userID int64, passphrase string) (bool, error) {
					return false, nil
				}
			},
			update:       makeUpdate("/auth wrong", 100, 123),
			wantContains: []string{"Invalid passphrase"},
		},
		{
			name:   "user not on whitelist",
			userID: 456,
			setupAuth: func(fa *FakeAuth) {
				// user 456 not whitelisted
			},
			update:       makeUpdate("/auth secret", 100, 456),
			wantContains: []string{"not on the allowed users list"},
		},
		{
			name:   "already authenticated",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			update:       makeUpdate("/auth secret", 100, 123),
			wantContains: []string{"already authenticated"},
		},
		{
			name:   "missing passphrase",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
			},
			update:       makeUpdate("/auth", 100, 123),
			wantContains: []string{"Usage: /auth"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()
			auth := NewFakeAuth()
			tc.setupAuth(auth)

			bot := NewFakeBot()
			cfg := &HandlerConfig{}
			queue := NewFakePromptQueue()
			logger := slog.Default()

			h := NewHandlers(bot, auth, cfg, queue, logger, nil)
			h.HandleAuth(context.Background(), nil, tc.update)

			if len(bot.SentTo[tc.update.Message.Chat.ID]) == 0 {
				t.Fatal("expected message to be sent")
			}
			msg := bot.SentTo[tc.update.Message.Chat.ID][0]
			for _, want := range tc.wantContains {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q does not contain %q", msg, want)
				}
			}
		})
	}
}

func TestHandleHelp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		userID       int64
		setupAuth    func(*FakeAuth)
		setupSM      func(*FakeSessionManager)
		update       *Update
		wantContains []string
	}{
		{
			name:   "whitelisted and authenticated user sees session commands",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.allowedCmds = []string{"/begin", "/stop", "/status", "/help"}
			},
			update:       makeUpdate("/help", 100, 123),
			wantContains: []string{"/begin", "/stop", "/status"},
		},
		{
			name:   "whitelisted but not authenticated sees auth prompt",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
			},
			setupSM:      func(fsm *FakeSessionManager) {},
			update:       makeUpdate("/help", 100, 123),
			wantContains: []string{"/auth"},
		},
		{
			name:         "not whitelisted user sees denied message",
			userID:       456,
			setupAuth:    func(fa *FakeAuth) {},
			setupSM:      func(fsm *FakeSessionManager) {},
			update:       makeUpdate("/help", 100, 456),
			wantContains: []string{"not on the allowed users list"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()
			auth := NewFakeAuth()
			tc.setupAuth(auth)

			sm := NewFakeSessionManager()
			tc.setupSM(sm)

			bot := NewFakeBot()
			cfg := &HandlerConfig{}
			queue := NewFakePromptQueue()
			logger := slog.Default()

			h := NewHandlers(bot, auth, cfg, queue, logger, nil)
			h.SetSessionManager(sm)
			h.HandleHelp(context.Background(), nil, tc.update)

			if len(bot.SentTo[tc.update.Message.Chat.ID]) == 0 {
				t.Fatal("expected message to be sent")
			}
			msg := bot.SentTo[tc.update.Message.Chat.ID][0]
			for _, want := range tc.wantContains {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q does not contain %q", msg, want)
				}
			}
		})
	}
}

func TestHandleStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		userID       int64
		setupAuth    func(*FakeAuth)
		setupSM      func(*FakeSessionManager)
		update       *Update
		wantContains []string
	}{
		{
			name:   "shows status for authenticated user",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusRunning
				fsm.sessionID = "sess-abc123"
				fsm.workDir = "/home/user/project"
			},
			update:       makeUpdate("/status", 100, 123),
			wantContains: []string{"Status: <b>running</b>", "Session ID:", "sess-abc123", "Work dir:"},
		},
		{
			name:         "denied for non-whitelisted user",
			userID:       456,
			setupAuth:    func(fa *FakeAuth) {},
			setupSM:      func(fsm *FakeSessionManager) {},
			update:       makeUpdate("/status", 100, 456),
			wantContains: []string{"not on the allowed users list"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()
			auth := NewFakeAuth()
			tc.setupAuth(auth)

			sm := NewFakeSessionManager()
			tc.setupSM(sm)

			bot := NewFakeBot()
			cfg := &HandlerConfig{}
			queue := NewFakePromptQueue()
			logger := slog.Default()

			h := NewHandlers(bot, auth, cfg, queue, logger, nil)
			h.SetSessionManager(sm)
			h.HandleStatus(context.Background(), nil, tc.update)

			if len(bot.SentTo[tc.update.Message.Chat.ID]) == 0 {
				t.Fatal("expected message to be sent")
			}
			msg := bot.SentTo[tc.update.Message.Chat.ID][0]
			for _, want := range tc.wantContains {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q does not contain %q", msg, want)
				}
			}
		})
	}
}

func TestHandleBegin(t *testing.T) {
	t.Parallel()

	// Use /tmp for tests since it exists
	tmpDir := "/tmp"

	tests := []struct {
		name         string
		userID       int64
		setupAuth    func(*FakeAuth)
		setupSM      func(*FakeSessionManager)
		cfg          *HandlerConfig
		update       *Update
		wantContains []string
		wantStart    bool
	}{
		{
			name:   "starts session with default workdir",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusIdle
			},
			cfg: &HandlerConfig{
				DefaultWorkDir: tmpDir,
				AllowedPaths:   []string{tmpDir},
			},
			update:       makeUpdate("/begin", 100, 123),
			wantContains: []string{"Session started"},
			wantStart:    true,
		},
		{
			name:   "starts session with specified workdir",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusIdle
			},
			cfg: &HandlerConfig{
				DefaultWorkDir: "/some/other",
				AllowedPaths:   []string{tmpDir},
			},
			update:       makeUpdate("/begin "+tmpDir, 100, 123),
			wantContains: []string{"Session started"},
			wantStart:    true,
		},
		{
			name:   "denied for non-authenticated user",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusIdle
			},
			cfg: &HandlerConfig{
				DefaultWorkDir: tmpDir,
				AllowedPaths:   []string{tmpDir},
			},
			update:       makeUpdate("/begin", 100, 123),
			wantContains: []string{"Please authenticate"},
			wantStart:    false,
		},
		{
			name:      "denied for non-whitelisted user",
			userID:    456,
			setupAuth: func(fa *FakeAuth) {},
			setupSM:   func(fsm *FakeSessionManager) {},
			cfg: &HandlerConfig{
				DefaultWorkDir: tmpDir,
				AllowedPaths:   []string{tmpDir},
			},
			update:       makeUpdate("/begin", 100, 456),
			wantContains: []string{"not on the allowed users list"},
			wantStart:    false,
		},
		{
			name:   "blocked in running state",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusRunning
				fsm.guardError = errors.New("command /begin not allowed in state running")
			},
			cfg: &HandlerConfig{
				DefaultWorkDir: tmpDir,
				AllowedPaths:   []string{tmpDir},
			},
			update:       makeUpdate("/begin", 100, 123),
			wantContains: []string{"Cannot start session"},
			wantStart:    false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			auth := NewFakeAuth()
			tc.setupAuth(auth)

			sm := NewFakeSessionManager()
			tc.setupSM(sm)

			bot := NewFakeBot()
			queue := NewFakePromptQueue()
			logger := slog.Default()

			h := NewHandlers(bot, auth, tc.cfg, queue, logger, nil)
			h.SetSessionManager(sm)
			h.HandleBegin(context.Background(), nil, tc.update)

			if tc.wantStart && !sm.startCalled {
				t.Error("expected Start to be called")
			}
			if !tc.wantStart && sm.startCalled {
				t.Error("expected Start NOT to be called")
			}

			if len(bot.SentTo[tc.update.Message.Chat.ID]) == 0 {
				t.Fatal("expected message to be sent")
			}
			msg := bot.SentTo[tc.update.Message.Chat.ID][0]
			for _, want := range tc.wantContains {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q does not contain %q", msg, want)
				}
			}
		})
	}
}

func TestHandleStop(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		userID       int64
		setupAuth    func(*FakeAuth)
		setupSM      func(*FakeSessionManager)
		update       *Update
		wantContains []string
		wantStop     bool
	}{
		{
			name:   "stops running session",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusRunning
			},
			update:       makeUpdate("/stop", 100, 123),
			wantContains: []string{"Session stopped"},
			wantStop:     true,
		},
		{
			name:   "denied for non-authenticated user",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusRunning
			},
			update:       makeUpdate("/stop", 100, 123),
			wantContains: []string{"Please authenticate"},
			wantStop:     false,
		},
		{
			name:   "blocked in idle state",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusIdle
				fsm.guardError = errors.New("command /stop not allowed in state idle")
			},
			update:       makeUpdate("/stop", 100, 123),
			wantContains: []string{"Cannot stop session"},
			wantStop:     false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()
			auth := NewFakeAuth()
			tc.setupAuth(auth)

			sm := NewFakeSessionManager()
			tc.setupSM(sm)

			bot := NewFakeBot()
			cfg := &HandlerConfig{}
			queue := NewFakePromptQueue()
			logger := slog.Default()

			h := NewHandlers(bot, auth, cfg, queue, logger, nil)
			h.SetSessionManager(sm)
			h.HandleStop(context.Background(), nil, tc.update)

			if tc.wantStop && !sm.stopCalled {
				t.Error("expected Stop to be called")
			}
			if !tc.wantStop && sm.stopCalled {
				t.Error("expected Stop NOT to be called")
			}

			if len(bot.SentTo[tc.update.Message.Chat.ID]) == 0 {
				t.Fatal("expected message to be sent")
			}
			msg := bot.SentTo[tc.update.Message.Chat.ID][0]
			for _, want := range tc.wantContains {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q does not contain %q", msg, want)
				}
			}
		})
	}
}

func TestHandleCancel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		userID       int64
		setupAuth    func(*FakeAuth)
		setupSM      func(*FakeSessionManager)
		update       *Update
		wantContains []string
		wantStop     bool
	}{
		{
			name:   "cancels running session",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusRunning
			},
			update:       makeUpdate("/cancel", 100, 123),
			wantContains: []string{"Operation cancelled"},
			wantStop:     true,
		},
		{
			name:   "blocked in idle state",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusIdle
				fsm.guardError = errors.New("command /cancel not allowed in state idle")
			},
			update:       makeUpdate("/cancel", 100, 123),
			wantContains: []string{"Cannot cancel operation"},
			wantStop:     false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()
			auth := NewFakeAuth()
			tc.setupAuth(auth)

			sm := NewFakeSessionManager()
			tc.setupSM(sm)

			bot := NewFakeBot()
			cfg := &HandlerConfig{}
			queue := NewFakePromptQueue()
			logger := slog.Default()

			h := NewHandlers(bot, auth, cfg, queue, logger, nil)
			h.SetSessionManager(sm)
			h.HandleCancel(context.Background(), nil, tc.update)

			if tc.wantStop && !sm.stopCalled {
				t.Error("expected Stop to be called")
			}
			if !tc.wantStop && sm.stopCalled {
				t.Error("expected Stop NOT to be called")
			}

			if len(bot.SentTo[tc.update.Message.Chat.ID]) == 0 {
				t.Fatal("expected message to be sent")
			}
			msg := bot.SentTo[tc.update.Message.Chat.ID][0]
			for _, want := range tc.wantContains {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q does not contain %q", msg, want)
				}
			}
		})
	}
}

func TestHandleResume(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		userID        int64
		setupAuth     func(*FakeAuth)
		setupSM       func(*FakeSessionManager)
		update        *Update
		wantContains  []string
		wantResume    bool
		wantSessionID string
	}{
		{
			name:   "resumes with last session",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusCrashed
			},
			update:        makeUpdate("/resume", 100, 123),
			wantContains:  []string{"Session resumed"},
			wantResume:    true,
			wantSessionID: "",
		},
		{
			name:   "resumes specific session",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusCrashed
			},
			update:        makeUpdate("/resume sess-xyz789", 100, 123),
			wantContains:  []string{"Session resumed"},
			wantResume:    true,
			wantSessionID: "sess-xyz789",
		},
		{
			name:   "blocked in running state",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusRunning
				fsm.guardError = errors.New("command /resume not allowed in state running")
			},
			update:        makeUpdate("/resume", 100, 123),
			wantContains:  []string{"Cannot resume session"},
			wantResume:    false,
			wantSessionID: "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()
			auth := NewFakeAuth()
			tc.setupAuth(auth)

			sm := NewFakeSessionManager()
			tc.setupSM(sm)

			bot := NewFakeBot()
			cfg := &HandlerConfig{}
			queue := NewFakePromptQueue()
			logger := slog.Default()

			h := NewHandlers(bot, auth, cfg, queue, logger, nil)
			h.SetSessionManager(sm)
			h.HandleResume(context.Background(), nil, tc.update)

			if tc.wantResume && !sm.resumeCalled {
				t.Error("expected Resume to be called")
			}
			if !tc.wantResume && sm.resumeCalled {
				t.Error("expected Resume NOT to be called")
			}
			if tc.wantSessionID != "" && sm.lastSessionID != tc.wantSessionID {
				t.Errorf("expected session ID %q, got %q", tc.wantSessionID, sm.lastSessionID)
			}

			if len(bot.SentTo[tc.update.Message.Chat.ID]) == 0 {
				t.Fatal("expected message to be sent")
			}
			msg := bot.SentTo[tc.update.Message.Chat.ID][0]
			for _, want := range tc.wantContains {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q does not contain %q", msg, want)
				}
			}
		})
	}
}

func TestHandleSessions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		userID       int64
		setupAuth    func(*FakeAuth)
		setupSM      func(*FakeSessionManager)
		update       *Update
		wantContains []string
	}{
		{
			name:   "shows current session",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusRunning
				fsm.sessionID = "sess-abc"
				fsm.workDir = "/home/user/project"
			},
			update:       makeUpdate("/sessions", 100, 123),
			wantContains: []string{"Sessions:", "sess-abc", "running"},
		},
		{
			name:   "shows no active session",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusIdle
			},
			update:       makeUpdate("/sessions", 100, 123),
			wantContains: []string{"No active session"},
		},
		{
			name:   "denied for non-authenticated user",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
			},
			setupSM:      func(fsm *FakeSessionManager) {},
			update:       makeUpdate("/sessions", 100, 123),
			wantContains: []string{"Please authenticate"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()
			auth := NewFakeAuth()
			tc.setupAuth(auth)

			sm := NewFakeSessionManager()
			tc.setupSM(sm)

			bot := NewFakeBot()
			cfg := &HandlerConfig{}
			queue := NewFakePromptQueue()
			logger := slog.Default()

			h := NewHandlers(bot, auth, cfg, queue, logger, nil)
			h.SetSessionManager(sm)
			h.HandleSessions(context.Background(), nil, tc.update)

			if len(bot.SentTo[tc.update.Message.Chat.ID]) == 0 {
				t.Fatal("expected message to be sent")
			}
			msg := bot.SentTo[tc.update.Message.Chat.ID][0]
			for _, want := range tc.wantContains {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q does not contain %q", msg, want)
				}
			}
		})
	}
}

func TestHandleReload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		userID       int64
		setupAuth    func(*FakeAuth)
		update       *Update
		wantContains []string
	}{
		{
			name:   "reloads config for authenticated user",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			update:       makeUpdate("/reload", 100, 123),
			wantContains: []string{"Configuration reloaded"},
		},
		{
			name:   "denied for non-authenticated user",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
			},
			update:       makeUpdate("/reload", 100, 123),
			wantContains: []string{"Please authenticate"},
		},
		{
			name:         "denied for non-whitelisted user",
			userID:       456,
			setupAuth:    func(fa *FakeAuth) {},
			update:       makeUpdate("/reload", 100, 456),
			wantContains: []string{"not on the allowed users list"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()
			auth := NewFakeAuth()
			tc.setupAuth(auth)

			bot := NewFakeBot()
			cfg := &HandlerConfig{}
			queue := NewFakePromptQueue()
			logger := slog.Default()

			h := NewHandlers(bot, auth, cfg, queue, logger, nil)
			h.HandleReload(context.Background(), nil, tc.update)

			if len(bot.SentTo[tc.update.Message.Chat.ID]) == 0 {
				t.Fatal("expected message to be sent")
			}
			msg := bot.SentTo[tc.update.Message.Chat.ID][0]
			for _, want := range tc.wantContains {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q does not contain %q", msg, want)
				}
			}
		})
	}
}

func TestHandleTextMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		userID        int64
		setupAuth     func(*FakeAuth)
		setupSM       func(*FakeSessionManager)
		setupQueue    func(*FakePromptQueue)
		text          string
		wantContains  []string
		wantEnqueued  bool
		wantSanitized bool
	}{
		{
			name:   "enqueues sanitized prompt in running state",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusRunning
			},
			setupQueue:    func(fq *FakePromptQueue) {},
			text:          "Hello Claude!",
			wantContains:  nil,
			wantEnqueued:  true,
			wantSanitized: true,
		},
		{
			name:   "blocked in idle state",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusIdle
				fsm.guardError = errors.New("command prompt not allowed in state idle")
			},
			setupQueue:    func(fq *FakePromptQueue) {},
			text:          "Hello Claude!",
			wantContains:  []string{"Cannot send prompt"},
			wantEnqueued:  false,
			wantSanitized: false,
		},
		{
			name:   "queue full returns error",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
				fa.authenticated[123] = true
			},
			setupSM: func(fsm *FakeSessionManager) {
				fsm.currentStatus = types.StatusRunning
			},
			setupQueue: func(fq *FakePromptQueue) {
				fq.enqueueErr = errors.New("queue full")
			},
			text:          "Hello Claude!",
			wantContains:  []string{"Queue full"},
			wantEnqueued:  false,
			wantSanitized: false,
		},
		{
			name:   "denied for non-authenticated user",
			userID: 123,
			setupAuth: func(fa *FakeAuth) {
				fa.whitelisted[123] = true
			},
			setupSM:       func(fsm *FakeSessionManager) {},
			setupQueue:    func(fq *FakePromptQueue) {},
			text:          "Hello Claude!",
			wantContains:  []string{"Please authenticate"},
			wantEnqueued:  false,
			wantSanitized: false,
		},
		{
			name:          "denied for non-whitelisted user",
			userID:        456,
			setupAuth:     func(fa *FakeAuth) {},
			setupSM:       func(fsm *FakeSessionManager) {},
			setupQueue:    func(fq *FakePromptQueue) {},
			text:          "Hello Claude!",
			wantContains:  []string{"not on the allowed users list"},
			wantEnqueued:  false,
			wantSanitized: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()
			auth := NewFakeAuth()
			tc.setupAuth(auth)

			sm := NewFakeSessionManager()
			tc.setupSM(sm)

			queue := NewFakePromptQueue()
			tc.setupQueue(queue)

			bot := NewFakeBot()
			cfg := &HandlerConfig{}
			logger := slog.Default()

			h := NewHandlers(bot, auth, cfg, queue, logger, nil)
			h.SetSessionManager(sm)
			h.HandleTextMessage(context.Background(), nil, makeUpdate(tc.text, 100, tc.userID))

			if tc.wantEnqueued && len(queue.enqueued) == 0 {
				t.Error("expected prompt to be enqueued")
			}
			if !tc.wantEnqueued && len(queue.enqueued) > 0 {
				t.Error("expected prompt NOT to be enqueued")
			}

			// Check that the enqueued prompt was sanitized and JSON marshaled
			if tc.wantSanitized && len(queue.enqueued) > 0 {
				enqueued := queue.enqueued[0]
				// JSON marshaled string should be quoted
				if !strings.HasPrefix(enqueued, "\"") || !strings.HasSuffix(enqueued, "\"") {
					t.Errorf("expected JSON marshaled string, got %q", enqueued)
				}
			}

			if len(bot.SentTo[100]) == 0 && tc.wantContains != nil {
				t.Fatal("expected message to be sent")
			}
			if tc.wantContains != nil && len(bot.SentTo[100]) > 0 {
				msg := bot.SentTo[100][0]
				for _, want := range tc.wantContains {
					if !strings.Contains(msg, want) {
						t.Errorf("message %q does not contain %q", msg, want)
					}
				}
			}
		})
	}
}

func TestAuditLogger_CommandAndAuth(t *testing.T) {
	t.Helper()

	// Create temp file for audit log
	tmpFile, err := os.CreateTemp("", "audit-test-*.log")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// Create audit logger
	auditLogger, err := audit.NewWithFile(tmpPath)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}
	defer auditLogger.Close()

	// Create handlers with audit logger
	auth := NewFakeAuth()
	auth.whitelisted[123] = true
	auth.authenticated[123] = true
	auth.authenticateFn = func(userID int64, passphrase string) (bool, error) {
		return passphrase == "secret", nil
	}

	sm := NewFakeSessionManager()
	sm.currentStatus = types.StatusIdle

	bot := NewFakeBot()
	cfg := &HandlerConfig{
		DefaultWorkDir: "/tmp",
		AllowedPaths:   []string{"/tmp"},
	}
	queue := NewFakePromptQueue()
	logger := slog.Default()

	h := NewHandlers(bot, auth, cfg, queue, logger, auditLogger)
	h.SetSessionManager(sm)

	// Test 1: HandleBegin produces Command audit entry
	h.HandleBegin(context.Background(), nil, makeUpdate("/begin", 100, 123))

	// Read audit file content
	content, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("Failed to read audit file: %v", err)
	}

	// Verify Command entry for begin
	if !strings.Contains(string(content), `"command"`) {
		t.Error("audit file does not contain command entry")
	}
	if !strings.Contains(string(content), `"begin"`) {
		t.Error("audit file does not contain begin command")
	}

	// Test 2: HandleAuth with wrong passphrase produces AuthAttempt entry
	// Use a different user (456) who is whitelisted but NOT authenticated
	auth.whitelisted[456] = true
	h.HandleAuth(context.Background(), nil, makeUpdate("/auth wrongpass", 100, 456))

	// Read full audit file and verify all entries are present
	content, err = os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("Failed to read audit file: %v", err)
	}

	// Verify both Command and AuthAttempt entries
	if !strings.Contains(string(content), `"command"`) {
		t.Error("audit file does not contain command entry")
	}
	if !strings.Contains(string(content), `"begin"`) {
		t.Error("audit file does not contain begin command")
	}
	if !strings.Contains(string(content), `"auth_attempt"`) {
		t.Error("audit file does not contain auth_attempt entry")
	}
	if !strings.Contains(string(content), `"failure"`) {
		t.Error("audit file does not contain failure outcome")
	}
}
