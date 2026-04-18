// Package router provides NDJSON event routing with classification.
package router

import "github.com/decko/craudinei/internal/types"

// Action represents the action to take for a classified event.
type Action string

// Action constants define the possible actions for classified events.
const (
	ActionText      Action = "text"       // assistant text output to display
	ActionToolUse   Action = "tool_use"   // tool needs approval or auto-approved
	ActionResult    Action = "result"     // tool result (success or error)
	ActionThinking  Action = "thinking"   // assistant thinking (silent, don't display)
	ActionSystem    Action = "system"     // system message (init, etc.)
	ActionRateLimit Action = "rate_limit" // rate limit hit
	ActionAPIRetry  Action = "api_retry"  // API retry with countdown
	ActionError     Action = "error"      // error event
)

// ClassifiedEvent represents an event that has been classified for handling.
type ClassifiedEvent struct {
	Action Action      // the action to take
	Event  types.Event // the original event
	Text   string      // extracted text for display
}
