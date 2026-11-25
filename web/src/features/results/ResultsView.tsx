import { useState } from "react";
import { usePoll } from "../../shared/api/usePoll";
import { StateView } from "../../shared/ui/StateView";
import { ablationMeasures, loadReport, worse, type Report, type Split } from "./report";
import s from "./ResultsView.module.css";

function Headline({ r }: { r: Split }) {
  const a = r.designs[r.main[0]!]!, o = r.designs[r.main[1]!]!;
  const tiles = [
    { label: "Shard-minutes down because of the operator's own changes", from: Math.round(a.unavailableByOps).toLocaleString("en"), to: Math.round(o.unavailableByOps).toLocaleString("en") },
    { label: "Shards still without their data at the end", from: a.shardsLostAtEnd.toFixed(0), to: o.shardsLostAtEnd.toFixed(0) },
    { label: "Requested changes completed", from: `${(a.changesCompletedShare * 100).toFixed(1)}%`, to: `${(o.changesCompletedShare * 100).toFixed(1)}%` },
    { label: "DR clusters restored after a region loss", from: `${Math.round(a.drRecoveredShare * 100)}%`, to: `${Math.round(o.drRecoveredShare * 100)}%` },
  ];
  return (
    <ul className={s.tiles}>
      {tiles.map((t) => (
        <li key={t.label} className={s.tile}>
          <span className={s.tileLabel}>{t.label}</span>
          <span className={s.pair}><span className={s.from}>{t.from}</span><span className={s.arrow} aria-label="becomes">→</span><span className={s.to}>{t.to}</span></span>
          <span className={s.who}>{r.names[r.main[0]!]} → {r.names[r.main[1]!]}</span>
        </li>
      ))}
    </ul>
  );
}

function Ablations({ r }: { r: Split }) {
  const op = r.designs[r.main[1]!]!;
  const rows = [r.main[1]!, ...r.ablations];
  return (
    <table className={s.table}>
      <caption>Each safeguard switched off on its own. Bold: worse than the full operator.</caption>
      <thead><tr><th scope="col">Design</th>{ablationMeasures.map((m) => <th key={m.label} scope="col" className={s.num}>{m.label}</th>)}</tr></thead>
      <tbody>
        {rows.map((n) => {
          const d = r.designs[n]!;
          return (
            <tr key={n}>
              <th scope="row" title={r.labels[n]}>{r.names[n]}</th>
              {ablationMeasures.map((m) => {
                const none = m.dr && d.drRecovered === 0;
                const bad = n !== r.main[1] && !none && worse(m, d, op);
                return <td key={m.label} className={`${s.num} ${bad ? s.bad : ""}`}>{none ? "–" : m.fmt(m.get(d))}</td>;
              })}
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function Gates({ r }: { r: Split }) {
  return (
    <ul className={s.gates}>
      {r.gates.operator.map((g) => (
        <li key={g.gate}>
          <span className={g.pass ? s.pass : s.fail}>{g.pass ? "Pass" : "Fail"}</span>
          {g.gate}: {Number(g.value.toPrecision(4))} ({g.kind === "max" ? "at most" : "at least"} {g.limit})
        </li>
      ))}
    </ul>
  );
}

function Body({ report }: { report: Report }) {
  const [split, setSplit] = useState<"dev" | "heldout">(report.heldout ? "heldout" : "dev");
  const r = split === "heldout" && report.heldout ? report.heldout : report.dev;
  const passed = r.gates.operator.filter((g) => g.pass).length;
  return (
    <div className={s.view}>
      <header className={s.head}>
        <h2 className={s.title}>Same fleet, same failures, two ways to run it</h2>
        {report.heldout && (
          <div role="group" aria-label="Fleet" className={s.switch}>
            {(["heldout", "dev"] as const).map((k) => (
              <button key={k} aria-pressed={split === k} onClick={() => setSplit(k)}>{k === "dev" ? "Dev fleet" : "Held-out fleet (run once)"}</button>
            ))}
          </div>
        )}
      </header>
      <p className={s.lede}>
        {r.fleet.clusters} clusters, {r.fleet.nodes} nodes and about {r.fleet.shards} shards over {r.fleet.days.toFixed(0)} simulated days, seeds {r.seeds.join(", ")}.
        Every design sees the same crashes, disk losses, API failures and region loss. {passed} of {r.gates.operator.length} gates pass.
      </p>
      <Headline r={r} />
      <Ablations r={r} />
      <h3 className={s.h3}>Gates</h3>
      <Gates r={r} />
    </div>
  );
}

export function ResultsView() {
  const report = usePoll(loadReport, 0, "report");
  return <StateView value={report} loading="Loading the lab report…">{(r) => <Body report={r} />}</StateView>;
}
