import { AnimatePresence, MotionConfig, motion } from "framer-motion";
import { useState } from "react";
import { LiveFleet } from "../features/fleet/LiveFleet";
import { ResultsView } from "../features/results/ResultsView";
import s from "./App.module.css";

type Tab = "fleet" | "lab";

export function App() {
  const [tab, setTab] = useState<Tab>("fleet");
  return (
    <MotionConfig reducedMotion="user">
      <div className={s.shell}>
        <header className={s.bar}>
          <h1 className={s.brand}>Vector DB fleet</h1>
          <nav className={s.tabs} aria-label="Views">
            <button aria-pressed={tab === "fleet"} onClick={() => setTab("fleet")}>Live clusters</button>
            <button aria-pressed={tab === "lab"} onClick={() => setTab("lab")}>Lab results</button>
          </nav>
        </header>
        <main className={s.content}>
          <AnimatePresence mode="wait" initial={false}>
            <motion.div key={tab} initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} transition={{ duration: 0.15 }}>
              {tab === "fleet" ? <LiveFleet /> : <ResultsView />}
            </motion.div>
          </AnimatePresence>
        </main>
      </div>
    </MotionConfig>
  );
}
