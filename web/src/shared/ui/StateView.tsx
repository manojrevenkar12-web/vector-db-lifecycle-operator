import type { ReactNode } from "react";
import type { Loadable } from "../api/usePoll";
import s from "./StateView.module.css";

interface Props<T> {
  value: Loadable<T>;
  loading: string;
  isEmpty?: (data: T) => boolean;
  empty?: ReactNode;
  children: (data: T) => ReactNode;
}

/** Every data view goes through here, so each one has its loading, error and empty state. */
export function StateView<T>({ value, loading, isEmpty, empty, children }: Props<T>) {
  if (value.state === "loading") {
    return (
      <div className={s.box} role="status" aria-live="polite">
        <span className={s.pulse} aria-hidden /> {loading}
      </div>
    );
  }
  if (value.state === "error") {
    return (
      <div className={`${s.box} ${s.error}`} role="alert">
        {value.message}
      </div>
    );
  }
  if (isEmpty?.(value.data)) {
    return <div className={s.box}>{empty}</div>;
  }
  return <>{children(value.data)}</>;
}
