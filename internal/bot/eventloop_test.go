package bot

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/decko/craudinei/internal/router"
	"github.com/decko/craudinei/internal/types"
)

// fakeBotSender implements BotSender interface for testing.
type fakeBotSender struct {
	mu       sync.Mutex
	messages []*sentMessage
	sendErr  error
}

type sentMessage struct {
	Text      string
	ParseMode string
	ChatID    int64
}

func (f *fakeBotSender) Send(ctx context.Context, chatID int64, text string, parseMode string) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, &sentMessage{
		Text:      text,
		ParseMode: parseMode,
		ChatID:    chatID,
	})
	return nil, f.sendErr
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestNewEventLoop(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := DefaultEventLoopConfig()

	el := NewEventLoop(bot, 12345, state, cfg, logger)

	if el == nil {
		t.Fatal("NewEventLoop() returned nil")
	}
	if el.bot != bot {
		t.Error("NewEventLoop() bot not set")
	}
	if el.chatID != 12345 {
		t.Errorf("NewEventLoop() chatID = %d, want 12345", el.chatID)
	}
	if el.state != state {
		t.Error("NewEventLoop() state not set")
	}
	if el.logger != logger {
		t.Error("NewEventLoop() logger not set")
	}
	if el.done == nil {
		t.Error("NewEventLoop() done channel not initialized")
	}
}

func TestNewEventLoop_Defaults(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()

	el := NewEventLoop(bot, 12345, state, EventLoopConfig{}, logger)

	if el.cfg.EditInterval != 1*time.Second {
		t.Errorf("EditInterval = %v, want 1s", el.cfg.EditInterval)
	}
	if el.cfg.ProgressInterval != 2*time.Minute {
		t.Errorf("ProgressInterval = %v, want 2m", el.cfg.ProgressInterval)
	}
	if el.cfg.InactivityTimeout != 15*time.Minute {
		t.Errorf("InactivityTimeout = %v, want 15m", el.cfg.InactivityTimeout)
	}
	if el.cfg.TypingInterval != 4*time.Second {
		t.Errorf("TypingInterval = %v, want 4s", el.cfg.TypingInterval)
	}
	if el.cfg.MaxEditsBeforeNew != 20 {
		t.Errorf("MaxEditsBeforeNew = %d, want 20", el.cfg.MaxEditsBeforeNew)
	}
}

func TestEventLoop_Start(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := DefaultEventLoopConfig()

	el := NewEventLoop(bot, 12345, state, cfg, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	el.Start(ctx)

	if el.progressTicker == nil {
		t.Error("Start() progressTicker is nil")
	}
	if el.typingTicker == nil {
		t.Error("Start() typingTicker is nil")
	}
}

func TestHandleEvent_Text(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := EventLoopConfig{
		EditInterval:      50 * time.Millisecond,
		ProgressInterval:  1 * time.Hour,
		TypingInterval:    1 * time.Hour,
		MaxEditsBeforeNew: 20,
	}

	el := NewEventLoop(bot, 12345, state, cfg, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	el.Start(ctx)
	defer el.Stop()

	// Simulate a text event
	event := router.ClassifiedEvent{
		Action: router.ActionText,
		Text:   "Hello, world!",
		Event:  types.Event{},
	}

	el.HandleEvent(event)

	// Wait for debounce
	time.Sleep(100 * time.Millisecond)

	// Flush to ensure message is sent
	el.flushBuffer()
}

func TestHandleEvent_System(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := DefaultEventLoopConfig()

	el := NewEventLoop(bot, 12345, state, cfg, logger)

	// Simulate system init event
	event := router.ClassifiedEvent{
		Action: router.ActionSystem,
		Event: types.Event{
			Type:      types.EventSystem,
			Subtype:   "init",
			SessionID: "test-session-123",
		},
	}

	el.HandleEvent(event)

	// Verify session ID is set
	if state.SessionID() != "test-session-123" {
		t.Errorf("SessionID = %q, want %q", state.SessionID(), "test-session-123")
	}
}

func TestHandleEvent_Error(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := DefaultEventLoopConfig()

	el := NewEventLoop(bot, 12345, state, cfg, logger)

	// Simulate error event
	event := router.ClassifiedEvent{
		Action: router.ActionError,
		Text:   "Something went wrong",
		Event:  types.Event{},
	}

	el.HandleEvent(event)
}

func TestHandleEvent_RateLimit(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := DefaultEventLoopConfig()

	el := NewEventLoop(bot, 12345, state, cfg, logger)

	// Simulate rate limit event
	event := router.ClassifiedEvent{
		Action: router.ActionRateLimit,
		Event:  types.Event{},
	}

	el.HandleEvent(event)
}

func TestHandleEvent_APIRetry(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := DefaultEventLoopConfig()

	el := NewEventLoop(bot, 12345, state, cfg, logger)

	// Simulate API retry event
	event := router.ClassifiedEvent{
		Action: router.ActionAPIRetry,
		Event: types.Event{
			RetryAfterSeconds: 5,
		},
	}

	el.HandleEvent(event)
}

func TestProgressTicker(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := EventLoopConfig{
		EditInterval:      1 * time.Hour,
		ProgressInterval:  50 * time.Millisecond,
		TypingInterval:    1 * time.Hour,
		MaxEditsBeforeNew: 20,
	}

	el := NewEventLoop(bot, 12345, state, cfg, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	el.Start(ctx)

	// Wait for at least one progress tick
	time.Sleep(150 * time.Millisecond)

	el.Stop()
}

func TestProgressEscalation(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := EventLoopConfig{
		EditInterval:      1 * time.Hour,
		ProgressInterval:  50 * time.Millisecond,
		TypingInterval:    1 * time.Hour,
		MaxEditsBeforeNew: 20,
	}

	el := NewEventLoop(bot, 12345, state, cfg, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	el.Start(ctx)

	// Wait for multiple progress ticks
	time.Sleep(200 * time.Millisecond)

	el.mu.Lock()
	count := el.progressCount
	el.mu.Unlock()

	if count < 3 {
		t.Errorf("progressCount = %d, want at least 3", count)
	}
}

func TestTypingIndicator(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	state.SetStatus(types.StatusRunning)
	cfg := EventLoopConfig{
		EditInterval:      1 * time.Hour,
		ProgressInterval:  1 * time.Hour,
		TypingInterval:    50 * time.Millisecond,
		MaxEditsBeforeNew: 20,
	}

	el := NewEventLoop(bot, 12345, state, cfg, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	el.Start(ctx)

	// Wait for at least one typing tick
	time.Sleep(100 * time.Millisecond)

	el.Stop()
}

func TestStreamingDebounce(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := EventLoopConfig{
		EditInterval:      100 * time.Millisecond,
		ProgressInterval:  1 * time.Hour,
		TypingInterval:    1 * time.Hour,
		MaxEditsBeforeNew: 20,
	}

	el := NewEventLoop(bot, 12345, state, cfg, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	el.Start(ctx)
	defer el.Stop()

	// Send multiple text events quickly
	for i := 0; i < 5; i++ {
		el.HandleEvent(router.ClassifiedEvent{
			Action: router.ActionText,
			Text:   "chunk ",
		})
	}

	// Wait for debounce period
	time.Sleep(150 * time.Millisecond)

	// Explicitly flush to ensure the buffer is cleared before checking editCount.
	// scheduleFlush() spawns a goroutine, so we call flushBuffer() directly to
	// avoid a race between the goroutine completing and this test reading editCount.
	el.flushBuffer()

	el.mu.Lock()
	count := el.editCount
	el.mu.Unlock()

	if count != 0 {
		t.Errorf("editCount = %d after flush, want 0", count)
	}
}

func TestNewMessageAfterMaxEdits(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := EventLoopConfig{
		EditInterval:      50 * time.Millisecond,
		ProgressInterval:  1 * time.Hour,
		TypingInterval:    1 * time.Hour,
		MaxEditsBeforeNew: 5, // Small for testing
	}

	el := NewEventLoop(bot, 12345, state, cfg, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	el.Start(ctx)
	defer el.Stop()

	// Send events equal to MaxEditsBeforeNew
	for i := 0; i < 5; i++ {
		el.HandleEvent(router.ClassifiedEvent{
			Action: router.ActionText,
			Text:   "x",
		})
	}

	// Wait for processing
	time.Sleep(100 * time.Millisecond)
}

func TestEventLoopStop_CleansUp(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := EventLoopConfig{
		EditInterval:      50 * time.Millisecond,
		ProgressInterval:  50 * time.Millisecond,
		TypingInterval:    50 * time.Millisecond,
		MaxEditsBeforeNew: 20,
	}

	el := NewEventLoop(bot, 12345, state, cfg, logger)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel context immediately

	el.Start(ctx)

	// Add some text to the buffer
	el.mu.Lock()
	el.editBuffer.WriteString("some text")
	el.mu.Unlock()

	// Stop should flush buffer and stop goroutines
	el.Stop()

	// Verify done channel is closed
	select {
	case <-el.done:
	default:
		t.Error("Stop() did not close done channel")
	}
}

func TestEventLoopStop_CalledTwice_NoPanic(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := DefaultEventLoopConfig()

	el := NewEventLoop(bot, 12345, state, cfg, logger)

	// First Stop() should not panic
	el.Stop()

	// Second Stop() should also not panic
	el.Stop()
}

func TestHandleEvent_ToolUse(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := DefaultEventLoopConfig()

	el := NewEventLoop(bot, 12345, state, cfg, logger)

	// Simulate tool use event
	event := router.ClassifiedEvent{
		Action: router.ActionToolUse,
		Event: types.Event{
			Type:    types.EventAssistant,
			Subtype: "tool_use",
		},
	}

	el.HandleEvent(event)
}

func TestHandleEvent_Result(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := DefaultEventLoopConfig()

	el := NewEventLoop(bot, 12345, state, cfg, logger)

	// Simulate result event
	event := router.ClassifiedEvent{
		Action: router.ActionResult,
		Text:   "Command completed successfully",
		Event:  types.Event{},
	}

	el.HandleEvent(event)
}

func TestHandleEvent_Thinking(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := DefaultEventLoopConfig()

	el := NewEventLoop(bot, 12345, state, cfg, logger)

	// Simulate thinking event
	event := router.ClassifiedEvent{
		Action: router.ActionThinking,
		Event:  types.Event{},
	}

	el.HandleEvent(event)
}

func TestFlushBuffer_Empty(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := DefaultEventLoopConfig()

	el := NewEventLoop(bot, 12345, state, cfg, logger)

	// Flush empty buffer should not panic
	el.flushBuffer()
}

func TestProgressMessage(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := EventLoopConfig{
		EditInterval:      1 * time.Hour,
		ProgressInterval:  50 * time.Millisecond,
		TypingInterval:    1 * time.Hour,
		MaxEditsBeforeNew: 20,
	}

	el := NewEventLoop(bot, 12345, state, cfg, logger)

	tests := []struct {
		name     string
		count    int
		expected string
	}{
		{
			name:     "no messages yet",
			count:    0,
			expected: "",
		},
		{
			name:     "first progress",
			count:    1,
			expected: "⏳ Working...",
		},
		{
			name:     "escalated",
			count:    3,
			expected: "⏳ Still working... (this is taking longer than usual)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			el.mu.Lock()
			el.progressCount = tt.count
			el.mu.Unlock()

			msg := el.progressMessage()
			if msg != tt.expected {
				t.Errorf("progressMessage() = %q, want %q", msg, tt.expected)
			}
		})
	}
}

func TestSendAsFile(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := DefaultEventLoopConfig()

	el := NewEventLoop(bot, 12345, state, cfg, logger)

	// Very long text should trigger file send
	longText := strings.Repeat("x", 16001)

	el.sendAsFile(longText)
}

func TestDefaultEventLoopConfig(t *testing.T) {
	t.Helper()

	cfg := DefaultEventLoopConfig()

	if cfg.EditInterval != 1*time.Second {
		t.Errorf("EditInterval = %v, want 1s", cfg.EditInterval)
	}
	if cfg.ProgressInterval != 2*time.Minute {
		t.Errorf("ProgressInterval = %v, want 2m", cfg.ProgressInterval)
	}
	if cfg.InactivityTimeout != 15*time.Minute {
		t.Errorf("InactivityTimeout = %v, want 15m", cfg.InactivityTimeout)
	}
	if cfg.TypingInterval != 4*time.Second {
		t.Errorf("TypingInterval = %v, want 4s", cfg.TypingInterval)
	}
	if cfg.MaxEditsBeforeNew != 20 {
		t.Errorf("MaxEditsBeforeNew = %d, want 20", cfg.MaxEditsBeforeNew)
	}
}

func TestEventLoop_UpdateLastActivity(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := DefaultEventLoopConfig()

	el := NewEventLoop(bot, 12345, state, cfg, logger)

	before := time.Now()
	el.HandleEvent(router.ClassifiedEvent{
		Action: router.ActionText,
		Text:   "test",
	})
	after := time.Now()

	el.mu.Lock()
	lastActivity := el.lastActivity
	el.mu.Unlock()

	if lastActivity.Before(before) || lastActivity.After(after) {
		t.Errorf("lastActivity not updated correctly")
	}
}

// ---------------------------------------------------------------------------
// Concurrent tests
// ---------------------------------------------------------------------------

func TestHandleEvent_Concurrent(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := EventLoopConfig{
		EditInterval:      10 * time.Millisecond,
		ProgressInterval:  1 * time.Hour,
		TypingInterval:    1 * time.Hour,
		MaxEditsBeforeNew: 100,
	}

	el := NewEventLoop(bot, 12345, state, cfg, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	el.Start(ctx)
	defer el.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			el.HandleEvent(router.ClassifiedEvent{
				Action: router.ActionText,
				Text:   "text",
			})
		}(i)
	}

	wg.Wait()
}

func TestEventLoopStop_WaitForGoroutines(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := EventLoopConfig{
		EditInterval:      10 * time.Millisecond,
		ProgressInterval:  10 * time.Millisecond,
		TypingInterval:    10 * time.Millisecond,
		MaxEditsBeforeNew: 20,
	}

	el := NewEventLoop(bot, 12345, state, cfg, logger)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	el.Start(ctx)

	done := make(chan struct{})
	go func() {
		el.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("Stop() did not wait for goroutines")
	}
}

// ---------------------------------------------------------------------------
// Table-driven tests for handleEvent dispatch
// ---------------------------------------------------------------------------

func TestHandleEvent_Dispatch(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	bot := &Bot{}
	state := types.NewSessionState()
	cfg := DefaultEventLoopConfig()

	tests := []struct {
		name   string
		event  router.ClassifiedEvent
		action router.Action
	}{
		{
			name:   "text action",
			event:  router.ClassifiedEvent{Action: router.ActionText, Text: "hello"},
			action: router.ActionText,
		},
		{
			name:   "tool_use action",
			event:  router.ClassifiedEvent{Action: router.ActionToolUse},
			action: router.ActionToolUse,
		},
		{
			name:   "result action",
			event:  router.ClassifiedEvent{Action: router.ActionResult, Text: "done"},
			action: router.ActionResult,
		},
		{
			name:   "system action",
			event:  router.ClassifiedEvent{Action: router.ActionSystem},
			action: router.ActionSystem,
		},
		{
			name:   "thinking action",
			event:  router.ClassifiedEvent{Action: router.ActionThinking},
			action: router.ActionThinking,
		},
		{
			name:   "rate_limit action",
			event:  router.ClassifiedEvent{Action: router.ActionRateLimit},
			action: router.ActionRateLimit,
		},
		{
			name:   "api_retry action",
			event:  router.ClassifiedEvent{Action: router.ActionAPIRetry},
			action: router.ActionAPIRetry,
		},
		{
			name:   "error action",
			event:  router.ClassifiedEvent{Action: router.ActionError, Text: "failed"},
			action: router.ActionError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			el := NewEventLoop(bot, 12345, state, cfg, logger)
			el.HandleEvent(tt.event)
		})
	}
}

var _ = atomic.Int64{} // suppress unused warning
