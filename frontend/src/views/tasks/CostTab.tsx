import { Fragment, useEffect, useState } from "react";
import { getTaskCost } from "../../bridge";
import { useUsageVersion } from "../../events";
import type { CallTotals, TaskCost, WorkflowRun } from "../../types";
import {
  Disclosure,
  Metric,
  Metrics,
  StateChip,
  TabPanel,
  TableScroll,
  Tabs,
  tableClass,
  tdClass,
  thClass,
} from "../common";
import {
  bestCostLabel,
  callTime,
  costBreakdown,
  costGroups,
  groupCalls,
  groupLabel,
  modelOf,
  planningKey,
  stepColors,
  subscriptionNote,
  type CostGrouping,
  type PlacedCall,
} from "../cost";
import {
  cacheReadShare,
  callLabel,
  compactTokens,
  costLabel,
  costSource,
  totalInput,
  usageLine,
} from "../usage";
import { CostBars } from "./CostBars";
import { InlineAlert, SubHeading, TabBody } from "./shared";

const groupings: Array<{ id: CostGrouping; label: string }> = [
  { id: "step", label: "Step" },
  { id: "agent", label: "Agent" },
  { id: "runtime", label: "Runtime" },
  { id: "model", label: "Model" },
];

const cell = `${tdClass} whitespace-nowrap`;
const number = `${tdClass} whitespace-nowrap text-right font-mono tabular-nums`;
const numberHead = `${thClass} text-right`;

/**
 * What the run's task has cost across its planning and every run: totals,
 * a bar per run split by step, and Go's breakdown by step, agent, runtime,
 * or model, each row opening onto its calls.
 */
export function CostTab({ run }: { run: WorkflowRun }) {
  const [loaded, setLoaded] = useState<{
    taskID: string;
    cost: TaskCost;
  } | null>(null);
  const [error, setError] = useState("");
  const [by, setBy] = useState<CostGrouping>("step");
  // Read again as the run moves on and as its calls are stored, like the
  // run's usage panel, and as the task's totals change: another run of the
  // task, such as a rerun, stores calls that this run's events never show.
  const last = run.events?.at(-1);
  const latest = last ? `${last.runId}:${last.sequence}:${last.timestamp}` : "";
  const stored = useUsageVersion(run.id);
  const taskTotals = run.taskCost
    ? `${run.taskCost.calls}:${run.taskCost.durationMs}:${run.taskCost.bestCents}`
    : "";
  useEffect(() => {
    let active = true;
    getTaskCost(run.taskId)
      .then((cost) => {
        if (active) {
          setLoaded({ taskID: run.taskId, cost });
          setError("");
        }
      })
      .catch((reason: unknown) => {
        if (active) setError(String(reason));
      });
    return () => {
      active = false;
    };
  }, [run.taskId, run.state, latest, stored, taskTotals]);
  const cost = loaded?.taskID === run.taskId ? loaded.cost : null;
  if (error) {
    return (
      <TabBody>
        <InlineAlert>{error}</InlineAlert>
      </TabBody>
    );
  }
  if (!cost) {
    return (
      <TabBody>
        <p className="m-0 text-xs text-muted">Reading the task&apos;s cost…</p>
      </TabBody>
    );
  }
  if (cost.total.calls === 0) {
    return (
      <TabBody>
        <p className="m-0 text-xs text-muted">
          No model call has been recorded for this task yet.
        </p>
      </TabBody>
    );
  }
  const share = cacheReadShare(cost.total.usage);
  const colors = stepColors(cost);
  const estimated = cost.total.cost.estimatedCents > 0;
  const subscription = subscriptionNote(cost.total);
  return (
    <TabBody>
      <section aria-label="Task cost" className="grid min-w-0 gap-3">
        <div className="min-w-0 [&_strong]:text-lg">
          <Metrics flush>
            <Metric value={bestCostLabel(cost.total)} label="Task cost" />
            <Metric
              value={compactTokens(totalInput(cost.total.usage))}
              label="Input tokens"
            />
            <Metric
              value={share === null ? "—" : `${share}%`}
              label="Input read from cache"
            />
            <Metric
              value={compactTokens(cost.total.usage.outputTokens)}
              label="Output tokens"
            />
            <Metric value={String(cost.total.calls)} label="Model calls" />
            <Metric
              value={callTime(cost.total.durationMs)}
              label="Time in calls"
            />
          </Metrics>
        </div>
        <p className="m-0 text-[11px] wrap-anywhere text-muted">
          Across {cost.runs.length} {cost.runs.length === 1 ? "run" : "runs"} of
          this task and its planning: {costBreakdown(cost.total)}.
        </p>
        <Planning cost={cost} />
      </section>
      <section aria-label="Cost per run" className="grid min-w-0 gap-3">
        <SubHeading>Cost per run</SubHeading>
        <CostBars cost={cost} colors={colors} />
      </section>
      <section aria-label="Cost breakdown" className="grid min-w-0 gap-3">
        <SubHeading>Breakdown</SubHeading>
        <Tabs
          tabs={groupings}
          active={by}
          onChange={setBy}
          label="Group the task's calls by"
          idPrefix="cost-by"
        />
        <TabPanel idPrefix="cost-by" tab={by}>
          {/* Keyed by grouping so open rows close when it changes. */}
          <Breakdown key={by} cost={cost} by={by} colors={colors} />
        </TabPanel>
      </section>
      {(estimated || subscription) && (
        <p className="m-0 text-[11px] leading-relaxed text-muted">
          {estimated &&
            "≈ marks a cost that includes estimates from tokens, or Copilot premium requests, at the prices in Settings. "}
          {subscription && `${subscription}.`}
        </p>
      )}
    </TabBody>
  );
}

/** Planning's own line, apart from the runs, with its calls behind it. */
function Planning({ cost }: { cost: TaskCost }) {
  const planning = cost.planning;
  if (planning.total.calls === 0) {
    return (
      <p className="m-0 text-[11px] text-muted">
        Planning made no model call: the orchestrator reused a staffing decision
        or was off.
      </p>
    );
  }
  return (
    <div className="min-w-0 bg-field px-3 pt-2.5">
      <p className="m-0 text-xs wrap-anywhere">
        <b className="font-semibold text-ink-strong">Planning</b>{" "}
        <span className="font-mono text-ink">
          {bestCostLabel(planning.total)}
        </span>{" "}
        <span className="text-muted">
          · {planning.total.calls}{" "}
          {planning.total.calls === 1
            ? "orchestrator call"
            : "orchestrator calls"}{" "}
          · {usageLine(planning.total.usage)}
        </span>
      </p>
      <Disclosure label="Planning calls">
        <CallsTable
          calls={planning.calls.map((call) => ({ call }))}
          showRun={false}
        />
      </Disclosure>
    </div>
  );
}

function Breakdown({
  cost,
  by,
  colors,
}: {
  cost: TaskCost;
  by: CostGrouping;
  colors: Map<string, string>;
}) {
  const [open, setOpen] = useState<Set<string>>(new Set());
  const groups = costGroups(cost, by);
  const runs = new Map(
    cost.runs.map((run, index) => [run.executionId, index + 1]),
  );
  const heading = groupings.find((grouping) => grouping.id === by)?.label;
  return (
    <TableScroll flush>
      <table className={tableClass}>
        <caption className="sr-only">Task cost by {heading}</caption>
        <thead>
          <tr>
            <th className={thClass}>{heading}</th>
            <th className={numberHead}>Calls</th>
            <th className={numberHead}>Input</th>
            <th className={numberHead}>Cached</th>
            <th className={numberHead}>Output</th>
            <th className={numberHead}>Time</th>
            <th className={numberHead}>Cost</th>
          </tr>
        </thead>
        <tbody>
          {groups.map((group) => {
            const expanded = open.has(group.key);
            const color = by === "step" ? colors.get(group.key) : undefined;
            const label = groupLabel(group.key, by);
            return (
              <Fragment key={group.key}>
                <tr>
                  <td className={cell}>
                    <button
                      type="button"
                      aria-expanded={expanded}
                      className="flex cursor-pointer items-center gap-2 border-0 bg-transparent p-0 text-left text-[11px] text-ink hover:text-ink-strong"
                      onClick={() =>
                        setOpen((current) => {
                          const next = new Set(current);
                          if (next.has(group.key)) next.delete(group.key);
                          else next.add(group.key);
                          return next;
                        })
                      }
                    >
                      <span aria-hidden="true" className="w-2 text-muted">
                        {expanded ? "▾" : "▸"}
                      </span>
                      {color && (
                        <span
                          aria-hidden="true"
                          className="inline-block size-2.5 flex-none rounded-[2px]"
                          style={{ background: color }}
                        />
                      )}
                      <span
                        className={
                          group.key === planningKey && by === "step"
                            ? "text-muted"
                            : ""
                        }
                      >
                        {label}
                      </span>
                    </button>
                  </td>
                  <Totals total={group.total} />
                </tr>
                {expanded && (
                  <tr>
                    <td
                      className="border-b border-line bg-field p-2.5"
                      colSpan={7}
                    >
                      <CallsTable
                        calls={groupCalls(cost, by, group.key)}
                        runs={runs}
                        showRun
                      />
                    </td>
                  </tr>
                )}
              </Fragment>
            );
          })}
        </tbody>
        <tfoot>
          <tr className="font-semibold">
            <td className={cell}>Task total</td>
            <Totals total={cost.total} />
          </tr>
        </tfoot>
      </table>
    </TableScroll>
  );
}

function Totals({ total }: { total: CallTotals }) {
  const share = cacheReadShare(total.usage);
  return (
    <>
      <td className={number}>{total.calls}</td>
      <td className={number}>{compactTokens(totalInput(total.usage))}</td>
      <td className={number}>{share === null ? "—" : `${share}%`}</td>
      <td className={number}>{compactTokens(total.usage.outputTokens)}</td>
      <td className={number}>{callTime(total.durationMs)}</td>
      <td className={number} title={costBreakdown(total)}>
        {bestCostLabel(total)}
      </td>
    </>
  );
}

/** The individual turns and peer answers behind a row. */
function CallsTable({
  calls,
  runs,
  showRun,
}: {
  calls: PlacedCall[];
  runs?: Map<string, number>;
  showRun: boolean;
}) {
  // Zero width with a full minimum keeps a wide list of calls from widening
  // the breakdown it opens in: it scrolls inside its own cell instead.
  return (
    <div className="w-0 min-w-full overflow-x-auto pb-2">
      <table className={tableClass} aria-label="Calls">
        <thead>
          <tr>
            <th className={thClass}>Call</th>
            {showRun && <th className={thClass}>Run</th>}
            <th className={thClass}>Step</th>
            <th className={thClass}>Model</th>
            <th className={thClass}>Outcome</th>
            <th className={thClass}>Tokens</th>
            <th className={numberHead}>Cost</th>
          </tr>
        </thead>
        <tbody>
          {calls.map(({ call, runID }) => (
            <tr key={call.id}>
              <td className={cell}>{callLabel(call)}</td>
              {showRun && (
                <td className={cell}>
                  {runID ? `Run ${runs?.get(runID) ?? "?"}` : "Planning"}
                </td>
              )}
              <td className={cell}>{call.stepId}</td>
              <td className={cell}>
                {[call.runtime, modelOf(call) || "default"].join(" · ")}
              </td>
              <td className={cell}>
                <StateChip state={call.outcome} />
              </td>
              <td className={`${cell} text-muted`}>{usageLine(call.usage)}</td>
              <td className={number} title={costSource(call)}>
                {costLabel(call.cost)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
