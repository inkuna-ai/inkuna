import type {
  CallTotals,
  CostGroup,
  TaskCost,
  TaskCostSummary,
  UsageRecord,
} from "../types";
import { dollars } from "./usage";

/** How a task's calls can be grouped; Go returns each grouping. */
export type CostGrouping = "step" | "agent" | "runtime" | "model";

/** The row that stands for planning where a task's steps are listed. */
export const planningKey = "(planning)";

/**
 * A total's best available cost, as Go summed it: marked ≈ when any part is
 * an estimate, with the calls nothing priced counted beside it rather than
 * valued at zero. A total of no calls has no cost to show.
 */
export function bestCostLabel(total?: CallTotals): string {
  if (!total || total.calls === 0) return "—";
  const unpriced = total.cost.unknownCalls;
  if (total.bestCents === 0 && unpriced > 0) return `${unpriced} unpriced`;
  const label = `${total.cost.estimatedCents > 0 ? "≈" : ""}${dollars(total.bestCents)}`;
  return unpriced > 0 ? `${label} + ${unpriced} unpriced` : label;
}

/** Where a total's figure came from, for the hint on it. */
export function costBreakdown(total: CallTotals): string {
  const parts: string[] = [];
  if (total.cost.reportedCents > 0)
    parts.push(`${dollars(total.cost.reportedCents)} reported`);
  if (total.cost.estimatedCents > 0)
    parts.push(`≈${dollars(total.cost.estimatedCents)} estimated`);
  if (total.cost.unknownCalls > 0)
    parts.push(
      `${total.cost.unknownCalls} ${total.cost.unknownCalls === 1 ? "call" : "calls"} without a price`,
    );
  const note = subscriptionNote(total);
  return [parts.join(", ") || "No cost", note].filter(Boolean).join(". ");
}

/**
 * Says how much of a reported cost a subscription login reported, which is
 * what those calls would cost through the API, not what was billed.
 */
export function subscriptionNote(total?: CallTotals): string {
  if (!total || total.subscriptionCents <= 0) return "";
  return `${dollars(total.subscriptionCents)} of it was reported under a subscription login: what those calls would cost through the API, not a bill`;
}

/** How long calls ran, to the second: "45s", "2m 29s", "1h 4m". */
export function callTime(durationMs: number): string {
  const seconds = Math.round(durationMs / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

/** The model a call was priced by; empty when the runtime chose and did not say. */
export function modelOf(call: UsageRecord): string {
  return call.modelUsed || call.model || "";
}

/**
 * Go's grouping of a task's calls. Steps leave planning out, so it gets a
 * row of its own and the rows add up to the task.
 */
export function costGroups(cost: TaskCost, by: CostGrouping): CostGroup[] {
  switch (by) {
    case "agent":
      return cost.byAgent;
    case "runtime":
      return cost.byRuntime;
    case "model":
      return cost.byModel;
    default:
      return cost.planning.total.calls > 0
        ? [...cost.byStep, { key: planningKey, total: cost.planning.total }]
        : cost.byStep;
  }
}

/** How a group's key reads: a model the runtime chose and did not name is "default". */
export function groupLabel(key: string, by: CostGrouping): string {
  if (by === "step" && key === planningKey) return "Planning";
  if (by === "model" && key === "") return "default";
  return key;
}

/** A call with the run it was made in; planning calls have none. */
export interface PlacedCall {
  call: UsageRecord;
  runID?: string;
}

/** Every call made for the task: planning, then each run's in step order. */
export function taskCalls(cost: TaskCost): PlacedCall[] {
  return [
    ...cost.planning.calls.map((call) => ({ call })),
    ...cost.runs.flatMap((run) =>
      run.steps.flatMap((step) =>
        step.calls.map((call) => ({ call, runID: run.executionId })),
      ),
    ),
  ];
}

/** The calls behind one row of a grouping, for its drill-down. */
export function groupCalls(
  cost: TaskCost,
  by: CostGrouping,
  key: string,
): PlacedCall[] {
  return taskCalls(cost).filter(({ call, runID }) => {
    switch (by) {
      case "agent":
        return call.agent === key;
      case "runtime":
        return call.runtime === key;
      case "model":
        return modelOf(call) === key;
      default:
        return key === planningKey
          ? runID === undefined
          : runID !== undefined && call.stepId === key;
    }
  });
}

/**
 * Where each of a bar's segments sits on a scale whose full width is
 * largest: its start and its width, both in percent. Segments are placed at
 * their exact share, so a bar of many small steps measures what they add up
 * to; the gaps between them are cut from the segments, not added to the bar.
 */
export function barSegments(
  values: number[],
  largest: number,
): Array<{ start: number; width: number }> {
  let start = 0;
  return values.map((value) => {
    const width = largest > 0 ? (100 * Math.max(value, 0)) / largest : 0;
    const segment = { start, width };
    start += width;
    return segment;
  });
}

/** Categorical slots validated together on the panel surface (index.css). */
const seriesSlots = 8;

/**
 * Gives each of a task's steps a series color in the order they first
 * started, so a step keeps its color in every run, however many runs there
 * are; steps past the validated slots share the neutral.
 */
export function stepColors(cost: TaskCost): Map<string, string> {
  const colors = new Map<string, string>();
  for (const run of cost.runs) {
    for (const step of run.steps) {
      if (colors.has(step.stepId)) continue;
      const slot = colors.size + 1;
      colors.set(
        step.stepId,
        slot <= seriesSlots
          ? `var(--color-series-${slot})`
          : "var(--color-series-other)",
      );
    }
  }
  return colors;
}

const csvColumns = [
  "task",
  "project",
  "objective",
  "kind",
  "state",
  "last_call",
  "calls",
  "input_tokens",
  "cache_write_tokens",
  "cache_read_tokens",
  "output_tokens",
  "duration_ms",
  "reported_usd",
  "estimated_usd",
  "unknown_calls",
  "best_usd",
  "subscription_usd",
];

function csvField(value: string | number): string {
  const text = String(value);
  return /[",\r\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text;
}

/**
 * Keeps a text cell from being read as a formula, as `inkunactl cost --csv`
 * does: a spreadsheet runs a cell that starts with =, +, -, or @, or with a
 * tab, carriage return, or line feed before one, and some locales take the
 * full-width ＝ for =. Objectives, names, and ids come from users and
 * repositories. An apostrophe in front makes it text.
 */
function spreadsheetText(value: string): string {
  return /^[=+\-@\t\r\n＝]/.test(value) ? `'${value}` : value;
}

const usd = (cents: number) => (cents / 100).toFixed(6);

/**
 * The listed tasks as CSV, one row each in the order shown, with the same
 * columns as `inkunactl cost --csv`. Costs are dollars without a sign, so a
 * spreadsheet reads them as numbers.
 */
export function costsCSV(
  tasks: TaskCostSummary[],
  projectName: (projectID: string) => string,
): string {
  const rows = tasks.map((task) => {
    const { usage, cost } = task.total;
    return [
      ...[
        task.taskId,
        projectName(task.projectId),
        task.objective,
        task.kind,
        task.state,
      ].map(spreadsheetText),
      task.lastCallAt,
      task.total.calls,
      usage.inputTokens ?? 0,
      usage.cacheWriteTokens ?? 0,
      usage.cacheReadTokens ?? 0,
      usage.outputTokens ?? 0,
      task.total.durationMs,
      usd(cost.reportedCents),
      usd(cost.estimatedCents),
      cost.unknownCalls,
      usd(task.total.bestCents),
      usd(task.total.subscriptionCents),
    ]
      .map(csvField)
      .join(",");
  });
  return [csvColumns.join(","), ...rows].join("\n") + "\n";
}
