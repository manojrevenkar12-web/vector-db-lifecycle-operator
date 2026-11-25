// Package plan decides the next safe steps for one cluster from what can be observed. It keeps no memory between
// passes: everything it needs is in the spec and the observation, so an operator that crashes and restarts picks
// up where the cluster is, not where it thought it was.
package plan

import (
	"fmt"
	"sort"

	"github.com/manojrevenkar12-web/vector-db-lifecycle-operator/internal/model"
)

// Options turn individual safeguards on or off. The operator uses Safe(); the lab switches them off one at a time.
type Options struct {
	WaitForActive bool // the next node restarts only when every replica is Active again, not just when pods are Ready
	Drain         bool // move replicas off a node before deleting it
	Repair        bool // re-replicate shards that have fewer copies than the replication factor
	Surge         bool // before restarting the only serving copy of a shard, add a temporary copy elsewhere
	Rebalance     bool // even out replicas across nodes and drop temporary copies afterwards
	VerifyBackups bool // restore each backup into scratch space before trusting it
	ShipBackups   bool // copy restorable backups to the disaster-recovery region
	RepairAfter   int  // minutes a node may be down before its replicas are rebuilt elsewhere
}

// Safe is the operator's configuration.
func Safe() Options {
	return Options{WaitForActive: true, Drain: true, Repair: true, Surge: true, Rebalance: true, VerifyBackups: true, ShipBackups: true, RepairAfter: 15}
}

// AsIs is what a StatefulSet and a backup cron job give you: pods restarted in order when the previous one is
// Ready, the highest pod deleted on scale-down, snapshots taken and never tested or copied out of the region.
func AsIs() Options {
	return Options{RepairAfter: 15}
}

type view struct {
	spec    model.Spec
	obs     model.Observed
	opt     Options
	byPeer  map[uint64]model.Node
	byName  map[string]model.Node
	load    map[uint64]int
	ready   []model.Node
	effRF   int
	partial bool
}

func newView(spec model.Spec, obs model.Observed, opt Options) *view {
	v := &view{spec: spec, obs: obs, opt: opt, byPeer: map[uint64]model.Node{}, byName: map[string]model.Node{}, load: map[uint64]int{}}
	for _, n := range obs.Nodes {
		v.byPeer[n.Peer] = n
		v.byName[n.Name] = n
		if n.Ready {
			v.ready = append(v.ready, n)
		}
	}
	sort.Slice(v.ready, func(i, j int) bool { return v.ready[i].Ordinal < v.ready[j].Ordinal })
	for _, s := range obs.Shards {
		for _, r := range s.Replicas {
			v.load[r.Peer]++
			if r.State == model.Partial || r.State == model.Initializing {
				v.partial = true
			}
		}
	}
	v.effRF = spec.ReplicationFactor
	if n := min(spec.Nodes, len(obs.Nodes)); v.effRF > n {
		v.effRF = n
	}
	if v.effRF < 1 {
		v.effRF = 1
	}
	return v
}

func (v *view) serving(r model.Replica) bool {
	n, ok := v.byPeer[r.Peer]
	return ok && n.Ready && r.State == model.Active
}

func (v *view) active(s model.Shard, except uint64) int {
	c := 0
	for _, r := range s.Replicas {
		if r.Peer != except && v.serving(r) {
			c++
		}
	}
	return c
}

// expected counts serving replicas plus those on nodes that are down only briefly and should come back.
func (v *view) expected(s model.Shard) int {
	c := 0
	for _, r := range s.Replicas {
		n, ok := v.byPeer[r.Peer]
		if v.serving(r) || (ok && !n.Ready && n.DownFor < v.opt.RepairAfter) || (ok && n.Ready && (r.State == model.Partial || r.State == model.Initializing)) {
			c++
		}
	}
	return c
}

// lost: every copy is dead on a node that is up again (its volume was lost), or there is no copy at all.
func (v *view) lost(s model.Shard) bool {
	for _, r := range s.Replicas {
		n, ok := v.byPeer[r.Peer]
		if !ok || !n.Ready || r.State != model.Dead {
			return false
		}
	}
	return true
}

func (v *view) trusted(b model.Backup) bool {
	return b.OK && (!v.opt.VerifyBackups || (b.Verified && b.Restores)) && !(b.Verified && !b.Restores)
}

func (v *view) newestTrusted() (model.Backup, bool) {
	var best model.Backup
	found := false
	for _, b := range v.obs.Backups {
		if v.trusted(b) && (!found || b.At > best.At) {
			best, found = b, true
		}
	}
	return best, found
}

// restoreTarget prefers a ready node without the shard, then one holding only a dead copy of it.
func (v *view) restoreTarget(s model.Shard, busy map[uint64]bool) (uint64, bool) {
	if p, ok := v.target(s, busy); ok {
		return p, true
	}
	for _, r := range s.Replicas {
		if n, ok := v.byPeer[r.Peer]; ok && n.Ready && !busy[r.Peer] {
			return r.Peer, true
		}
	}
	return 0, false
}

func (v *view) inFlight(s model.Shard) bool {
	for _, t := range v.obs.Transfers {
		if t.Collection == s.Collection && t.Shard == s.ID {
			return true
		}
	}
	return false
}

func has(s model.Shard, peer uint64) bool {
	for _, r := range s.Replicas {
		if r.Peer == peer {
			return true
		}
	}
	return false
}

func (v *view) source(s model.Shard, avoid uint64) (uint64, bool) {
	for _, r := range s.Replicas {
		if r.Peer != avoid && v.serving(r) {
			return r.Peer, true
		}
	}
	for _, r := range s.Replicas {
		if v.serving(r) {
			return r.Peer, true
		}
	}
	return 0, false
}

// target is the least-loaded ready node that does not hold the shard, skipping the given nodes.
func (v *view) target(s model.Shard, skip map[uint64]bool) (uint64, bool) {
	best, found := uint64(0), false
	for _, n := range v.ready {
		if skip[n.Peer] || has(s, n.Peer) {
			continue
		}
		if !found || v.load[n.Peer] < v.load[best] {
			best, found = n.Peer, true
		}
	}
	return best, found
}

// Plan returns the next steps. Data movement happens one step at a time; backups run alongside.
func Plan(spec model.Spec, obs model.Observed, opt Options) model.Decision {
	v := newView(spec, obs, opt)
	d := v.lifecycle()
	d.Actions = append(d.Actions, v.backups()...)
	return d
}

func (v *view) lifecycle() model.Decision {
	spec, obs, opt := v.spec, v.obs, v.opt
	// 1. nodes that should exist and do not
	var create []model.Action
	for i := 0; i < spec.Nodes; i++ {
		if _, ok := v.byName[NodeName(i)]; !ok {
			create = append(create, model.Action{Kind: model.CreateNode, Node: NodeName(i), Ordinal: i, Version: spec.Version, Reason: fmt.Sprintf("spec asks for %d nodes", spec.Nodes)})
		}
	}
	if len(create) > 0 {
		phase := model.Provisioning
		if len(obs.Shards) > 0 {
			phase = model.Scaling
		}
		return model.Decision{Phase: phase, Actions: create, Message: fmt.Sprintf("creating %d node(s)", len(create))}
	}
	// 2. repair first, and in parallel on nodes that are not already copying: the shards with the fewest serving
	// copies go first. Restores bring back shards with no surviving copy from the newest trusted backup.
	if opt.Repair {
		busy := map[uint64]bool{}
		for _, t := range obs.Transfers {
			busy[t.From], busy[t.To] = true, true
		}
		var acts []model.Action
		blocked := ""
		for _, s := range obs.Shards {
			if !v.lost(s) {
				continue
			}
			b, ok := v.newestTrusted()
			if !ok {
				blocked = fmt.Sprintf("%s/%d has no surviving copy and there is no restorable backup", s.Collection, s.ID)
				continue
			}
			to, ok := v.restoreTarget(s, busy)
			if !ok {
				continue
			}
			busy[to] = true
			acts = append(acts, model.Action{Kind: model.RestoreShard, Collection: s.Collection, Shard: s.ID, To: to, Backup: b.ID, Reason: fmt.Sprintf("no surviving copy; restoring from %s (taken at minute %d)", b.ID, b.At)})
		}
		for _, s := range obs.Shards {
			for _, r := range s.Replicas {
				if n, ok := v.byPeer[r.Peer]; ok && n.Ready && r.State == model.Dead && v.active(s, r.Peer) > 0 && !busy[r.Peer] {
					acts = append(acts, model.Action{Kind: model.DropReplica, Collection: s.Collection, Shard: s.ID, From: r.Peer, Reason: "dead replica on a healthy node"})
				}
			}
		}
		under := []model.Shard{}
		for _, s := range obs.Shards {
			if v.expected(s) < v.effRF && v.active(s, 0) > 0 && !v.inFlight(s) {
				under = append(under, s)
			}
		}
		sort.SliceStable(under, func(i, j int) bool { return v.active(under[i], 0) < v.active(under[j], 0) })
		for _, s := range under {
			from, ok := v.source(s, 0)
			if !ok || busy[from] {
				continue
			}
			to, ok := v.target(s, busy)
			if !ok {
				continue
			}
			busy[from], busy[to] = true, true
			acts = append(acts, model.Action{Kind: model.ReplicateShard, Collection: s.Collection, Shard: s.ID, From: from, To: to, Reason: fmt.Sprintf("%d of %d copies serving", v.active(s, 0), v.effRF)})
		}
		if len(acts) > 0 {
			return model.Decision{Phase: model.Repairing, Actions: acts, Message: fmt.Sprintf("%d repair step(s)", len(acts))}
		}
		if blocked != "" {
			return model.Decision{Phase: model.Blocked, Message: blocked}
		}
	}
	// 3. everything else moves one step at a time, after transfers settle
	if len(obs.Transfers) > 0 {
		t := obs.Transfers[0]
		return model.Decision{Phase: model.Waiting, Message: fmt.Sprintf("waiting for %s/%d to copy to peer %d", t.Collection, t.Shard, t.To)}
	}
	notReady := len(obs.Nodes) - len(v.ready)
	if notReady > 0 {
		return model.Decision{Phase: model.Waiting, Message: fmt.Sprintf("%d node(s) not ready", notReady)}
	}
	if opt.WaitForActive && v.partial {
		return model.Decision{Phase: model.Waiting, Message: "replicas are catching up"}
	}
	// 4. scale down, highest ordinal first
	if len(obs.Nodes) > spec.Nodes {
		victim := obs.Nodes[0]
		for _, n := range obs.Nodes {
			if n.Ordinal > victim.Ordinal {
				victim = n
			}
		}
		if opt.Drain {
			skip := map[uint64]bool{victim.Peer: true}
			after := min(spec.ReplicationFactor, spec.Nodes)
			for _, s := range obs.Shards {
				if !has(s, victim.Peer) {
					continue
				}
				if v.active(s, victim.Peer) >= max(after, 1) {
					return model.Decision{Phase: model.Scaling, Actions: []model.Action{{Kind: model.DropReplica, Collection: s.Collection, Shard: s.ID, From: victim.Peer, Reason: "draining " + victim.Name}}}
				}
				if to, ok := v.target(s, skip); ok {
					from, _ := v.source(s, 0)
					return model.Decision{Phase: model.Scaling, Actions: []model.Action{{Kind: model.ReplicateShard, Collection: s.Collection, Shard: s.ID, From: from, To: to, Reason: "moving a copy off " + victim.Name + " before it is removed"}}}
				}
				return model.Decision{Phase: model.Blocked, Message: fmt.Sprintf("cannot drain %s: nowhere to put %s/%d", victim.Name, s.Collection, s.ID)}
			}
		}
		return model.Decision{Phase: model.Scaling, Actions: []model.Action{{Kind: model.DeleteNode, Node: victim.Name, Ordinal: victim.Ordinal, Reason: fmt.Sprintf("spec asks for %d nodes", spec.Nodes)}}}
	}
	// 5. upgrades, highest ordinal first, one node at a time
	var outdated []model.Node
	for _, n := range obs.Nodes {
		if n.Version != spec.Version {
			outdated = append(outdated, n)
		}
	}
	if len(outdated) > 0 {
		sort.Slice(outdated, func(i, j int) bool { return outdated[i].Ordinal > outdated[j].Ordinal })
		x := outdated[0]
		if opt.Surge {
			for _, s := range obs.Shards {
				if has(s, x.Peer) && v.active(s, x.Peer) == 0 {
					if to, ok := v.target(s, map[uint64]bool{x.Peer: true}); ok {
						return model.Decision{Phase: model.Upgrading, Actions: []model.Action{{Kind: model.ReplicateShard, Collection: s.Collection, Shard: s.ID, From: x.Peer, To: to, Reason: fmt.Sprintf("temporary copy so %s/%d keeps serving while %s restarts", s.Collection, s.ID, x.Name)}}}
					}
				}
			}
		}
		return model.Decision{Phase: model.Upgrading, Actions: []model.Action{{Kind: model.RestartNode, Node: x.Name, Ordinal: x.Ordinal, Version: spec.Version, Reason: fmt.Sprintf("%s -> %s (%d node(s) left)", x.Version, spec.Version, len(outdated))}}}
	}
	// 6. rebalance: drop surplus copies, then even out the load
	if opt.Rebalance {
		for _, s := range obs.Shards {
			if v.active(s, 0) > spec.ReplicationFactor && v.active(s, 0) > 1 {
				var worst uint64
				for _, r := range s.Replicas {
					if v.serving(r) && (worst == 0 || v.load[r.Peer] > v.load[worst]) {
						worst = r.Peer
					}
				}
				return model.Decision{Phase: model.Rebalancing, Actions: []model.Action{{Kind: model.DropReplica, Collection: s.Collection, Shard: s.ID, From: worst, Reason: "more copies than the replication factor"}}}
			}
		}
		if len(v.ready) > 1 {
			hi, lo := v.ready[0], v.ready[0]
			for _, n := range v.ready {
				if v.load[n.Peer] > v.load[hi.Peer] {
					hi = n
				}
				if v.load[n.Peer] < v.load[lo.Peer] {
					lo = n
				}
			}
			if v.load[hi.Peer]-v.load[lo.Peer] > 1 {
				for _, s := range obs.Shards {
					if has(s, hi.Peer) && !has(s, lo.Peer) {
						return model.Decision{Phase: model.Rebalancing, Actions: []model.Action{{Kind: model.ReplicateShard, Collection: s.Collection, Shard: s.ID, From: hi.Peer, To: lo.Peer, Reason: fmt.Sprintf("%s holds %d replicas, %s holds %d", hi.Name, v.load[hi.Peer], lo.Name, v.load[lo.Peer])}}}
					}
				}
			}
		}
	}
	return model.Decision{Phase: model.Healthy, Message: "nothing to do"}
}

func (v *view) backups() []model.Action {
	spec, obs, opt := v.spec, v.obs, v.opt
	if spec.BackupEveryMinutes <= 0 {
		return nil
	}
	bs := append([]model.Backup(nil), obs.Backups...)
	sort.Slice(bs, func(i, j int) bool { return bs[i].At > bs[j].At }) // newest first
	var out []model.Action
	last := -1 << 31
	for _, b := range bs {
		if b.OK {
			last = b.At
			break
		}
	}
	retryWait := len(bs) > 0 && !bs[0].OK && obs.Now-bs[0].At < BackupRetryMinutes
	if obs.Now-last >= spec.BackupEveryMinutes && !retryWait {
		out = append(out, model.Action{Kind: model.TakeBackup, Backup: fmt.Sprintf("b-%d", obs.Now), Reason: "scheduled backup"})
	}
	trusted := v.trusted
	for _, b := range bs {
		if !b.OK {
			continue
		}
		if opt.VerifyBackups && !b.Verified {
			out = append(out, model.Action{Kind: model.VerifyBackup, Backup: b.ID, Reason: "restore drill into scratch space"})
			break
		}
		if trusted(b) {
			if opt.ShipBackups && spec.DR != nil && !b.Shipped {
				out = append(out, model.Action{Kind: model.ShipBackup, Backup: b.ID, Reason: "copy to " + spec.DR.Region})
			}
			break
		}
	}
	// retention: keep the newest BackupRetain trusted backups; never delete the newest one that restores
	keep := map[string]bool{}
	n := 0
	for _, b := range bs {
		if trusted(b) && n < max(spec.BackupRetain, 1) {
			keep[b.ID] = true
			n++
		}
	}
	for _, b := range bs {
		pending := b.OK && opt.VerifyBackups && !b.Verified
		if !keep[b.ID] && !pending && b.At < obs.Now-spec.BackupEveryMinutes {
			out = append(out, model.Action{Kind: model.DeleteBackup, Backup: b.ID, Reason: "outside retention or failed"})
		}
	}
	return out
}

// BackupRetryMinutes is how long to wait after a failed snapshot before trying again.
const BackupRetryMinutes = 30

// NodeName is the stable name of the node with this ordinal.
func NodeName(i int) string { return fmt.Sprintf("vdb-%d", i) }
