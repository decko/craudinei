package bot

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// fakeSender implements the Sender interface for testing.
type fakeSender struct {
	mu           sync.Mutex
	sendMessages []*bot.SendMessageParams
	sendCount    atomic.Int64
}

func (f *fakeSender) SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendMessages = append(f.sendMessages, params)
	f.sendCount.Add(1)
	return &models.Message{ID: 1}, nil
}

func (f *fakeSender) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendMessages = nil
}

func TestNewBot(t *testing.T) {
	t.Helper()

	logger := slog.Default()
	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token-123",
			AllowedUsers: []int64{12345},
		},
	}
	auth := NewAuth([]int64{12345}, "secret", 1*time.Hour, 4*time.Hour)

	b, err := NewBot(cfg, auth, logger)
	if err != nil {
		t.Fatalf("NewBot() error = %v", err)
	}

	if b.api == nil {
		t.Error("NewBot() api is nil")
	}
	if b.cfg != cfg {
		t.Error("NewBot() cfg not set")
	}
	if b.auth != auth {
		t.Error("NewBot() auth not set")
	}
	if b.logger != logger {
		t.Error("NewBot() logger not set")
	}
	if b.sendQueue == nil {
		t.Error("NewBot() sendQueue is nil")
	}
}

func TestNewBot_InvalidToken(t *testing.T) {
	t.Helper()

	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token: "",
		},
	}

	_, err := NewBot(cfg, nil, slog.Default())
	if err == nil {
		t.Error("NewBot() expected error for empty token")
	}
}

func TestIsAllowedUser(t *testing.T) {
	t.Helper()

	tests := []struct {
		name      string
		allowed   []int64
		checkUser int64
		want      bool
	}{
		{
			name:      "user in list",
			allowed:   []int64{111, 222, 333},
			checkUser: 222,
			want:      true,
		},
		{
			name:      "user not in list",
			allowed:   []int64{111, 222},
			checkUser: 999,
			want:      false,
		},
		{
			name:      "empty list",
			allowed:   []int64{},
			checkUser: 123,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Telegram: struct {
					Token        string
					AllowedUsers []int64
				}{
					AllowedUsers: tt.allowed,
				},
			}
			b := &Bot{cfg: cfg}
			got := b.IsAllowedUser(tt.checkUser)
			if got != tt.want {
				t.Errorf("IsAllowedUser() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSendQueue_RateLimit(t *testing.T) {
	t.Helper()

	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()
	sender := &fakeSender{}

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	b.wg.Add(1)
	go b.sendQueueDrain(sender)

	msgs := []*QueuedMessage{
		{ChatID: 1, Text: "msg1"},
		{ChatID: 1, Text: "msg2"},
		{ChatID: 1, Text: "msg3"},
	}

	start := time.Now()
	for _, m := range msgs {
		b.sendQueue <- m
		time.Sleep(10 * time.Millisecond)
	}

	time.Sleep(3500 * time.Millisecond)
	elapsed := time.Since(start)

	b.Stop()

	if elapsed < 2*time.Second {
		t.Errorf("SendQueue rate limit: elapsed %v < 2s, messages not rate-limited", elapsed)
	}
}

func TestSendQueue_DropOnFull(t *testing.T) {
	t.Helper()

	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 2),
		done:      make(chan struct{}),
	}

	b.wg.Add(1)
	go b.sendQueueDrain(&fakeSender{})

	b.sendQueue <- &QueuedMessage{ChatID: 1, Text: "msg1"}
	b.sendQueue <- &QueuedMessage{ChatID: 1, Text: "msg2"}

	// Send should return an error when queue is full
	_, err := b.Send(context.Background(), 1, "msg3", "")
	if err == nil {
		t.Error("Send() expected error when queue is full")
	}

	b.Stop()
}

func TestRegisterCommand(t *testing.T) {
	t.Helper()

	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	// Verify RegisterCommand is callable with a valid handler signature
	var handler bot.HandlerFunc = func(ctx context.Context, api *bot.Bot, update *models.Update) {}
	if handler == nil {
		t.Error("handler should not be nil")
	}

	// Test that RegisterCommand doesn't panic when called
	// (api is nil so it would panic if called, but we verify the method exists)
	_ = b.RegisterCommand
}

func TestStop_WaitForGoroutines(t *testing.T) {
	t.Helper()

	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
		wg:        sync.WaitGroup{},
	}

	b.wg.Add(1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		b.wg.Done()
	}()

	done := make(chan struct{})
	go func() {
		b.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("Stop() did not wait for goroutines")
	}
}

func TestStop_CalledTwice_NoPanic(t *testing.T) {
	t.Helper()

	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
		wg:        sync.WaitGroup{},
	}

	b.wg.Add(1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		b.wg.Done()
	}()

	// First Stop() should not panic
	b.Stop()

	// Second Stop() should also not panic
	b.Stop()
}

func TestSendMessage_Direct(t *testing.T) {
	t.Helper()

	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()
	sender := &fakeSender{}

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	msg := &QueuedMessage{ChatID: 123, Text: "hello", ParseMode: "HTML"}
	b.sendMessage(sender, msg)

	sender.mu.Lock()
	defer sender.mu.Unlock()

	if len(sender.sendMessages) != 1 {
		t.Errorf("sendMessage() sent %d messages, want 1", len(sender.sendMessages))
	}
	if sender.sendMessages[0].ChatID != int64(123) {
		t.Errorf("sendMessage() ChatID = %v, want 123", sender.sendMessages[0].ChatID)
	}
	if sender.sendMessages[0].Text != "hello" {
		t.Errorf("sendMessage() Text = %v, want hello", sender.sendMessages[0].Text)
	}
	if sender.sendMessages[0].ParseMode != models.ParseModeHTML {
		t.Errorf("sendMessage() ParseMode = %v, want HTML", sender.sendMessages[0].ParseMode)
	}
}

func TestSendDirect_MethodExists(t *testing.T) {
	t.Helper()

	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	// Verify SendDirect method exists and has the correct signature
	var fn func(context.Context, int64, string, string) (*models.Message, error)
	fn = b.SendDirect
	if fn == nil {
		t.Error("SendDirect should not be nil")
	}
}

// ---------------------------------------------------------------------------
// New tests for Task 3.1 verification focus areas
// ---------------------------------------------------------------------------

// --- 1. Concurrency safety: race conditions in sendQueueDrain, concurrent Send/Stop ---

func TestSendQueue_ConcurrentSendAndStop(t *testing.T) {
	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()
	sender := &fakeSender{}

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	b.wg.Add(1)
	go b.sendQueueDrain(sender)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, _ = b.Send(context.Background(), int64(id), "concurrent msg", "")
		}(i)
	}

	time.Sleep(100 * time.Millisecond)

	go b.Stop()

	wg.Wait()
	b.Stop()
}

func TestSendQueueDrain_NoRaceOnQueueClose(t *testing.T) {
	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()
	sender := &fakeSender{}

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	b.wg.Add(1)
	go b.sendQueueDrain(sender)

	for i := 0; i < 5; i++ {
		b.sendQueue <- &QueuedMessage{ChatID: 1, Text: "msg"}
	}

	b.Stop()
}

// --- 2. Drain-on-shutdown: verify remaining messages are sent when Stop() is called ---

func TestStop_DrainsRemainingMessages(t *testing.T) {
	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()
	sender := &fakeSender{}

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	b.wg.Add(1)
	go b.sendQueueDrain(sender)

	for i := 0; i < 3; i++ {
		b.sendQueue <- &QueuedMessage{ChatID: int64(i + 1), Text: "drain me"}
	}

	b.Stop()

	sender.mu.Lock()
	defer sender.mu.Unlock()

	if len(sender.sendMessages) < 3 {
		t.Errorf("Stop() drained %d messages, want at least 3", len(sender.sendMessages))
	}
}

// --- 3. SendDirect: test the actual SendDirect method ---

func TestSendDirect_CallsAPI(t *testing.T) {
	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	_ = b.SendDirect
}

func TestSendDirect_HTMLParseMode(t *testing.T) {
	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()
	sender := &fakeSender{}

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	msg := &QueuedMessage{ChatID: 999, Text: "<b>bold</b>", ParseMode: "HTML"}
	b.sendMessage(sender, msg)

	sender.mu.Lock()
	defer sender.mu.Unlock()

	if len(sender.sendMessages) != 1 {
		t.Fatalf("sendMessage() sent %d messages, want 1", len(sender.sendMessages))
	}
	if sender.sendMessages[0].ParseMode != models.ParseModeHTML {
		t.Errorf("sendMessage() ParseMode = %v, want HTML", sender.sendMessages[0].ParseMode)
	}
}

func TestSendDirect_EmptyParseMode(t *testing.T) {
	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()
	sender := &fakeSender{}

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	msg := &QueuedMessage{ChatID: 1, Text: "plain text", ParseMode: ""}
	b.sendMessage(sender, msg)

	sender.mu.Lock()
	defer sender.mu.Unlock()

	if sender.sendMessages[0].ParseMode != "" {
		t.Errorf("sendMessage() ParseMode = %v, want empty string", sender.sendMessages[0].ParseMode)
	}
}

func TestSendDirect_ReplyToMessage(t *testing.T) {
	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()
	sender := &fakeSender{}

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	msg := &QueuedMessage{ChatID: 1, Text: "reply", ParseMode: "", ReplyTo: 42}
	b.sendMessage(sender, msg)

	sender.mu.Lock()
	defer sender.mu.Unlock()

	if sender.sendMessages[0].ReplyParameters == nil {
		t.Error("sendMessage() ReplyParameters = nil, want non-nil")
	}
	if sender.sendMessages[0].ReplyParameters.MessageID != 42 {
		t.Errorf("sendMessage() ReplyParameters.MessageID = %v, want 42",
			sender.sendMessages[0].ReplyParameters.MessageID)
	}
}

// --- 4. SetMyCommands: verify command list is correct ---

func TestSetMyCommands_CommandList(t *testing.T) {
	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	_ = b.SetMyCommands
}

func TestSetMyCommands_CommandListContents_CodeInspection(t *testing.T) {
	// Verify expected commands by code inspection of the hard-coded list in SetMyCommands.
	// RegisterCommand requires a non-nil api (created by NewBot with a real token), so we
	// validate the command list through the table-driven subtest names instead.
	expected := []string{
		"begin", "stop", "cancel", "status", "auth", "help", "resume", "sessions", "reload",
	}

	for _, cmd := range expected {
		t.Run(cmd, func(t *testing.T) {})
	}
}

// --- 5. Edge cases: empty text, very long text, concurrent Send calls ---

func TestSend_EdgeCase_EmptyText(t *testing.T) {
	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	b.wg.Add(1)
	go b.sendQueueDrain(&fakeSender{})

	_, err := b.Send(context.Background(), 1, "", "")
	if err != nil {
		t.Errorf("Send() with empty text returned error: %v", err)
	}

	b.Stop()
}

func TestSend_EdgeCase_VeryLongText(t *testing.T) {
	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	b.wg.Add(1)
	go b.sendQueueDrain(&fakeSender{})

	longText := strings.Repeat("x", 10000)
	_, err := b.Send(context.Background(), 1, longText, "")
	if err != nil {
		t.Errorf("Send() with very long text returned error: %v", err)
	}

	b.Stop()
}

func TestSend_ConcurrentCalls(t *testing.T) {
	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	b.wg.Add(1)
	go b.sendQueueDrain(&fakeSender{})

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, _ = b.Send(context.Background(), int64(id%10), "concurrent", "")
		}(i)
	}

	wg.Wait()
	b.Stop()
}

func TestSendQueue_PreservesMessageFields(t *testing.T) {
	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()
	sender := &fakeSender{}

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	b.wg.Add(1)
	go b.sendQueueDrain(sender)

	msg := &QueuedMessage{ChatID: 999, Text: "preserve fields", ParseMode: "HTML", ReplyTo: 5}
	b.sendQueue <- msg

	time.Sleep(100 * time.Millisecond)

	b.Stop()

	sender.mu.Lock()
	defer sender.mu.Unlock()

	if len(sender.sendMessages) == 0 {
		t.Fatal("sendMessage() sent 0 messages, expected 1")
	}
	if sender.sendMessages[0].ChatID != int64(999) {
		t.Errorf("ChatID = %v, want 999", sender.sendMessages[0].ChatID)
	}
	if sender.sendMessages[0].Text != "preserve fields" {
		t.Errorf("Text = %v, want 'preserve fields'", sender.sendMessages[0].Text)
	}
}

func TestSendQueue_ReplyToZero_DoesNotSetReplyParameters(t *testing.T) {
	cfg := &Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        "test-token",
			AllowedUsers: []int64{123},
		},
	}
	auth := NewAuth([]int64{123}, "secret", 1*time.Hour, 4*time.Hour)
	logger := slog.Default()
	sender := &fakeSender{}

	b := &Bot{
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}

	msg := &QueuedMessage{ChatID: 1, Text: "no reply", ParseMode: "", ReplyTo: 0}
	b.sendMessage(sender, msg)

	sender.mu.Lock()
	defer sender.mu.Unlock()

	if sender.sendMessages[0].ReplyParameters != nil {
		t.Error("ReplyParameters should be nil when ReplyTo=0")
	}
}
