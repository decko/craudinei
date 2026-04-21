package approval

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/decko/craudinei/internal/config"
)

func TestServerStartStop(t *testing.T) {
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

	port := srv.Port()
	if port == 0 {
		t.Error("Port() = 0, want non-zero port")
	}
	if port < 1024 || port > 65535 {
		t.Errorf("Port() = %d, want 1024-65535", port)
	}

	srv.Stop()
}

func TestServerPortAssignment(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name   string
		port   int
		wantOk bool
	}{
		{"auto assign", 0, true},
		{"ephemeral range", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bot := &fakeBotSender{}
			handler := NewHandler(123, bot, 5*time.Second, logger, nil)
			cfg := &config.ApprovalConfig{Port: tt.port}

			srv, err := NewServer(cfg, handler, logger)
			if err != nil {
				t.Fatalf("NewServer() = %v, want nil", err)
			}

			ctx, cancel := context.WithCancel(context.Background())

			err = srv.Start(ctx)
			if (err == nil) != tt.wantOk {
				t.Errorf("Start() error = %v, wantOk %v", err, tt.wantOk)
			}

			cancel()
			srv.Stop()
		})
	}
}

func TestServerLocalhostOnly(t *testing.T) {
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

	// Verify the port was assigned
	port := srv.Port()
	if port == 0 {
		t.Fatal("Port() = 0, want non-zero port")
	}

	// Verify connecting to localhost works
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Errorf("Server not accepting connections on 127.0.0.1: %v", err)
	} else {
		conn.Close()
	}
}

func TestMCPConfigGeneration(t *testing.T) {
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

	// Verify the config contains the port
	port := srv.Port()
	if port == 0 {
		t.Error("Port() = 0 before GenerateMCPConfig, want non-zero")
	}

	// Should contain the port in the args
	expectedPort := fmt.Sprintf("%d", port)
	if !strings.Contains(cfgOut, expectedPort) {
		t.Errorf("GenerateMCPConfig() = %s, want to contain port %s", cfgOut, expectedPort)
	}

	// Should use craudinei server name
	if !strings.Contains(cfgOut, `"craudinei"`) {
		t.Errorf("GenerateMCPConfig() = %s, want to contain craudinei server", cfgOut)
	}
}

func TestServerInvalidPort(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 5*time.Second, logger, nil)

	tests := []struct {
		name string
		port int
	}{
		{"too low", 1023},
		{"too high", 65536},
		{"negative", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.ApprovalConfig{Port: tt.port}
			_, err := NewServer(cfg, handler, logger)
			if err == nil {
				t.Errorf("NewServer(port=%d) = nil, want error", tt.port)
			}
		})
	}
}

func TestConcurrentRequests(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	// Short timeout so requests don't hang
	handler := NewHandler(123, bot, 100*time.Millisecond, logger, nil)
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

	// Send multiple concurrent requests - they will all timeout but that's OK
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/approval", srv.Port()), nil)
			req.Header.Set("Content-Type", "application/json")
			client := &http.Client{Timeout: 1 * time.Second}
			_, _ = client.Do(req)
		}(i)
	}
	wg.Wait()
}

func TestServerNilConfig(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 5*time.Second, logger, nil)

	_, err := NewServer(nil, handler, logger)
	if err == nil {
		t.Error("NewServer(nil config) = nil, want error")
	}
}

func TestServerNilHandler(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.ApprovalConfig{Port: 0}

	_, err := NewServer(cfg, nil, logger)
	if err == nil {
		t.Error("NewServer(nil handler) = nil, want error")
	}
}

func TestServerNilLogger(t *testing.T) {
	t.Parallel()

	bot := &fakeBotSender{}
	handler := NewHandler(123, bot, 5*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	cfg := &config.ApprovalConfig{Port: 0}

	_, err := NewServer(cfg, handler, nil)
	if err == nil {
		t.Error("NewServer(nil logger) = nil, want error")
	}
}
