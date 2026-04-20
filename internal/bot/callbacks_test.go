package bot

import (
	"context"
	"testing"
)

// fakeApprovalHandler implements the approval handler interface for testing.
type fakeApprovalHandler struct {
	calls []struct {
		callbackData string
		decision     string
	}
}

func (f *fakeApprovalHandler) HandleCallback(callbackData string, decision string) {
	f.calls = append(f.calls, struct {
		callbackData string
		decision     string
	}{callbackData, decision})
}

func TestCallbackDispatchApprove(t *testing.T) {
	t.Helper()

	fake := &fakeApprovalHandler{}
	disp := NewCallbackDispatcher(fake, nil)

	result := disp.Dispatch(context.Background(), "a:req123")

	if result.AnswerText != "✅ Approved" {
		t.Errorf("expected '✅ Approved', got %q", result.AnswerText)
	}
	if result.Action != "approve" {
		t.Errorf("expected action 'approve', got %q", result.Action)
	}
	if result.Target != "req123" {
		t.Errorf("expected target 'req123', got %q", result.Target)
	}

	if len(fake.calls) != 1 {
		t.Errorf("expected 1 call, got %d", len(fake.calls))
	}
	if fake.calls[0].callbackData != "req123" {
		t.Errorf("expected callbackData 'req123', got %q", fake.calls[0].callbackData)
	}
	if fake.calls[0].decision != "approve" {
		t.Errorf("expected decision 'approve', got %q", fake.calls[0].decision)
	}
}

func TestCallbackDispatchDeny(t *testing.T) {
	t.Helper()

	fake := &fakeApprovalHandler{}
	disp := NewCallbackDispatcher(fake, nil)

	result := disp.Dispatch(context.Background(), "d:req456")

	if result.AnswerText != "❌ Denied" {
		t.Errorf("expected '❌ Denied', got %q", result.AnswerText)
	}
	if result.Action != "deny" {
		t.Errorf("expected action 'deny', got %q", result.Action)
	}
	if result.Target != "req456" {
		t.Errorf("expected target 'req456', got %q", result.Target)
	}

	if len(fake.calls) != 1 {
		t.Errorf("expected 1 call, got %d", len(fake.calls))
	}
	if fake.calls[0].callbackData != "req456" {
		t.Errorf("expected callbackData 'req456', got %q", fake.calls[0].callbackData)
	}
	if fake.calls[0].decision != "deny" {
		t.Errorf("expected decision 'deny', got %q", fake.calls[0].decision)
	}
}

func TestCallbackDispatchSnooze(t *testing.T) {
	t.Helper()

	fake := &fakeApprovalHandler{}
	disp := NewCallbackDispatcher(fake, nil)

	result := disp.Dispatch(context.Background(), "s:req789")

	if result.AnswerText != "⏰ Snoozed" {
		t.Errorf("expected '⏰ Snoozed', got %q", result.AnswerText)
	}
	if result.Action != "snooze" {
		t.Errorf("expected action 'snooze', got %q", result.Action)
	}
	if result.Target != "req789" {
		t.Errorf("expected target 'req789', got %q", result.Target)
	}

	if len(fake.calls) != 1 {
		t.Errorf("expected 1 call, got %d", len(fake.calls))
	}
	if fake.calls[0].callbackData != "req789" {
		t.Errorf("expected callbackData 'req789', got %q", fake.calls[0].callbackData)
	}
	if fake.calls[0].decision != "snooze" {
		t.Errorf("expected decision 'snooze', got %q", fake.calls[0].decision)
	}
}

func TestCallbackDispatchNavigate(t *testing.T) {
	t.Helper()

	tests := []struct {
		name   string
		data   string
		want   string
		action string
		target string
	}{
		{name: "home", data: "n:home", want: "🏠 Home screen", action: "navigate", target: "home"},
		{name: "sessions", data: "n:sessions", want: "📋 Sessions list", action: "navigate", target: "sessions"},
		{name: "unknown", data: "n:unknown", want: "Unknown navigation target", action: "error", target: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeApprovalHandler{}
			disp := NewCallbackDispatcher(fake, nil)

			result := disp.Dispatch(context.Background(), tt.data)

			if result.AnswerText != tt.want {
				t.Errorf("expected %q, got %q", tt.want, result.AnswerText)
			}
			if result.Action != tt.action {
				t.Errorf("expected action %q, got %q", tt.action, result.Action)
			}
			if result.Target != tt.target {
				t.Errorf("expected target %q, got %q", tt.target, result.Target)
			}

			// Navigation should not call approval handler
			if len(fake.calls) != 0 {
				t.Errorf("expected 0 approval calls, got %d", len(fake.calls))
			}
		})
	}
}

func TestCallbackDispatchCommand(t *testing.T) {
	t.Helper()

	tests := []struct {
		name   string
		data   string
		want   string
		action string
		target string
	}{
		{name: "begin tmp", data: "c:begin:0", want: "📁 Starting session...", action: "command", target: "0"},
		{name: "begin home", data: "c:begin:5", want: "📁 Starting session...", action: "command", target: "5"},
		{name: "unknown command", data: "c:unknown", want: "Unknown command", action: "error", target: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeApprovalHandler{}
			disp := NewCallbackDispatcher(fake, nil)

			result := disp.Dispatch(context.Background(), tt.data)

			if result.AnswerText != tt.want {
				t.Errorf("expected %q, got %q", tt.want, result.AnswerText)
			}
			if result.Action != tt.action {
				t.Errorf("expected action %q, got %q", tt.action, result.Action)
			}
			if result.Target != tt.target {
				t.Errorf("expected target %q, got %q", tt.target, result.Target)
			}

			// Command should not call approval handler
			if len(fake.calls) != 0 {
				t.Errorf("expected 0 approval calls, got %d", len(fake.calls))
			}
		})
	}
}

func TestCallbackDispatchUnknown(t *testing.T) {
	t.Helper()

	tests := []struct {
		name   string
		data   string
		want   string
		action string
	}{
		{name: "empty string", data: "", want: "Unknown action", action: "error"},
		{name: "no colon", data: "abc", want: "Unknown action", action: "error"},
		{name: "unknown prefix", data: "x:something", want: "Unknown action type", action: "error"},
		{name: "too short", data: "a", want: "Unknown action", action: "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeApprovalHandler{}
			disp := NewCallbackDispatcher(fake, nil)

			result := disp.Dispatch(context.Background(), tt.data)

			if result.AnswerText != tt.want {
				t.Errorf("expected %q, got %q", tt.want, result.AnswerText)
			}
			if result.Action != tt.action {
				t.Errorf("expected action %q, got %q", tt.action, result.Action)
			}
		})
	}
}

func TestCallbackDispatch_ApproveIdempotent(t *testing.T) {
	t.Helper()

	// Test that multiple approve calls for the same request are handled
	fake := &fakeApprovalHandler{}
	disp := NewCallbackDispatcher(fake, nil)

	// First approve
	disp.Dispatch(context.Background(), "a:req123")
	// Second approve (idempotent)
	disp.Dispatch(context.Background(), "a:req123")

	if len(fake.calls) != 2 {
		t.Errorf("expected 2 calls (idempotent), got %d", len(fake.calls))
	}
}
