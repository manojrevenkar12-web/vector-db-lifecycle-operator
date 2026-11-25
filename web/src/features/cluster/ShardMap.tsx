import { motion } from "framer-motion";
import type { ReplicaState } from "../../shared/api/types";
import type { MapModel } from "./shardMap";
import s from "./ShardMap.module.css";

const letter: Record<ReplicaState, string> = { Active: "A", Partial: "P", Initializing: "I", Dead: "D" };
const fill: Record<ReplicaState, string> = {
  Active: "var(--state-active)", Partial: "var(--state-partial)", Initializing: "var(--state-initializing)", Dead: "var(--state-dead)",
};

export function ShardMap({ model }: { model: MapModel }) {
  return (
    <div className={s.wrap}>
      <table className={s.map}>
        <caption className={s.caption}>Where every shard's copies live. Each column is a shard, each row a node.</caption>
        <thead>
          <tr>
            <th scope="col" className={s.corner}>Node</th>
            {model.groups.map((g) => (
              <th key={g.collection} scope="colgroup" colSpan={g.columns.length} className={s.group}>{g.collection}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {model.nodes.map((n) => (
            <tr key={n.name} className={n.ready ? undefined : s.down}>
              <th scope="row" className={s.node}>
                <span className={s.nodeName}>{n.name}</span>
                <span className={s.nodeMeta}>{n.ready ? n.version : `down ${n.downFor} min`}</span>
              </th>
              {model.groups.flatMap((g) =>
                g.columns.map((c) => {
                  const st = model.cell(n.peer, c);
                  return (
                    <td key={c.key} className={s.td}>
                      {st ? (
                        <motion.span
                          className={s.cell}
                          initial={false}
                          animate={{ backgroundColor: fill[st], opacity: n.ready ? 1 : 0.45 }}
                          transition={{ duration: 0.35 }}
                          title={`${c.key} on ${n.name}: ${st}`}
                        >
                          {letter[st]}
                        </motion.span>
                      ) : (
                        <span className={s.empty} aria-label={`${c.key}: no copy on ${n.name}`} />
                      )}
                    </td>
                  );
                }),
              )}
            </tr>
          ))}
        </tbody>
      </table>
      <ul className={s.legend} aria-label="Replica states">
        {(Object.keys(letter) as ReplicaState[]).map((k) => (
          <li key={k}><span className={s.swatch} style={{ background: fill[k] }}>{letter[k]}</span>{k}</li>
        ))}
        <li><span className={`${s.swatch} ${s.swatchEmpty}`} />No copy</li>
      </ul>
    </div>
  );
}
