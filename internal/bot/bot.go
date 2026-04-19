package bot

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// QueuedMessage represents a message waiting to be sent through the rate-limited queue.
type QueuedMessage struct {
	ChatID    int64
	Text      string
	ParseMode string // "HTML" or ""
	ReplyTo   int    // message ID to reply to, 0 for no reply
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

// Send enqueues a message for delivery. Non-blocking.
func (b *Bot) Send(ctx context.Context, chatID int64, text string, parseMode string) (*models.Message, error) {
	msg := &QueuedMessage{
		ChatID:    chatID,
		Text:      text,
		ParseMode: parseMode,
	}

	select {
	case b.sendQueue <- msg:
		return nil, nil
	default:
		return nil, fmt.Errorf("bot: send queue full, message dropped")
	}
}

// SendDirect sends a message immediately, bypassing the rate-limited queue.
func (b *Bot) SendDirect(ctx context.Context, chatID int64, text string, parseMode string) (*models.Message, error) {
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
