package upgrade

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// CutoverState tracks which instance ("old" or "new") is currently the
// active target, plus what it was before, so a single rollback can always
// undo the last cutover.
//
// This is intentionally a local marker file, not a real connection-routing
// mechanism — in production this step is "reconfigure PgBouncer/HAProxy (or
// your app's connection string) to point at the new primary." Faking that
// convincingly would need a real proxy in the demo stack for no real payoff;
// what actually needs proving here is the decision procedure (validate, then
// commit, with an undo), not the network plumbing. See docs/architecture.md.
type CutoverState struct {
	Active    string    `json:"active"`
	Previous  string    `json:"previous"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ReadState loads the cutover state file, defaulting to "old" active with no
// history if it doesn't exist yet (a fresh cluster before its first upgrade).
func ReadState(path string) (*CutoverState, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &CutoverState{Active: "old"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading cutover state %s: %w", path, err)
	}
	var s CutoverState
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parsing cutover state %s: %w", path, err)
	}
	return &s, nil
}

func writeState(path string, s *CutoverState) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling cutover state: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("writing cutover state %s: %w", path, err)
	}
	return nil
}

// Cutover marks `to` ("old" or "new") as the active target, recording the
// previous active target so Rollback can undo it.
func Cutover(path, to string) (*CutoverState, error) {
	if to != "old" && to != "new" {
		return nil, fmt.Errorf("cutover target must be \"old\" or \"new\", got %q", to)
	}
	current, err := ReadState(path)
	if err != nil {
		return nil, err
	}
	next := &CutoverState{Active: to, Previous: current.Active, UpdatedAt: time.Now().UTC()}
	if err := writeState(path, next); err != nil {
		return nil, err
	}
	return next, nil
}

// Rollback reverts the most recent Cutover.
func Rollback(path string) (*CutoverState, error) {
	current, err := ReadState(path)
	if err != nil {
		return nil, err
	}
	if current.Previous == "" {
		return nil, fmt.Errorf("no prior cutover to roll back to")
	}
	next := &CutoverState{Active: current.Previous, Previous: current.Active, UpdatedAt: time.Now().UTC()}
	if err := writeState(path, next); err != nil {
		return nil, err
	}
	return next, nil
}
