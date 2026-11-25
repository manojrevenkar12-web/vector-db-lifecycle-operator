import type { ClusterSummary } from "../../shared/api/types";
import { PhaseTag } from "../../shared/ui/PhaseTag";
import s from "./FleetList.module.css";

interface Props { clusters: ClusterSummary[]; selected: string | null; onSelect: (id: string) => void }

export function FleetList({ clusters, selected, onSelect }: Props) {
  return (
    <nav aria-label="Clusters" className={s.list}>
      {clusters.map((c) => (
        <button key={c.id} className={s.item} aria-current={c.id === selected ? "true" : undefined} onClick={() => onSelect(c.id)}>
          <span className={s.id}>{c.id}</span>
          <PhaseTag phase={c.phase} />
          <span className={s.meta}>
            {c.spec.version}, {c.spec.nodes} nodes, {c.spec.replicationFactor}× copies{c.spec.dr ? `, DR ${c.spec.dr.region}` : ""}
          </span>
        </button>
      ))}
    </nav>
  );
}
