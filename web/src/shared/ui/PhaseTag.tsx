import type { Phase } from "../api/types";
import s from "./PhaseTag.module.css";

const tone: Record<Phase, "good" | "busy" | "bad"> = {
  Healthy: "good", Provisioning: "busy", Repairing: "busy", Scaling: "busy", Upgrading: "busy", Rebalancing: "busy", Waiting: "busy", Blocked: "bad",
};

export function PhaseTag({ phase }: { phase: Phase }) {
  return (
    <span className={`${s.tag} ${s[tone[phase]]}`}>
      <span className={s.dot} aria-hidden />
      {phase}
    </span>
  );
}
