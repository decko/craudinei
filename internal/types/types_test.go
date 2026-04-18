// Package types provides tests for shared data types.
package types

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestSessionStatusValues(t *testing.T) {
	t.Helper()

	tests := []struct {
		name     string
		status   SessionStatus
		expected string
	}{
		{name: "StatusIdle", status: StatusIdle, expected: "idle"},
		{name: "StatusStarting", status: StatusStarting, expected: "starting"},
		{name: "StatusRunning", status: StatusRunning, expected: "running"},
		{name: "StatusWaitingApproval", status: StatusWaitingApproval, expected: "waiting_approval"},
		{name: "StatusStopping", status: StatusStopping, expected: "stopping"},
		{name: "StatusCrashed", status: StatusCrashed, expected: "crashed"},
	}

	seen := make(map[string]string)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.status.String() != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, tc.status.String())
			}
			if existing := seen[tc.expected]; existing != "" {
				t.Errorf("duplicate status value: %q (first seen in %s)", tc.expected, existing)
			}
			seen[tc.expected] = tc.name
		})
	}

	if len(seen) != 6 {
		t.Errorf("expected 6 unique status constants, got %d", len(seen))
	}
}

func TestEventTypeValues(t *testing.T) {
	t.Helper()

	tests := []struct {
		name      string
		eventType EventType
		expected  string
	}{
		{name: "EventSystem", eventType: EventSystem, expected: "system"},
		{name: "EventAssistant", eventType: EventAssistant, expected: "assistant"},
		{name: "EventUser", eventType: EventUser, expected: "user"},
		{name: "EventResult", eventType: EventResult, expected: "result"},
		{name: "EventRateLimit", eventType: EventRateLimit, expected: "rate_limit"},
	}

	seen := make(map[string]string)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if string(tc.eventType) != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, string(tc.eventType))
			}
			if existing := seen[tc.expected]; existing != "" {
				t.Errorf("duplicate event type value: %q (first seen in %s)", tc.expected, existing)
			}
			seen[tc.expected] = tc.name
		})
	}

	if len(seen) != 5 {
		t.Errorf("expected 5 unique event type constants, got %d", len(seen))
	}
}

func TestContentBlockFiltering(t *testing.T) {
	t.Helper()

	blocks := []ContentBlock{
		{Type: "text", Text: "Hello"},
		{Type: "thinking", Thinking: "I am thinking..."},
		{Type: "text", Text: "World"},
		{Type: "tool_use", Name: "bash", ID: "tool_1"},
		{Type: "thinking", Thinking: "More thinking"},
		{Type: "text", Text: "Final"},
	}

	tests := []struct {
		name      string
		blockType string
		wantLen   int
		wantTypes []string
	}{
		{
			name:      "filter_text",
			blockType: "text",
			wantLen:   3,
			wantTypes: []string{"text", "text", "text"},
		},
		{
			name:      "filter_thinking",
			blockType: "thinking",
			wantLen:   2,
			wantTypes: []string{"thinking", "thinking"},
		},
		{
			name:      "filter_tool_use",
			blockType: "tool_use",
			wantLen:   1,
			wantTypes: []string{"tool_use"},
		},
		{
			name:      "filter_nonexistent",
			blockType: "nonexistent",
			wantLen:   0,
			wantTypes: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FilterContentBlocks(blocks, tc.blockType)
			if len(got) != tc.wantLen {
				t.Errorf("expected %d blocks, got %d", tc.wantLen, len(got))
			}
			for i, block := range got {
				if block.Type != tc.wantTypes[i] {
					t.Errorf("block[%d] type = %q, want %q", i, block.Type, tc.wantTypes[i])
				}
			}
		})
	}
}

func TestSessionStateAccessors(t *testing.T) {
	t.Helper()

	state := NewSessionState()

	t.Run("initial_status", func(t *testing.T) {
		if state.Status() != StatusIdle {
			t.Errorf("expected StatusIdle, got %v", state.Status())
		}
	})

	t.Run("set_status", func(t *testing.T) {
		state.SetStatus(StatusRunning)
		if state.Status() != StatusRunning {
			t.Errorf("expected StatusRunning, got %v", state.Status())
		}
	})

	t.Run("session_id", func(t *testing.T) {
		if state.SessionID() != "" {
			t.Errorf("expected empty string, got %q", state.SessionID())
		}
		state.SetSessionID("sess_123")
		if state.SessionID() != "sess_123" {
			t.Errorf("expected sess_123, got %q", state.SessionID())
		}
	})

	t.Run("workdir", func(t *testing.T) {
		if state.WorkDir() != "" {
			t.Errorf("expected empty string, got %q", state.WorkDir())
		}
		state.StartSession("/tmp/work")
		if state.WorkDir() != "/tmp/work" {
			t.Errorf("expected /tmp/work, got %q", state.WorkDir())
		}
	})

	t.Run("started_at", func(t *testing.T) {
		before := time.Now()
		state.StartSession("/tmp")
		after := time.Now()
		started := state.StartedAt()
		if started.Before(before) || started.After(after) {
			t.Errorf("StartedAt() outside expected range")
		}
	})

	t.Run("last_activity", func(t *testing.T) {
		before := time.Now()
		state.StartSession("/tmp")
		after := time.Now()
		activity := state.LastActivity()
		if activity.Before(before) || activity.After(after) {
			t.Errorf("LastActivity() outside expected range")
		}
	})

	t.Run("touch_activity", func(t *testing.T) {
		initial := state.LastActivity()
		time.Sleep(1 * time.Millisecond)
		state.TouchActivity()
		newActivity := state.LastActivity()
		if !newActivity.After(initial) {
			t.Errorf("LastActivity() not updated after TouchActivity()")
		}
	})

	t.Run("pending_approval", func(t *testing.T) {
		if state.PendingApproval() != nil {
			t.Errorf("expected nil pending approval, got %v", state.PendingApproval())
		}
		tc := &ToolCall{ID: "call_1", Name: "bash", Input: json.RawMessage(`{}`)}
		state.SetPendingApproval(tc)
		pending := state.PendingApproval()
		if pending == nil {
			t.Fatal("expected non-nil pending approval")
		}
		if pending.ID != "call_1" {
			t.Errorf("expected call_1, got %q", pending.ID)
		}
	})

	t.Run("set_cumulative_totals", func(t *testing.T) {
		state.SetCumulativeTotals(5, 1)
		cost, _, _, turns := state.Stats()
		if cost != 5 {
			t.Errorf("expected cost 5, got %f", cost)
		}
		if turns != 1 {
			t.Errorf("expected turns 1, got %d", turns)
		}

		state.SetCumulativeTotals(10, 2)
		cost, _, _, turns = state.Stats()
		if cost != 10 {
			t.Errorf("expected cost 10, got %f", cost)
		}
		if turns != 2 {
			t.Errorf("expected turns 2, got %d", turns)
		}
	})

	t.Run("add_token_usage", func(t *testing.T) {
		state.AddTokenUsage(100, 200)
		_, tokensIn, tokensOut, _ := state.Stats()
		if tokensIn != 100 {
			t.Errorf("expected tokensIn 100, got %d", tokensIn)
		}
		if tokensOut != 200 {
			t.Errorf("expected tokensOut 200, got %d", tokensOut)
		}

		state.AddTokenUsage(150, 300)
		_, tokensIn, tokensOut, _ = state.Stats()
		if tokensIn != 250 {
			t.Errorf("expected tokensIn 250, got %d", tokensIn)
		}
		if tokensOut != 500 {
			t.Errorf("expected tokensOut 500, got %d", tokensOut)
		}
	})
}

func TestSessionStateConcurrent(t *testing.T) {
	t.Helper()

	state := NewSessionState()
	state.StartSession("/tmp/concurrent")

	var wg sync.WaitGroup
	const goroutines = 100

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			state.SetStatus(StatusRunning)
			_ = state.Status()
			state.SetSessionID("sess_concurrent")
			_ = state.SessionID()
			state.TouchActivity()
			_ = state.LastActivity()
			state.SetPendingApproval(&ToolCall{ID: "call"})
			_ = state.PendingApproval()
			state.SetCumulativeTotals(1, 1)
			state.AddTokenUsage(10, 20)
			_, _, _, _ = state.Stats()
		}(i)
	}
	wg.Wait()

	// Note: SetCumulativeTotals uses = so last goroutine wins for cost/turns
	// AddTokenUsage uses += so we get goroutines * 10 and goroutines * 20
	_, tokensIn, tokensOut, _ := state.Stats()
	if tokensIn != 10*goroutines {
		t.Errorf("expected tokensIn %d, got %d", 10*goroutines, tokensIn)
	}
	if tokensOut != 20*goroutines {
		t.Errorf("expected tokensOut %d, got %d", 20*goroutines, tokensOut)
	}
}

func TestSessionStateSetPendingApprovalStatus(t *testing.T) {
	t.Helper()

	state := NewSessionState()
	state.SetStatus(StatusRunning)

	t.Run("sets_waiting_approval_when_non_nil", func(t *testing.T) {
		tc := &ToolCall{ID: "call_1", Name: "bash", Input: json.RawMessage(`{}`)}
		state.SetPendingApproval(tc)
		if state.Status() != StatusWaitingApproval {
			t.Errorf("expected StatusWaitingApproval, got %v", state.Status())
		}
	})

	t.Run("sets_running_when_nil", func(t *testing.T) {
		state.SetPendingApproval(nil)
		if state.Status() != StatusRunning {
			t.Errorf("expected StatusRunning, got %v", state.Status())
		}
	})
}

func TestSessionStateStartSessionResetsCounters(t *testing.T) {
	t.Helper()

	state := NewSessionState()
	state.SetCumulativeTotals(100, 10)
	state.AddTokenUsage(500, 600)

	state.StartSession("/new/workdir")

	cost, tokensIn, tokensOut, turns := state.Stats()
	if cost != 0 {
		t.Errorf("expected cost 0, got %f", cost)
	}
	if tokensIn != 0 {
		t.Errorf("expected tokensIn 0, got %d", tokensIn)
	}
	if tokensOut != 0 {
		t.Errorf("expected tokensOut 0, got %d", tokensOut)
	}
	if turns != 0 {
		t.Errorf("expected turns 0, got %d", turns)
	}
	if state.WorkDir() != "/new/workdir" {
		t.Errorf("expected workdir /new/workdir, got %q", state.WorkDir())
	}
	if state.PendingApproval() != nil {
		t.Errorf("expected nil pending approval, got %v", state.PendingApproval())
	}
}
