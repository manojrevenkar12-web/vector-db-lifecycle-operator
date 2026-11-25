// Package model holds the shapes shared by the planner, the simulated fleet, the Kubernetes adapter and the API.
// No behaviour and no truth: only what an operator can observe and what it may ask for.
package model

// ReplicaState is how the database reports one replica of a shard.
type ReplicaState string

const (
	Active       ReplicaState = "Active"
	Partial      ReplicaState = "Partial"      // catching up after a restart or a transfer
	Initializing ReplicaState = "Initializing" // being created by a transfer
	Dead         ReplicaState = "Dead"         // on a node that is down, or lost
)

// Spec is what the customer (or an agent acting for them) asked for.
type Spec struct {
	Version            string  `json:"version"`
	Nodes              int     `json:"nodes"`
	ReplicationFactor  int     `json:"replicationFactor"`
	BackupEveryMinutes int     `json:"backupEveryMinutes"` // 0: no scheduled backups
	BackupRetain       int     `json:"backupRetain"`
	DR                 *DRSpec `json:"dr,omitempty"`
}

// DRSpec asks for backups to be shipped to a second region, so the cluster can be restored there if the first is lost.
type DRSpec struct {
	Region string `json:"region"`
}

// Node is one database node (a pod and its volume), as the platform reports it.
type Node struct {
	Name    string `json:"name"`
	Ordinal int    `json:"ordinal"`
	Peer    uint64 `json:"peer"`
	Version string `json:"version"`
	Ready   bool   `json:"ready"`
	// DownFor is how many minutes the node has not been ready (0 when ready).
	DownFor int `json:"downFor"`
}

// Replica is one copy of a shard on one peer.
type Replica struct {
	Peer  uint64       `json:"peer"`
	State ReplicaState `json:"state"`
}

// Shard is one shard of one collection and where its replicas are.
type Shard struct {
	Collection string    `json:"collection"`
	ID         int       `json:"id"`
	Replicas   []Replica `json:"replicas"`
}

// Transfer is a shard replica being copied from one peer to another.
type Transfer struct {
	Collection string `json:"collection"`
	Shard      int    `json:"shard"`
	From       uint64 `json:"from"`
	To         uint64 `json:"to"`
}

// Backup is a full snapshot of the cluster.
type Backup struct {
	ID       string `json:"id"`
	At       int    `json:"at"` // minutes
	OK       bool   `json:"ok"` // the snapshot call succeeded
	Verified bool   `json:"verified"`
	Restores bool   `json:"restores"` // a restore drill succeeded (meaningful only when Verified)
	Shipped  bool   `json:"shipped"`  // copied to the DR region
}

// Observed is everything the operator can see in one pass.
type Observed struct {
	Now       int        `json:"now"` // minutes
	Nodes     []Node     `json:"nodes"`
	Shards    []Shard    `json:"shards"`
	Transfers []Transfer `json:"transfers"`
	Backups   []Backup   `json:"backups"`
}

// ActionKind is one step the operator can take.
type ActionKind string

const (
	CreateNode     ActionKind = "CreateNode"
	DeleteNode     ActionKind = "DeleteNode"
	RestartNode    ActionKind = "RestartNode" // with Version: an upgrade
	ReplicateShard ActionKind = "ReplicateShard"
	DropReplica    ActionKind = "DropReplica"
	TakeBackup     ActionKind = "TakeBackup"
	VerifyBackup   ActionKind = "VerifyBackup"
	ShipBackup     ActionKind = "ShipBackup"
	DeleteBackup   ActionKind = "DeleteBackup"
	RestoreShard   ActionKind = "RestoreShard" // a shard with no surviving copy, from the newest trusted backup
)

// Action is one step, with the reason a person reading the status would need.
type Action struct {
	Kind       ActionKind `json:"kind"`
	Node       string     `json:"node,omitempty"`
	Ordinal    int        `json:"ordinal,omitempty"`
	Version    string     `json:"version,omitempty"`
	Collection string     `json:"collection,omitempty"`
	Shard      int        `json:"shard,omitempty"`
	From       uint64     `json:"from,omitempty"`
	To         uint64     `json:"to,omitempty"`
	Backup     string     `json:"backup,omitempty"`
	Reason     string     `json:"reason"`
}

// Phase is the cluster's state as shown to people.
type Phase string

const (
	Healthy      Phase = "Healthy"
	Provisioning Phase = "Provisioning"
	Repairing    Phase = "Repairing"
	Scaling      Phase = "Scaling"
	Upgrading    Phase = "Upgrading"
	Rebalancing  Phase = "Rebalancing"
	Waiting      Phase = "Waiting" // something is in flight; nothing safe to do until it settles
	Blocked      Phase = "Blocked" // needs a person: the reason says why
)

// Decision is the result of one reconcile pass: at most a few safe steps, and the phase.
type Decision struct {
	Phase   Phase    `json:"phase"`
	Actions []Action `json:"actions"`
	Message string   `json:"message"`
}
