package sim

import (
	"testing"

	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/model"
)

func calm() Faults {
	return Faults{DownMinutes: [2]int{3, 3}, MinutesPerGB: 1, CatchUpMinutes: [2]int{2, 2}, RestartMinutes: [2]int{2, 2}, ProvisionMinutes: [2]int{4, 4}, ShipMinutes: 10, RestoreAttemptHours: 1}
}

func spec(n, rf int) model.Spec { return model.Spec{Version: "v1", Nodes: n, ReplicationFactor: rf} }

func TestLayoutPlacesReplicationFactorCopies(t *testing.T) {
	c := NewCluster(1, "x", spec(3, 2), 2, 6, [2]float64{1, 1}, calm(), 100)
	o := c.Observe()
	if len(o.Nodes) != 3 || len(o.Shards) != 12 {
		t.Fatalf("%d nodes %d shards", len(o.Nodes), len(o.Shards))
	}
	for _, s := range o.Shards {
		if len(s.Replicas) != 2 || s.Replicas[0].Peer == s.Replicas[1].Peer {
			t.Fatalf("%+v", s)
		}
	}
}

func TestRestartTakesTheNodeDownThenReplicasCatchUp(t *testing.T) {
	c := NewCluster(1, "x", spec(2, 2), 1, 2, [2]float64{1, 1}, calm(), 100)
	if !c.Apply(model.Action{Kind: model.RestartNode, Node: "vdb-1", Version: "v1"}) {
		t.Fatal("restart refused")
	}
	c.Tick()
	o := c.Observe()
	if o.Nodes[1].Ready || o.Nodes[1].Version != "v1" {
		t.Fatalf("%+v", o.Nodes[1])
	}
	for i := 0; i < 3; i++ {
		c.Tick()
	}
	o = c.Observe()
	if !o.Nodes[1].Ready || o.Shards[0].Replicas[1].State != model.Partial {
		t.Fatalf("back but catching up: %+v %+v", o.Nodes[1], o.Shards[0])
	}
	for i := 0; i < 3; i++ {
		c.Tick()
	}
	if c.Observe().Shards[0].Replicas[1].State != model.Active || !c.Converged() {
		t.Fatalf("%+v", c.Observe().Shards[0])
	}
	if c.Stats.UnavailableMinutes != 0 {
		t.Fatal("two copies: no unavailability")
	}
}

func TestTheDatabaseRefusesToDropTheLastCopy(t *testing.T) {
	c := NewCluster(1, "x", spec(2, 1), 1, 2, [2]float64{1, 1}, calm(), 100)
	s := c.Observe().Shards[0]
	if c.Apply(model.Action{Kind: model.DropReplica, Collection: s.Collection, Shard: s.ID, From: s.Replicas[0].Peer}) {
		t.Fatal("dropping the last copy must be refused")
	}
}

func TestDeletingANodeWithTheOnlyCopyLosesTheShard(t *testing.T) {
	c := NewCluster(1, "x", spec(2, 1), 1, 2, [2]float64{1, 1}, calm(), 100)
	c.Apply(model.Action{Kind: model.DeleteNode, Node: "vdb-1"})
	c.Tick()
	if c.Stats.LossEvents == 0 || c.Stats.UnavailableByOps == 0 {
		t.Fatalf("%+v", c.Stats)
	}
}

func TestTransferCopiesAndRegionLossUsesShippedBackups(t *testing.T) {
	c := NewCluster(1, "x", spec(2, 1), 1, 1, [2]float64{2, 2}, calm(), 100)
	s := c.Observe().Shards[0]
	from := s.Replicas[0].Peer
	to := uint64(3) - from
	if !c.Apply(model.Action{Kind: model.ReplicateShard, Collection: s.Collection, Shard: s.ID, From: from, To: to}) {
		t.Fatal("transfer refused")
	}
	if len(c.Observe().Transfers) != 1 {
		t.Fatal("transfer not visible")
	}
	for i := 0; i < 4; i++ {
		c.Tick()
	}
	if len(c.Observe().Shards[0].Replicas) != 2 || c.Observe().Shards[0].Replicas[1].State != model.Active {
		t.Fatalf("%+v", c.Observe().Shards[0])
	}
	if rpo, _ := c.RegionLoss(); rpo != -1 {
		t.Fatal("nothing shipped yet: not recoverable")
	}
	c.Apply(model.Action{Kind: model.TakeBackup, Backup: "b"})
	c.Apply(model.Action{Kind: model.ShipBackup, Backup: "b"})
	for i := 0; i < 20; i++ {
		c.Tick()
	}
	if rpo, rto := c.RegionLoss(); rpo != 20 || rto != 1 {
		t.Fatalf("rpo %d rto %v", rpo, rto)
	}
}

func TestSameSeedSameWorld(t *testing.T) {
	f := calm()
	f.CrashPerNodeDay, f.DiskLossShare = 20, 0.5
	run := func() Stats {
		c := NewCluster(7, "x", spec(3, 2), 1, 6, [2]float64{1, 5}, f, 600)
		for i := 0; i < 600; i++ {
			c.Tick()
		}
		return c.Stats
	}
	if run() != run() {
		t.Fatal("same seed must give the same world")
	}
}

func TestTheOperatorCannotChangeTheLuck(t *testing.T) {
	f := calm()
	f.CrashPerNodeDay = 10
	a := NewCluster(3, "x", spec(3, 2), 1, 6, [2]float64{1, 1}, f, 600)
	b := NewCluster(3, "x", spec(3, 2), 1, 6, [2]float64{1, 1}, f, 600)
	b.Apply(model.Action{Kind: model.RestartNode, Node: "vdb-2", Version: "v1"})
	b.Apply(model.Action{Kind: model.TakeBackup, Backup: "b"})
	for i := 0; i < 100; i++ {
		b.Tick()
	}
	if len(a.crashes) != len(b.crashes) {
		t.Fatal("crash schedules differ")
	}
	for k, v := range a.crashes {
		if len(v) != len(b.crashes[k]) {
			t.Fatal("crash schedules differ")
		}
		for i := range v {
			if v[i] != b.crashes[k][i] {
				t.Fatal("crash schedules differ")
			}
		}
	}
}
