// Package router provides NDJSON event routing with classification.
package router

import (
	"sync"
	"testing"

	"github.com/decko/craudinei/internal/types"
)

func TestClassifyEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		event      types.Event
		wantAction Action
		wantText   string
	}{
		{
			name:       "system event",
			event:      types.Event{Type: types.EventSystem},
			wantAction: ActionSystem,
			wantText:   "",
		},
		{
			name:       "assistant text event",
			event:      makeTextEvent("hello world"),
			wantAction: ActionText,
			wantText:   "hello world",
		},
		{
			name:       "assistant thinking event",
			event:      types.Event{Type: types.EventAssistant, Subtype: "thinking"},
			wantAction: ActionThinking,
			wantText:   "",
		},
		{
			name:       "assistant tool_use event",
			event:      types.Event{Type: types.EventAssistant, Subtype: "tool_use"},
			wantAction: ActionToolUse,
			wantText:   "",
		},
		{
			name:       "result success event",
			event:      types.Event{Type: types.EventResult, Subtype: "success", Result: "done"},
			wantAction: ActionResult,
			wantText:   "done",
		},
		{
			name:       "result error event with subtype",
			event:      types.Event{Type: types.EventResult, Subtype: "error", Result: "failed", IsError: true},
			wantAction: ActionResult,
			wantText:   "failed",
		},
		{
			name:       "result error event with IsError flag",
			event:      types.Event{Type: types.EventResult, Result: "failed", IsError: true},
			wantAction: ActionResult,
			wantText:   "failed",
		},
		{
			name:       "rate_limit event",
			event:      types.Event{Type: types.EventRateLimit},
			wantAction: ActionRateLimit,
			wantText:   "",
		},
		{
			name:       "assistant api_retry event",
			event:      types.Event{Type: types.EventAssistant, Subtype: "api_retry"},
			wantAction: ActionAPIRetry,
			wantText:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyEvent(tt.event)
			if got.Action != tt.wantAction {
				t.Errorf("Action = %v, want %v", got.Action, tt.wantAction)
			}
			if got.Text != tt.wantText {
				t.Errorf("Text = %v, want %v", got.Text, tt.wantText)
			}
		})
	}
}

func TestClassifyEvent_UnknownType(t *testing.T) {
	t.Parallel()

	event := types.Event{Type: types.EventUser}
	got := ClassifyEvent(event)

	if got.Action != ActionError {
		t.Errorf("Action = %v, want %v", got.Action, ActionError)
	}
	if got.Text != "unknown event type" {
		t.Errorf("Text = %v, want %v", got.Text, "unknown event type")
	}
}

func TestExtractText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event types.Event
		want  string
	}{
		{
			name:  "single text block",
			event: makeTextEvent("hello"),
			want:  "hello",
		},
		{
			name:  "multiple text blocks",
			event: makeMultiTextEvent("hello", " world"),
			want:  "hello world",
		},
		{
			name:  "mixed content blocks",
			event: makeMixedContentEvent("visible text"),
			want:  "visible text",
		},
		{
			name:  "empty content blocks",
			event: types.Event{Type: types.EventAssistant},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractText(tt.event)
			if got != tt.want {
				t.Errorf("ExtractText() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractText_EmptyContent(t *testing.T) {
	t.Parallel()

	event := types.Event{
		Type: types.EventAssistant,
		Message: &types.Message{
			Content: []types.ContentBlock{},
		},
	}
	got := ExtractText(event)
	if got != "" {
		t.Errorf("ExtractText() = %v, want empty string", got)
	}
}

func TestFeed_NDJSON(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var events []ClassifiedEvent

	handler := func(ce ClassifiedEvent) {
		mu.Lock()
		events = append(events, ce)
		mu.Unlock()
	}

	router := NewRouter(handler)

	ndjson := `{"type":"system"}
{"type":"assistant","subtype":"text","message":{"content":[{"type":"text","text":"hello"}]}}
{"type":"result","subtype":"success","result":"done"}` + "\n"

	err := router.Feed([]byte(ndjson))
	if err != nil {
		t.Fatalf("Feed() error = %v", err)
	}

	mu.Lock()
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Action != ActionSystem {
		t.Errorf("event 0 Action = %v, want %v", events[0].Action, ActionSystem)
	}
	if events[1].Action != ActionText {
		t.Errorf("event 1 Action = %v, want %v", events[1].Action, ActionText)
	}
	if events[1].Text != "hello" {
		t.Errorf("event 1 Text = %v, want %v", events[1].Text, "hello")
	}
	if events[2].Action != ActionResult {
		t.Errorf("event 2 Action = %v, want %v", events[2].Action, ActionResult)
	}
	mu.Unlock()
}

func TestFeed_MalformedLine(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var count int

	handler := func(ce ClassifiedEvent) {
		mu.Lock()
		count++
		mu.Unlock()
	}

	router := NewRouter(handler)

	data := `{"type":"system"}
not valid json at all
{"type":"assistant","subtype":"text","message":{"content":[{"type":"text","text":"hello"}]}}` + "\n"

	err := router.Feed([]byte(data))
	if err != nil {
		t.Fatalf("Feed() error = %v", err)
	}

	mu.Lock()
	if count != 2 {
		t.Errorf("expected 2 events processed, got %d", count)
	}
	mu.Unlock()
}

func TestFeed_EmptyLine(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var count int

	handler := func(ce ClassifiedEvent) {
		mu.Lock()
		count++
		mu.Unlock()
	}

	router := NewRouter(handler)

	data := `{"type":"system"}

{"type":"assistant","subtype":"text","message":{"content":[{"type":"text","text":"hello"}]}}
` + "\n"

	err := router.Feed([]byte(data))
	if err != nil {
		t.Fatalf("Feed() error = %v", err)
	}

	mu.Lock()
	if count != 2 {
		t.Errorf("expected 2 events processed, got %d", count)
	}
	mu.Unlock()
}

func TestFeed_ClosedRouter(t *testing.T) {
	t.Parallel()

	handler := func(ce ClassifiedEvent) {}
	router := NewRouter(handler)

	router.Close()

	err := router.Feed([]byte(`{"type":"system"}` + "\n"))
	if err == nil {
		t.Fatalf("expected error when feeding closed router, got nil")
	}
	if err.Error() != "router: feed on closed router" {
		t.Errorf("error = %v, want %v", err, "router: feed on closed router")
	}
}

func TestFeed_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	handler := func(ce ClassifiedEvent) {}

	router := NewRouter(handler)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				data := []byte(`{"type":"system"}` + "\n")
				_ = router.Feed(data)
			}
		}()
	}

	wg.Wait()
}

func makeTextEvent(text string) types.Event {
	return types.Event{
		Type:    types.EventAssistant,
		Subtype: "text",
		Message: &types.Message{
			Content: []types.ContentBlock{
				{Type: "text", Text: text},
			},
		},
	}
}

func makeMultiTextEvent(parts ...string) types.Event {
	blocks := make([]types.ContentBlock, len(parts))
	for i, p := range parts {
		blocks[i] = types.ContentBlock{Type: "text", Text: p}
	}
	return types.Event{
		Type:    types.EventAssistant,
		Subtype: "text",
		Message: &types.Message{
			Content: blocks,
		},
	}
}

func makeMixedContentEvent(text string) types.Event {
	return types.Event{
		Type:    types.EventAssistant,
		Subtype: "text",
		Message: &types.Message{
			Content: []types.ContentBlock{
				{Type: "text", Text: text},
				{Type: "tool_use", Name: "bash"},
			},
		},
	}
}
