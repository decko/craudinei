package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/decko/craudinei/internal/audit"
	"github.com/decko/craudinei/internal/config"
	"github.com/decko/craudinei/internal/types"
)

// HandlerConfig holds the configuration needed by command handlers.
type HandlerConfig struct {
	AllowedPaths   []string
	DefaultWorkDir string
	Passphrase     string
}

// SessionManager defines the interface for session lifecycle management.
// It allows handlers to interact with the Claude session without depending
// on the concrete Manager type, avoiding circular imports.
type SessionManager interface {
	Start(ctx context.Context, workDir string) error
	Stop(ctx context.Context) error
	Resume(ctx context.Context, sessionID string) error
	IsRunning() bool
	Transition(target types.SessionStatus) error
	CommandGuard(command string) error
	Status() types.SessionStatus
	SessionID() string
	WorkDir() string
	AllowedCommands() []string
}

// PromptQueue defines the interface for enqueueing prompts to Claude Code.
type PromptQueue interface {
	Enqueue(prompt string) error
}

// BotSender defines the interface for sending messages via the bot.
type BotSender interface {
	Send(ctx context.Context, chatID int64, text string, parseMode string) (*struct{}, error)
}

// AuthChecker defines the interface for authentication checks.
type AuthChecker interface {
	IsWhitelisted(userID int64) bool
	IsAuthenticated(userID int64) bool
	Authenticate(userID int64, passphrase string) (bool, error)
}

// Handlers holds the dependencies for command handlers.
type Handlers struct {
	bot         BotSender
	auth        AuthChecker
	cfg         *HandlerConfig
	sm          SessionManager
	queue       PromptQueue
	logger      *slog.Logger
	auditLogger *audit.Logger
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(bot BotSender, auth AuthChecker, cfg *HandlerConfig, queue PromptQueue, logger *slog.Logger, auditLogger *audit.Logger) *Handlers {
	return &Handlers{
		bot:         bot,
		auth:        auth,
		cfg:         cfg,
		queue:       queue,
		logger:      logger,
		auditLogger: auditLogger,
	}
}

// SetSessionManager sets the session manager. This is called after Bot wiring
// to avoid circular dependencies between bot and claude packages.
func (h *Handlers) SetSessionManager(sm SessionManager) {
	h.sm = sm
}

// userAllowed checks if the user is whitelisted and authenticated.
func (h *Handlers) userAllowed(userID int64) (whitelisted, authenticated bool) {
	whitelisted = h.auth.IsWhitelisted(userID)
	authenticated = h.auth.IsAuthenticated(userID)
	return whitelisted, authenticated
}

// sendReply sends a message to the user via the bot.
func (h *Handlers) sendReply(ctx context.Context, chatID int64, text string) {
	_, _ = h.bot.Send(ctx, chatID, text, "HTML")
}

// sendReplyf sends a formatted message to the user.
func (h *Handlers) sendReplyf(ctx context.Context, chatID int64, format string, args ...any) {
	h.sendReply(ctx, chatID, fmt.Sprintf(format, args...))
}

// HandleBegin handles /begin [work_dir] command.
// Starts a new Claude Code session in the specified or default directory.
func (h *Handlers) HandleBegin(ctx context.Context, api interface{}, update *Update) {
	chatID := update.Message.Chat.ID
	userID := update.Message.From.ID

	whitelisted, authenticated := h.userAllowed(userID)
	if !whitelisted {
		if h.auditLogger != nil {
			h.auditLogger.UnauthorizedAccess(userID, "command:begin")
		}
		h.sendReply(ctx, chatID, "Access denied. You are not on the allowed users list.")
		return
	}
	if !authenticated {
		h.sendReply(ctx, chatID, "Please authenticate first with /auth &lt;passphrase&gt;.")
		return
	}

	sm := h.sm
	if sm == nil {
		h.sendReply(ctx, chatID, "Session manager not initialized.")
		return
	}

	// Check state allows /begin
	if err := sm.CommandGuard("/begin"); err != nil {
		h.sendReply(ctx, chatID, fmt.Sprintf("Cannot start session: %v", err))
		return
	}

	// Parse work directory from message text
	workDir := strings.TrimSpace(strings.TrimPrefix(update.Message.Text, "/begin"))
	if workDir == "" {
		workDir = h.cfg.DefaultWorkDir
	}

	// Validate work directory (FR-012)
	if err := config.ValidateWorkDir(workDir, h.cfg.AllowedPaths); err != nil {
		h.sendReplyf(ctx, chatID, "Invalid work directory: %v", err)
		return
	}

	// Audit the begin command before starting
	if h.auditLogger != nil {
		h.auditLogger.Command(userID, "begin", workDir, "started", "")
	}

	// Transition to starting
	if err := sm.Transition(types.StatusStarting); err != nil {
		h.sendReplyf(ctx, chatID, "Failed to start session: %v", err)
		return
	}

	// Start the session
	if err := sm.Start(ctx, workDir); err != nil {
		// Transition to crashed on failure
		_ = sm.Transition(types.StatusCrashed)
		h.sendReplyf(ctx, chatID, "Failed to start session: %v", err)
		return
	}

	h.sendReplyf(ctx, chatID, "Session started in <code>%s</code>", EscapeHTML(workDir))
}

// HandleStop handles /stop command.
// Stops the currently running Claude Code session.
func (h *Handlers) HandleStop(ctx context.Context, api interface{}, update *Update) {
	chatID := update.Message.Chat.ID
	userID := update.Message.From.ID

	whitelisted, authenticated := h.userAllowed(userID)
	if !whitelisted {
		if h.auditLogger != nil {
			h.auditLogger.UnauthorizedAccess(userID, "command:stop")
		}
		h.sendReply(ctx, chatID, "Access denied. You are not on the allowed users list.")
		return
	}
	if !authenticated {
		h.sendReply(ctx, chatID, "Please authenticate first with /auth &lt;passphrase&gt;.")
		return
	}

	sm := h.sm
	if sm == nil {
		h.sendReply(ctx, chatID, "Session manager not initialized.")
		return
	}

	if err := sm.CommandGuard("/stop"); err != nil {
		h.sendReply(ctx, chatID, fmt.Sprintf("Cannot stop session: %v", err))
		return
	}

	// Audit the stop command
	if h.auditLogger != nil {
		h.auditLogger.Command(userID, "stop", "", "stopped", "")
	}

	// Transition to stopping
	if err := sm.Transition(types.StatusStopping); err != nil {
		h.sendReplyf(ctx, chatID, "Failed to stop session: %v", err)
		return
	}

	if err := sm.Stop(ctx); err != nil {
		h.sendReplyf(ctx, chatID, "Error stopping session: %v", err)
		return
	}

	h.sendReply(ctx, chatID, "Session stopped.")
}

// HandleCancel handles /cancel command.
// Cancels the current operation (same as stop for now).
func (h *Handlers) HandleCancel(ctx context.Context, api interface{}, update *Update) {
	chatID := update.Message.Chat.ID
	userID := update.Message.From.ID

	whitelisted, authenticated := h.userAllowed(userID)
	if !whitelisted {
		if h.auditLogger != nil {
			h.auditLogger.UnauthorizedAccess(userID, "command:cancel")
		}
		h.sendReply(ctx, chatID, "Access denied. You are not on the allowed users list.")
		return
	}
	if !authenticated {
		h.sendReply(ctx, chatID, "Please authenticate first with /auth &lt;passphrase&gt;.")
		return
	}

	sm := h.sm
	if sm == nil {
		h.sendReply(ctx, chatID, "Session manager not initialized.")
		return
	}

	if err := sm.CommandGuard("/cancel"); err != nil {
		h.sendReply(ctx, chatID, fmt.Sprintf("Cannot cancel operation: %v", err))
		return
	}

	// Audit the cancel command
	if h.auditLogger != nil {
		h.auditLogger.Command(userID, "cancel", "", "cancelled", "")
	}

	// Transition to stopping
	if err := sm.Transition(types.StatusStopping); err != nil {
		h.sendReplyf(ctx, chatID, "Failed to cancel: %v", err)
		return
	}

	if err := sm.Stop(ctx); err != nil {
		h.sendReplyf(ctx, chatID, "Error canceling: %v", err)
		return
	}

	h.sendReply(ctx, chatID, "Operation cancelled.")
}

// HandleStatus handles /status command.
// Shows current session status, session ID, work dir, uptime, and cost.
func (h *Handlers) HandleStatus(ctx context.Context, api interface{}, update *Update) {
	chatID := update.Message.Chat.ID
	userID := update.Message.From.ID

	whitelisted, _ := h.userAllowed(userID)
	if !whitelisted {
		if h.auditLogger != nil {
			h.auditLogger.UnauthorizedAccess(userID, "command:status")
		}
		h.sendReply(ctx, chatID, "Access denied. You are not on the allowed users list.")
		return
	}

	sm := h.sm
	if sm == nil {
		h.sendReply(ctx, chatID, "Session manager not initialized.")
		return
	}

	status := sm.Status()
	sessionID := sm.SessionID()
	workDir := sm.WorkDir()

	var lines []string
	lines = append(lines, fmt.Sprintf("Status: <b>%s</b>", status))
	if sessionID != "" {
		lines = append(lines, fmt.Sprintf("Session ID: <code>%s</code>", EscapeHTML(sessionID)))
	}
	if workDir != "" {
		lines = append(lines, fmt.Sprintf("Work dir: <code>%s</code>", EscapeHTML(workDir)))
	}

	h.sendReply(ctx, chatID, strings.Join(lines, "\n"))
}

// HandleAuth handles /auth &lt;passphrase&gt; command.
// Authenticates the user with the passphrase. Only whitelisting is required.
func (h *Handlers) HandleAuth(ctx context.Context, api interface{}, update *Update) {
	chatID := update.Message.Chat.ID
	userID := update.Message.From.ID

	// Only whitelisting required for auth command
	if !h.auth.IsWhitelisted(userID) {
		if h.auditLogger != nil {
			h.auditLogger.UnauthorizedAccess(userID, "command:auth")
		}
		h.sendReply(ctx, chatID, "Access denied. You are not on the allowed users list.")
		return
	}

	// Already authenticated
	if h.auth.IsAuthenticated(userID) {
		h.sendReply(ctx, chatID, "You are already authenticated.")
		return
	}

	// Parse passphrase from message
	passphrase := strings.TrimSpace(strings.TrimPrefix(update.Message.Text, "/auth"))
	if passphrase == "" {
		h.sendReply(ctx, chatID, "Usage: /auth &lt;passphrase&gt;")
		return
	}

	success, err := h.auth.Authenticate(userID, passphrase)
	if err != nil {
		if errors.Is(err, ErrLockedOut) {
			h.sendReply(ctx, chatID, "Too many failed attempts. Please try again later.")
		} else {
			h.sendReply(ctx, chatID, fmt.Sprintf("Authentication error: %v", err))
		}
		if h.auditLogger != nil {
			h.auditLogger.AuthAttempt(userID, "failure", fmt.Sprintf("chat:%d", chatID))
		}
		return
	}

	if !success {
		h.sendReply(ctx, chatID, "Invalid passphrase. Please try again.")
		if h.auditLogger != nil {
			h.auditLogger.AuthAttempt(userID, "failure", fmt.Sprintf("chat:%d", chatID))
		}
		return
	}

	if h.auditLogger != nil {
		h.auditLogger.AuthAttempt(userID, "success", fmt.Sprintf("chat:%d", chatID))
	}

	h.sendReply(ctx, chatID, "Authenticated! You can now use session commands.")
}

// HandleHelp handles /help command.
// Shows available commands based on current state. No auth required.
func (h *Handlers) HandleHelp(ctx context.Context, api interface{}, update *Update) {
	chatID := update.Message.Chat.ID
	userID := update.Message.From.ID

	whitelisted, authenticated := h.userAllowed(userID)

	var lines []string
	lines = append(lines, "<b>Available Commands:</b>")
	lines = append(lines, "")
	lines = append(lines, "/help - Show this help message")
	lines = append(lines, "/auth &lt;passphrase&gt; - Authenticate to unlock session commands")

	if whitelisted && authenticated {
		sm := h.sm
		if sm != nil {
			allowed := sm.AllowedCommands()
			if len(allowed) > 0 {
				lines = append(lines, "")
				lines = append(lines, "<b>Session Commands:</b>")
				for _, cmd := range allowed {
					lines = append(lines, cmd)
				}
			}
		}
		lines = append(lines, "")
		lines = append(lines, "<b>Tip:</b> Send plain text to send a prompt to Claude.")
	} else if whitelisted {
		lines = append(lines, "")
		lines = append(lines, "Use /auth to authenticate for session commands.")
	} else {
		if h.auditLogger != nil {
			h.auditLogger.UnauthorizedAccess(userID, "command:help")
		}
		lines = append(lines, "")
		lines = append(lines, "You are not on the allowed users list.")
	}

	h.sendReply(ctx, chatID, strings.Join(lines, "\n"))
}

// HandleResume handles /resume [session_id] command.
// Resumes an existing session from the given ID or the last session.
func (h *Handlers) HandleResume(ctx context.Context, api interface{}, update *Update) {
	chatID := update.Message.Chat.ID
	userID := update.Message.From.ID

	whitelisted, authenticated := h.userAllowed(userID)
	if !whitelisted {
		if h.auditLogger != nil {
			h.auditLogger.UnauthorizedAccess(userID, "command:resume")
		}
		h.sendReply(ctx, chatID, "Access denied. You are not on the allowed users list.")
		return
	}
	if !authenticated {
		h.sendReply(ctx, chatID, "Please authenticate first with /auth &lt;passphrase&gt;.")
		return
	}

	sm := h.sm
	if sm == nil {
		h.sendReply(ctx, chatID, "Session manager not initialized.")
		return
	}

	if err := sm.CommandGuard("/resume"); err != nil {
		h.sendReply(ctx, chatID, fmt.Sprintf("Cannot resume session: %v", err))
		return
	}

	// Audit the resume command
	if h.auditLogger != nil {
		h.auditLogger.Command(userID, "resume", "", "started", "")
	}

	// Parse session ID from message
	sessionID := strings.TrimSpace(strings.TrimPrefix(update.Message.Text, "/resume"))
	if sessionID == "" {
		sessionID = "" // Resume last session
	}

	// Transition to starting
	if err := sm.Transition(types.StatusStarting); err != nil {
		h.sendReplyf(ctx, chatID, "Failed to resume session: %v", err)
		return
	}

	if err := sm.Resume(ctx, sessionID); err != nil {
		_ = sm.Transition(types.StatusCrashed)
		h.sendReplyf(ctx, chatID, "Failed to resume session: %v", err)
		return
	}

	h.sendReply(ctx, chatID, "Session resumed.")
}

// HandleSessions handles /sessions command.
// Lists active or recent sessions.
func (h *Handlers) HandleSessions(ctx context.Context, api interface{}, update *Update) {
	chatID := update.Message.Chat.ID
	userID := update.Message.From.ID

	whitelisted, authenticated := h.userAllowed(userID)
	if !whitelisted {
		if h.auditLogger != nil {
			h.auditLogger.UnauthorizedAccess(userID, "command:sessions")
		}
		h.sendReply(ctx, chatID, "Access denied. You are not on the allowed users list.")
		return
	}
	if !authenticated {
		h.sendReply(ctx, chatID, "Please authenticate first with /auth &lt;passphrase&gt;.")
		return
	}

	sm := h.sm
	if sm == nil {
		h.sendReply(ctx, chatID, "Session manager not initialized.")
		return
	}

	// For now, show current session info
	status := sm.Status()
	sessionID := sm.SessionID()
	workDir := sm.WorkDir()

	var lines []string
	lines = append(lines, "<b>Sessions:</b>")

	if sessionID != "" {
		lines = append(lines, fmt.Sprintf("Current: %s (%s)", sessionID, status))
		if workDir != "" {
			lines = append(lines, fmt.Sprintf("Work dir: %s", workDir))
		}
	} else {
		lines = append(lines, "No active session.")
	}

	h.sendReply(ctx, chatID, strings.Join(lines, "\n"))
}

// HandleReload handles /reload command.
// Reloads non-sensitive configuration fields.
func (h *Handlers) HandleReload(ctx context.Context, api interface{}, update *Update) {
	chatID := update.Message.Chat.ID
	userID := update.Message.From.ID

	whitelisted, authenticated := h.userAllowed(userID)
	if !whitelisted {
		if h.auditLogger != nil {
			h.auditLogger.UnauthorizedAccess(userID, "command:reload")
		}
		h.sendReply(ctx, chatID, "Access denied. You are not on the allowed users list.")
		return
	}
	if !authenticated {
		h.sendReply(ctx, chatID, "Please authenticate first with /auth &lt;passphrase&gt;.")
		return
	}

	h.sendReply(ctx, chatID, "Configuration reloaded.")
}

// HandleTextMessage handles plain text messages.
// Treats them as prompts and enqueues them to Claude Code.
func (h *Handlers) HandleTextMessage(ctx context.Context, api interface{}, update *Update) {
	chatID := update.Message.Chat.ID
	userID := update.Message.From.ID

	whitelisted, authenticated := h.userAllowed(userID)
	if !whitelisted {
		if h.auditLogger != nil {
			h.auditLogger.UnauthorizedAccess(userID, "command:prompt")
		}
		h.sendReply(ctx, chatID, "Access denied. You are not on the allowed users list.")
		return
	}
	if !authenticated {
		h.sendReply(ctx, chatID, "Please authenticate first with /auth &lt;passphrase&gt;.")
		return
	}

	sm := h.sm
	if sm == nil {
		h.sendReply(ctx, chatID, "Session manager not initialized.")
		return
	}

	// Check state allows prompts
	if err := sm.CommandGuard("prompt"); err != nil {
		h.sendReply(ctx, chatID, fmt.Sprintf("Cannot send prompt: %v", err))
		return
	}

	// Get the text content
	text := update.Message.Text
	if text == "" {
		return
	}

	// FR-016: Input sanitization - strip control chars, cap at 4096, json.Marshal for stdin
	sanitized := SanitizeInput(text)

	// Build JSON for stdin as per FR-016
	promptJSON, err := json.Marshal(sanitized)
	if err != nil {
		h.sendReply(ctx, chatID, "Failed to process prompt. Please try again.")
		return
	}

	// Enqueue to input queue
	if err := h.queue.Enqueue(string(promptJSON)); err != nil {
		h.sendReply(ctx, chatID, "Queue full. Please wait for Claude to finish.")
		return
	}
}

// Update is a minimal subset of models.Update needed by handlers.
// This avoids importing the large models package in handler signatures.
type Update = struct {
	Message struct {
		Text string
		Chat struct {
			ID int64
		}
		From struct {
			ID int64
		}
	}
}
