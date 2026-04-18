// Package types provides shared data types for the Craudinei project.
package types

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// SessionStatus represents the current state of a session.
type SessionStatus string

// Session status constants.
const (
	StatusIdle            SessionStatus = "idle"
	StatusStarting        SessionStatus = "starting"
	StatusRunning         SessionStatus = "running"
	StatusWaitingApproval SessionStatus = "waiting_approval"
	StatusStopping        SessionStatus = "stopping"
	StatusCrashed         SessionStatus = "crashed"
)

// String returns the string representation of the session status.
func (s SessionStatus) String() string {
	return string(s)
}

// EventType represents the type of an event.
type EventType string

// Event type constants.
const (
	EventSystem    EventType = "system"
	EventAssistant EventType = "assistant"
	EventUser      EventType = "user"
	EventResult    EventType = "result"
	EventRateLimit EventType = "rate_limit"
)

// ContentBlock represents a content block in a message.
type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	Name     string          `json:"name,omitempty"`
	ID       string          `json:"id,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

// Message represents a message in a conversation.
type Message struct {
	ID         string         `json:"id,omitempty"`
	Model      string         `json:"model,omitempty"`
	StopReason string         `json:"stop_reason,omitempty"`
	Content    []ContentBlock `json:"content,omitempty"`
	Role       string         `json:"role,omitempty"`
}

// Usage represents token usage statistics.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Event represents an event in the system.
type Event struct {
	Type              EventType `json:"type"`
	Subtype           string    `json:"subtype,omitempty"`
	SessionID         string    `json:"session_id,omitempty"`
	Message           *Message  `json:"message,omitempty"`
	Result            string    `json:"result,omitempty"`
	IsError           bool      `json:"is_error,omitempty"`
	Usage             *Usage    `json:"usage,omitempty"`
	TotalCostUSD      float64   `json:"total_cost_usd,omitempty"`
	NumTurns          int       `json:"num_turns,omitempty"`
	RetryAfterSeconds int       `json:"retry_after_seconds,omitempty"`
	Attempt           int       `json:"attempt,omitempty"`
	MaxRetries        int       `json:"max_retries,omitempty"`
}

// ToolCall represents a tool call (not for JSON serialization).
type ToolCall struct {
	ID       string
	Name     string
	Input    json.RawMessage
	Handled  bool
	Decision string
}

// SessionState holds the state of a session.
type SessionState struct {
	mu sync.Mutex

	status          SessionStatus
	sessionID       string
	workDir         string
	startedAt       time.Time
	lastActivity    time.Time
	pendingApproval *ToolCall
	totalCost       float64
	totalTokensIn   int
	totalTokensOut  int
	numTurns        int
}

// NewSessionState creates a new SessionState with default values.
func NewSessionState() *SessionState {
	return &SessionState{
		status: StatusIdle,
	}
}

// Status returns the current session status.
func (s *SessionState) Status() SessionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// SetStatus sets the session status.
func (s *SessionState) SetStatus(status SessionStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

// TransitionStatus atomically transitions from old to new status.
// Returns true if the current status was old and the transition was performed.
func (s *SessionState) TransitionStatus(old, new SessionStatus) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != old {
		return false
	}
	s.status = new
	return true
}

// SessionID returns the session ID.
func (s *SessionState) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// SetSessionID sets the session ID.
func (s *SessionState) SetSessionID(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = sessionID
}

// WorkDir returns the working directory.
func (s *SessionState) WorkDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workDir
}

// StartSession initializes a new session with the given working directory.
func (s *SessionState) StartSession(workDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workDir = workDir
	s.startedAt = time.Now()
	s.lastActivity = s.startedAt
	s.totalCost = 0
	s.totalTokensIn = 0
	s.totalTokensOut = 0
	s.numTurns = 0
	s.pendingApproval = nil
}

// StartedAt returns the session start time.
func (s *SessionState) StartedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startedAt
}

// LastActivity returns the time of the last activity.
func (s *SessionState) LastActivity() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastActivity
}

// TouchActivity updates the last activity time to now.
func (s *SessionState) TouchActivity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastActivity = time.Now()
}

// PendingApproval returns the pending tool call awaiting approval.
func (s *SessionState) PendingApproval() *ToolCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingApproval
}

// SetPendingApproval sets the pending tool call awaiting approval.
func (s *SessionState) SetPendingApproval(tc *ToolCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingApproval = tc
	if tc != nil {
		s.status = StatusWaitingApproval
	} else {
		s.status = StatusRunning
	}
}

// SetCumulativeTotals sets the cumulative cost and turn counts (total, not delta).
func (s *SessionState) SetCumulativeTotals(cost float64, turns int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalCost = cost
	s.numTurns = turns
}

// AddTokenUsage adds per-event token usage to the cumulative totals.
func (s *SessionState) AddTokenUsage(tokensIn, tokensOut int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalTokensIn += tokensIn
	s.totalTokensOut += tokensOut
}

// Stats returns the current usage statistics.
func (s *SessionState) Stats() (cost float64, tokensIn, tokensOut, turns int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.totalCost, s.totalTokensIn, s.totalTokensOut, s.numTurns
}

// FilterContentBlocks filters content blocks by type.
func FilterContentBlocks(blocks []ContentBlock, blockType string) []ContentBlock {
	var result []ContentBlock
	for _, block := range blocks {
		if block.Type == blockType {
			result = append(result, block)
		}
	}
	return result
}

// IsValidSessionStatus checks if a status string is a known session status.
func IsValidSessionStatus(status string) bool {
	switch SessionStatus(status) {
	case StatusIdle, StatusStarting, StatusRunning, StatusWaitingApproval, StatusStopping, StatusCrashed:
		return true
	default:
		return false
	}
}

// NormalizeBlockType normalizes block type strings for comparison.
func NormalizeBlockType(t string) string {
	return strings.ToLower(strings.TrimSpace(t))
}
