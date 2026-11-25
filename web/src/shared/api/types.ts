export type ReplicaState = "Active" | "Partial" | "Initializing" | "Dead";
export type Phase = "Healthy" | "Provisioning" | "Repairing" | "Scaling" | "Upgrading" | "Rebalancing" | "Waiting" | "Blocked";

export interface Spec {
  version: string;
  nodes: number;
  replicationFactor: number;
  backupEveryMinutes?: number;
  backupRetain?: number;
  dr?: { region: string } | null;
}
export interface Node { name: string; ordinal: number; peer: number; version: string; ready: boolean; downFor: number }
export interface Replica { peer: number; state: ReplicaState }
export interface Shard { collection: string; id: number; replicas: Replica[] }
export interface Transfer { collection: string; shard: number; from: number; to: number }
export interface Backup { id: string; at: number; ok: boolean; verified: boolean; restores: boolean; shipped: boolean }
export interface Observed { now: number; nodes: Node[]; shards: Shard[] | null; transfers: Transfer[] | null; backups: Backup[] | null }
export interface Action { kind: string; node?: string; collection?: string; shard?: number; from?: number; to?: number; reason?: string }
export interface Status { phase: Phase; actions: Action[] | null; message: string }

export interface ClusterSummary { id: string; generation: number; phase: Phase; message: string; spec: Spec }
export interface ClusterDetail { id: string; generation: number; spec: Spec; status: Status; observed: Observed }

export interface Patch { version?: string; nodes?: number; replicationFactor?: number; dr?: boolean; drRegion?: string }
export interface Effects {
  restarts: number; nodesAdded: number; nodesRemoved: number; replicasToMove: number; temporaryCopies: number;
  risks: string[]; needsPerson: boolean; needsPersonReason: string[];
}
export interface Preview { id: string; cluster: string; generation: number; from: Spec; to: Spec; effects: Effects; approvedBy?: string; applied: boolean }
export interface Actor { kind: "human" | "agent"; id: string }
