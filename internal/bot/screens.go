package bot

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/decko/craudinei/internal/types"
	"github.com/go-telegram/bot/models"
)

// SessionInfo holds the information needed to display a session in the sessions list.
type SessionInfo struct {
	SessionID string
	Status    types.SessionStatus
	WorkDir   string
}

// ScreenBuilder creates rich UI screens for the Telegram bot.
// Each screen is an HTML-formatted message with optional inline keyboard markup.
type ScreenBuilder struct {
	chatID int64
	bot    *Bot
	logger *slog.Logger
}

// NewScreenBuilder creates a new ScreenBuilder for the given chat.
func NewScreenBuilder(bot *Bot, chatID int64, logger *slog.Logger) *ScreenBuilder {
	return &ScreenBuilder{
		chatID: chatID,
		bot:    bot,
		logger: logger,
	}
}

// WelcomeScreen returns the welcome screen message and inline keyboard shown to unauthenticated users.
func WelcomeScreen() (string, [][]models.InlineKeyboardButton) {
	text := "<b>Welcome to Craudinei Bot!</b>\n\n" +
		"This bot allows you to interact with Claude Code sessions.\n\n" +
		"<b>Getting Started:</b>\n" +
		"1. Use /auth &lt;passphrase&gt; to authenticate\n" +
		"2. Use /begin to start a new session\n\n" +
		"For help, use /help."

	keyboard := [][]models.InlineKeyboardButton{
		{
			{Text: "🔑 Authenticate", CallbackData: "n:auth"},
			{Text: "❓ Help", CallbackData: "n:help"},
		},
	}

	return text, keyboard
}

// HomeScreen returns the post-authentication home screen message and inline keyboard.
// Shows available commands and current session status.
func HomeScreen(status types.SessionStatus, workDir string) (string, [][]models.InlineKeyboardButton) {
	statusLine := "<b>Status:</b> " + EscapeHTML(string(status))
	if workDir != "" {
		statusLine += " | <b>Dir:</b> <code>" + EscapeHTML(workDir) + "</code>"
	}

	text := "<b>🏠 Home</b>\n\n" +
		statusLine + "\n\n" +
		"<b>Commands:</b>\n" +
		"/begin [dir] — Start a new session\n" +
		"/stop — Stop current session\n" +
		"/status — Show session status\n" +
		"/sessions — List sessions\n" +
		"/resume [id] — Resume a session\n" +
		"/help — Show help\n\n" +
		"Send plain text to send a prompt to Claude."

	keyboard := [][]models.InlineKeyboardButton{
		{
			{Text: "🚀 Begin Session", CallbackData: "n:begin"},
			{Text: "📋 Sessions", CallbackData: "n:sessions"},
		},
		{
			{Text: "📊 Status", CallbackData: "n:status"},
			{Text: "❓ Help", CallbackData: "n:help"},
		},
	}

	return text, keyboard
}

// DirectoryPickerScreen returns a directory picker screen with inline keyboard.
// Shows allowed directories as buttons. The keyboard row count matches the number
// of directories, with up to 3 buttons per row.
// It also returns a map from button index to directory path for the dispatcher.
func DirectoryPickerScreen(dirs []string) (string, [][]models.InlineKeyboardButton, map[int]string) {
	// Build index-to-dir mapping for the dispatcher
	indexToDir := make(map[int]string, len(dirs))
	for i, dir := range dirs {
		indexToDir[i] = dir
	}

	var rows [][]models.InlineKeyboardButton

	// Build rows with up to 3 buttons each
	for i := 0; i < len(dirs); i++ {
		// Start a new row every 3 buttons
		if i%3 == 0 {
			rows = append(rows, []models.InlineKeyboardButton{})
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], models.InlineKeyboardButton{
			Text:         EscapeHTML(dirs[i]),
			CallbackData: "c:begin:" + strconv.Itoa(i),
		})
	}

	msg := "<b>Select Working Directory</b>\n\nChoose a directory for the session:"

	return msg, rows, indexToDir
}

// SessionsListScreen returns a sessions list screen with inline keyboard for navigation.
func SessionsListScreen(sessions []SessionInfo) (string, [][]models.InlineKeyboardButton) {
	var lines []string
	lines = append(lines, "<b>Sessions</b>\n")

	if len(sessions) == 0 {
		lines = append(lines, "No sessions found.")
	} else {
		for _, s := range sessions {
			lines = append(lines, fmt.Sprintf("%s — <b>%s</b>",
				EscapeHTML(s.SessionID),
				EscapeHTML(string(s.Status))))
			if s.WorkDir != "" {
				lines = append(lines, "  "+EscapeHTML(s.WorkDir))
			}
		}
	}

	// Navigation row
	rows := [][]models.InlineKeyboardButton{
		{
			{Text: "🔄 Refresh", CallbackData: "n:sessions"},
			{Text: "🏠 Home", CallbackData: "n:home"},
		},
	}

	return strings.Join(lines, "\n"), rows
}

// SessionStatusBar returns a compact status bar for the active session.
func SessionStatusBar(status types.SessionStatus, sessionID, workDir string, cost float64, turns int) string {
	lines := []string{
		fmt.Sprintf("<b>Session:</b> %s", EscapeHTML(sessionID)),
		fmt.Sprintf("<b>Status:</b> %s", EscapeHTML(string(status))),
	}
	if workDir != "" {
		lines = append(lines, fmt.Sprintf("<b>Dir:</b> <code>%s</code>", EscapeHTML(workDir)))
	}
	if cost > 0 {
		lines = append(lines, fmt.Sprintf("<b>Cost:</b> $%.4f", cost))
	}
	if turns > 0 {
		lines = append(lines, fmt.Sprintf("<b>Turns:</b> %d", turns))
	}
	return strings.Join(lines, " | ")
}

// ApprovalCard returns an approval card for a tool call with inline keyboard.
// The keyboard has approve/deny/snooze buttons with callback data prefixed by action.
// The requestID is included in the callback data to route to the correct pending request.
func ApprovalCard(toolName string, input json.RawMessage, requestID string) (string, [][]models.InlineKeyboardButton) {
	// Format the tool input for display
	var inputPretty string
	if len(input) > 0 {
		var m map[string]any
		if err := json.Unmarshal(input, &m); err == nil {
			data, _ := json.MarshalIndent(m, "", "  ")
			inputPretty = string(data)
		} else {
			inputPretty = string(input)
		}
	}

	// Truncate very long input for display
	if len(inputPretty) > 500 {
		inputPretty = inputPretty[:500] + "\n... (truncated)"
	}

	text := fmt.Sprintf(
		"<b>⚠️ Approval Required</b>\n\n<b>Tool:</b> <code>%s</code>\n\n<b>Input:</b>\n<pre>%s</pre>",
		EscapeHTML(toolName),
		EscapeHTML(inputPretty),
	)

	keyboard := [][]models.InlineKeyboardButton{
		{
			{Text: "✅ Approve", CallbackData: "a:" + requestID},
			{Text: "❌ Deny", CallbackData: "d:" + requestID},
			{Text: "⏰ Snooze", CallbackData: "s:" + requestID},
		},
	}

	return text, keyboard
}
