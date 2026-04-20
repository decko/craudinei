package bot

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// MessagePriority represents the priority of a queued message.
// High-priority messages (results, errors) are never dropped when the queue is full.
// Low-priority messages (progress, status) are dropped first.
type MessagePriority int

const (
	PriorityLow  MessagePriority = iota // progress, status, thinking
	PriorityHigh                        // results, errors, tool approvals
)

// QueuedMessage represents a message waiting to be sent through the rate-limited queue.
type QueuedMessage struct {
	ChatID    int64
	Text      string
	ParseMode string // "HTML" or ""
	ReplyTo   int    // message ID to reply to, 0 for no reply
	Priority  MessagePriority
}

// Sender is the interface for sending messages. Allows injection of a fake for testing.
type Sender interface {
	SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error)
}

// Bot wraps the Telegram bot API with a rate-limited send queue and authentication.
type Bot struct {
	api       *bot.Bot
	cfg       *Config
	auth      *Auth
	logger    *slog.Logger
	sendQueue chan *QueuedMessage
	done      chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup

	// bannerFileID caches the Telegram file_id for the banner image to avoid
	// re-downloading on every SendPhoto call.
	bannerFileID   string
	bannerFileIDMu sync.RWMutex
}

// Config is the subset of config.Config needed by Bot.
type Config = struct {
	Telegram struct {
		Token        string
		AllowedUsers []int64
	}
}

// NewBot creates a new Bot instance. Returns an error if the token is empty.
func NewBot(cfg *Config, auth *Auth, logger *slog.Logger) (*Bot, error) {
	if cfg.Telegram.Token == "" {
		return nil, fmt.Errorf("bot: empty token")
	}

	api, err := bot.New(cfg.Telegram.Token, bot.WithSkipGetMe())
	if err != nil {
		return nil, fmt.Errorf("bot: creating: %w", err)
	}

	return &Bot{
		api:       api,
		cfg:       cfg,
		auth:      auth,
		logger:    logger,
		sendQueue: make(chan *QueuedMessage, 100),
		done:      make(chan struct{}),
	}, nil
}

// Start deletes any stale webhook, starts the send queue goroutine, and begins long-polling.
func (b *Bot) Start(ctx context.Context) error {
	if _, err := b.api.DeleteWebhook(ctx, &bot.DeleteWebhookParams{}); err != nil {
		return fmt.Errorf("bot: deleting webhook: %w", err)
	}

	b.wg.Add(1)
	go b.sendQueueDrain(b.api)

	b.logger.Info("starting long polling")
	b.api.Start(ctx)
	return nil
}

// Stop closes the done channel and waits for all goroutines to finish.
func (b *Bot) Stop() {
	b.stopOnce.Do(func() {
		close(b.done)
	})
	b.wg.Wait()
}

// sendQueueDrain drains the send queue at a rate of ~1 message per second, dropping
// progress/status events if the queue is full while preserving result/error events.
func (b *Bot) sendQueueDrain(sender Sender) {
	defer b.wg.Done()

	for {
		select {
		case <-b.done:
			// Drain remaining messages before exit
			for {
				select {
				case msg := <-b.sendQueue:
					b.sendMessage(sender, msg)
				default:
					return
				}
			}
		case msg := <-b.sendQueue:
			b.sendMessage(sender, msg)
			time.Sleep(1 * time.Second)
		}
	}
}

// sendMessage delivers a single queued message via the provided sender.
func (b *Bot) sendMessage(sender Sender, msg *QueuedMessage) {
	params := &bot.SendMessageParams{
		ChatID: msg.ChatID,
		Text:   msg.Text,
	}
	if msg.ParseMode == "HTML" {
		params.ParseMode = models.ParseModeHTML
	}
	if msg.ReplyTo > 0 {
		params.ReplyParameters = &models.ReplyParameters{
			MessageID: msg.ReplyTo,
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := sender.SendMessage(ctx, params)
	if err != nil {
		b.logger.Error("failed to send message", "chat_id", msg.ChatID, "err", err)
	}
}

// Send enqueues a message for delivery with low priority. Non-blocking.
func (b *Bot) Send(ctx context.Context, chatID int64, text string, parseMode string) (*models.Message, error) {
	return b.SendPriority(ctx, chatID, text, parseMode, PriorityLow)
}

// SendPriority enqueues a message with the given priority. Non-blocking.
// When the queue is full, high-priority messages displace low-priority messages
// to make room, while low-priority messages are dropped.
func (b *Bot) SendPriority(ctx context.Context, chatID int64, text string, parseMode string, priority MessagePriority) (*models.Message, error) {
	msg := &QueuedMessage{
		ChatID:    chatID,
		Text:      text,
		ParseMode: parseMode,
		Priority:  priority,
	}

	// Try to enqueue directly
	select {
	case b.sendQueue <- msg:
		return nil, nil
	default:
	}

	// Queue is full
	if priority == PriorityHigh {
		// High-priority: drain one message to make room, then retry
		select {
		case dropped := <-b.sendQueue:
			if b.logger != nil {
				b.logger.Debug("dropping message to make room for high-priority", "dropped_priority", dropped.Priority, "text_preview", truncate(dropped.Text, 50))
			}
			// Retry send — the channel has capacity now
			select {
			case b.sendQueue <- msg:
				return nil, nil
			default:
				// Race: slot was filled by concurrent sender between drain and retry.
				// The drain goroutine may have freed more space. Try once more.
				select {
				case b.sendQueue <- msg:
					return nil, nil
				default:
					return nil, fmt.Errorf("bot: send queue full, high-priority message dropped")
				}
			}
		default:
			// Channel was drained between initial send failure and here. Retry send.
			select {
			case b.sendQueue <- msg:
				return nil, nil
			default:
				return nil, fmt.Errorf("bot: send queue full, high-priority message dropped")
			}
		}
	}

	// Low-priority: drop silently
	if b.logger != nil {
		b.logger.Debug("dropping low-priority message", "text_preview", truncate(text, 50))
	}
	return nil, fmt.Errorf("bot: send queue full, low-priority message dropped")
}

// truncate truncates a string to maxLen characters, appending "..." if truncated.
// Uses rune-based truncation to handle multi-byte characters correctly.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// SendDirect sends a message immediately, bypassing the rate-limited queue.
func (b *Bot) SendDirect(ctx context.Context, chatID int64, text string, parseMode string) (*models.Message, error) {
	if b.api == nil {
		return nil, fmt.Errorf("bot: api not initialized")
	}
	params := &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	}
	if parseMode == "HTML" {
		params.ParseMode = models.ParseModeHTML
	}

	msg, err := b.api.SendMessage(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("bot: send direct: %w", err)
	}
	return msg, nil
}

// SendEdit edits an existing message using the Telegram editMessageText API.
func (b *Bot) SendEdit(ctx context.Context, chatID int64, messageID int, text string, parseMode string) (any, error) {
	if b.api == nil {
		return nil, fmt.Errorf("bot: api not initialized")
	}
	params := &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      text,
	}
	if parseMode == "HTML" {
		params.ParseMode = models.ParseModeHTML
	}

	msg, err := b.api.EditMessageText(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("bot: send edit: %w", err)
	}
	return msg, nil
}

// IsAllowedUser checks whether a user ID is in the allowed users list.
func (b *Bot) IsAllowedUser(userID int64) bool {
	for _, id := range b.cfg.Telegram.AllowedUsers {
		if id == userID {
			return true
		}
	}
	return false
}

// RegisterCommand registers a handler for a bot command.
func (b *Bot) RegisterCommand(command string, handler func(ctx context.Context, api *bot.Bot, update *models.Update)) {
	b.api.RegisterHandler(bot.HandlerTypeMessageText, "/"+command, bot.MatchTypeExact, handler)
}

// RegisterCallbackHandler registers a handler for callback queries matching the given prefix.
func (b *Bot) RegisterCallbackHandler(prefix string, handler func(ctx context.Context, api *bot.Bot, update *models.Update)) {
	b.api.RegisterHandler(bot.HandlerTypeCallbackQueryData, prefix, bot.MatchTypePrefix, handler)
}

// AnswerCallbackQuery answers a callback query, showing a brief text notification
// to the user. Must be called within 30 seconds of receiving the callback query.
func (b *Bot) AnswerCallbackQuery(ctx context.Context, callbackQueryID string, text string) error {
	if b.api == nil {
		return fmt.Errorf("bot: api not initialized")
	}
	_, err := b.api.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: callbackQueryID,
		Text:            text,
	})
	if err != nil {
		return fmt.Errorf("bot: answer callback query: %w", err)
	}
	return nil
}

// SetMyCommands registers the bot's command list with Telegram.
func (b *Bot) SetMyCommands(ctx context.Context) error {
	commands := []models.BotCommand{
		{Command: "begin", Description: "Start a new Claude Code session"},
		{Command: "stop", Description: "Stop the current session"},
		{Command: "cancel", Description: "Cancel the current operation"},
		{Command: "status", Description: "Show current session status"},
		{Command: "auth", Description: "Authenticate to unlock session controls"},
		{Command: "help", Description: "Show help information"},
		{Command: "resume", Description: "Resume an existing session"},
		{Command: "sessions", Description: "List active sessions"},
		{Command: "reload", Description: "Reload configuration"},
	}

	_, err := b.api.SetMyCommands(ctx, &bot.SetMyCommandsParams{
		Commands: commands,
	})
	if err != nil {
		return fmt.Errorf("bot: set commands: %w", err)
	}
	return nil
}

// SendDocument sends a document (file attachment) to the specified chat.
// The document is sent as a .txt file with the given filename and content.
func (b *Bot) SendDocument(ctx context.Context, chatID int64, filename string, content string, caption string) (*models.Message, error) {
	if b.api == nil {
		return nil, fmt.Errorf("bot: api not initialized")
	}
	params := &bot.SendDocumentParams{
		ChatID: chatID,
		Document: &models.InputFileUpload{
			Filename: filename,
			Data:     bytes.NewReader([]byte(content)),
		},
		Caption: caption,
	}
	msg, err := b.api.SendDocument(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("bot: send document: %w", err)
	}
	return msg, nil
}

// SendPhoto sends a photo with optional inline keyboard to the specified chat.
// If photoURL is empty, falls back to sendMessage with the caption text and keyboard.
// When photoURL is a URL, the returned file_id is cached for subsequent sends.
func (b *Bot) SendPhoto(ctx context.Context, chatID int64, photoURL string, caption string, parseMode string, keyboard models.InlineKeyboardMarkup) (*models.Message, error) {
	if b.api == nil {
		return nil, fmt.Errorf("bot: api not initialized")
	}

	if photoURL == "" {
		params := &bot.SendMessageParams{
			ChatID:      chatID,
			Text:        caption,
			ReplyMarkup: keyboard,
		}
		if parseMode == "HTML" {
			params.ParseMode = models.ParseModeHTML
		}
		return b.api.SendMessage(ctx, params)
	}

	// Check cache for banner file_id
	b.bannerFileIDMu.RLock()
	cachedFileID := b.bannerFileID
	b.bannerFileIDMu.RUnlock()

	var photoInput models.InputFile
	if cachedFileID != "" {
		photoInput = &models.InputFileString{Data: cachedFileID}
	} else {
		photoInput = &models.InputFileString{Data: photoURL}
	}

	params := &bot.SendPhotoParams{
		ChatID:      chatID,
		Photo:       photoInput,
		Caption:     caption,
		ReplyMarkup: keyboard,
	}
	if parseMode == "HTML" {
		params.ParseMode = models.ParseModeHTML
	}
	msg, err := b.api.SendPhoto(ctx, params)
	if err != nil {
		if cachedFileID != "" {
			b.bannerFileIDMu.Lock()
			b.bannerFileID = ""
			b.bannerFileIDMu.Unlock()
		}
		return nil, fmt.Errorf("bot: send photo: %w", err)
	}

	// Cache file_id from first photo in response for future use
	if msg.Photo != nil && len(msg.Photo) > 0 && msg.Photo[0].FileID != "" {
		b.bannerFileIDMu.Lock()
		b.bannerFileID = msg.Photo[0].FileID
		b.bannerFileIDMu.Unlock()
	}

	return msg, nil
}
