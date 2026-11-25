import type { Node, ReplicaState, Shard } from "../../shared/api/types";

export interface Column { key: string; collection: string; shard: number; serving: number }
export interface MapModel {
  nodes: Node[];
  groups: { collection: string; columns: Column[] }[];
  cell: (peer: number, col: Column) => ReplicaState | null;
  atRisk: number;
}

/** Lays out replicas as nodes × shards. A shard is at risk when fewer than `rf` copies are serving on ready nodes. */
export function buildMap(nodes: Node[], shards: Shard[], rf: number): MapModel {
  const ready = new Set(nodes.filter((n) => n.ready).map((n) => n.peer));
  const byKey = new Map<string, Map<number, ReplicaState>>();
  const groups: MapModel["groups"] = [];
  let atRisk = 0;
  for (const sh of [...shards].sort((a, b) => a.collection.localeCompare(b.collection) || a.id - b.id)) {
    const key = `${sh.collection}/${sh.id}`;
    const m = new Map<number, ReplicaState>();
    for (const r of sh.replicas) m.set(r.peer, r.state);
    byKey.set(key, m);
    const serving = sh.replicas.filter((r) => r.state === "Active" && ready.has(r.peer)).length;
    if (serving < rf) atRisk++;
    let g = groups[groups.length - 1];
    if (!g || g.collection !== sh.collection) {
      g = { collection: sh.collection, columns: [] };
      groups.push(g);
    }
    g.columns.push({ key, collection: sh.collection, shard: sh.id, serving });
  }
  return {
    nodes: [...nodes].sort((a, b) => a.ordinal - b.ordinal),
    groups,
    cell: (peer, col) => byKey.get(col.key)?.get(peer) ?? null,
    atRisk,
  };
}
