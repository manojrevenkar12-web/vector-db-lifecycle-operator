package lab

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/plan"
)

func TestFleetIsTheSameForEveryDesign(t *testing.T) {
	w, err := LoadWorld("dev")
	if err != nil {
		t.Fatal(err)
	}
	a, b := Fleet(w, 11), Fleet(w, 11)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("the fleet must depend on the seed only")
	}
	if reflect.DeepEqual(a, Fleet(w, 12)) {
		t.Fatal("a different seed should give a different fleet")
	}
}

func TestOperatorUpgradesACalmClusterWithoutDowntime(t *testing.T) {
	w, _ := LoadWorld("dev")
	w.Faults.CrashPerNodeDay, w.Faults.APIFailure, w.Faults.TransferFailure, w.Faults.BackupFailure = 0, 0, 0, 0
	w.Minutes = 1440
	for _, cp := range Fleet(w, 11)[:6] {
		r := RunCluster(w, 11, cp, plan.Safe())
		if r.UnavailableByOps != 0 || r.ShardsLostAtEnd != 0 {
			t.Fatalf("%s: %+v", cp.ID, r.Stats)
		}
		if len(cp.Changes) > 0 && r.ChangesCompleted != r.ChangesRequested {
			t.Fatalf("%s: %d of %d changes", cp.ID, r.ChangesCompleted, r.ChangesRequested)
		}
	}
}

func TestAnAblationThatChangesNothingFailsTheGate(t *testing.T) {
	g, _ := LoadGates()
	same := Summary{ChangesCompletedShare: 1, DRRecoveredShare: 1, DRWorstRPOMinutes: 300}
	r := SplitResult{Ablations: []string{"no_x"}, Designs: map[string]Summary{"operator": same, "as_is": {LossEventsReplicated: 3, UnavailableByOps: 100}, "no_x": same}}
	gr := CheckGates(r, g)
	if gr.Pass {
		t.Fatal("an ablation equal to the operator must fail the each-safeguard-matters gate")
	}
	worse := same
	worse.LossEvents = 1
	r.Designs["no_x"] = worse
	if !CheckGates(r, g).Pass {
		t.Fatalf("%+v", CheckGates(r, g))
	}
}

func TestHeldoutRunsOnce(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"designs.json", "gates.json", "seeds.json", "world.json"} {
		b, err := os.ReadFile(filepath.Join(Root(), "config", f))
		if err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, "config", f), b, 0o644)
	}
	_ = os.MkdirAll(filepath.Join(dir, "reports"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "reports", "heldout.lock"), []byte("{}"), 0o644)
	t.Setenv("OPERATOR_ROOT", dir)
	if _, err := RunHeldoutOnce(); !errors.Is(err, ErrLocked) {
		t.Fatalf("want ErrLocked, got %v", err)
	}
}
