import { useCallback, useEffect, useRef, useState } from "react";
import { getTaskCosts, saveCSV } from "../bridge";
import { useInkunaEvent } from "../events";
import { formatTime } from "../format";
import type { PeriodCost, Project } from "../types";
import {
  Alert,
  Button,
  Empty,
  Field,
  Header,
  Notice,
  StateChip,
  TableScroll,
  inputClass,
  tableClass,
  tdClass,
  thClass,
} from "./common";
import {
  bestCostLabel,
  costBreakdown,
  costsCSV,
  subscriptionNote,
} from "./cost";
import { compactTokens, totalInput } from "./usage";

const cell = `${tdClass} whitespace-nowrap`;
const number = `${tdClass} whitespace-nowrap text-right font-mono tabular-nums`;
const numberHead = `${thClass} text-right`;

/** A day as a date input holds it, in local time. */
function dayOf(date: Date): string {
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

/** The start of a local day, or of the day after it, as Go reads a bound. */
function dayStart(day: string, after = false): string {
  if (!day) return "";
  const date = new Date(`${day}T00:00:00`);
  if (Number.isNaN(date.getTime())) return "";
  if (after) date.setDate(date.getDate() + 1);
  return date.toISOString();
}

function defaultRange() {
  const today = new Date();
  const start = new Date(today);
  start.setDate(start.getDate() - 29);
  return { from: dayOf(start), to: dayOf(today) };
}

/**
 * What tasks cost over a range of days, the most expensive first as Go
 * orders them, for one project or all of them, with the range's total and
 * the listed rows as CSV. Each day is the user's local day; the range
 * includes both ends.
 */
export function CostsView({
  projects,
  onOpenRun,
}: {
  projects: Project[];
  onOpenRun(runID: string): void;
}) {
  const [projectID, setProjectID] = useState("");
  const [range, setRange] = useState(defaultRange);
  // A result and an export each keep the filters they were made for, and
  // show only while those are the filters chosen: rows read for an older
  // range must never sit under a newer range's caption or be exported as it,
  // even when reading the newer range fails.
  const filters = `${projectID}|${range.from}|${range.to}`;
  const [loaded, setLoaded] = useState<{
    filters: string;
    cost: PeriodCost;
  } | null>(null);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState<{
    filters: string;
    path: string;
    rows: number;
  } | null>(null);
  const cost = loaded?.filters === filters ? loaded.cost : null;

  // Only the latest request's answer is shown, so a slow one for an older
  // filter cannot replace a newer one.
  const latest = useRef(0);
  const load = useCallback(async () => {
    const request = ++latest.current;
    try {
      const next = await getTaskCosts(
        projectID,
        dayStart(range.from),
        dayStart(range.to, true),
      );
      if (request !== latest.current) return;
      setLoaded({ filters, cost: next });
      setError("");
    } catch (reason) {
      if (request === latest.current) setError(String(reason));
    }
  }, [projectID, range, filters]);
  useEffect(() => {
    void load();
  }, [load]);
  // Each stored call adds to a task's cost.
  useInkunaEvent("inkuna:usage", load);

  const names = new Map(projects.map((project) => [project.id, project.name]));
  const projectName = (id: string) => names.get(id) ?? id;

  async function exportCSV() {
    if (!cost) return;
    setSaved(null);
    try {
      const path = await saveCSV(
        `inkuna-costs-${range.from}-to-${range.to}.csv`,
        costsCSV(cost.tasks, projectName),
      );
      if (path) setSaved({ filters, path, rows: cost.tasks.length });
    } catch (reason) {
      setError(String(reason));
    }
  }

  const tasks = cost?.tasks ?? [];
  const subscription = subscriptionNote(cost?.total);
  const estimated = (cost?.total.cost.estimatedCents ?? 0) > 0;
  return (
    <section>
      <Header
        eyebrow="Work"
        title="Costs"
        actions={
          <Button
            onClick={() => void exportCSV()}
            disabled={tasks.length === 0}
          >
            Export CSV
          </Button>
        }
      />
      {error && <Alert>{error}</Alert>}
      <div
        role="group"
        aria-label="Filter costs"
        className="mb-5 grid grid-cols-[minmax(180px,280px)_repeat(2,minmax(140px,180px))] items-end gap-3 max-[700px]:grid-cols-1"
      >
        <Field label="Project" flush>
          <select
            aria-label="Project"
            className={inputClass}
            value={projectID}
            onChange={(event) => setProjectID(event.target.value)}
          >
            <option value="">All projects</option>
            {projects.map((project) => (
              <option key={project.id} value={project.id}>
                {project.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="From" flush>
          <input
            type="date"
            aria-label="From"
            className={inputClass}
            value={range.from}
            onChange={(event) =>
              setRange((current) => ({ ...current, from: event.target.value }))
            }
          />
        </Field>
        <Field label="To" flush>
          <input
            type="date"
            aria-label="To"
            className={inputClass}
            value={range.to}
            onChange={(event) =>
              setRange((current) => ({ ...current, to: event.target.value }))
            }
          />
        </Field>
      </div>
      {saved?.filters === filters && (
        <Notice role="status" className="mt-0 mb-5">
          Saved {saved.rows} {saved.rows === 1 ? "row" : "rows"} to{" "}
          <span className="font-mono wrap-anywhere text-ink">{saved.path}</span>
        </Notice>
      )}
      {cost && tasks.length === 0 && (
        <Empty title="No costs in this range">
          <p>
            No model call made for a task ended between these days. Widen the
            range or choose another project.
          </p>
        </Empty>
      )}
      {tasks.length > 0 && cost && (
        <TableScroll flush>
          <table className={tableClass}>
            <caption className="mb-2 text-left text-muted">
              Tasks by cost, from {range.from || "the start"} to{" "}
              {range.to || "now"}
            </caption>
            <thead>
              <tr>
                <th className={thClass}>Task</th>
                <th className={numberHead}>Cost</th>
                <th className={numberHead}>Calls</th>
                <th className={numberHead}>Input</th>
                <th className={numberHead}>Output</th>
                <th className={thClass}>Last call</th>
                <th className={thClass}>Project</th>
                <th className={thClass}>Latest run</th>
                <th className={thClass}>
                  <span className="sr-only">Open</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {tasks.map((task) => (
                <tr key={task.taskId}>
                  <td className={`${tdClass} min-w-[220px]`}>
                    <span className="block text-ink-strong">
                      {task.title || task.objective || task.taskId}
                    </span>
                    <small className="font-mono text-[10px] text-faint">
                      {task.taskId}
                    </small>
                  </td>
                  <td className={number} title={costBreakdown(task.total)}>
                    {bestCostLabel(task.total)}
                  </td>
                  <td className={number}>{task.total.calls}</td>
                  <td className={number}>
                    {compactTokens(totalInput(task.total.usage))}
                  </td>
                  <td className={number}>
                    {compactTokens(task.total.usage.outputTokens)}
                  </td>
                  <td className={cell}>{formatTime(task.lastCallAt)}</td>
                  <td className={cell}>{projectName(task.projectId)}</td>
                  <td className={cell}>
                    <StateChip state={task.state} />
                  </td>
                  <td className={cell}>
                    {task.latestRunId && (
                      <Button
                        variant="ghost"
                        className="!px-0 !py-0 text-[11px]"
                        aria-label={`Open the latest run of ${task.title || task.objective || task.taskId}`}
                        onClick={() => onOpenRun(task.latestRunId!)}
                      >
                        Open run
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
            <tfoot>
              <tr className="font-semibold">
                <td className={cell}>
                  Total · {tasks.length} {tasks.length === 1 ? "task" : "tasks"}
                </td>
                <td className={number} title={costBreakdown(cost.total)}>
                  {bestCostLabel(cost.total)}
                </td>
                <td className={number}>{cost.total.calls}</td>
                <td className={number}>
                  {compactTokens(totalInput(cost.total.usage))}
                </td>
                <td className={number}>
                  {compactTokens(cost.total.usage.outputTokens)}
                </td>
                <td className={cell} colSpan={4} />
              </tr>
            </tfoot>
          </table>
        </TableScroll>
      )}
      {(estimated || subscription) && (
        <p className="mt-3 mb-0 text-[11px] leading-relaxed text-muted">
          {estimated &&
            "≈ marks a cost that includes estimates from tokens, or Copilot premium requests, at the prices in Settings. "}
          {subscription && `Of the total, ${subscription}.`}
        </p>
      )}
    </section>
  );
}
