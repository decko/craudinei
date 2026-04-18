// Package router provides NDJSON event routing with classification.
package router

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/decko/craudinei/internal/types"
)

// Router handles NDJSON event routing with backpressure handling.
type Router struct {
	mu      sync.Mutex
	closed  bool
	handler func(ClassifiedEvent)
	buffer  *bytes.Buffer
}

// NewRouter creates a new Router with the given handler callback.
func NewRouter(handler func(ClassifiedEvent)) *Router {
	return &Router{
		handler: handler,
		buffer:  new(bytes.Buffer),
	}
}

// Feed processes raw NDJSON data, classifying and routing events.
// Each line is treated as a separate JSON event. Malformed lines are logged
// and skipped; valid lines trigger the handler for their classified event.
//
// The handler callback is invoked synchronously while the router's mutex is held.
// The handler MUST NOT call Feed() on the same router, as this will deadlock.
func (r *Router) Feed(data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return errors.New("router: feed on closed router")
	}

	if _, err := r.buffer.Write(data); err != nil {
		return fmt.Errorf("router: write to buffer: %w", err)
	}

	for {
		line, err := r.buffer.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Put back any remaining bytes
				if len(line) > 0 {
					r.buffer.Write(line)
				}
				break
			}
			return fmt.Errorf("router: read from buffer: %w", err)
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var event types.Event
		if err := json.Unmarshal(line, &event); err != nil {
			log.Printf("router: skip malformed line: %v", err)
			continue
		}

		classified := ClassifyEvent(event)
		r.handler(classified)
	}

	return nil
}

// ClassifyEvent classifies an event by type and subtype into an Action.
func ClassifyEvent(event types.Event) ClassifiedEvent {
	var action Action
	var text string

	switch event.Type {
	case types.EventSystem:
		action = ActionSystem

	case types.EventAssistant:
		switch event.Subtype {
		case "text":
			action = ActionText
			text = ExtractText(event)
		case "thinking":
			action = ActionThinking
		case "tool_use":
			action = ActionToolUse
		case "api_retry":
			action = ActionAPIRetry
		default:
			action = ActionError
			text = "unknown assistant subtype"
		}

	case types.EventResult:
		action = ActionResult
		text = event.Result

	case types.EventRateLimit:
		action = ActionRateLimit

	default:
		action = ActionError
		text = "unknown event type"
	}

	return ClassifiedEvent{
		Action: action,
		Event:  event,
		Text:   text,
	}
}

// ExtractText extracts displayable text from event content blocks.
// Only text blocks are included; other block types are skipped.
func ExtractText(event types.Event) string {
	if event.Message == nil {
		return ""
	}

	var sb bytes.Buffer
	for _, block := range event.Message.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return sb.String()
}

// Close marks the router as closed. No more events will be accepted.
func (r *Router) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
}
