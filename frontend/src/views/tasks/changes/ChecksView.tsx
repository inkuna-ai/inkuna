import { useCallback, useEffect, useState } from "react";
import { getRunChecks } from "../../../bridge";
import { useInkunaEvent } from "../../../events";
import { formatDuration } from "../../../format";
import type { Check, CheckStatus } from "../../../types";
import { Button } from "../../common";
import { formatCommands } from "../../rules/model";
import { InlineAlert } from "../shared";
import { CheckLogView } from "./CheckLogView";

/** How often checks are read again while one is queued or running and no event says so. */
const pollEvery = 1000;

const statusStyles: Record<
  CheckStatus,
  { mark: string; tone: string; label: string }
> = {
  queued: { mark: "◌", tone: "text-muted", label: "queued" },
  running: { mark: "●", tone: "text-accent", label: "running" },
  passed: { mark: "✓", tone: "text-ok", label: "passed" },
  failed: { mark: "✗", tone: "text-danger", label: "failed" },
  cancelled: { mark: "⊘", tone: "text-warn", label: "cancelled" },
  skipped: { mark: "–", tone: "text-faint", label: "skipped" },
};

interface StepRun {
  stepRunId: string;
  stepId: string;
  title: string;
  checks: Check[];
}

/**
 * A run's checks under the evaluation step run that ran them, in the order
 * the step runs first appear. Two evaluation steps can run at once and
 * record their checks between each other's, so a step run's checks are
 * gathered from the whole list, in their own order.
 */
function byStepRun(checks: Check[]): StepRun[] {
  const runs = new Map<string, StepRun>();
  for (const check of checks) {
    const run = runs.get(check.stepRunId);
    if (run) run.checks.push(check);
    else
      runs.set(check.stepRunId, {
        stepRunId: check.stepRunId,
        stepId: check.stepId,
        title: check.stepTitle || check.stepId,
        checks: [check],
      });
  }
  for (const run of runs.values())
    run.checks.sort((a, b) => a.position - b.position);
  return [...runs.values()];
}

/**
 * The commands a run's evaluation steps ran, like a pull request's Checks:
 * each with its status, exit code, and duration, and its log a click away.
 * A failed or running check starts open.
 */
export function ChecksView({ runID }: { runID: string }) {
  const [checks, setChecks] = useState<Check[] | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(() => {
    getRunChecks(runID).then(
      (list) => {
        setChecks(list);
        setError("");
      },
      (reason: unknown) => setError(String(reason)),
    );
  }, [runID]);
  useEffect(load, [load]);
  const onChecks = useCallback(
    (data?: unknown) => {
      if (data === runID) load();
    },
    [runID, load],
  );
  useInkunaEvent("inkuna:checks", onChecks);
  const active = (checks ?? []).some(
    (check) => check.status === "running" || check.status === "queued",
  );
  useEffect(() => {
    if (!active) return;
    const timer = window.setInterval(load, pollEvery);
    return () => window.clearInterval(timer);
  }, [active, load]);

  const [opened, setOpened] = useState<ReadonlyMap<string, boolean>>(new Map());
  const isOpen = (check: Check) =>
    opened.get(check.id) ??
    (check.status === "failed" || check.status === "running");

  if (error && checks === null)
    return (
      <div className="grid gap-2">
        <InlineAlert>{error}</InlineAlert>
        <div>
          <Button className="px-2.5 py-1 text-xs" onClick={load}>
            Retry
          </Button>
        </div>
      </div>
    );
  if (checks === null)
    return <p className="m-0 text-xs text-muted">Loading checks…</p>;
  if (checks.length === 0)
    return (
      <p className="m-0 text-xs text-muted">
        No checks yet. An evaluation step records each command it runs here.
      </p>
    );
  return (
    <div className="grid min-w-0 gap-3">
      {error && <InlineAlert>{error}</InlineAlert>}
      {byStepRun(checks).map((run) => (
        <section
          key={run.stepRunId}
          aria-label={`Checks of ${run.title}`}
          className="min-w-0 border border-line bg-field"
        >
          <h4 className="m-0 border-b border-line bg-raised px-3 py-1.5 text-xs font-normal text-ink-soft">
            step{" "}
            <b className="font-mono font-medium text-ink-strong">
              {run.stepId}
            </b>
            {run.title !== run.stepId && (
              <span className="text-muted"> · {run.title}</span>
            )}
          </h4>
          <ol className="m-0 list-none p-0">
            {run.checks.map((check) => (
              <CheckRow
                key={check.id}
                check={check}
                open={isOpen(check)}
                onToggle={() =>
                  setOpened((all) => new Map(all).set(check.id, !isOpen(check)))
                }
              />
            ))}
          </ol>
        </section>
      ))}
    </div>
  );
}

function CheckRow({
  check,
  open,
  onToggle,
}: {
  check: Check;
  open: boolean;
  onToggle(): void;
}) {
  const style = statusStyles[check.status] ?? statusStyles.queued;
  const command = formatCommands([check.argv]);
  const hasLog = check.status !== "queued" && check.status !== "skipped";
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (check.status !== "running") return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [check.status]);
  const duration =
    check.status === "running" && check.startedAt
      ? now - Date.parse(check.startedAt)
      : check.durationMs;
  return (
    <li className="min-w-0 border-b border-line last:border-b-0">
      <button
        type="button"
        aria-expanded={hasLog ? open : undefined}
        disabled={!hasLog}
        className="flex w-full min-w-0 cursor-pointer flex-wrap items-center gap-x-3 gap-y-1 border-0 bg-transparent px-3 py-2 text-left text-xs text-ink hover:bg-raised disabled:cursor-default disabled:hover:bg-transparent"
        onClick={onToggle}
      >
        <span
          role="img"
          aria-label={style.label}
          className={`w-3 flex-none text-center ${style.tone}`}
        >
          {style.mark}
        </span>
        <span className="min-w-0 flex-1 basis-[200px] font-mono wrap-anywhere">
          {command}
        </span>
        {check.status === "failed" && check.exitCode !== undefined && (
          <span className="font-mono text-danger">exit {check.exitCode}</span>
        )}
        {check.error && (
          <span className="text-danger wrap-anywhere">{check.error}</span>
        )}
        {check.status === "skipped" && (
          <span className="text-muted">did not run</span>
        )}
        {check.status === "queued" && (
          <span className="text-muted">waiting</span>
        )}
        {(check.startedAt || check.durationMs > 0) && (
          <span className="font-mono whitespace-nowrap text-muted">
            {formatDuration(duration)}
          </span>
        )}
        {hasLog && (
          <span aria-hidden="true" className="w-3 text-ink-soft">
            {open ? "▾" : "▸"}
          </span>
        )}
      </button>
      {hasLog && open && <CheckLogView check={check} />}
    </li>
  );
}
