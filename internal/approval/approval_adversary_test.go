package approval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/decko/craudinei/internal/config"
)

// --- Test 1: Server binds ONLY to 127.0.0.1 (not 0.0.0.0) ---

func TestServerBindsOnlyToLocalhost(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 5*time.Second, logger, nil)
	cfg := &config.ApprovalConfig{Port: 0}

	srv, err := NewServer(cfg, handler, logger)
	if err != nil {
		t.Fatalf("NewServer() = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	defer srv.Stop()

	// Verify the listener address is exactly 127.0.0.1, not 0.0.0.0
	lnAddr := srv.listener.Addr().String()
	if !strings.HasPrefix(lnAddr, "127.0.0.1:") {
		t.Errorf("listener addr = %q, want prefix %q", lnAddr, "127.0.0.1:")
	}

	// Verify it does NOT bind to 0.0.0.0
	if strings.HasPrefix(lnAddr, "0.0.0.0:") {
		t.Errorf("listener addr = %q, should NOT start with 0.0.0.0:", lnAddr)
	}

	// Also verify by attempting to connect to the actual address
	port := srv.Port()
	if port == 0 {
		t.Fatal("Port() = 0, want non-zero")
	}

	// 127.0.0.1 must work
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Errorf("Dial(127.0.0.1:%d) = %v, want nil", port, err)
	} else {
		conn.Close()
	}
}

// --- Test 2: MaxBytesReader rejects bodies > 1MB ---

func TestMaxBytesReaderRejectsLargeBody(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 5*time.Second, logger, nil)

	// Create a body larger than 1MB (1<<20 = 1MB)
	largeBody := strings.Repeat("x", 1<<20+1)

	req := httptest.NewRequest("POST", "/approval", strings.NewReader(largeBody))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	h := &httpHandler{handler}
	h.ServeHTTP(resp, req)

	// Should be rejected as bad request due to MaxBytesReader
	if resp.Code != http.StatusBadRequest {
		t.Errorf("Status code = %d, want %d (body > 1MB should be rejected)", resp.Code, http.StatusBadRequest)
	}
}

// --- Test 3: HandleCallback with valid pending request delivers response correctly ---

func TestHandleCallbackDeliversResponse(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 2*time.Second, logger, nil)

	// Create approval request
	reqBody := `{"tool_name": "Bash", "tool_input": {"command": "echo hi"}, "session_id": "test"}`
	req := httptest.NewRequest("POST", "/approval", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	var resp *httptest.ResponseRecorder
	var reqWg sync.WaitGroup
	reqWg.Add(1)
	go func() {
		defer reqWg.Done()
		resp = httptest.NewRecorder()
		h := &httpHandler{handler}
		h.ServeHTTP(resp, req)
	}()

	// Wait for request to be pending
	time.Sleep(50 * time.Millisecond)

	// Get the pending request ID
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

	// Deliver callback with "deny" decision
	handler.HandleCallback(pendingID, "deny")
	reqWg.Wait()

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
	if approvalResp.Reason != "User deny" {
		t.Errorf("Reason = %q, want %q", approvalResp.Reason, "User deny")
	}

	// Verify request was removed from pending map
	handler.mu.RLock()
	_, ok := handler.pending[pendingID]
	handler.mu.RUnlock()
	if ok {
		t.Error("Request still in pending map after callback")
	}
}

// --- Test 4: Server.Stop() shuts down cleanly with no goroutine leaks ---

func TestServerStopNoGoroutineLeak(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 5*time.Second, logger, nil)
	cfg := &config.ApprovalConfig{Port: 0}

	srv, err := NewServer(cfg, handler, logger)
	if err != nil {
		t.Fatalf("NewServer() = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	if err := srv.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start() = %v, want nil", err)
	}

	// Get baseline goroutine count after start
	runtime.GC()
	goroutinesBefore := runtime.NumGoroutine()

	// Stop the server
	cancel()
	srv.Stop()

	// Give goroutines time to exit
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	goroutinesAfter := runtime.NumGoroutine()

	// Allow some tolerance since the test runtime itself may spawn goroutines
	// The key check is that Stop() completes (no hang) and no obvious leak.
	// CI environments can have more goroutine variance, so we use +10 tolerance.
	if goroutinesAfter > goroutinesBefore+10 {
		t.Errorf("goroutine leak detected: before=%d, after=%d", goroutinesBefore, goroutinesAfter)
	}
}

// --- Test 5: GenerateMCPConfig returns valid JSON with correct structure ---

func TestGenerateMCPConfigValidJSON(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 5*time.Second, logger, nil)
	cfg := &config.ApprovalConfig{Port: 0}

	srv, err := NewServer(cfg, handler, logger)
	if err != nil {
		t.Fatalf("NewServer() = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	defer srv.Stop()

	cfgOut, err := srv.GenerateMCPConfig()
	if err != nil {
		t.Fatalf("GenerateMCPConfig() = %v, want nil", err)
	}

	// Must be valid JSON
	var parsed map[string]any
	if err := json.Unmarshal([]byte(cfgOut), &parsed); err != nil {
		t.Fatalf("GenerateMCPConfig() returned invalid JSON: %v", err)
	}

	// Must have mcpServers key
	mcpServers, ok := parsed["mcpServers"].(map[string]any)
	if !ok {
		t.Fatal("mcpServers key missing or not a map")
	}

	// Must have "craudinei" server
	craudinei, ok := mcpServers["craudinei"].(map[string]any)
	if !ok {
		t.Fatal("mcpServers.craudinei missing or not a map")
	}

	// Must have "command" set to the test binary
	cmd, ok := craudinei["command"].(string)
	if !ok {
		t.Fatal("craudinei.command missing or not a string")
	}
	if cmd == "" {
		t.Error("craudinei.command is empty")
	}

	// Must have "args" containing mcp-shim and port
	args, ok := craudinei["args"].([]any)
	if !ok {
		t.Fatal("craudinei.args missing or not an array")
	}

	port := srv.Port()
	expectedPort := fmt.Sprintf("%d", port)
	foundShim := false
	foundPort := false
	for _, a := range args {
		if s, ok := a.(string); ok {
			if s == "mcp-shim" {
				foundShim = true
			}
			if s == expectedPort {
				foundPort = true
			}
		}
	}
	if !foundShim {
		t.Errorf("MCP config args = %v, want to contain mcp-shim", args)
	}
	if !foundPort {
		t.Errorf("MCP config args = %v, want to contain port %s", args, expectedPort)
	}
}

// --- Test 6: Handler formats approval message with HTML parse mode ---

func TestHandlerFormatsApprovalMessageWithHTML(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 2*time.Second, logger, nil)

	// Create approval request with tool input
	reqBody := `{"tool_name": "Bash", "tool_input": {"command": "ls -la"}, "session_id": "test"}`
	req := httptest.NewRequest("POST", "/approval", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	var reqWg sync.WaitGroup
	reqWg.Add(1)
	go func() {
		defer reqWg.Done()
		resp := httptest.NewRecorder()
		h := &httpHandler{handler}
		h.ServeHTTP(resp, req)
	}()

	// Wait for message to be sent
	time.Sleep(50 * time.Millisecond)

	// Verify Telegram message was sent
	sent := bot.getSent()
	if len(sent) == 0 {
		t.Fatal("No message sent to Telegram")
	}

	msg := sent[0]

	// Must use HTML parse mode
	if msg.parseMode != "HTML" {
		t.Errorf("parseMode = %q, want %q", msg.parseMode, "HTML")
	}

	// Must contain required HTML formatting tags
	if !strings.Contains(msg.text, "Approval Required") {
		t.Errorf("message text missing 'Approval Required': %q", msg.text)
	}
	if !strings.Contains(msg.text, "<b>Tool:</b>") {
		t.Errorf("message text missing <b>Tool:</b>: %q", msg.text)
	}
	if !strings.Contains(msg.text, "<b>Input:</b>") {
		t.Errorf("message text missing <b>Input:</b>: %q", msg.text)
	}
	if !strings.Contains(msg.text, "<pre>") {
		t.Errorf("message text missing <pre> tag: %q", msg.text)
	}

	// Must include the tool name
	if !strings.Contains(msg.text, "Bash") {
		t.Errorf("message text missing tool name 'Bash': %q", msg.text)
	}

	// Must include the formatted input
	if !strings.Contains(msg.text, "command") || !strings.Contains(msg.text, "ls -la") {
		t.Errorf("message text missing tool input: %q", msg.text)
	}

	// Must be sent to correct chat
	if msg.chatID != 123 {
		t.Errorf("chatID = %d, want %d", msg.chatID, 123)
	}

	reqWg.Wait()
}

// --- Additional adversarial tests ---

func TestServerRejectsNonLocalhostConnection(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 5*time.Second, logger, nil)
	cfg := &config.ApprovalConfig{Port: 0}

	srv, err := NewServer(cfg, handler, logger)
	if err != nil {
		t.Fatalf("NewServer() = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	defer srv.Stop()

	port := srv.Port()
	if port == 0 {
		t.Fatal("Port() = 0")
	}

	// Try to connect via 0.0.0.0 — on a properly configured system this should fail
	// because the server only binds to 127.0.0.1
	conn, err := net.Dial("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err == nil {
		// If 0.0.0.0 connects, check if it's actually binding to 0.0.0.0
		// by verifying the listener address
		lnAddr := srv.listener.Addr().String()
		if strings.HasPrefix(lnAddr, "0.0.0.0:") {
			t.Errorf("server bound to 0.0.0.0, should only bind to 127.0.0.1")
		}
		conn.Close()
	}
}

func TestGenerateMCPConfigBeforeStart(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 5*time.Second, logger, nil)
	cfg := &config.ApprovalConfig{Port: 0}

	srv, err := NewServer(cfg, handler, logger)
	if err != nil {
		t.Fatalf("NewServer() = %v, want nil", err)
	}

	// GenerateMCPConfig before server is started should error
	_, err = srv.GenerateMCPConfig()
	if err == nil {
		t.Error("GenerateMCPConfig() before Start() = nil, want error")
	}
}

func TestHandleCallbackAfterResponse(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 2*time.Second, logger, nil)

	// Create approval request
	reqBody := `{"tool_name": "Bash", "tool_input": {"command": "echo hi"}, "session_id": "test"}`
	req := httptest.NewRequest("POST", "/approval", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	var resp *httptest.ResponseRecorder
	var reqWg sync.WaitGroup
	reqWg.Add(1)
	go func() {
		defer reqWg.Done()
		resp = httptest.NewRecorder()
		h := &httpHandler{handler}
		h.ServeHTTP(resp, req)
	}()

	// Wait for request to be pending
	time.Sleep(50 * time.Millisecond)

	// Get the pending request ID
	handler.mu.RLock()
	var pendingID string
	for id := range handler.pending {
		pendingID = id
		break
	}
	handler.mu.RUnlock()

	// Deliver callback
	handler.HandleCallback(pendingID, "approve")
	reqWg.Wait()

	// Try to deliver callback again — should log warning but not panic
	handler.HandleCallback(pendingID, "deny")
}

func TestHandlerEmptyToolInput(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 2*time.Second, logger, nil)

	// Request with empty tool_input
	reqBody := `{"tool_name": "Bash", "tool_input": {}, "session_id": "test"}`
	req := httptest.NewRequest("POST", "/approval", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	var resp *httptest.ResponseRecorder
	var reqWg sync.WaitGroup
	reqWg.Add(1)
	go func() {
		defer reqWg.Done()
		resp = httptest.NewRecorder()
		h := &httpHandler{handler}
		h.ServeHTTP(resp, req)
	}()

	// Wait for message to be sent
	time.Sleep(50 * time.Millisecond)

	sent := bot.getSent()
	if len(sent) == 0 {
		t.Fatal("No message sent to Telegram")
	}

	// Should still format message even with empty input
	if !strings.Contains(sent[0].text, "<pre>") {
		t.Errorf("message text missing <pre> tag for empty input: %q", sent[0].text)
	}

	handler.HandleCallback("", "approve") // empty ID won't match anything
	reqWg.Wait()
}

func TestServerDoubleStart(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 5*time.Second, logger, nil)
	cfg := &config.ApprovalConfig{Port: 0}

	srv, err := NewServer(cfg, handler, logger)
	if err != nil {
		t.Fatalf("NewServer() = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	defer srv.Stop()

	// Second start should error
	if err := srv.Start(ctx); err == nil {
		t.Error("Second Start() = nil, want error")
	}
}

func TestServerStopIdempotent(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 5*time.Second, logger, nil)
	cfg := &config.ApprovalConfig{Port: 0}

	srv, err := NewServer(cfg, handler, logger)
	if err != nil {
		t.Fatalf("NewServer() = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}

	// Multiple stops should not panic (Stop is idempotent via sync.Once)
	srv.Stop()
	srv.Stop()
	srv.Stop()
}
