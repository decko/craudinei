package bot

import (
	"crypto/subtle"
	"errors"
	"sync"
	"time"
)

var (
	// ErrLockedOut indicates the user is currently locked out due to too many
	// failed authentication attempts.
	ErrLockedOut = errors.New("authentication locked out due to too many failures")
)

// authSession tracks an authenticated user's session state.
type authSession struct {
	authenticatedAt time.Time
	lastActivity    time.Time
}

// authFailure tracks failed authentication attempts for rate limiting.
type authFailure struct {
	count    int
	firstAt  time.Time
	lockedAt time.Time
}

// Auth handles user authentication with whitelist checking, passphrase
// validation, rate limiting, and session management.
type Auth struct {
	mu                 sync.Mutex
	whitelist          map[int64]bool
	passphrase         string
	idleTimeout        time.Duration
	maxSessionDuration time.Duration
	sessions           map[int64]*authSession
	failures           map[int64]*authFailure
}

// NewAuth creates a new Auth instance with the given allowed users, passphrase,
// idle timeout, and maximum session duration for sessions.
func NewAuth(allowedUsers []int64, passphrase string, idleTimeout, maxSessionDuration time.Duration) *Auth {
	m := make(map[int64]bool, len(allowedUsers))
	for _, id := range allowedUsers {
		m[id] = true
	}
	// Cap max session duration at 4 hours
	if maxSessionDuration > 4*time.Hour {
		maxSessionDuration = 4 * time.Hour
	}
	return &Auth{
		whitelist:          m,
		passphrase:         passphrase,
		idleTimeout:        idleTimeout,
		maxSessionDuration: maxSessionDuration,
		sessions:           make(map[int64]*authSession),
		failures:           make(map[int64]*authFailure),
	}
}

// IsWhitelisted checks if a user ID is in the whitelist.
func (a *Auth) IsWhitelisted(userID int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.whitelist[userID]
}

// IsAuthenticated checks if a user has an active (non-expired) session.
func (a *Auth) IsAuthenticated(userID int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[userID]
	if !ok {
		return false
	}
	if time.Since(session.lastActivity) > a.idleTimeout {
		delete(a.sessions, userID)
		return false
	}
	if a.maxSessionDuration > 0 && time.Since(session.authenticatedAt) > a.maxSessionDuration {
		delete(a.sessions, userID)
		return false
	}
	return true
}

// TouchSession updates the last activity timestamp for an existing session.
// If no session exists, this is a no-op.
func (a *Auth) TouchSession(userID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if session, ok := a.sessions[userID]; ok {
		session.lastActivity = time.Now()
	}
}

// Authenticate validates the passphrase and manages authentication state.
// Returns (true, nil) on success, (false, nil) on wrong passphrase,
// and (false, ErrLockedOut) when locked out.
func (a *Auth) Authenticate(userID int64, passphrase string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.whitelist[userID] {
		return false, nil
	}

	if lockout := a.checkLockout(userID); lockout > 0 {
		return false, ErrLockedOut
	}

	if subtle.ConstantTimeCompare([]byte(passphrase), []byte(a.passphrase)) != 1 {
		a.recordFailure(userID)
		if lockout := a.checkLockout(userID); lockout > 0 {
			return false, ErrLockedOut
		}
		return false, nil
	}

	a.clearFailures(userID)
	now := time.Now()
	a.sessions[userID] = &authSession{
		authenticatedAt: now,
		lastActivity:    now,
	}
	return true, nil
}

// RevokeAll clears all active sessions. Use as a kill switch.
func (a *Auth) RevokeAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions = make(map[int64]*authSession)
}

// lockDuration returns the lockout duration based on the number of failures.
func (a *Auth) lockDuration(failCount int) time.Duration {
	switch {
	case failCount >= 9:
		return 4 * time.Hour
	case failCount >= 6:
		return 1 * time.Hour
	case failCount >= 3:
		return 30 * time.Minute
	default:
		return 0
	}
}

func (a *Auth) checkLockout(userID int64) time.Duration {
	failure, ok := a.failures[userID]
	if !ok {
		return 0
	}
	// If locked out, check if lockout has expired
	if !failure.lockedAt.IsZero() {
		duration := a.lockDuration(failure.count)
		if time.Since(failure.lockedAt) < duration {
			return duration - time.Since(failure.lockedAt)
		}
		// Lockout expired, reset
		delete(a.failures, userID)
		return 0
	}
	// Not locked out, check if 5-minute window has passed
	if time.Since(failure.firstAt) > 5*time.Minute {
		delete(a.failures, userID)
		return 0
	}
	return 0
}

func (a *Auth) recordFailure(userID int64) {
	failure, ok := a.failures[userID]
	if !ok {
		failure = &authFailure{
			firstAt: time.Now(),
		}
		a.failures[userID] = failure
	}
	failure.count++
	if a.lockDuration(failure.count) > 0 && failure.lockedAt.IsZero() {
		failure.lockedAt = time.Now()
	}
}

func (a *Auth) clearFailures(userID int64) {
	delete(a.failures, userID)
}
