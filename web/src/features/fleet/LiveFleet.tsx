import { useEffect, useState } from "react";
import { api } from "../../shared/api/client";
import { usePoll } from "../../shared/api/usePoll";
import { StateView } from "../../shared/ui/StateView";
import { ChangePanel } from "../changes/ChangePanel";
import { ClusterView } from "../cluster/ClusterView";
import { FleetList } from "./FleetList";
import s from "./LiveFleet.module.css";

function Selected({ id }: { id: string }) {
  const detail = usePoll(() => api.cluster(id), 1500, id);
  return (
    <div className={s.main}>
      <StateView value={detail} loading={`Reading ${id}…`}>{(c) => <ClusterView c={c} />}</StateView>
      {detail.state === "ready" && <ChangePanel key={`${id}-${detail.data.generation}`} cluster={id} spec={detail.data.spec} />}
    </div>
  );
}

export function LiveFleet() {
  const fleet = usePoll(api.clusters, 2500, "fleet");
  const [selected, setSelected] = useState<string | null>(null);
  useEffect(() => {
    if (!selected && fleet.state === "ready" && fleet.data[0]) setSelected(fleet.data[0].id);
  }, [fleet, selected]);
  return (
    <StateView value={fleet} loading="Connecting to the change API…" isEmpty={(d) => d.length === 0} empty="No clusters yet. Create one with a VectorCluster resource or start the simulated fleet with `go run ./cmd/api`.">
      {(clusters) => (
        <div className={s.layout}>
          <FleetList clusters={clusters} selected={selected} onSelect={setSelected} />
          {selected && <Selected id={selected} />}
        </div>
      )}
    </StateView>
  );
}
