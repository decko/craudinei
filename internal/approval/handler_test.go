package approval

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/decko/craudinei/internal/audit"
)

// httpHandler wraps Handler to implement http.Handler for testing.
type httpHandler struct {
	*Handler
}

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.Handler.HandleApproval(w, r)
}

func TestApprovalApprove(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 2*time.Second, logger, nil)

	// Create approval request
	reqBody := `{"tool_name": "Bash", "tool_input": {"command": "ls -la"}, "session_id": "test-session"}`
	req := httptest.NewRequest("POST", "/approval", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	// Run the request in a goroutine so we can send the callback
	var resp *httptest.ResponseRecorder
	var reqWg sync.WaitGroup
	reqWg.Add(1)
	go func() {
		defer reqWg.Done()
		resp = httptest.NewRecorder()
		h := &httpHandler{handler}
		h.ServeHTTP(resp, req)
	}()

	// Wait for message to be sent to Telegram
	time.Sleep(50 * time.Millisecond)

	// Verify Telegram message was sent
	sent := bot.getSent()
	if len(sent) == 0 {
		t.Fatal("No message sent to Telegram")
	}
	if sent[0].chatID != 123 {
		t.Errorf("chatID = %d, want 123", sent[0].chatID)
	}

	// Get the pending request ID from the handler
	handler.mu.RLock()
	var pendingID string
	for id := range handler.pending {
		pendingID = id
		break
	}
	handler.mu.RUnlock()

	if pendingID == "" {
		t.Fatal("No pending request found")
	}

	// Simulate callback with approve decision
	handler.HandleCallback(pendingID, "approve")

	// Wait for response
	reqWg.Wait()

	if resp == nil {
		t.Fatal("Response is nil")
	}

	if resp.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", resp.Code, http.StatusOK)
	}

	var approvalResp ApprovalResponse
	if err := json.NewDecoder(resp.Body).Decode(&approvalResp); err != nil {
		t.Fatalf("Decoding response: %v", err)
	}

	if approvalResp.Decision != "approve" {
		t.Errorf("Decision = %q, want %q", approvalResp.Decision, "approve")
	}
}

func TestApprovalDeny(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 2*time.Second, logger, nil)

	// Create approval request
	reqBody := `{"tool_name": "Bash", "tool_input": {"command": "rm -rf /"}, "session_id": "test-session"}`
	req := httptest.NewRequest("POST", "/approval", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	// Run the request in a goroutine so we can send the callback
	var resp *httptest.ResponseRecorder
	var reqWg sync.WaitGroup
	reqWg.Add(1)
	go func() {
		defer reqWg.Done()
		resp = httptest.NewRecorder()
		h := &httpHandler{handler}
		h.ServeHTTP(resp, req)
	}()

	// Wait for message to be sent to Telegram
	time.Sleep(50 * time.Millisecond)

	// Get the pending request ID from the handler
	handler.mu.RLock()
	var pendingID string
	for id := range handler.pending {
		pendingID = id
		break
	}
	handler.mu.RUnlock()

	// Simulate callback with deny decision
	handler.HandleCallback(pendingID, "deny")

	// Wait for response
	reqWg.Wait()

	if resp == nil {
		t.Fatal("Response is nil")
	}

	if resp.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", resp.Code, http.StatusOK)
	}

	var approvalResp ApprovalResponse
	if err := json.NewDecoder(resp.Body).Decode(&approvalResp); err != nil {
		t.Fatalf("Decoding response: %v", err)
	}

	if approvalResp.Decision != "deny" {
		t.Errorf("Decision = %q, want %q", approvalResp.Decision, "deny")
	}
}

func TestTimeout(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	// Very short timeout for testing
	handler := NewHandler(123, bot, 50*time.Millisecond, logger, nil)

	// Create approval request
	reqBody := `{"tool_name": "Bash", "tool_input": {"command": "ls -la"}, "session_id": "test-session"}`
	req := httptest.NewRequest("POST", "/approval", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	resp := httptest.NewRecorder()
	h := &httpHandler{handler}
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Errorf("Status code = %d, want %d", resp.Code, http.StatusOK)
	}

	var approvalResp ApprovalResponse
	if err := json.NewDecoder(resp.Body).Decode(&approvalResp); err != nil {
		t.Fatalf("Decoding response: %v", err)
	}

	if approvalResp.Decision != "timeout" {
		t.Errorf("Decision = %q, want %q", approvalResp.Decision, "timeout")
	}
}

func TestIdempotentRequest(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 2*time.Second, logger, nil)

	// Create two identical requests sequentially
	// Each should get its own approval flow with a unique ID
	reqBody := `{"tool_name": "Bash", "tool_input": {"command": "ls -la"}, "session_id": "test-session"}`

	// First request
	req1 := httptest.NewRequest("POST", "/approval", strings.NewReader(reqBody))
	req1.Header.Set("Content-Type", "application/json")
	resp1 := httptest.NewRecorder()
	h := &httpHandler{handler}

	// Run first request in goroutine so we can send callback
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.ServeHTTP(resp1, req1)
	}()

	// Wait for first request to be pending
	time.Sleep(50 * time.Millisecond)

	// Verify we have exactly one pending request
	handler.mu.RLock()
	pendingCount := len(handler.pending)
	handler.mu.RUnlock()
	if pendingCount != 1 {
		t.Errorf("pending count = %d, want 1", pendingCount)
	}

	// Get the first request's ID
	handler.mu.RLock()
	var firstID string
	for id := range handler.pending {
		firstID = id
		break
	}
	handler.mu.RUnlock()

	// Send callback to unblock first request
	handler.HandleCallback(firstID, "approve")
	wg.Wait()

	// Verify first response
	if resp1.Code != http.StatusOK {
		t.Errorf("First request status = %d, want %d", resp1.Code, http.StatusOK)
	}
	var approvalResp1 ApprovalResponse
	if err := json.NewDecoder(resp1.Body).Decode(&approvalResp1); err != nil {
		t.Fatalf("Decoding first response: %v", err)
	}
	if approvalResp1.Decision != "approve" {
		t.Errorf("First decision = %q, want %q", approvalResp1.Decision, "approve")
	}

	// Second request - should get its own approval flow (not share the first's)
	req2 := httptest.NewRequest("POST", "/approval", strings.NewReader(reqBody))
	req2.Header.Set("Content-Type", "application/json")
	resp2 := httptest.NewRecorder()

	wg.Add(1)
	go func() {
		defer wg.Done()
		h.ServeHTTP(resp2, req2)
	}()

	// Wait for second request to be pending
	time.Sleep(50 * time.Millisecond)

	// Get the second request's ID
	handler.mu.RLock()
	var secondID string
	for id := range handler.pending {
		secondID = id
		break
	}
	handler.mu.RUnlock()

	// Verify IDs are different (each request gets its own flow)
	if firstID == secondID {
		t.Errorf("firstID = secondID = %q, want different IDs", firstID)
	}

	// Send callback for second request
	handler.HandleCallback(secondID, "approve")
	wg.Wait()

	// Verify second response
	if resp2.Code != http.StatusOK {
		t.Errorf("Second request status = %d, want %d", resp2.Code, http.StatusOK)
	}
	var approvalResp2 ApprovalResponse
	if err := json.NewDecoder(resp2.Body).Decode(&approvalResp2); err != nil {
		t.Fatalf("Decoding second response: %v", err)
	}
	if approvalResp2.Decision != "approve" {
		t.Errorf("Second decision = %q, want %q", approvalResp2.Decision, "approve")
	}
}

func TestInvalidHTTPMethod(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 5*time.Second, logger, nil)

	req := httptest.NewRequest("GET", "/approval", nil)
	resp := httptest.NewRecorder()
	h := &httpHandler{handler}
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status code = %d, want %d", resp.Code, http.StatusMethodNotAllowed)
	}
}

func TestMalformedJSON(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 5*time.Second, logger, nil)

	tests := []struct {
		name string
		body string
	}{
		{"invalid json", "{invalid}"},
		{"empty body", ""},
		{"partial json", `{"tool_name": "Bash"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/approval", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			h := &httpHandler{handler}
			h.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Errorf("Status code = %d, want %d", resp.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestConcurrentApprovals(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 100*time.Millisecond, logger, nil)

	// Send multiple concurrent requests - they will all timeout but that's OK
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			reqBody := fmt.Sprintf(`{"tool_name": "Bash", "tool_input": {"command": "ls -la"}, "session_id": "test-session-%d"}`, idx)
			req := httptest.NewRequest("POST", "/approval", strings.NewReader(reqBody))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			h := &httpHandler{handler}
			h.ServeHTTP(resp, req)
		}(i)
	}
	wg.Wait()
}

func TestCleanup(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	// Very short timeout
	handler := NewHandler(123, bot, 50*time.Millisecond, logger, nil)

	// Manually add an expired request
	expiredReq := &ApprovalRequest{
		ID:        "expired-id",
		ToolName:  "Bash",
		Response:  make(chan *ApprovalResponse, 1),
		CreatedAt: time.Now().Add(-100 * time.Millisecond),
	}
	handler.mu.Lock()
	handler.pending["expired-id"] = expiredReq
	handler.mu.Unlock()

	// Run cleanup
	handler.Cleanup()

	// Verify the expired request was removed
	handler.mu.RLock()
	_, ok := handler.pending["expired-id"]
	handler.mu.RUnlock()
	if ok {
		t.Error("Expired request was not cleaned up")
	}
}

func TestHandleCallbackUnknownRequest(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 5*time.Second, logger, nil)

	// Should not panic on unknown request
	handler.HandleCallback("unknown-id", "approve")
}

func TestNewHandlerNilBot(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	defer func() {
		if r := recover(); r == nil {
			t.Error("NewHandler with nil bot did not panic")
		}
	}()

	NewHandler(123, nil, 5*time.Second, logger, nil)
}

func TestNewHandlerZeroTimeout(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}

	defer func() {
		if r := recover(); r == nil {
			t.Error("NewHandler with zero timeout did not panic")
		}
	}()

	NewHandler(123, bot, 0, logger, nil)
}

func TestAuditLogger_ToolDecision(t *testing.T) {
	t.Helper()

	// Create temp file for audit log
	tmpFile, err := os.CreateTemp("", "audit-approval-test-*.log")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// Create audit logger
	auditLogger, err := audit.NewWithFile(tmpPath)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}
	defer auditLogger.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 2*time.Second, logger, auditLogger)

	// Create approval request
	reqBody := `{"tool_name": "Bash", "tool_input": {"command": "ls -la"}, "session_id": "test-session"}`
	req := httptest.NewRequest("POST", "/approval", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	// Run the request in a goroutine so we can send the callback
	var resp *httptest.ResponseRecorder
	var reqWg sync.WaitGroup
	reqWg.Add(1)
	go func() {
		defer reqWg.Done()
		resp = httptest.NewRecorder()
		h := &httpHandler{handler}
		h.ServeHTTP(resp, req)
	}()

	// Wait for message to be sent to Telegram
	time.Sleep(50 * time.Millisecond)

	// Get the pending request ID from the handler
	handler.mu.RLock()
	var pendingID string
	for id := range handler.pending {
		pendingID = id
		break
	}
	handler.mu.RUnlock()

	if pendingID == "" {
		t.Fatal("No pending request found")
	}

	// Simulate callback with approve decision
	handler.HandleCallback(pendingID, "approve")

	// Wait for response
	reqWg.Wait()

	// Read audit file content
	content, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("Failed to read audit file: %v", err)
	}

	// Verify ToolDecision entry
	if !strings.Contains(string(content), `"tool_decision"`) {
		t.Error("audit file does not contain tool_decision entry")
	}
	if !strings.Contains(string(content), `"Bash"`) {
		t.Error("audit file does not contain tool name")
	}
	if !strings.Contains(string(content), `"approved"`) {
		t.Error("audit file does not contain approved outcome")
	}
}
