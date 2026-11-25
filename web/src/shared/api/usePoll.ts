import { useEffect, useRef, useState } from "react";

export type Loadable<T> = { state: "loading" } | { state: "error"; message: string } | { state: "ready"; data: T };

/** Calls load now and every `every` ms; keeps the last good data while a later call fails. */
export function usePoll<T>(load: () => Promise<T>, every: number, key: string): Loadable<T> {
  const [value, setValue] = useState<Loadable<T>>({ state: "loading" });
  const loadRef = useRef(load);
  loadRef.current = load;
  useEffect(() => {
    let alive = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    setValue({ state: "loading" });
    const tick = async () => {
      try {
        const data = await loadRef.current();
        if (alive) setValue({ state: "ready", data });
      } catch (e) {
        if (alive) setValue((v) => (v.state === "ready" ? v : { state: "error", message: e instanceof Error ? e.message : String(e) }));
      }
      if (alive && every > 0) timer = setTimeout(tick, every);
    };
    void tick();
    return () => {
      alive = false;
      if (timer) clearTimeout(timer);
    };
  }, [every, key]);
  return value;
}
