package lab

import (
	"fmt"
	"math"
	"sort"

	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/model"
	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/plan"
	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/rng"
	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/sim"
)

// ClusterPlan is one cluster of the fleet: its starting spec, layout and the changes its owner will ask for.
type ClusterPlan struct {
	ID          string
	Spec        model.Spec
	Collections int
	Changes     []struct {
		At   int
		Spec model.Spec
		Kind string
	}
}

// Fleet draws the fleet for a seed. It depends only on the world and the seed, never on the design.
func Fleet(w World, seed int64) []ClusterPlan {
	var out []ClusterPlan
	for i := 0; i < w.Clusters; i++ {
		id := fmt.Sprintf("c%02d", i)
		r := rng.Keyed(seed, id, "fleet")
		nodes := pick(w.Nodes, r.Float64())
		rf := min(pick(w.ReplicationFactor, r.Float64()), nodes)
		spec := model.Spec{Version: w.Versions[0], Nodes: nodes, ReplicationFactor: rf, BackupEveryMinutes: w.BackupEveryMinutes, BackupRetain: w.BackupRetain}
		if r.Float64() < w.DRShare {
			spec.DR = &model.DRSpec{Region: "eu-west"}
		}
		cp := ClusterPlan{ID: id, Spec: spec, Collections: w.Collections[0] + r.IntN(w.Collections[1]-w.Collections[0]+1)}
		cur := spec
		vi := 0
		for _, ch := range w.Changes {
			if r.Float64() >= ch.Share {
				continue
			}
			next := cur
			switch ch.Kind {
			case "upgrade":
				if vi+1 >= len(w.Versions) {
					continue
				}
				vi++
				next.Version = w.Versions[vi]
			case "scaleUp":
				next.Nodes = min(cur.Nodes+1+r.IntN(2), 7)
			case "scaleDown":
				if cur.Nodes <= 1 {
					continue
				}
				next.Nodes = cur.Nodes - 1
			}
			if next == cur {
				continue
			}
			cp.Changes = append(cp.Changes, struct {
				At   int
				Spec model.Spec
				Kind string
			}{ch.At + r.IntN(120), next, ch.Kind})
			cur = next
		}
		out = append(out, cp)
	}
	return out
}

// Result is one design on one cluster.
type Result struct {
	sim.Stats
	DR          bool    `json:"dr"`
	RPOMinutes  int     `json:"rpoMinutes"` // -1: not recoverable
	RTOHours    float64 `json:"rtoHours"`
	Phases      map[model.Phase]int
	ChangeKinds map[string][2]int // kind -> requested, completed
}

// RunCluster plays one cluster through the whole window under one design.
func RunCluster(w World, seed int64, cp ClusterPlan, opt plan.Options) Result {
	c := sim.NewCluster(seed, cp.ID, cp.Spec, cp.Collections, cp.Spec.Nodes*w.ShardsPerNode, w.ShardGB, w.Faults, w.Minutes)
	res := Result{Phases: map[model.Phase]int{}, ChangeKinds: map[string][2]int{}}
	next := 0
	pending := false
	requested := 0
	kind := ""
	for t := 0; t < w.Minutes; t++ {
		if next < len(cp.Changes) && cp.Changes[next].At == t {
			if pending { // superseded before it finished
				pending = false
			}
			c.Spec = cp.Changes[next].Spec
			kind = cp.Changes[next].Kind
			k := res.ChangeKinds[kind]
			k[0]++
			res.ChangeKinds[kind] = k
			c.Stats.ChangesRequested++
			requested, pending = t, true
			next++
		}
		d := plan.Plan(c.Spec, c.Observe(), opt)
		res.Phases[d.Phase]++
		if d.Phase == model.Blocked {
			c.Stats.BlockedMinutes++
		}
		for _, a := range d.Actions {
			c.Apply(a)
		}
		c.Tick()
		if pending && c.Converged() {
			pending = false
			c.Stats.ChangesCompleted++
			c.Stats.ChangeMinutes += t - requested
			k := res.ChangeKinds[kind]
			k[1]++
			res.ChangeKinds[kind] = k
		}
	}
	c.Finish()
	res.Stats = c.Stats
	if cp.Spec.DR != nil {
		res.DR = true
		res.RPOMinutes, res.RTOHours = c.RegionLoss()
	}
	return res
}

// Summary is one design over the whole fleet for one seed (or the mean over seeds).
type Summary struct {
	Clusters                int     `json:"clusters"`
	ShardMinutes            float64 `json:"shardMinutes"`
	Availability            float64 `json:"availability"` // share of shard-minutes with a serving copy
	UnavailableShardMinutes float64 `json:"unavailableShardMinutes"`
	UnavailableByOps        float64 `json:"unavailableByOps"`
	UnderReplicatedMinutes  float64 `json:"underReplicatedShardMinutes"`
	LossEvents              float64 `json:"lossEvents"`
	LossEventsReplicated    float64 `json:"lossEventsReplicated"`
	ShardsRestored          float64 `json:"shardsRestored"`
	RestoreLossHours        float64 `json:"restoreLossHours"`
	RestoresFailed          float64 `json:"restoresFailed"`
	ShardsLostAtEnd         float64 `json:"shardsLostAtEnd"`
	ClustersWithLoss        float64 `json:"clustersWithLoss"`
	ChangesRequested        float64 `json:"changesRequested"`
	ChangesCompleted        float64 `json:"changesCompleted"`
	ChangesCompletedShare   float64 `json:"changesCompletedShare"`
	MeanChangeMinutes       float64 `json:"meanChangeMinutes"`
	UpgradesCompletedShare  float64 `json:"upgradesCompletedShare"`
	ScaleDownsCompleted     float64 `json:"scaleDownsCompletedShare"`
	Restarts                float64 `json:"restarts"`
	Transfers               float64 `json:"transfers"`
	TransfersFailed         float64 `json:"transfersFailed"`
	APIFailures             float64 `json:"apiFailures"`
	Refused                 float64 `json:"refused"`
	BlockedMinutes          float64 `json:"blockedMinutes"`
	BackupsTaken            float64 `json:"backupsTaken"`
	BackupsUnrestorable     float64 `json:"backupsUnrestorable"`
	SurgeGBHours            float64 `json:"surgeGBHours"`
	DRClusters              float64 `json:"drClusters"`
	DRRecovered             float64 `json:"drRecovered"`
	DRRecoveredShare        float64 `json:"drRecoveredShare"`
	DRMeanRPOMinutes        float64 `json:"drMeanRpoMinutes"`
	DRWorstRPOMinutes       float64 `json:"drWorstRpoMinutes"`
	DRMeanRTOHours          float64 `json:"drMeanRtoHours"`
}

// Summarise adds up the clusters of one run.
func Summarise(rs []Result) Summary {
	s := Summary{Clusters: len(rs)}
	var rpo []float64
	var rto []float64
	up, upDone, down, downDone := 0, 0, 0, 0
	for _, r := range rs {
		s.ShardMinutes += float64(r.ShardMinutes)
		s.UnavailableShardMinutes += float64(r.UnavailableMinutes)
		s.UnavailableByOps += float64(r.UnavailableByOps)
		s.UnderReplicatedMinutes += float64(r.UnderReplicatedMins)
		s.LossEvents += float64(r.LossEvents)
		s.LossEventsReplicated += float64(r.LossEventsReplicated)
		s.ShardsRestored += float64(r.ShardsRestored)
		s.RestoreLossHours += float64(r.RestoreLossMinutes) / 60
		s.RestoresFailed += float64(r.RestoresFailed)
		s.ShardsLostAtEnd += float64(r.ShardsLostAtEnd)
		if r.ShardsLostAtEnd > 0 {
			s.ClustersWithLoss++
		}
		s.ChangesRequested += float64(r.ChangesRequested)
		s.ChangesCompleted += float64(r.ChangesCompleted)
		s.MeanChangeMinutes += float64(r.ChangeMinutes)
		s.Restarts += float64(r.Restarts)
		s.Transfers += float64(r.Transfers)
		s.TransfersFailed += float64(r.TransfersFailed)
		s.APIFailures += float64(r.APIFailures)
		s.Refused += float64(r.Refused)
		s.BlockedMinutes += float64(r.BlockedMinutes)
		s.BackupsTaken += float64(r.BackupsTaken)
		s.BackupsUnrestorable += float64(r.BackupsUnrestorable)
		s.SurgeGBHours += float64(r.StorageGBMinutesSurge) / 60
		up += r.ChangeKinds["upgrade"][0]
		upDone += r.ChangeKinds["upgrade"][1]
		down += r.ChangeKinds["scaleDown"][0]
		downDone += r.ChangeKinds["scaleDown"][1]
		if r.DR {
			s.DRClusters++
			rto = append(rto, r.RTOHours)
			if r.RPOMinutes >= 0 {
				s.DRRecovered++
				rpo = append(rpo, float64(r.RPOMinutes))
			}
		}
	}
	if s.ChangesCompleted > 0 {
		s.MeanChangeMinutes /= s.ChangesCompleted
	}
	s.ChangesCompletedShare = ratio(s.ChangesCompleted, s.ChangesRequested)
	s.UpgradesCompletedShare = ratio(float64(upDone), float64(up))
	s.ScaleDownsCompleted = ratio(float64(downDone), float64(down))
	s.Availability = 1 - ratio(s.UnavailableShardMinutes, s.ShardMinutes)
	s.DRRecoveredShare = ratio(s.DRRecovered, s.DRClusters)
	if len(rpo) > 0 {
		s.DRMeanRPOMinutes = mean(rpo)
		sort.Float64s(rpo)
		s.DRWorstRPOMinutes = rpo[len(rpo)-1]
	}
	if len(rto) > 0 {
		s.DRMeanRTOHours = mean(rto)
	}
	return s
}

func ratio(a, b float64) float64 {
	if b == 0 {
		return 1
	}
	return a / b
}

func mean(xs []float64) float64 {
	t := 0.0
	for _, x := range xs {
		t += x
	}
	return t / math.Max(float64(len(xs)), 1)
}
