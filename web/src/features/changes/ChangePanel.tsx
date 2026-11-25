import { AnimatePresence, motion } from "framer-motion";
import { useState } from "react";
import { api, ApiError } from "../../shared/api/client";
import type { Actor, Patch, Preview, Spec } from "../../shared/api/types";
import s from "./ChangePanel.module.css";

const versions = ["v1.15.5", "v1.16.3", "v1.17.1"];
const actors: Record<string, Actor> = { agent: { kind: "agent", id: "ops-agent" }, human: { kind: "human", id: "maria.k" } };

function key(): string {
  return `ui-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

function diff(from: Spec, f: { version: string; nodes: number; rf: number; dr: boolean }): Patch {
  const p: Patch = {};
  if (f.version !== from.version) p.version = f.version;
  if (f.nodes !== from.nodes) p.nodes = f.nodes;
  if (f.rf !== from.replicationFactor) p.replicationFactor = f.rf;
  if (f.dr !== Boolean(from.dr)) p.dr = f.dr;
  return p;
}

type Outcome = { tone: "ok" | "refused"; text: string } | null;

export function ChangePanel({ cluster, spec }: { cluster: string; spec: Spec }) {
  const [form, setForm] = useState({ version: spec.version, nodes: spec.nodes, rf: spec.replicationFactor, dr: Boolean(spec.dr) });
  const [who, setWho] = useState<"agent" | "human">("agent");
  const [preview, setPreview] = useState<Preview | null>(null);
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<Outcome>(null);
  const patch = diff(spec, form);
  const changed = Object.keys(patch).length > 0;

  async function run(step: () => Promise<void>) {
    setBusy(true);
    setOutcome(null);
    try {
      await step();
    } catch (e) {
      const status = e instanceof ApiError ? e.status : 0;
      const text = e instanceof Error ? e.message : String(e);
      setOutcome({ tone: "refused", text: status ? `Refused (${status}): ${text}` : text });
    } finally {
      setBusy(false);
    }
  }

  return (
    <aside className={s.panel} aria-labelledby="change-title">
      <h2 id="change-title" className={s.title}>Change this cluster</h2>
      <p className={s.lede}>Every change is previewed first. A risky change needs a person's approval before anyone, person or agent, can apply it.</p>
      <div className={s.fields}>
        <label>Version
          <select value={form.version} onChange={(e) => { setForm({ ...form, version: e.target.value }); setPreview(null); }}>
            {[...new Set([spec.version, ...versions, "v1.14.0"])].map((v) => <option key={v}>{v}</option>)}
          </select>
        </label>
        <label>Nodes
          <input type="number" min={1} max={12} value={form.nodes} onChange={(e) => { setForm({ ...form, nodes: Number(e.target.value) }); setPreview(null); }} />
        </label>
        <label>Copies per shard
          <input type="number" min={1} max={4} value={form.rf} onChange={(e) => { setForm({ ...form, rf: Number(e.target.value) }); setPreview(null); }} />
        </label>
        <label className={s.check}>
          <input type="checkbox" checked={form.dr} onChange={(e) => { setForm({ ...form, dr: e.target.checked }); setPreview(null); }} />
          Copy backups to a DR region
        </label>
      </div>
      <fieldset className={s.who}>
        <legend>Acting as</legend>
        {(["agent", "human"] as const).map((w) => (
          <label key={w}><input type="radio" name="who" checked={who === w} onChange={() => setWho(w)} />{w === "agent" ? "Agent (ops-agent)" : "Person (maria.k)"}</label>
        ))}
      </fieldset>
      <button className={s.primary} disabled={!changed || busy} onClick={() => run(async () => setPreview(await api.preview(cluster, patch)))}>
        Preview change
      </button>
      <AnimatePresence initial={false}>
        {preview && (
          <motion.div key={preview.id} className={s.preview} initial={{ opacity: 0, y: -6 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }} transition={{ duration: 0.2 }}>
            <h3 className={s.h3}>What this change will do</h3>
            <ul className={s.effects}>
              <li>{preview.effects.restarts} node restart{preview.effects.restarts === 1 ? "" : "s"}</li>
              <li>{preview.effects.nodesAdded} added, {preview.effects.nodesRemoved} removed</li>
              <li>{preview.effects.replicasToMove} copies to move first</li>
              <li>{preview.effects.temporaryCopies} temporary copies for single-copy shards</li>
              {preview.effects.risks.map((r) => <li key={r} className={s.risk}>{r}</li>)}
            </ul>
            {preview.effects.needsPerson ? (
              <p className={s.gate}>Needs a person: {preview.effects.needsPersonReason.join("; ")}.{preview.approvedBy ? ` Approved by ${preview.approvedBy}.` : ""}</p>
            ) : (
              <p className={s.quiet}>An agent may apply this on its own.</p>
            )}
            <div className={s.row}>
              {preview.effects.needsPerson && !preview.approvedBy && (
                <button disabled={busy} onClick={() => run(async () => { const p = await api.approve(preview.id, actors[who]!); setPreview(p); setOutcome({ tone: "ok", text: `Approved by ${p.approvedBy}.` }); })}>
                  Approve
                </button>
              )}
              <button className={s.primary} disabled={busy || preview.applied} onClick={() => run(async () => { const r = await api.apply(preview.id, actors[who]!, key()); setPreview({ ...preview, applied: true }); setOutcome({ tone: "ok", text: `Applied. The cluster is now at generation ${r.generation}; the operator takes it from here.` }); })}>
                Apply
              </button>
            </div>
          </motion.div>
        )}
      </AnimatePresence>
      <div aria-live="polite">
        {outcome && <p className={outcome.tone === "ok" ? s.ok : s.refused}>{outcome.text}</p>}
      </div>
    </aside>
  );
}
