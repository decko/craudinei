package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/decko/craudinei/internal/router"
	"github.com/decko/craudinei/internal/types"
)

// EventLoopConfig holds configuration for the event loop.
type EventLoopConfig struct {
	// EditInterval is the debounce interval for edit-based streaming (default 1s).
	EditInterval time.Duration
	// ProgressInterval is the interval for progress updates (default 2min).
	ProgressInterval time.Duration
	// InactivityTimeout is the timeout before inactivity notification (default 15min).
	InactivityTimeout time.Duration
	// TypingInterval is the interval for typing indicator (default 4s).
	TypingInterval time.Duration
	// MaxEditsBeforeNew is the max edits before sending a new message (default 20).
	MaxEditsBeforeNew int
}

// DefaultEventLoopConfig returns the default event loop configuration.
func DefaultEventLoopConfig() EventLoopConfig {
	return EventLoopConfig{
		EditInterval:      1 * time.Second,
		ProgressInterval:  2 * time.Minute,
		InactivityTimeout: 15 * time.Minute,
		TypingInterval:    4 * time.Second,
		MaxEditsBeforeNew: 20,
	}
}

// EventLoop connects classified router events to Telegram actions.
// It manages the streaming of assistant text, progress updates,
// typing indicators, and inactivity detection.
type EventLoop struct {
	bot    *Bot
	chatID int64
	state  *types.SessionState
	logger *slog.Logger

	// Streaming state
	mu         sync.Mutex
	lastMsgID  int
	lastEdit   time.Time
	editBuffer strings.Builder
	editCount  int

	// Progress ticker
	progressTicker   *time.Ticker
	progressCount    int
	progressInterval time.Duration // current interval (can be doubled)
	lastActivity     time.Time

	// Typing indicator
	typingTicker *time.Ticker

	// Configuration
	cfg EventLoopConfig

	// Lifecycle
	done     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewEventLoop creates a new EventLoop with the given configuration.
func NewEventLoop(bot *Bot, chatID int64, state *types.SessionState, cfg EventLoopConfig, logger *slog.Logger) *EventLoop {
	if cfg.EditInterval == 0 {
		cfg.EditInterval = 1 * time.Second
	}
	if cfg.ProgressInterval == 0 {
		cfg.ProgressInterval = 2 * time.Minute
	}
	if cfg.InactivityTimeout == 0 {
		cfg.InactivityTimeout = 15 * time.Minute
	}
	if cfg.TypingInterval == 0 {
		cfg.TypingInterval = 4 * time.Second
	}
	if cfg.MaxEditsBeforeNew == 0 {
		cfg.MaxEditsBeforeNew = 20
	}

	el := &EventLoop{
		bot:          bot,
		chatID:       chatID,
		state:        state,
		logger:       logger,
		cfg:          cfg,
		lastActivity: time.Now(),
		done:         make(chan struct{}),
	}

	return el
}

// Start begins the progress ticker and typing indicator goroutines.
func (el *EventLoop) Start(ctx context.Context) {
	el.progressInterval = el.cfg.ProgressInterval
	el.progressTicker = time.NewTicker(el.progressInterval)
	el.typingTicker = time.NewTicker(el.cfg.TypingInterval)

	el.wg.Add(2)
	go el.startProgressTicker(ctx)
	go el.startTypingIndicator(ctx)
}

// HandleEvent is the main event dispatcher. It dispatches to specific handlers
// based on the event action type.
func (el *EventLoop) HandleEvent(event router.ClassifiedEvent) {
	el.mu.Lock()
	el.lastActivity = time.Now()
	el.progressCount = 0
	el.mu.Unlock()

	el.state.TouchActivity()

	switch event.Action {
	case router.ActionText:
		el.handleText(event)
	case router.ActionToolUse:
		el.handleToolUse(event)
	case router.ActionResult:
		el.handleResult(event)
	case router.ActionSystem:
		el.handleSystem(event)
	case router.ActionThinking:
		el.handleThinking(event)
	case router.ActionRateLimit:
		el.handleRateLimit(event)
	case router.ActionAPIRetry:
		el.handleAPIRetry(event)
	case router.ActionError:
		el.handleError(event)
	}
}

// handleText accumulates text in the edit buffer and debounces edits to Telegram.
func (el *EventLoop) handleText(event router.ClassifiedEvent) {
	if event.Text == "" {
		return
	}

	el.mu.Lock()
	el.editBuffer.WriteString(event.Text)
	el.editCount++
	shouldFlush := el.editCount >= el.cfg.MaxEditsBeforeNew
	el.mu.Unlock()

	if shouldFlush {
		el.flushBuffer()
	} else {
		el.scheduleFlush()
	}
}

// handleToolUse sends an approval card for tool use events.
func (el *EventLoop) handleToolUse(event router.ClassifiedEvent) {
	// Extract tool call info from event
	if event.Event.Message != nil {
		for _, block := range event.Event.Message.Content {
			if block.Type == "tool_use" {
				// Send a notification about tool use
				toolName := block.Name
				if toolName == "" {
					toolName = "unknown"
				}

				text := fmt.Sprintf("🔧 <b>Tool Use:</b> %s", EscapeHTML(toolName))
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_, _ = el.bot.SendPriority(ctx, el.chatID, text, "HTML", PriorityHigh)
				return
			}
		}
	}

	// Fallback: log the tool use
	el.logger.Info("eventloop: tool use event", "session_id", el.state.SessionID())
}

// handleResult sends result text to the user.
func (el *EventLoop) handleResult(event router.ClassifiedEvent) {
	// Flush any pending text first
	el.flushBuffer()

	if event.Text == "" {
		return
	}

	// Check if we should send as file (file content is NOT HTML-escaped)
	if ShouldSendAsFile(event.Text) {
		el.sendAsFile(event.Text)
		return
	}

	// Escape HTML for inline display
	escapedText := EscapeHTML(event.Text)

	chunks := ChunkMessages(escapedText)
	for _, chunk := range chunks {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, _ = el.bot.SendPriority(ctx, el.chatID, chunk, "HTML", PriorityHigh)
		cancel()
	}
}

// handleSystem handles system events (init, etc.).
func (el *EventLoop) handleSystem(event router.ClassifiedEvent) {
	el.logger.Info("eventloop: system event", "subtype", event.Event.Subtype, "session_id", event.Event.SessionID)

	// Handle init event — set session ID and transition starting → running
	if event.Event.Subtype == "init" && event.Event.SessionID != "" {
		el.state.SetSessionID(event.Event.SessionID)
		if el.state.TransitionStatus(types.StatusStarting, types.StatusRunning) {
			el.logger.Info("eventloop: session ready", "session_id", event.Event.SessionID)
		}
	}
}

// handleThinking sends a typing indicator.
func (el *EventLoop) handleThinking(event router.ClassifiedEvent) {
	// Send typing indicator - this is a no-op as the typing ticker handles it
	// The thinking action indicates Claude is thinking, which should
	// trigger/show typing indicator to user
	el.logger.Debug("eventloop: thinking", "session_id", el.state.SessionID())
}

// handleRateLimit notifies the user of rate limits.
func (el *EventLoop) handleRateLimit(event router.ClassifiedEvent) {
	text := "⚠️ <b>Rate Limit</b>\nPlease wait before sending more messages."
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = el.bot.SendPriority(ctx, el.chatID, text, "HTML", PriorityLow)
}

// handleAPIRetry notifies the user of API retries with countdown.
func (el *EventLoop) handleAPIRetry(event router.ClassifiedEvent) {
	retryAfter := event.Event.RetryAfterSeconds
	text := fmt.Sprintf("🔄 <b>API Retry</b>\nRetrying in %d seconds...", retryAfter)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = el.bot.SendPriority(ctx, el.chatID, text, "HTML", PriorityLow)
}

// handleError sends an error message to the user.
func (el *EventLoop) handleError(event router.ClassifiedEvent) {
	// Flush any pending text first
	el.flushBuffer()

	text := fmt.Sprintf("❌ <b>Error:</b> %s", EscapeHTML(event.Text))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = el.bot.SendPriority(ctx, el.chatID, text, "HTML", PriorityHigh)
}

// scheduleFlush schedules a debounced flush of the edit buffer.
// It only sends an edit if the EditInterval has passed since the last edit.
func (el *EventLoop) scheduleFlush() {
	el.mu.Lock()
	defer el.mu.Unlock()

	now := time.Now()
	if el.editBuffer.Len() == 0 {
		return
	}

	// If enough time has passed since last edit, flush immediately
	if now.Sub(el.lastEdit) >= el.cfg.EditInterval {
		go el.flushBuffer()
		el.lastEdit = now
	}
}

// flushBuffer sends the current buffer content as an edit to the last message
// or as a new message, then resets the buffer atomically.
func (el *EventLoop) flushBuffer() {
	el.mu.Lock()
	if el.editBuffer.Len() == 0 {
		el.mu.Unlock()
		return
	}

	text := el.editBuffer.String()
	editCount := el.editCount
	el.editBuffer.Reset()
	el.editCount = 0
	el.mu.Unlock()

	if text == "" {
		return
	}

	// Check if we should send as file (raw content, not HTML-escaped)
	if ShouldSendAsFile(text) {
		el.sendAsFile(text)
		return
	}

	// Escape HTML for inline Telegram display
	escapedText := EscapeHTML(text)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use edit-based streaming if we have a lastMsgID and haven't exceeded MaxEditsBeforeNew
	if editCount > 0 && editCount < el.cfg.MaxEditsBeforeNew && el.lastMsgID != 0 {
		_, err := el.bot.SendEdit(ctx, el.chatID, el.lastMsgID, escapedText, "HTML")
		if err != nil {
			el.logger.Error("eventloop: editing message", "err", err)
			// On error, try sending as new message
			msg, err := el.bot.SendDirect(ctx, el.chatID, escapedText, "HTML")
			if err == nil && msg != nil {
				el.lastMsgID = int(msg.ID)
			}
		}
	} else {
		// Send as new message
		chunks := ChunkMessages(escapedText)
		for _, chunk := range chunks {
			msg, err := el.bot.SendDirect(ctx, el.chatID, chunk, "HTML")
			if err != nil {
				el.logger.Error("eventloop: sending message", "err", err)
			} else if msg != nil {
				el.lastMsgID = int(msg.ID)
			}
		}
	}
}

// sendAsFile sends the text as a .txt file attachment via Telegram SendDocument API.
// If SendDocument fails, it falls back to sending the content as regular messages.
func (el *EventLoop) sendAsFile(text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	filename := fmt.Sprintf("output_%s.txt", time.Now().Format("20060102_150405"))
	caption := "📎 Output (too long for inline display)"

	if _, err := el.bot.SendDocument(ctx, el.chatID, filename, text, caption); err != nil {
		el.logger.Error("failed to send document", "err", err)
		// Fallback: send as regular message chunks (escaped for HTML)
		chunks := ChunkMessages(EscapeHTML(text))
		for _, chunk := range chunks {
			ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
			_, _ = el.bot.SendPriority(ctx2, el.chatID, chunk, "HTML", PriorityHigh)
			cancel2()
		}
	}
}

// startProgressTicker runs a goroutine that sends progress updates when no
// events arrive for InactivityTimeout. After 3 consecutive updates with no
// activity, it doubles the interval and changes the message.
func (el *EventLoop) startProgressTicker(ctx context.Context) {
	defer el.wg.Done()

	for {
		select {
		case <-el.done:
			return
		case <-el.progressTicker.C:
			el.mu.Lock()
			el.progressCount++
			lastActivity := el.lastActivity
			timeSinceActivity := time.Since(lastActivity)
			el.mu.Unlock()

			// Check if we should send a progress update using InactivityTimeout
			if timeSinceActivity >= el.cfg.InactivityTimeout {
				text := el.progressMessage()
				if text != "" {
					sendCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					_, _ = el.bot.SendPriority(sendCtx, el.chatID, text, "HTML", PriorityLow)
					cancel()
				}

				// After 3 consecutive progress updates, double the interval
				el.mu.Lock()
				if el.progressCount >= 3 {
					el.progressInterval *= 2
					el.progressTicker.Stop()
					el.progressTicker = time.NewTicker(el.progressInterval)
				}
				el.mu.Unlock()
			}
		}
	}
}

// progressMessage generates the appropriate progress message based on
// consecutive update count.
func (el *EventLoop) progressMessage() string {
	el.mu.Lock()
	count := el.progressCount
	el.mu.Unlock()

	if count >= 3 {
		return "⏳ Still working... (this is taking longer than usual)"
	}
	if count >= 1 {
		return "⏳ Working..."
	}
	return ""
}

// startTypingIndicator runs a goroutine that sends typing indicator
// sendChatAction every TypingInterval while the session is running.
func (el *EventLoop) startTypingIndicator(ctx context.Context) {
	defer el.wg.Done()

	for {
		select {
		case <-el.done:
			return
		case <-el.typingTicker.C:
			status := el.state.Status()
			if status == types.StatusRunning || status == types.StatusStarting {
				// In a real implementation, we would use the Telegram API to send
				// sendChatAction with ChatActionTyping. For now, we just log it.
				el.logger.Debug("eventloop: typing indicator", "chat_id", el.chatID)
			}
		}
	}
}

// Stop stops all goroutines, flushes the remaining buffer, and waits for completion.
func (el *EventLoop) Stop() {
	el.stopOnce.Do(func() {
		close(el.done)
	})

	// Flush any remaining buffer
	el.flushBuffer()

	// Stop the tickers
	if el.progressTicker != nil {
		el.progressTicker.Stop()
	}
	if el.typingTicker != nil {
		el.typingTicker.Stop()
	}

	el.wg.Wait()
}
