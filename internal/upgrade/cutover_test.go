package upgrade

import (
	"path/filepath"
	"testing"
)

func TestCutoverAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cutover-state.json")

	// A fresh cluster defaults to "old" active with nothing to roll back to.
	state, err := ReadState(path)
	if err != nil {
		t.Fatalf("ReadState on missing file: %v", err)
	}
	if state.Active != "old" {
		t.Fatalf("expected default active = old, got %q", state.Active)
	}

	state, err = Cutover(path, "new")
	if err != nil {
		t.Fatalf("Cutover: %v", err)
	}
	if state.Active != "new" || state.Previous != "old" {
		t.Fatalf("unexpected state after cutover: %+v", state)
	}

	reloaded, err := ReadState(path)
	if err != nil {
		t.Fatalf("ReadState after cutover: %v", err)
	}
	if reloaded.Active != "new" {
		t.Fatalf("cutover state was not persisted, got active=%q", reloaded.Active)
	}

	rolledBack, err := Rollback(path)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if rolledBack.Active != "old" {
		t.Fatalf("expected rollback to restore active=old, got %q", rolledBack.Active)
	}
}

func TestCutover_RejectsInvalidTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cutover-state.json")
	if _, err := Cutover(path, "sideways"); err == nil {
		t.Fatalf("expected an error for an invalid cutover target")
	}
}

func TestRollback_ErrorsWithNoHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cutover-state.json")
	if _, err := Rollback(path); err == nil {
		t.Fatalf("expected an error rolling back a cluster that was never cut over")
	}
}
