import { useMemo } from "react";
import type { Action, ClusterDetail } from "../../shared/api/types";
import { PhaseTag } from "../../shared/ui/PhaseTag";
import { buildMap } from "./shardMap";
import { ShardMap } from "./ShardMap";
import s from "./ClusterView.module.css";

function describe(a: Action): string {
  switch (a.kind) {
    case "ReplicateShard": return `Copy ${a.collection}/${a.shard} from peer ${a.from} to peer ${a.to}`;
    case "DropReplica": return `Drop the copy of ${a.collection}/${a.shard} on peer ${a.from}`;
    case "RestartNode": return `Restart ${a.node}`;
    case "CreateNode": return `Create ${a.node}`;
    case "DeleteNode": return `Delete ${a.node}`;
    case "RestoreShard": return `Restore ${a.collection}/${a.shard} from a backup`;
    default: return a.kind.replace(/([a-z])([A-Z])/g, "$1 $2");
  }
}

export function ClusterView({ c }: { c: ClusterDetail }) {
  const model = useMemo(() => buildMap(c.observed.nodes, c.observed.shards ?? [], c.spec.replicationFactor), [c.observed.nodes, c.observed.shards, c.spec.replicationFactor]);
  const shards = c.observed.shards ?? [];
  const moves = c.observed.transfers ?? [];
  const backups = [...(c.observed.backups ?? [])].sort((a, b) => b.at - a.at).slice(0, 4);
  const actions = c.status.actions ?? [];
  return (
    <section aria-labelledby="cluster-title" className={s.view}>
      <header className={s.head}>
        <h2 id="cluster-title" className={s.title}>{c.id}</h2>
        <PhaseTag phase={c.status.phase} />
        <p className={s.message}>{c.status.message}</p>
      </header>
      <dl className={s.facts}>
        <div><dt>Version</dt><dd>{c.spec.version}</dd></div>
        <div><dt>Nodes ready</dt><dd>{c.observed.nodes.filter((n) => n.ready).length} of {c.spec.nodes}</dd></div>
        <div><dt>Copies per shard</dt><dd>{c.spec.replicationFactor}</dd></div>
        <div><dt>Shards below that</dt><dd className={model.atRisk > 0 ? s.alert : undefined}>{model.atRisk} of {shards.length}</dd></div>
        <div><dt>Backups to</dt><dd>{c.spec.dr ? c.spec.dr.region : "same region only"}</dd></div>
      </dl>
      {shards.length === 0 ? (
        <p className={s.none}>This cluster has no collections yet, so there are no shards to place.</p>
      ) : (
        <ShardMap model={model} />
      )}
      <div className={s.columns}>
        <div>
          <h3 className={s.h3}>What the operator is doing</h3>
          {actions.length === 0 ? (
            <p className={s.quiet}>No steps this minute.</p>
          ) : (
            <ul className={s.steps}>{actions.map((a, i) => <li key={i}>{describe(a)}{a.reason ? <span className={s.reason}>{a.reason}</span> : null}</li>)}</ul>
          )}
          {moves.length > 0 && (
            <>
              <h3 className={s.h3}>Copies in flight</h3>
              <ul className={s.steps}>{moves.map((m) => <li key={`${m.collection}/${m.shard}/${m.to}`}>{m.collection}/{m.shard}: peer {m.from === 0 ? "backup" : m.from} → peer {m.to}</li>)}</ul>
            </>
          )}
        </div>
        <div>
          <h3 className={s.h3}>Latest backups</h3>
          {backups.length === 0 ? (
            <p className={s.quiet}>No backups yet.</p>
          ) : (
            <table className={s.backups}>
              <thead><tr><th>Backup</th><th>Taken</th><th>Restore drill</th><th>Copied out</th></tr></thead>
              <tbody>
                {backups.map((b) => (
                  <tr key={b.id}>
                    <td>{b.id}</td>
                    <td>{Math.max(0, c.observed.now - b.at)} min ago</td>
                    <td>{!b.ok ? "failed" : !b.verified ? "pending" : b.restores ? "restores" : "does not restore"}</td>
                    <td>{b.shipped ? "yes" : "no"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </section>
  );
}
