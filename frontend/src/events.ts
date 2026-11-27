import { useCallback, useEffect, useState } from "react";
import { EventsOn } from "../wailsjs/runtime/runtime";

/**
 * Subscribes to a Wails event when the desktop runtime is present. In the
 * browser preview and in tests there is no window.runtime, so the hook is a
 * no-op and views fall back to polling. The callback gets the event's
 * payload, such as the run an inkuna:usage event names.
 */
export function subscribe(
  name: string,
  callback: (data?: unknown) => void,
): () => void {
  if (typeof window === "undefined" || !window.runtime) return () => {};
  try {
    return EventsOn(name, (data?: unknown) => callback(data));
  } catch {
    return () => {};
  }
}

export function useInkunaEvent(
  name: string,
  callback: (data?: unknown) => void,
) {
  useEffect(() => subscribe(name, callback), [name, callback]);
}

/**
 * Counts the inkuna:usage events Go sends for one run: each means a usage
 * row was stored, which can happen after the run itself has finished, so a
 * view that reads usage refetches when the count changes.
 */
export function useUsageVersion(runID: string): number {
  const [seen, setSeen] = useState({ runID, count: 0 });
  const onUsage = useCallback(
    (data?: unknown) => {
      if (data === runID)
        setSeen((current) => ({
          runID,
          count: current.runID === runID ? current.count + 1 : 1,
        }));
    },
    [runID],
  );
  useInkunaEvent("inkuna:usage", onUsage);
  return seen.runID === runID ? seen.count : 0;
}
