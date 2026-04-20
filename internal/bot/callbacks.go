package bot

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
)

// CallbackResult represents the outcome of dispatching a callback query.
// AnswerText is shown briefly to the user; Action and Target drive caller behavior.
type CallbackResult struct {
	AnswerText string // text shown briefly to user
	Action     string // "approve", "deny", "snooze", "navigate", "command"
	Target     string // request ID, "home", "sessions", or directory index
}

// CallbackDispatcher routes Telegram callback queries to the appropriate handler.
type CallbackDispatcher struct {
	approvalHandler interface {
		HandleCallback(callbackData string, decision string)
	}
	logger  *slog.Logger
	dirMaps map[int64]map[int]string // userID -> index -> directory path
	mu      sync.RWMutex
}

// NewCallbackDispatcher creates a new callback dispatcher.
func NewCallbackDispatcher(
	approvalHandler interface {
		HandleCallback(callbackData string, decision string)
	},
	logger *slog.Logger,
) *CallbackDispatcher {
	return &CallbackDispatcher{
		approvalHandler: approvalHandler,
		logger:          logger,
		dirMaps:         make(map[int64]map[int]string),
	}
}

// SetDirMap stores a directory map for a user. Call this when showing the
// directory picker screen so the dispatcher can resolve indices later.
func (d *CallbackDispatcher) SetDirMap(userID int64, dirMap map[int]string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dirMaps[userID] = dirMap
}

// GetDirMap returns the stored directory map for a user.
func (d *CallbackDispatcher) GetDirMap(userID int64) map[int]string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.dirMaps[userID]
}

// Dispatch parses the callback data and routes to the appropriate handler.
// Returns a CallbackResult with the text to display and the action to take.
func (d *CallbackDispatcher) Dispatch(ctx context.Context, callbackData string) CallbackResult {
	if len(callbackData) < 3 || !strings.Contains(callbackData, ":") {
		return CallbackResult{AnswerText: "Unknown action", Action: "error", Target: ""}
	}

	// Parse prefix: first char(s) before the colon (e.g., "a", "n", "c")
	colonIdx := strings.Index(callbackData, ":")
	if colonIdx < 1 {
		return CallbackResult{AnswerText: "Invalid callback format", Action: "error", Target: ""}
	}

	prefix := callbackData[:colonIdx]
	payload := callbackData[colonIdx+1:]

	switch prefix {
	case "a":
		// Approve action
		d.approvalHandler.HandleCallback(payload, "approve")
		return CallbackResult{AnswerText: "✅ Approved", Action: "approve", Target: payload}

	case "d":
		// Deny action
		d.approvalHandler.HandleCallback(payload, "deny")
		return CallbackResult{AnswerText: "❌ Denied", Action: "deny", Target: payload}

	case "s":
		// Snooze action
		d.approvalHandler.HandleCallback(payload, "snooze")
		return CallbackResult{AnswerText: "⏰ Snoozed", Action: "snooze", Target: payload}

	case "n":
		// Navigation actions
		switch payload {
		case "home":
			return CallbackResult{AnswerText: "🏠 Home screen", Action: "navigate", Target: "home"}
		case "sessions":
			return CallbackResult{AnswerText: "📋 Sessions list", Action: "navigate", Target: "sessions"}
		case "auth":
			return CallbackResult{AnswerText: "🔑 Use /auth <passphrase> to authenticate", Action: "navigate", Target: "auth"}
		case "help":
			return CallbackResult{AnswerText: "❓ Help", Action: "navigate", Target: "help"}
		default:
			return CallbackResult{AnswerText: "Unknown navigation target", Action: "error", Target: ""}
		}

	case "c":
		// Command actions
		switch {
		case strings.HasPrefix(payload, "begin:"):
			idxStr := strings.TrimPrefix(payload, "begin:")
			return CallbackResult{AnswerText: "📁 Starting session...", Action: "command", Target: idxStr}
		default:
			return CallbackResult{AnswerText: "Unknown command", Action: "error", Target: ""}
		}

	default:
		return CallbackResult{AnswerText: "Unknown action type", Action: "error", Target: ""}
	}
}

// DispatchWithDirMap is like Dispatch but for directory begin commands where the
// target is a directory index that needs to be resolved via the provided map.
func (d *CallbackDispatcher) DispatchWithDirMap(ctx context.Context, callbackData string, indexToDir map[int]string) CallbackResult {
	result := d.Dispatch(ctx, callbackData)
	// If it's a begin command, resolve the index to a directory path
	if result.Action == "command" && strings.HasPrefix(callbackData, "c:begin:") {
		idx, err := strconv.Atoi(result.Target)
		if err == nil {
			if dir, ok := indexToDir[idx]; ok {
				result.Target = dir
			}
		}
	}
	return result
}
