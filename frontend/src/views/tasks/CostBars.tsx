import { Fragment, useState } from "react";
import type { CallTotals, TaskCost } from "../../types";
import { StateChip } from "../common";
import { barSegments, bestCostLabel, callTime } from "../cost";
import { usageLine, totalInput } from "../usage";

/**
 * A bar per run, split into its steps, on one scale so runs compare. Bars
 * measure the best available cost; when nothing in the task has a price
 * they measure tokens instead, and say so. Hovering or focusing a segment
 * reads it out below; the breakdown table holds every value too.
 */
export function CostBars({
  cost,
  colors,
}: {
  cost: TaskCost;
  colors: Map<string, string>;
}) {
  const [active, setActive] = useState<{ run: string; step: string } | null>(
    null,
  );
  const byCost = cost.total.bestCents > 0;
  const measure = (total: CallTotals) =>
    byCost
      ? total.bestCents
      : totalInput(total.usage) + (total.usage.outputTokens ?? 0);
  const largest = Math.max(...cost.runs.map((run) => measure(run.total)), 0);
  if (cost.runs.length === 0) {
    return (
      <p className="m-0 text-xs text-muted">
        The task has not run yet; only its planning has cost anything.
      </p>
    );
  }
  const steps = [...colors.keys()];
  const readout = active
    ? cost.runs
        .find((run) => run.executionId === active.run)
        ?.steps.find((step) => step.stepId === active.step)
    : undefined;
  return (
    <div className="grid min-w-0 gap-3">
      {steps.length > 1 && (
        <ul
          aria-label="Steps"
          className="m-0 flex list-none flex-wrap gap-x-4 gap-y-1 p-0 text-[11px] text-ink-soft"
        >
          {steps.map((step) => (
            <li key={step} className="flex min-w-0 items-center gap-1.5">
              <span
                aria-hidden="true"
                className="inline-block size-2.5 flex-none rounded-[2px]"
                style={{ background: colors.get(step) }}
              />
              <span className="min-w-0 wrap-anywhere">{step}</span>
            </li>
          ))}
        </ul>
      )}
      {/* One grid for every run, so the figure column takes the widest
          figure and every bar gets the same width to draw on. */}
      <div className="grid min-w-0 grid-cols-[minmax(96px,150px)_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2.5 max-[600px]:grid-cols-[minmax(0,1fr)_auto] max-[600px]:gap-y-1">
        {cost.runs.map((run, index) => {
          const placed = barSegments(
            run.steps.map((step) => measure(step.total)),
            largest,
          );
          return (
            <Fragment key={run.executionId}>
              <div
                className="flex min-w-0 items-center gap-2 text-[11px] text-ink-soft max-[600px]:col-span-2 max-[600px]:mt-1.5"
                title={run.executionId}
              >
                <span className="flex-none font-mono">Run {index + 1}</span>
                <StateChip state={run.state} />
              </div>
              <div
                className="relative h-3 min-w-0"
                role="group"
                aria-label={`Run ${index + 1} by step`}
              >
                {run.steps.map((step, position) => {
                  const segment = placed[position];
                  if (segment.width <= 0) return null;
                  const current =
                    active?.run === run.executionId &&
                    active.step === step.stepId;
                  const last = position === run.steps.length - 1;
                  return (
                    <span
                      key={step.stepId}
                      tabIndex={0}
                      role="img"
                      aria-label={`${step.stepId}: ${bestCostLabel(step.total)}`}
                      className={`absolute top-0 h-full cursor-default outline-offset-2 ${last ? "rounded-r" : ""} ${
                        active && !current ? "opacity-50" : ""
                      }`}
                      style={{
                        left: `${segment.start}%`,
                        // The gap to the next segment comes out of this one,
                        // so the bar still ends at the run's own value.
                        width: last
                          ? `${segment.width}%`
                          : `max(0px, calc(${segment.width}% - 2px))`,
                        background: colors.get(step.stepId),
                      }}
                      onMouseEnter={() =>
                        setActive({ run: run.executionId, step: step.stepId })
                      }
                      onMouseLeave={() => setActive(null)}
                      onFocus={() =>
                        setActive({ run: run.executionId, step: step.stepId })
                      }
                      onBlur={() => setActive(null)}
                    />
                  );
                })}
              </div>
              <span className="font-mono text-[11px] text-ink tabular-nums">
                {bestCostLabel(run.total)}
              </span>
            </Fragment>
          );
        })}
      </div>
      <p
        aria-live="polite"
        className="m-0 min-h-[1.25rem] text-[11px] wrap-anywhere text-muted"
      >
        {readout ? (
          <>
            <b className="font-mono font-medium text-ink-strong">
              {bestCostLabel(readout.total)}
            </b>{" "}
            {readout.stepId} · {readout.total.calls}{" "}
            {readout.total.calls === 1 ? "call" : "calls"} ·{" "}
            {usageLine(readout.total.usage)} ·{" "}
            {callTime(readout.total.durationMs)}
          </>
        ) : byCost ? (
          "Each bar is a run's best available cost, split by step. Hover or focus a step for its figures."
        ) : (
          "Nothing in this task has a price, so each bar is a run's tokens, split by step."
        )}
      </p>
    </div>
  );
}
