// Package approval provides a localhost HTTP server that acts as a
// permission-prompt-tool endpoint for Claude Code and bridges approval
// requests to Telegram via inline buttons.
package approval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/decko/craudinei/internal/config"
)

// Server manages the HTTP server for handling approval requests from Claude Code.
type Server struct {
	listener net.Listener
	handler  *Handler
	cfg      *config.ApprovalConfig
	logger   *slog.Logger
	srv      *http.Server
	done     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewServer creates a new approval server with the given configuration,
// handler, and logger. The port is validated: 0 means auto-assign (ephemeral),
// otherwise must be in the range 1024-65535.
func NewServer(cfg *config.ApprovalConfig, handler *Handler, logger *slog.Logger) (*Server, error) {
	if cfg == nil {
		return nil, fmt.Errorf("approval: nil config")
	}
	if handler == nil {
		return nil, fmt.Errorf("approval: nil handler")
	}
	if logger == nil {
		return nil, fmt.Errorf("approval: nil logger")
	}
	// Port 0 means auto-assign (ephemeral port)
	if cfg.Port != 0 && (cfg.Port < 1024 || cfg.Port > 65535) {
		return nil, fmt.Errorf("approval: port must be 0 or 1024-65535, got %d", cfg.Port)
	}

	return &Server{
		handler: handler,
		cfg:     cfg,
		logger:  logger,
		done:    make(chan struct{}),
	}, nil
}

// Start begins listening on localhost only (127.0.0.1) at the configured port.
// If port is 0, an ephemeral port is automatically assigned.
func (s *Server) Start(ctx context.Context) error {
	if s.listener != nil {
		return fmt.Errorf("approval: server already started")
	}

	addr := fmt.Sprintf("127.0.0.1:%d", s.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("approval: listening: %w", err)
	}
	s.listener = ln

	s.srv = &http.Server{
		Handler: s.handler,
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// Serve requests until context is cancelled or Stop is called
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("approval server error", "err", err)
		}
	}()

	s.logger.Info("approval server listening", "addr", ln.Addr().String())
	return nil
}

// Port returns the actual port the server is listening on.
// Useful for MCP config generation when port is auto-assigned (0).
func (s *Server) Port() int {
	if s.listener == nil {
		return 0
	}
	return s.listener.Addr().(*net.TCPAddr).Port
}

// Stop gracefully shuts down the server with a timeout.
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
		if s.srv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.srv.Shutdown(ctx)
		}
		if s.listener != nil {
			_ = s.listener.Close()
		}
		s.wg.Wait()
	})
}

// GenerateMCPConfig produces the MCP server configuration JSON that tells
// Claude Code how to connect to this approval server.
func (s *Server) GenerateMCPConfig() (string, error) {
	if s.listener == nil {
		return "", fmt.Errorf("approval: server not started")
	}

	port := s.Port()
	cfg := struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}{
		MCPServers: map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}{
			"approval": {
				Command: "curl",
				Args: []string{
					"-X", "POST",
					"-H", "Content-Type: application/json",
					"-d", "@-",
					fmt.Sprintf("http://127.0.0.1:%d/approval", port),
				},
			},
		},
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("approval: marshalling MCP config: %w", err)
	}
	return string(data), nil
}
