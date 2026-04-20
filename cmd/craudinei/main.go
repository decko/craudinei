// Package main provides the entry point for the Craudinei Telegram bot.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/decko/craudinei/internal/approval"
	"github.com/decko/craudinei/internal/audit"
	"github.com/decko/craudinei/internal/bot"
	"github.com/decko/craudinei/internal/claude"
	"github.com/decko/craudinei/internal/config"
	"github.com/decko/craudinei/internal/router"
	"github.com/decko/craudinei/internal/types"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// run wires all components together, starts the bot and approval server,
// and blocks until a shutdown signal is received or an error occurs.
func run() error {
	configPath := flag.String("config", defaultConfigPath(), "path to configuration file")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}

	logger := setupLogging(cfg)

	auditLogger, err := audit.NewWithFile(cfg.Logging.AuditFile)
	if err != nil {
		return fmt.Errorf("main: creating audit logger: %w", err)
	}
	defer auditLogger.Close()

	auth := bot.NewAuth(
		cfg.Telegram.AllowedUsers,
		cfg.Telegram.AuthPassphrase,
		cfg.Telegram.AuthIdleTimeout,
		cfg.Telegram.AuthIdleTimeout,
	)

	sm := claude.NewStateMachine(types.StatusIdle)
	queue := claude.NewInputQueue(5)

	eventRouter := router.NewRouter(func(event router.ClassifiedEvent) {
		handleClassifiedEvent(event, sm, logger)
	})

	manager := claude.NewManager(cfg, sm, queue, eventRouter, logger)

	botCfg := &bot.Config{
		Telegram: struct {
			Token        string
			AllowedUsers []int64
		}{
			Token:        cfg.Telegram.Token,
			AllowedUsers: cfg.Telegram.AllowedUsers,
		},
	}

	telegramBot, err := bot.NewBot(botCfg, auth, logger)
	if err != nil {
		return fmt.Errorf("main: creating bot: %w", err)
	}

	handlerCfg := &bot.HandlerConfig{
		AllowedPaths:   cfg.Claude.AllowedPaths,
		DefaultWorkDir: cfg.Claude.DefaultWorkDir,
		Passphrase:     cfg.Telegram.AuthPassphrase,
	}

	// Adapter for bot.Bot to satisfy handlers.BotSender interface.
	// The return type difference (*models.Message vs *struct{}) requires adaptation.
	handlersBotSender := &handlersBotSenderAdapter{bot: telegramBot}
	handlers := bot.NewHandlers(handlersBotSender, auth, handlerCfg, queue, logger, auditLogger)
	handlers.SetSessionManager(manager)

	telegramBot.RegisterCommand("begin", wrapHandler(handlers, (*bot.Handlers).HandleBegin))
	telegramBot.RegisterCommand("stop", wrapHandler(handlers, (*bot.Handlers).HandleStop))
	telegramBot.RegisterCommand("cancel", wrapHandler(handlers, (*bot.Handlers).HandleCancel))
	telegramBot.RegisterCommand("status", wrapHandler(handlers, (*bot.Handlers).HandleStatus))
	telegramBot.RegisterCommand("auth", wrapHandler(handlers, (*bot.Handlers).HandleAuth))
	telegramBot.RegisterCommand("help", wrapHandler(handlers, (*bot.Handlers).HandleHelp))
	telegramBot.RegisterCommand("resume", wrapHandler(handlers, (*bot.Handlers).HandleResume))
	telegramBot.RegisterCommand("sessions", wrapHandler(handlers, (*bot.Handlers).HandleSessions))
	telegramBot.RegisterCommand("reload", wrapHandler(handlers, (*bot.Handlers).HandleReload))

	// Adapter for bot.Bot to satisfy approval.BotSender interface.
	// The return type difference (*models.Message vs *struct{}) requires adaptation.
	approvalBotSender := &approvalBotSenderAdapter{bot: telegramBot}
	if len(cfg.Telegram.AllowedUsers) == 0 {
		return fmt.Errorf("main: no allowed users configured")
	}
	approvalHandler := approval.NewHandler(
		cfg.Telegram.AllowedUsers[0],
		approvalBotSender,
		cfg.Telegram.ApprovalTimeout,
		logger,
		auditLogger,
	)

	// Register callback query handler (FR-008: answer within 30 seconds)
	dispatcher := bot.NewCallbackDispatcher(approvalHandler, logger)
	telegramBot.RegisterCallbackHandler("", func(ctx context.Context, api *tgbot.Bot, update *models.Update) {
		if update.CallbackQuery == nil {
			return
		}

		// Issue 2 fix: handle inaccessible messages (too old per Telegram API)
		var chatID int64
		if update.CallbackQuery.Message.Message != nil {
			chatID = update.CallbackQuery.Message.Message.Chat.ID
		} else {
			// Inaccessible message (too old) — use user ID as fallback
			chatID = update.CallbackQuery.From.ID
		}
		userID := update.CallbackQuery.From.ID

		if !telegramBot.IsAllowedUser(userID) {
			_ = telegramBot.AnswerCallbackQuery(ctx, update.CallbackQuery.ID, "Access denied")
			return
		}
		if !auth.IsAuthenticated(userID) {
			_ = telegramBot.AnswerCallbackQuery(ctx, update.CallbackQuery.ID, "Please authenticate with /auth")
			return
		}

		// Dispatch is non-blocking: HandleCallback uses a buffered channel with
		// select/default, so it returns immediately. AnswerCallbackQuery is called
		// within the 30-second window.
		dirMap := dispatcher.GetDirMap(userID)
		result := dispatcher.DispatchWithDirMap(ctx, update.CallbackQuery.Data, dirMap)

		// Answer the callback query within 30 seconds (FR-008)
		if err := telegramBot.AnswerCallbackQuery(ctx, update.CallbackQuery.ID, result.AnswerText); err != nil {
			logger.Warn("failed to answer callback query", "err", err)
		}

		// Handle navigation and command actions
		switch result.Action {
		case "navigate":
			switch result.Target {
			case "home":
				screenText, screenKeyboard := bot.HomeScreen(manager.Status(), manager.WorkDir())
				if _, err := telegramBot.SendPhoto(ctx, chatID, cfg.Telegram.BannerImage, screenText, "HTML", models.InlineKeyboardMarkup{InlineKeyboard: screenKeyboard}); err != nil {
					logger.Warn("send photo failed, falling back to text", "err", err)
					telegramBot.Send(ctx, chatID, screenText, "HTML")
				}
			case "sessions":
				screenText, screenKeyboard := bot.SessionsListScreen(nil)
				if _, err := telegramBot.SendPhoto(ctx, chatID, cfg.Telegram.BannerImage, screenText, "HTML", models.InlineKeyboardMarkup{InlineKeyboard: screenKeyboard}); err != nil {
					logger.Warn("send photo failed, falling back to text", "err", err)
					telegramBot.Send(ctx, chatID, screenText, "HTML")
				}
			}
		case "command":
			if strings.HasPrefix(update.CallbackQuery.Data, "c:begin:") {
				workDir := result.Target
				if err := config.ValidateWorkDir(workDir, cfg.Claude.AllowedPaths); err != nil {
					logger.Warn("invalid work dir from callback", "dir", workDir, "err", err)
					return
				}
				if err := sm.CommandGuard("/begin"); err != nil {
					return
				}
				if err := sm.Transition(types.StatusStarting); err != nil {
					return
				}
				if err := manager.Start(ctx, workDir); err != nil {
					_ = sm.Transition(types.StatusCrashed)
					return
				}
			}
		}
	})

	approvalServer, err := approval.NewServer(&cfg.Approval, approvalHandler, logger)
	if err != nil {
		return fmt.Errorf("main: creating approval server: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := approvalServer.Start(ctx); err != nil {
		return fmt.Errorf("main: starting approval server: %w", err)
	}

	if err := telegramBot.SetMyCommands(ctx); err != nil {
		logger.Warn("failed to set bot commands", "err", err)
	}

	if err := telegramBot.Start(ctx); err != nil {
		return fmt.Errorf("main: starting bot: %w", err)
	}

	shutdownCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	<-shutdownCtx.Done()
	logger.Info("shutdown signal received")

	// Send shutdown notification before cancelling the context.
	// Using SendDirect (synchronous) instead of Send (async/enqueue) to ensure
	// delivery before cancel(). Send only enqueues to a channel; the actual
	// API call happens later during Stop(). SendDirect makes the HTTP call now.
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer sendCancel()
	shutdownMsg := "<b>🛑 Shutting down</b>\n\nCraudinei is shutting down. Your session will be terminated."
	if _, err := telegramBot.SendDirect(sendCtx, cfg.Telegram.AllowedUsers[0], shutdownMsg, "HTML"); err != nil {
		logger.Warn("failed to send shutdown notification", "err", err)
	}

	cancel()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := manager.Stop(stopCtx); err != nil {
		logger.Error("manager stop error", "err", err)
	}

	telegramBot.Stop()

	approvalServer.Stop()

	logger.Info("shutdown complete")
	return nil
}

// loadConfig loads configuration from the given path, expanding the path itself.
func loadConfig(path string) (*config.Config, error) {
	path = os.ExpandEnv(path)
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("main: resolving config path: %w", err)
		}
		path = abs
	}

	cfg, err := config.Load(path)
	if err != nil {
		return nil, fmt.Errorf("main: loading config: %w", err)
	}

	if err := config.Validate(cfg); err != nil {
		return nil, fmt.Errorf("main: validating config: %w", err)
	}

	return cfg, nil
}

// setupLogging configures slog based on the logging config level.
func setupLogging(cfg *config.Config) *slog.Logger {
	var level slog.Level
	switch cfg.Logging.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler = slog.NewJSONHandler(os.Stderr, opts)
	if cfg.Logging.File != "" {
		file, err := os.OpenFile(cfg.Logging.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err == nil {
			handler = slog.NewJSONHandler(file, opts)
		}
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

// defaultConfigPath returns the default configuration file path.
func defaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".craudinei", "craudinei.yaml")
}

// handlersBotSenderAdapter adapts bot.Bot to bot.BotSender (handlers package).
// The handlers.BotSender interface expects Send to return (*struct{}, error),
// but bot.Bot.Send returns (*models.Message, error). This adapter discards
// the message and returns nil to satisfy the interface.
type handlersBotSenderAdapter struct {
	bot *bot.Bot
}

func (a *handlersBotSenderAdapter) Send(ctx context.Context, chatID int64, text string, parseMode string) (*struct{}, error) {
	_, err := a.bot.Send(ctx, chatID, text, parseMode)
	return nil, err
}

// approvalBotSenderAdapter adapts bot.Bot to approval.BotSender.
// The approval.BotSender interface expects Send to return (any, error),
// which bot.Bot.Send satisfies since *models.Message is assignable to any.
type approvalBotSenderAdapter struct {
	bot *bot.Bot
}

func (a *approvalBotSenderAdapter) Send(ctx context.Context, chatID int64, text string, parseMode string) (any, error) {
	return a.bot.SendPriority(ctx, chatID, text, parseMode, bot.PriorityHigh)
}

// handleClassifiedEvent processes router events and updates state.
func handleClassifiedEvent(event router.ClassifiedEvent, sm *claude.StateMachine, logger *slog.Logger) {
	switch event.Action {
	case router.ActionSystem:
		logger.Debug("system event", "subtype", event.Event.Subtype)
	case router.ActionText:
		logger.Debug("text event", "text", event.Text)
	case router.ActionToolUse:
		logger.Debug("tool use event", "subtype", event.Event.Subtype)
	case router.ActionResult:
		logger.Debug("result event", "result", event.Text)
	case router.ActionThinking:
		logger.Debug("thinking event")
	case router.ActionRateLimit:
		logger.Warn("rate limit hit", "retry_after", event.Event.RetryAfterSeconds)
	case router.ActionAPIRetry:
		logger.Warn("api retry", "attempt", event.Event.Attempt, "max", event.Event.MaxRetries)
	case router.ActionError:
		logger.Error("error event", "is_error", event.Event.IsError)
	}
}

// handlerFunc is the function signature expected by bot.RegisterCommand.
type handlerFunc func(ctx context.Context, api *tgbot.Bot, update *models.Update)

// wrapHandler adapts a bot.Handlers method to the handlerFunc signature.
// The handlers use bot.Update (a simple struct alias) while RegisterCommand
// provides models.Update. We convert between them and discard the api param.
func wrapHandler(h *bot.Handlers, fn func(*bot.Handlers, context.Context, any, *bot.Update)) handlerFunc {
	return func(ctx context.Context, api *tgbot.Bot, update *models.Update) {
		botUpdate := convertUpdate(update)
		fn(h, ctx, nil, botUpdate)
	}
}

// convertUpdate converts a models.Update to a bot.Update.
func convertUpdate(src *models.Update) *bot.Update {
	if src == nil {
		return nil
	}
	dst := &bot.Update{}
	if src.Message != nil {
		dst.Message.Text = src.Message.Text
		// Chat and From are value types, not pointers
		dst.Message.Chat.ID = src.Message.Chat.ID
		if src.Message.From != nil {
			dst.Message.From.ID = src.Message.From.ID
		}
	}
	return dst
}
