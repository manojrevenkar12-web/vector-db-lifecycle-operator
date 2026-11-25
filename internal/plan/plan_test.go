package plan

import (
	"testing"

	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/model"
)

func nodes(version string, n int) []model.Node {
	out := make([]model.Node, n)
	for i := range out {
		out[i] = model.Node{Name: NodeName(i), Ordinal: i, Peer: uint64(i + 1), Version: version, Ready: true}
	}
	return out
}

func shard(id int, peers ...uint64) model.Shard {
	s := model.Shard{Collection: "c", ID: id}
	for _, p := range peers {
		s.Replicas = append(s.Replicas, model.Replica{Peer: p, State: model.Active})
	}
	return s
}

func spec(v string, n, rf int) model.Spec {
	return model.Spec{Version: v, Nodes: n, ReplicationFactor: rf}
}

func only(t *testing.T, d model.Decision, kind model.ActionKind) model.Action {
	t.Helper()
	if len(d.Actions) != 1 || d.Actions[0].Kind != kind {
		t.Fatalf("want one %s, got %s %+v (%s)", kind, d.Phase, d.Actions, d.Message)
	}
	return d.Actions[0]
}

func TestCreatesMissingNodes(t *testing.T) {
	d := Plan(spec("v1", 3, 2), model.Observed{Nodes: nodes("v1", 1)}, Safe())
	if d.Phase != model.Provisioning || len(d.Actions) != 2 || d.Actions[0].Node != "vdb-1" {
		t.Fatalf("%+v", d)
	}
}

func TestUpgradeRestartsOneNodeAtATimeHighestFirst(t *testing.T) {
	obs := model.Observed{Nodes: nodes("v1", 3), Shards: []model.Shard{shard(0, 1, 2), shard(1, 2, 3), shard(2, 3, 1)}}
	a := only(t, Plan(spec("v2", 3, 2), obs, Safe()), model.RestartNode)
	if a.Node != "vdb-2" || a.Version != "v2" {
		t.Fatalf("%+v", a)
	}
	obs.Nodes[2].Ready = false
	if d := Plan(spec("v2", 3, 2), obs, Safe()); d.Phase != model.Waiting || len(d.Actions) != 0 {
		t.Fatalf("must wait while a node restarts: %+v", d)
	}
}

func TestWaitsForReplicasToCatchUpNotJustPods(t *testing.T) {
	obs := model.Observed{Nodes: nodes("v1", 3), Shards: []model.Shard{shard(0, 1, 2), shard(1, 2, 3)}}
	obs.Nodes[2].Version = "v2"
	obs.Shards[1].Replicas[1].State = model.Partial
	if d := Plan(spec("v2", 3, 2), obs, Safe()); d.Phase != model.Waiting {
		t.Fatalf("safe operator must wait for Active: %+v", d)
	}
	o := Safe()
	o.WaitForActive = false
	if d := Plan(spec("v2", 3, 2), obs, o); len(d.Actions) == 0 {
		t.Fatalf("without the safeguard the upgrade moves on: %+v", d)
	}
}

func TestSingleCopyShardGetsATemporaryCopyBeforeItsNodeRestarts(t *testing.T) {
	obs := model.Observed{Nodes: nodes("v1", 2), Shards: []model.Shard{shard(0, 2), shard(1, 1)}}
	a := only(t, Plan(spec("v2", 2, 1), obs, Safe()), model.ReplicateShard)
	if a.From != 2 || a.To != 1 || a.Shard != 0 {
		t.Fatalf("%+v", a)
	}
	o := Safe()
	o.Surge = false
	only(t, Plan(spec("v2", 2, 1), obs, o), model.RestartNode)
	// after the upgrade the temporary copy is dropped again
	done := model.Observed{Nodes: nodes("v2", 2), Shards: []model.Shard{shard(0, 2, 1), shard(1, 1)}}
	only(t, Plan(spec("v2", 2, 1), done, Safe()), model.DropReplica)
}

func TestScaleDownDrainsBeforeDeleting(t *testing.T) {
	obs := model.Observed{Nodes: nodes("v1", 3), Shards: []model.Shard{shard(0, 3, 1), shard(1, 3)}}
	a := only(t, Plan(spec("v1", 2, 2), obs, Safe()), model.ReplicateShard)
	if a.From != 3 || a.To == 3 {
		t.Fatalf("%+v", a)
	}
	obs.Shards = []model.Shard{shard(0, 3, 1, 2), shard(1, 3, 1)}
	a = only(t, Plan(spec("v1", 2, 2), obs, Safe()), model.DropReplica)
	if a.From != 3 {
		t.Fatalf("%+v", a)
	}
	obs.Shards = []model.Shard{shard(0, 1, 2), shard(1, 1, 2)}
	a = only(t, Plan(spec("v1", 2, 2), obs, Safe()), model.DeleteNode)
	if a.Node != "vdb-2" {
		t.Fatalf("%+v", a)
	}
	o := Safe()
	o.Drain = false
	obs.Shards = []model.Shard{shard(0, 3, 1)}
	only(t, Plan(spec("v1", 2, 2), obs, o), model.DeleteNode)
}

func TestRepairsMostAtRiskShardsInParallelOnIdleNodes(t *testing.T) {
	obs := model.Observed{Nodes: nodes("v1", 4), Shards: []model.Shard{shard(0, 1, 2, 3), shard(1, 1), shard(2, 2)}}
	d := Plan(spec("v1", 4, 3), obs, Safe())
	if d.Phase != model.Repairing || len(d.Actions) != 2 {
		t.Fatalf("%+v", d)
	}
	seen := map[uint64]bool{}
	for _, a := range d.Actions {
		if a.Kind != model.ReplicateShard || seen[a.From] || seen[a.To] {
			t.Fatalf("parallel repairs must use different nodes: %+v", d.Actions)
		}
		seen[a.From], seen[a.To] = true, true
	}
}

func TestBriefOutagesAreNotRepairedLongOnesAre(t *testing.T) {
	obs := model.Observed{Nodes: nodes("v1", 3), Shards: []model.Shard{shard(0, 1, 2)}}
	obs.Nodes[1].Ready, obs.Nodes[1].DownFor = false, 5
	obs.Shards[0].Replicas[1].State = model.Dead
	if d := Plan(spec("v1", 3, 2), obs, Safe()); len(d.Actions) != 0 {
		t.Fatalf("a 5-minute outage should be waited out: %+v", d)
	}
	obs.Nodes[1].DownFor = 30
	only(t, Plan(spec("v1", 3, 2), obs, Safe()), model.ReplicateShard)
}

func TestLostShardIsRestoredFromTheNewestTrustedBackup(t *testing.T) {
	obs := model.Observed{Now: 1000, Nodes: nodes("v1", 2), Shards: []model.Shard{{Collection: "c", ID: 0, Replicas: []model.Replica{{Peer: 1, State: model.Dead}}}},
		Backups: []model.Backup{{ID: "old", At: 100, OK: true, Verified: true, Restores: true}, {ID: "bad", At: 700, OK: true, Verified: true, Restores: false}, {ID: "new", At: 800, OK: true}}}
	a := only(t, Plan(spec("v1", 2, 1), obs, Safe()), model.RestoreShard)
	if a.Backup != "old" || a.To != 2 {
		t.Fatalf("verified-good backup on a fresh node: %+v", a)
	}
	o := Safe()
	o.VerifyBackups = false
	if a := only(t, Plan(spec("v1", 2, 1), obs, o), model.RestoreShard); a.Backup != "new" {
		t.Fatalf("unverified mode picks the newest that has not failed: %+v", a)
	}
	obs.Backups = nil
	if d := Plan(spec("v1", 2, 1), obs, Safe()); d.Phase != model.Blocked {
		t.Fatalf("no backup: blocked for a person, %+v", d)
	}
}

func TestBackupsAreTakenVerifiedShippedAndPruned(t *testing.T) {
	s := model.Spec{Version: "v1", Nodes: 1, ReplicationFactor: 1, BackupEveryMinutes: 60, BackupRetain: 2, DR: &model.DRSpec{Region: "eu-west"}}
	kinds := func(as []model.Action) map[model.ActionKind]string {
		m := map[model.ActionKind]string{}
		for _, a := range as {
			m[a.Kind] += a.Backup + " "
		}
		return m
	}
	obs := model.Observed{Now: 500, Nodes: nodes("v1", 1), Backups: []model.Backup{
		{ID: "b1", At: 100, OK: true, Verified: true, Restores: true, Shipped: true},
		{ID: "b2", At: 200, OK: true, Verified: true, Restores: true, Shipped: true},
		{ID: "b3", At: 300, OK: true, Verified: true, Restores: true},
		{ID: "b4", At: 470, OK: true},
	}}
	k := kinds(Plan(s, obs, Safe()).Actions)
	if k[model.TakeBackup] != "" || k[model.VerifyBackup] != "b4 " || k[model.DeleteBackup] != "b1 " {
		t.Fatalf("%v", k)
	}
	obs.Backups[3].Verified, obs.Backups[3].Restores = true, true
	k = kinds(Plan(s, obs, Safe()).Actions)
	if k[model.ShipBackup] != "b4 " {
		t.Fatalf("%v", k)
	}
	obs.Now = 600
	obs.Backups = append(obs.Backups, model.Backup{ID: "fail", At: 590})
	if k := kinds(Plan(s, obs, Safe()).Actions); k[model.TakeBackup] != "" {
		t.Fatalf("a failed snapshot is retried after a pause, not every pass: %v", k)
	}
}

func TestStatelessSameInputSameDecision(t *testing.T) {
	obs := model.Observed{Nodes: nodes("v1", 3), Shards: []model.Shard{shard(0, 1, 2), shard(1, 2, 3)}}
	a, b := Plan(spec("v2", 3, 2), obs, Safe()), Plan(spec("v2", 3, 2), obs, Safe())
	if len(a.Actions) != len(b.Actions) || a.Actions[0] != b.Actions[0] {
		t.Fatal("the planner must be deterministic")
	}
}
