package approval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/decko/craudinei/internal/audit"
	"github.com/decko/craudinei/internal/bot"
)

// ApprovalRequest represents a pending approval request awaiting user response.
type ApprovalRequest struct {
	ID        string
	ToolName  string
	Input     json.RawMessage
	Response  chan *ApprovalResponse
	CreatedAt time.Time
}

// ApprovalResponse contains the user's decision on an approval request.
type ApprovalResponse struct {
	Decision string // "approve", "deny", or "timeout"
	Reason   string
}

// BotSender defines the interface for sending messages via the bot.
type BotSender interface {
	Send(ctx context.Context, chatID int64, text string, parseMode string) (any, error)
}

// Handler manages approval requests and routes them to Telegram.
type Handler struct {
	pending     map[string]*ApprovalRequest
	mu          sync.RWMutex
	chatID      int64
	bot         BotSender
	timeout     time.Duration
	logger      *slog.Logger
	auditLogger *audit.Logger
	idCounter   uint64
}

// NewHandler creates a new approval handler that sends approval requests
// to the specified Telegram chat and blocks until the user responds or
// the timeout fires.
func NewHandler(chatID int64, bot BotSender, timeout time.Duration, logger *slog.Logger, auditLogger *audit.Logger) *Handler {
	if bot == nil {
		panic("approval: nil bot")
	}
	if timeout <= 0 {
		panic("approval: timeout must be positive")
	}
	return &Handler{
		pending:     make(map[string]*ApprovalRequest),
		chatID:      chatID,
		bot:         bot,
		timeout:     timeout,
		logger:      logger,
		auditLogger: auditLogger,
	}
}

// HandleApproval is the HTTP handler for approval requests from Claude Code.
// It accepts POST requests with JSON body: {"tool_name": "...", "tool_input": {...}, "session_id": "..."}
// The handler blocks until the user responds or the timeout fires.
func (h *Handler) HandleApproval(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
		SessionID string          `json:"session_id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Warn("approval: malformed request", "err", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Generate unique ID for this request
	id := fmt.Sprintf("%d", atomic.AddUint64(&h.idCounter, 1))

	approvalReq := &ApprovalRequest{
		ID:        id,
		ToolName:  req.ToolName,
		Input:     req.ToolInput,
		Response:  make(chan *ApprovalResponse, 1),
		CreatedAt: time.Now(),
	}
	h.mu.Lock()
	h.pending[id] = approvalReq
	h.mu.Unlock()

	// Send Telegram message with inline buttons
	h.sendApprovalMessage(req.ToolName, req.ToolInput, id)

	// Wait for response or timeout
	var resp *ApprovalResponse
	select {
	case resp = <-approvalReq.Response:
		// Response received
	case <-time.After(h.timeout):
		resp = &ApprovalResponse{
			Decision: "timeout",
			Reason:   "Timed out — auto-denied",
		}
		// Edit the Telegram message to indicate timeout
		h.editTimeoutMessage(id)
	}

	// Audit the tool decision
	if h.auditLogger != nil {
		outcome := resp.Decision
		if outcome == "approve" {
			outcome = "approved"
		} else if outcome == "deny" {
			outcome = "denied"
		}
		runes := []rune(string(req.ToolInput))
		inputSummary := string(runes)
		if len(runes) > 200 {
			inputSummary = string(runes[:200])
		}
		h.auditLogger.ToolDecision(h.chatID, req.ToolName, inputSummary, outcome, "")
	}

	// Remove from pending map
	h.mu.Lock()
	delete(h.pending, id)
	h.mu.Unlock()

	h.writeResponse(w, resp)
}

// HandleCallback is called by the Telegram callback handler when the user
// presses an inline button. It finds the pending request by ID and sends
// the decision on the response channel.
func (h *Handler) HandleCallback(callbackData string, decision string) {
	h.mu.RLock()
	req, ok := h.pending[callbackData]
	h.mu.RUnlock()
	if !ok {
		h.logger.Warn("approval: callback for unknown request", "id", callbackData)
		return
	}

	reason := "User " + decision
	resp := &ApprovalResponse{
		Decision: decision,
		Reason:   reason,
	}

	select {
	case req.Response <- resp:
		// Response sent successfully
	default:
		h.logger.Warn("approval: response channel already sent", "id", callbackData)
	}
}

// ServeHTTP implements http.Handler for the Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.HandleApproval(w, r)
}

// Cleanup removes expired pending requests that have been pending for
// longer than the configured timeout.
func (h *Handler) Cleanup() {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	for id, req := range h.pending {
		if now.Sub(req.CreatedAt) > h.timeout {
			select {
			case req.Response <- &ApprovalResponse{Decision: "timeout", Reason: "Expired"}:
			default:
			}
			delete(h.pending, id)
		}
	}
}

func (h *Handler) writeResponse(w http.ResponseWriter, resp *ApprovalResponse) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) sendApprovalMessage(toolName string, toolInput json.RawMessage, id string) {
	// Format the tool input for display
	var inputPretty string
	if len(toolInput) > 0 {
		var m map[string]any
		if err := json.Unmarshal(toolInput, &m); err == nil {
			data, _ := json.MarshalIndent(m, "", "  ")
			inputPretty = string(data)
		} else {
			inputPretty = string(toolInput)
		}
	}

	text := fmt.Sprintf(
		"<b>Approval Required</b>\n\n<b>Tool:</b> %s\n<b>Input:</b>\n<pre>%s</pre>\n\nReact to approve/deny.",
		bot.EscapeHTML(toolName),
		bot.EscapeHTML(inputPretty),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := h.bot.Send(ctx, h.chatID, text, "HTML")
	if err != nil {
		h.logger.Error("approval: sending approval message", "err", err)
	}
}

func (h *Handler) editTimeoutMessage(id string) {
	// In a real implementation, we would edit the original message
	// to show "(Timed out — auto-denied)". For now, we just log it.
	h.logger.Info("approval: request timed out", "id", id)
}
