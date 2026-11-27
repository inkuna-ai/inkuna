import type { Runtime, RuntimeInfo } from "../../types";

/**
 * Options for a runtime select. A value that is no longer declared stays
 * listed, marked, so an old choice is visible instead of silently replaced.
 */
export function RuntimeOptions({
  runtimes,
  value,
  writeOnly = false,
}: {
  runtimes: RuntimeInfo[];
  value?: Runtime | "";
  /** Leave out runtimes that can only run read-only steps. */
  writeOnly?: boolean;
}) {
  const listed = runtimes.filter(
    (runtime) => !writeOnly || runtime.canWrite || runtime.name === value,
  );
  return (
    <>
      {listed.map((runtime) => (
        <option key={runtime.name} value={runtime.name}>
          {runtime.label}
          {runtime.canWrite ? "" : " (read-only)"}
        </option>
      ))}
      {value && !runtimes.some((runtime) => runtime.name === value) && (
        <option value={value}>{value} (not declared)</option>
      )}
    </>
  );
}
