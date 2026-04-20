// Package claude provides core engine components for the Craudinei session runner.
package claude

import (
	"fmt"
	"slices"

	"github.com/decko/craudinei/internal/types"
)

// validTransitions maps each source status to its allowed target statuses.
var validTransitions = map[types.SessionStatus][]types.SessionStatus{
	types.StatusIdle:            {types.StatusStarting},
	types.StatusStarting:        {types.StatusRunning, types.StatusCrashed},
	types.StatusRunning:         {types.StatusWaitingApproval, types.StatusStopping, types.StatusCrashed},
	types.StatusWaitingApproval: {types.StatusRunning, types.StatusStopping},
	types.StatusStopping:        {types.StatusIdle, types.StatusCrashed},
	types.StatusCrashed:         {types.StatusIdle},
}

// allowedCommands maps each status to its allowed commands.
var allowedCommands = map[types.SessionStatus][]string{
	types.StatusIdle:            {"/begin", "/resume", "/status", "/help", "/sessions"},
	types.StatusStarting:        {"/status", "/help"},
	types.StatusRunning:         {"/stop", "/cancel", "/status", "/help", "prompt"},
	types.StatusWaitingApproval: {"/stop", "/cancel", "/status", "/help", "prompt"},
	types.StatusStopping:        {"/status", "/help"},
	types.StatusCrashed:         {"/begin", "/resume", "/status", "/help", "/sessions"},
}

// StateMachine manages session state transitions with validation.
type StateMachine struct {
	state *types.SessionState
}

// NewStateMachine creates a new StateMachine with the given initial status.
func NewStateMachine(initialStatus types.SessionStatus) *StateMachine {
	sm := &StateMachine{
		state: types.NewSessionState(),
	}
	sm.state.SetStatus(initialStatus)
	return sm
}

// Transition validates and performs a state transition.
func (sm *StateMachine) Transition(target types.SessionStatus) error {
	current := sm.state.Status()
	if !sm.CanTransition(target) {
		return fmt.Errorf("cannot transition from %s to %s", current, target)
	}
	if !sm.state.TransitionStatus(current, target) {
		return fmt.Errorf("cannot transition from %s to %s (concurrent transition)", current, target)
	}
	return nil
}

// CanTransition checks if a transition is valid without performing it.
func (sm *StateMachine) CanTransition(target types.SessionStatus) bool {
	current := sm.state.Status()
	allowed, ok := validTransitions[current]
	if !ok {
		return false
	}
	return slices.Contains(allowed, target)
}

// CommandGuard validates a command against the current state.
func (sm *StateMachine) CommandGuard(command string) error {
	current := sm.state.Status()
	allowed, ok := allowedCommands[current]
	if !ok {
		return fmt.Errorf("command %s not allowed in state %s (allowed: none)", command, current)
	}
	if slices.Contains(allowed, command) {
		return nil
	}
	return fmt.Errorf("command %s not allowed in state %s (allowed: %v)", command, current, allowed)
}

// Status returns the current session status.
func (sm *StateMachine) Status() types.SessionStatus {
	return sm.state.Status()
}

// SessionState returns the internal session state for use by the event loop.
func (sm *StateMachine) SessionState() *types.SessionState {
	return sm.state
}

// AllowedCommands returns the list of allowed commands for the current state.
func (sm *StateMachine) AllowedCommands() []string {
	current := sm.state.Status()
	allowed, ok := allowedCommands[current]
	if !ok {
		return nil
	}
	result := make([]string, len(allowed))
	copy(result, allowed)
	return result
}
