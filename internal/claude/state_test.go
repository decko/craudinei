package claude

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/decko/craudinei/internal/types"
)

func TestTransition_ValidTransitions(t *testing.T) {
	t.Parallel()

	validCases := []struct {
		name string
		from types.SessionStatus
		to   types.SessionStatus
	}{
		{"idle to starting", types.StatusIdle, types.StatusStarting},
		{"starting to running", types.StatusStarting, types.StatusRunning},
		{"starting to crashed", types.StatusStarting, types.StatusCrashed},
		{"running to waiting_approval", types.StatusRunning, types.StatusWaitingApproval},
		{"running to stopping", types.StatusRunning, types.StatusStopping},
		{"running to crashed", types.StatusRunning, types.StatusCrashed},
		{"waiting_approval to running", types.StatusWaitingApproval, types.StatusRunning},
		{"waiting_approval to stopping", types.StatusWaitingApproval, types.StatusStopping},
		{"stopping to idle", types.StatusStopping, types.StatusIdle},
		{"stopping to crashed", types.StatusStopping, types.StatusCrashed},
		{"crashed to idle", types.StatusCrashed, types.StatusIdle},
	}

	for _, tc := range validCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sm := NewStateMachine(tc.from)
			err := sm.Transition(tc.to)
			if err != nil {
				t.Errorf("Transition(%s → %s) unexpected error: %v", tc.from, tc.to, err)
			}
			if got := sm.Status(); got != tc.to {
				t.Errorf("Status after transition = %s, want %s", got, tc.to)
			}
		})
	}
}

func TestTransition_InvalidTransitions(t *testing.T) {
	t.Parallel()

	invalidCases := []struct {
		name string
		from types.SessionStatus
		to   types.SessionStatus
	}{
		{"idle to running", types.StatusIdle, types.StatusRunning},
		{"idle to stopping", types.StatusIdle, types.StatusStopping},
		{"running to starting", types.StatusRunning, types.StatusStarting},
		{"running to idle", types.StatusRunning, types.StatusIdle},
		{"crashed to running", types.StatusCrashed, types.StatusRunning},
		{"crashed to starting", types.StatusCrashed, types.StatusStarting},
		{"waiting_approval to crashed", types.StatusWaitingApproval, types.StatusCrashed},
		{"starting to idle", types.StatusStarting, types.StatusIdle},
	}

	for _, tc := range invalidCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sm := NewStateMachine(tc.from)
			err := sm.Transition(tc.to)
			if err == nil {
				t.Errorf("Transition(%s → %s) expected error, got nil", tc.from, tc.to)
			}
			if got := sm.Status(); got != tc.from {
				t.Errorf("Status after failed transition = %s, want %s", got, tc.from)
			}
		})
	}
}

func TestTransition_ConcurrentAccess(t *testing.T) {
	sm := NewStateMachine(types.StatusIdle)
	const goroutines = 10
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// Alternate between valid transitions: idle→starting, starting→running
				// and then reverse: running→stopping, stopping→idle
				seq := (i + j) % 4
				switch seq {
				case 0:
					_ = sm.Transition(types.StatusStarting)
				case 1:
					_ = sm.Transition(types.StatusRunning)
				case 2:
					_ = sm.Transition(types.StatusStopping)
				case 3:
					_ = sm.Transition(types.StatusIdle)
				}
			}
		}()
	}

	wg.Wait()
}

func TestCommandGuard_AllowedCommands(t *testing.T) {
	t.Parallel()

	cases := []struct {
		state    types.SessionStatus
		commands []string
	}{
		{
			types.StatusIdle,
			[]string{"/begin", "/resume", "/status", "/help", "/sessions"},
		},
		{
			types.StatusStarting,
			[]string{"/status", "/help"},
		},
		{
			types.StatusRunning,
			[]string{"/stop", "/cancel", "/status", "/help"},
		},
		{
			types.StatusWaitingApproval,
			[]string{"/stop", "/cancel", "/status", "/help"},
		},
		{
			types.StatusStopping,
			[]string{"/status", "/help"},
		},
		{
			types.StatusCrashed,
			[]string{"/begin", "/resume", "/status", "/help", "/sessions"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.state.String(), func(t *testing.T) {
			t.Parallel()
			sm := NewStateMachine(tc.state)

			for _, cmd := range tc.commands {
				cmd := cmd
				t.Run(cmd, func(t *testing.T) {
					if err := sm.CommandGuard(cmd); err != nil {
						t.Errorf("CommandGuard(%s) in state %s unexpected error: %v", cmd, tc.state, err)
					}
				})
			}
		})
	}
}

func TestCommandGuard_DisallowedCommands(t *testing.T) {
	t.Parallel()

	disallowedCases := []struct {
		state       types.SessionStatus
		command     string
		expectInMsg string
	}{
		{types.StatusIdle, "/stop", "/stop"},
		{types.StatusIdle, "/cancel", "/cancel"},
		{types.StatusRunning, "/begin", "/begin"},
		{types.StatusRunning, "/resume", "/resume"},
		{types.StatusRunning, "/sessions", "/sessions"},
		{types.StatusStarting, "/begin", "/begin"},
		{types.StatusStarting, "/stop", "/stop"},
		{types.StatusStarting, "/cancel", "/cancel"},
		{types.StatusWaitingApproval, "/begin", "/begin"},
		{types.StatusWaitingApproval, "/resume", "/resume"},
		{types.StatusWaitingApproval, "/sessions", "/sessions"},
		{types.StatusStopping, "/begin", "/begin"},
		{types.StatusStopping, "/stop", "/stop"},
		{types.StatusStopping, "/cancel", "/cancel"},
		{types.StatusStopping, "/resume", "/resume"},
		{types.StatusCrashed, "/stop", "/stop"},
		{types.StatusCrashed, "/cancel", "/cancel"},
	}

	for _, tc := range disallowedCases {
		tc := tc
		t.Run(fmt.Sprintf("%s/%s", tc.state, tc.command), func(t *testing.T) {
			t.Parallel()
			sm := NewStateMachine(tc.state)
			err := sm.CommandGuard(tc.command)
			if err == nil {
				t.Errorf("CommandGuard(%s) in state %s expected error, got nil", tc.command, tc.state)
				return
			}
			if !strings.Contains(err.Error(), tc.expectInMsg) {
				t.Errorf("error message = %q, want to contain %q", err.Error(), tc.expectInMsg)
			}
			if !strings.Contains(err.Error(), string(tc.state)) {
				t.Errorf("error message = %q, want to contain %q", err.Error(), tc.state)
			}
		})
	}
}

func TestCanTransition(t *testing.T) {
	t.Parallel()

	// Test specific valid/invalid transitions for CanTransition
	smCan := NewStateMachine(types.StatusIdle)
	if !smCan.CanTransition(types.StatusStarting) {
		t.Error("CanTransition(idle → starting) = false, want true")
	}
	if smCan.CanTransition(types.StatusRunning) {
		t.Error("CanTransition(idle → running) = true, want false")
	}
	if smCan.CanTransition(types.StatusCrashed) {
		t.Error("CanTransition(idle → crashed) = true, want false")
	}

	// crashed→idle is valid
	smCrashed := NewStateMachine(types.StatusCrashed)
	if !smCrashed.CanTransition(types.StatusIdle) {
		t.Error("CanTransition(crashed → idle) = false, want true")
	}
	if smCrashed.CanTransition(types.StatusRunning) {
		t.Error("CanTransition(crashed → running) = true, want false")
	}

	// Verify CanTransition and Transition agree for valid transitions
	smAgree := NewStateMachine(types.StatusIdle)
	for _, target := range []types.SessionStatus{
		types.StatusStarting, types.StatusRunning, types.StatusWaitingApproval,
		types.StatusStopping, types.StatusIdle,
	} {
		target := target
		t.Run(target.String(), func(t *testing.T) {
			if smAgree.CanTransition(target) {
				if err := smAgree.Transition(target); err != nil {
					t.Errorf("CanTransition returned true but Transition failed: %v", err)
				}
			}
			// Reset to idle for next iteration
			_ = smAgree.Transition(types.StatusIdle)
		})
	}
}

func TestAllowedCommands_ReturnsCopy(t *testing.T) {
	t.Parallel()

	sm := NewStateMachine(types.StatusIdle)
	original := sm.AllowedCommands()

	// Mutate the returned slice
	original = append(original, "/destroy-everything")

	// Verify internal state is unchanged
	after := sm.AllowedCommands()
	if len(after) != len([]string{"/begin", "/resume", "/status", "/help", "/sessions"}) {
		t.Errorf("AllowedCommands length = %d, want %d", len(after), 5)
	}
}
