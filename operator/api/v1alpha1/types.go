// Package v1alpha1 holds the VectorCluster resource: the spec a person or an agent edits, and the status the operator reports.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DRSpec asks for backups to be copied to another region.
type DRSpec struct {
	Region string `json:"region"`
}

// VectorClusterSpec is the desired cluster.
type VectorClusterSpec struct {
	// Version is the database image tag, e.g. v1.16.0.
	Version string `json:"version"`
	// Image is the image repository; the tag comes from Version.
	// +optional
	Image string `json:"image,omitempty"`
	// Nodes is the number of database nodes.
	Nodes int `json:"nodes"`
	// ReplicationFactor is the number of copies each shard should have.
	ReplicationFactor int `json:"replicationFactor"`
	// StorageSize is the volume size per node, e.g. 1Gi.
	// +optional
	StorageSize string `json:"storageSize,omitempty"`
	// StorageClassName for the node volumes.
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`
	// BackupEveryMinutes and BackupRetain are accepted but not acted on by this adapter yet (see README, Limits).
	// +optional
	BackupEveryMinutes int `json:"backupEveryMinutes,omitempty"`
	// +optional
	BackupRetain int `json:"backupRetain,omitempty"`
	// +optional
	DR *DRSpec `json:"dr,omitempty"`
}

// NodeStatus is one database node as the operator sees it.
type NodeStatus struct {
	Name    string `json:"name"`
	Ordinal int    `json:"ordinal"`
	Peer    uint64 `json:"peer,omitempty"`
	Version string `json:"version"`
	Ready   bool   `json:"ready"`
}

// VectorClusterStatus is what the operator last saw and decided.
type VectorClusterStatus struct {
	// Phase is Healthy, Provisioning, Repairing, Scaling, Upgrading, Rebalancing, Waiting or Blocked.
	Phase   string `json:"phase,omitempty"`
	Message string `json:"message,omitempty"`
	// LastActions are the steps taken in the last reconcile.
	LastActions        []string     `json:"lastActions,omitempty"`
	ObservedGeneration int64        `json:"observedGeneration,omitempty"`
	ReadyNodes         int          `json:"readyNodes,omitempty"`
	Nodes              []NodeStatus `json:"nodes,omitempty"`
	// Peers remembers each ordinal's database peer id, so a node that is down keeps its identity.
	Peers      map[string]uint64  `json:"peers,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// VectorCluster is a vector database cluster managed by the operator.
type VectorCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VectorClusterSpec   `json:"spec,omitempty"`
	Status VectorClusterStatus `json:"status,omitempty"`
}

// VectorClusterList is a list of VectorCluster.
type VectorClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VectorCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VectorCluster{}, &VectorClusterList{})
}
