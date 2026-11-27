import { useCallback, useEffect, useRef, useState } from "react";
import { listRuntimes } from "../../bridge";
import { useInkunaEvent } from "../../events";
import { builtInRuntimes, type RuntimeInfo } from "../../types";

/**
 * The declared runtimes, for pickers. It starts from the built-ins so a picker
 * is never empty, and follows configuration saves made anywhere in the app.
 */
export function useRuntimes(): RuntimeInfo[] {
  const [runtimes, setRuntimes] = useState<RuntimeInfo[]>(builtInRuntimes);
  // Configuration events can start a load while another is in flight; only
  // the newest request may set the list.
  const latest = useRef(0);
  const load = useCallback(() => {
    let active = true;
    const request = ++latest.current;
    listRuntimes()
      .then(
        (value) =>
          active &&
          request === latest.current &&
          value.length > 0 &&
          setRuntimes(value),
      )
      .catch(() => {});
    return () => {
      active = false;
    };
  }, []);
  useEffect(load, [load]);
  useInkunaEvent("inkuna:config", load);
  return runtimes;
}
