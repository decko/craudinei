// Package types provides adversarial tests for shared data types.
package types

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// ─── Zero-Value SessionState ─────────────────────────────────────────────────

func TestSessionStateZeroValue(t *testing.T) {
	t.Run("status_zero_value_is_empty_string", func(t *testing.T) {
		s := &SessionState{}
		if s.Status() != "" {
			t.Errorf("expected empty string, got %q", s.Status())
		}
	})

	t.Run("set_and_get_status", func(t *testing.T) {
		s := &SessionState{}
		s.SetStatus(StatusRunning)
		if s.Status() != StatusRunning {
			t.Errorf("expected StatusRunning, got %v", s.Status())
		}
	})

	t.Run("set_and_get_session_id", func(t *testing.T) {
		s := &SessionState{}
		s.SetSessionID("zero_sess")
		if s.SessionID() != "zero_sess" {
			t.Errorf("expected zero_sess, got %q", s.SessionID())
		}
	})

	t.Run("stats_on_zero_value", func(t *testing.T) {
		s := &SessionState{}
		cost, tokensIn, tokensOut, turns := s.Stats()
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
	})

	t.Run("pending_approval_nil_by_default", func(t *testing.T) {
		s := &SessionState{}
		if s.PendingApproval() != nil {
			t.Errorf("expected nil, got %v", s.PendingApproval())
		}
	})

	t.Run("start_session_on_zero_value", func(t *testing.T) {
		s := &SessionState{}
		s.StartSession("/tmp/zero")
		if s.WorkDir() != "/tmp/zero" {
			t.Errorf("expected /tmp/zero, got %q", s.WorkDir())
		}
		if s.StartedAt().IsZero() {
			t.Errorf("StartedAt should not be zero after StartSession")
		}
	})
}

// ─── SetPendingApproval with nil when StatusRunning ──────────────────────────

func TestSetPendingApprovalNilFromRunning(t *testing.T) {
	s := NewSessionState()
	s.SetStatus(StatusRunning)
	s.SetPendingApproval(nil)
	if s.Status() != StatusRunning {
		t.Errorf("SetPendingApproval(nil) from StatusRunning: expected StatusRunning, got %v", s.Status())
	}
	if s.PendingApproval() != nil {
		t.Errorf("expected nil pending approval, got %v", s.PendingApproval())
	}
}

func TestSetPendingApprovalNilFromOtherStatuses(t *testing.T) {
	statuses := []SessionStatus{StatusIdle, StatusStarting, StatusStopping, StatusCrashed}
	for _, st := range statuses {
		t.Run(string(st), func(t *testing.T) {
			s := NewSessionState()
			s.SetStatus(st)
			s.SetPendingApproval(nil)
			if s.Status() != StatusRunning {
				t.Errorf("from %s: expected StatusRunning, got %v", st, s.Status())
			}
		})
	}
}

func TestSetPendingApprovalNonNilTransitionsToWaitingApproval(t *testing.T) {
	s := NewSessionState()
	s.SetStatus(StatusIdle)
	s.SetPendingApproval(&ToolCall{ID: "call_x"})
	if s.Status() != StatusWaitingApproval {
		t.Errorf("expected StatusWaitingApproval, got %v", s.Status())
	}
	if s.PendingApproval() == nil {
		t.Error("expected non-nil pending approval")
	}
}

// ─── SetCumulativeTotals with negative cost ───────────────────────────────────

func TestSetCumulativeTotalsNegativeCost(t *testing.T) {
	s := NewSessionState()
	s.SetCumulativeTotals(-1.5, 0)
	cost, _, _, _ := s.Stats()
	if cost != -1.5 {
		t.Errorf("expected -1.5, got %f", cost)
	}
	cost2, _, _, _ := s.Stats()
	if cost2 != -1.5 {
		t.Errorf("Stats() returned different cost: expected -1.5, got %f", cost2)
	}
}

func TestSetCumulativeTotalsNegativeTurns(t *testing.T) {
	s := NewSessionState()
	s.SetCumulativeTotals(0, -5)
	_, _, _, turns := s.Stats()
	if turns != -5 {
		t.Errorf("expected -5 turns, got %d", turns)
	}
}

func TestSetCumulativeTotalsNegativeBoth(t *testing.T) {
	s := NewSessionState()
	s.SetCumulativeTotals(-10.0, -3)
	cost, _, _, turns := s.Stats()
	if cost != -10.0 {
		t.Errorf("expected -10.0, got %f", cost)
	}
	if turns != -3 {
		t.Errorf("expected -3 turns, got %d", turns)
	}
}

// ─── AddTokenUsage with negative tokens ──────────────────────────────────────

func TestAddTokenUsageNegativeTokensIn(t *testing.T) {
	s := NewSessionState()
	s.AddTokenUsage(-100, 0)
	_, tokensIn, _, _ := s.Stats()
	if tokensIn != -100 {
		t.Errorf("expected -100, got %d", tokensIn)
	}
}

func TestAddTokenUsageNegativeTokensOut(t *testing.T) {
	s := NewSessionState()
	s.AddTokenUsage(0, -200)
	_, _, tokensOut, _ := s.Stats()
	if tokensOut != -200 {
		t.Errorf("expected -200, got %d", tokensOut)
	}
}

func TestAddTokenUsageNegativeBoth(t *testing.T) {
	s := NewSessionState()
	s.AddTokenUsage(-50, -75)
	_, tokensIn, tokensOut, _ := s.Stats()
	if tokensIn != -50 {
		t.Errorf("expected -50, got %d", tokensIn)
	}
	if tokensOut != -75 {
		t.Errorf("expected -75, got %d", tokensOut)
	}
}

func TestAddTokenUsageNegativeThenPositive(t *testing.T) {
	s := NewSessionState()
	s.AddTokenUsage(-100, -50)
	s.AddTokenUsage(150, 75)
	_, tokensIn, tokensOut, _ := s.Stats()
	if tokensIn != 50 {
		t.Errorf("expected 50, got %d", tokensIn)
	}
	if tokensOut != 25 {
		t.Errorf("expected 25, got %d", tokensOut)
	}
}

// ─── FilterContentBlocks edge cases ─────────────────────────────────────────

func TestFilterContentBlocksEmptySlice(t *testing.T) {
	blocks := []ContentBlock{}
	result := FilterContentBlocks(blocks, "text")
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestFilterContentBlocksNilSlice(t *testing.T) {
	var blocks []ContentBlock
	result := FilterContentBlocks(blocks, "text")
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestFilterContentBlocksNoMatches(t *testing.T) {
	blocks := []ContentBlock{
		{Type: "text", Text: "Hello"},
		{Type: "thinking", Thinking: "Thinking"},
	}
	result := FilterContentBlocks(blocks, "tool_use")
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestFilterContentBlocksEmptyType(t *testing.T) {
	blocks := []ContentBlock{
		{Type: "", Text: "Empty type"},
		{Type: "text", Text: "Has type"},
	}
	result := FilterContentBlocks(blocks, "")
	if len(result) != 1 {
		t.Errorf("expected 1, got %d", len(result))
	}
	if result[0].Text != "Empty type" {
		t.Errorf("expected 'Empty type', got %q", result[0].Text)
	}
}

func TestFilterContentBlocksNilInput(t *testing.T) {
	result := FilterContentBlocks(nil, "text")
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

// ─── ContentBlock with empty/zero-value fields ──────────────────────────────

func TestContentBlockZeroValue(t *testing.T) {
	var cb ContentBlock
	if cb.Type != "" {
		t.Errorf("expected empty Type, got %q", cb.Type)
	}
	if cb.Text != "" {
		t.Errorf("expected empty Text, got %q", cb.Text)
	}
	if cb.Thinking != "" {
		t.Errorf("expected empty Thinking, got %q", cb.Thinking)
	}
	if cb.Name != "" {
		t.Errorf("expected empty Name, got %q", cb.Name)
	}
	if cb.ID != "" {
		t.Errorf("expected empty ID, got %q", cb.ID)
	}
	if cb.Input != nil {
		t.Errorf("expected nil Input, got %v", cb.Input)
	}
}

func TestContentBlockJSONMarshalEmpty(t *testing.T) {
	cb := ContentBlock{}
	data, err := json.Marshal(cb)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if parsed["type"] != "" {
		t.Errorf("expected type to be empty string, got %v", parsed["type"])
	}
}

func TestContentBlockJSONMarshalAllFields(t *testing.T) {
	cb := ContentBlock{
		Type:     "tool_use",
		Text:     "some text",
		Thinking: "thinking text",
		Name:     "bash",
		ID:       "tool_1",
		Input:    json.RawMessage(`{"cmd":"ls"}`),
	}
	data, err := json.Marshal(cb)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var parsed ContentBlock
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if parsed.Type != "tool_use" {
		t.Errorf("expected tool_use, got %q", parsed.Type)
	}
	if parsed.Text != "some text" {
		t.Errorf("expected some text, got %q", parsed.Text)
	}
	if parsed.Thinking != "thinking text" {
		t.Errorf("expected thinking text, got %q", parsed.Thinking)
	}
	if parsed.Name != "bash" {
		t.Errorf("expected bash, got %q", parsed.Name)
	}
	if parsed.ID != "tool_1" {
		t.Errorf("expected tool_1, got %q", parsed.ID)
	}
}

// ─── SessionStatus and EventType invalid strings ────────────────────────────

func TestIsValidSessionStatusInvalid(t *testing.T) {
	invalid := []string{
		"",
		"running ",
		" Running",
		"RUNNING",
		"pending",
		"stopped",
		"idle_",
		"_idle",
		"idleee",
	}
	for _, s := range invalid {
		if IsValidSessionStatus(s) {
			t.Errorf("IsValidSessionStatus(%q) = true, want false", s)
		}
	}
}

func TestIsValidSessionStatusValid(t *testing.T) {
	valid := []string{
		"idle",
		"starting",
		"running",
		"waiting_approval",
		"stopping",
		"crashed",
	}
	for _, s := range valid {
		if !IsValidSessionStatus(s) {
			t.Errorf("IsValidSessionStatus(%q) = false, want true", s)
		}
	}
}

func TestSessionStatusString(t *testing.T) {
	validStatuses := map[SessionStatus]string{
		StatusIdle:            "idle",
		StatusStarting:        "starting",
		StatusRunning:         "running",
		StatusWaitingApproval: "waiting_approval",
		StatusStopping:        "stopping",
		StatusCrashed:         "crashed",
	}
	for status, expected := range validStatuses {
		if status.String() != expected {
			t.Errorf("Status(%q).String() = %q, want %q", status, status.String(), expected)
		}
	}
}

func TestSessionStatusInvalidString(t *testing.T) {
	invalidStatus := SessionStatus("not_a_status")
	if invalidStatus.String() != "not_a_status" {
		t.Errorf("expected 'not_a_status', got %q", invalidStatus.String())
	}
	if IsValidSessionStatus(string(invalidStatus)) {
		t.Errorf("IsValidSessionStatus(%q) = true, want false", invalidStatus)
	}
}

func TestEventTypeInvalidString(t *testing.T) {
	invalidType := EventType("not_an_event")
	if string(invalidType) != "not_an_event" {
		t.Errorf("expected 'not_an_event', got %q", string(invalidType))
	}
}

func TestEventTypeString(t *testing.T) {
	validTypes := map[EventType]string{
		EventSystem:    "system",
		EventAssistant: "assistant",
		EventUser:      "user",
		EventResult:    "result",
		EventRateLimit: "rate_limit",
	}
	for et, expected := range validTypes {
		if string(et) != expected {
			t.Errorf("EventType(%q) = %q, want %q", et, string(et), expected)
		}
	}
}

// ─── Concurrent access ───────────────────────────────────────────────────────

func TestConcurrentSetCumulativeTotalsDataRace(t *testing.T) {
	s := NewSessionState()
	var wg sync.WaitGroup
	const goroutines = 200

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if id%2 == 0 {
				s.SetCumulativeTotals(float64(id), id)
			} else {
				s.SetCumulativeTotals(-float64(id), -id)
			}
		}(i)
	}
	wg.Wait()
	// The race detector will fail if there's a data race.
	_, _, _, _ = s.Stats()
}

func TestConcurrentAddTokenUsageDataRace(t *testing.T) {
	s := NewSessionState()
	var wg sync.WaitGroup
	const goroutines = 200

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			s.AddTokenUsage(id, id*2)
		}(i)
	}
	wg.Wait()
	expectedIn := (goroutines - 1) * goroutines / 2
	expectedOut := expectedIn * 2
	_, tokensIn, tokensOut, _ := s.Stats()
	if tokensIn != expectedIn {
		t.Errorf("tokensIn: expected %d, got %d", expectedIn, tokensIn)
	}
	if tokensOut != expectedOut {
		t.Errorf("tokensOut: expected %d, got %d", expectedOut, tokensOut)
	}
}

func TestConcurrentSetPendingApprovalDataRace(t *testing.T) {
	s := NewSessionState()
	var wg sync.WaitGroup
	const goroutines = 100

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			if id%2 == 0 {
				s.SetPendingApproval(&ToolCall{ID: "call"})
			} else {
				s.SetPendingApproval(nil)
			}
		}(i)
	}
	wg.Wait()
	st := s.Status()
	if st != StatusWaitingApproval && st != StatusRunning {
		t.Errorf("unexpected status: %v", st)
	}
}

func TestConcurrentMixedOperations(t *testing.T) {
	s := NewSessionState()
	s.StartSession("/tmp")
	var wg sync.WaitGroup
	const goroutines = 100

	wg.Add(goroutines * 7)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			s.SetStatus(StatusRunning)
		}()
		go func() {
			defer wg.Done()
			s.SetSessionID("concurrent")
		}()
		go func() {
			defer wg.Done()
			s.TouchActivity()
		}()
		go func() {
			defer wg.Done()
			s.SetPendingApproval(&ToolCall{ID: "call"})
		}()
		go func() {
			defer wg.Done()
			s.SetPendingApproval(nil)
		}()
		go func(val int) {
			defer wg.Done()
			s.SetCumulativeTotals(float64(val), val)
		}(i)
		go func(val int) {
			defer wg.Done()
			s.AddTokenUsage(val, val)
		}(i)
	}
	wg.Wait()
}

func TestConcurrentStatsReads(t *testing.T) {
	s := NewSessionState()
	s.StartSession("/tmp")
	var wg sync.WaitGroup
	const goroutines = 100

	wg.Add(goroutines * 2)
	for i := 0; i < goroutines; i++ {
		go func(val int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				s.SetCumulativeTotals(float64(j), j)
				s.AddTokenUsage(j, j)
				s.SetPendingApproval(&ToolCall{ID: "call"})
			}
		}(i)
		go func(val int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				s.Stats()
				s.Status()
				s.PendingApproval()
			}
		}(i)
	}
	wg.Wait()
}

// ─── NormalizeBlockType ─────────────────────────────────────────────────────

func TestNormalizeBlockType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"text", "text"},
		{"TEXT", "text"},
		{"Text", "text"},
		{"  text  ", "text"},
		{"  TEXT", "text"},
		{"text ", "text"},
		{"", ""},
		{"  ", ""},
		{"  leading", "leading"},
		{"trailing  ", "trailing"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := NormalizeBlockType(tc.input)
			if got != tc.expected {
				t.Errorf("NormalizeBlockType(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestNormalizeBlockTypeWithUnicode(t *testing.T) {
	result := NormalizeBlockType("tëxt")
	if result != "tëxt" {
		t.Errorf("expected tëxt, got %q", result)
	}
}

// ─── Message JSON round-trip ────────────────────────────────────────────────

func TestMessageJSONRoundTrip(t *testing.T) {
	msg := Message{
		ID:         "msg_1",
		Model:      "claude-3-5",
		StopReason: "end_turn",
		Role:       "assistant",
		Content: []ContentBlock{
			{Type: "text", Text: "Hello"},
			{Type: "tool_use", Name: "bash", ID: "tool_1", Input: json.RawMessage(`{}`)},
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var parsed Message
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if parsed.ID != msg.ID {
		t.Errorf("ID: expected %q, got %q", msg.ID, parsed.ID)
	}
	if parsed.Role != msg.Role {
		t.Errorf("Role: expected %q, got %q", msg.Role, parsed.Role)
	}
	if len(parsed.Content) != len(msg.Content) {
		t.Errorf("Content length: expected %d, got %d", len(msg.Content), len(parsed.Content))
	}
}

// ─── ToolCall behavior ──────────────────────────────────────────────────────

func TestToolCallZeroValue(t *testing.T) {
	var tc ToolCall
	if tc.ID != "" {
		t.Errorf("expected empty ID, got %q", tc.ID)
	}
	if tc.Name != "" {
		t.Errorf("expected empty Name, got %q", tc.Name)
	}
	if tc.Input != nil {
		t.Errorf("expected nil Input, got %v", tc.Input)
	}
	if tc.Handled {
		t.Errorf("expected Handled=false, got true")
	}
	if tc.Decision != "" {
		t.Errorf("expected empty Decision, got %q", tc.Decision)
	}
}

func TestToolCallFields(t *testing.T) {
	tc := ToolCall{
		ID:       "call_1",
		Name:     "bash",
		Input:    json.RawMessage(`{"cmd":"ls -la"}`),
		Handled:  true,
		Decision: "approved",
	}
	if tc.ID != "call_1" {
		t.Errorf("ID: expected call_1, got %q", tc.ID)
	}
	if tc.Name != "bash" {
		t.Errorf("Name: expected bash, got %q", tc.Name)
	}
	if tc.Handled != true {
		t.Errorf("Handled: expected true, got %v", tc.Handled)
	}
	if tc.Decision != "approved" {
		t.Errorf("Decision: expected approved, got %q", tc.Decision)
	}
}

// ─── Usage zero value ────────────────────────────────────────────────────────

func TestUsageZeroValue(t *testing.T) {
	var u Usage
	if u.InputTokens != 0 {
		t.Errorf("expected 0, got %d", u.InputTokens)
	}
	if u.OutputTokens != 0 {
		t.Errorf("expected 0, got %d", u.OutputTokens)
	}
}

func TestUsageJSONMarshal(t *testing.T) {
	u := Usage{InputTokens: 100, OutputTokens: 200}
	data, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var parsed Usage
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if parsed.InputTokens != 100 {
		t.Errorf("InputTokens: expected 100, got %d", parsed.InputTokens)
	}
	if parsed.OutputTokens != 200 {
		t.Errorf("OutputTokens: expected 200, got %d", parsed.OutputTokens)
	}
}

// ─── Event with nil/empty fields ────────────────────────────────────────────

func TestEventZeroValue(t *testing.T) {
	var e Event
	if e.Type != "" {
		t.Errorf("expected empty Type, got %v", e.Type)
	}
	if e.SessionID != "" {
		t.Errorf("expected empty SessionID, got %q", e.SessionID)
	}
	if e.Message != nil {
		t.Errorf("expected nil Message, got %v", e.Message)
	}
	if e.Usage != nil {
		t.Errorf("expected nil Usage, got %v", e.Usage)
	}
	if e.TotalCostUSD != 0 {
		t.Errorf("expected 0, got %f", e.TotalCostUSD)
	}
	if e.NumTurns != 0 {
		t.Errorf("expected 0, got %d", e.NumTurns)
	}
	if e.IsError != false {
		t.Errorf("expected false, got %v", e.IsError)
	}
}

func TestEventJSONRoundTrip(t *testing.T) {
	e := Event{
		Type:         EventAssistant,
		Subtype:      "sub",
		SessionID:    "sess_1",
		Result:       "done",
		IsError:      true,
		TotalCostUSD: 0.05,
		NumTurns:     3,
		Usage:        &Usage{InputTokens: 100, OutputTokens: 200},
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var parsed Event
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if parsed.Type != EventAssistant {
		t.Errorf("Type: expected %q, got %q", EventAssistant, parsed.Type)
	}
	if parsed.IsError != true {
		t.Errorf("IsError: expected true, got %v", parsed.IsError)
	}
	if parsed.NumTurns != 3 {
		t.Errorf("NumTurns: expected 3, got %d", parsed.NumTurns)
	}
	if parsed.Usage == nil {
		t.Fatal("Usage should not be nil after unmarshal")
	}
	if parsed.Usage.InputTokens != 100 {
		t.Errorf("Usage.InputTokens: expected 100, got %d", parsed.Usage.InputTokens)
	}
}

// ─── Time fields ──────────────────────────────────────────────────────────────

func TestSessionStateTimeFields(t *testing.T) {
	t.Run("started_at_zero_before_start_session", func(t *testing.T) {
		s := NewSessionState()
		if !s.StartedAt().IsZero() {
			t.Errorf("expected zero, got %v", s.StartedAt())
		}
	})

	t.Run("last_activity_zero_before_start_session", func(t *testing.T) {
		s := NewSessionState()
		if !s.LastActivity().IsZero() {
			t.Errorf("expected zero, got %v", s.LastActivity())
		}
	})

	t.Run("started_at_set_after_start_session", func(t *testing.T) {
		s := NewSessionState()
		before := time.Now()
		s.StartSession("/tmp")
		after := time.Now()
		if s.StartedAt().Before(before) || s.StartedAt().After(after) {
			t.Errorf("StartedAt not in expected range")
		}
	})

	t.Run("last_activity_set_after_start_session", func(t *testing.T) {
		s := NewSessionState()
		before := time.Now()
		s.StartSession("/tmp")
		after := time.Now()
		if s.LastActivity().Before(before) || s.LastActivity().After(after) {
			t.Errorf("LastActivity not in expected range")
		}
	})
}

// ─── Large values ────────────────────────────────────────────────────────────

func TestAddTokenUsageLargeValues(t *testing.T) {
	s := NewSessionState()
	s.AddTokenUsage(1e9, 1e9)
	_, tokensIn, tokensOut, _ := s.Stats()
	if tokensIn != 1e9 {
		t.Errorf("expected 1e9, got %d", tokensIn)
	}
	if tokensOut != 1e9 {
		t.Errorf("expected 1e9, got %d", tokensOut)
	}
}

func TestSetCumulativeTotalsLargeValues(t *testing.T) {
	s := NewSessionState()
	s.SetCumulativeTotals(1e15, 1e9)
	cost, _, _, turns := s.Stats()
	if cost != 1e15 {
		t.Errorf("expected 1e15, got %e", cost)
	}
	if turns != 1e9 {
		t.Errorf("expected 1e9, got %d", turns)
	}
}

func TestFilterContentBlocksManyMatches(t *testing.T) {
	blocks := make([]ContentBlock, 1000)
	for i := 0; i < 1000; i++ {
		if i%2 == 0 {
			blocks[i] = ContentBlock{Type: "text", Text: "match"}
		} else {
			blocks[i] = ContentBlock{Type: "other", Text: "no"}
		}
	}
	result := FilterContentBlocks(blocks, "text")
	if len(result) != 500 {
		t.Errorf("expected 500 matches, got %d", len(result))
	}
}
