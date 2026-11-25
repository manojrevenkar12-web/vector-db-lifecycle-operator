export interface Summary {
  availability: number; unavailableByOps: number; unavailableShardMinutes: number; lossEvents: number; lossEventsReplicated: number;
  shardsRestored: number; shardsLostAtEnd: number; underReplicatedShardMinutes: number; changesCompletedShare: number;
  meanChangeMinutes: number; drRecovered: number; drRecoveredShare: number; drWorstRpoMinutes: number; drMeanRtoHours: number; surgeGBHours: number;
}
export interface Gate { gate: string; value: number; limit: number; kind: "min" | "max"; pass: boolean }
export interface Split {
  split: string; seeds: number[]; main: string[]; ablations: string[];
  names: Record<string, string>; labels: Record<string, string>; designs: Record<string, Summary>;
  fleet: { clusters: number; days: number; nodes: number; shards: number; changes: Record<string, number>; dr: number };
  gates: { operator: Gate[]; pass: boolean };
}
export interface Report { dev: Split; heldout?: Split }

export async function loadReport(): Promise<Report> {
  const res = await fetch("./data/report.json");
  if (!res.ok) throw new Error("The lab report is missing. Run `go run ./cmd/lab dev` to generate it.");
  return (await res.json()) as Report;
}

export interface Measure { label: string; get: (s: Summary) => number; fmt: (v: number) => string; better: "lower" | "higher"; dr?: boolean }

const n0 = (v: number) => Math.round(v).toLocaleString("en");
const n2 = (v: number) => v.toFixed(2);

export const ablationMeasures: Measure[] = [
  { label: "Shards lost at the end", get: (s) => s.shardsLostAtEnd, fmt: n2, better: "lower" },
  { label: "Shards losing every copy", get: (s) => s.lossEvents, fmt: n2, better: "lower" },
  { label: "Downtime from its own changes (shard-min)", get: (s) => s.unavailableByOps, fmt: n0, better: "lower" },
  { label: "Under-replicated (shard-h)", get: (s) => s.underReplicatedShardMinutes / 60, fmt: n0, better: "lower" },
  { label: "DR restored", get: (s) => s.drRecoveredShare, fmt: (v) => `${Math.round(v * 100)}%`, better: "higher" },
  { label: "Worst RPO (min)", get: (s) => s.drWorstRpoMinutes, fmt: n0, better: "lower", dr: true },
  { label: "Mean RTO (h)", get: (s) => s.drMeanRtoHours, fmt: n2, better: "lower", dr: true },
];

/** True when x is worse than the reference on this measure. */
export function worse(m: Measure, x: Summary, ref: Summary): boolean {
  const a = m.get(x), b = m.get(ref);
  return m.better === "lower" ? a > b * 1.0001 + 1e-9 : a < b * 0.9999 - 1e-9;
}
