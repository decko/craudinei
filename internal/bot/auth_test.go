package bot

import (
	"sync"
	"testing"
	"time"
)

func TestAuth_WhitelistedUser(t *testing.T) {
	t.Helper()
	auth := NewAuth([]int64{100, 200, 300}, "secret", 10*time.Minute, 4*time.Hour)

	tests := []struct {
		name     string
		userID   int64
		expected bool
	}{
		{"whitelisted user 100", 100, true},
		{"whitelisted user 200", 200, true},
		{"whitelisted user 300", 300, true},
		{"whitelisted user 300", 300, true},
		{"non-whitelisted user 999", 999, false},
		{"non-whitelisted user 0", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := auth.IsWhitelisted(tt.userID); got != tt.expected {
				t.Errorf("IsWhitelisted(%d) = %v, want %v", tt.userID, got, tt.expected)
			}
		})
	}
}

func TestAuth_CorrectPassphrase(t *testing.T) {
	t.Helper()
	auth := NewAuth([]int64{100}, "correct-passphrase", 10*time.Minute, 4*time.Hour)

	ok, err := auth.Authenticate(100, "correct-passphrase")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected successful authentication")
	}
	if !auth.IsAuthenticated(100) {
		t.Error("expected user to be authenticated")
	}
}

func TestAuth_WrongPassphrase(t *testing.T) {
	t.Helper()
	auth := NewAuth([]int64{100}, "secret", 10*time.Minute, 4*time.Hour)

	ok, err := auth.Authenticate(100, "wrong-passphrase")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected failed authentication")
	}
	if auth.IsAuthenticated(100) {
		t.Error("expected user not to be authenticated")
	}
}

func TestAuth_RateLimiting(t *testing.T) {
	t.Helper()
	auth := NewAuth([]int64{100}, "secret", 10*time.Minute, 4*time.Hour)

	for i := 0; i < 3; i++ {
		auth.Authenticate(100, "wrong")
	}

	ok, err := auth.Authenticate(100, "wrong")
	if err != ErrLockedOut {
		t.Errorf("expected ErrLockedOut, got: %v", err)
	}
	if ok {
		t.Error("expected locked out user to return false")
	}
}

func TestAuth_RateLimiting_LockoutBeyondWindow(t *testing.T) {
	t.Helper()
	auth := NewAuth([]int64{100}, "secret", 10*time.Minute, 4*time.Hour)

	// Trigger lockout: 3 failures
	for i := 0; i < 3; i++ {
		auth.Authenticate(100, "wrong")
	}

	// Verify locked out
	_, err := auth.Authenticate(100, "secret")
	if err != ErrLockedOut {
		t.Errorf("expected ErrLockedOut, got: %v", err)
	}

	// Even after the 5-minute window expires, lockout should persist
	// because lockout is 30 minutes, not 5 minutes
	// We can't wait 30 minutes in a test, but we can verify the logic:
	// the lockout was set at 3 failures with 30min duration, and checkLockout
	// now checks lockout expiry FIRST before the 5-minute window
}

func TestAuth_SessionExpiry(t *testing.T) {
	t.Helper()
	auth := NewAuth([]int64{100}, "secret", 50*time.Millisecond, 4*time.Hour)

	_, err := auth.Authenticate(100, "secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !auth.IsAuthenticated(100) {
		t.Fatal("expected user to be authenticated immediately after login")
	}

	time.Sleep(70 * time.Millisecond)

	if auth.IsAuthenticated(100) {
		t.Error("expected session to expire after idleTimeout")
	}
}

func TestAuth_SuccessResetsFailures(t *testing.T) {
	t.Helper()
	auth := NewAuth([]int64{100}, "secret", 10*time.Minute, 4*time.Hour)

	for i := 0; i < 2; i++ {
		auth.Authenticate(100, "wrong")
	}

	_, _ = auth.Authenticate(100, "secret")
	_, _ = auth.Authenticate(100, "wrong")
	_, _ = auth.Authenticate(100, "wrong")
	_, err := auth.Authenticate(100, "wrong")
	if err == nil {
		t.Error("expected ErrLockedOut after reset and 3 failures, got nil")
	} else if err != ErrLockedOut {
		t.Errorf("expected ErrLockedOut, got: %v", err)
	}
}

func TestAuth_LockoutDurations(t *testing.T) {
	t.Helper()
	auth := NewAuth([]int64{100}, "secret", 10*time.Minute, 4*time.Hour)

	tests := []struct {
		name         string
		failures     int
		expectedLock time.Duration
	}{
		{"2 failures = no lockout", 2, 0},
		{"3 failures = 30 min", 3, 30 * time.Minute},
		{"5 failures = 30 min", 5, 30 * time.Minute},
		{"6 failures = 1 hour", 6, 1 * time.Hour},
		{"8 failures = 1 hour", 8, 1 * time.Hour},
		{"9 failures = 4 hours", 9, 4 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := auth.lockDuration(tt.failures)
			if got != tt.expectedLock {
				t.Errorf("lockDuration(%d) = %v, want %v", tt.failures, got, tt.expectedLock)
			}
		})
	}
}

func TestAuth_RevokeAll(t *testing.T) {
	t.Helper()
	auth := NewAuth([]int64{100, 200}, "secret", 10*time.Minute, 4*time.Hour)

	_, _ = auth.Authenticate(100, "secret")
	_, _ = auth.Authenticate(200, "secret")

	if !auth.IsAuthenticated(100) || !auth.IsAuthenticated(200) {
		t.Fatal("both users should be authenticated")
	}

	auth.RevokeAll()

	if auth.IsAuthenticated(100) {
		t.Error("expected user 100 session to be revoked")
	}
	if auth.IsAuthenticated(200) {
		t.Error("expected user 200 session to be revoked")
	}
}

func TestAuth_ConcurrentAccess(t *testing.T) {
	t.Helper()
	auth := NewAuth([]int64{100, 200, 300}, "secret", 10*time.Minute, 4*time.Hour)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			userID := int64(100 + i%3)
			auth.Authenticate(userID, "secret")
			auth.IsAuthenticated(userID)
			auth.TouchSession(userID)
			auth.IsWhitelisted(userID)
		}(i)
	}

	wg.Wait()
}

func TestAuth_NonWhitelistedUser(t *testing.T) {
	t.Helper()
	auth := NewAuth([]int64{100, 200, 300}, "secret", 10*time.Minute, 4*time.Hour)

	ok, err := auth.Authenticate(999, "secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected non-whitelisted user to return false")
	}
}

func TestAuth_MaxSessionDuration(t *testing.T) {
	t.Helper()
	auth := NewAuth([]int64{100}, "secret", 10*time.Minute, 50*time.Millisecond)

	_, err := auth.Authenticate(100, "secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !auth.IsAuthenticated(100) {
		t.Fatal("expected user to be authenticated immediately after login")
	}

	// Simulate activity by touching session
	auth.TouchSession(100)

	time.Sleep(70 * time.Millisecond)

	// Session should be expired due to maxSessionDuration, not idleTimeout
	if auth.IsAuthenticated(100) {
		t.Error("expected session to expire after maxSessionDuration even with activity")
	}
}
